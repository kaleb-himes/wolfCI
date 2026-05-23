package agentsvc_test

/* internal/agentsvc/pending_test.go - PLAN.md 19.4 gating
 * tests.
 *
 * TestAgentSvc_RegisterClaimsPendingAgent seeds a
 * PendingAgentClaimer with one pending record, calls
 * Server.Register with the matching agent_id, and asserts:
 *
 *   1. The resulting AgentInfo carries the pending's Labels
 *      and Executors (overriding the values the agent passed
 *      in the RegisterRequest).
 *   2. The pending record is removed (the claimer's Delete
 *      method was called exactly once).
 *
 * TestAgentSvc_RegisterIgnoresNonPending exercises the
 * pass-through path: when no pending record matches, the
 * stored AgentInfo keeps the agent-supplied Labels +
 * Executors verbatim.
 *
 * Uses an in-process Server (no mTLS round-trip) so the test
 * stays focused on the Register claim logic; the wire path
 * is covered by TestAgentService_RegisterOverMTLS.
 */

import (
    "context"
    "testing"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
    "github.com/kaleb-himes/wolfCI/internal/agentsvc"
)

func TestAgentSvc_RegisterClaimsPendingAgent(t *testing.T) {
    claimer := &fakePendingClaimer{
        records: map[string]fakePending{
            "linux-builder-1": {
                labels:    []string{"linux", "build", "x86_64"},
                executors: 4,
            },
        },
    }
    srv := agentsvc.New("test-version")
    srv.PendingClaimer = claimer

    /* The agent self-describes with executors=1 + an
     * unrelated label - the claim should override both. */
    resp, err := srv.Register(context.Background(),
        &wolfciv1.AgentInfo{
            AgentId:   "linux-builder-1",
            Executors: 1,
            Labels:    []string{"self-reported-label"},
        })
    if err != nil {
        t.Fatalf("Register: %v", err)
    }
    if !resp.Accepted {
        t.Fatalf("Register: Accepted = false")
    }
    /* Read back through Agents(). */
    agents := srv.Agents()
    var info *wolfciv1.AgentInfo
    for _, a := range agents {
        if a.AgentId == "linux-builder-1" {
            info = a
            break
        }
    }
    if info == nil {
        t.Fatalf("registered agent not found in Agents()")
    }
    if info.Executors != 4 {
        t.Errorf("Executors = %d, want 4 (pending value)",
            info.Executors)
    }
    if len(info.Labels) != 3 ||
        info.Labels[0] != "linux" ||
        info.Labels[1] != "build" ||
        info.Labels[2] != "x86_64" {
        t.Errorf("Labels = %v, want [linux build x86_64] "+
            "(pending values, overriding self-reported)",
            info.Labels)
    }
    if claimer.deletes != 1 {
        t.Errorf("DeletePendingAgent calls = %d, want 1",
            claimer.deletes)
    }
    /* The pending record is gone. */
    if _, _, found, _ :=
        claimer.LookupPendingAgent(
            "linux-builder-1"); found {
        t.Errorf("pending record still present after claim")
    }
}

func TestAgentSvc_RegisterIgnoresNonPending(t *testing.T) {
    claimer := &fakePendingClaimer{
        records: map[string]fakePending{},
    }
    srv := agentsvc.New("test-version")
    srv.PendingClaimer = claimer

    _, err := srv.Register(context.Background(),
        &wolfciv1.AgentInfo{
            AgentId:   "unknown-agent",
            Executors: 2,
            Labels:    []string{"ad-hoc"},
        })
    if err != nil {
        t.Fatalf("Register: %v", err)
    }
    agents := srv.Agents()
    var info *wolfciv1.AgentInfo
    for _, a := range agents {
        if a.AgentId == "unknown-agent" {
            info = a
            break
        }
    }
    if info == nil {
        t.Fatalf("registered agent not found")
    }
    if info.Executors != 2 {
        t.Errorf("Executors = %d, want 2 (agent-supplied)",
            info.Executors)
    }
    if len(info.Labels) != 1 || info.Labels[0] != "ad-hoc" {
        t.Errorf("Labels = %v, want [ad-hoc]", info.Labels)
    }
    if claimer.deletes != 0 {
        t.Errorf("DeletePendingAgent calls = %d, want 0 "+
            "(no claim should have happened)",
            claimer.deletes)
    }
}

/* fakePendingClaimer is the test-local
 * agentsvc.PendingAgentClaimer: a map of agent_id ->
 * (labels, executors) plus a counter for Delete calls so the
 * gating tests can assert one-shot claim semantics without
 * touching disk. */
type fakePendingClaimer struct {
    records map[string]fakePending
    deletes int
}

type fakePending struct {
    labels    []string
    executors int
}

func (f *fakePendingClaimer) LookupPendingAgent(
    name string) ([]string, int, bool, error) {
    rec, ok := f.records[name]
    if !ok {
        return nil, 0, false, nil
    }
    return rec.labels, rec.executors, true, nil
}

func (f *fakePendingClaimer) DeletePendingAgent(
    name string) error {
    delete(f.records, name)
    f.deletes++
    return nil
}
