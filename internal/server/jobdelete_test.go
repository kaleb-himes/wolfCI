package server_test

/* Phase 13.4 - Delete project action.
 *
 * POST /jobs/<name>/delete removes jobs/<name>/job.yaml (the
 * spec) and the rest of jobs/<name>/, but leaves builds/<name>/
 * in place so the operator can re-create the job under the
 * same name and the history is still there. A separate "wipe
 * history too" affordance lands as a backlog item; this Phase
 * 13 step intentionally keeps it small.
 *
 * Permission gate today is requireSession (the same gate
 * /jobs/<name>/edit uses); the jobs.configure permission name
 * is reserved for the matrix-driven HTTP authz follow-up
 * tracked under the Phase 12.7 note. The test below verifies
 * what we can actually enforce now: anonymous callers are
 * denied.
 */

import (
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobDelete_RemovesSpec(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "doomed",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Verify the spec is on disk before the delete. */
    specPath := filepath.Join(st.Root(), "jobs", "doomed",
        "job.yaml")
    if _, err := os.Stat(specPath); err != nil {
        t.Fatalf("spec not present before delete: %v", err)
    }

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/doomed/delete", nil)
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }
    if loc := resp.Header.Get("Location"); loc != "/jobs" {
        t.Errorf("Location = %q, want /jobs", loc)
    }

    /* Spec is gone; LoadJob returns an error. */
    if _, err := os.Stat(specPath); !os.IsNotExist(err) {
        t.Errorf("spec still present after delete: stat err = %v",
            err)
    }
    if _, err := st.LoadJob("doomed"); err == nil {
        t.Errorf("LoadJob succeeded after delete")
    }
    /* The job directory itself is gone too. */
    jobDir := filepath.Join(st.Root(), "jobs", "doomed")
    if _, err := os.Stat(jobDir); !os.IsNotExist(err) {
        t.Errorf("jobs/doomed/ still present: stat err = %v",
            err)
    }
}

func TestJobDelete_KeepsBuildHistory(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "keepme",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Drop a synthetic build directory with a marker file.
     * Delete must not touch this side of the tree.
     */
    buildDir := filepath.Join(st.Root(), "builds", "keepme",
        "1")
    if err := os.MkdirAll(buildDir, 0o755); err != nil {
        t.Fatalf("mkdir build dir: %v", err)
    }
    marker := filepath.Join(buildDir, "result.json")
    if err := os.WriteFile(marker,
        []byte(`{"status":"success"}`), 0o644); err != nil {
        t.Fatalf("write marker: %v", err)
    }

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/keepme/delete", nil)
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }

    /* builds/keepme/1/result.json must still be present. */
    if _, err := os.Stat(marker); err != nil {
        t.Errorf("build history wiped along with the spec: %v",
            err)
    }
    /* And jobs/keepme is gone. */
    if _, err := st.LoadJob("keepme"); err == nil {
        t.Errorf("LoadJob succeeded after delete")
    }
}

func TestJobDelete_RequiresConfigurePermission(t *testing.T) {
    /* The matrix-driven HTTP authz is not wired into
     * requireSession yet (see the Phase 12.7 follow-up note).
     * For now we assert the gate we DO have: anonymous
     * requests are bounced to the login page, and the spec
     * is left intact.
     */
    ts, _ := newAuthedUI(t)
    defer ts.Close()

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "guarded",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    specPath := filepath.Join(st.Root(), "jobs", "guarded",
        "job.yaml")

    /* No cookie jar -> no session. */
    anonClient := &http.Client{
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := anonClient.PostForm(
        ts.URL+"/jobs/guarded/delete", nil)
    if err != nil {
        t.Fatalf("anon POST: %v", err)
    }
    resp.Body.Close()
    /* requireSession redirects unauthenticated callers to /. */
    if resp.StatusCode != http.StatusSeeOther &&
        resp.StatusCode != http.StatusFound {
        t.Errorf("anon delete status = %d, want a 3xx redirect",
            resp.StatusCode)
    }
    if loc := resp.Header.Get("Location"); !strings.HasPrefix(
        loc, "/") {
        t.Errorf("anon delete Location = %q, want /", loc)
    }
    /* Spec must be untouched. */
    if _, err := os.Stat(specPath); err != nil {
        t.Errorf("anon delete erased the spec: %v", err)
    }
}
