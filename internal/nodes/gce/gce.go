package gce

import (
	"context"

	"github.com/kaleb-himes/wolfCI/internal/nodes"
)

// Provisioner is the GCE nodes.Provisioner. Phase 5.4a ships
// only the type and a stub Provision/Terminate that return
// nodes.ErrNotImplemented. The real google.golang.org/api/compute/v1
// integration lands in PLAN.md task 5.4b.
type Provisioner struct {
	cfg *Config
}

// New constructs a GCE Provisioner against cfg. cfg must have
// been validated via Validate (LoadConfig does this).
func New(cfg *Config) *Provisioner {
	return &Provisioner{cfg: cfg}
}

// Provision will create a fresh GCE VM. Stub until 5.4b.
func (p *Provisioner) Provision(ctx context.Context, label string) (*nodes.Node, error) {
	_ = ctx
	_ = label
	return nil, nodes.ErrNotImplemented
}

// Terminate will tear down the VM. Stub until 5.4b.
func (p *Provisioner) Terminate(ctx context.Context, nodeID string) error {
	_ = ctx
	_ = nodeID
	return nodes.ErrNotImplemented
}

// Config returns the underlying configuration.
func (p *Provisioner) Config() *Config { return p.cfg }
