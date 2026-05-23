package agente2e_test

/* tests/agent_e2e_test.go - whole-binary end-to-end gate for
 * PLAN.md 5.2.
 *
 * Stands up an in-process agentsvc.Server behind a wolfSSL mTLS
 * listener, queues a JobAssignment, builds a fresh wolfci-agent
 * binary into the test's temp dir, then exec's that binary as a
 * subprocess pointed at the listener. Verifies that:
 *
 *   1. The agent registers with the server.
 *   2. The agent picks up the queued JobAssignment off its
 *      Connect stream.
 *   3. The agent runs the shell step in its work_dir and reports
 *      BuildComplete with status "success".
 *
 * Companion to internal/agentsvc/connect_test.go, which exercises
 * the same gRPC + mTLS path with an in-process client. This test
 * adds the binary surface that the internal test cannot reach:
 * argv parsing, on-disk YAML config loading, and the
 * cmd/wolfci-agent main loop.
 *
 * Skip conditions:
 *   - testing.Short(): the build step takes a few seconds.
 *   - build/wolfssl-install/lib/libwolfssl.a missing: cgo build
 *     would fail; skip rather than report a confusing error.
 */

import (
    "context"
    "crypto/tls"
    "fmt"
    "net"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "syscall"
    "testing"
    "time"

    "google.golang.org/grpc"
    "gopkg.in/yaml.v3"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
    "github.com/kaleb-himes/wolfCI/internal/agent"
    "github.com/kaleb-himes/wolfCI/internal/agentsvc"
    "github.com/kaleb-himes/wolfCI/internal/testcerts"
    "github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

func TestAgentBinary_RegistersAndRunsJob(t *testing.T) {
    if testing.Short() {
        t.Skip("e2e: builds a subprocess; skipped in -short")
    }
    repoRoot := findRepoRoot(t)
    libPath := filepath.Join(repoRoot, "build",
        "wolfssl-install", "lib", "libwolfssl.a")
    if _, err := os.Stat(libPath); err != nil {
        t.Skipf("e2e: wolfSSL static lib not built (%s); run "+
            "scripts/build-wolfssl.sh first", libPath)
    }

    workDir := t.TempDir()
    chain := testcerts.NewMTLSChain(t)

    /* Server side: a wolfSSL mTLS listener wraps a plain TCP
     * socket on an ephemeral port, then we serve gRPC on top.
     * StaleThreshold drops to 2s so ConnectedAgents reflects
     * the subprocess agent's heartbeats well inside the test
     * deadline.
     */
    inner, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("net.Listen: %v", err)
    }
    defer inner.Close()

    serverLn, err := tlsutil.NewListener(inner, &tlsutil.Config{
        Certificate: chain.ServerCert,
        Key:         chain.ServerKey,
        ClientCAs:   chain.CACert,
        MinVersion:  tls.VersionTLS13,
    })
    if err != nil {
        t.Fatalf("tlsutil.NewListener: %v", err)
    }
    defer serverLn.Close()

    svc := agentsvc.New("e2e-binary-test")
    svc.StaleThreshold = 2 * time.Second
    svc.QueueJob(&wolfciv1.JobAssignment{
        JobName:     "e2e-demo",
        BuildNumber: 1,
        Steps:       []*wolfciv1.Step{{Shell: "true"}},
    })

    grpcSrv := grpc.NewServer()
    wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
    go func() { _ = grpcSrv.Serve(serverLn) }()
    defer grpcSrv.Stop()

    /* Cert files plus agent.yaml all live under the test's temp
     * dir so the subprocess sees the same trust chain we just
     * minted.
     */
    certPath := filepath.Join(workDir, "agent.pem")
    keyPath := filepath.Join(workDir, "agent.key")
    caPath := filepath.Join(workDir, "ca.pem")
    cfgPath := filepath.Join(workDir, "agent.yaml")
    agentWorkDir := filepath.Join(workDir, "work")
    writeFile(t, certPath, chain.AgentCert)
    writeFile(t, keyPath, chain.AgentKey)
    writeFile(t, caPath, chain.CACert)

    cfg := &agent.Config{
        AgentID:           "e2e-binary-agent",
        ServerAddress:     inner.Addr().String(),
        Executors:         1,
        Certificate:       certPath,
        Key:               keyPath,
        CACertificate:     caPath,
        WorkDir:           agentWorkDir,
        HeartbeatInterval: "200ms",
    }
    cfgBytes, err := yaml.Marshal(cfg)
    if err != nil {
        t.Fatalf("yaml.Marshal: %v", err)
    }
    writeFile(t, cfgPath, cfgBytes)

    /* Build the wolfci-agent binary into the temp dir. Doing
     * the build inside the test (rather than reusing a stale
     * artifact from build/bin/) keeps the test self-contained
     * and matches the pattern internal/plugin/plugin_test.go
     * uses for its example-plugin build.
     */
    binPath := filepath.Join(workDir, "wolfci-agent")
    if runtime.GOOS == "windows" {
        binPath += ".exe"
    }
    build := exec.Command("go", "build", "-o", binPath, ".")
    build.Dir = filepath.Join(repoRoot, "cmd", "wolfci-agent")
    if out, err := build.CombinedOutput(); err != nil {
        t.Fatalf("go build wolfci-agent: %v\n%s", err, out)
    }

    /* Spawn the agent subprocess. The test owns it via the
     * context's cancel and a SIGTERM in the deferred cleanup
     * so a t.Fatal never leaves an orphan agent behind.
     */
    ctx, cancel := context.WithTimeout(context.Background(),
        30*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, binPath, "-config", cfgPath)
    cmd.Stdout = &prefixWriter{prefix: "[agent stdout] "}
    cmd.Stderr = &prefixWriter{prefix: "[agent stderr] "}
    if err := cmd.Start(); err != nil {
        t.Fatalf("start agent: %v", err)
    }
    defer func() {
        _ = cmd.Process.Signal(syscall.SIGTERM)
        _ = cmd.Wait()
    }()

    /* Wait for the agent to be visible as a ConnectedAgent
     * (heartbeat-fresh) before checking the build completion.
     */
    if !waitFor(15*time.Second, func() bool {
        for _, a := range svc.ConnectedAgents() {
            if a.AgentId == "e2e-binary-agent" {
                return true
            }
        }
        return false
    }) {
        t.Fatalf("agent never connected to server within timeout")
    }

    /* Wait for the agent to report BuildComplete for the queued
     * job.
     */
    if !waitFor(15*time.Second, func() bool {
        for _, c := range svc.Completed() {
            if c.BuildNumber == 1 && c.Status == "success" {
                return true
            }
        }
        return false
    }) {
        t.Fatalf("agent never reported success for build 1; "+
            "got %v", svc.Completed())
    }
}

