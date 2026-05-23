package ghprb

/* internal/ghprb/state.go - PLAN.md 18.7 poller debounce.
 *
 * State tracks the most-recent HeadSHA fired for each open PR
 * so repeated polls do not re-emit the same TriggerEvent. State
 * is persisted to a YAML file under
 * config-files/ghprb-state/<job_id>.yaml; LoadState tolerates a
 * missing file as the first-run condition, and Save creates
 * intermediate directories.
 *
 * State.Filter is the side-effecting dedup: it returns the
 * subset of events whose (PRID, HeadSHA) differs from the
 * stored value AND records the new HeadSHA for each event it
 * lets through. The split between Filter (in-memory) and
 * Save (on-disk) means tests can exercise the dedup without
 * touching the filesystem on every poll.
 */

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "gopkg.in/yaml.v3"
)

/* State is the debounce ledger. The yaml shape is
 *
 *   entries:
 *     "1": deadbeef
 *     "2": cafebabe
 *
 * where the map key is the PR id (rendered as a string because
 * YAML 1.2's mapping keys are typically strings) and the value
 * is the last HeadSHA the poller emitted for that PR.
 */
type State struct {
    Entries map[string]string `yaml:"entries"`
}

/* LoadState reads path into a State. A missing file is the
 * empty State and a nil error; any other I/O or parse failure
 * propagates so the caller does not silently lose dedup
 * history.
 */
func LoadState(path string) (*State, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return &State{Entries: map[string]string{}}, nil
        }
        return nil, fmt.Errorf("ghprb.LoadState: %w", err)
    }
    st := &State{}
    if err := yaml.Unmarshal(data, st); err != nil {
        return nil, fmt.Errorf("ghprb.LoadState: parse %s: %w",
            path, err)
    }
    if st.Entries == nil {
        st.Entries = map[string]string{}
    }
    return st, nil
}

/* Save serializes s to path, creating intermediate directories.
 * Atomicity comes from a temp-file-plus-rename so a crash
 * mid-write cannot corrupt the ledger.
 */
func (s *State) Save(path string) error {
    data, err := yaml.Marshal(s)
    if err != nil {
        return fmt.Errorf("ghprb.State.Save: marshal: %w", err)
    }
    if err := os.MkdirAll(filepath.Dir(path),
        0o755); err != nil {
        return fmt.Errorf("ghprb.State.Save: mkdir: %w", err)
    }
    f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
    if err != nil {
        return fmt.Errorf("ghprb.State.Save: tempfile: %w", err)
    }
    tmpPath := f.Name()
    if _, err := f.Write(data); err != nil {
        _ = f.Close()
        _ = os.Remove(tmpPath)
        return fmt.Errorf("ghprb.State.Save: write: %w", err)
    }
    if err := f.Close(); err != nil {
        _ = os.Remove(tmpPath)
        return fmt.Errorf("ghprb.State.Save: close: %w", err)
    }
    return os.Rename(tmpPath, path)
}

/* Filter returns the subset of events whose (PRID, HeadSHA) is
 * new relative to s. Side effect: every event Filter lets
 * through is recorded in s so a subsequent Filter call with the
 * same event is a no-op.
 *
 * Filter does NOT call Save - the caller is responsible for
 * persisting the state once a polling cycle has finished
 * successfully (so a transient write failure cannot truncate
 * the ledger mid-emit).
 */
func (s *State) Filter(events []TriggerEvent) []TriggerEvent {
    if s.Entries == nil {
        s.Entries = map[string]string{}
    }
    out := make([]TriggerEvent, 0, len(events))
    for _, e := range events {
        key := fmt.Sprintf("%d", e.PRID)
        if prev, ok := s.Entries[key]; ok && prev == e.HeadSHA {
            continue
        }
        s.Entries[key] = e.HeadSHA
        out = append(out, e)
    }
    return out
}

/* PollWithState wraps Poll with Filter: fetches the current
 * open PR list, drops anything whose HeadSHA matches the
 * previously stored value for that PR, and records the new
 * HeadSHA for each event it returns. Callers persist by
 * calling st.Save(path) after a successful poll.
 */
func (p *Poller) PollWithState(ctx context.Context,
    st *State) ([]TriggerEvent, error) {

    events, err := p.Poll(ctx)
    if err != nil {
        return nil, err
    }
    return st.Filter(events), nil
}
