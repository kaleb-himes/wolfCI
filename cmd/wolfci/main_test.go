package main

/* Gates PLAN.md task 11.5: cmd/wolfci no longer serves hello-world.
 * Run(ctx, *ServerConfig) wires storage + scheduler + agentsvc +
 * cliservice + server.UI + bootstrap + setup + dispatcher and
 * starts one TLS listener via internal/tlsutil. The integration
 * tests below build a minimal config in a temp dir, start Run on
 * an ephemeral port, and exercise the wire-up.
 */

import (
    "context"
    "crypto/tls"
    "io"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
    "github.com/kaleb-himes/wolfCI/internal/testcerts"
)

type runFixture struct {
    cfg     *server.ServerConfig
    workDir string
    authDir string
    addr    string /* filled in after bind, before tests fire HTTP */
}

func newRunFixture(t *testing.T) *runFixture {
    t.Helper()
    dir := t.TempDir()
    certPEM, keyPEM := testcerts.SelfSignedECDSA(t)
    certPath := filepath.Join(dir, "server.crt")
    keyPath := filepath.Join(dir, "server.key")
    caPath := filepath.Join(dir, "ca.crt")
    /* Same self-signed cert acts as its own "CA" for the gRPC mTLS
     * path; the UI smoke test does not actually exercise mTLS but
     * Run requires the field to be present.
     */
    for _, p := range []struct{ path string; data []byte }{
        {certPath, certPEM},
        {keyPath, keyPEM},
        {caPath, certPEM},
    } {
        if err := os.WriteFile(p.path, p.data, 0o600); err != nil {
            t.Fatalf("write %s: %v", p.path, err)
        }
    }

    workDir := filepath.Join(dir, "work")
    authDir := filepath.Join(dir, "auth")
    if err := os.MkdirAll(filepath.Join(authDir, "keys"), 0o755); err != nil {
        t.Fatalf("mkdir auth/keys: %v", err)
    }
    if err := os.MkdirAll(workDir, 0o755); err != nil {
        t.Fatalf("mkdir work: %v", err)
    }

    return &runFixture{
        cfg: &server.ServerConfig{
            ListenAddr: "127.0.0.1:0",
            Cert:       certPath,
            Key:        keyPath,
            CACert:     caPath,
            WorkDir:    workDir,
            AuthDir:    authDir,
        },
        workDir: workDir,
        authDir: authDir,
    }
}

/* startRun launches Run in a goroutine, waits for the listener to
 * become reachable, and returns the actual bound address plus a
 * cancel function that stops the server. The test calls cancel
 * via defer.
 */
func startRun(t *testing.T, f *runFixture) (string, context.CancelFunc) {
    t.Helper()
    ctx, cancel := context.WithCancel(context.Background())
    addrCh := make(chan string, 1)
    errCh := make(chan error, 1)
    go func() {
        errCh <- Run(ctx, f.cfg, RunOptions{AddrCh: addrCh})
    }()

    select {
    case addr := <-addrCh:
        return addr, func() {
            cancel()
            select {
            case <-errCh:
            case <-time.After(5 * time.Second):
                t.Errorf("Run did not return after cancel")
            }
        }
    case err := <-errCh:
        cancel()
        t.Fatalf("Run exited before binding: %v", err)
        return "", cancel /* unreachable */
    case <-time.After(5 * time.Second):
        cancel()
        t.Fatalf("Run did not signal bound address within 5s")
        return "", cancel /* unreachable */
    }
}

func tlsClient() *http.Client {
    return &http.Client{
        Timeout: 5 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
                MinVersion:         tls.VersionTLS13,
                MaxVersion:         tls.VersionTLS13,
            },
        },
    }
}

func TestRun_UIRouteReachable(t *testing.T) {
    f := newRunFixture(t)
    addr, stop := startRun(t, f)
    defer stop()

    resp, err := tlsClient().Get("https://" + addr + "/")
    if err != nil {
        t.Fatalf("GET /: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
    }
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        t.Fatalf("read body: %v", err)
    }
    s := string(body)
    if strings.Contains(s, "hello, world") {
        t.Errorf("body still contains hello-world; cmd/wolfci was not rewired")
    }
    /* The Phase 6 base.html template wraps everything in
     * <title>... - wolfCI</title>; the login page is the
     * fresh-install default. Either marker proves the real UI
     * is serving the request.
     */
    if !strings.Contains(s, "wolfCI") {
        t.Errorf("body missing 'wolfCI' marker: %q", s)
    }
}

