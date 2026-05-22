package server_test

/* Phase 14.4 - Rename project.
 *
 * POST /jobs/<old>/rename moves jobs/<old>/ -> jobs/<new>/
 * AND builds/<old>/ -> builds/<new>/. The spec's `name`
 * field is rewritten to match, so a later edit does not
 * silently revert the rename.
 *
 * Gate is requireSession today; the jobs.configure matrix
 * check is the same matrix-driven HTTP authz follow-up
 * tracked under the Phase 12.7 note.
 */

import (
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestRename_MovesSpecAndBuildHistory(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:        "oldname",
        Description: "before",
        Steps:       []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    /* Plant a build under the old name so the rename has
     * something nontrivial to move.
     */
    buildDir := filepath.Join(st.Root(), "builds", "oldname",
        "1")
    if err := os.MkdirAll(buildDir, 0o755); err != nil {
        t.Fatalf("mkdir build: %v", err)
    }
    marker := filepath.Join(buildDir, "result.json")
    if err := os.WriteFile(marker,
        []byte(`{"status":"success"}`), 0o644); err != nil {
        t.Fatalf("write marker: %v", err)
    }

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/oldname/rename",
        url.Values{"new_name": {"newname"}})
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }
    if loc := resp.Header.Get("Location"); loc != "/jobs/newname" {
        t.Errorf("Location = %q, want /jobs/newname", loc)
    }

    /* The new name resolves; the old name does not. */
    if _, err := st.LoadJob("newname"); err != nil {
        t.Errorf("LoadJob(newname): %v", err)
    }
    if _, err := st.LoadJob("oldname"); err == nil {
        t.Errorf("LoadJob(oldname) still succeeds after rename")
    }

    /* Spec's `name` field updated to match. */
    if got, err := st.LoadJob("newname"); err == nil {
        if got.Name != "newname" {
            t.Errorf("renamed spec Name = %q, want newname",
                got.Name)
        }
    }

    /* Build history moved with the spec. */
    movedMarker := filepath.Join(st.Root(), "builds", "newname",
        "1", "result.json")
    if _, err := os.Stat(movedMarker); err != nil {
        t.Errorf("history not at builds/newname/1: %v", err)
    }
    oldDir := filepath.Join(st.Root(), "builds", "oldname")
    if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
        t.Errorf("builds/oldname still present: %v", err)
    }
}

func TestRename_RejectsExistingTargetName(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "src",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob src: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:  "dst",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob dst: %v", err)
    }

    resp := mustPostForm(t, client,
        ts.URL+"/jobs/src/rename",
        url.Values{"new_name": {"dst"}})
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusConflict {
        t.Errorf("status = %d, want 409", resp.StatusCode)
    }
    if !strings.Contains(strings.ToLower(body), "already exists") {
        t.Errorf("body missing 'already exists': %q", body)
    }

    /* Both original specs untouched. */
    if _, err := st.LoadJob("src"); err != nil {
        t.Errorf("LoadJob(src) failed after refused rename: %v",
            err)
    }
    if _, err := st.LoadJob("dst"); err != nil {
        t.Errorf("LoadJob(dst) failed after refused rename: %v",
            err)
    }
}

func TestRename_RequiresJobsConfigurePermission(t *testing.T) {
    /* The matrix-driven HTTP authz is not wired into
     * requireSession yet. We assert what we DO enforce:
     * anon POST is bounced to /, neither spec moves.
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

    anon := &http.Client{
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := anon.PostForm(
        ts.URL+"/jobs/guarded/rename",
        url.Values{"new_name": {"sneaky"}})
    if err != nil {
        t.Fatalf("anon POST: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther &&
        resp.StatusCode != http.StatusFound {
        t.Errorf("anon rename status = %d, want a 3xx",
            resp.StatusCode)
    }
    if _, err := st.LoadJob("guarded"); err != nil {
        t.Errorf("anon rename moved the spec: %v", err)
    }
    if _, err := st.LoadJob("sneaky"); err == nil {
        t.Errorf("anon rename created the target")
    }
}
