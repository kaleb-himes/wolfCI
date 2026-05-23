/* gowolfssh_agent_smoke_test.go - smoke test for the wolfssh
 * agent wrappers added by go-wolfssl patch 0008.
 *
 * Drives the public surface of go-wolfssl/wolfssh's agent.go
 * end-to-end so the wrapper stays gated from wolfCI's test
 * suite, not just the in-submodule tests (which can be
 * skipped on a fresh clone before the patch is re-applied).
 *
 * Mirrors TestAgent_AddListSign in
 * third_party/go-wolfssl/wolfssh/agent_test.go but lives here
 * to keep the wolfCI side independent from the submodule's
 * own test-run policy.
 */
package authssh_test

import (
    "bytes"
    "encoding/binary"
    "testing"

    gowolf "github.com/wolfssl/go-wolfssl"
    gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"
)

/* TestGoWolfSSH_AgentAddListSign_Callable mints an Ed25519
 * identity via the wolfCrypt wrappers, registers it on a
 * fresh wolfssh.Agent, asks the agent to list identities
 * over the SSH agent protocol (RFC 4253-style framed
 * messages), then asks it to sign a challenge and verifies
 * the signature with wolfCrypt's verifier. Failure in any
 * step (RNG init, sign primitive, wire decode, signature
 * verify) fails the test - this is the per-task gate the
 * 0008 patch README points at. */
func TestGoWolfSSH_AgentAddListSign_Callable(t *testing.T) {
    var rng gowolf.WC_RNG
    if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
        t.Fatalf("Wc_InitRng: %d", rc)
    }
    defer gowolf.Wc_FreeRng(&rng)
    var signer gowolf.Ed25519_key
    if rc := gowolf.Wc_ed25519_init(&signer); rc != 0 {
        t.Fatalf("Wc_ed25519_init: %d", rc)
    }
    defer gowolf.Wc_ed25519_free(&signer)
    if rc := gowolf.Wc_ed25519_make_key(&rng, 32,
        &signer); rc != 0 {
        t.Fatalf("Wc_ed25519_make_key: %d", rc)
    }
    pub := make([]byte, 32)
    pubLen := 32
    if rc := gowolf.Wc_ed25519_export_public(&signer, pub,
        &pubLen); rc != 0 {
        t.Fatalf("Wc_ed25519_export_public: %d", rc)
    }
    var verifier gowolf.Ed25519_key
    if rc := gowolf.Wc_ed25519_init(&verifier); rc != 0 {
        t.Fatalf("Wc_ed25519_init (verifier): %d", rc)
    }
    defer gowolf.Wc_ed25519_free(&verifier)
    if rc := gowolf.Wc_ed25519_import_public(pub, len(pub),
        &verifier); rc != 0 {
        t.Fatalf("Wc_ed25519_import_public: %d", rc)
    }

    keyBody := gowolfssh.EncodeSSHString(pub)
    agent := gowolfssh.NewAgent()
    agent.AddIdentity(gowolfssh.AgentIdentity{
        KeyBlob:   keyBody,
        Algorithm: gowolfssh.AlgSSHEd25519,
        Comment:   "wolfci-smoke@localhost",
        Sign: func(c []byte) ([]byte, error) {
            sig := make([]byte, 64)
            sigLen := 64
            rc := gowolf.Wc_ed25519_sign_msg(c, len(c),
                sig, &sigLen, &signer)
            if rc != 0 {
                return nil, &shortRcErr{rc: rc}
            }
            return sig[:sigLen], nil
        },
    })

    /* List identities. */
    listResp, err := agent.HandleMessage(
        []byte{gowolfssh.AgentMsgRequestIdentities})
    if err != nil {
        t.Fatalf("HandleMessage(list): %v", err)
    }
    if listResp[0] != gowolfssh.AgentMsgIdentitiesAnswer {
        t.Fatalf("list msgID=%d, want IDENTITIES_ANSWER",
            listResp[0])
    }
    if n := binary.BigEndian.Uint32(
        listResp[1:5]); n != 1 {
        t.Fatalf("list reported %d keys, want 1", n)
    }
    fullBlob, rest, err := gowolfssh.ReadSSHString(
        listResp[5:])
    if err != nil {
        t.Fatalf("read keyblob: %v", err)
    }
    _, _, _ = gowolfssh.ReadSSHString(rest) /* comment */

    /* Sign request: the listed blob, a fixed challenge,
     * uint32 flags=0. */
    challenge := []byte("agent-smoke-challenge")
    var req bytes.Buffer
    req.WriteByte(gowolfssh.AgentMsgSignRequest)
    req.Write(gowolfssh.EncodeSSHString(fullBlob))
    req.Write(gowolfssh.EncodeSSHString(challenge))
    req.Write([]byte{0, 0, 0, 0})
    signResp, err := agent.HandleMessage(req.Bytes())
    if err != nil {
        t.Fatalf("HandleMessage(sign): %v", err)
    }
    if signResp[0] != gowolfssh.AgentMsgSignResponse {
        t.Fatalf("sign msgID=%d, want SIGN_RESPONSE",
            signResp[0])
    }
    sigBlob, _, err := gowolfssh.ReadSSHString(signResp[1:])
    if err != nil {
        t.Fatalf("read sig blob: %v", err)
    }
    algo, sigInner, err := gowolfssh.ReadSSHString(sigBlob)
    if err != nil {
        t.Fatalf("parse algo: %v", err)
    }
    if string(algo) != gowolfssh.AlgSSHEd25519 {
        t.Fatalf("sig algo=%q, want ssh-ed25519",
            string(algo))
    }
    sigBytes, _, err := gowolfssh.ReadSSHString(sigInner)
    if err != nil {
        t.Fatalf("parse sig bytes: %v", err)
    }
    var verifyRes int
    if rc := gowolf.Wc_ed25519_verify_msg(sigBytes,
        len(sigBytes), challenge, len(challenge),
        &verifyRes, &verifier); rc != 0 || verifyRes != 1 {
        t.Fatalf("Wc_ed25519_verify_msg rc=%d res=%d "+
            "(agent-produced signature did not verify)",
            rc, verifyRes)
    }
}

/* shortRcErr is a minimal error type that surfaces a numeric
 * wolfCrypt return code without dragging fmt into the smoke
 * test. */
type shortRcErr struct{ rc int }

func (e *shortRcErr) Error() string {
    return "Wc_ed25519_sign_msg rc=" + itoa(e.rc)
}

func itoa(n int) string {
    if n == 0 {
        return "0"
    }
    neg := false
    if n < 0 {
        neg = true
        n = -n
    }
    var buf [16]byte
    i := len(buf)
    for n > 0 {
        i--
        buf[i] = byte('0' + n%10)
        n /= 10
    }
    if neg {
        i--
        buf[i] = '-'
    }
    return string(buf[i:])
}
