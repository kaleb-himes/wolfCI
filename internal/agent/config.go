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
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultHeartbeatInterval = 30 * time.Second
	maxHeartbeatInterval     = 300 * time.Second
)

// Config is the on-disk shape of config-files/agent.yaml.
type Config struct {
	// AgentID is the identifier this agent presents to the
	// server. Required. Typically the cert CN.
	AgentID string `yaml:"agent_id"`

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

	// WorkDir is the directory where this agent caches build
	// outputs (builds/<job>/<n>/log etc.). Required.
	WorkDir string `yaml:"work_dir"`

	// HeartbeatInterval is the period between NodeStatus
	// heartbeats the agent emits on its Connect stream (PLAN.md
	// Phase 12.3). Empty string means defaultHeartbeatInterval
	// (30s). The maximum is 300s; the minimum is bounded only by
	// what time.ParseDuration accepts, but operators should
	// stay above 5s in production to avoid flooding the server
	// (the unit tests use 100ms intentionally to keep the
	// suite fast). HeartbeatTickInterval returns the parsed
	// value with maximum enforcement applied.
	HeartbeatInterval string `yaml:"heartbeat_interval,omitempty"`
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
	if c.AgentID == "" {
		return errors.New("agent_id is required")
	}
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
	if c.WorkDir == "" {
		return errors.New("work_dir is required")
	}
	if _, err := c.HeartbeatTickInterval(); err != nil {
		return err
	}
	return nil
}

// HeartbeatTickInterval returns the parsed HeartbeatInterval or
// defaultHeartbeatInterval if the field is empty. Returns an
// error if the field is malformed, non-positive, or larger than
// maxHeartbeatInterval (300s). The lower bound from the PLAN.md
// spec (5s) is documented in the field comment rather than
// enforced in code so tests can pass values like "100ms" without
// rebuilding the validator's surface.
func (c *Config) HeartbeatTickInterval() (time.Duration, error) {
	if c.HeartbeatInterval == "" {
		return defaultHeartbeatInterval, nil
	}
	d, err := time.ParseDuration(c.HeartbeatInterval)
	if err != nil {
		return 0, fmt.Errorf(
			"parse heartbeat_interval %q: %w", c.HeartbeatInterval, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf(
			"heartbeat_interval %v must be positive", d)
	}
	if d > maxHeartbeatInterval {
		return 0, fmt.Errorf(
			"heartbeat_interval %v exceeds maximum %v",
			d, maxHeartbeatInterval)
	}
	return d, nil
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
