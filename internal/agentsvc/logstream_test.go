package agentsvc_test

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agent"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestServer_LogChunksDeliveredDuringExecution is the gating
// test for PLAN.md 5.7. It stands up a real mTLS gRPC server +
// real on-prem agent, queues a multi-line shell job, and
// asserts that the server's LogSink received the expected step
// output before SubmitAndWait returned.
func TestServer_LogChunksDeliveredDuringExecution(t *testing.T) {
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

	svc := agentsvc.New("log-stream-test")
	sink := &captureSink{}
	svc.SetLogSink(sink)

	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

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
		AgentID:       "log-stream-agent",
		ServerAddress: inner.Addr().String(),
		Executors:     1,
		Labels:        []string{"streaming"},
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
	go func() { _ = client.Run(clientCtx) }()

	// Wait for the agent's Connect stream to register.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svc.IdleAgentWithLabel("streaming") == "log-stream-agent" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// SubmitAndWait drives the assignment; the job prints two
	// distinct lines so we can confirm both arrive.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bc, err := svc.SubmitAndWait(ctx, "log-stream-agent", &wolfciv1.JobAssignment{
		JobName:     "log-stream-demo",
		BuildNumber: 1,
		Steps: []*wolfciv1.Step{
			{Shell: `echo line-one-streamed && echo line-two-streamed`},
		},
	})
	if err != nil {
		t.Fatalf("SubmitAndWait: %v", err)
	}
	if bc.Status != "success" {
		t.Fatalf("BuildComplete.Status = %q, want success", bc.Status)
	}

	got := sink.contents("log-stream-demo", 1)
	if !strings.Contains(got, "line-one-streamed") {
		t.Errorf("LogSink missing 'line-one-streamed'; got %q", got)
	}
	if !strings.Contains(got, "line-two-streamed") {
		t.Errorf("LogSink missing 'line-two-streamed'; got %q", got)
	}
	if calls := sink.callCount(); calls == 0 {
		t.Errorf("LogSink.WriteLogChunk call count = 0, want >= 1")
	}
}

// captureSink collects every LogChunk by (jobName, buildNum).
type captureSink struct {
	mu    sync.Mutex
	calls int
	logs  map[string]string // "<jobName>/<buildNum>" -> joined chunk bytes
}

func (s *captureSink) WriteLogChunk(jobName string, buildNum int, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logs == nil {
		s.logs = map[string]string{}
	}
	key := jobName + "/" + itoa(buildNum)
	s.logs[key] += string(data)
	s.calls++
}

func (s *captureSink) contents(jobName string, buildNum int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logs[jobName+"/"+itoa(buildNum)]
}

func (s *captureSink) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
