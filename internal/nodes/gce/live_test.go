package gce_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/nodes/gce"
)

// TestLive_GCEProvisionTerminate is the project owner's gated
// placeholder for actually exercising the GCE driver against a
// real Compute Engine project.
//
// Set WOLFCI_GCE_LIVE_TEST=1 plus the configuration env vars
// (WOLFCI_GCE_PROJECT_ID, WOLFCI_GCE_ZONE, WOLFCI_GCE_MACHINE_TYPE,
// WOLFCI_GCE_SERVICE_ACCOUNT_KEY, WOLFCI_GCE_IMAGE) to run it.
// Without those the test t.Skips so day-to-day local runs and CI
// stay safe.
//
// The body provisions one VM with the label "wolfci-live" and
// then terminates it. Cost-aware: do not enable in unattended
// CI without billing protections.
func TestLive_GCEProvisionTerminate(t *testing.T) {
	if os.Getenv("WOLFCI_GCE_LIVE_TEST") != "1" {
		t.Skip("WOLFCI_GCE_LIVE_TEST is not set; skipping live GCE driver test")
	}

	cfg := &gce.Config{
		ProjectID:         requireEnv(t, "WOLFCI_GCE_PROJECT_ID"),
		Zone:              requireEnv(t, "WOLFCI_GCE_ZONE"),
		MachineType:       requireEnv(t, "WOLFCI_GCE_MACHINE_TYPE"),
		ServiceAccountKey: requireEnv(t, "WOLFCI_GCE_SERVICE_ACCOUNT_KEY"),
		Image:             requireEnv(t, "WOLFCI_GCE_IMAGE"),
		Network:           os.Getenv("WOLFCI_GCE_NETWORK"),
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config invalid: %v", err)
	}

	prov := gce.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	node, err := prov.Provision(ctx, "wolfci-live")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if node.ID == "" {
		t.Fatal("Provision returned empty Node.ID")
	}
	t.Logf("provisioned node %s in %s/%s", node.ID, cfg.ProjectID, cfg.Zone)

	if err := prov.Terminate(ctx, node.ID); err != nil {
		t.Fatalf("Terminate(%s): %v", node.ID, err)
	}
	t.Logf("terminated node %s", node.ID)
}

func requireEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required when WOLFCI_GCE_LIVE_TEST=1", key)
	}
	return v
}
