//go:build linux

package nodeinfo_test

/* Linux-only gate for PLAN.md 12.1. The cross-platform tests
 * cover the disk and temp wiring; this file proves the linux-
 * specific helpers (/proc/meminfo SwapFree, /proc/uptime) also
 * return plausible values on a real Linux host.
 */

import (
    "runtime"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/nodeinfo"
)

func TestSnapshot_LinuxReturnsNonZero(t *testing.T) {
    if runtime.GOOS != "linux" {
        t.Skipf("linux-only gate; running on %s", runtime.GOOS)
    }
    snap, err := nodeinfo.Take(t.TempDir())
    if err != nil {
        t.Fatalf("Take: %v", err)
    }
    if snap.FreeDiskBytes <= 0 {
        t.Errorf("FreeDiskBytes = %d, want > 0 on a real Linux host",
            snap.FreeDiskBytes)
    }
    if snap.FreeTempBytes <= 0 {
        t.Errorf("FreeTempBytes = %d, want > 0", snap.FreeTempBytes)
    }
    /* Swap may legitimately be zero on a machine that has
     * disabled the swap file or is running inside a container
     * with the swap accounting disabled. The CI environment
     * happens to disable swap, so we tolerate zero here; a
     * negative value would still indicate a parse bug.
     */
    if snap.FreeSwapBytes < 0 {
        t.Errorf("FreeSwapBytes = %d, want >= 0", snap.FreeSwapBytes)
    }
    if snap.HostUptime <= 0 {
        t.Errorf("HostUptime = %v, want > 0", snap.HostUptime)
    }
    if max := 100 * 365 * 24 * time.Hour; snap.HostUptime > max {
        t.Errorf("HostUptime = %v, want < %v (likely parse bug)",
            snap.HostUptime, max)
    }
}
