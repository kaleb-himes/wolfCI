package agent_test

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agent"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestClient_RunDispatchesAndReports is the agent-runtime gate.
// It stands up a wolfSSL mTLS gRPC server with an agentsvc
// pre-queued with a JobAssignment, then spins up a real
// agent.Client whose LocalExecutor actually runs the shell step.
// The test waits for the server to record the BuildComplete and
// asserts on its contents.
func TestClient_RunDispatchesAndReports(t *testing.T) {
	chain := testcerts.NewMTLSChain(t)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	caPath := filepath.Join(dir, "ca.crt")
	for _, e := range []struct {
		path string
		data []byte
	}{
		{certPath, chain.AgentCert},
		{keyPath, chain.AgentKey},
		{caPath, chain.CACert},
	} {
		if err := os.WriteFile(e.path, e.data, 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", e.path, err)
		}
	}

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

	svc := agentsvc.New("v1-agent-runtime")
	svc.QueueJob(&wolfciv1.JobAssignment{
		JobName:     "demo",
		BuildNumber: 1,
		Steps: []*wolfciv1.Step{
			{Name: "say-hi", Shell: "echo hello-from-agent"},
		},
	})

	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	cfg := &agent.Config{
		AgentID:       "wolfci-agent-1",
		ServerAddress: inner.Addr().String(),
		Executors:     1,
		Labels:        []string{"linux", "test"},
		Certificate:   certPath,
		Key:           keyPath,
		CACertificate: caPath,
		WorkDir:       filepath.Join(dir, "work"),
	}
	client, err := agent.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- client.Run(ctx) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if completed := svc.Completed(); len(completed) == 1 {
			bc := completed[0]
			if bc.BuildNumber != 1 {
				t.Fatalf("BuildNumber = %d, want 1", bc.BuildNumber)
			}
			if bc.Status != "success" {
				t.Fatalf("Status = %q, want success", bc.Status)
			}
			if bc.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0", bc.ExitCode)
			}

			logPath := filepath.Join(cfg.WorkDir, "builds", "demo", "1", "log")
			logBytes, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read agent build log: %v", err)
			}
			if !contains(string(logBytes), "hello-from-agent") {
				t.Errorf("agent log = %q, want to contain 'hello-from-agent'", logBytes)
			}

			cancel()
			select {
			case <-runDone:
			case <-time.After(2 * time.Second):
				t.Fatal("client.Run did not return after ctx cancel")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	t.Fatalf("server did not record completion within timeout; got %d", len(svc.Completed()))
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
