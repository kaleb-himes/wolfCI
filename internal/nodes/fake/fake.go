// Package fake is an in-memory nodes.Provisioner used by tests
// that exercise scheduler + provisioner integration without
// touching a real cloud backend.
package fake

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/kaleb-himes/wolfCI/internal/nodes"
)

// Provisioner is an in-memory nodes.Provisioner.
type Provisioner struct {
	counter uint64

	mu    sync.Mutex
	nodes map[string]*nodes.Node
}

// NewProvisioner constructs an empty fake provisioner.
func NewProvisioner() *Provisioner {
	return &Provisioner{
		nodes: make(map[string]*nodes.Node),
	}
}

// Provision fabricates a node carrying the requested label.
func (p *Provisioner) Provision(ctx context.Context, label string) (*nodes.Node, error) {
	_ = ctx
	id := fmt.Sprintf("fake-node-%d", atomic.AddUint64(&p.counter, 1))
	n := &nodes.Node{
		ID:      id,
		Labels:  []string{label, "fake"},
		Address: "fake:0",
	}
	p.mu.Lock()
	p.nodes[id] = n
	p.mu.Unlock()
	return n, nil
}

// Terminate removes the node from the in-memory map.
func (p *Provisioner) Terminate(ctx context.Context, nodeID string) error {
	_ = ctx
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.nodes[nodeID]; !ok {
		return nodes.ErrNoSuchNode
	}
	delete(p.nodes, nodeID)
	return nil
}

// ListLive returns a snapshot of the nodes that have been
// Provisioned but not yet Terminated. Test-only.
func (p *Provisioner) ListLive() []*nodes.Node {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*nodes.Node, 0, len(p.nodes))
	for _, n := range p.nodes {
		out = append(out, n)
	}
	return out
}
