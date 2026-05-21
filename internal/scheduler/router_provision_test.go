package scheduler_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agent"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/nodes"
	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestRouter_ProvisionsWhenNoOnPremMatch is the gating test for
// PLAN.md 5.5c. The Router is given a Provisioner that spawns
// fresh agent.Client instances pointing back at the test's own
// agentsvc. A Job with a label no on-prem agent advertises
// triggers Provision; the spawned agent connects; SubmitAndWait
// returns success; Terminate is called.
func TestRouter_ProvisionsWhenNoOnPremMatch(t *testing.T) {
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

	svc := agentsvc.New("router-provision-test")
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

	prov := &spawningProvisioner{
		t:             t,
		serverAddress: inner.Addr().String(),
		certPath:      certPath,
		keyPath:       keyPath,
		caPath:        caPath,
		workDirRoot:   filepath.Join(dir, "spawned"),
		cancels:       map[string]context.CancelFunc{},
	}
	defer prov.shutdownAll()

	serverStore, err := storage.New(filepath.Join(dir, "server-work"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	localExec := scheduler.NewLocalExecutor(serverStore)
	router := scheduler.NewRouter(svc, localExec, []string{"local"}).
		WithProvisioner(prov).
		WithProvisionTimeout(8 * time.Second)

	job := &storage.Job{
		Name:      "needs-overflow",
		NodeLabel: "overflow-label",
		Steps:     []storage.Step{{Shell: "echo hello-from-overflow"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result := router.Execute(ctx, job, 1)
	if result.Status != scheduler.StatusSuccess {
		t.Fatalf("Status = %q, want success (err=%q)", result.Status, result.Error)
	}
	if got := atomic.LoadInt64(&prov.provisions); got != 1 {
		t.Errorf("Provision call count = %d, want 1", got)
	}
	if got := atomic.LoadInt64(&prov.terminates); got != 1 {
		t.Errorf("Terminate call count = %d, want 1", got)
	}

	// The spawned agent should no longer be considered idle.
	if id := svc.IdleAgentWithLabel("overflow-label"); id != "" {
		t.Errorf("overflow agent still present after Terminate: %q", id)
	}

	// And the agent's local build log should contain the echo.
	prov.mu.Lock()
	logRoot := prov.lastWorkDir
	prov.mu.Unlock()
	if logRoot == "" {
		t.Fatal("provisioner did not record the spawned agent's WorkDir")
	}
	logBytes, err := os.ReadFile(filepath.Join(logRoot, "builds", "needs-overflow", "1", "log"))
	if err != nil {
		t.Fatalf("read spawned agent log: %v", err)
	}
	if !strings.Contains(string(logBytes), "hello-from-overflow") {
		t.Errorf("spawned agent log = %q, want to contain 'hello-from-overflow'", logBytes)
	}
}

// spawningProvisioner implements nodes.Provisioner by spinning
// up a fresh agent.Client (in-process) that dials the same
// agentsvc the Router is using. The Node.ID and the agent_id
// the agent passes to Register are the same value, which is
// what Router.provisionAndWait assumes.
type spawningProvisioner struct {
	t             *testing.T
	serverAddress string
	certPath      string
	keyPath       string
	caPath        string
	workDirRoot   string

	provisions int64
	terminates int64

	counter uint64

	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	lastWorkDir string
}

func (p *spawningProvisioner) Provision(ctx context.Context, label string) (*nodes.Node, error) {
	_ = ctx
	atomic.AddInt64(&p.provisions, 1)
	id := fmt.Sprintf("spawned-%d", atomic.AddUint64(&p.counter, 1))
	workDir := filepath.Join(p.workDirRoot, id)
	cfg := &agent.Config{
		AgentID:       id,
		ServerAddress: p.serverAddress,
		Executors:     1,
		Labels:        []string{label, "overflow"},
		Certificate:   p.certPath,
		Key:           p.keyPath,
		CACertificate: p.caPath,
		WorkDir:       workDir,
	}
	client, err := agent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	clientCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = client.Run(clientCtx) }()

	p.mu.Lock()
	p.cancels[id] = cancel
	p.lastWorkDir = workDir
	p.mu.Unlock()

	return &nodes.Node{ID: id, Labels: cfg.Labels, Address: p.serverAddress}, nil
}

func (p *spawningProvisioner) Terminate(ctx context.Context, id string) error {
	_ = ctx
	atomic.AddInt64(&p.terminates, 1)
	p.mu.Lock()
	cancel, ok := p.cancels[id]
	delete(p.cancels, id)
	p.mu.Unlock()
	if !ok {
		return nodes.ErrNoSuchNode
	}
	cancel()
	// Give the agent's stream a moment to actually disconnect so
	// IdleAgentWithLabel reflects the change.
	time.Sleep(150 * time.Millisecond)
	return nil
}

func (p *spawningProvisioner) shutdownAll() {
	p.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(p.cancels))
	for _, c := range p.cancels {
		cancels = append(cancels, c)
	}
	p.cancels = nil
	p.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}
