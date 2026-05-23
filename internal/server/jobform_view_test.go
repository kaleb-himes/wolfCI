package server_test

/* Phase 17.1 - dual configuration editor.
 *
 * /jobs/<name>/edit?view=raw  -> existing YAML textarea
 * /jobs/<name>/edit?view=form -> per-field inputs with a
 *   dropdown for trigger.type (the canonical "finite set
 *   of options" field in the schema).
 *
 * Both views POST back to the same URL; a hidden "view"
 * field tells the handler which parser to run, and both
 * paths exit through storage.SaveJob so the cycle check +
 * flock + write semantics are identical regardless of
 * which editor the operator preferred.
 */

import (
    "net/http"
    "net/url"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobEdit_BothViewTabsLinked(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "tabs",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Default view (no ?view=) shows raw editor + a link
     * to the form view. ?view=form does the inverse.
     */
    for _, path := range []string{
        "/jobs/tabs/edit",
        "/jobs/tabs/edit?view=form",
    } {
        resp := mustGet(t, client, ts.URL+path)
        body := readBody(t, resp)
        if resp.StatusCode != http.StatusOK {
            t.Errorf("GET %s status = %d, want 200",
                path, resp.StatusCode)
            continue
        }
        if !strings.Contains(body, "?view=raw") {
            t.Errorf("GET %s body missing tab link "+
                "?view=raw", path)
        }
        if !strings.Contains(body, "?view=form") {
            t.Errorf("GET %s body missing tab link "+
                "?view=form", path)
        }
    }
}

func TestJobEdit_FormViewHasFieldPerSchemaItem(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:        "fields",
        Description: "old desc",
        NodeLabel:   "linux",
        Timeout:     "5m",
        Retries:     2,
        Steps:       []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    resp := mustGet(t, client,
        ts.URL+"/jobs/fields/edit?view=form")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* One named input per top-level schema item. */
    for _, want := range []string{
        `name="name"`,
        `name="description"`,
        `name="node_label"`,
        `name="timeout"`,
        `name="retries"`,
        `name="retention_max_builds"`,
        `name="retention_max_age"`,
        `name="upstream"`,
        `name="steps_yaml"`,
        `name="parameters_yaml"`,
        `name="axis_yaml"`,
        `name="triggers_downstream_yaml"`,
        /* Hidden field that tells POST it came from the
         * form view rather than the raw textarea.
         */
        `name="view"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form view missing input %q", want)
        }
    }
    /* Existing scalar values should pre-fill so the form
     * is an edit, not a blank slate.
     */
    for _, want := range []string{
        "old desc", "linux", "5m",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form view did not pre-fill %q", want)
        }
    }
}

func TestJobEdit_FormViewTriggerTypeIsDropdown(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "drop",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    resp := mustGet(t, client,
        ts.URL+"/jobs/drop/edit?view=form")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* At least one trigger row's type field must be a
     * <select> with the known options.
     */
    if !strings.Contains(body, `<select`) {
        t.Errorf("form view has no <select> elements")
    }
    for _, opt := range []string{
        `value="cron"`,
        `value="webhook"`,
        `value="scm"`,
    } {
        if !strings.Contains(body, opt) {
            t.Errorf("trigger-type dropdown missing %q",
                opt)
        }
    }
}

func TestJobEdit_FormViewSubmitBuildsSpec(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "submitme",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    form := url.Values{
        "view":                 {"form"},
        "name":                 {"submitme"},
        "description":          {"updated via form"},
        "node_label":           {"darwin"},
        "timeout":              {"15m"},
        "retries":              {"3"},
        "retention_max_builds": {"30"},
        "retention_max_age":    {"720h"},
        "upstream":             {"upstream-a\nupstream-b"},
        "trigger_0_type":       {"cron"},
        "trigger_0_config":     {"0 * * * *"},
        "trigger_1_type":       {""},
        "trigger_1_config":     {""},
        "trigger_2_type":       {""},
        "trigger_2_config":     {""},
        "steps_yaml": {"- name: build\n" +
            "  shell: make all\n"},
        "parameters_yaml":          {""},
        "axis_yaml":                {""},
        "triggers_downstream_yaml": {""},
    }
    resp := mustPostForm(t, client,
        ts.URL+"/jobs/submitme/edit?view=form", form)
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("status = %d, want 303", resp.StatusCode)
    }

    got, err := st.LoadJob("submitme")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if got.Description != "updated via form" {
        t.Errorf("Description = %q, want %q",
            got.Description, "updated via form")
    }
    if got.NodeLabel != "darwin" {
        t.Errorf("NodeLabel = %q, want darwin",
            got.NodeLabel)
    }
    if got.Timeout != "15m" {
        t.Errorf("Timeout = %q, want 15m", got.Timeout)
    }
    if got.Retries != 3 {
        t.Errorf("Retries = %d, want 3", got.Retries)
    }
    if got.Retention == nil ||
        got.Retention.MaxBuilds != 30 ||
        got.Retention.MaxAge != "720h" {
        t.Errorf("Retention = %+v, want {MaxBuilds:30, "+
            "MaxAge:720h}", got.Retention)
    }
    if len(got.Upstream) != 2 ||
        got.Upstream[0] != "upstream-a" ||
        got.Upstream[1] != "upstream-b" {
        t.Errorf("Upstream = %v, want [upstream-a "+
            "upstream-b]", got.Upstream)
    }
    if len(got.Triggers) != 1 ||
        got.Triggers[0].Type != "cron" {
        t.Errorf("Triggers = %+v, want one cron trigger",
            got.Triggers)
    }
    if len(got.Steps) != 1 ||
        got.Steps[0].Shell != "make all" {
        t.Errorf("Steps = %+v, want one shell=make-all",
            got.Steps)
    }
}
