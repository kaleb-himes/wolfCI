package server_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/auth"
	"github.com/kaleb-himes/wolfCI/internal/server"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestUI_NodesPage is the gating test for PLAN.md task 6.5.
// It pre-registers an agent on agentsvc and asserts that
// GET /nodes lists it with its labels and executor count.
func TestUI_NodesPage(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	if _, err := svc.Register(context.Background(), &wolfciv1.AgentInfo{
		AgentId:   "node-alpha",
		Labels:    []string{"linux", "x86_64"},
		Executors: 4,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"node-alpha", "linux", "x86_64", ">4<"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Without an open Connect stream the agent should display as
	// offline; the status column reflects this.
	if !strings.Contains(body, "offline") {
		t.Errorf("body missing 'offline' status; got:\n%s", body)
	}
}

// TestUI_NodesPage_Empty verifies the page renders cleanly with
// no agents registered and points operators at the right docs.
func TestUI_NodesPage_Empty(t *testing.T) {
	ts, jar := newAuthedUI(t)
	defer ts.Close()
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "No nodes registered") {
		t.Errorf("expected 'No nodes registered' message; got:\n%s", body)
	}
}

// newAuthedUIWithAgentSvc is the sibling of newAuthedUI that
// also wires an agentsvc.Server through Options so /nodes has
// something to display.
func newAuthedUIWithAgentSvc(t *testing.T) (*httptest.Server, http.CookieJar, *agentsvc.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	authCfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: 1000}
	pwStore := auth.NewPasswordStore(filepath.Join(dir, "passwords"), authCfg)
	if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	sessions := server.NewSessionStore(filepath.Join(dir, "sessions"), time.Hour)
	svc := agentsvc.New("nodes-test")

	srv := server.New(server.Options{
		Storage:      st,
		Auth:         authCfg,
		Passwords:    pwStore,
		Sessions:     sessions,
		AgentSvc:     svc,
		CookieSecure: false,
	})
	ts := httptest.NewServer(srv)
	testStorageByURL[ts.URL] = st

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/login", url.Values{
		"username": {"alice"},
		"password": {"hunter2"},
	})
	if err != nil {
		ts.Close()
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		ts.Close()
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}
	return ts, jar, svc
}
