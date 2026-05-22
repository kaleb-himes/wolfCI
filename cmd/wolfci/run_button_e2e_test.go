package main

/* End-to-end test that mirrors the Firefox flow the user reported:
 * setup -> login -> click Run -> follow the 303 -> buildlog page
 * loads -> SSE endpoint reachable. Plus an ALPN sanity check that
 * wolfSSL isn't accidentally advertising h2 (which would confuse a
 * Go net/http.Server that only speaks HTTP/1.1).
 */

import (
    "context"
    "crypto/tls"
    "io"
    "net/http"
    "net/http/cookiejar"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/storage"
    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"

    gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"
)

/* e2eClient returns an HTTPS client with a cookie jar that follows
 * 303 redirects but stops short of double-following so the test
 * can inspect each Location header. NextProtos is intentionally
 * empty (HTTP/1.1 only) for the main flow; a separate test asks
 * for h2 to probe ALPN.
 */
func e2eClient(t *testing.T) *http.Client {
    t.Helper()
    jar, _ := cookiejar.New(nil)
    return &http.Client{
        Jar:     jar,
        Timeout: 15 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
                MinVersion:         tls.VersionTLS13,
                MaxVersion:         tls.VersionTLS13,
            },
            /* Keep-alives are intentionally enabled on the
             * client. The server-side fix is
             * httpSrv.SetKeepAlivesEnabled(false) in
             * cmd/wolfci/main.go; this test proves that the
             * server's Connection: close response actually
             * prevents the misframing the user reported in
             * Firefox.
             */
        },
    }
}

