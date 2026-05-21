package main

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/cliservice"
	"github.com/kaleb-himes/wolfCI/internal/storage"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestBuildLog_EndToEnd is the CLI-side gate for 8.1c. It
// stands up an mTLS gRPC CLIService over wolfSSL, drops
// content into log.live, and runs `build log demo 1` via
// dispatch. The captured stdout must contain every emitted
// chunk.
func TestBuildLog_EndToEnd(t *testing.T) {
	dir := t.TempDir()

	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	logPath := filepath.Join(dir, "builds", "demo", "1", "log.live")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(logPath, []byte("hello-from-stream\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svc := cliservice.New(st, nil)
	svc.LogPollInterval = 25 * time.Millisecond
	svc.LogIdleTimeout = 300 * time.Millisecond

	chain := testcerts.NewMTLSChain(t)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
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

	grpcSrv := grpc.NewServer()
	cliv1.RegisterCLIServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	certPath := filepath.Join(dir, "ctl.crt")
	keyPath := filepath.Join(dir, "ctl.key")
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
			t.Fatalf("write %s: %v", e.path, err)
		}
	}

	ctlYaml := filepath.Join(dir, "ctl.yaml")
	if err := (&Config{
		ServerAddress: inner.Addr().String(),
		Certificate:   certPath,
		Key:           keyPath,
		CACertificate: caPath,
	}).Save(ctlYaml); err != nil {
		t.Fatalf("Save ctl.yaml: %v", err)
	}

	stdout, stderr, getOut, getErr := captureFiles(t)
	code := dispatch([]string{"build", "log", "--config", ctlYaml, "demo", "1"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, getErr())
	}
	out := getOut()
	if !strings.Contains(out, "hello-from-stream") {
		t.Errorf("stdout missing 'hello-from-stream'; got: %q", out)
	}
}

// TestBuildLog_BadArgs covers usage errors at the CLI layer.
func TestBuildLog_BadArgs(t *testing.T) {
	cases := [][]string{
		{"build", "log"},                  // missing both positional args
		{"build", "log", "demo"},          // missing build number
		{"build", "log", "demo", "zero"},  // non-numeric build number
		{"build", "log", "demo", "-1"},    // non-positive build number
		{"build", "log", "demo", "1", "2"}, // too many positional args
	}
	for _, args := range cases {
		stdout, stderr, _, _ := captureFiles(t)
		if code := dispatch(args, stdout, stderr); code != 2 {
			t.Errorf("dispatch(%v) exit = %d, want 2", args, code)
		}
	}
}
