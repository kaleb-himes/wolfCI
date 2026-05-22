package server_test

/* Phase 13.3 - paginated /jobs/<name>/builds index page.
 *
 * The detail page caps its build-history table at 100; the
 * "see all" affordance points here for the complete list.
 *
 * Contract:
 *   - 25 builds per page, newest-first.
 *   - ?page=N selects the page (1-based); out-of-range pages
 *     render an empty list, not 404.
 *   - ?since=<rfc3339> restricts to builds whose recorded
 *     activity time is >= that instant.
 *   - The footer carries Prev / Next pagination links that
 *     preserve any active ?since= filter.
 */

import (
    "fmt"
    "net/http"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestBuildsIndex_FirstPageReturnsNewest(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "bigjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Seed 30 builds with #30 newest and #1 oldest. Mtimes
     * are spaced 1 minute apart so the relative-time strings
     * vary and ordering is unambiguous.
     */
    base := time.Now().Add(-90 * time.Minute)
    for n := 1; n <= 30; n++ {
        seedBuild(t, st.Root(), "bigjob", n,
            scheduler.StatusSuccess,
            base.Add(time.Duration(n)*time.Minute), true)
    }

    resp := mustGet(t, client, ts.URL+"/jobs/bigjob/builds")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }

    /* First page shows the newest 25 (#30 .. #6). #30
     * appears before #6 and #5 does NOT appear (it bleeds
     * onto page 2).
     */
    for n := 6; n <= 30; n++ {
        want := fmt.Sprintf("#%d", n)
        if !strings.Contains(body, want) {
            t.Errorf("body missing %q", want)
        }
    }
    /* #5 .. #1 must NOT appear on page 1. We probe #5 only
     * because #1..#4 substrings could also match parts of a
     * relative timestamp like "1m ago" inside a row.
     */
    if strings.Contains(body, "#5") {
        t.Errorf("page 1 leaked #5; expected to be on page 2")
    }

    /* Ordering: #30 (newest) renders before #6. */
    i30 := strings.Index(body, "#30")
    i6 := strings.Index(body, "#6")
    if !(i30 >= 0 && i6 > i30) {
        t.Errorf("order broken: #30=%d, #6=%d", i30, i6)
    }
}

func TestBuildsIndex_RespectsSinceFilter(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "filterjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Five builds:
     *   #1 at base-4h
     *   #2 at base-3h
     *   #3 at base-2h     <- cutoff sits just after this
     *   #4 at base-1h
     *   #5 at base
     *
     * since=base-90m should yield {#5, #4} only.
     */
    base := time.Now()
    times := []time.Time{
        base.Add(-4 * time.Hour),
        base.Add(-3 * time.Hour),
        base.Add(-2 * time.Hour),
        base.Add(-1 * time.Hour),
        base,
    }
    for i, when := range times {
        seedBuild(t, st.Root(), "filterjob", i+1,
            scheduler.StatusSuccess, when, true)
    }

    cutoff := base.Add(-90 * time.Minute).UTC().
        Format(time.RFC3339)
    url := ts.URL + "/jobs/filterjob/builds?since=" + cutoff
    resp := mustGet(t, client, url)
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }

    for _, want := range []string{"#4", "#5"} {
        if !strings.Contains(body, want) {
            t.Errorf("body missing %q (should pass the filter)",
                want)
        }
    }
    /* #1..#3 are before the cutoff. We assert on the link
     * substring (rather than "#1") so a "1m ago" relative
     * timestamp in a passing row does not falsely match.
     */
    for _, suffix := range []string{
        `/jobs/filterjob/builds/1"`,
        `/jobs/filterjob/builds/2"`,
        `/jobs/filterjob/builds/3"`,
    } {
        if strings.Contains(body, suffix) {
            t.Errorf("body leaked %q past the since filter",
                suffix)
        }
    }
}

func TestBuildsIndex_PaginationLinks(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "pagedjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* 60 builds -> 3 pages of 25 + 10. */
    base := time.Now().Add(-90 * time.Minute)
    for n := 1; n <= 60; n++ {
        seedBuild(t, st.Root(), "pagedjob", n,
            scheduler.StatusSuccess,
            base.Add(time.Duration(n)*time.Minute), true)
    }

    /* Page 1: no Prev link, Next -> page 2. */
    resp := mustGet(t, client, ts.URL+"/jobs/pagedjob/builds")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("page 1 status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, `href="?page=2"`) {
        t.Errorf("page 1 missing Next link to ?page=2")
    }
    if strings.Contains(body, `href="?page=0"`) {
        t.Errorf("page 1 should not link to ?page=0")
    }

    /* Page 2: Prev -> page 1, Next -> page 3. */
    resp = mustGet(t, client, ts.URL+"/jobs/pagedjob/builds?page=2")
    body = readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("page 2 status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, `href="?page=1"`) {
        t.Errorf("page 2 missing Prev link to ?page=1")
    }
    if !strings.Contains(body, `href="?page=3"`) {
        t.Errorf("page 2 missing Next link to ?page=3")
    }

    /* Page 3 (last): Prev -> page 2, no Next link. */
    resp = mustGet(t, client, ts.URL+"/jobs/pagedjob/builds?page=3")
    body = readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("page 3 status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, `href="?page=2"`) {
        t.Errorf("page 3 missing Prev link to ?page=2")
    }
    if strings.Contains(body, `href="?page=4"`) {
        t.Errorf("page 3 should not link to ?page=4")
    }
}
