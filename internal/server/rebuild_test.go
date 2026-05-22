package server_test

/* Phase 14.3 - Rebuild Last + Rebuild With Current Spec.
 *
 * Each Enqueue snapshots the live spec into
 * builds/<job>/<n>/spec.yaml so a future rebuild knows what
 * THIS build saw, not whatever shape the spec drifted into
 * since. POST /jobs/<name>/rebuild loads the snapshot of the
 * most-recent build and re-enqueues from it. POST
 * /jobs/<name>/rebuild?spec=current explicitly ignores the
 * snapshot and uses the live spec instead.
 */

import (
    "net/http"
    "net/http/cookiejar"
    "net/http/httptest"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "sync"
    "testing"
    "time"

    "gopkg.in/yaml.v3"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestRebuild_UsesSnapshottedSpec(t *testing.T) {
    /* Snapshot has shell="echo SNAPSHOT", live spec has
     * shell="echo LIVE". POST /jobs/<name>/rebuild must
     * enqueue with the SNAPSHOT shell.
     */
    ts, jar, recorder := newServerWithRecordingRunner(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "rj",
        Steps: []storage.Step{{Shell: "echo LIVE"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    /* Seed an existing build #1 with a snapshot that has
     * a divergent step. We do not go through the
     * scheduler here; the rebuild handler reads the
     * snapshot regardless of who wrote it.
     */
    seedSpecSnapshot(t, st.Root(), "rj", 1, &storage.Job{
        Name:  "rj",
        Steps: []storage.Step{{Shell: "echo SNAPSHOT"}},
    }, time.Now())

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/rj/rebuild", nil)
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }

    jobs := recorder.calls()
    if len(jobs) != 1 {
        t.Fatalf("recorder calls = %d, want 1", len(jobs))
    }
    got := jobs[0]
    if len(got.Steps) != 1 ||
        got.Steps[0].Shell != "echo SNAPSHOT" {
        t.Errorf("enqueued shell = %q, want %q "+
            "(rebuild must use the snapshot, not the live spec)",
            got.Steps[0].Shell, "echo SNAPSHOT")
    }
}

func TestRebuild_CurrentSpecVariantUsesLiveSpec(t *testing.T) {
    ts, jar, recorder := newServerWithRecordingRunner(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "rj2",
        Steps: []storage.Step{{Shell: "echo LIVE"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    seedSpecSnapshot(t, st.Root(), "rj2", 1, &storage.Job{
        Name:  "rj2",
        Steps: []storage.Step{{Shell: "echo OLD"}},
    }, time.Now())

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/rj2/rebuild?spec=current", nil)
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }
    jobs := recorder.calls()
    if len(jobs) != 1 {
        t.Fatalf("recorder calls = %d, want 1", len(jobs))
    }
    if jobs[0].Steps[0].Shell != "echo LIVE" {
        t.Errorf("enqueued shell = %q, want %q "+
            "(spec=current must override the snapshot)",
            jobs[0].Steps[0].Shell, "echo LIVE")
    }
}

func TestRebuild_RequiresJobsBuildPermission(t *testing.T) {
    /* Like 13.4, the matrix-driven HTTP authz is not
     * wired into requireSession yet. We assert what we
     * DO enforce: anonymous POST is bounced to /, the
     * recorder sees no enqueue.
     */
    ts, _, recorder := newServerWithRecordingRunner(t)
    defer ts.Close()
    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "rj3",
        Steps: []storage.Step{{Shell: "echo X"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    seedSpecSnapshot(t, st.Root(), "rj3", 1, &storage.Job{
        Name:  "rj3",
        Steps: []storage.Step{{Shell: "echo X"}},
    }, time.Now())

    anon := &http.Client{
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := anon.PostForm(ts.URL+"/jobs/rj3/rebuild", nil)
    if err != nil {
        t.Fatalf("anon POST: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther &&
        resp.StatusCode != http.StatusFound {
        t.Errorf("anon rebuild status = %d, want a 3xx",
            resp.StatusCode)
    }
    if len(recorder.calls()) != 0 {
        t.Errorf("recorder saw %d enqueues from an anon "+
            "request; want 0", len(recorder.calls()))
    }
}

/* recordingRunner is a JobRunner that records every job it
 * sees. Tests assert on the recorded sequence after a
 * handler call.
 */
type recordingRunner struct {
    mu      sync.Mutex
    jobs    []*storage.Job
    nextNum int
}

func (r *recordingRunner) Enqueue(j *storage.Job) (int, error) {
    r.mu.Lock()
    defer r.mu.Unlock()
    cp := *j
    r.jobs = append(r.jobs, &cp)
    r.nextNum++
    return r.nextNum, nil
}

func (r *recordingRunner) calls() []*storage.Job {
    r.mu.Lock()
    defer r.mu.Unlock()
    out := make([]*storage.Job, len(r.jobs))
    copy(out, r.jobs)
    return out
}

/* newServerWithRecordingRunner builds a logged-in test
 * server with a JobRunner that records every Enqueue. Used
 * by the rebuild tests to assert exactly which spec the
 * handler enqueued.
 */
func newServerWithRecordingRunner(t *testing.T) (
    *httptest.Server, http.CookieJar, *recordingRunner) {

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

    rec := &recordingRunner{nextNum: 1}
    srv := server.New(server.Options{
        Storage:      st,
        Auth:         authCfg,
        Passwords:    pwStore,
        Sessions:     sessions,
        CookieSecure: false,
        JobRunner:    rec,
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
    return ts, jar, rec
}

/* seedSpecSnapshot writes builds/<jobName>/<num>/spec.yaml
 * with the given job spec and sets the build dir's mtime so
 * the "most-recent build" lookup in the rebuild handler
 * picks it up. Independent of scheduler.Enqueue so the
 * rebuild tests stay focused on the handler decision.
 */
func seedSpecSnapshot(t *testing.T, root, jobName string,
    num int, spec *storage.Job, when time.Time) {

    t.Helper()
    dir := filepath.Join(root, "builds", jobName,
        strconv.Itoa(num))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    data, err := yaml.Marshal(spec)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    p := filepath.Join(dir, "spec.yaml")
    if err := os.WriteFile(p, data, 0o644); err != nil {
        t.Fatalf("write spec.yaml: %v", err)
    }
    /* Also drop a minimal result.json so the rebuild
     * handler's most-recent lookup (which uses the same
     * scanAllBuilds as the detail page) treats this build
     * as completed.
     */
    rp := filepath.Join(dir, "result.json")
    if err := os.WriteFile(rp,
        []byte(`{"status":"success"}`), 0o644); err != nil {
        t.Fatalf("write result.json: %v", err)
    }
    if err := os.Chtimes(rp, when, when); err != nil {
        t.Fatalf("chtimes result.json: %v", err)
    }
    if err := os.Chtimes(dir, when, when); err != nil {
        t.Fatalf("chtimes dir: %v", err)
    }
}
