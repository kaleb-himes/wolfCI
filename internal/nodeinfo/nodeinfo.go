/* Package nodeinfo collects portable host metrics for the wolfCI
 * Nodes view: architecture, Go runtime version, free disk / swap
 * / temp space, host uptime, and wall clock at snapshot. Per-OS
 * internals live in nodeinfo_darwin.go and nodeinfo_linux.go;
 * other GOOSes link via nodeinfo_unsupported.go and return
 * ErrUnsupported. Phase 12.2 will marshal Snapshot into a gRPC
 * NodeStatus message the agent sends on its Connect heartbeat.
 */
package nodeinfo

import (
    "errors"
    "runtime"
    "time"
)

/* ErrUnsupported is returned by Take on a GOOS that has no
 * nodeinfo implementation linked in (anything other than darwin
 * or linux today). The error is sentinel so callers can switch
 * to a "metrics unavailable" UI branch via errors.Is.
 */
var ErrUnsupported = errors.New("nodeinfo: unsupported GOOS")

/* Snapshot is the host-metrics surface the Nodes view renders.
 * Fields are independently fillable: a partial read (e.g. a
 * statfs failure on root) still populates the cheap fields
 * (Architecture, GoVersion, Now) so the UI can show "node up
 * but disk metric unavailable" rather than dropping the whole
 * row. Zero values mean "the underlying source could not be
 * read"; check err first.
 */
type Snapshot struct {
    /* Architecture is the host's GOOS/GOARCH string, e.g.
     * "darwin/arm64", "linux/amd64". Used as a column in the
     * Nodes view and as a filter when the Phase 5 router
     * decides which jobs can land on which agents.
     */
    Architecture string

    /* GoVersion is runtime.Version() for the running binary.
     * wolfCI uses this in place of Jenkins's "JVM Version"
     * column; on the master row it identifies the server's
     * Go runtime, on agent rows the agent's runtime.
     */
    GoVersion string

    /* FreeDiskBytes is statfs Bavail*Bsize on the configured
     * root (typically the wolfCI work directory). 0 means the
     * statfs failed; the accompanying error has the details.
     */
    FreeDiskBytes int64

    /* FreeSwapBytes is the OS-reported free swap space. 0 is
     * legitimate (swap disabled); negative would indicate a
     * parse bug.
     */
    FreeSwapBytes int64

    /* FreeTempBytes is statfs Bavail*Bsize on os.TempDir().
     * The agent uses this to refuse new builds when the temp
     * partition is near full, which is one of Jenkins's
     * common silent-failure modes.
     */
    FreeTempBytes int64

    /* HostUptime is the duration since the host booted. The
     * Nodes view renders this as a human-readable "up 5d 2h".
     */
    HostUptime time.Duration

    /* Now is time.Now() at the moment Take returned. The
     * server compares Now to its own clock to compute the
     * per-node clock-difference column (Phase 12.6).
     */
    Now time.Time
}

/* Take returns a Snapshot of the host's current state. root is
 * the path the FreeDiskBytes statfs targets (typically the
 * wolfCI work directory). On error the Snapshot still carries
 * whatever fields could be filled in before the failure -
 * Architecture, GoVersion, and Now are always populated because
 * they cost nothing to read.
 *
 * Errors:
 *   - ErrUnsupported on a GOOS without a nodeinfo implementation.
 *   - any error from statfs on root or os.TempDir.
 *   - any error from the platform-specific swap/uptime reader.
 *
 * Take does not retry; the agent's heartbeat loop in Phase 12.3
 * decides how often to call it.
 */
func Take(root string) (Snapshot, error) {
    snap := Snapshot{
        Architecture: runtime.GOOS + "/" + runtime.GOARCH,
        GoVersion:    runtime.Version(),
        Now:          time.Now(),
    }
    return platformTake(snap, root)
}
