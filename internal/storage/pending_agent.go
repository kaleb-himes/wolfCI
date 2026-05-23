// internal/storage/pending_agent.go - PLAN.md 19.1.
//
// PendingAgent is a pre-registered permanent node slot: the
// operator typed name/labels/executors/description into
// /nodes/new/permanent, the server saved the record here, and
// the agent will inherit the slot's labels + executors the
// first time it calls AgentSvc.Register with the matching
// agent_id.
//
// Storage layout:
//
//   <root>/nodes/<name>/pending.yaml
//
// One pending record per directory; the directory disappears
// when the agent claims the slot (DeletePendingAgent) or the
// operator deletes the pending entry from the UI. The split
// from internal/agentsvc's connected-agent registry is
// deliberate: pending records survive a server restart on
// disk; connected agents live in the in-memory registry and
// re-register on reconnect.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// PendingAgent is the on-disk shape of a pre-registered node
// slot. Same field naming convention as storage.Job - keys are
// snake_case on disk, MixedCaps in Go.
type PendingAgent struct {
	// Name is the agent_id the remote wolfci-agent will pass
	// to AgentSvc.Register. Doubles as the directory name
	// under nodes/, so the same path-validation rules apply
	// (no separators, no leading dot, non-empty).
	Name string `yaml:"name"`

	// Labels the slot advertises. The connected AgentInfo
	// inherits this list on Register.
	Labels []string `yaml:"labels,omitempty"`

	// Executors is the parallel-build cap the slot advertises.
	// Must be >= 1. The 19.3 form defaults to 1.
	Executors int `yaml:"executors"`

	// Description is free-form operator-typed text shown on
	// /nodes/<name> for the slot. Optional.
	Description string `yaml:"description,omitempty"`

	// CreatedAt is the timestamp the slot was saved. Set by
	// SaveAgent if the caller leaves it zero, so the form
	// handler does not have to stamp it.
	CreatedAt time.Time `yaml:"created_at"`
}

// pendingAgentNamePattern enforces the same shape job names
// use: alphanumeric + dash + underscore + internal dot, 1..128
// chars. Conservative - the name appears as a path segment
// AND in the wolfci-agent invocation shown on the
// connection-command page.
var pendingAgentNamePattern = regexp.MustCompile(
	`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,126}[A-Za-z0-9_-]$|^[A-Za-z0-9_-]$`,
)

// validatePendingAgentName rejects empty / dotted / separator-
// bearing names with an actionable diagnostic.
func validatePendingAgentName(name string) error {
	if name == "" {
		return errors.New("pending agent: name is required")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf(
			"pending agent: name %q contains a path separator",
			name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf(
			"pending agent: name %q must not start with a dot",
			name)
	}
	if !pendingAgentNamePattern.MatchString(name) {
		return fmt.Errorf(
			"pending agent: name %q must match "+
				"[A-Za-z0-9_-] with optional internal dots",
			name)
	}
	return nil
}

// SaveAgent writes a PendingAgent to disk under
// <root>/nodes/<name>/pending.yaml, holding an exclusive
// advisory lock during the write. Intermediate directories
// are created. If CreatedAt is the zero time, it is set to
// time.Now() UTC before write.
func (s *Storage) SaveAgent(p *PendingAgent) error {
	if p == nil {
		return errors.New("storage.SaveAgent: nil PendingAgent")
	}
	if err := validatePendingAgentName(p.Name); err != nil {
		return fmt.Errorf("storage.SaveAgent: %w", err)
	}
	if p.Executors < 1 {
		return fmt.Errorf(
			"storage.SaveAgent: executors must be >= 1 "+
				"(got %d)", p.Executors)
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	path := s.PendingAgentPath(p.Name)
	if err := os.MkdirAll(filepath.Dir(path),
		0o755); err != nil {
		return fmt.Errorf("storage.SaveAgent: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE,
		0o644)
	if err != nil {
		return fmt.Errorf("storage.SaveAgent: open: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()),
		syscall.LOCK_EX); err != nil {
		return fmt.Errorf(
			"storage.SaveAgent: flock LOCK_EX: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}()
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf(
			"storage.SaveAgent: truncate: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("storage.SaveAgent: seek: %w", err)
	}
	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(p); err != nil {
		_ = enc.Close()
		return fmt.Errorf(
			"storage.SaveAgent: yaml encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf(
			"storage.SaveAgent: yaml close: %w", err)
	}
	return nil
}

// LoadPendingAgent reads <root>/nodes/<name>/pending.yaml and
// returns the parsed PendingAgent. Returns an error wrapping
// os.ErrNotExist when the slot does not exist so callers can
// distinguish "no such pending" from a read failure.
func (s *Storage) LoadPendingAgent(
	name string) (*PendingAgent, error) {
	if err := validatePendingAgentName(name); err != nil {
		return nil, fmt.Errorf(
			"storage.LoadPendingAgent: %w", err)
	}
	path := s.PendingAgentPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf(
			"storage.LoadPendingAgent: read %s: %w",
			path, err)
	}
	p := &PendingAgent{}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf(
			"storage.LoadPendingAgent: parse %s: %w",
			path, err)
	}
	return p, nil
}

// ListPendingAgents returns every PendingAgent currently on
// disk under <root>/nodes/. Malformed entries are skipped.
// The returned slice is in directory-listing order
// (typically alphabetical).
func (s *Storage) ListPendingAgents() ([]*PendingAgent, error) {
	nodesDir := filepath.Join(s.root, "nodes")
	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"storage.ListPendingAgents: read %s: %w",
			nodesDir, err)
	}
	var out []*PendingAgent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := s.LoadPendingAgent(e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// DeletePendingAgent removes <root>/nodes/<name>/pending.yaml
// and the surrounding directory (if empty). Called by
// AgentSvc when an agent claims the slot. Returns a wrapped
// os.ErrNotExist when the slot is already gone so the caller
// can decide whether to treat that as a no-op.
func (s *Storage) DeletePendingAgent(name string) error {
	if err := validatePendingAgentName(name); err != nil {
		return fmt.Errorf(
			"storage.DeletePendingAgent: %w", err)
	}
	path := s.PendingAgentPath(name)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf(
			"storage.DeletePendingAgent: %w", err)
	}
	// Best-effort: remove the now-empty directory. Errors
	// here are tolerated since the slot is gone from the
	// catalog as far as ListPendingAgents is concerned.
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// PendingAgentPath returns the canonical on-disk path for a
// pending agent's YAML. Exposed so tests can inspect raw
// bytes; production code goes through SaveAgent / LoadPendingAgent.
func (s *Storage) PendingAgentPath(name string) string {
	return filepath.Join(s.root, "nodes", name, "pending.yaml")
}
