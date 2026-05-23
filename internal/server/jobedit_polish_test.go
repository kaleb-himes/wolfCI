package server_test

/* internal/server/jobedit_polish_test.go - Polish queue P1
 * + P2 structural gates.
 *
 * CLAUDE.md rule 14 requires visual sign-off through a real
 * browser screenshot, which lives outside the Go test
 * suite. These tests guard the structural / className
 * markers the CSS hooks into so a future template churn
 * cannot silently strip the editor-card / fieldset / kind-
 * grid classes the polish rules style.
 *
 * The visual layer (spacing rhythm, color, hover states) is
 * verified by the operator when they exercise the page;
 * any layout regression that does not break these markers
 * is in scope for a follow-up commit.
 */

import (
    "net/http"
    "net/url"
    "strings"
    "testing"
)

func TestJobEdit_RawView_HasEditorCardMarkers(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    /* Create a job so /jobs/<name>/edit has something to
     * pre-fill the raw view with. */
    resp := mustPostForm(t, client, ts.URL+"/jobs/new",
        url.Values{
            "spec": {"name: polish-target\nsteps:\n  - shell: 'true'\n"},
        })
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("create POST status = %d; body:\n%s",
            resp.StatusCode, body)
    }
    resp = mustGet(t, client,
        ts.URL+"/jobs/polish-target/edit?view=raw")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET raw view status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    /* The raw view's polish markers. */
    for _, want := range []string{
        `class="editor-card"`,
        `class="editor-form"`,
        `class="editor-textarea"`,
        `class="editor-action-bar"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("raw view body missing %q (polish "+
                "marker for P1)", want)
        }
    }
}

func TestJobEdit_FormView_HasFieldsetGroupings(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp := mustPostForm(t, client, ts.URL+"/jobs/new",
        url.Values{
            "spec": {"name: polish-form\nsteps:\n  - shell: 'true'\n"},
        })
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("create POST status = %d; body:\n%s",
            resp.StatusCode, body)
    }
    resp = mustGet(t, client,
        ts.URL+"/jobs/polish-form/edit?view=form")
    body := readBody(t, resp)
    /* The form view's polish markers: each major group is
     * wrapped in a <fieldset class="form-group"> with a
     * <legend>. The General + Spec details fieldsets are
     * the new groupings 18.27's General-options work
     * deliberately did not add yet; the polish pass
     * introduces them. */
    for _, want := range []string{
        `<legend>General</legend>`,
        `<legend>Retention</legend>`,
        `<legend>Triggers</legend>`,
        `<legend>Spec details (YAML fragments)</legend>`,
        `<legend>GitHub Pull Request Builder</legend>`,
        `<legend>Pipeline script from SCM</legend>`,
        `<legend>Build environment</legend>`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form view body missing %q (polish "+
                "marker for P2)", want)
        }
    }
}
