package server_test

/* internal/server/nodes_new_test.go - PLAN.md 19.2 gating
 * tests.
 *
 * TestUI_NodesNew_LandingHasThreeCards asserts /nodes/new
 * renders the three "kind picker" cards (Permanent Agent /
 * Google Compute Engine / Copy existing node) each linking
 * to the right /nodes/new/<kind> URL.
 *
 * TestUI_NodesPageHasNewButton asserts the existing /nodes
 * page now carries a "New node" button linking to
 * /nodes/new.
 */

import (
    "net/http"
    "strings"
    "testing"
)

func TestUI_NodesNew_LandingHasThreeCards(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client, ts.URL+"/nodes/new")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET /nodes/new status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    /* Each card is a link to a kind-specific path. The test
     * checks both the visible heading + the href so a future
     * refactor that moves the card markup around still has to
     * preserve the routes. */
    for _, want := range []string{
        "Permanent Agent",
        `href="/nodes/new/permanent"`,
        "Google Compute Engine",
        `href="/nodes/new/gce"`,
        "Copy existing node",
        `href="/nodes/new?copy=`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("/nodes/new body missing %q", want)
        }
    }
}

func TestUI_NodesPageHasNewButton(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client, ts.URL+"/nodes")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET /nodes status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, `href="/nodes/new"`) {
        t.Errorf("/nodes body missing href=\"/nodes/new\"")
    }
    if !strings.Contains(body, "New node") {
        t.Errorf("/nodes body missing the \"New node\" " +
            "button label")
    }
}
