package agent_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/agent"
)

// TestConfig_RoundTrip is the gating test for the config half
// of PLAN.md task 5.1: agent.yaml round-trips Save -> Load
// without data loss.
func TestConfig_RoundTrip(t *testing.T) {
	original := &agent.Config{
		AgentID:       "wolfci-agent-1",
		ServerAddress: "ci.example.com:8443",
		Executors:     4,
		Labels:        []string{"linux", "x86_64", "hardware-tpm"},
		Certificate:   "/etc/wolfci/agent.crt",
		Key:           "/etc/wolfci/agent.key",
		CACertificate: "/etc/wolfci/ca.crt",
		WorkDir:       "/var/lib/wolfci-agent",
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "wolfci", "agent.yaml")
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := agent.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("round-trip mismatch.\noriginal: %+v\nloaded:   %+v", original, loaded)
	}
}

// TestConfig_Defaults pins the agent's default executor count.
func TestConfig_Defaults(t *testing.T) {
	def := agent.DefaultConfig()
	if def.Executors != 1 {
		t.Errorf("DefaultConfig.Executors = %d, want 1", def.Executors)
	}
}

// TestLoadConfig_RejectsMissingFields ensures the loader catches
// configs missing any of the required fields (server address,
// executor count, cert paths).
func TestLoadConfig_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"empty", ""},
		{"missing agent_id", "server_address: a:1\nexecutors: 2\ncertificate: /c\nkey: /k\nca_certificate: /ca\nwork_dir: /w\n"},
		{"missing server_address", "agent_id: a\nexecutors: 2\ncertificate: /c\nkey: /k\nca_certificate: /ca\nwork_dir: /w\n"},
		{"executors zero", "agent_id: a\nserver_address: a:1\nexecutors: 0\ncertificate: /c\nkey: /k\nca_certificate: /ca\nwork_dir: /w\n"},
		{"missing certificate", "agent_id: a\nserver_address: a:1\nexecutors: 1\nkey: /k\nca_certificate: /ca\nwork_dir: /w\n"},
		{"missing key", "agent_id: a\nserver_address: a:1\nexecutors: 1\ncertificate: /c\nca_certificate: /ca\nwork_dir: /w\n"},
		{"missing ca_certificate", "agent_id: a\nserver_address: a:1\nexecutors: 1\ncertificate: /c\nkey: /k\nwork_dir: /w\n"},
		{"missing work_dir", "agent_id: a\nserver_address: a:1\nexecutors: 1\ncertificate: /c\nkey: /k\nca_certificate: /ca\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := agent.LoadConfig(path); err == nil {
				t.Errorf("expected error for %q, got nil", tc.name)
			}
		})
	}
}

// TestConfig_HasLabel exercises the label-match helper used by
// the scheduler-side node-routing code (Phase 5 follow-on).
func TestConfig_HasLabel(t *testing.T) {
	c := &agent.Config{Labels: []string{"linux", "x86_64"}}
	if !c.HasLabel("linux") {
		t.Error("HasLabel(linux) = false, want true")
	}
	if c.HasLabel("macos") {
		t.Error("HasLabel(macos) = true, want false")
	}
	// Empty label matches any node (Job.node_label unset = no requirement).
	if !c.HasLabel("") {
		t.Error(`HasLabel("") = false, want true (empty = no requirement)`)
	}
}
