package server_test

/* Phase 16.1 - dark theme by default + "stupid white view
 * that nobody wants" toggle.
 *
 * Theme markers asserted in the rendered HTML:
 *   - data-theme="dark" lives on the <html> tag by default
 *     so server-rendered pages already match the dark theme
 *     before the inline localStorage script runs (no flash
 *     of light theme on reload).
 *   - The toggle button's label uses the project owner's
 *     verbatim phrasing.
 */

import (
    "net/http"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestTheme_DefaultIsDark(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "demo",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Both /jobs and /jobs/<name> should carry the dark
     * theme marker since the base template sets it for
     * every page that extends "base".
     */
    for _, path := range []string{"/jobs", "/jobs/demo"} {
        resp := mustGet(t, client, ts.URL+path)
        body := readBody(t, resp)
        if resp.StatusCode != http.StatusOK {
            t.Errorf("%s status = %d, want 200",
                path, resp.StatusCode)
            continue
        }
        if !strings.Contains(body, `data-theme="dark"`) {
            t.Errorf("%s body missing data-theme=\"dark\" "+
                "(dark must be the default)", path)
        }
    }
}

func TestTheme_LightToggleHasVerbatimName(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    resp := mustGet(t, client, ts.URL+"/jobs")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* Verbatim phrasing per the project owner; do not
     * substitute synonyms or softer wording.
     */
    want := "stupid white view that nobody wants"
    if !strings.Contains(body, want) {
        t.Errorf("body missing the verbatim toggle label "+
            "%q", want)
    }
}
