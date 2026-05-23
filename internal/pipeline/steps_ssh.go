/* internal/pipeline/steps_ssh.go - PLAN.md 18.20.
 *
 * SSH agent step library:
 *
 *   sshagent(credentials: ['cred-id-1', 'cred-id-2']) {
 *       sh 'git clone git@github.com:wolfssl/wolfssl.git'
 *   }
 *
 * For each credentials id the step unseals an ssh-private-key
 * record from the runtime's credstore, parses its OPENSSH
 * PRIVATE KEY PEM, registers the resulting identity on a
 * wolfssh.Agent, starts a unix-domain-socket listener that
 * serves the agent protocol via Agent.HandleFramedMessage, and
 * exposes the socket path to the wrapped closure body as
 * $SSH_AUTH_SOCK. Tear-down (close listener, remove socket
 * file) runs via defer so a panic or build failure in the
 * closure still cleans up.
 *
 * The agent uses wolfCrypt's Wc_ed25519_sign_msg as the
 * signing primitive (matching how 18.19's agent_test.go wires
 * its closure); other algorithms surface an actionable error
 * at credential-load time. Per PLAN.md 18.20 the only
 * algorithm in 18.20 is ssh-ed25519; ECDSA / RSA support is
 * follow-on work that extends loadAgentIdentity.
 *
 * NO openssh ssh-agent binary - the listener is a goroutine in
 * the same process as the build runner, and SSH_AUTH_SOCK
 * points at a file inside the build workspace (or a temp dir
 * when no workspace is set), so the spawned shells discover
 * the agent through the standard env var that any ssh / git /
 * scp / rsync client already consults.
 */
package pipeline

import (
    "context"
    "encoding/binary"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net"
    "os"
    "path/filepath"
    "sync"

    gowolf "github.com/wolfssl/go-wolfssl"
    "github.com/wolfssl/go-wolfssl/wolfssh"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

/* registerSshSteps installs the 18.20 sshagent step on the
 * runtime. Called from registerCoreSteps so the native surface
 * stays discoverable in one place. */
func registerSshSteps(rt *scriptRuntime) {
    rt.globals.define("sshagent",
        &sNative{name: "sshagent", fn: nativeSshagent})
}

/* nativeSshagent is the body of the sshagent step. Expected
 * shape:
 *
 *   sshagent(credentials: ['id1', 'id2']) { <closure> }
 *
 * The named-arg pre-pass in exec_script.go's collectCallArgs
 * collapses the bindings list into args[0] as an sMap with key
 * "credentials" -> sList of sStr. The trailing closure is
 * args[len(args)-1]. */
func nativeSshagent(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) < 2 {
        return nil, fmt.Errorf(
            "sshagent: expected a named-arg map " +
                "(credentials: [...]) and a closure body")
    }
    m, ok := args[0].(*sMap)
    if !ok {
        return nil, fmt.Errorf(
            "sshagent: first arg must be a map of named "+
                "args (got %T)", args[0])
    }
    credsList, ok := m.values["credentials"].(*sList)
    if !ok || credsList == nil {
        return nil, fmt.Errorf(
            "sshagent: missing 'credentials' list")
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "sshagent: last arg must be a closure")
    }
    if rt.creds == nil {
        return nil, fmt.Errorf(
            "sshagent: credstore not configured on executor")
    }

    /* Build the agent + identities. */
    agent := wolfssh.NewAgent()
    keys := make([]*ed25519KeyHandle, 0, len(credsList.items))
    defer func() {
        for _, k := range keys {
            k.close()
        }
    }()
    for _, item := range credsList.items {
        idStr, ok := item.(*sStr)
        if !ok {
            return nil, fmt.Errorf(
                "sshagent: credentials list entries must "+
                    "be strings (got %T)", item)
        }
        identity, handle, err := loadAgentIdentity(
            rt.creds, idStr.v)
        if err != nil {
            return nil, fmt.Errorf(
                "sshagent: load %q: %w", idStr.v, err)
        }
        agent.AddIdentity(identity)
        keys = append(keys, handle)
    }

    /* Pick a socket path. Unix-domain socket paths have a
     * tight length cap (104 bytes on macOS, 108 on Linux),
     * so the build workspace - which is often a long
     * t.TempDir() / builds/<job>/<n>/workspace path - is
     * unsafe. Always create a fresh short temp dir at the
     * platform default location and put the socket there.
     * The dir is removed in the deferred cleanup below. */
    sockDir, err := os.MkdirTemp("", "wolfci-sshagent-")
    if err != nil {
        return nil, fmt.Errorf(
            "sshagent: create temp dir: %w", err)
    }
    sockPath := filepath.Join(sockDir, "agent.sock")
    /* Make sure a stale file from a prior aborted run does
     * not block bind(). The check-and-remove is racy in
     * theory but the workspace is per-build and not shared. */
    _ = os.Remove(sockPath)

    listener, err := net.Listen("unix", sockPath)
    if err != nil {
        return nil, fmt.Errorf(
            "sshagent: listen %s: %w", sockPath, err)
    }
    /* Tighten the socket file permissions so other users on
     * the host cannot impersonate the build via the agent. */
    if err := os.Chmod(sockPath, 0o600); err != nil {
        _ = listener.Close()
        _ = os.Remove(sockPath)
        return nil, fmt.Errorf(
            "sshagent: chmod %s: %w", sockPath, err)
    }

    var wg sync.WaitGroup
    accept := agentAcceptLoop(listener, agent, &wg)
    defer func() {
        _ = listener.Close()
        wg.Wait()
        _ = os.Remove(sockPath)
        _ = os.Remove(sockDir)
        /* Drain the accept channel's final error (if any) so
         * the goroutine does not leak. */
        <-accept
    }()

    /* Push SSH_AUTH_SOCK onto the env stack. Empty mask -
     * the path is not a secret (it carries no key bytes). */
    frame := rt.pushSecrets(
        []string{"SSH_AUTH_SOCK=" + sockPath},
        nil)
    defer rt.popSecrets(frame)

    if _, err := invokeClosure(ctx, rt, cl, nil); err != nil {
        return nil, err
    }
    return &sNull{}, nil
}

