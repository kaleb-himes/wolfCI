package storage_test

import (
	"reflect"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestStorage_RoundtripJob is the gating test for PLAN.md tasks
// 2.1 (failing test), 2.2 (on-disk format), and 2.3 (storage
// implementation with file locking).
//
// It writes a fully-populated Job spec to a temporary storage
// root, reads it back, and asserts the loaded Job equals the
// original. Any field added to Job must be exercised here or the
// next round-trip change risks silent data loss.
func TestStorage_RoundtripJob(t *testing.T) {
	root := t.TempDir()
	s, err := storage.New(root)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}

	original := &storage.Job{
		Name:        "demo",
		Description: "a demo job covering every spec field",
		NodeLabel:   "linux",
		Timeout:     "5m",
		Retries:     2,
		Triggers: []storage.Trigger{
			{Type: "cron", Config: map[string]string{"schedule": "0 * * * *"}},
			{Type: "webhook"},
		},
		Parameters: []storage.Parameter{
			{Name: "branch", Description: "git branch", Default: "main", Required: true},
			{Name: "verbose", Default: "false"},
		},
		Steps: []storage.Step{
			{Name: "build", Shell: "make all", Env: map[string]string{"CC": "gcc"}},
			{Name: "test", Shell: "make test"},
		},
		Axis: []storage.AxisDimension{
			{Name: "os", Values: []string{"linux", "macos"}},
			{Name: "go", Values: []string{"1.21", "1.22"}},
		},
	}

	if err := s.SaveJob(original); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	loaded, err := s.LoadJob("demo")
	if err != nil {
		t.Fatalf("LoadJob: %v", err)
	}

	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("Job round-trip mismatch.\noriginal: %+v\nloaded:   %+v", original, loaded)
	}
}

// TestStorage_LoadJob_NotFound covers the error path for a job
// that was never saved.
func TestStorage_LoadJob_NotFound(t *testing.T) {
	root := t.TempDir()
	s, err := storage.New(root)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if _, err := s.LoadJob("does-not-exist"); err == nil {
		t.Fatal("LoadJob: expected error for missing job, got nil")
	}
}

// TestStorage_SaveJob_RequiresName guards against writing a job
// with no name (which would result in jobs//job.yaml).
func TestStorage_SaveJob_RequiresName(t *testing.T) {
	root := t.TempDir()
	s, err := storage.New(root)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := s.SaveJob(&storage.Job{}); err == nil {
		t.Fatal("SaveJob: expected error for empty Name, got nil")
	}
}
