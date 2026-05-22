//go:build linux

/* Linux nodeinfo implementation. statfs comes via
 * golang.org/x/sys/unix; swap and uptime are parsed out of
 * /proc/meminfo and /proc/uptime. /proc has been the stable
 * source for these on every Linux release since 2.6, and is
 * available inside every container runtime we are likely to
 * encounter (Docker, containerd, podman); no fallback path is
 * needed.
 */
package nodeinfo

import (
    "bufio"
    "fmt"
    "os"
    "strconv"
    "strings"
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

    swap, err := readSwapLinux()
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: swap: %w", err)
    }
    snap.FreeSwapBytes = swap

    uptime, err := readUptimeLinux()
    if err != nil {
        return snap, fmt.Errorf("nodeinfo.Take: uptime: %w", err)
    }
    snap.HostUptime = uptime

    return snap, nil
}

/* statfsFree returns the bytes available to non-root users on
 * the filesystem containing path. Bavail*Bsize matches the
 * "Available" column of `df -k` on Linux.
 */
func statfsFree(path string) (int64, error) {
    var s unix.Statfs_t
    if err := unix.Statfs(path, &s); err != nil {
        return 0, err
    }
    return int64(s.Bavail) * int64(s.Bsize), nil
}

/* readSwapLinux parses /proc/meminfo for the SwapFree line.
 * Format is "<key>: <value> kB" with arbitrary whitespace; the
 * scanner walks lines until it finds the right key, then
 * converts the value from KiB to bytes.
 *
 * A missing SwapFree line (a kernel that does not expose swap
 * accounting) returns 0 and no error - swap absence is a
 * legitimate config, not a parse failure.
 */
func readSwapLinux() (int64, error) {
    f, err := os.Open("/proc/meminfo")
    if err != nil {
        return 0, fmt.Errorf("open /proc/meminfo: %w", err)
    }
    defer f.Close()

    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := scanner.Text()
        if !strings.HasPrefix(line, "SwapFree:") {
            continue
        }
        fields := strings.Fields(line)
        if len(fields) < 2 {
            return 0, fmt.Errorf(
                "/proc/meminfo SwapFree malformed: %q", line)
        }
        kb, err := strconv.ParseInt(fields[1], 10, 64)
        if err != nil {
            return 0, fmt.Errorf(
                "/proc/meminfo SwapFree value %q: %w", fields[1], err)
        }
        return kb * 1024, nil
    }
    if err := scanner.Err(); err != nil {
        return 0, fmt.Errorf("read /proc/meminfo: %w", err)
    }
    /* No SwapFree line in /proc/meminfo. Treat as zero swap,
     * not as an error: containers with swap accounting
     * disabled and the few kernels compiled without CONFIG_SWAP
     * fall into this branch legitimately.
     */
    return 0, nil
}

/* readUptimeLinux reads the first field of /proc/uptime, which
 * is the host uptime in seconds (with two decimal places). The
 * second field is the cumulative CPU idle time, which we
 * deliberately ignore - it is not what the Nodes view wants.
 */
func readUptimeLinux() (time.Duration, error) {
    data, err := os.ReadFile("/proc/uptime")
    if err != nil {
        return 0, fmt.Errorf("read /proc/uptime: %w", err)
    }
    fields := strings.Fields(string(data))
    if len(fields) == 0 {
        return 0, fmt.Errorf("/proc/uptime empty")
    }
    secs, err := strconv.ParseFloat(fields[0], 64)
    if err != nil {
        return 0, fmt.Errorf(
            "/proc/uptime parse %q: %w", fields[0], err)
    }
    if secs < 0 {
        return 0, fmt.Errorf("/proc/uptime negative: %v", secs)
    }
    return time.Duration(secs * float64(time.Second)), nil
}
