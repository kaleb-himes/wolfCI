package storage_test

/* internal/storage/pending_agent_test.go - PLAN.md 19.1
 * gating tests.
 *
 * TestPendingAgent_Roundtrip writes a PendingAgent with each
 * field populated, reads it back via LoadPendingAgent, and
 * asserts the round-trip preserves every field including
 * CreatedAt (within a tolerance). ListPendingAgents returns
 * the entry. DeletePendingAgent removes it.
 *
 * TestPendingAgent_Validation walks the rejection rules:
 * empty name, names with path separators, names with leading
 * dot, executors < 1, nil receiver.
 */

import (
    "os"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestPendingAgent_Roundtrip(t *testing.T) {
    s, err := storage.New(t.TempDir())
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    in := &storage.PendingAgent{
        Name:        "linux-builder-1",
        Labels:      []string{"linux", "build", "x86_64"},
        Executors:   4,
        Description: "permanent linux build node",
    }
    if err := s.SaveAgent(in); err != nil {
        t.Fatalf("SaveAgent: %v", err)
    }
    if in.CreatedAt.IsZero() {
        t.Errorf("SaveAgent did not stamp CreatedAt")
    }
    out, err := s.LoadPendingAgent("linux-builder-1")
    if err != nil {
        t.Fatalf("LoadPendingAgent: %v", err)
    }
    if out.Name != in.Name {
        t.Errorf("Name = %q, want %q", out.Name, in.Name)
    }
    if !reflect.DeepEqual(out.Labels, in.Labels) {
        t.Errorf("Labels = %v, want %v", out.Labels, in.Labels)
    }
    if out.Executors != in.Executors {
        t.Errorf("Executors = %d, want %d",
            out.Executors, in.Executors)
    }
    if out.Description != in.Description {
        t.Errorf("Description = %q, want %q",
            out.Description, in.Description)
    }
    drift := out.CreatedAt.Sub(in.CreatedAt)
    if drift < 0 {
        drift = -drift
    }
    if drift > time.Second {
        t.Errorf("CreatedAt round-trip drifted by %v", drift)
    }
    list, err := s.ListPendingAgents()
    if err != nil {
        t.Fatalf("ListPendingAgents: %v", err)
    }
    if len(list) != 1 || list[0].Name != "linux-builder-1" {
        t.Fatalf("ListPendingAgents = %+v, want one entry",
            list)
    }
    /* On-disk path matches PendingAgentPath. */
    path := s.PendingAgentPath("linux-builder-1")
    if _, err := os.Stat(path); err != nil {
        t.Errorf("expected file at %s: %v", path, err)
    }
    /* The pending YAML lives under nodes/<name>/pending.yaml. */
    if filepath.Base(filepath.Dir(path)) != "linux-builder-1" {
        t.Errorf("unexpected path layout: %s", path)
    }
    if err := s.DeletePendingAgent("linux-builder-1"); err != nil {
        t.Fatalf("DeletePendingAgent: %v", err)
    }
    list, err = s.ListPendingAgents()
    if err != nil {
        t.Fatalf("ListPendingAgents after delete: %v", err)
    }
    if len(list) != 0 {
        t.Errorf("after delete: %d entries remain, want 0",
            len(list))
    }
}

func TestPendingAgent_Validation(t *testing.T) {
    s, err := storage.New(t.TempDir())
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    cases := []struct {
        name    string
        agent   *storage.PendingAgent
        wantErr bool
    }{
        {"nil receiver", nil, true},
        {"empty name",
            &storage.PendingAgent{Executors: 1}, true},
        {"path separator",
            &storage.PendingAgent{
                Name: "foo/bar", Executors: 1}, true},
        {"backslash",
            &storage.PendingAgent{
                Name: "foo\\bar", Executors: 1}, true},
        {"leading dot",
            &storage.PendingAgent{
                Name: ".hidden", Executors: 1}, true},
        {"zero executors",
            &storage.PendingAgent{
                Name: "ok-name", Executors: 0}, true},
        {"negative executors",
            &storage.PendingAgent{
                Name: "ok-name", Executors: -1}, true},
        {"good",
            &storage.PendingAgent{
                Name: "ok-name", Executors: 1}, false},
        {"good with dots",
            &storage.PendingAgent{
                Name: "ci.linux.01", Executors: 2}, false},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            err := s.SaveAgent(c.agent)
            if c.wantErr && err == nil {
                t.Errorf("expected error, got nil")
            }
            if !c.wantErr && err != nil {
                t.Errorf("unexpected error: %v", err)
            }
        })
    }
}
