package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestUI_BuildLogPage is the gating test for PLAN.md task 6.4
// (UI side; the SSE transport itself is gated by
// TestLogTail_LivePersistAndStream in logtail_test.go). It
// asserts that GET /jobs/{name}/builds/{n} returns an HTML page
// containing the JS hook that connects to the matching SSE
// endpoint.
func TestUI_BuildLogPage(t *testing.T) {
	ts, jar := newAuthedUI(t)
	defer ts.Close()
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	resp := mustGet(t, client, ts.URL+"/jobs/demo/builds/3")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{
		"Build 3",
		"demo",
		`EventSource("/api/v1/builds/demo/3/log")`,
		"addEventListener(\"log\"",
		"<pre",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestUI_BuildLogPage_BadNumber rejects non-positive build
// numbers with 400.
func TestUI_BuildLogPage_BadNumber(t *testing.T) {
	ts, jar := newAuthedUI(t)
	defer ts.Close()
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/jobs/demo/builds/-1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// TestUI_SSEEndpointRequiresAuth covers the auth gate on the
// SSE endpoint itself. Anonymous request gets redirected to /.
func TestUI_SSEEndpointRequiresAuth(t *testing.T) {
	ts, _ := newAuthedUI(t)
	defer ts.Close()

	anonClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, anonClient, ts.URL+"/api/v1/builds/anything/1/log")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Errorf("anon /api/v1/builds status = %d, want redirect", resp.StatusCode)
	}
}
