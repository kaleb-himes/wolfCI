//go:build darwin

package nodeinfo_test

/* Darwin-only gate for PLAN.md 12.1. The cross-platform tests
 * cover the disk and temp wiring; this file proves the darwin-
 * specific helpers (sysctl vm.swapusage, sysctl kern.boottime)
 * also return plausible non-zero values on a real macOS host.
 */

import (
    "runtime"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/nodeinfo"
)

func TestSnapshot_DarwinReturnsNonZero(t *testing.T) {
    if runtime.GOOS != "darwin" {
        t.Skipf("darwin-only gate; running on %s", runtime.GOOS)
    }
    snap, err := nodeinfo.Take(t.TempDir())
    if err != nil {
        t.Fatalf("Take: %v", err)
    }
    if snap.FreeDiskBytes <= 0 {
        t.Errorf("FreeDiskBytes = %d, want > 0 on a real macOS host",
            snap.FreeDiskBytes)
    }
    if snap.FreeTempBytes <= 0 {
        t.Errorf("FreeTempBytes = %d, want > 0", snap.FreeTempBytes)
    }
    /* Swap may legitimately be zero on a machine that has
     * disabled the swap file, so we only assert "not negative"
     * here. A negative value would mean we mis-parsed the
     * vm.swapusage struct.
     */
    if snap.FreeSwapBytes < 0 {
        t.Errorf("FreeSwapBytes = %d, want >= 0", snap.FreeSwapBytes)
    }
    /* Uptime must be positive and below 100 years (the latter
     * catches a sign-extension or byte-order mistake on
     * kern.boottime).
     */
    if snap.HostUptime <= 0 {
        t.Errorf("HostUptime = %v, want > 0", snap.HostUptime)
    }
    if max := 100 * 365 * 24 * time.Hour; snap.HostUptime > max {
        t.Errorf("HostUptime = %v, want < %v (likely byte-order bug)",
            snap.HostUptime, max)
    }
}
