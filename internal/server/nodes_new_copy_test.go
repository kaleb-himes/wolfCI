package server_test

/* internal/server/nodes_new_copy_test.go - PLAN.md 19.7
 * gating tests.
 *
 * TestNodesNewCopy_PrefillsFormFromSource seeds a
 * PendingAgent, GETs /nodes/new?copy=<source>, and asserts
 * the rendered Permanent Agent form pre-fills the source's
 * labels + executors. The Name field stays empty so the
 * operator types a fresh name.
 *
 * TestNodesNewCopy_EmptySourceShowsPicker GETs
 * /nodes/new?copy= (no source) and asserts the page renders
 * a picker listing every pending agent with a "Copy" link
 * back to /nodes/new?copy=<id>.
 */

import (
    "net/http"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestNodesNewCopy_PrefillsFormFromSource(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    st := storageFromServer(ts)
    if err := st.SaveAgent(&storage.PendingAgent{
        Name:      "source-node",
        Labels:    []string{"linux", "build", "arm64"},
        Executors: 8,
    }); err != nil {
        t.Fatalf("SaveAgent: %v", err)
    }
    resp := mustGet(t, client,
        ts.URL+"/nodes/new?copy=source-node")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    /* The form is the Permanent Agent form. */
    if !strings.Contains(body,
        `action="/nodes/new/permanent"`) {
        t.Errorf("body missing the Permanent Agent form " +
            "action - the copy=source-node request should " +
            "have routed to the permanent form pre-filled")
    }
    /* Labels textarea pre-filled with one entry per line. */
    for _, want := range []string{"linux", "build", "arm64"} {
        if !strings.Contains(body, want) {
            t.Errorf("form body missing label %q from source",
                want)
        }
    }
    /* Executors pre-filled to source's value. */
    if !strings.Contains(body, `value="8"`) {
        t.Errorf("form body missing executors=8 pre-fill " +
            "from source")
    }
    /* The form's name field stays empty (operator must
     * choose a fresh name) - the value attribute should be
     * empty. */
    if strings.Contains(body, `value="source-node"`) {
        t.Errorf("form body unexpectedly carries the " +
            "source's name verbatim; operator should " +
            "have to type a fresh name")
    }
}

func TestNodesNewCopy_EmptySourceShowsPicker(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    st := storageFromServer(ts)
    if err := st.SaveAgent(&storage.PendingAgent{
        Name:      "pending-foo",
        Labels:    []string{"linux"},
        Executors: 2,
    }); err != nil {
        t.Fatalf("SaveAgent: %v", err)
    }
    resp := mustGet(t, client, ts.URL+"/nodes/new?copy=")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, "pending-foo") {
        t.Errorf("picker body missing the pending-foo " +
            "source row")
    }
    if !strings.Contains(body,
        `href="/nodes/new?copy=pending-foo"`) {
        t.Errorf("picker body missing the per-source Copy " +
            "link with copy=<id>")
    }
}
