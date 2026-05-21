package cliservice_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/cliservice"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestCLIService_ListJobs is the gating test for the CLIService
// half of PLAN.md task 8.1b. It stores a job through the real
// storage layer, then exercises the gRPC ListJobs RPC end to
// end (no TLS in this test; TLS is gated separately at the
// wolfci server bootstrap).
func TestCLIService_ListJobs(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := st.SaveJob(&storage.Job{
		Name:        "build-all",
		Description: "compile everything",
		NodeLabel:   "linux",
		Steps: []storage.Step{
			{Shell: "make"},
			{Shell: "make test"},
		},
	}); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	srv, addr, stop := startCLIServer(t, st, nil)
	defer stop()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.ListJobs(ctx, &cliv1.Empty{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("Jobs len = %d, want 1", len(resp.Jobs))
	}
	j := resp.Jobs[0]
	if j.Name != "build-all" {
		t.Errorf("Name = %q, want build-all", j.Name)
	}
	if j.Description != "compile everything" {
		t.Errorf("Description = %q", j.Description)
	}
	if j.NodeLabel != "linux" {
		t.Errorf("NodeLabel = %q", j.NodeLabel)
	}
	if j.StepCount != 2 {
		t.Errorf("StepCount = %d, want 2", j.StepCount)
	}
	_ = srv
}

// TestCLIService_ListNodes registers two agents on the
// agentsvc.Server (Register only - no Connect stream open) and
// asserts the CLIService surfaces them both with Connected=false.
func TestCLIService_ListNodes(t *testing.T) {
	dir := t.TempDir()
	st, _ := storage.New(dir)
	asv := agentsvc.New("cli-list-nodes")

	for _, info := range []*wolfciv1.AgentInfo{
		{AgentId: "node-alpha", Labels: []string{"linux"}, Executors: 4},
		{AgentId: "node-beta", Labels: []string{"macos", "arm64"}, Executors: 2},
	} {
		if _, err := asv.Register(context.Background(), info); err != nil {
			t.Fatalf("Register(%s): %v", info.AgentId, err)
		}
	}

	_, addr, stop := startCLIServer(t, st, asv)
	defer stop()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.ListNodes(ctx, &cliv1.Empty{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("Nodes len = %d, want 2", len(resp.Nodes))
	}
	byID := map[string]*cliv1.Node{}
	for _, n := range resp.Nodes {
		byID[n.AgentId] = n
	}
	alpha, ok := byID["node-alpha"]
	if !ok {
		t.Fatal("node-alpha missing")
	}
	if alpha.Executors != 4 || len(alpha.Labels) != 1 || alpha.Connected {
		t.Errorf("alpha = %+v, want Executors=4 Labels=[linux] Connected=false", alpha)
	}
	beta, ok := byID["node-beta"]
	if !ok {
		t.Fatal("node-beta missing")
	}
	if beta.Executors != 2 || len(beta.Labels) != 2 || beta.Connected {
		t.Errorf("beta = %+v, want Executors=2 Labels=[macos arm64] Connected=false", beta)
	}
}

// TestCLIService_ListNodes_NoAgentSvc tolerates a nil agentSvc.
func TestCLIService_ListNodes_NoAgentSvc(t *testing.T) {
	dir := t.TempDir()
	st, _ := storage.New(dir)
	_, addr, stop := startCLIServer(t, st, nil)
	defer stop()
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := client.ListNodes(ctx, &cliv1.Empty{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(resp.Nodes) != 0 {
		t.Errorf("Nodes len = %d, want 0", len(resp.Nodes))
	}
}

func startCLIServer(t *testing.T, st *storage.Storage, asv *agentsvc.Server) (*cliservice.Server, string, func()) {
	t.Helper()
	svc := cliservice.New(st, asv)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	gsrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(gsrv, svc)
	go func() { _ = gsrv.Serve(lis) }()
	stop := func() {
		gsrv.Stop()
		_ = lis.Close()
	}
	return svc, lis.Addr().String(), stop
}
