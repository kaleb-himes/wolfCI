package server_test

/* internal/server/nodes_pending_detail_test.go - PLAN.md
 * 19.5 gating tests.
 *
 * TestNodesPendingDetail_RendersCommand seeds a PendingAgent
 * via storage, GETs /nodes/<name>, and asserts the page
 * renders:
 *
 *   - the wolfci-agent command line carrying the agent_id
 *     and the configured AgentEndpoint;
 *   - the cert-material instructions block (ca.pem +
 *     agent.pem + agent.key);
 *   - the slot's labels + executors metadata.
 *
 * TestNodesPendingDetail_NoEndpointShowsWarning asserts that
 * when server.Options.AgentEndpoint is empty, the page
 * surfaces a placeholder + an error block instructing the
 * operator to fill it in.
 *
 * Connected agents keep using the existing
 * handleNodeDetail surface - the pending route only fires
 * when the storage has a matching PendingAgent record.
 */

import (
    "net/http"
    "net/http/cookiejar"
    "net/http/httptest"
    "net/url"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestNodesPendingDetail_RendersCommand(t *testing.T) {
    ts, jar, st := newAuthedUIWithEndpoint(t,
        "ci.example.com:8443")
    defer ts.Close()
    client := &http.Client{Jar: jar}

    pa := &storage.PendingAgent{
        Name:        "linux-builder-1",
        Labels:      []string{"linux", "build", "x86_64"},
        Executors:   4,
        Description: "shop linux box",
    }
    if err := st.SaveAgent(pa); err != nil {
        t.Fatalf("SaveAgent: %v", err)
    }
    resp := mustGet(t, client,
        ts.URL+"/nodes/linux-builder-1")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    for _, want := range []string{
        "wolfci-agent",
        "--server-addr ci.example.com:8443",
        "--agent-id linux-builder-1",
        "--cert-dir /etc/wolfci-agent",
        "ca.pem",
        "agent.pem",
        "agent.key",
        "wolfci-ctl agent-cert mint --name linux-builder-1",
        "linux", "build", "x86_64",
        "(pending)",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("page missing %q", want)
        }
    }
}

func TestNodesPendingDetail_NoEndpointShowsWarning(t *testing.T) {
    ts, jar, st := newAuthedUIWithEndpoint(t, "")
    defer ts.Close()
    client := &http.Client{Jar: jar}
    if err := st.SaveAgent(&storage.PendingAgent{
        Name:      "stub-node",
        Executors: 1,
    }); err != nil {
        t.Fatalf("SaveAgent: %v", err)
    }
    resp := mustGet(t, client, ts.URL+"/nodes/stub-node")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, "SERVER_HOST:PORT") {
        t.Errorf("page missing the SERVER_HOST:PORT " +
            "placeholder")
    }
    if !strings.Contains(body, "AgentEndpoint") {
        t.Errorf("page missing the AgentEndpoint " +
            "configuration diagnostic")
    }
}

/* newAuthedUIWithEndpoint is the test scaffold variant that
 * lets the gating test inject server.Options.AgentEndpoint.
 * Returns the test server, an authenticated cookie jar, and
 * the underlying *storage.Storage so the test can seed
 * pending records directly. */
func newAuthedUIWithEndpoint(t *testing.T,
    endpoint string) (*httptest.Server, http.CookieJar,
    *storage.Storage) {
    t.Helper()
    dir := t.TempDir()
    st, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    authCfg := &auth.Config{PasswordEnabled: true,
        PBKDF2Iterations: 1000}
    pwStore := auth.NewPasswordStore(
        filepath.Join(dir, "passwords"), authCfg)
    if err := pwStore.SetPassword("alice",
        "hunter2"); err != nil {
        t.Fatalf("SetPassword: %v", err)
    }
    sessions := server.NewSessionStore(
        filepath.Join(dir, "sessions"), time.Hour)
    srv := server.New(server.Options{
        Storage:       st,
        Auth:          authCfg,
        Passwords:     pwStore,
        Sessions:      sessions,
        CookieSecure:  false,
        AgentEndpoint: endpoint,
    })
    ts := httptest.NewServer(srv)
    testStorageByURL[ts.URL] = st
    jar, _ := cookiejar.New(nil)
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request,
            []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := client.PostForm(ts.URL+"/login",
        url.Values{
            "username": {"alice"}, "password": {"hunter2"},
        })
    if err != nil {
        ts.Close()
        t.Fatalf("login: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        ts.Close()
        t.Fatalf("login status = %d, want 303",
            resp.StatusCode)
    }
    return ts, jar, st
}
