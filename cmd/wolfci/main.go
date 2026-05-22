/* Package main is the wolfCI server binary.
 *
 * Phase 11.5 wired the full dependency graph: storage layer,
 * scheduler, agent service, CLI service, plugin host (TODO),
 * server.UI, first-admin bootstrap, /setup endpoint, and the
 * single-port HTTP+gRPC dispatcher all share one TLS listener
 * via internal/tlsutil.
 */
package main

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "log"
    "net"
    "net/http"
    "os"
    "os/signal"
    "path/filepath"
    "syscall"
    "time"

    "google.golang.org/grpc"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
    cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
    "github.com/kaleb-himes/wolfCI/internal/agentsvc"
    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/cliservice"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
    "github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

/* serverVersion is the value reported to agents during Register.
 * Tied to the wolfCI release cadence once that exists; for now,
 * "dev" is the marker for "not yet released".
 */
const serverVersion = "wolfCI/dev"

/* defaultSessionTTL is how long a UI session cookie stays valid.
 * 24h is a reasonable middle ground - long enough that operators
 * are not retyping creds every shift, short enough that a stolen
 * cookie has a bounded blast radius.
 */
const defaultSessionTTL = "24h"

/* RunOptions is the optional plumbing surface for callers of Run.
 * Production main passes a zero value; tests use it for race-free
 * observation of the bound address, access to the live scheduler
 * (so a long-running build can be enqueued before signalling
 * shutdown), and Executor injection (so a non-cancellable build
 * can exercise the drain-timeout-anyway path).
 */
type RunOptions struct {
    /* AddrCh, if non-nil, receives rawLn.Addr().String() as soon
     * as the TLS listener is bound. Tests use it to know exactly
     * when to start firing HTTP requests instead of polling.
     */
    AddrCh chan<- string

    /* SchedulerCh, if non-nil, receives the live
     * *scheduler.Scheduler immediately after sched.Start. Tests
     * use it to enqueue a known long-running job before sending
     * the shutdown signal so the drain semantics are exercised
     * end-to-end.
     */
    SchedulerCh chan<- *scheduler.Scheduler

    /* Executor, if non-nil, replaces the default LocalExecutor.
     * Tests use it to install an executor that ignores ctx
     * cancellation, which is the only way to make Run's drain
     * timeout actually fire (LocalExecutor + exec.CommandContext
     * would otherwise kill the subprocess via SIGKILL well
     * inside the drain budget).
     */
    Executor scheduler.Executor
}

/* Run is the wolfCI server entry point. Tests call this directly
 * with an in-memory config; main wraps it with --config parsing
 * and signal handling.
 *
 * opts carries optional test hooks; production main passes a
 * zero-value RunOptions.
 */
