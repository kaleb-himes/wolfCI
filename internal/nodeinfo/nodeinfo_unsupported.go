//go:build !darwin && !linux

/* Fallback nodeinfo implementation for GOOSes wolfCI does not
 * officially target (windows, freebsd, openbsd, netbsd, plan9,
 * solaris, ...). The Snapshot's cheap fields are still
 * populated by Take in nodeinfo.go before platformTake is
 * called; the unsupported branch only has to refuse the
 * platform-specific reads so the binary still LINKS and
 * downstream code can switch on errors.Is(err,
 * nodeinfo.ErrUnsupported) to render "N/A" in the Nodes view.
 */
package nodeinfo

func platformTake(snap Snapshot, _ string) (Snapshot, error) {
    return snap, ErrUnsupported
}