/* ed25519KeyHandle wraps a wolfCrypt ed25519_key whose lifetime
 * is the surrounding sshagent block. close() releases the
 * underlying C state; we hold the handle in a slice so the
 * defer in nativeSshagent can release every identity on a
 * single pass. */
type ed25519KeyHandle struct {
    key *gowolf.Ed25519_key
}

func (h *ed25519KeyHandle) close() {
    if h.key != nil {
        gowolf.Wc_ed25519_free(h.key)
        h.key = nil
    }
}

/* loadAgentIdentity unseals an ssh-private-key cred and builds
 * a wolfssh.AgentIdentity backed by wolfCrypt's
 * Wc_ed25519_sign_msg. Returns the identity, a key handle (the
 * caller closes the handle when the agent block exits), and an
 * error.
 *
 * Other algorithms (RSA, ECDSA) come later; for 18.20 only
 * ssh-ed25519 is supported and a non-ed25519 cred surfaces an
 * actionable message. */
func loadAgentIdentity(store *credstore.Store,
    credID string) (wolfssh.AgentIdentity,
    *ed25519KeyHandle, error) {
    var zero wolfssh.AgentIdentity
    rec, err := store.Get(credID)
    if err != nil {
        return zero, nil, err
    }
    if rec.Type != credstore.TypeSshPrivateKey {
        return zero, nil, fmt.Errorf(
            "expected ssh-private-key cred, got %q",
            rec.Type)
    }
    var payload credstore.SshPrivateKeyPayload
    if err := json.Unmarshal(rec.Payload,
        &payload); err != nil {
        return zero, nil, fmt.Errorf(
            "unmarshal payload: %w", err)
    }
    if payload.Passphrase != "" {
        return zero, nil, errors.New(
            "passphrase-protected keys are not supported in " +
                "18.20 (unsealed keys only)")
    }
    parsed, err := wolfssh.ParseOpenSshPrivateKeyPEM(
        []byte(payload.PrivateKey))
    if err != nil {
        return zero, nil, fmt.Errorf(
            "parse PEM: %w", err)
    }
    if parsed.Algorithm != wolfssh.AlgSSHEd25519 {
        return zero, nil, fmt.Errorf(
            "algorithm %q not supported in 18.20 "+
                "(ssh-ed25519 only)", parsed.Algorithm)
    }
    /* Import the seed+pub into a wolfCrypt ed25519_key so the
     * agent's per-sign-request closure can call
     * Wc_ed25519_sign_msg directly without re-parsing. */
    var key gowolf.Ed25519_key
    if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
        return zero, nil, fmt.Errorf(
            "Wc_ed25519_init: %d", rc)
    }
    if rc := gowolf.Wc_ed25519_import_private_key(
        parsed.Ed25519Seed, len(parsed.Ed25519Seed),
        parsed.Ed25519Pub, len(parsed.Ed25519Pub),
        &key); rc != 0 {
        gowolf.Wc_ed25519_free(&key)
        return zero, nil, fmt.Errorf(
            "Wc_ed25519_import_private_key: %d", rc)
    }
    handle := &ed25519KeyHandle{key: &key}
    comment := parsed.Comment
    if comment == "" {
        comment = credID
    }
    sign := func(challenge []byte) ([]byte, error) {
        sig := make([]byte, 64)
        sigLen := 64
        rc := gowolf.Wc_ed25519_sign_msg(challenge,
            len(challenge), sig, &sigLen, handle.key)
        if rc != 0 {
            return nil, fmt.Errorf(
                "Wc_ed25519_sign_msg rc=%d", rc)
        }
        return sig[:sigLen], nil
    }
    return wolfssh.AgentIdentity{
        KeyBlob:   parsed.KeyBlob,
        Algorithm: parsed.Algorithm,
        Comment:   comment,
        Sign:      sign,
    }, handle, nil
}

