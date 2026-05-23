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

	// Retention bounds how much build history is kept on disk.
	// Nil means "keep every build forever"; the sweeper skips
	// jobs with no Retention block. Either or both fields of
	// the embedded struct may be set.
	Retention *Retention `yaml:"retention,omitempty"`

	// Upstream lists job names that may trigger this job when
	// they succeed. Advisory metadata - the actual fan-out is
	// driven by the upstream job's TriggersDownstream entry
	// for this job, not by this list. Surfacing it here lets
	// the per-job detail page render an "Upstream Projects"
	// section and lets operators reason about the graph from
	// either side. Phase 15.1.
	Upstream []string `yaml:"upstream,omitempty"`

	// TriggersDownstream is the active edge of the graph: when
	// a build of this job reaches StatusSuccess, the scheduler
	// walks this slice and enqueues each named job, passing
	// artifacts as declared. Phase 15.1.
	TriggersDownstream []TriggerSpec `yaml:"triggers_downstream,omitempty"`

	// GitHubPRB carries the GitHub Pull Request Builder trigger
	// configuration when set. Phase 18.5 decision: the existing
	// `triggers: []` field stays as the list of cron/webhook/scm
	// sources; GitHub PRB lives in its own top-level subtree
	// because its config carries lists and booleans the legacy
	// Trigger.Config map[string]string cannot represent.
	// Nil means GitHub PRB polling is not configured.
	GitHubPRB *GitHubPRBTrigger `yaml:"github_prb_trigger,omitempty"`

	// Env is a build-level env overlay applied to every Step's
	// env at execution time. Populated by the scheduler at
	// enqueue time for trigger sources that pass per-build
	// context (e.g. GitHub PRB injects the ghprb* vars here in
	// Phase 18.8). Normal job specs leave this empty.
	Env map[string]string `yaml:"env,omitempty"`
}

// GitHubPRBTrigger is the per-job GitHub Pull Request Builder
// configuration: which GitHub repo to poll, how to talk to the
// GitHub API, which authors are allowed to trigger a build, and
// how often to poll. Phase 18.6+ consumes this to drive a poller
// that emits TriggerEvents and enqueues builds with the right
// ghprb* env vars (Phase 18.9).
type GitHubPRBTrigger struct {
	// APICredentialsID names the secret-text credential in
	// internal/credstore that holds the GitHub API token.
	// Required.
	APICredentialsID string `yaml:"api_credentials_id"`

	// GHProjectURL is the canonical URL of the GitHub repo,
	// e.g. https://github.com/wolfSSL/wolfssl/. Required.
	GHProjectURL string `yaml:"gh_project_url"`

	// AdminUsers is the allowlist of GitHub login names whose
	// PRs auto-build without requiring an "ok to test" comment.
	// PRs from anyone else are queued behind an admin comment.
	AdminUsers []string `yaml:"admin_users,omitempty"`

	// BranchesToBuild is the list of refspecs to match against
	// the PR's target branch (e.g. "*/master", "*/release-*").
	// Empty means every branch matches.
	BranchesToBuild []string `yaml:"branches_to_build,omitempty"`

	// PollIntervalSeconds is the poll cadence in seconds. Zero
	// means "use the default" (300 seconds, decided in PLAN.md
	// Phase 18 decisions); the scheduler applies the default at
	// dispatch time.
	PollIntervalSeconds int `yaml:"poll_interval_seconds,omitempty"`

	// BuildMergeRef toggles between checking out refs/pull/N/merge
	// (the GitHub-computed merge of the PR head into the base,
	// true) and checking out refs/pull/N/head (the PR commit
	// itself, false). Default false.
	BuildMergeRef bool `yaml:"build_merge_ref,omitempty"`
}

// TriggerSpec is one outgoing edge in the downstream trigger
// graph. Name identifies the downstream job; Artifacts are
// file paths (relative to this build's workspace) the
// executor copies into builds/<job>/<n>/artifacts/ and the
// downstream build sees under $WOLFCI_INPUTS/<basename>.
// Phase 15.1 lands the field; Phase 15.3 lands the executor
// copy and the downstream consume.
type TriggerSpec struct {
	Name      string   `yaml:"name"`
	Artifacts []string `yaml:"artifacts,omitempty"`
}

