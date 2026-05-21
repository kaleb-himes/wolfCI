package scheduler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestLocalExecutor_SuccessAndLog is the gating test for PLAN.md
// task 4.3. A successful one-step job leaves a log file with the
// step's stdout and a result.json that round-trips through
// encoding/json.
func TestLocalExecutor_SuccessAndLog(t *testing.T) {
	store := mustStore(t)
	exec := scheduler.NewLocalExecutor(store)

	job := &storage.Job{
		Name: "demo",
		Steps: []storage.Step{
			{Name: "echo", Shell: "echo hello world"},
		},
	}

	result := exec.Execute(context.Background(), job, 42)

	if result.Status != scheduler.StatusSuccess {
		t.Errorf("Status = %q, want %q", result.Status, scheduler.StatusSuccess)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	logPath := filepath.Join(store.Root(), "builds", "demo", "42", "log")
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logBytes), "hello world") {
		t.Errorf("log = %q, want to contain 'hello world'", logBytes)
	}

	resultPath := filepath.Join(store.Root(), "builds", "demo", "42", "result.json")
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	var loaded scheduler.BuildResult
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("parse result.json: %v", err)
	}
	if loaded.Status != scheduler.StatusSuccess {
		t.Errorf("result.json Status = %q, want %q", loaded.Status, scheduler.StatusSuccess)
	}
	if loaded.JobName != "demo" {
		t.Errorf("result.json JobName = %q, want demo", loaded.JobName)
	}
	if loaded.Number != 42 {
		t.Errorf("result.json Number = %d, want 42", loaded.Number)
	}
}

// TestLocalExecutor_FailingStep verifies that a non-zero exit
// terminates the build, sets Status=failure, and records the
// shell's exit code.
func TestLocalExecutor_FailingStep(t *testing.T) {
	store := mustStore(t)
	exec := scheduler.NewLocalExecutor(store)

	job := &storage.Job{
		Name:  "fail",
		Steps: []storage.Step{{Shell: "exit 7"}},
	}

	result := exec.Execute(context.Background(), job, 1)
	if result.Status != scheduler.StatusFailure {
		t.Errorf("Status = %q, want %q", result.Status, scheduler.StatusFailure)
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
}

// TestLocalExecutor_StopsAtFirstFailure proves that later steps
// do not run after an earlier step fails.
func TestLocalExecutor_StopsAtFirstFailure(t *testing.T) {
	store := mustStore(t)
	exec := scheduler.NewLocalExecutor(store)

	job := &storage.Job{
		Name: "multi",
		Steps: []storage.Step{
			{Name: "first", Shell: "echo first-step-ran"},
			{Name: "boom", Shell: "exit 3"},
			{Name: "skipped", Shell: "echo should-not-run"},
		},
	}

	result := exec.Execute(context.Background(), job, 1)
	if result.Status != scheduler.StatusFailure {
		t.Errorf("Status = %q, want failure", result.Status)
	}
	if result.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", result.ExitCode)
	}

	logBytes, err := os.ReadFile(filepath.Join(store.Root(), "builds", "multi", "1", "log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "first-step-ran") {
		t.Errorf("log should show first step output, got %q", log)
	}
	if strings.Contains(log, "should-not-run") {
		t.Errorf("log should NOT show third step output, got %q", log)
	}
}

// TestLocalExecutor_PerStepEnv verifies that Step.Env is overlaid
// on the host environment for that step.
func TestLocalExecutor_PerStepEnv(t *testing.T) {
	store := mustStore(t)
	exec := scheduler.NewLocalExecutor(store)

	job := &storage.Job{
		Name: "envtest",
		Steps: []storage.Step{
			{Shell: `echo "WOLFCI_TEST=$WOLFCI_TEST"`, Env: map[string]string{"WOLFCI_TEST": "value-from-step"}},
		},
	}

	result := exec.Execute(context.Background(), job, 1)
	if result.Status != scheduler.StatusSuccess {
		t.Fatalf("Status = %q, want success", result.Status)
	}
	logBytes, _ := os.ReadFile(filepath.Join(store.Root(), "builds", "envtest", "1", "log"))
	if !strings.Contains(string(logBytes), "WOLFCI_TEST=value-from-step") {
		t.Errorf("log should contain 'WOLFCI_TEST=value-from-step', got %q", logBytes)
	}
}
