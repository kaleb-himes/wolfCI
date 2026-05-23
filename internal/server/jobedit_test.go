package server_test

/* internal/server/jobedit_test.go - PLAN.md 18.27 gating
 * test.
 *
 * TestJobEdit_GeneralOptionsRoundtrip POSTs a job-create form
 * with the "General options" block from 18.27 (description,
 * discard_old_builds.strategy / .max_builds / .days_to_keep,
 * github_project_url) and asserts the resulting on-disk job
 * YAML carries each value through the storage.Job mapping
 * we just extended. The test covers the form -> storage ->
 * YAML chain at the public HTTP surface, mirroring how the
 * existing TestUI_JobCreateAndEdit gate exercises the raw
 * textarea path.
 *
 * The form posts with view=form so buildJobFromForm in
 * jobform_builder.go is the path under test (not the raw
 * YAML textarea). days_to_keep is intentionally blank to
 * exercise the "max_age omitted" branch of the discard
 * mapping; max_builds is 30 to exercise the storage.Retention.
 * MaxBuilds carry-through.
 */

import (
    "net/http"
    "net/url"
    "strings"
    "testing"
)

func TestJobEdit_GeneralOptionsRoundtrip(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    form := url.Values{
        "view":                              {"form"},
        "name":                              {"general-opts"},
        "description":                       {"general options job"},
        "discard_old_builds.strategy":       {"log_rotation"},
        "discard_old_builds.max_builds":     {"30"},
        "discard_old_builds.days_to_keep":   {""},
        "github_project_url":                {"https://github.com/wolfssl/wolfssl/"},
        "steps_yaml":                        {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST /jobs/new status = %d, want 303; "+
            "body:\n%s", resp.StatusCode, body)
    }

    st := storageFromServer(ts)
    job, err := st.LoadJob("general-opts")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if job.Description != "general options job" {
        t.Errorf("Description = %q, want %q",
            job.Description, "general options job")
    }
    if job.GitHubProjectURL !=
        "https://github.com/wolfssl/wolfssl/" {
        t.Errorf("GitHubProjectURL = %q, want %q",
            job.GitHubProjectURL,
            "https://github.com/wolfssl/wolfssl/")
    }
    if job.Retention == nil {
        t.Fatalf("Retention is nil; expected MaxBuilds=30")
    }
    if job.Retention.MaxBuilds != 30 {
        t.Errorf("Retention.MaxBuilds = %d, want 30",
            job.Retention.MaxBuilds)
    }
    if job.Retention.MaxAge != "" {
        t.Errorf("Retention.MaxAge = %q, want empty "+
            "(days_to_keep was blank)",
            job.Retention.MaxAge)
    }

    /* Round-trip the YAML to confirm the storage shape
     * matches what an operator would see in the raw editor
     * after the form save. The github_project_url and
     * retention.max_builds fields must both appear in the
     * marshalled YAML. */
    resp = mustGet(t, client,
        ts.URL+"/jobs/general-opts/edit?view=raw")
    body := readBody(t, resp)
    for _, want := range []string{
        "name: general-opts",
        "description: general options job",
        "github_project_url: https://github.com/wolfssl/wolfssl/",
        "max_builds: 30",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("edit raw view body missing %q", want)
        }
    }
}

func TestJobEdit_DiscardOldBuilds_DaysSetMaxAge(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    form := url.Values{
        "view": {"form"},
        "name": {"days-only"},
        "discard_old_builds.strategy":     {"log_rotation"},
        "discard_old_builds.max_builds":   {""},
        "discard_old_builds.days_to_keep": {"14"},
        "steps_yaml":                      {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 303; body:\n%s",
            resp.StatusCode, body)
    }
    st := storageFromServer(ts)
    job, err := st.LoadJob("days-only")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if job.Retention == nil {
        t.Fatalf("Retention is nil; days_to_keep should " +
            "populate it")
    }
    if job.Retention.MaxAge != "14d" {
        t.Errorf("Retention.MaxAge = %q, want %q",
            job.Retention.MaxAge, "14d")
    }
    if job.Retention.MaxBuilds != 0 {
        t.Errorf("Retention.MaxBuilds = %d, want 0 (max_builds "+
            "was blank)", job.Retention.MaxBuilds)
    }
}

func TestJobEdit_DiscardOldBuilds_RejectsUnknownStrategy(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    form := url.Values{
        "view": {"form"},
        "name": {"bad-strat"},
        "discard_old_builds.strategy": {"unknown_strategy"},
        "steps_yaml":                  {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", form)
    if resp.StatusCode != http.StatusOK {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 200 (form "+
            "re-render with error); body:\n%s",
            resp.StatusCode, body)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, "discard_old_builds.strategy") {
        t.Errorf("error body missing 'discard_old_builds."+
            "strategy' diagnostic:\n%s", body)
    }
}
