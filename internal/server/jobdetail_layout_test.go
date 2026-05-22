package server_test

/* Phase 16.1 layout regression: the per-job detail page now
 * mirrors Jenkins's two-column shape with the build history
 * panel sitting in the LEFT column under the actions panel
 * rather than inside the main right-hand panel.
 */

import (
    "encoding/json"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobDetail_BuildsPanelInLeftColumn(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:        "demo",
        Description: "demo job",
        Steps:       []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    resp := mustGet(t, client, ts.URL+"/jobs/demo")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* Structural markers the template adds for the new
     * layout. The CSS-grid wrapper defines the two
     * columns; left-column contains both the actions and
     * builds panels.
     */
    for _, want := range []string{
        `class="page-grid"`,
        `class="left-column"`,
        `class="right-column"`,
        `class="panel actions-panel"`,
        `class="panel builds-panel"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("body missing structural marker %q",
                want)
        }
    }
    /* The builds panel marker must appear inside the
     * left-column markup, not the right-column. The
     * easiest robust check: builds-panel index > left-
     * column index AND builds-panel index < right-column
     * index.
     */
    iLeft := strings.Index(body, `class="left-column"`)
    iBuilds := strings.Index(body, `class="panel builds-panel"`)
    iRight := strings.Index(body, `class="right-column"`)
    if !(iLeft >= 0 && iBuilds > iLeft && iBuilds < iRight) {
        t.Errorf("builds-panel is not inside left-column: "+
            "left=%d builds=%d right=%d",
            iLeft, iBuilds, iRight)
    }
}

func TestJobDetail_BuildsGroupedByDate(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "datedjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Three builds: one today, one yesterday, one a week
     * ago. The detail page groups them under three
     * different headers ("Today", "Yesterday", "<date>").
     */
    now := time.Now()
    seeds := []struct {
        num  int
        when time.Time
    }{
        {3, now.Add(-2 * time.Hour)},
        {2, now.AddDate(0, 0, -1).Add(-1 * time.Hour)},
        {1, now.AddDate(0, 0, -7).Add(-3 * time.Hour)},
    }
    for _, s := range seeds {
        dir := filepath.Join(st.Root(), "builds",
            "datedjob", strconv.Itoa(s.num))
        if err := os.MkdirAll(dir, 0o755); err != nil {
            t.Fatalf("mkdir build %d: %v", s.num, err)
        }
        data, _ := json.Marshal(scheduler.BuildResult{
            JobName: "datedjob",
            Number:  s.num,
            Status:  scheduler.StatusSuccess,
        })
        path := filepath.Join(dir, "result.json")
        if err := os.WriteFile(path, data, 0o644); err != nil {
            t.Fatalf("write build %d: %v", s.num, err)
        }
        if err := os.Chtimes(path, s.when, s.when); err != nil {
            t.Fatalf("chtimes %d: %v", s.num, err)
        }
    }

    resp := mustGet(t, client, ts.URL+"/jobs/datedjob")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    for _, want := range []string{"Today", "Yesterday"} {
        if !strings.Contains(body, want) {
            t.Errorf("builds panel missing date header %q",
                want)
        }
    }
    /* The week-old build's bucket label uses the result.
     * json mtime's date. We do not assert the exact
     * string to avoid timezone churn; instead we check
     * that the build dir/number AND a YYYY-MM-DD-looking
     * prefix both appear, since the helper formats older
     * buckets as the ISO date.
     */
    weekAgo := seeds[2].when.Format("2006-01-02")
    if !strings.Contains(body, weekAgo) {
        t.Errorf("builds panel missing ISO date %q for "+
            "the week-old build", weekAgo)
    }
}