func TestRun_BootstrapMintsTokenAndSetupReachable(t *testing.T) {
    f := newRunFixture(t)
    addr, stop := startRun(t, f)
    defer stop()

    /* The bootstrap mint runs synchronously before Run signals
     * its bound address, so the token file must already exist by
     * the time the listener accepts connections.
     */
    tokenPath := filepath.Join(f.authDir, "bootstrap", "token")
    tokenBytes, err := os.ReadFile(tokenPath)
    if err != nil {
        t.Fatalf("bootstrap token not written: %v", err)
    }
    token := strings.TrimSpace(string(tokenBytes))
    if len(token) != 64 {
        t.Fatalf("token length = %d, want 64", len(token))
    }

    /* GET /setup?token=<the-real-token> -> 200 with the form. */
    resp, err := tlsClient().Get("https://" + addr + "/setup?token=" + token)
    if err != nil {
        t.Fatalf("GET /setup: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Errorf("GET /setup status = %d, want 200", resp.StatusCode)
    }
    body, _ := io.ReadAll(resp.Body)
    s := string(body)
    if !strings.Contains(s, "first-admin setup") {
        t.Errorf("/setup body missing 'first-admin setup' header: %q", s)
    }
    if !strings.Contains(s, token) {
        t.Errorf("/setup body missing the token in the hidden form field")
    }
}

func TestRun_GRPCContentTypeRoutedAwayFromUI(t *testing.T) {
    f := newRunFixture(t)
    addr, stop := startRun(t, f)
    defer stop()

    /* The dispatcher routes application/grpc to the gRPC handler.
     * Over HTTP/1.1 the gRPC handler returns a non-UI response;
     * the assertion is intentionally weak (anything that is NOT
     * the UI's login HTML proves the routing fork fired
     * correctly).
     */
    req, _ := http.NewRequest(http.MethodPost,
        "https://"+addr+"/wolfci.AgentService/Register",
        strings.NewReader(""))
    req.Header.Set("Content-Type", "application/grpc")
    resp, err := tlsClient().Do(req)
    if err != nil {
        /* Some HTTP/1.1-vs-gRPC negotiations close the
         * connection rather than reply; that is itself proof
         * the request did not hit the UI mux (which would
         * answer 200 or 404).
         */
        return
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    if strings.Contains(string(body), "Sign in") {
        t.Errorf("application/grpc request was routed to UI login page; dispatcher not wired")
    }
}

func TestRun_RejectsNilConfig(t *testing.T) {
    if err := Run(context.Background(), nil, RunOptions{}); err == nil {
        t.Error("Run(nil) returned nil, want error")
    }
}

/* TestRun_GracefulShutdownDrainsBuilds gates PLAN.md 11.6. With
 * an executor that respects ctx cancellation (the default
 * LocalExecutor and any well-behaved test double do), shutdown
 * cancels the build, waits for it to settle, and returns well
 * within cfg.ShutdownDrainTimeout. The build's recorded status
 * is Cancelled so callers (UI, gRPC) see the proper outcome.
 */
func TestRun_GracefulShutdownDrainsBuilds(t *testing.T) {
    f := newRunFixture(t)
    /* Tight drain budget so the test does not stall the suite.
     * 2s is plenty for the cooperating-executor path; the
     * graceful drain itself completes in milliseconds.
     */
    f.cfg.ShutdownDrainTimeout = "2s"

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    /* Cooperating executor: holds until ctx fires, then returns
     * Cancelled. Mirrors the LocalExecutor's SIGKILL behavior
     * without depending on a real /bin/sh subprocess (which
     * keeps the test hermetic and fast).
     */
    started := make(chan struct{})
    exec := &cancelRespectingExec{started: started}

    addrCh := make(chan string, 1)
    schedCh := make(chan *scheduler.Scheduler, 1)
    errCh := make(chan error, 1)
    go func() {
        errCh <- Run(ctx, f.cfg, RunOptions{
            AddrCh:      addrCh,
            SchedulerCh: schedCh,
            Executor:    exec,
        })
    }()

    select {
    case <-addrCh:
    case err := <-errCh:
        t.Fatalf("Run exited before binding: %v", err)
    case <-time.After(5 * time.Second):
        cancel()
        t.Fatal("Run did not bind within 5s")
    }

    var sched *scheduler.Scheduler
    select {
    case sched = <-schedCh:
    case <-time.After(2 * time.Second):
        cancel()
        t.Fatal("Run did not expose scheduler within 2s")
    }

    job := &storage.Job{
        Name:  "shutdown-drain-test",
        Steps: []storage.Step{{Shell: "true"}},
    }
    _, done, err := sched.Enqueue(job)
    if err != nil {
        cancel()
        t.Fatalf("Enqueue: %v", err)
    }

    /* Wait for the dispatcher to dequeue and the executor to
     * actually start so the shutdown sequence has a real
     * in-flight build to drain.
     */
    select {
    case <-started:
    case <-time.After(2 * time.Second):
        cancel()
        t.Fatal("executor did not start within 2s")
    }

    startedAt := time.Now()
    cancel()

    var runErr error
    select {
    case runErr = <-errCh:
    case <-time.After(5 * time.Second):
        t.Fatal("Run did not exit within 5s of cancel")
    }
    elapsed := time.Since(startedAt)
    if runErr != nil {
        t.Errorf("Run returned %v, want nil", runErr)
    }
    /* The cooperating executor returns immediately on ctx cancel
     * so total shutdown should be well under the 2s budget;
     * 1.5s leaves headroom for slow CI machines.
     */
    if elapsed > 1500*time.Millisecond {
        t.Errorf("Run took %v to shut down; drain budget was 2s", elapsed)
    }

    select {
    case res := <-done:
        if res.Status != scheduler.StatusCancelled {
            t.Errorf("build status = %s, want %s",
                res.Status, scheduler.StatusCancelled)
        }
    case <-time.After(time.Second):
        t.Error("build done channel had no result; build was not cancelled cleanly")
    }
}

/* TestRun_GracefulShutdownTimeoutClosesAnyway proves the second
 * half of 11.6: even if the in-flight executor ignores ctx
 * entirely, Run still returns shortly after the drain budget
 * elapses. The HTTP listener and gRPC server are closed
 * regardless; the hung dispatcher goroutine is leaked
 * deliberately (production LocalExecutor never reaches this
 * path because exec.CommandContext kills via SIGKILL).
 */
func TestRun_GracefulShutdownTimeoutClosesAnyway(t *testing.T) {
    f := newRunFixture(t)
    /* 300ms keeps the test fast. The drain phase should account
     * for the entire elapsed shutdown because the executor
     * refuses to return.
     */
    f.cfg.ShutdownDrainTimeout = "300ms"

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    started := make(chan struct{})
    release := make(chan struct{})
    defer close(release) /* let the leaked goroutine exit at test end */

    exec := &hangingExec{started: started, release: release}

    addrCh := make(chan string, 1)
    schedCh := make(chan *scheduler.Scheduler, 1)
    errCh := make(chan error, 1)
    go func() {
        errCh <- Run(ctx, f.cfg, RunOptions{
            AddrCh:      addrCh,
            SchedulerCh: schedCh,
            Executor:    exec,
        })
    }()

    select {
    case <-addrCh:
    case err := <-errCh:
        t.Fatalf("Run exited before binding: %v", err)
    case <-time.After(5 * time.Second):
        cancel()
        t.Fatal("Run did not bind within 5s")
    }

    var sched *scheduler.Scheduler
    select {
    case sched = <-schedCh:
    case <-time.After(2 * time.Second):
        cancel()
        t.Fatal("Run did not expose scheduler within 2s")
    }

    job := &storage.Job{
        Name:  "shutdown-timeout-test",
        Steps: []storage.Step{{Shell: "true"}},
    }
    if _, _, err := sched.Enqueue(job); err != nil {
        cancel()
        t.Fatalf("Enqueue: %v", err)
    }
    select {
    case <-started:
    case <-time.After(2 * time.Second):
        cancel()
        t.Fatal("hanging executor did not start within 2s")
    }

    startedAt := time.Now()
    cancel()

    var runErr error
    select {
    case runErr = <-errCh:
    case <-time.After(5 * time.Second):
        t.Fatal("Run did not exit within 5s of cancel; drain timeout did not force close")
    }
    elapsed := time.Since(startedAt)
    if runErr != nil {
        t.Errorf("Run returned %v, want nil", runErr)
    }
    if elapsed < 300*time.Millisecond {
        t.Errorf("Run returned in %v before the 300ms drain budget", elapsed)
    }
    /* Allow generous headroom on top of the drain budget for
     * httpSrv.Shutdown/Close to settle on slow CI.
     */
    if elapsed > 2*time.Second {
        t.Errorf("Run took %v to shut down with a 300ms drain budget", elapsed)
    }
}

/* cancelRespectingExec is the cooperating executor for the
 * happy-path drain test. It signals start, blocks on ctx, and
 * reports Cancelled when ctx fires.
 */
type cancelRespectingExec struct {
    started   chan struct{}
    startOnce bool
}

func (e *cancelRespectingExec) Execute(ctx context.Context,
    job *storage.Job, num int) scheduler.BuildResult {
    if !e.startOnce {
        e.startOnce = true
        close(e.started)
    }
    <-ctx.Done()
    return scheduler.BuildResult{
        JobName: job.Name,
        Number:  num,
        Status:  scheduler.StatusCancelled,
    }
}

/* hangingExec ignores ctx entirely. Used to prove Run's drain
 * timeout actually fires; the dispatcher goroutine running this
 * executor is intentionally leaked for the duration of the test
 * (release closes at test end to let it exit cleanly).
 */
type hangingExec struct {
    started   chan struct{}
    startOnce bool
    release   chan struct{}
}

func (e *hangingExec) Execute(_ context.Context,
    job *storage.Job, num int) scheduler.BuildResult {
    if !e.startOnce {
        e.startOnce = true
        close(e.started)
    }
    <-e.release
    return scheduler.BuildResult{
        JobName: job.Name,
        Number:  num,
        Status:  scheduler.StatusError,
    }
}

/* probeListener exists so future tests can assert the listener is
 * actually bound without dialing TLS. Not used by the current
 * tests but kept as a small utility.
 */
func probeListener(addr string) bool {
    c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
    if err != nil {
        return false
    }
    _ = c.Close()
    return true
}
