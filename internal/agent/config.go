// Package agent contains the wolfCI executor that runs on a node
// and reports back to the server. The on-disk config and the
// runtime types live here; cmd/wolfci-agent is the thin binary
// that wires them together.
//
// The full agent protocol (gRPC over wolfSSL mTLS) lands with
// PLAN.md task 5.3. Task 5.1 ships only the config layer and the
// command-line scaffold so the binary exists and the
// configuration surface is locked in.
package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape of config-files/agent.yaml.
type Config struct {
	// ServerAddress is the host:port of the wolfCI server, e.g.
	// "ci.example.com:8443".
	ServerAddress string `yaml:"server_address"`

	// Executors is the maximum number of jobs this agent runs
	// concurrently. Must be >= 1.
	Executors int `yaml:"executors"`

	// Labels declared by this agent. A Job.node_label matches if
	// this list contains that string. Multiple labels per agent
	// are supported; a single required label per job is supported.
	Labels []string `yaml:"labels,omitempty"`

	// Certificate is the path to the agent's PEM cert file.
	Certificate string `yaml:"certificate"`

	// Key is the path to the agent's PEM private key file.
	Key string `yaml:"key"`

	// CACertificate is the path to the PEM file containing the
	// wolfCI server's CA bundle. Used to verify the server's
	// certificate during the mTLS handshake.
	CACertificate string `yaml:"ca_certificate"`
}

// DefaultConfig returns a Config with sensible defaults. The
// caller still has to fill in ServerAddress and the cert paths.
func DefaultConfig() *Config {
	return &Config{
		Executors: 1,
	}
}

// LoadConfig reads an agent config from disk and validates it.
// Missing-field validation runs after YAML parsing, so unknown
// extra keys in the file are ignored.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("agent.LoadConfig: %w", err)
	}
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("agent.LoadConfig: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("agent.LoadConfig: %w", err)
	}
	return cfg, nil
}

// Save writes c to path, creating intermediate directories.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("agent.Config.Save: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent.Config.Save: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("agent.Config.Save: write: %w", err)
	}
	return nil
}

// Validate checks that all required fields are populated and
// within bounds. Returns nil if the config is usable.
func (c *Config) Validate() error {
	if c.ServerAddress == "" {
		return errors.New("server_address is required")
	}
	if c.Executors < 1 {
		return fmt.Errorf("executors must be >= 1, got %d", c.Executors)
	}
	if c.Certificate == "" {
		return errors.New("certificate is required")
	}
	if c.Key == "" {
		return errors.New("key is required")
	}
	if c.CACertificate == "" {
		return errors.New("ca_certificate is required")
	}
	return nil
}

// HasLabel reports whether this agent declares the given label.
// An empty label matches any agent so the scheduler can treat
// "no node_label requirement" as a wildcard.
func (c *Config) HasLabel(label string) bool {
	if label == "" {
		return true
	}
	for _, got := range c.Labels {
		if got == label {
			return true
		}
	}
	return false
}