func Run(ctx context.Context, cfg *server.ServerConfig, opts RunOptions) error {
    if cfg == nil {
        return errors.New("wolfci.Run: nil ServerConfig")
    }
    if err := cfg.Validate(); err != nil {
        return fmt.Errorf("wolfci.Run: %w", err)
    }

    /* Storage + auth + sessions. The auth config file is optional;
     * defaults disable password auth (SSH-key-only).
     */
    store, err := storage.New(cfg.WorkDir)
    if err != nil {
        return fmt.Errorf("wolfci.Run: storage.New: %w", err)
    }

    authCfg := auth.DefaultConfig()
    authConfigPath := filepath.Join(cfg.AuthDir, "config.yaml")
    if _, statErr := os.Stat(authConfigPath); statErr == nil {
        loaded, loadErr := auth.LoadConfig(authConfigPath)
        if loadErr != nil {
            return fmt.Errorf("wolfci.Run: auth.LoadConfig: %w", loadErr)
        }
        authCfg = loaded
    }

    passwords := auth.NewPasswordStore(
        filepath.Join(cfg.AuthDir, "passwords"), authCfg)

    sessionsTTL, _ := parseDurationOr(defaultSessionTTL)
    sessions := server.NewSessionStore(
        filepath.Join(cfg.WorkDir, "sessions"), sessionsTTL)

    /* Agent service + scheduler. The local executor handles jobs
     * that label "local" or carry no node_label. Phase 5.x's
     * Router would also dispatch to remote agents; wiring that
     * in lands when the first real on-prem agent does.
     */
    svc := agentsvc.New(serverVersion)
    svc.SetLogSink(agentsvc.NewFileLogSink(cfg.WorkDir))

    var exec scheduler.Executor = scheduler.NewLocalExecutor(store)
    if opts.Executor != nil {
        exec = opts.Executor
    }
    sched := scheduler.New(store, exec)
    sched.Start(ctx)
    /* Safety net: bound the worst case (a hung executor) so any
     * error-return path can never wedge function exit. The
     * ctx.Done success branch already drains within
     * cfg.ShutdownDrainTimeout; idle paths return instantly via
     * the same call.
     */
    defer func() {
        _ = sched.Drain(time.Second)
    }()
    if opts.SchedulerCh != nil {
        opts.SchedulerCh <- sched
    }

    /* cliservice for wolfci-ctl. EnqueuerFunc adapts the
     * scheduler's three-return Enqueue to the cli's two-return
     * shape.
     */
    cli := cliservice.New(store, svc).WithEnqueuer(
        cliservice.EnqueuerFunc(func(job *storage.Job) (int, error) {
            num, _, err := sched.Enqueue(job)
            return num, err
        }))

    /* UI handler from Phase 6. The JobRunner adapter wraps the
     * scheduler's three-return Enqueue (build number, completion
     * channel, error) into the two-return shape the UI's Run
     * button needs.
     */
    uiSrv := server.New(server.Options{
        Storage:      store,
        Auth:         authCfg,
        Passwords:    passwords,
        Sessions:     sessions,
        CookieSecure: true,
        AgentSvc:     svc,
        JobRunner: server.JobRunnerFunc(func(job *storage.Job) (int, error) {
            num, _, err := sched.Enqueue(job)
            return num, err
        }),
    })

    /* First-admin bootstrap: prints a setup URL to stdout if no
     * admins are on disk yet. Per BYOK, no keypair is generated;
     * the operator pastes their own pubkey at /setup.
     */
    bs := &server.Bootstrap{
        KeysDir:      filepath.Join(cfg.AuthDir, "keys"),
        BootstrapDir: filepath.Join(cfg.AuthDir, "bootstrap"),
        ListenAddr:   cfg.ListenAddr,
    }
    mintRes, err := bs.Mint()
    if err != nil {
        return fmt.Errorf("wolfci.Run: bootstrap mint: %w", err)
    }
    if mintRes != nil {
        fmt.Println("wolfci: first-admin setup URL:")
        fmt.Println("  " + mintRes.SetupURL)
    }

    setupHandler := &server.SetupHandler{
        KeysDir:        filepath.Join(cfg.AuthDir, "keys"),
        BootstrapDir:   filepath.Join(cfg.AuthDir, "bootstrap"),
        MatrixPath:     filepath.Join(cfg.AuthDir, "matrix.yaml"),
        Passwords:      passwords,
        AuthConfig:     authCfg,
        AuthConfigPath: authConfigPath,
    }

    /* Top-level UI mux: /setup routes around the auth gate to the
     * one-shot bootstrap form; everything else falls through to
     * the authenticated UI tree.
     */
    topMux := http.NewServeMux()
    topMux.Handle("/setup", setupHandler)
    topMux.Handle("/setup/", setupHandler)
    topMux.Handle("/", uiSrv)

    /* gRPC server hosting agent + cli services on the same TCP
     * port as the UI; the dispatcher fork is Content-Type based.
     */
    grpcSrv := grpc.NewServer()
    wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
    cliv1.RegisterCLIServiceServer(grpcSrv, cli)

    dispatcher := &server.Dispatcher{UI: topMux, GRPC: grpcSrv}

    /* TLS listener. tlsutil wraps a raw net.Listener with
     * wolfSSL-terminated TLS 1.3.
     */
    cert, err := os.ReadFile(cfg.Cert)
    if err != nil {
        return fmt.Errorf("wolfci.Run: read cert: %w", err)
    }
    key, err := os.ReadFile(cfg.Key)
    if err != nil {
        return fmt.Errorf("wolfci.Run: read key: %w", err)
    }
    /* CACert is loaded eagerly so a fresh install with a missing
     * file fails at startup, not at first agent connect. mTLS
     * itself is NOT enforced at the TLS layer because the same
     * listener serves browsers (no client cert) and gRPC clients
     * (with client cert); per-request authz on the gRPC handler
     * will enforce the client-cert -> matrix mapping in a follow-
     * up phase.
     */
    if _, err := os.ReadFile(cfg.CACert); err != nil {
        return fmt.Errorf("wolfci.Run: read ca_cert: %w", err)
    }

    rawLn, err := net.Listen("tcp", cfg.ListenAddr)
    if err != nil {
        return fmt.Errorf("wolfci.Run: net.Listen: %w", err)
    }
    ln, err := tlsutil.NewListener(rawLn, &tlsutil.Config{
        Certificate: cert,
        Key:         key,
        MinVersion:  tlsutil.VersionTLS13,
    })
    if err != nil {
        _ = rawLn.Close()
        return fmt.Errorf("wolfci.Run: tlsutil.NewListener: %w", err)
    }
    defer ln.Close()

    if opts.AddrCh != nil {
        opts.AddrCh <- rawLn.Addr().String()
    }

    httpSrv := &http.Server{Handler: dispatcher}
    /* Keep-alive is disabled at the http.Server level because the
     * underlying tlsutil net.Conn (wolfSSL CGO) misframes data on
     * connection reuse: the second request on a keep-alive conn
     * comes through as a malformed request line and the server
     * replies 400, which Firefox surfaces as "Secure Connection
     * Failed". Connection: close on every response sidesteps the
     * issue; the real fix is the PLAN.md backlog item to harden
     * internal/tlsutil's per-conn serialization, after which this
     * line can be removed.
     */
    httpSrv.SetKeepAlivesEnabled(false)
    serveErr := make(chan error, 1)
    go func() { serveErr <- httpSrv.Serve(ln) }()

    select {
    case <-ctx.Done():
        /* Graceful shutdown (PLAN.md 11.6). cfg.DrainTimeout()
         * bounds how long we wait for the in-flight build (via
         * sched.Drain) AND for in-flight HTTP requests (via
         * httpSrv.Shutdown). Both run within the same time
         * budget: scheduler first because builds dominate the
         * shutdown latency, then Shutdown for what remains.
         * Drain budget is consumed by whichever phase needs it;
         * if a phase exceeds its share, we force-close and
         * accept the abrupt termination. Production executors
         * (LocalExecutor + exec.CommandContext) cancel via
         * SIGKILL so the scheduler returns fast and the HTTP
         * phase gets most of the budget.
         */
        drainTimeout, _ := cfg.DrainTimeout()
        deadline := time.Now().Add(drainTimeout)

        _ = sched.Drain(drainTimeout)

        remaining := time.Until(deadline)
        if remaining < 0 {
            remaining = 0
        }
        shutCtx, shutCancel := context.WithTimeout(
            context.Background(), remaining)
        if err := httpSrv.Shutdown(shutCtx); err != nil {
            /* Shutdown returned ctx.DeadlineExceeded or some
             * other error; force-close so the serve goroutine
             * returns.
             */
            _ = httpSrv.Close()
        }
        shutCancel()
        <-serveErr
        return nil
    case err := <-serveErr:
        if errors.Is(err, http.ErrServerClosed) {
            return nil
        }
        return fmt.Errorf("wolfci.Run: http.Server.Serve: %w", err)
    }
}

