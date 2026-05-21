package gce_test

import (
	"strings"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/nodes/gce"
)

// TestStartupScript_MentionsAgentIDAndLabel pins the script's
// observable surface: a future bootstrap script change will
// reach further, but at minimum the spawn parameters must reach
// the VM so an operator can correlate logs.
func TestStartupScript_MentionsAgentIDAndLabel(t *testing.T) {
	cfg := &gce.Config{
		ProjectID:         "wolfci-test",
		Zone:              "us-central1-a",
		MachineType:       "e2-medium",
		ServiceAccountKey: "/etc/wolfci/sa.json",
		Image:             "projects/debian-cloud/global/images/family/debian-12",
	}
	script := gce.StartupScript("wolfci-agent-12345", "overflow-label", cfg)
	if !strings.HasPrefix(script, "#!/bin/bash") {
		t.Errorf("script does not start with bash shebang:\n%s", script)
	}
	if !strings.Contains(script, "agent_id=wolfci-agent-12345") {
		t.Errorf("script missing agent_id line:\n%s", script)
	}
	if !strings.Contains(script, "label=overflow-label") {
		t.Errorf("script missing label line:\n%s", script)
	}
	if !strings.Contains(script, "set -eu") {
		t.Errorf("script missing 'set -eu':\n%s", script)
	}
}

// TestNewProvisioner_LazyService confirms that constructing a
// Provisioner does NOT immediately reach for the GCE API. This
// keeps offline unit tests cheap and makes a missing
// service_account_key surface at Provision time rather than at
// boot.
func TestNewProvisioner_LazyService(t *testing.T) {
	cfg := &gce.Config{
		ProjectID:         "p",
		Zone:              "z",
		MachineType:       "m",
		ServiceAccountKey: "/definitely/not/a/real/path",
		Image:             "i",
	}
	if p := gce.New(cfg); p == nil {
		t.Fatal("gce.New: nil Provisioner")
	}
	// Should not panic, should not contact GCE.
}
