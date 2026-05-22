package storage_test

/* Phase 15.1 - trigger graph cycle check.
 *
 * A job spec may declare TriggersDownstream: a list of jobs
 * the scheduler enqueues after a successful build. If the
 * graph closes a loop, builds would cascade forever. SaveJob
 * rejects specs that would close such a loop and returns
 * ErrCycleInTriggerGraph.
 */

import (
    "errors"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJob_TriggerGraphAcyclic_OK(t *testing.T) {
    root := t.TempDir()
    s, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }

    /* A -> B -> C is acyclic. All three SaveJobs succeed. */
    if err := s.SaveJob(&storage.Job{
        Name: "A",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    if err := s.SaveJob(&storage.Job{
        Name: "B",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "C"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob B: %v", err)
    }
    if err := s.SaveJob(&storage.Job{
        Name:  "C",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob C: %v", err)
    }
}

func TestJob_TriggerGraphRejectsSelfLoop(t *testing.T) {
    root := t.TempDir()
    s, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    err = s.SaveJob(&storage.Job{
        Name: "self",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "self"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    })
    if !errors.Is(err, storage.ErrCycleInTriggerGraph) {
        t.Fatalf("SaveJob self-loop err = %v, want ErrCycleInTriggerGraph",
            err)
    }
    /* And the spec was NOT persisted. */
    if _, err := s.LoadJob("self"); err == nil {
        t.Errorf("self-loop spec persisted despite the cycle")
    }
}

func TestJob_TriggerGraphRejectsTwoNodeCycle(t *testing.T) {
    root := t.TempDir()
    s, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }

    if err := s.SaveJob(&storage.Job{
        Name: "A",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    /* Saving B with B -> A closes the loop. */
    err = s.SaveJob(&storage.Job{
        Name: "B",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "A"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    })
    if !errors.Is(err, storage.ErrCycleInTriggerGraph) {
        t.Fatalf("SaveJob B err = %v, want ErrCycleInTriggerGraph",
            err)
    }
    /* B is not on disk; A is unchanged. */
    if _, err := s.LoadJob("B"); err == nil {
        t.Errorf("two-node cycle persisted B")
    }
    loadedA, err := s.LoadJob("A")
    if err != nil {
        t.Fatalf("LoadJob A: %v", err)
    }
    if len(loadedA.TriggersDownstream) != 1 ||
        loadedA.TriggersDownstream[0].Name != "B" {
        t.Errorf("A's downstream changed: %+v",
            loadedA.TriggersDownstream)
    }
}

func TestJob_TriggerGraphRejectsLongerCycle(t *testing.T) {
    root := t.TempDir()
    s, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }

    /* A -> B -> C -> D acyclic. Then saving D with D -> A
     * closes a four-node cycle.
     */
    edges := []struct{ from, to string }{
        {"A", "B"},
        {"B", "C"},
        {"C", "D"},
    }
    for _, e := range edges {
        if err := s.SaveJob(&storage.Job{
            Name: e.from,
            TriggersDownstream: []storage.TriggerSpec{
                {Name: e.to},
            },
            Steps: []storage.Step{{Shell: "true"}},
        }); err != nil {
            t.Fatalf("SaveJob %s: %v", e.from, err)
        }
    }
    /* D is the terminal node; saving D with downstream A
     * closes the loop A -> B -> C -> D -> A.
     */
    err = s.SaveJob(&storage.Job{
        Name: "D",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "A"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    })
    if !errors.Is(err, storage.ErrCycleInTriggerGraph) {
        t.Fatalf("SaveJob D err = %v, want ErrCycleInTriggerGraph",
            err)
    }
}
