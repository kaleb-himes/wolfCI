//go:build darwin

/* Darwin nodeinfo implementation. statfs comes via
 * golang.org/x/sys/unix; the swap and uptime readings come from
 * sysctl. The sysctl bytes are parsed with encoding/binary
 * rather than unsafe pointer-casts so the file stays portable
 * across darwin/amd64 and darwin/arm64 without per-arch shims.
 */
package nodeinfo

import (
    "encoding/binary"
    "fmt"
    "os"
    "time"

    "golang.org/x/sys/unix"
)

func platformTake(snap Snapshot, root string) (Snapshot, error) {
    diskBytes, err := statfsFree(root)
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: free disk %q: %w",
            root, err)
    }
    snap.FreeDiskBytes = diskBytes

    tempBytes, err := statfsFree(os.TempDir())
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: free temp: %w", err)
    }
    snap.FreeTempBytes = tempBytes

    swap, err := readSwapDarwin()
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: swap: %w", err)
    }
    snap.FreeSwapBytes = swap

    uptime, err := readUptimeDarwin()
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: uptime: %w", err)
    }
    snap.HostUptime = uptime

    return snap, nil
}

/* statfsFree returns the bytes available to non-root users on
 * the filesystem containing path. statfs Bavail counts the
 * blocks reserved for unprivileged use; multiplying by Bsize
 * yields a bytes figure that matches what `df -k` reports.
 */
func statfsFree(path string) (int64, error) {
    var s unix.Statfs_t
    if err := unix.Statfs(path, &s); err != nil {
        return 0, err
    }
    return int64(s.Bavail) * int64(s.Bsize), nil
}

/* readSwapDarwin reads sysctl vm.swapusage. The kernel returns
 * a xsw_usage struct laid out as
 *     uint64 xsu_total
 *     uint64 xsu_avail
 *     uint64 xsu_used
 *     uint32 xsu_pagesize
 *     boolean_t xsu_encrypted
 * Bytes 0..7 are total, 8..15 are avail, 16..23 are used. We
 * only need avail; everything past byte 15 is ignored.
 */
func readSwapDarwin() (int64, error) {
    data, err := unix.SysctlRaw("vm.swapusage")
    if err != nil {
        return 0, fmt.Errorf("sysctl vm.swapusage: %w", err)
    }
    if len(data) < 16 {
        return 0, fmt.Errorf(
            "sysctl vm.swapusage: short read (%d bytes)", len(data))
    }
    avail := binary.LittleEndian.Uint64(data[8:16])
    /* Cast through int64 so a > MaxInt64 reading (physically
     * impossible today but cheap to guard) saturates instead
     * of wrapping negative.
     */
    if avail > 1<<62 {
        return 1 << 62, nil
    }
    return int64(avail), nil
}

/* readUptimeDarwin reads sysctl kern.boottime, which returns a
 * struct timeval { tv_sec; tv_usec }. On both darwin/amd64 and
 * darwin/arm64 tv_sec is a 64-bit signed seconds count and
 * tv_usec is a 32-bit microseconds count. We round to seconds
 * for the duration (microsecond precision against a boot-time
 * stamp is noise).
 */
func readUptimeDarwin() (time.Duration, error) {
    data, err := unix.SysctlRaw("kern.boottime")
    if err != nil {
        return 0, fmt.Errorf("sysctl kern.boottime: %w", err)
    }
    if len(data) < 8 {
        return 0, fmt.Errorf(
            "sysctl kern.boottime: short read (%d bytes)", len(data))
    }
    bootSec := int64(binary.LittleEndian.Uint64(data[0:8]))
    bootTime := time.Unix(bootSec, 0)
    delta := time.Since(bootTime)
    if delta < 0 {
        return 0, fmt.Errorf(
            "sysctl kern.boottime: future boot time %v", bootTime)
    }
    return delta, nil
}
