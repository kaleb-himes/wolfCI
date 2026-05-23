package ghprb_test

/* internal/ghprb/state_test.go - PLAN.md 18.7 gating test.
 *
 * Polls a fake GitHub server three times with the same head sha
 * and asserts that only the first poll emits an event. A second
 * fake-server fixture then advances the head sha; the next poll
 * re-emits. State persists to disk so a fresh State loaded from
 * the same path remembers the previous emission.
 */

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "path/filepath"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
)

func TestPoller_DebounceSameSHA(t *testing.T) {
    var headSHA = "deadbeef"
    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            fmt.Fprintf(w, `[
              {
                "number": 1,
                "state": "open",
                "head": { "sha": "%s" },
                "user": { "login": "octocat" },
                "base": { "ref": "master" }
              }
            ]`, headSHA)
        }))
    defer srv.Close()

    statePath := filepath.Join(t.TempDir(),
        "ghprb-state", "my-job.yaml")

    poller := ghprb.New(srv.URL, "t", "o", "r", srv.Client())
    st, err := ghprb.LoadState(statePath)
    if err != nil {
        t.Fatalf("LoadState (initial): %v", err)
    }

    /* First poll: fires once. */
    events, err := poller.PollWithState(
        context.Background(), st)
    if err != nil {
        t.Fatalf("Poll 1: %v", err)
    }
    if len(events) != 1 || events[0].PRID != 1 {
        t.Fatalf("Poll 1 events = %+v, want one PRID=1", events)
    }
    if err := st.Save(statePath); err != nil {
        t.Fatalf("Save: %v", err)
    }

    /* Second poll: same head sha; emits nothing. */
    events, err = poller.PollWithState(
        context.Background(), st)
    if err != nil {
        t.Fatalf("Poll 2: %v", err)
    }
    if len(events) != 0 {
        t.Errorf("Poll 2 events = %+v, want zero", events)
    }

    /* Third poll: still the same head sha; still nothing. */
    events, err = poller.PollWithState(
        context.Background(), st)
    if err != nil {
        t.Fatalf("Poll 3: %v", err)
    }
    if len(events) != 0 {
        t.Errorf("Poll 3 events = %+v, want zero", events)
    }

    /* The PR gets a new commit; head sha changes. The next
     * poll re-emits.
     */
    headSHA = "cafebabe"
    events, err = poller.PollWithState(
        context.Background(), st)
    if err != nil {
        t.Fatalf("Poll 4: %v", err)
    }
    if len(events) != 1 || events[0].HeadSHA != "cafebabe" {
        t.Fatalf("Poll 4 events = %+v, want one head=cafebabe",
            events)
    }
    if err := st.Save(statePath); err != nil {
        t.Fatalf("Save 2: %v", err)
    }

    /* Reload state from disk into a fresh struct; the new
     * poller must remember the cafebabe firing and emit nothing
     * on a repeat.
     */
    reloaded, err := ghprb.LoadState(statePath)
    if err != nil {
        t.Fatalf("LoadState (reload): %v", err)
    }
    events, err = poller.PollWithState(
        context.Background(), reloaded)
    if err != nil {
        t.Fatalf("Poll after reload: %v", err)
    }
    if len(events) != 0 {
        t.Errorf("post-reload events = %+v, want zero "+
            "(state did not survive restart)", events)
    }
}

func TestLoadState_MissingFileIsEmpty(t *testing.T) {
    /* A missing state file must not error; it is the
     * "first run" condition.
     */
    path := filepath.Join(t.TempDir(), "never-created.yaml")
    st, err := ghprb.LoadState(path)
    if err != nil {
        t.Fatalf("LoadState: %v", err)
    }
    if st == nil {
        t.Fatal("State nil")
    }
    /* Filter on an empty state should let every event through. */
    in := []ghprb.TriggerEvent{
        {PRID: 1, HeadSHA: "a"},
        {PRID: 2, HeadSHA: "b"},
    }
    out := st.Filter(in)
    if len(out) != 2 {
        t.Errorf("empty state filtered %d events, want 2",
            len(out))
    }
}