func main() {
    var configPath string
    flag.StringVar(&configPath, "config", "",
        "path to server.yaml (production mode)")
    flag.Usage = printUsage
    flag.Parse()

    cfg, err := resolveConfig(configPath, flag.Args())
    if err != nil {
        log.Fatalf("wolfci: %v", err)
    }

    ctx, cancel := signal.NotifyContext(context.Background(),
        os.Interrupt, syscall.SIGTERM)
    defer cancel()

    if err := Run(ctx, cfg, RunOptions{}); err != nil {
        log.Fatalf("wolfci: %v", err)
    }
}

/* resolveConfig picks between the two supported CLI shapes:
 *
 *   wolfci --config <path>      Production: load YAML from disk.
 *   wolfci <addr> <cert> <key>  Quick: inline config with CWD-
 *                               relative work/auth dirs and the
 *                               cert acting as its own CA.
 *
 * Both shapes feed the same Run() function; the quick shape just
 * dodges the requirement to author a YAML before a first run.
 */
func resolveConfig(configPath string, args []string) (*server.ServerConfig, error) {
    if configPath != "" {
        if len(args) > 0 {
            return nil, fmt.Errorf("--config is exclusive with positional args; got %d extra args", len(args))
        }
        return server.LoadServerConfig(configPath)
    }
    if len(args) == 3 {
        cwd, err := os.Getwd()
        if err != nil {
            return nil, fmt.Errorf("getwd: %w", err)
        }
        cfg := server.DefaultServerConfig()
        cfg.ListenAddr = args[0]
        cfg.Cert = args[1]
        cfg.Key = args[2]
        /* Quick mode: the server cert is its own CA. Single-node
         * trust; fine for local development and the basic-test-*
         * scripts. Production should set ca_cert explicitly in
         * server.yaml.
         */
        cfg.CACert = args[1]
        cfg.WorkDir = filepath.Join(cwd, "work")
        cfg.AuthDir = filepath.Join(cwd, "auth")
        if err := cfg.Validate(); err != nil {
            return nil, err
        }
        return cfg, nil
    }
    return nil, fmt.Errorf("invalid invocation; see -h for usage")
}

func printUsage() {
    out := flag.CommandLine.Output()
    fmt.Fprintln(out, "wolfCI server")
    fmt.Fprintln(out)
    fmt.Fprintln(out, "Usage:")
    fmt.Fprintln(out, "  wolfci --config <path-to-server.yaml>")
    fmt.Fprintln(out, "  wolfci <host:port> <cert.pem> <key.pem>")
    fmt.Fprintln(out)
    fmt.Fprintln(out, "Quick mode (positional) uses ./work and ./auth for runtime")
    fmt.Fprintln(out, "state and treats <cert.pem> as its own CA. Use --config for")
    fmt.Fprintln(out, "production deployments where work_dir, auth_dir, and ca_cert")
    fmt.Fprintln(out, "should differ from the defaults.")
    fmt.Fprintln(out)
    fmt.Fprintln(out, "On first start, wolfci prints a /setup URL to stdout. Visit")
    fmt.Fprintln(out, "it, paste your OpenSSH public key, and you become the first")
    fmt.Fprintln(out, "admin (BYOK: wolfci never generates user keypairs).")
}

/* parseDurationOr returns time.ParseDuration(s); on failure it
 * returns 0 and the parse error rather than panicking. Used for
 * compile-time-known defaults where the panic-free branch is
 * dead but documents intent.
 */
func parseDurationOr(s string) (time.Duration, error) {
    return time.ParseDuration(s)
}
