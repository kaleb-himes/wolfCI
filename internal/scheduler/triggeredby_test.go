package scheduler_test

/* Phase 15.2 - BuildResult.TriggeredBy attribution.
 *
 * A downstream build records which (upstream job, build
 * number) caused its enqueue. result.json round-trips the
 * field via standard json encoding; an empty TriggeredBy
 * means "root build" (operator clicked Run or a trigger
 * fired against an upstream that itself had no parent).
 */

import (
    "encoding/json"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
)

func TestBuildResult_TriggeredByRoundtrip(t *testing.T) {
    original := scheduler.BuildResult{
        JobName: "windows-test",
        Number:  7,
        Status:  scheduler.StatusSuccess,
        TriggeredBy: &scheduler.BuildRef{
            Job:   "linux-build",
            Build: 42,
        },
    }
    data, err := json.Marshal(original)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var loaded scheduler.BuildResult
    if err := json.Unmarshal(data, &loaded); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if loaded.TriggeredBy == nil {
        t.Fatalf("TriggeredBy roundtripped to nil")
    }
    if loaded.TriggeredBy.Job != "linux-build" {
        t.Errorf("TriggeredBy.Job = %q, want linux-build",
            loaded.TriggeredBy.Job)
    }
    if loaded.TriggeredBy.Build != 42 {
        t.Errorf("TriggeredBy.Build = %d, want 42",
            loaded.TriggeredBy.Build)
    }
}

func TestBuildResult_TriggeredByEmptyForRootBuild(t *testing.T) {
    /* An operator-triggered build (Run button, /jobs/<n>/run
     * POST) has no parent. TriggeredBy must be the zero
     * value and must serialize compactly so old consumers
     * that ignore the field do not see a misleading
     * "Job: ''" entry.
     */
    original := scheduler.BuildResult{
        JobName: "linux-build",
        Number:  1,
        Status:  scheduler.StatusSuccess,
    }
    if original.TriggeredBy != nil {
        t.Errorf("default TriggeredBy = %+v, want nil",
            original.TriggeredBy)
    }
    data, err := json.Marshal(original)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    /* omitempty on the field means the JSON should NOT
     * contain "triggered_by" when the ref is empty.
     */
    if got := string(data); contains(got, "triggered_by") {
        t.Errorf("root-build JSON contains triggered_by: %s",
            got)
    }
}

func contains(s, sub string) bool {
    for i := 0; i+len(sub) <= len(s); i++ {
        if s[i:i+len(sub)] == sub {
            return true
        }
    }
    return false
}