/* agentAcceptLoop spawns the accept goroutine and returns a
 * one-shot done channel the caller drains after closing the
 * listener so the goroutine is guaranteed flushed. wg tracks
 * spawned per-connection handler goroutines so the deferred
 * cleanup waits for in-flight requests to finish. */
func agentAcceptLoop(listener net.Listener,
    agent *wolfssh.Agent, wg *sync.WaitGroup) <-chan struct{} {
    done := make(chan struct{}, 1)
    go func() {
        defer func() { done <- struct{}{} }()
        for {
            conn, err := listener.Accept()
            if err != nil {
                /* Closed listener returns an error here; the
                 * surrounding defer handles cleanup. Any other
                 * accept error is also fatal for this loop. */
                return
            }
            wg.Add(1)
            go func(c net.Conn) {
                defer wg.Done()
                defer c.Close()
                serveAgentConn(c, agent)
            }(conn)
        }
    }()
    return done
}

/* serveAgentConn handles one agent-protocol session over a
 * net.Conn. The wire framing is uint32 big-endian length
 * prefix followed by message body; we read repeatedly until
 * the peer closes. Any wire error closes the connection
 * (silent: the client decides whether to retry). */
func serveAgentConn(c net.Conn, agent *wolfssh.Agent) {
    var lenBuf [4]byte
    for {
        if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
            return
        }
        length := binary.BigEndian.Uint32(lenBuf[:])
        if length == 0 || length > 1<<20 {
            return
        }
        body := make([]byte, length)
        if _, err := io.ReadFull(c, body); err != nil {
            return
        }
        framed := append(lenBuf[:], body...)
        resp, err := agent.HandleFramedMessage(framed)
        if err != nil {
            return
        }
        if _, err := c.Write(resp); err != nil {
            return
        }
    }
}
