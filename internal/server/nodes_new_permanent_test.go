package server_test

/* internal/server/nodes_new_permanent_test.go - PLAN.md 19.3
 * gating tests.
 *
 * TestNodesNewPermanent_FormHasFields GETs /nodes/new/permanent
 * and asserts the four named inputs are present in the
 * rendered form.
 *
 * TestNodesNewPermanent_PostCreatesPending POSTs the form
 * with valid inputs, asserts the response redirects to
 * /nodes/<name>, and confirms a storage.PendingAgent record
 * landed with the supplied values.
 *
 * TestNodesNewPermanent_PostRejectsBadName POSTs a name with
 * a path separator and asserts the response re-renders the
 * form with an error (no redirect, no record on disk).
 */

import (
    "net/http"
    "net/url"
    "reflect"
    "strings"
    "testing"
)

func TestNodesNewPermanent_FormHasFields(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client, ts.URL+"/nodes/new/permanent")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    for _, want := range []string{
        `name="name"`,
        `<textarea`, `name="labels"`,
        `type="number"`, `name="executors"`,
        `name="description"`,
        `action="/nodes/new/permanent"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form body missing %q", want)
        }
    }
    /* Default executors=1 must pre-fill the input. */
    if !strings.Contains(body, `value="1"`) {
        t.Errorf("form body missing executors default of 1")
    }
}

func TestNodesNewPermanent_PostCreatesPending(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "name":        {"linux-builder-1"},
        "labels":      {"linux\nbuild\nx86_64\n"},
        "executors":   {"4"},
        "description": {"shop linux box"},
    }
    resp := mustPostForm(t, client,
        ts.URL+"/nodes/new/permanent", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 303; body:\n%s",
            resp.StatusCode, body)
    }
    if got := resp.Header.Get("Location"); got !=
        "/nodes/linux-builder-1" {
        t.Errorf("Location = %q, want /nodes/linux-builder-1",
            got)
    }
    st := storageFromServer(ts)
    pa, err := st.LoadPendingAgent("linux-builder-1")
    if err != nil {
        t.Fatalf("LoadPendingAgent: %v", err)
    }
    if pa.Executors != 4 {
        t.Errorf("Executors = %d, want 4", pa.Executors)
    }
    if !reflect.DeepEqual(pa.Labels,
        []string{"linux", "build", "x86_64"}) {
        t.Errorf("Labels = %v, want [linux build x86_64]",
            pa.Labels)
    }
    if pa.Description != "shop linux box" {
        t.Errorf("Description = %q, want %q",
            pa.Description, "shop linux box")
    }
}

func TestNodesNewPermanent_PostRejectsBadName(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "name":      {"bad/name"},
        "executors": {"1"},
    }
    resp := mustPostForm(t, client,
        ts.URL+"/nodes/new/permanent", form)
    /* Server re-renders the form with an error - 200, not
     * 303. */
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("POST status = %d, want 200 (form rerender)",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, "path separator") {
        t.Errorf("error body does not mention the "+
            "validation reason: %s", body)
    }
    /* The bad name did not land on disk. */
    st := storageFromServer(ts)
    if _, err := st.LoadPendingAgent("bad"); err == nil {
        t.Errorf("LoadPendingAgent(\"bad\") returned nil " +
            "error - the bad-name POST should not have " +
            "persisted anything")
    }
}
