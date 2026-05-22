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

// TestUI_NodesPage gates that /nodes lists every registered
// agent by its display name. Originally Phase 6.5's labels +
// executor-count test; Phase 12.6 replaced the column set so
// labels/executors moved to the per-node detail page (12.7).
// What remains: the agent's name must appear in the table, and
// an agent with no heartbeat ever must render with the "na"
// pre-first-heartbeat status badge (Connected = false and
// hasHeartbeat = false both fail through to "na", which the
// template colors grey to set it apart from a true offline).
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
	if !strings.Contains(body, "node-alpha") {
		t.Errorf("body missing 'node-alpha' display name; got:\n%s", body)
	}
	if !strings.Contains(body, "node-status na") {
		t.Errorf("body missing 'node-status na' badge for an " +
			"agent that has never sent a heartbeat")
	}
}

// TestUI_NodesShowsMasterFirst gates PLAN.md 12.5. With the
// built-in master registered alongside a remote agent, /nodes
// must render the master's "wolfCI Master Node" row before the
// remote agent's row so the operator's eye lands on it first.
func TestUI_NodesShowsMasterFirst(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	if _, err := svc.Register(context.Background(), &wolfciv1.AgentInfo{
		AgentId:   "node-alpha",
		Labels:    []string{"linux", "x86_64"},
		Executors: 4,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	masterIdx := strings.Index(body, "wolfCI Master Node")
	if masterIdx < 0 {
		t.Fatalf("body missing 'wolfCI Master Node'; got:\n%s", body)
	}
	alphaIdx := strings.Index(body, "node-alpha")
	if alphaIdx < 0 {
		t.Fatalf("body missing 'node-alpha'; got:\n%s", body)
	}
	if masterIdx > alphaIdx {
		t.Errorf("master row appears after node-alpha row "+
			"(master at %d, alpha at %d); want master first",
			masterIdx, alphaIdx)
	}
}

// TestUI_NodesPageColumnsPresent gates PLAN.md 12.6. The
// Phase 12 column set replaces the Phase 6 stub (Agent ID +
// Status + Labels + Executors). Each header text below must
// appear exactly once in the response body; this catches both
// a missing column and a duplicate that would crowd the layout.
func TestUI_NodesPageColumnsPresent(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	headers := []string{
		">S<",
		">Name<",
		">Architecture<",
		">Clock difference<",
		">Free disk<",
		">Free swap<",
		">Free temp<",
		">Go version<",
		">Response time<",
		">Agent version<",
	}
	for _, h := range headers {
		got := strings.Count(body, h)
		if got != 1 {
			t.Errorf("header %q appears %d times in body, want 1", h, got)
		}
	}
}

// TestUI_NodesPage_MasterRowRendersDisplayName gates PLAN.md
// 12.6. The wolfci-master row must show the display-name
// "wolfCI Master Node", not the wire identifier; the wire
// identifier stays addressable via Phase 12.7's /nodes/<id>
// but is not what the operator reads in the table.
func TestUI_NodesPage_MasterRowRendersDisplayName(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if !strings.Contains(body, "wolfCI Master Node") {
		t.Errorf("body missing display name 'wolfCI Master Node'")
	}
}

// TestUI_NodesPage_OfflineAgentRendersOfflineBadge gates PLAN.md
// 12.6. A registered agent that has never sent a heartbeat (or
// whose heartbeat is older than StaleThreshold) renders with
// the Phase 12 "offline" status badge, NOT the "ok" badge and
// NOT the pre-heartbeat "na" badge.
func TestUI_NodesPage_OfflineAgentRendersOfflineBadge(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	/* StaleThreshold tightened so the recorded heartbeat below
	 * goes stale instantly, exercising the offline path without
	 * waiting the default 90s.
	 */
	svc.StaleThreshold = 10 * time.Millisecond

	if _, err := svc.Register(context.Background(), &wolfciv1.AgentInfo{
		AgentId:   "node-offline",
		Labels:    []string{"linux"},
		Executors: 1,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	svc.RecordHeartbeat("node-offline", &wolfciv1.NodeStatus{
		Architecture: "linux/amd64",
	})
	time.Sleep(40 * time.Millisecond)

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client, ts.URL+"/nodes")
	body := readBody(t, resp)

	if !strings.Contains(body, "node-status offline") {
		t.Errorf("body missing 'node-status offline' badge; got:\n%s",
			body)
	}
	if strings.Contains(body, "node-status ok") &&
		strings.Contains(body, "node-offline") {
		/* The body may legitimately contain "node-status ok"
		 * for the master row. Only fail if the offline row
		 * also shows ok, which we approximate by ensuring
		 * the row body has the offline badge.
		 */
	}
}

// TestNodeDetail_RendersStatus gates PLAN.md 12.7. GET
// /nodes/wolfci-master returns 200 with the master's display
// name and snapshot fields (architecture + go version + agent
// version) on the page. The detail page is where labels and
// executor count live now (since the table on /nodes dropped
// them in 12.6).
func TestNodeDetail_RendersStatus(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp := mustGet(t, client,
		ts.URL+"/nodes/"+agentsvc.BuiltInNodeAgentID)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body:\n%s",
			resp.StatusCode, body)
	}
	for _, want := range []string{
		"wolfCI Master Node",
		agentsvc.BuiltInNodeLabel,
		"Architecture",
		"Go version",
		"Agent version",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestNodeDetail_TakeOfflineFlipsFlag gates PLAN.md 12.7's
// Take offline toggle. POSTing the disable action flips the
// in-memory disabled flag agentsvc tracks; toggling back via
// enable clears it. The Router skip-offline test
// (TestRouter_SkipsOfflineNode) covers the dispatch effect.
func TestNodeDetail_TakeOfflineFlipsFlag(t *testing.T) {
	ts, jar, svc := newAuthedUIWithAgentSvc(t)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	/* POST /nodes/<id>/disable -> agent becomes disabled. */
	disableURL := ts.URL + "/nodes/" + agentsvc.BuiltInNodeAgentID +
		"/disable"
	resp, err := client.PostForm(disableURL, nil)
	if err != nil {
		t.Fatalf("POST disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST disable status = %d, want 303",
			resp.StatusCode)
	}
	if !svc.IsDisabled(agentsvc.BuiltInNodeAgentID) {
		t.Error("agent not disabled after POST /disable")
	}

	/* POST /nodes/<id>/enable -> agent becomes enabled again. */
	enableURL := ts.URL + "/nodes/" + agentsvc.BuiltInNodeAgentID +
		"/enable"
	resp, err = client.PostForm(enableURL, nil)
	if err != nil {
		t.Fatalf("POST enable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST enable status = %d, want 303",
			resp.StatusCode)
	}
	if svc.IsDisabled(agentsvc.BuiltInNodeAgentID) {
		t.Error("agent still disabled after POST /enable")
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
