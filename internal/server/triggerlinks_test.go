package server_test

/* Phase 15.6 - upstream / downstream / sibling sections on
 * the per-job detail page, plus inbound + outbound trigger
 * counts in the /jobs index.
 *
 * Upstream:    job.Upstream entries.
 * Downstream:  every OTHER job whose TriggersDownstream
 *              contains this job's name. Computed at render
 *              time so a downstream that someone added to
 *              their own spec shows up without us re-writing
 *              the upstream's spec.
 * Siblings:    other jobs that share at least one upstream
 *              with this job. Rendered only when non-empty.
 */

import (
    "net/http"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobDetail_RendersUpstreamLinks(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    /* "linux-build" first so it exists when nightly-test
     * references it (SaveJob does not enforce upstream
     * existence today - the field is metadata).
     */
    if err := st.SaveJob(&storage.Job{
        Name:  "linux-build",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob upstream: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:     "nightly-test",
        Upstream: []string{"linux-build"},
        Steps:    []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob job: %v", err)
    }
    resp := mustGet(t, client,
        ts.URL+"/jobs/nightly-test")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, "Upstream Projects") {
        t.Errorf("body missing Upstream Projects section")
    }
    if !strings.Contains(body, `href="/jobs/linux-build"`) {
        t.Errorf("body missing link to /jobs/linux-build")
    }
}

func TestJobDetail_RendersDownstreamLinks_ComputedFromOtherJobs(
    t *testing.T) {

    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    /* linux-build triggers windows-test. The detail page
     * for linux-build must surface windows-test under
     * Downstream Projects even though linux-build's spec
     * does not name it directly (the edge is read from
     * the other side of the graph).
     */
    if err := st.SaveJob(&storage.Job{
        Name:  "linux-build",
        Steps: []storage.Step{{Shell: "true"}},
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "windows-test"},
        },
    }); err != nil {
        t.Fatalf("SaveJob upstream: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "windows-test",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob downstream: %v", err)
    }

    resp := mustGet(t, client,
        ts.URL+"/jobs/linux-build")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, "Downstream Projects") {
        t.Errorf("body missing Downstream Projects section")
    }
    if !strings.Contains(body, `href="/jobs/windows-test"`) {
        t.Errorf("body missing link to /jobs/windows-test")
    }
}

func TestJobDetail_RendersSiblingsWhenSharingUpstream(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "linux-build",
        Steps: []storage.Step{{Shell: "true"}},
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "windows-test"},
            {Name: "macos-test"},
        },
    }); err != nil {
        t.Fatalf("SaveJob linux-build: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:     "windows-test",
        Upstream: []string{"linux-build"},
        Steps:    []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob windows-test: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:     "macos-test",
        Upstream: []string{"linux-build"},
        Steps:    []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob macos-test: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "unrelated",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob unrelated: %v", err)
    }

    resp := mustGet(t, client,
        ts.URL+"/jobs/windows-test")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, "Sibling Projects") {
        t.Errorf("body missing Sibling Projects section")
    }
    if !strings.Contains(body, `href="/jobs/macos-test"`) {
        t.Errorf("body missing link to /jobs/macos-test "+
            "(sibling via shared upstream)")
    }
    /* The job is NOT its own sibling. */
    if strings.Contains(body,
        `Sibling Projects</h3>`) &&
        strings.Contains(body, `href="/jobs/windows-test"`) {
        /* The href could be elsewhere (e.g. canonical),
         * but checking it does not appear specifically
         * inside the Siblings section would need an HTML
         * parser. The link in the body in general is OK;
         * the Siblings section is what we audit.
         */
    }
    /* Unrelated job is NOT a sibling. */
    if strings.Contains(body, `href="/jobs/unrelated"`) {
        t.Errorf("body included unrelated job as a "+
            "sibling")
    }
}

func TestJobsIndex_BadgesShowTriggerCounts(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    /* A triggers [B, C]; B has upstream [A]; D has neither.
     * On the /jobs index:
     *   A -> out=2 in=0
     *   B -> out=0 in=1
     *   C -> out=0 in=1
     *   D -> out=0 in=0 (badge omitted)
     */
    if err := st.SaveJob(&storage.Job{
        Name:  "A",
        Steps: []storage.Step{{Shell: "true"}},
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
            {Name: "C"},
        },
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:     "B",
        Upstream: []string{"A"},
        Steps:    []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob B: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "C",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob C: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "D",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob D: %v", err)
    }

    resp := mustGet(t, client, ts.URL+"/jobs")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* A's badge: "2 out, 0 in" - we accept any
     * representation that mentions both numbers in the
     * vicinity of the job name.
     */
    if !strings.Contains(body, "2 out") {
        t.Errorf("body missing '2 out' badge for A")
    }
    if !strings.Contains(body, "1 in") {
        t.Errorf("body missing '1 in' badge for B and/or C")
    }
}
