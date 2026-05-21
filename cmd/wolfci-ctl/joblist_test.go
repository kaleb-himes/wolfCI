package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

// TestFormatJobs_Text pins the column header + per-row layout
// for human output.
func TestFormatJobs_Text(t *testing.T) {
	jobs := []*cliv1.Job{
		{Name: "build-all", Description: "compile", NodeLabel: "linux", StepCount: 3},
		{Name: "lint", Description: "static checks", NodeLabel: "", StepCount: 1},
	}
	got := formatJobs(jobs, false)
	for _, want := range []string{
		"NAME", "DESCRIPTION", "NODE LABEL", "STEPS",
		"build-all", "compile", "linux", "3",
		"lint", "static checks", "-", "1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q\nfull:\n%s", want, got)
		}
	}
}

// TestFormatJobs_Empty surfaces the empty-state marker.
func TestFormatJobs_Empty(t *testing.T) {
	got := formatJobs(nil, false)
	if !strings.Contains(got, "(no jobs)") {
		t.Errorf("empty text output missing '(no jobs)': %q", got)
	}
}

// TestFormatJobs_JSON returns a parseable array.
func TestFormatJobs_JSON(t *testing.T) {
	jobs := []*cliv1.Job{
		{Name: "build-all", Description: "compile", NodeLabel: "linux", StepCount: 3},
	}
	got := formatJobs(jobs, true)
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v\nfull: %s", err, got)
	}
	if len(parsed) != 1 {
		t.Fatalf("len = %d, want 1", len(parsed))
	}
	if parsed[0]["name"] != "build-all" {
		t.Errorf("json: name = %v, want build-all", parsed[0]["name"])
	}
}

// TestFormatNodes_Text exercises the node table.
func TestFormatNodes_Text(t *testing.T) {
	nodes := []*cliv1.Node{
		{AgentId: "alpha", Labels: []string{"linux", "x86_64"}, Executors: 4, Connected: true},
		{AgentId: "beta", Labels: nil, Executors: 1, Connected: false},
	}
	got := formatNodes(nodes, false)
	for _, want := range []string{
		"AGENT_ID", "STATUS", "LABELS", "EXECUTORS",
		"alpha", "connected", "linux,x86_64", "4",
		"beta", "offline", "-", "1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q\nfull:\n%s", want, got)
		}
	}
}

// TestJobList_EndToEnd is the gating mTLS test for the
// `wolfci-ctl job list` subcommand. It stands up a wolfSSL
// mTLS gRPC server exporting CLIService, writes a ctl.yaml
// pointing at it, and runs the subcommand against captured
// pipes.
func TestJobList_EndToEnd(t *testing.T) {
	dir := t.TempDir()

	// Storage with one job pre-saved.
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := st.SaveJob(&storage.Job{
		Name:        "demo",
		Description: "end to end",
		NodeLabel:   "linux",
		Steps:       []storage.Step{{Shell: "true"}},
	}); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

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
	cliv1.RegisterCLIServiceServer(grpcSrv, cliservice.New(st, nil))
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	// Write the agent's cert material to disk; ctl.yaml refers
	// to these paths.
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
	code := dispatch([]string{"job", "list", "--config", ctlYaml}, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, getErr())
	}
	out := getOut()
	for _, want := range []string{"NAME", "demo", "end to end", "linux", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}

	// And the same call with --json.
	stdout2, stderr2, getOut2, _ := captureFiles(t)
	code = dispatch([]string{"job", "list", "--config", ctlYaml, "--json"}, stdout2, stderr2)
	if code != 0 {
		t.Fatalf("--json exit = %d", code)
	}
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(getOut2())), &parsed); err != nil {
		t.Fatalf("unmarshal --json: %v", err)
	}
	if len(parsed) != 1 || parsed[0]["name"] != "demo" {
		t.Errorf("--json output = %+v", parsed)
	}

	_ = context.TODO()
	_ = time.Second
}
