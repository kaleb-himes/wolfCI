// Package gce will host the Google Compute Engine implementation
// of nodes.Provisioner. Phase 5.4a ships only the Config layer
// and a stub Provisioner that returns nodes.ErrNotImplemented;
// the real google.golang.org/api/compute/v1 integration lands in
// PLAN.md task 5.4b.
package gce

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape of config-files/nodes/gce.yaml.
//
// Per the locked-in Phase 5 decision, credentials come from
// ServiceAccountKey (a path to a Google-issued JSON key file).
// wolfCI does NOT consult GOOGLE_APPLICATION_CREDENTIALS or VM
// metadata; the config is the single source of truth.
type Config struct {
	// ProjectID is the GCP project under which instances are
	// created.
	ProjectID string `yaml:"project_id"`

	// Zone is the GCP zone (e.g. "us-central1-a").
	Zone string `yaml:"zone"`

	// MachineType is the GCE machine type (e.g. "e2-medium").
	MachineType string `yaml:"machine_type"`

	// ServiceAccountKey is the absolute path to the JSON service
	// account key wolfCI will use to call the GCE API.
	ServiceAccountKey string `yaml:"service_account_key"`

	// Image is the source image URL for new instances (e.g.
	// "projects/debian-cloud/global/images/family/debian-12").
	Image string `yaml:"image"`

	// Network is the GCE network to attach to. Empty defaults
	// to "default".
	Network string `yaml:"network,omitempty"`

	// Labels are advertised by every node this provisioner
	// creates. Used by the scheduler to match Job.node_label.
	Labels []string `yaml:"labels,omitempty"`

	// MaxInstances caps how many VMs this provisioner is
	// allowed to keep alive simultaneously. Zero (the YAML-
	// omitted default) means "no cap" - the scheduler boots
	// instances as long as work is queued. Surfaced for the
	// 19.6 form so an operator can put a budget guard on a
	// pool; the scheduler-side enforcement lands when the
	// overflow router consults the configs.
	MaxInstances int `yaml:"max_instances,omitempty"`
}

// LoadConfig reads a Config from disk and validates that every
// field required to actually call GCE is present.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gce.LoadConfig: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("gce.LoadConfig: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("gce.LoadConfig: %w", err)
	}
	return cfg, nil
}

// Save writes the Config to path, creating intermediate dirs.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("gce.Config.Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("gce.Config.Save: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("gce.Config.Save: write: %w", err)
	}
	return nil
}

// Validate checks the required fields. Network defaults to
// "default" if empty; everything else is required.
func (c *Config) Validate() error {
	if c.ProjectID == "" {
		return errors.New("project_id is required")
	}
	if c.Zone == "" {
		return errors.New("zone is required")
	}
	if c.MachineType == "" {
		return errors.New("machine_type is required")
	}
	if c.ServiceAccountKey == "" {
		return errors.New("service_account_key is required")
	}
	if c.Image == "" {
		return errors.New("image is required")
	}
	return nil
}
