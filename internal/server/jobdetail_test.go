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