// Retention is the per-job build-history retention policy.
// MaxBuilds keeps the most-recent N completed builds; MaxAge
// (a time.ParseDuration string like "720h") keeps anything
// newer than that age. If both are set, EITHER condition
// protects a build (the more lenient of the two), so an
// operator who wants "at least 30 builds and at least 30
// days" gets exactly that. Zero / empty fields are ignored;
// a Retention block with both fields zero is equivalent to
// no Retention block at all.
type Retention struct {
	MaxBuilds int    `yaml:"max_builds,omitempty"`
	MaxAge    string `yaml:"max_age,omitempty"`
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
//
// Phase 15.1: rejects specs whose TriggersDownstream would close
// a cycle in the trigger graph (returns ErrCycleInTriggerGraph).
func (s *Storage) SaveJob(job *Job) error {
	if job == nil {
		return errors.New("storage.SaveJob: nil Job")
	}
	if job.Name == "" {
		return errors.New("storage.SaveJob: Job.Name is required")
	}
	if err := s.validateNoCycle(job); err != nil {
		return err
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

// SaveSpecSnapshot writes job to builds/<job.Name>/<buildNum>/
// spec.yaml. Phase 14.3 calls this from scheduler.Enqueue so
// "Rebuild Last" can re-enqueue with the exact spec a past
// build saw, even if the live spec has drifted since.
//
// Intermediate directories are created. No lock: each
// snapshot lives at a unique build number, so there is no
// concurrent writer to serialize against.
func (s *Storage) SaveSpecSnapshot(jobName string, buildNum int,
	job *Job) error {
	if jobName == "" {
		return errors.New("storage.SaveSpecSnapshot: jobName is required")
	}
	if buildNum < 1 {
		return errors.New("storage.SaveSpecSnapshot: buildNum must be >= 1")
	}
	if job == nil {
		return errors.New("storage.SaveSpecSnapshot: nil Job")
	}
	dir := filepath.Join(s.root, "builds", jobName,
		fmt.Sprintf("%d", buildNum))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage.SaveSpecSnapshot: mkdir: %w", err)
	}
	data, err := yaml.Marshal(job)
	if err != nil {
		return fmt.Errorf("storage.SaveSpecSnapshot: marshal: %w", err)
	}
	path := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("storage.SaveSpecSnapshot: write: %w", err)
	}
	return nil
}

// LoadSpecSnapshot reads builds/<jobName>/<buildNum>/spec.yaml.
// Returns os.ErrNotExist if no snapshot was written for this
// build (older builds from before Phase 14.3, or third-party
// executors that bypassed the scheduler).
func (s *Storage) LoadSpecSnapshot(jobName string, buildNum int) (
	*Job, error) {
	if jobName == "" {
		return nil, errors.New("storage.LoadSpecSnapshot: jobName is required")
	}
	if buildNum < 1 {
		return nil, errors.New("storage.LoadSpecSnapshot: buildNum must be >= 1")
	}
	path := filepath.Join(s.root, "builds", jobName,
		fmt.Sprintf("%d", buildNum), "spec.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("storage.LoadSpecSnapshot: read: %w", err)
	}
	var job Job
	if err := yaml.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("storage.LoadSpecSnapshot: parse: %w", err)
	}
	return &job, nil
}

// ErrJobExists is returned by RenameJob when the target name
// is already taken. Callers map this to 409 Conflict in the
// HTTP layer.
var ErrJobExists = errors.New("storage: target job name already exists")

// RenameJob moves jobs/<oldName>/ to jobs/<newName>/, rewrites
// the spec's Name field to newName, and moves
// builds/<oldName>/ to builds/<newName>/ (if it exists). The
// spec move is the source of truth: if it succeeds, the
// rename is considered to have happened, and a follow-up
// builds-move failure is reported as an error but does not
// roll back the spec move (the operator can recover by
// renaming the builds directory by hand).
//
// Returns ErrJobExists when jobs/<newName>/ is already on
// disk so the caller can reject without clobbering. Returns
// os.ErrNotExist when jobs/<oldName>/ is missing.
func (s *Storage) RenameJob(oldName, newName string) error {
	if oldName == "" || newName == "" {
		return errors.New("storage.RenameJob: oldName and newName are required")
	}
	if oldName == newName {
		return nil
	}
	oldDir := filepath.Join(s.root, "jobs", oldName)
	newDir := filepath.Join(s.root, "jobs", newName)
	if _, err := os.Stat(oldDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("storage.RenameJob: stat old: %w", err)
	}
	if _, err := os.Stat(newDir); err == nil {
		return ErrJobExists
	}
	job, err := s.LoadJob(oldName)
	if err != nil {
		return fmt.Errorf("storage.RenameJob: load: %w", err)
	}
	job.Name = newName
	if err := s.SaveJob(job); err != nil {
		return fmt.Errorf("storage.RenameJob: save new: %w", err)
	}
	if err := s.DeleteJob(oldName); err != nil {
		return fmt.Errorf("storage.RenameJob: delete old: %w", err)
	}
	oldBuilds := filepath.Join(s.root, "builds", oldName)
	if _, err := os.Stat(oldBuilds); err == nil {
		newBuilds := filepath.Join(s.root, "builds", newName)
		if err := os.Rename(oldBuilds, newBuilds); err != nil {
			return fmt.Errorf("storage.RenameJob: "+
				"move builds: %w (spec already renamed)",
				err)
		}
	}
	return nil
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
