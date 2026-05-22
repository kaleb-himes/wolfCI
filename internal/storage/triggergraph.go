package storage

/* Phase 15.1 - trigger graph cycle check.
 *
 * A job spec may declare TriggersDownstream, a list of jobs
 * to enqueue after a successful build. The graph these edges
 * form must stay acyclic: a cycle would cause builds to
 * cascade forever once any node in the cycle ran.
 *
 * SaveJob calls validateNoCycle BEFORE writing the new spec.
 * The check assumes the persisted graph (every other job
 * already on disk) is itself acyclic - prior saves validated
 * that invariant, so the only new risk is the spec we are
 * about to write.
 *
 * Algorithm: build the directed graph from "every other job
 * on disk" + the new spec; from each direct downstream of
 * the new spec, DFS forward; if the new spec's own name is
 * reachable, we have a cycle. Self-loops (TriggersDownstream
 * containing the new spec's own name) are caught by the
 * same DFS without a special case.
 */

import (
    "errors"
    "fmt"
)

// ErrCycleInTriggerGraph is returned by SaveJob when the spec
// being saved would close a loop in the downstream trigger
// graph. Callers map this to 400 Bad Request in the HTTP
// edit handler.
var ErrCycleInTriggerGraph = errors.New(
    "storage: TriggersDownstream would close a cycle")

/* validateNoCycle returns nil iff saving newSpec keeps the
 * trigger graph acyclic. It reads every other job on disk
 * to build the graph; the caller is expected to hold no
 * lock since this is a read-only pass.
 */
func (s *Storage) validateNoCycle(newSpec *Job) error {
    if newSpec == nil || len(newSpec.TriggersDownstream) == 0 {
        return nil
    }
    existing, err := s.ListJobs()
    if err != nil {
        return fmt.Errorf("storage.validateNoCycle: list: %w", err)
    }
    /* graph[x] is the set of downstream job names x triggers.
     * Use the new spec's edges in place of any existing
     * spec with the same name so an in-flight edit can
     * REMOVE an edge without false-positiving on the old
     * persisted version.
     */
    graph := make(map[string][]string, len(existing)+1)
    for _, j := range existing {
        if j.Name == newSpec.Name {
            continue
        }
        graph[j.Name] = downstreamNames(j)
    }
    graph[newSpec.Name] = downstreamNames(newSpec)

    /* From each direct downstream of the new spec, DFS
     * forward through the graph. If we re-enter the new
     * spec's name, the new edge closed a cycle.
     */
    target := newSpec.Name
    visited := make(map[string]bool)
    for _, ts := range newSpec.TriggersDownstream {
        if reachable(graph, ts.Name, target, visited) {
            return fmt.Errorf("%w: %s -> ... -> %s",
                ErrCycleInTriggerGraph, target, target)
        }
    }
    return nil
}

func downstreamNames(j *Job) []string {
    if len(j.TriggersDownstream) == 0 {
        return nil
    }
    out := make([]string, 0, len(j.TriggersDownstream))
    for _, ts := range j.TriggersDownstream {
        if ts.Name == "" {
            continue
        }
        out = append(out, ts.Name)
    }
    return out
}

/* reachable returns true iff target is reachable from start
 * in graph. visited is a shared work-in-progress set so the
 * caller can amortize the DFS across multiple starts.
 */
func reachable(graph map[string][]string, start, target string,
    visited map[string]bool) bool {

    if start == target {
        return true
    }
    if visited[start] {
        return false
    }
    visited[start] = true
    for _, next := range graph[start] {
        if reachable(graph, next, target, visited) {
            return true
        }
    }
    return false
}
