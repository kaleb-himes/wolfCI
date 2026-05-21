package cliservice_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/cliservice"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestCLIService_StreamBuildLog writes content to a log.live
// file, opens StreamBuildLog, and asserts every byte arrives.
// Idle timeout is shrunk to a few hundred ms so the stream
// closes cleanly after the content is drained.
func TestCLIService_StreamBuildLog(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	logPath := filepath.Join(dir, "builds", "demo", "1", "log.live")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("first chunk\nsecond chunk\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svc := cliservice.New(st, nil)
	svc.LogPollInterval = 25 * time.Millisecond
	svc.LogIdleTimeout = 500 * time.Millisecond

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.StreamBuildLog(ctx, &cliv1.BuildLogRequest{
		JobName:     "demo",
		BuildNumber: 1,
	})
	if err != nil {
		t.Fatalf("StreamBuildLog: %v", err)
	}

	var got []byte
	for {
		line, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		got = append(got, line.Data...)
		if strings.Contains(string(got), "second chunk") {
			break
		}
	}
	if !strings.Contains(string(got), "first chunk") {
		t.Errorf("missing 'first chunk' in: %q", got)
	}
	if !strings.Contains(string(got), "second chunk") {
		t.Errorf("missing 'second chunk' in: %q", got)
	}
}

// TestCLIService_StreamBuildLog_BadInput rejects invalid job
// name or build number.
func TestCLIService_StreamBuildLog_BadInput(t *testing.T) {
	dir := t.TempDir()
	st, _ := storage.New(dir)
	svc := cliservice.New(st, nil)
	svc.LogPollInterval = 25 * time.Millisecond
	svc.LogIdleTimeout = 200 * time.Millisecond

	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	gsrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(gsrv, svc)
	go func() { _ = gsrv.Serve(lis) }()
	defer gsrv.Stop()

	conn, _ := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()
	client := cliv1.NewCLIServiceClient(conn)

	cases := []*cliv1.BuildLogRequest{
		{JobName: "../escape", BuildNumber: 1},
		{JobName: "demo", BuildNumber: 0},
		{JobName: "", BuildNumber: 1},
	}
	for _, req := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		stream, err := client.StreamBuildLog(ctx, req)
		if err != nil {
			cancel()
			continue // some errors surface on initial call
		}
		_, err = stream.Recv()
		cancel()
		if err == nil {
			t.Errorf("StreamBuildLog(%+v) accepted; want error", req)
		}
	}
}
