package server_test

/* Phase 13.1 - per-job detail page at /jobs/<name>.
 *
 * Today /jobs lists every job but there is no per-job page; the
 * only navigable per-job URLs are /jobs/<name>/edit (the spec
 * editor) and /jobs/<name>/builds/<n> (the live log). 13.1 adds
 * the detail page that carries:
 *
 *   - the sidebar with Build Now / Configure / Delete (Phase 13)
 *     and placeholders for Rebuild Last / Rename / Workspace
 *     (Phase 14),
 *   - <h2>{{ .Name }}</h2> and the description block,
 *   - the build-history panel (newest first, capped at 100).
 */

import (
    "encoding/json"
    "net/http"
    "net/http/cookiejar"
    "net/http/httptest"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobDetail_Returns200WithSidebar(t *testing.T) {
    /* This test needs the Build Now form, which only renders
     * when a JobRunner is wired. The shared newAuthedUI helper
     * does not wire one (it predates Phase 4's runner), so we
     * stand up a one-off server here.
     */
    ts, jar := newAuthedUIWithRunner(t,
        server.JobRunnerFunc(func(j *storage.Job) (int, error) {
            return 1, nil
        }))
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
    /* Sidebar actions shipped in Phase 13: Build Now, Configure,
     * Delete. Phase 14 adds Rebuild Last + Rename + Workspace -
     * those render as disabled placeholders today so the chrome
     * does not shift when the operational features land.
     */
    for _, want := range []string{
        `action="/jobs/demo/run"`,
        "Build Now",
        `href="/jobs/demo/edit"`,
        "Configure",
        `action="/jobs/demo/delete"`,
        "Delete",
        "Rebuild Last",
        "Rename",
        "Workspace",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("body missing %q", want)
        }
    }
}

