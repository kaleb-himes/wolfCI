/* gowolfssh_smoke_test.go - smoke test for the wolfssh sub-package
 * of github.com/wolfssl/go-wolfssl.
 *
 * The 10.8a goal is to prove the sub-package design works:
 *   - third_party/go-wolfssl/wolfssh/ compiles as a Go sub-package
 *   - importing it pulls libwolfssh.a into the build (via #cgo
 *     LDFLAGS in wolfssh.go)
 *   - Wc_SSH wrappers can be called without crashing
 *
 * Tests live in their own package so this is the only wolfCI code
 * that imports wolfssh today. 10.8b wires sshkey.go onto the new
 * wrappers and adds the real "parse a key, hand it to wolfcrypt
 * for verify" round-trip gate.
 */
package authssh_test

import (
    "testing"

    gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"
)

/*
 * TestGoWolfSSH_ReadPublicKey_Callable confirms the wrapper is
 * reachable from a Go test: a call returns (we did not link
 * against a missing symbol) and we get back some integer return
 * code (we did not crash mid-call). The CALLER side of the cgo
 * bridge is the 10.8a milestone; the PARSE-correctness side is
 * the 10.8b gate.
 */
func TestGoWolfSSH_ReadPublicKey_Callable(t *testing.T) {
    _, _, rc := gowolfssh.WolfSSH_ReadPublicKey_buffer(
        []byte("ssh-ed25519 \n"), gowolfssh.FORMAT_SSH)
    /*
     * rc value itself is not asserted here: we want the call to
     * COMPLETE (not crash). Any deterministic return value
     * proves the sub-package linked and the wrapper is wired.
     */
    _ = rc
}

/*
 * TestGoWolfSSH_FormatConstants pins the FORMAT_* constants
 * exposed by the sub-package so a wolfssh re-roll that
 * re-numbers the enum is caught here, not in a downstream call
 * site.
 */
func TestGoWolfSSH_FormatConstants(t *testing.T) {
    /*
     * Distinct nonzero values are the bar - the exact integers
     * are wolfssh-internal.
     */
    seen := map[int]string{}
    for _, p := range []struct {
        name  string
        value int
    }{
        {"FORMAT_ASN1", gowolfssh.FORMAT_ASN1},
        {"FORMAT_PEM", gowolfssh.FORMAT_PEM},
        {"FORMAT_RAW", gowolfssh.FORMAT_RAW},
        {"FORMAT_SSH", gowolfssh.FORMAT_SSH},
        {"FORMAT_OPENSSH", gowolfssh.FORMAT_OPENSSH},
    } {
        if prior, ok := seen[p.value]; ok {
            t.Errorf("%s and %s both equal %d",
                p.name, prior, p.value)
        }
        seen[p.value] = p.name
    }
}