/* waitFor polls cond every 50ms until it returns true or d
 * elapses. Returns true if cond fired before the deadline. The
 * trailing cond() call covers the case where the deadline fires
 * between the loop's last sleep and the next time check.
 */
func waitFor(d time.Duration, cond func() bool) bool {
    deadline := time.Now().Add(d)
    for time.Now().Before(deadline) {
        if cond() {
            return true
        }
        time.Sleep(50 * time.Millisecond)
    }
    return cond()
}

/* findRepoRoot walks up from this source file until it sees a
 * go.mod, returning that directory. Aborts the test on failure
 * so the rest of the body never sees an empty path.
 */
func findRepoRoot(t *testing.T) string {
    t.Helper()
    _, thisFile, _, ok := runtime.Caller(0)
    if !ok {
        t.Fatalf("runtime.Caller failed")
    }
    dir := filepath.Dir(thisFile)
    for i := 0; i < 6; i++ {
        if _, err := os.Stat(
            filepath.Join(dir, "go.mod")); err == nil {
            return dir
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            break
        }
        dir = parent
    }
    t.Fatalf("could not find go.mod walking up from %s", thisFile)
    return ""
}

func writeFile(t *testing.T, path string, data []byte) {
    t.Helper()
    if err := os.WriteFile(path, data, 0o600); err != nil {
        t.Fatalf("write %s: %v", path, err)
    }
}

/* prefixWriter is a tiny io.Writer that tags every line of agent
 * subprocess output with a fixed prefix so a failed run is easy
 * to diagnose from the test log.
 */
type prefixWriter struct {
    prefix string
}

func (w *prefixWriter) Write(p []byte) (int, error) {
    text := strings.ReplaceAll(string(p), "\n", "\n"+w.prefix)
    fmt.Print(w.prefix + text)
    return len(p), nil
}
