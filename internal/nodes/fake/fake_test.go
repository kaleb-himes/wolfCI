package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/nodes"
	"github.com/kaleb-himes/wolfCI/internal/nodes/fake"
)

// TestFake_ProvisionTerminate is the gating test for PLAN.md
// 5.4a. The fake provisioner implements the nodes.Provisioner
// contract by returning fabricated Nodes with the requested
// label and tracking termination calls.
func TestFake_ProvisionTerminate(t *testing.T) {
	p := fake.NewProvisioner()
	ctx := context.Background()

	node, err := p.Provision(ctx, "linux")
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if node.ID == "" {
		t.Error("Provision: empty Node.ID")
	}
	if !hasLabel(node.Labels, "linux") {
		t.Errorf("Provision: node.Labels = %v, want to contain 'linux'", node.Labels)
	}

	if err := p.Terminate(ctx, node.ID); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	// Second Terminate of the same ID should return ErrNoSuchNode.
	if err := p.Terminate(ctx, node.ID); !errors.Is(err, nodes.ErrNoSuchNode) {
		t.Errorf("second Terminate: %v, want ErrNoSuchNode", err)
	}
}

// TestFake_ListLive snapshots the set of provisioned-but-not-
// terminated nodes.
func TestFake_ListLive(t *testing.T) {
	p := fake.NewProvisioner()
	ctx := context.Background()

	if got := p.ListLive(); len(got) != 0 {
		t.Errorf("ListLive (empty): len = %d, want 0", len(got))
	}

	n1, _ := p.Provision(ctx, "linux")
	n2, _ := p.Provision(ctx, "macos")
	if got := p.ListLive(); len(got) != 2 {
		t.Errorf("ListLive (after 2 Provision): len = %d, want 2", len(got))
	}

	_ = p.Terminate(ctx, n1.ID)
	live := p.ListLive()
	if len(live) != 1 || live[0].ID != n2.ID {
		t.Errorf("ListLive (after 1 Terminate): %v, want only %s", live, n2.ID)
	}
}

func hasLabel(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
