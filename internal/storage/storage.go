// Package storage persists wolfCI artifacts as plain files on
// disk. There is no external database.
//
// On-disk layout (rooted at the path passed to New):
//
//	jobs/<name>/job.yaml         - job spec, YAML 1.2
//	builds/<job>/<n>/result.json - build result, JSON (Phase 4)
//	builds/<job>/<n>/log         - build log stream (Phase 4)
//
// Concurrent writers are serialized with POSIX advisory locks
// (flock(2)) on the target file. Readers take a shared lock.
package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Storage manages on-disk persistence rooted at a single directory.
// All paths returned by JobPath and friends are absolute joins
// against this root.
type Storage struct {
	root string
}

// New returns a Storage rooted at the given directory. The directory
// itself is NOT auto-created; the caller decides whether to mkdir.
// SaveJob will create intermediate directories under root as needed.
func New(root string) (*Storage, error) {
	if root == "" {
		return nil, errors.New("storage.New: root is required")
	}
	return &Storage{root: root}, nil
}

// Root returns the storage root directory.
func (s *Storage) Root() string { return s.root }

// JobPath returns the on-disk path for the named job's spec file.
func (s *Storage) JobPath(name string) string {
	return filepath.Join(s.root, "jobs", name, "job.yaml")
}

// Job is the wolfCI job specification, modeled on Jenkins's mental
// model but kept narrow. New fields require an update to the
// round-trip test in storage_test.go.
type Job struct {
	// Name is the job identifier; used as the directory name under
	// jobs/. Required.
	Name string `yaml:"name"`

	// Description is free-form human text shown in the UI.
	Description string `yaml:"description,omitempty"`

	// NodeLabel restricts the job to executors whose configured
	// labels include this value. Empty matches any node.
	NodeLabel string `yaml:"node_label,omitempty"`

	// Timeout is a time.ParseDuration-compatible string, e.g. "5m".
	// Empty means no timeout.
	Timeout string `yaml:"timeout,omitempty"`

	// Retries is the number of retry attempts after the first
	// failure. Zero means no retries.
	Retries int `yaml:"retries,omitempty"`

	// Triggers cause the job to enqueue a build.
	Triggers []Trigger `yaml:"triggers,omitempty"`

	// Parameters are user-supplied inputs for a build.
	Parameters []Parameter `yaml:"parameters,omitempty"`

	// Steps run in order on the assigned executor.
	Steps []Step `yaml:"steps"`

	// Axis declares matrix dimensions. A build fans out into the
	// cartesian product of all dimensions.
	Axis []AxisDimension `yaml:"axis,omitempty"`
}

// Trigger names a single trigger source and its configuration.
type Trigger struct {
	// Type is the trigger kind, e.g. "cron", "webhook", "scm".
	Type string `yaml:"type"`

	// Config holds type-specific options. For "cron" this might be
	// {"schedule": "0 * * * *"}.
	Config map[string]string `yaml:"config,omitempty"`
}

// Parameter is a build input.
type Parameter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Default     string `yaml:"default,omitempty"`
	Required    bool   `yaml:"required,omitempty"`
}

// Step is one shell command in a job's pipeline.
type Step struct {
	// Name is shown in build logs and the UI.
	Name string `yaml:"name,omitempty"`

	// Shell is the command run via /bin/sh -c.
	Shell string `yaml:"shell,omitempty"`

	// Env are environment variables overlaid on the executor's env
	// for the duration of this step.
	Env map[string]string `yaml:"env,omitempty"`
}

// AxisDimension is one matrix dimension with its candidate values.
type AxisDimension struct {
	Name   string   `yaml:"name"`
	Values []string `yaml:"values"`
}

// SaveJob writes job to its canonical location under root, holding
// an exclusive advisory lock during the write. Intermediate
// directories are created.
func (s *Storage) SaveJob(job *Job) error {
	if job == nil {
		return errors.New("storage.SaveJob: nil Job")
	}
	if job.Name == "" {
		return errors.New("storage.SaveJob: Job.Name is required")
	}

	path := s.JobPath(job.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("storage.SaveJob: mkdir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("storage.SaveJob: open: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("storage.SaveJob: flock LOCK_EX: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("storage.SaveJob: truncate: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("storage.SaveJob: seek: %w", err)
	}

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(job); err != nil {
		_ = enc.Close()
		return fmt.Errorf("storage.SaveJob: yaml encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("storage.SaveJob: yaml close: %w", err)
	}
	return nil
}

// ListJobs returns every Job whose spec is currently on disk
// under <root>/jobs/. Malformed entries are skipped. The
// returned slice is in directory-listing order (typically
// alphabetical).
func (s *Storage) ListJobs() ([]*Job, error) {
	jobsDir := filepath.Join(s.root, "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage.ListJobs: read %s: %w", jobsDir, err)
	}
	var out []*Job
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		job, err := s.LoadJob(e.Name())
		if err != nil {
			continue
		}
		out = append(out, job)
	}
	return out, nil
}

// DeleteJob removes the named job's spec directory
// (jobs/<name>/) including any sibling files inside it. It
// does NOT touch builds/<name>/: the operator can re-create
// the job under the same name and the history is still on
// disk. A separate "wipe history too" affordance is reserved
// for a destructive UI flow.
//
// Returns os.ErrNotExist if jobs/<name>/ is already gone, so
// callers can distinguish "already deleted" from a real I/O
// failure.
func (s *Storage) DeleteJob(name string) error {
	if name == "" {
		return errors.New("storage.DeleteJob: name is required")
	}
	jobDir := filepath.Join(s.root, "jobs", name)
	if _, err := os.Stat(jobDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("storage.DeleteJob: stat: %w", err)
	}
	if err := os.RemoveAll(jobDir); err != nil {
		return fmt.Errorf("storage.DeleteJob: remove: %w", err)
	}
	return nil
}

// LoadJob reads and decodes the named job from disk, taking a
// shared lock for the duration of the read.
func (s *Storage) LoadJob(name string) (*Job, error) {
	if name == "" {
		return nil, errors.New("storage.LoadJob: name is required")
	}
	path := s.JobPath(name)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("storage.LoadJob: open: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		return nil, fmt.Errorf("storage.LoadJob: flock LOCK_SH: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	var job Job
	if err := yaml.NewDecoder(f).Decode(&job); err != nil {
		return nil, fmt.Errorf("storage.LoadJob: yaml decode: %w", err)
	}
	return &job, nil
}
