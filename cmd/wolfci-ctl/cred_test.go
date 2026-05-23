package main

/* cmd/wolfci-ctl/cred_test.go - PLAN.md 18.4 gating test.
 *
 * Exercises the wolfci-ctl cred subcommands end-to-end:
 *   wolfci-ctl cred add  --id X --type secret-text  (stdin = secret)
 *   wolfci-ctl cred list
 *   wolfci-ctl cred rm   --id X
 *
 * Reuses internal/credstore from 18.3. The test stages a fake
 * server.yaml in a temp dir, populates credential_master_secret
 * and credential_dir, and points every subcommand at that
 * server.yaml via --server-config.
 */

import (
    "encoding/hex"
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestCtl_CredentialLifecycle(t *testing.T) {
    dir := t.TempDir()
    credDir := filepath.Join(dir, "credentials")

    /* Mint a 32-byte master secret and write a minimal-but-
     * valid server.yaml that names it + the cred dir.
     */
    master := make([]byte, 32)
    for i := range master {
        master[i] = byte(i + 1)
    }
    masterHex := hex.EncodeToString(master)

    serverYAML := filepath.Join(dir, "server.yaml")
    if err := os.WriteFile(serverYAML, []byte(""+
        "listen_addr: 127.0.0.1:8443\n"+
        "cert: /dev/null\n"+
        "key: /dev/null\n"+
        "ca_cert: /dev/null\n"+
        "work_dir: "+filepath.Join(dir, "work")+"\n"+
        "auth_dir: "+filepath.Join(dir, "auth")+"\n"+
        "credential_master_secret: "+masterHex+"\n"+
        "credential_dir: "+credDir+"\n",
    ), 0o600); err != nil {
        t.Fatalf("write server.yaml: %v", err)
    }

    /* Run `wolfci-ctl cred add --id gh-token --type secret-text`
     * with the secret on stdin.
     */
    code := runCredWithStdin(t, "ghp_xxx",
        "cred", "add",
        "--server-config", serverYAML,
        "--id", "gh-token",
        "--type", "secret-text",
        "--label", "GitHub PAT")
    if code != 0 {
        t.Fatalf("cred add exit = %d, want 0", code)
    }

    /* The sealed file must now exist under credDir. */
    if _, err := os.Stat(filepath.Join(credDir,
        "gh-token.sealed")); err != nil {
        t.Errorf("sealed file missing: %v", err)
    }

    /* `wolfci-ctl cred list` prints "gh-token" and "secret-text". */
    listOut, code := runCredCapture(t,
        "cred", "list",
        "--server-config", serverYAML)
    if code != 0 {
        t.Fatalf("cred list exit = %d, want 0", code)
    }
    for _, want := range []string{
        "gh-token", "secret-text", "GitHub PAT",
    } {
        if !strings.Contains(listOut, want) {
            t.Errorf("cred list output missing %q\nfull:\n%s",
                want, listOut)
        }
    }

    /* `wolfci-ctl cred rm --id gh-token` removes both the file
     * and the index entry.
     */
    code = runCredCaptureSimple(t,
        "cred", "rm",
        "--server-config", serverYAML,
        "--id", "gh-token")
    if code != 0 {
        t.Fatalf("cred rm exit = %d, want 0", code)
    }
    if _, err := os.Stat(filepath.Join(credDir,
        "gh-token.sealed")); !os.IsNotExist(err) {
        t.Errorf("sealed file still present after rm: %v", err)
    }
    listOut, code = runCredCapture(t,
        "cred", "list",
        "--server-config", serverYAML)
    if code != 0 {
        t.Fatalf("cred list (post-rm) exit = %d, want 0", code)
    }
    if strings.Contains(listOut, "gh-token") {
        t.Errorf("gh-token still in list output:\n%s", listOut)
    }
}

func TestCtl_CredAdd_RequiresStdinForSecretText(t *testing.T) {
    dir := t.TempDir()
    serverYAML := filepath.Join(dir, "server.yaml")
    master := make([]byte, 32)
    masterHex := hex.EncodeToString(master[:])
    /* a 32-byte master of all zeros is fine for this negative
     * test - we never actually unseal anything.
     */
    if err := os.WriteFile(serverYAML, []byte(""+
        "listen_addr: 127.0.0.1:8443\n"+
        "cert: /dev/null\n"+
        "key: /dev/null\n"+
        "ca_cert: /dev/null\n"+
        "work_dir: "+filepath.Join(dir, "work")+"\n"+
        "auth_dir: "+filepath.Join(dir, "auth")+"\n"+
        "credential_master_secret: "+masterHex+"\n"+
        "credential_dir: "+filepath.Join(dir, "creds")+"\n",
    ), 0o600); err != nil {
        t.Fatalf("write server.yaml: %v", err)
    }

    /* Empty stdin must fail (an empty secret is almost always a
     * mistake; reject it loudly rather than seal an empty value
     * that future Unseal calls will silently succeed on).
     */
    code := runCredWithStdin(t, "",
        "cred", "add",
        "--server-config", serverYAML,
        "--id", "blank",
        "--type", "secret-text")
    if code == 0 {
        t.Fatalf("cred add with empty stdin returned 0; "+
            "want non-zero exit")
    }
}

/* runCredWithStdin runs dispatch with the given args and feeds
 * stdinBody to the subcommand on stdin. Returns the exit code.
 */
func runCredWithStdin(t *testing.T, stdinBody string,
    args ...string) int {

    t.Helper()
    inR, inW, err := os.Pipe()
    if err != nil {
        t.Fatalf("stdin pipe: %v", err)
    }
    t.Cleanup(func() {
        inR.Close()
        inW.Close()
    })
    origStdin := os.Stdin
    os.Stdin = inR
    t.Cleanup(func() { os.Stdin = origStdin })

    go func() {
        defer inW.Close()
        if stdinBody != "" {
            _, _ = inW.Write([]byte(stdinBody))
        }
    }()

    out, errOut, _, _ := captureFiles(t)
    return dispatch(args, out, errOut)
}

/* runCredCapture runs dispatch with the given args and returns
 * (stdout text, exit code).
 */
func runCredCapture(t *testing.T,
    args ...string) (string, int) {

    t.Helper()
    out, errOut, getOut, _ := captureFiles(t)
    code := dispatch(args, out, errOut)
    return getOut(), code
}

/* runCredCaptureSimple is like runCredCapture but discards the
 * stdout text (the caller only cares about the exit code).
 */
func runCredCaptureSimple(t *testing.T,
    args ...string) int {

    t.Helper()
    _, code := runCredCapture(t, args...)
    return code
}
