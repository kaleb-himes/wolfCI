package server_test

/* Regression test: the jobs page exposes a Run button when a
 * JobRunner is wired; POST /jobs/<name>/run enqueues and
 * redirects to the live build-log page.
 */

import (
    "net/http"
    "net/http/cookiejar"
    "net/http/httptest"
    "net/url"
    "path/filepath"
    "strings"
    "sync/atomic"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestUI_JobRunEnqueuesAndRedirects(t *testing.T) {
    dir := t.TempDir()
    st, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "wolfssl-build",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    authCfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: 1000}
    pwStore := auth.NewPasswordStore(filepath.Join(dir, "passwords"), authCfg)
    if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
        t.Fatalf("SetPassword: %v", err)
    }
    sessions := server.NewSessionStore(filepath.Join(dir, "sessions"), time.Hour)

    var runnerCalls int32
    var calledJob string
    fakeRunner := server.JobRunnerFunc(func(job *storage.Job) (int, error) {
        atomic.AddInt32(&runnerCalls, 1)
        calledJob = job.Name
        return 7, nil
    })

    srv := server.New(server.Options{
        Storage:      st,
        Auth:         authCfg,
        Passwords:    pwStore,
        Sessions:     sessions,
        CookieSecure: false,
        JobRunner:    fakeRunner,
    })

    ts := httptest.NewServer(srv)
    defer ts.Close()

    jar, _ := cookiejar.New(nil)
    client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
        return http.ErrUseLastResponse
    }}

    /* Sign in. */
    resp, err := client.PostForm(ts.URL+"/login", url.Values{
        "username": {"alice"},
        "password": {"hunter2"},
    })
    if err != nil {
        t.Fatalf("POST /login: %v", err)
    }
    resp.Body.Close()

    /* Jobs page should now include the Run form. */
    resp, err = client.Get(ts.URL + "/jobs")
    if err != nil {
        t.Fatalf("GET /jobs: %v", err)
    }
    body, _ := readAllString(resp.Body)
    resp.Body.Close()
    if !strings.Contains(body, `action="/jobs/wolfssl-build/run"`) {
        t.Errorf("/jobs body missing Run form: %q", body)
    }

    /* POST the Run form. */
    resp, err = client.PostForm(ts.URL+"/jobs/wolfssl-build/run", nil)
    if err != nil {
        t.Fatalf("POST /jobs/wolfssl-build/run: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("POST run status = %d, want 303", resp.StatusCode)
    }
    if loc := resp.Header.Get("Location"); loc != "/jobs/wolfssl-build/builds/7" {
        t.Errorf("Location = %q, want /jobs/wolfssl-build/builds/7", loc)
    }
    if atomic.LoadInt32(&runnerCalls) != 1 {
        t.Errorf("runnerCalls = %d, want 1", runnerCalls)
    }
    if calledJob != "wolfssl-build" {
        t.Errorf("calledJob = %q, want wolfssl-build", calledJob)
    }
}

func TestUI_JobRunReturns501WhenRunnerNotWired(t *testing.T) {
    dir := t.TempDir()
    st, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "wolfssl-build",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    authCfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: 1000}
    pwStore := auth.NewPasswordStore(filepath.Join(dir, "passwords"), authCfg)
    if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
        t.Fatalf("SetPassword: %v", err)
    }
    sessions := server.NewSessionStore(filepath.Join(dir, "sessions"), time.Hour)

    /* No JobRunner wired - the Run column should be hidden and the
     * POST endpoint should refuse with 501.
     */
    srv := server.New(server.Options{
        Storage:      st,
        Auth:         authCfg,
        Passwords:    pwStore,
        Sessions:     sessions,
        CookieSecure: false,
    })
    ts := httptest.NewServer(srv)
    defer ts.Close()

    jar, _ := cookiejar.New(nil)
    client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
        return http.ErrUseLastResponse
    }}
    resp, _ := client.PostForm(ts.URL+"/login", url.Values{
        "username": {"alice"}, "password": {"hunter2"},
    })
    resp.Body.Close()

    /* Jobs page hides the Run form. */
    resp, _ = client.Get(ts.URL + "/jobs")
    body, _ := readAllString(resp.Body)
    resp.Body.Close()
    if strings.Contains(body, `action="/jobs/wolfssl-build/run"`) {
        t.Errorf("Run form rendered without a JobRunner: %q", body)
    }

    /* POST is rejected. */
    resp, _ = client.PostForm(ts.URL+"/jobs/wolfssl-build/run", nil)
    resp.Body.Close()
    if resp.StatusCode != http.StatusNotImplemented {
        t.Errorf("POST run status = %d, want 501", resp.StatusCode)
    }
}

func readAllString(r interface{ Read([]byte) (int, error) }) (string, error) {
    buf := make([]byte, 0, 4096)
    tmp := make([]byte, 4096)
    for {
        n, err := r.Read(tmp)
        if n > 0 {
            buf = append(buf, tmp[:n]...)
        }
        if err != nil {
            if err.Error() == "EOF" {
                return string(buf), nil
            }
            return string(buf), err
        }
    }
}