func TestRun_RunButtonEndToEnd(t *testing.T) {
    f := newRunFixture(t)
    addr, stop := startRun(t, f)
    defer stop()
    base := "https://" + addr

    /* 1. Read the bootstrap token. */
    tokenBytes, err := os.ReadFile(filepath.Join(f.authDir, "bootstrap", "token"))
    if err != nil {
        t.Fatalf("read token: %v", err)
    }
    token := strings.TrimSpace(string(tokenBytes))

    /* 2. Build a real Ed25519 pubkey line (test-only keygen is
     *    explicitly allowed by feedback memory byok-no-keygen).
     */
    pub, _, err := wolfcrypt.Ed25519GenKey()
    if err != nil {
        t.Fatalf("Ed25519GenKey: %v", err)
    }
    pubLine := strings.TrimSpace(string(
        gowolfssh.EncodeSSHEd25519AuthorizedKey(pub, "test")))

    client := e2eClient(t)

    /* 3. POST /setup. */
    resp, err := client.PostForm(base+"/setup", url.Values{
        "token":            {token},
        "username":         {"tester"},
        "pubkey":           {pubLine},
        "password":         {"hunter2"},
        "password_confirm": {"hunter2"},
    })
    if err != nil {
        t.Fatalf("POST /setup: %v", err)
    }
    bodyMustNotContain(t, resp, "[setup]")
    resp.Body.Close()
    if resp.StatusCode >= 400 {
        t.Fatalf("/setup status = %d", resp.StatusCode)
    }

    /* 4. POST /login. */
    resp, err = client.PostForm(base+"/login", url.Values{
        "username": {"tester"},
        "password": {"hunter2"},
    })
    if err != nil {
        t.Fatalf("POST /login: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode >= 400 {
        t.Fatalf("/login status = %d", resp.StatusCode)
    }

    /* 5. Save a one-step job directly to the storage root. The
     *    storage layer reads from disk on every ListJobs/LoadJob;
     *    no in-memory cache to flush.
     */
    st, err := storage.New(f.workDir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := st.SaveJob(&storage.Job{
        Name:    "echo",
        Steps:   []storage.Step{{Shell: "echo wolfci-e2e-marker"}},
        Timeout: "30s",
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* 6. Click Run. Disable auto-redirect so we can inspect the
     *    303 Location ourselves.
     */
    client.CheckRedirect = func(*http.Request, []*http.Request) error {
        return http.ErrUseLastResponse
    }
    resp, err = client.PostForm(base+"/jobs/echo/run", nil)
    if err != nil {
        t.Fatalf("POST /jobs/echo/run: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        t.Fatalf("POST run status = %d, want 303", resp.StatusCode)
    }
    loc := resp.Header.Get("Location")
    if loc != "/jobs/echo/builds/1" {
        t.Fatalf("Location = %q, want /jobs/echo/builds/1", loc)
    }

    /* 7. Follow the redirect. This is the request Firefox was
     *    failing on with "Secure Connection Failed". If wolfSSL
     *    is happy and Go's http.Server renders the buildlog
     *    template, we get HTML.
     */
    resp, err = client.Get(base + loc)
    if err != nil {
        t.Fatalf("GET %s: %v", loc, err)
    }
    body, _ := io.ReadAll(resp.Body)
    resp.Body.Close()
    /* The Connection: close server-side workaround is exercised
     * implicitly: the test client uses default keep-alives, so
     * if cmd/wolfci's httpSrv.SetKeepAlivesEnabled(false) regresses
     * the second request after Run would fail with "Unsolicited
     * response received on idle HTTP channel" + a stale 400, the
     * same way Firefox saw it. Go's http.Client strips the
     * Connection header from resp.Header as a hop-by-hop value,
     * so we cannot assert it directly; we let the success of the
     * subsequent GET stand as proof.
     */
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET %s status = %d, want 200, body=%q",
            loc, resp.StatusCode, body)
    }
    /* The buildlog template renders <h2>Build N - jobname</h2>
     * and the EventSource script pointing at /api/v1/builds/...
     */
    s := string(body)
    for _, want := range []string{
        "Build 1 - echo",
        "/api/v1/builds/echo/1/log",
        "EventSource",
    } {
        if !strings.Contains(s, want) {
            t.Errorf("buildlog body missing %q", want)
        }
    }

    /* 8. SSE endpoint must be reachable and announce its content
     *    type. We don't read the whole stream; opening + checking
     *    headers + closing is enough to prove the route works.
     */
    ctx, cancel := context.WithTimeout(context.Background(),
        5*time.Second)
    defer cancel()
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
        base+"/api/v1/builds/echo/1/log", nil)
    resp, err = client.Do(req)
    if err != nil {
        t.Fatalf("GET log stream: %v", err)
    }
    if resp.StatusCode != http.StatusOK {
        t.Errorf("log stream status = %d, want 200", resp.StatusCode)
    }
    if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
        t.Errorf("log stream Content-Type = %q, want text/event-stream", ct)
    }
    /* Drain whatever is buffered up to ctx timeout, then close.
     * We don't gate on specific content - the job may or may not
     * have produced output yet.
     */
    _, _ = io.Copy(io.Discard, resp.Body)
    resp.Body.Close()
}

/* TestRun_ALPNDoesNotNegotiateH2 catches the suspected Firefox
 * problem: if wolfSSL is advertising h2 via ALPN even though we
 * never called wolfSSL_UseALPN, Go's net/http.Server (which only
 * speaks HTTP/1.1 by default) would parse incoming HTTP/2 frames
 * as malformed HTTP/1.1, producing the "Secure Connection Failed"
 * the user saw.
 *
 * Expected: the negotiated ALPN protocol is empty (no ALPN) OR
 * "http/1.1". A negotiated "h2" without explicit http2 wiring
 * in cmd/wolfci would be a bug.
 */
func TestRun_ALPNDoesNotNegotiateH2(t *testing.T) {
    f := newRunFixture(t)
    addr, stop := startRun(t, f)
    defer stop()

    conn, err := tls.Dial("tcp", addr, &tls.Config{
        InsecureSkipVerify: true,
        MinVersion:         tls.VersionTLS13,
        MaxVersion:         tls.VersionTLS13,
        NextProtos:         []string{"h2", "http/1.1"},
    })
    if err != nil {
        t.Fatalf("tls.Dial with ALPN h2,http/1.1: %v", err)
    }
    defer conn.Close()

    negotiated := conn.ConnectionState().NegotiatedProtocol
    if negotiated == "h2" {
        t.Errorf("wolfSSL negotiated h2 over ALPN; net/http.Server only speaks HTTP/1.1, this would break browsers")
    }
    /* Either "" (server offered nothing) or "http/1.1" is OK. */
}

func bodyMustNotContain(t *testing.T, resp *http.Response, marker string) {
    t.Helper()
    if resp == nil || resp.Body == nil {
        return
    }
    b, err := io.ReadAll(resp.Body)
    if err != nil {
        return
    }
    resp.Body = io.NopCloser(strings.NewReader(string(b)))
    if strings.Contains(string(b), marker) {
        t.Errorf("response body contained %q: %s", marker, b)
    }
}
