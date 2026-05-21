package cliservice_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/cliservice"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestCLIService_RunJob is the gating test for the RunJob RPC.
// It pre-saves a Job, wires cliservice to a fake EnqueuerFunc
// that records the call and returns build number 7, and
// asserts the response.
func TestCLIService_RunJob(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := st.SaveJob(&storage.Job{
		Name:        "demo",
		Description: "to be run",
		Steps:       []storage.Step{{Shell: "true"}},
	}); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	var (
		mu       sync.Mutex
		enqueued []*storage.Job
	)
	fake := cliservice.EnqueuerFunc(func(job *storage.Job) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		enqueued = append(enqueued, job)
		return 7, nil
	})

	svc := cliservice.New(st, nil).WithEnqueuer(fake)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	gsrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(gsrv, svc)
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.Stop()

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.RunJob(ctx, &cliv1.RunJobRequest{JobName: "demo"})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if resp.BuildNumber != 7 {
		t.Errorf("BuildNumber = %d, want 7", resp.BuildNumber)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(enqueued) != 1 || enqueued[0].Name != "demo" {
		t.Errorf("enqueued = %+v, want [demo]", enqueued)
	}
}

// TestCLIService_RunJob_NoScheduler returns an error when the
// server has no Enqueuer wired up.
func TestCLIService_RunJob_NoScheduler(t *testing.T) {
	dir := t.TempDir()
	st, _ := storage.New(dir)
	st.SaveJob(&storage.Job{Name: "demo", Steps: []storage.Step{{Shell: "true"}}})
	svc := cliservice.New(st, nil) // no WithEnqueuer

	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	gsrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(gsrv, svc)
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.Stop()

	conn, _ := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if _, err := client.RunJob(ctx, &cliv1.RunJobRequest{JobName: "demo"}); err == nil {
		t.Fatal("RunJob: nil error when no Enqueuer wired up; want error")
	}
}

// TestCLIService_RunJob_UnknownJob propagates LoadJob errors.
func TestCLIService_RunJob_UnknownJob(t *testing.T) {
	dir := t.TempDir()
	st, _ := storage.New(dir)
	fake := cliservice.EnqueuerFunc(func(*storage.Job) (int, error) { return 1, nil })
	svc := cliservice.New(st, nil).WithEnqueuer(fake)

	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	gsrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(gsrv, svc)
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.Stop()

	conn, _ := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if _, err := client.RunJob(ctx, &cliv1.RunJobRequest{JobName: "does-not-exist"}); err == nil {
		t.Fatal("RunJob: nil error for unknown job; want error")
	}
}
