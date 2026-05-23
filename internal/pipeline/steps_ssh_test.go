package pipeline_test

/* internal/pipeline/steps_ssh_test.go - PLAN.md 18.20 gating
 * test.
 *
 * TestStep_SshagentGitClone is the explicit gate named in
 * PLAN.md: it loads an ssh-private-key cred from a fresh
 * credstore, wraps a `sh` step in `sshagent { ... }`, and
 * asserts the wrapped shell call connects via wolfssh's agent
 * socket and exercises the expected identity successfully.
 *
 * Instead of standing up a real `sshd` + `git` + `ssh` chain
 * (which would couple this fast unit test to several OS
 * binaries plus a network namespace), the test substitutes a
 * tiny Go helper at internal/pipeline/sshagent_probe/ for the
 * `git clone git@...` invocation. The probe runs as a child of
 * /bin/sh -c "..." just like the real git client would, reads
 * the SSH_AUTH_SOCK env the sshagent step injects, and drives
 * the SSH agent protocol against the socket. A real git client
 * would do the same thing - the wire surface beyond the agent
 * is wolfssh's responsibility and is covered by the wolfssh
 * sub-package tests at third_party/go-wolfssl/wolfssh/.
 *
 * The wolfssh wire surface itself is gated by the
 * TestOpenSshPrivateKey_Ed25519_RoundTrip and TestAgent_* tests
 * inside the wolfssh sub-package, so by the time this test
 * runs we already know the agent protocol, the OpenSSH PRIVATE
 * KEY codec, and the wolfCrypt sign primitive each work in
 * isolation. This test verifies the pipeline step wires them
 * together correctly.
 */