func TestJobDetail_RendersDescription(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    desc := "builds the wolfSSL static library nightly"
    if err := st.SaveJob(&storage.Job{
        Name:        "nightly",
        Description: desc,
        Steps:       []storage.Step{{Shell: "make"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    resp := mustGet(t, client, ts.URL+"/jobs/nightly")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    if !strings.Contains(body, "<h2>nightly</h2>") {
        t.Errorf("body missing <h2>nightly</h2>")
    }
    if !strings.Contains(body, desc) {
        t.Errorf("body missing description %q", desc)
    }
}

func TestJobDetail_RendersBuildHistory_NewestFirst(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:        "histjob",
        Description: "has history",
        Steps:       []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Seed three builds: #1 success (oldest), #2 failure, #3
     * success (newest). The detail page renders newest-first.
     */
    seed := []struct {
        num    int
        status scheduler.Status
        when   time.Time
    }{
        {1, scheduler.StatusSuccess, time.Now().Add(-3 * time.Hour)},
        {2, scheduler.StatusFailure, time.Now().Add(-2 * time.Hour)},
        {3, scheduler.StatusSuccess, time.Now().Add(-1 * time.Hour)},
    }
    for _, b := range seed {
        dir := filepath.Join(st.Root(), "builds", "histjob",
            strconv.Itoa(b.num))
        if err := os.MkdirAll(dir, 0o755); err != nil {
            t.Fatalf("mkdir build %d: %v", b.num, err)
        }
        data, err := json.Marshal(scheduler.BuildResult{
            JobName: "histjob",
            Number:  b.num,
            Status:  b.status,
        })
        if err != nil {
            t.Fatalf("marshal build %d: %v", b.num, err)
        }
        path := filepath.Join(dir, "result.json")
        if err := os.WriteFile(path, data, 0o644); err != nil {
            t.Fatalf("write build %d: %v", b.num, err)
        }
        /* Force mtime so the "newest first" ordering is
         * deterministic - the handler scans the directory and
         * reads either the entry or the result.json mtime to
         * sort.
         */
        if err := os.Chtimes(path, b.when, b.when); err != nil {
            t.Fatalf("chtimes build %d: %v", b.num, err)
        }
    }

    resp := mustGet(t, client, ts.URL+"/jobs/histjob")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    for _, n := range []string{"#1", "#2", "#3"} {
        if !strings.Contains(body, n) {
            t.Errorf("body missing build label %q", n)
        }
    }
    i3 := strings.Index(body, "#3")
    i2 := strings.Index(body, "#2")
    i1 := strings.Index(body, "#1")
    if !(i3 >= 0 && i2 > i3 && i1 > i2) {
        t.Errorf("build order in body: #3=%d, #2=%d, #1=%d; "+
            "want #3 < #2 < #1", i3, i2, i1)
    }
    /* Each row should link to the live-log page. */
    if !strings.Contains(body, `href="/jobs/histjob/builds/3"`) {
        t.Errorf("body missing link to /jobs/histjob/builds/3")
    }
    /* Build #2 failed; its row should carry the failure status. */
    if !strings.Contains(body, "failure") {
        t.Errorf("body missing failure status for build #2")
    }
}

/* newAuthedUIWithRunner stands up an httptest.Server with the
 * given JobRunner wired and one signed-in "alice" session in
 * the returned cookie jar. Used by tests that need the Build
 * Now form rendered (the shared newAuthedUI in jobform_test.go
 * predates Phase 4's runner and does not wire one).
 */
func newAuthedUIWithRunner(t *testing.T,
    runner server.JobRunner) (*httptest.Server, http.CookieJar) {

    t.Helper()
    dir := t.TempDir()
    st, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    authCfg := &auth.Config{
        PasswordEnabled: true, PBKDF2Iterations: 1000,
    }
    pwStore := auth.NewPasswordStore(
        filepath.Join(dir, "passwords"), authCfg)
    if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
        t.Fatalf("SetPassword: %v", err)
    }
    sessions := server.NewSessionStore(
        filepath.Join(dir, "sessions"), time.Hour)

    srv := server.New(server.Options{
        Storage:      st,
        Auth:         authCfg,
        Passwords:    pwStore,
        Sessions:     sessions,
        CookieSecure: false,
        JobRunner:    runner,
    })
    ts := httptest.NewServer(srv)
    testStorageByURL[ts.URL] = st

    jar, _ := cookiejar.New(nil)
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := client.PostForm(ts.URL+"/login", url.Values{
        "username": {"alice"},
        "password": {"hunter2"},
    })
    if err != nil {
        ts.Close()
        t.Fatalf("login: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        ts.Close()
        t.Fatalf("login status = %d, want 303", resp.StatusCode)
    }
    return ts, jar
}

func TestJobDetail_404OnMissingJob(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    resp := mustGet(t, client, ts.URL+"/jobs/nope")
    resp.Body.Close()
    if resp.StatusCode != http.StatusNotFound {
        t.Errorf("status = %d, want 404", resp.StatusCode)
    }
}

/* Phase 13.2 - permalinks panel above the build history. The
 * header lists Last build, Last stable build, Last successful
 * build, Last unsuccessful build, Last completed build. Each
 * resolves to a /jobs/<name>/builds/<n> link or renders "none"
 * if no build matches. "Stable" == StatusSuccess for now;
 * Phase 14 tightens this once Rebuild Last lands and we can
 * distinguish a successful first try from a successful retry.
 */

func TestJobDetail_PermalinksReflectMostRecentStatuses(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "permajob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Seed five builds so each permalink resolves to a
     * different number, exercising the "most recent" rule for
     * each category. Numbers chosen so the expected links are
     * unambiguous when grepping the rendered body.
     *
     *   #1 success    (oldest)
     *   #2 failure
     *   #3 success    <- last stable, last successful
     *   #4 error      <- last unsuccessful, last completed
     *   #5 running    <- last build (no result.json yet)
     *
     * Mtimes are backdated 5h, 4h, ..., 1h so the newest-
     * first ordering is deterministic.
     */
    seedBuild(t, st.Root(), "permajob", 1, scheduler.StatusSuccess,
        time.Now().Add(-5*time.Hour), true)
    seedBuild(t, st.Root(), "permajob", 2, scheduler.StatusFailure,
        time.Now().Add(-4*time.Hour), true)
    seedBuild(t, st.Root(), "permajob", 3, scheduler.StatusSuccess,
        time.Now().Add(-3*time.Hour), true)
    seedBuild(t, st.Root(), "permajob", 4, scheduler.StatusError,
        time.Now().Add(-2*time.Hour), true)
    /* #5 has no result.json: an in-flight build. */
    seedBuild(t, st.Root(), "permajob", 5, "",
        time.Now().Add(-1*time.Hour), false)

    resp := mustGet(t, client, ts.URL+"/jobs/permajob")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }

    /* The permalinks panel must label each entry and link the
     * correct build number. We assert on substrings rather
     * than a full HTML shape so the template can choose its
     * own markup as long as the label + link are colocated.
     */
    checks := []struct {
        label string
        href  string
    }{
        {"Last build", `/jobs/permajob/builds/5`},
        {"Last stable build", `/jobs/permajob/builds/3`},
        {"Last successful build", `/jobs/permajob/builds/3`},
        {"Last unsuccessful build", `/jobs/permajob/builds/4`},
        {"Last completed build", `/jobs/permajob/builds/4`},
    }
    for _, c := range checks {
        idx := strings.Index(body, c.label)
        if idx < 0 {
            t.Errorf("body missing label %q", c.label)
            continue
        }
        /* Allow up to 200 bytes between the label and the
         * link target; that is enough for whitespace +
         * <span> + <a> wrappers without bleeding into the
         * next permalink row.
         */
        window := body[idx:]
        if len(window) > 200 {
            window = window[:200]
        }
        if !strings.Contains(window, c.href) {
            t.Errorf("permalink %q did not point at %q "+
                "within 200 bytes; got: %q",
                c.label, c.href, window)
        }
    }
}

func TestJobDetail_PermalinksHandlesNoBuilds(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "freshjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    resp := mustGet(t, client, ts.URL+"/jobs/freshjob")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }

    /* Every permalink label still renders, but each one shows
     * "none" instead of a link.
     */
    for _, label := range []string{
        "Last build",
        "Last stable build",
        "Last successful build",
        "Last unsuccessful build",
        "Last completed build",
    } {
        idx := strings.Index(body, label)
        if idx < 0 {
            t.Errorf("body missing label %q", label)
            continue
        }
        window := body[idx:]
        if len(window) > 200 {
            window = window[:200]
        }
        if !strings.Contains(window, "none") {
            t.Errorf("permalink %q without builds did not "+
                "render 'none'; got: %q", label, window)
        }
        /* Negative check: no <a href="/jobs/freshjob/builds/
         * should appear inside the window.
         */
        if strings.Contains(window,
            `href="/jobs/freshjob/builds/`) {
            t.Errorf("permalink %q rendered a build link "+
                "even though no builds exist; got: %q",
                label, window)
        }
    }
}

/* seedBuild writes a result.json (or an empty build dir if
 * withResult=false) under builds/<jobName>/<num>/ and force-
 * sets the mtime so the build's ordering is deterministic in
 * the rendered page.
 *
 * withResult=false simulates an in-flight build whose
 * executor has not written result.json yet; the detail page
 * still surfaces it as "running" in the build history and as
 * the Last build permalink, but it should NOT count as
 * Last completed / Last successful / Last unsuccessful.
 */
func seedBuild(t *testing.T, root, jobName string, num int,
    status scheduler.Status, when time.Time, withResult bool) {

    t.Helper()
    dir := filepath.Join(root, "builds", jobName,
        strconv.Itoa(num))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        t.Fatalf("mkdir build %d: %v", num, err)
    }
    target := dir
    if withResult {
        data, err := json.Marshal(scheduler.BuildResult{
            JobName: jobName,
            Number:  num,
            Status:  status,
        })
        if err != nil {
            t.Fatalf("marshal build %d: %v", num, err)
        }
        target = filepath.Join(dir, "result.json")
        if err := os.WriteFile(target, data, 0o644); err != nil {
            t.Fatalf("write build %d: %v", num, err)
        }
    }
    if err := os.Chtimes(target, when, when); err != nil {
        t.Fatalf("chtimes build %d: %v", num, err)
    }
}
