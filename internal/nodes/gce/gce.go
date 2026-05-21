package gce

import (
	"context"
	"fmt"
	"strings"
	"time"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/kaleb-himes/wolfCI/internal/nodes"
)

// Provisioner is the GCE nodes.Provisioner. It launches an
// instance per Provision call and tears it down on Terminate.
//
// Convention: Node.ID equals the GCE instance name AND the
// agent_id the spawned agent will pass to Register. The
// Router's "wait for the new node to connect" loop polls
// IdleAgentWithLabel for that label; the spawned agent must
// advertise it.
//
// The startup-script that boots wolfci-agent on the freshly
// created VM is a placeholder today; filling it in (with
// real binary distribution + cert material) is the remaining
// piece of cloud overflow.
type Provisioner struct {
	cfg *Config

	// service is lazily built so unit tests that only exercise
	// helpers do not need GCE credentials.
	service *compute.Service
}

// New constructs a GCE Provisioner against cfg. cfg must have
// been validated via Validate (LoadConfig does this).
func New(cfg *Config) *Provisioner { return &Provisioner{cfg: cfg} }

// Config returns the underlying configuration.
func (p *Provisioner) Config() *Config { return p.cfg }

// Provision launches a GCE instance advertising label and
// blocks until the create operation reaches DONE.
func (p *Provisioner) Provision(ctx context.Context, label string) (*nodes.Node, error) {
	svc, err := p.getService(ctx)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("wolfci-agent-%d", time.Now().UnixNano())
	network := p.cfg.Network
	if network == "" {
		network = "default"
	}

	instance := &compute.Instance{
		Name:        name,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", p.cfg.Zone, p.cfg.MachineType),
		Disks: []*compute.AttachedDisk{{
			Boot:       true,
			AutoDelete: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: p.cfg.Image,
			},
		}},
		NetworkInterfaces: []*compute.NetworkInterface{{
			Network: fmt.Sprintf("global/networks/%s", network),
			AccessConfigs: []*compute.AccessConfig{{
				Type: "ONE_TO_ONE_NAT", Name: "External NAT",
			}},
		}},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "startup-script",
					Value: stringPtr(StartupScript(name, label, p.cfg)),
				},
			},
		},
		Labels: map[string]string{
			"wolfci-label": sanitizeGCELabel(label),
		},
	}

	op, err := svc.Instances.Insert(p.cfg.ProjectID, p.cfg.Zone, instance).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gce.Provision: Instances.Insert: %w", err)
	}
	if err := p.waitOp(ctx, op); err != nil {
		return nil, fmt.Errorf("gce.Provision: wait: %w", err)
	}

	labels := append([]string{label}, p.cfg.Labels...)
	return &nodes.Node{ID: name, Labels: labels, Address: name}, nil
}

// Terminate deletes the named GCE instance and waits for the
// delete operation to reach DONE.
func (p *Provisioner) Terminate(ctx context.Context, id string) error {
	svc, err := p.getService(ctx)
	if err != nil {
		return err
	}
	op, err := svc.Instances.Delete(p.cfg.ProjectID, p.cfg.Zone, id).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gce.Terminate: Instances.Delete: %w", err)
	}
	if err := p.waitOp(ctx, op); err != nil {
		return fmt.Errorf("gce.Terminate: wait: %w", err)
	}
	return nil
}

// getService lazily constructs the compute.Service using the
// configured service-account-key path (per Phase 5 decisions;
// NO reliance on Application Default Credentials).
func (p *Provisioner) getService(ctx context.Context) (*compute.Service, error) {
	if p.service != nil {
		return p.service, nil
	}
	svc, err := compute.NewService(ctx, option.WithCredentialsFile(p.cfg.ServiceAccountKey))
	if err != nil {
		return nil, fmt.Errorf("gce: compute.NewService: %w", err)
	}
	p.service = svc
	return svc, nil
}

func (p *Provisioner) waitOp(ctx context.Context, op *compute.Operation) error {
	svc, err := p.getService(ctx)
	if err != nil {
		return err
	}
	for op.Status != "DONE" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		fresh, err := svc.ZoneOperations.Get(p.cfg.ProjectID, p.cfg.Zone, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("gce.waitOp: ZoneOperations.Get: %w", err)
		}
		op = fresh
	}
	if op.Error != nil && len(op.Error.Errors) > 0 {
		return fmt.Errorf("operation %s failed: %s", op.Name, op.Error.Errors[0].Message)
	}
	return nil
}

// StartupScript returns the bash script wolfCI hands to GCE as
// instance metadata. The current implementation is a placeholder
// that logs the requested agent_id/label so the VM boot can be
// verified end-to-end. A follow-on task wires in actual
// wolfci-agent installation + cert material distribution.
//
// Exported so tests can assert on the script's contents without
// reaching into private package state.
func StartupScript(agentID, label string, cfg *Config) string {
	_ = cfg
	return fmt.Sprintf(`#!/bin/bash
# wolfCI cloud-overflow bootstrap (Phase 5.6 placeholder).
# A follow-on task fills this in to:
#   1. fetch the wolfci-agent binary,
#   2. drop cert+key+CA material (via GCE Secret Manager or
#      similarly-secured channel),
#   3. write config-files/agent.yaml with this agent_id and label,
#   4. start wolfci-agent under systemd.
# Today the script just records the spawn parameters so VM
# boot can be observed.
set -eu
mkdir -p /var/log
{
  echo "wolfci-agent bootstrap placeholder"
  echo "agent_id=%s"
  echo "label=%s"
  date -u
} >> /var/log/wolfci-agent-bootstrap.log
`, agentID, label)
}

// sanitizeGCELabel maps an arbitrary wolfCI label into the
// reduced character set GCE requires for instance labels:
// lowercase letters, digits, hyphens.
func sanitizeGCELabel(label string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func stringPtr(s string) *string { return &s }
