package scheduler_test

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agent"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestRouter_SkipsOfflineNode gates PLAN.md 12.7. With the
// built-in master registered as a local label, a job labeled
// "master" normally flows through the LocalExecutor. After
// SetDisabled(BuiltInNodeAgentID, true) the Router must
// refuse to dispatch so the operator can pull the host out of
// service without taking the whole wolfCI server down.
func TestRouter_SkipsOfflineNode(t *testing.T) {
	svc := agentsvc.New("router-disabled-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.RegisterBuiltInNode(ctx, 100*time.Millisecond, t.TempDir())
	svc.SetDisabled(agentsvc.BuiltInNodeAgentID, true)

	local := &fakeExecutor{
		result: scheduler.BuildResult{Status: scheduler.StatusSuccess},
	}
	r := scheduler.NewRouter(svc, local, []string{agentsvc.BuiltInNodeLabel})

	job := &storage.Job{Name: "demo", NodeLabel: agentsvc.BuiltInNodeLabel}
	result := r.Execute(context.Background(), job, 1)

	if result.Status != scheduler.StatusError {
		t.Errorf("Status = %s, want %s",
			result.Status, scheduler.StatusError)
	}
	if !strings.Contains(result.Error, "no agent available") &&
		!strings.Contains(result.Error, "offline") {
		t.Errorf("Error = %q, want to mention 'no agent available' "+
			"or 'offline'", result.Error)
	}

	/* Re-enable the master; the Router should now resume
	 * dispatching to the local executor.
	 */
	svc.SetDisabled(agentsvc.BuiltInNodeAgentID, false)
	result = r.Execute(context.Background(), job, 2)
	if result.Status != scheduler.StatusSuccess {
		t.Errorf("after re-enable, Status = %s, want %s",
			result.Status, scheduler.StatusSuccess)
	}
}

// TestRouter_OnPremFirstAndLabelMatch is the gating test for
// PLAN.md 5.5b. It stands up a real wolfSSL+gRPC server with
// agentsvc, spins up a real on-prem agent.Client advertising
// the "linux" label, and exercises three routing decisions
// through scheduler.Router.Execute:
//
//   - node_label="" runs on the local LocalExecutor (no agent
//     involved).
//   - node_label="linux" routes through agentsvc to the agent,
//     whose LocalExecutor produces the build log.
//   - node_label="windows" returns Status=error (no matching
//     agent; provisioner integration is 5.5c).
func TestRouter_OnPremFirstAndLabelMatch(t *testing.T) {
	chain := testcerts.NewMTLSChain(t)

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

	svc := agentsvc.New("router-test")
	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	// Write the agent's cert material to a temp dir; the agent
	// config uses file paths.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	caPath := filepath.Join(dir, "ca.crt")
	for _, f := range []struct {
		path string
		data []byte
	}{
		{certPath, chain.AgentCert},
		{keyPath, chain.AgentKey},
		{caPath, chain.CACert},
	} {
		if err := os.WriteFile(f.path, f.data, 0o600); err != nil {
			t.Fatalf("write %s: %v", f.path, err)
		}
	}

	agentCfg := &agent.Config{
		AgentID:       "router-linux-agent",
		ServerAddress: inner.Addr().String(),
		Executors:     1,
		Labels:        []string{"linux"},
		Certificate:   certPath,
		Key:           keyPath,
		CACertificate: caPath,
		WorkDir:       filepath.Join(dir, "agent-work"),
	}
	client, err := agent.NewClient(agentCfg)
	if err != nil {
		t.Fatalf("agent.NewClient: %v", err)
	}
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()
	clientDone := make(chan struct{})
	go func() {
		_ = client.Run(clientCtx)
		close(clientDone)
	}()

	if err := waitUntil(3*time.Second, func() bool {
		return svc.IdleAgentWithLabel("linux") == "router-linux-agent"
	}); err != nil {
		t.Fatalf("agent never showed up as idle on linux: %v", err)
	}

	// Server-side scheduler store. The Router's local executor
	// writes its build artifacts here.
	serverStore, err := storage.New(filepath.Join(dir, "server-work"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	localExec := scheduler.NewLocalExecutor(serverStore)
	router := scheduler.NewRouter(svc, localExec, []string{"local"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1) no node_label -> local executor.
	localJob := &storage.Job{Name: "local-only", Steps: []storage.Step{{Shell: "echo on-server"}}}
	got := router.Execute(ctx, localJob, 1)
	if got.Status != scheduler.StatusSuccess {
		t.Fatalf("local: Status = %q, want success (err=%q)", got.Status, got.Error)
	}
	if data, err := os.ReadFile(filepath.Join(serverStore.Root(), "builds", "local-only", "1", "log")); err != nil {
		t.Fatalf("read server build log: %v", err)
	} else if !strings.Contains(string(data), "on-server") {
		t.Errorf("server log = %q, want to contain 'on-server'", data)
	}

	// 2) node_label=linux -> routed through the agent.
	remoteJob := &storage.Job{
		Name:      "needs-linux",
		NodeLabel: "linux",
		Steps:     []storage.Step{{Shell: "echo on-agent"}},
	}
	got = router.Execute(ctx, remoteJob, 1)
	if got.Status != scheduler.StatusSuccess {
		t.Fatalf("remote: Status = %q, want success (err=%q)", got.Status, got.Error)
	}
	if got.JobName != "needs-linux" {
		t.Errorf("remote: JobName = %q, want needs-linux", got.JobName)
	}
	agentLog := filepath.Join(agentCfg.WorkDir, "builds", "needs-linux", "1", "log")
	if data, err := os.ReadFile(agentLog); err != nil {
		t.Fatalf("read agent build log: %v", err)
	} else if !strings.Contains(string(data), "on-agent") {
		t.Errorf("agent log = %q, want to contain 'on-agent'", data)
	}

	// 3) node_label=windows -> no match, error status.
	noMatchJob := &storage.Job{
		Name:      "needs-windows",
		NodeLabel: "windows",
		Steps:     []storage.Step{{Shell: "true"}},
	}
	got = router.Execute(ctx, noMatchJob, 1)
	if got.Status != scheduler.StatusError {
		t.Fatalf("no-match: Status = %q, want error", got.Status)
	}
	if !strings.Contains(got.Error, "windows") {
		t.Errorf("no-match: Error = %q, want to mention 'windows'", got.Error)
	}
}

func waitUntil(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
