package server_test

/* internal/server/jobs_new_picker_test.go - operator-feedback
 * follow-up: /jobs/new gains a Jenkins-style 3-card picker
 * (Inline script / More complex / Most complex) and the
 * default ?view= flips to "form" so clicking "New job"
 * lands on the friendlier form view first.
 *
 * The picker matches /nodes/new in spirit - operators saw
 * the bare YAML editor on first paint and were confused;
 * the picker spells out the job shapes in plain language
 * and routes each card to the existing job-edit form
 * with the chosen kind seeded in the URL.
 */

import (
    "net/http"
    "strings"
    "testing"
)

func TestUI_JobsNew_LandingHasThreeCards(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client, ts.URL+"/jobs/new")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET /jobs/new status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    for _, want := range []string{
        "Inline script, simple job",
        `href="/jobs/new?kind=inline"`,
        "More complex, multi-step job",
        `href="/jobs/new?kind=pipeline"`,
        "Most complex, multi-path, multi-step job",
        `href="/jobs/new?kind=multibranch"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("/jobs/new picker missing %q", want)
        }
    }
}

func TestUI_JobsNew_KindRoutesToFormByDefault(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client,
        ts.URL+"/jobs/new?kind=pipeline")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET /jobs/new?kind=pipeline status = %d, "+
            "want 200", resp.StatusCode)
    }
    body := readBody(t, resp)
    /* The form view is the default - assert the form's
     * unique markers (one of the new fieldsets, the
     * description textarea) are present without an
     * explicit ?view=form. */
    for _, want := range []string{
        `<legend>General</legend>`,
        `id="f-desc"`,
        /* Kind banner appears for /jobs/new with a Kind. */
        "Pipeline (more complex, multi-step job)",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form view missing %q", want)
        }
    }
    /* The Raw YAML textarea (name="spec") is NOT in the
     * default form view - operators reach for it via the
     * Raw tab. */
    if strings.Contains(body, `name="spec"`) {
        t.Errorf("default ?view= rendered the raw spec " +
            "textarea instead of the form view")
    }
}

func TestUI_JobsNew_RawTabStillAccessible(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client,
        ts.URL+"/jobs/new?kind=inline&view=raw")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET ?view=raw status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, `name="spec"`) {
        t.Errorf("?view=raw body should carry the raw " +
            "spec textarea")
    }
}