import (
    "context"
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "testing"
    "time"

    gowolf "github.com/wolfssl/go-wolfssl"
    "github.com/wolfssl/go-wolfssl/wolfssh"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_SshagentGitClone(t *testing.T) {
    if runtime.GOOS == "windows" {
        t.Skip("unix-socket agent path not exercised on " +
            "Windows in 18.20")
    }
    workspace := t.TempDir()
    credsDir := t.TempDir()
    binDir := t.TempDir()

    /* (1) Build the probe binary - the stand-in for the
     * `git clone git@...` invocation. */
    probePath := filepath.Join(binDir, "sshagent-probe")
    build := exec.Command("go", "build", "-o", probePath, ".")
    build.Dir = filepath.Join(".", "sshagent_probe")
    if out, err := build.CombinedOutput(); err != nil {
        t.Fatalf("go build sshagent_probe: %v\n%s", err, out)
    }

    /* (2) Mint an Ed25519 keypair via wolfCrypt and encode it
     * as an OPENSSH PRIVATE KEY PEM. */
    seed, pub := newEd25519IdentityForCred(t)
    const credComment = "wolfci-bot@sshagent-test"
    pem, err := wolfssh.EncodeOpenSshPrivateKeyEd25519PEM(
        seed, pub, credComment, 0xc0ffeeee)
    if err != nil {
        t.Fatalf("EncodeOpenSshPrivateKeyEd25519PEM: %v", err)
    }

    /* (3) Seal the PEM into a fresh credstore as an
     * ssh-private-key record. */
    store, err := credstore.NewStore(credsDir,
        []byte("0123456789abcdef0123456789abcdef"))
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    payload, err := json.Marshal(credstore.SshPrivateKeyPayload{
        PrivateKey: string(pem),
    })
    if err != nil {
        t.Fatalf("marshal payload: %v", err)
    }
    if err := store.Add("bot-key", &credstore.Record{
        Type:      credstore.TypeSshPrivateKey,
        Payload:   payload,
        CreatedAt: time.Now(),
        Label:     "test ssh key",
    }); err != nil {
        t.Fatalf("Add: %v", err)
    }

    /* (4) Run a pipeline that wraps the probe invocation in
     * sshagent. The probe writes a witness file when the
     * agent reports the expected identity and signs the
     * test challenge successfully. */
    witnessPath := filepath.Join(workspace, "witness.txt")
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                sshagent(credentials: ['bot-key']) {
                    sh '` + probePath + ` "` +
        credComment + `" "` + witnessPath + `"'
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    exe := &pipeline.LocalExecutor{
        Workspace: workspace,
        Creds:     store,
    }
    build2, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build2.Status != pipeline.BuildSuccess {
        var allOut strings.Builder
        for _, st := range build2.Stages[0].Steps {
            allOut.WriteString(st.Output)
            allOut.WriteString("\n")
        }
        t.Fatalf("build.Status = %v, want BuildSuccess; "+
            "step output:\n%s", build2.Status, allOut.String())
    }

    /* (5) Witness file confirms the probe saw the expected
     * identity and the SIGN_REQUEST round trip succeeded. */
    data, err := os.ReadFile(witnessPath)
    if err != nil {
        t.Fatalf("read witness (probe did not run / agent "+
            "did not satisfy probe): %v", err)
    }
    s := string(data)
    if !strings.HasPrefix(s, "OK\n") {
        t.Errorf("witness = %q, want OK header", s)
    }
    if !strings.Contains(s, credComment) {
        t.Errorf("witness = %q, want probe to report comment "+
            "%q", s, credComment)
    }

    /* (6) sshagent's deferred cleanup tears down the
     * per-step temp dir that held the unix socket. We don't
     * know the exact path (it's randomised), but the witness
     * file having landed proves the socket was reachable and
     * served the agent protocol during the closure body. The
     * dir is created under the platform temp root and
     * removed on the way out; a leak would surface as a
     * monotonically-growing /tmp on a long-lived runner, not
     * as a test failure here.
     */
}

/* TestStep_SshagentMissingCred locks in the actionable error
 * path: a missing cred id surfaces as a build failure, not a
 * panic. */
func TestStep_SshagentMissingCred(t *testing.T) {
    workspace := t.TempDir()
    credsDir := t.TempDir()
    store, err := credstore.NewStore(credsDir,
        []byte("0123456789abcdef0123456789abcdef"))
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                sshagent(credentials: ['nope']) {
                    sh 'true'
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    exe := &pipeline.LocalExecutor{
        Workspace: workspace,
        Creds:     store,
    }
    build, _ := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if build == nil || build.Status != pipeline.BuildFailure {
        t.Errorf("expected BuildFailure for missing cred; "+
            "got %+v", build)
    }
}

/* newEd25519IdentityForCred mints a fresh ed25519 keypair and
 * returns (seed, pub). The seed is the 32-byte ed25519 private
 * seed wolfCrypt's Wc_ed25519_import_private_key consumes; pub
 * is the 32-byte public key. */
func newEd25519IdentityForCred(t *testing.T) (seed, pub []byte) {
    t.Helper()
    var rng gowolf.WC_RNG
    if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
        t.Fatalf("Wc_InitRng: %d", rc)
    }
    t.Cleanup(func() { gowolf.Wc_FreeRng(&rng) })

    var key gowolf.Ed25519_key
    if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
        t.Fatalf("Wc_ed25519_init: %d", rc)
    }
    t.Cleanup(func() { gowolf.Wc_ed25519_free(&key) })

    if rc := gowolf.Wc_ed25519_make_key(&rng, 32, &key); rc != 0 {
        t.Fatalf("Wc_ed25519_make_key: %d", rc)
    }
    pub = make([]byte, 32)
    pubLen := 32
    if rc := gowolf.Wc_ed25519_export_public(&key, pub,
        &pubLen); rc != 0 {
        t.Fatalf("Wc_ed25519_export_public: %d", rc)
    }
    seed = make([]byte, 32)
    seedLen := 32
    if rc := gowolf.Wc_ed25519_export_private_only(&key, seed,
        &seedLen); rc != 0 {
        t.Fatalf("Wc_ed25519_export_private_only: %d", rc)
    }
    return seed, pub
}
