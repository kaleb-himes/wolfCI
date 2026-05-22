package nodeinfo_test

/* Cross-platform gates for PLAN.md 12.1. TestSnapshot_RootMissing
 * proves the package returns an error (not zero values) when the
 * caller hands in a path that does not exist; this matters
 * because Phase 12.4 reports the snapshot to operators in the UI
 * and a silently-zero FreeDiskBytes would read as "this node is
 * full". TestSnapshot_TempDir proves the per-OS statfs wiring
 * actually reaches the kernel by checking that the host's
 * temp-dir bytes-free is non-zero on a machine with a usable
 * /tmp (the test environment guarantees this).
 *
 * The build-tagged darwin/linux gates live in
 * nodeinfo_darwin_test.go and nodeinfo_linux_test.go; they cover
 * the OS-specific fields (FreeSwapBytes, HostUptime) that this
 * file deliberately does not assert on (the unsupported-GOOS
 * fallback would otherwise have to spoof them).
 */

import (
    "errors"
    "path/filepath"
    "runtime"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/nodeinfo"
)

func TestSnapshot_RootMissing(t *testing.T) {
    /* Pick a path that demonstrably does not exist. t.TempDir
     * gives us a directory the runtime cleans up post-test;
     * dropping into a child that was never mkdir'd is a
     * deterministic non-existent path on every supported OS.
     */
    missing := filepath.Join(t.TempDir(), "definitely", "not", "here")

    snap, err := nodeinfo.Take(missing)
    if err == nil {
        t.Fatalf("Take(%q) returned nil err; want statfs failure", missing)
    }
    if snap.FreeDiskBytes != 0 {
        t.Errorf("FreeDiskBytes = %d, want 0 when root is missing",
            snap.FreeDiskBytes)
    }
    /* The shared fields should still be populated; the caller
     * uses Architecture + GoVersion + Now even on a partial
     * read so the UI can render "node has no disk metric"
     * rather than dropping the whole row.
     */
    if snap.Architecture == "" {
        t.Error("Architecture is empty even on a partial snapshot")
    }
    if snap.GoVersion == "" {
        t.Error("GoVersion is empty even on a partial snapshot")
    }
    if snap.Now.IsZero() {
        t.Error("Now is zero-value even on a partial snapshot")
    }
}

func TestSnapshot_TempDir(t *testing.T) {
    /* Pass t.TempDir as root so the FreeDiskBytes call also has
     * a valid target. FreeTempBytes is what we are actually
     * asserting on: the test environment is guaranteed to have
     * a writable os.TempDir(), so a zero reading proves the
     * statfs wiring is broken.
     */
    snap, err := nodeinfo.Take(t.TempDir())
    if err != nil {
        if errors.Is(err, nodeinfo.ErrUnsupported) {
            t.Skipf("nodeinfo unsupported on %s/%s",
                runtime.GOOS, runtime.GOARCH)
        }
        t.Fatalf("Take: %v", err)
    }
    if snap.FreeTempBytes <= 0 {
        t.Errorf("FreeTempBytes = %d, want > 0 on a host with a usable /tmp",
            snap.FreeTempBytes)
    }
    /* Sanity on the always-cheap fields. */
    if !strings.Contains(snap.Architecture, runtime.GOOS) {
        t.Errorf("Architecture = %q, want it to contain %q",
            snap.Architecture, runtime.GOOS)
    }
    if !strings.Contains(snap.Architecture, runtime.GOARCH) {
        t.Errorf("Architecture = %q, want it to contain %q",
            snap.Architecture, runtime.GOARCH)
    }
    if snap.GoVersion == "" {
        t.Error("GoVersion is empty")
    }
    /* Now should be within a small window of the wall clock
     * to prove the snapshot was taken right now and not on
     * package init.
     */
    if delta := time.Since(snap.Now); delta < 0 || delta > time.Second {
        t.Errorf("Now drift = %v, want within 1s of wall clock", delta)
    }
}
