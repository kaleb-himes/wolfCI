// Package nodes defines the Provisioner abstraction wolfCI uses
// to spin up and terminate on-demand build nodes. The concrete
// providers (GCE in internal/nodes/gce, a test fake in
// internal/nodes/fake) implement this interface.
//
// Phase 5 ships the interface plus a fake provisioner for tests
// and a stub GCE provisioner. The real GCE driver lands in
// PLAN.md task 5.4b once the google.golang.org/api/compute/v1
// dep is wired up.
package nodes

import (
	"context"
	"errors"
)

// Node is a provisioner-managed worker.
type Node struct {
	// ID uniquely identifies the node within the provisioner. For
	// GCE this is the instance name.
	ID string

	// Labels the node advertises to the scheduler. A Job's
	// node_label is matched against this set.
	Labels []string

	// Address (host:port) the wolfci-agent on this node uses to
	// reach the server. Set by the provisioner from its config.
	Address string
}

// Provisioner is the interface every node backend implements.
// Provision/Terminate are the only operations the scheduler
// needs to know about; backends may expose more (e.g. status
// queries) on their concrete types.
type Provisioner interface {
	// Provision launches a fresh node that advertises the given
	// label (in addition to any backend-defined defaults). The
	// returned Node may not yet have completed registration with
	// the server; callers wait on the Connect stream for that.
	Provision(ctx context.Context, label string) (*Node, error)

	// Terminate shuts down the named node and reclaims its
	// resources. Returns ErrNoSuchNode if the ID is not known.
	Terminate(ctx context.Context, nodeID string) error
}

// ErrNoSuchNode is returned when a Terminate or status call
// targets a node ID the provisioner does not know about.
var ErrNoSuchNode = errors.New("nodes: no such node")

// ErrNotImplemented is reserved for backends that exist as
// stubs only. The real GCE driver replaces its uses in PLAN.md
// task 5.4b.
var ErrNotImplemented = errors.New("nodes: not implemented")
