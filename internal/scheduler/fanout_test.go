package scheduler_test

/* Phase 15.4 - trigger fan-out on success.
 *
 * After every BuildResult with Status==Success, the
 * scheduler walks the just-finished job's TriggersDownstream
 * and Enqueues each named downstream job with TriggeredBy
 * set to the parent ref. Failures, cancels, and errors do
 * NOT fan out. Missing downstream specs (named but not on
 * disk) log a warning rather than crashing.
 */

import (
    "context"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestScheduler_FanoutOnSuccess(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* Upstream A triggers downstreams B and C. */
    if err := store.SaveJob(&storage.Job{
        Name: "A",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
            {Name: "C"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:  "B",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob B: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:  "C",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob C: %v", err)
    }

    exec := &recordingExecutor{
        result: scheduler.BuildResult{
            Status: scheduler.StatusSuccess,
        },
    }
    s := scheduler.New(store, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    s.Start(ctx)
    defer s.Stop()

    /* Enqueue A. Wait for B and C to also flow through the
     * executor.
     */
    upJob, err := store.LoadJob("A")
    if err != nil {
        t.Fatalf("LoadJob A: %v", err)
    }
    _, _, err = s.Enqueue(upJob)
    if err != nil {
        t.Fatalf("Enqueue A: %v", err)
    }

    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if exec.callCount() >= 3 {
            break
        }
        time.Sleep(10 * time.Millisecond)
    }
    seen := exec.calls()
    if len(seen) < 3 {
        t.Fatalf("executor saw %d builds, want at least 3 "+
            "(A + fan-out B + C); got=%+v", len(seen), seen)
    }
    /* Check that B and C ran with TriggeredBy=A. */
    var sawB, sawC bool
    for _, c := range seen {
        if c.job == "B" {
            sawB = true
            if c.parent == nil || c.parent.Job != "A" {
                t.Errorf("B's parent = %+v, want {Job:A,...}",
                    c.parent)
            }
        }
        if c.job == "C" {
            sawC = true
            if c.parent == nil || c.parent.Job != "A" {
                t.Errorf("C's parent = %+v, want {Job:A,...}",
                    c.parent)
            }
        }
    }
    if !sawB || !sawC {
        t.Errorf("fan-out did not enqueue both B and C: "+
            "sawB=%v sawC=%v", sawB, sawC)
    }
}

func TestScheduler_NoFanoutOnFailure(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name: "A",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
        },
        Steps: []storage.Step{{Shell: "false"}},
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:  "B",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob B: %v", err)
    }

    exec := &recordingExecutor{
        result: scheduler.BuildResult{
            Status: scheduler.StatusFailure,
        },
    }
    s := scheduler.New(store, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    s.Start(ctx)
    defer s.Stop()

    upJob, _ := store.LoadJob("A")
    _, _, err = s.Enqueue(upJob)
    if err != nil {
        t.Fatalf("Enqueue A: %v", err)
    }

    /* Give the scheduler a moment to do the (lack of)
     * fan-out work. We expect EXACTLY one executor call.
     */
    time.Sleep(150 * time.Millisecond)
    if got := exec.callCount(); got != 1 {
        t.Errorf("executor calls = %d, want 1 (failure "+
            "must not fan out)", got)
    }
}

func TestScheduler_FanoutSkipsMissingDownstreamJobWithWarning(
    t *testing.T) {

    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* A triggers B (exists) and ghost (missing). The
     * scheduler should enqueue B and skip ghost without
     * crashing the loop.
     */
    if err := store.SaveJob(&storage.Job{
        Name: "A",
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "B"},
            {Name: "ghost"},
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob A: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:  "B",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob B: %v", err)
    }

    exec := &recordingExecutor{
        result: scheduler.BuildResult{
            Status: scheduler.StatusSuccess,
        },
    }
    s := scheduler.New(store, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    s.Start(ctx)
    defer s.Stop()

    upJob, _ := store.LoadJob("A")
    _, _, err = s.Enqueue(upJob)
    if err != nil {
        t.Fatalf("Enqueue A: %v", err)
    }

    deadline := time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if exec.callCount() >= 2 {
            break
        }
        time.Sleep(10 * time.Millisecond)
    }
    seen := exec.calls()
    if len(seen) != 2 {
        t.Fatalf("executor saw %d builds, want exactly 2 "+
            "(A + B; ghost is missing): %+v",
            len(seen), seen)
    }
    /* Loop is still healthy: enqueueing another job is
     * accepted and runs.
     */
    if err := store.SaveJob(&storage.Job{
        Name:  "later",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob later: %v", err)
    }
    laterJob, _ := store.LoadJob("later")
    _, _, err = s.Enqueue(laterJob)
    if err != nil {
        t.Fatalf("Enqueue later: %v", err)
    }
    deadline = time.Now().Add(2 * time.Second)
    for time.Now().Before(deadline) {
        if exec.callCount() >= 3 {
            break
        }
        time.Sleep(10 * time.Millisecond)
    }
    if exec.callCount() < 3 {
        t.Errorf("loop wedged after missing-downstream: "+
            "exec calls = %d, want 3+", exec.callCount())
    }
}

/* recordingExecutor stamps every Execute call with the job
 * name + the TriggeredBy ref it found in ctx, so the fan-
 * out tests can verify which downstreams ran and whose
 * parent they were assigned.
 */
type recordingExecutor struct {
    mu      sync.Mutex
    log     []executorCall
    result  scheduler.BuildResult
    counter int32
}

type executorCall struct {
    job    string
    num    int
    parent *scheduler.BuildRef
}

func (e *recordingExecutor) Execute(ctx context.Context,
    job *storage.Job, num int) scheduler.BuildResult {

    atomic.AddInt32(&e.counter, 1)
    e.mu.Lock()
    e.log = append(e.log, executorCall{
        job:    job.Name,
        num:    num,
        parent: scheduler.TriggeredByFrom(ctx),
    })
    e.mu.Unlock()
    r := e.result
    r.JobName = job.Name
    r.Number = num
    return r
}

func (e *recordingExecutor) calls() []executorCall {
    e.mu.Lock()
    defer e.mu.Unlock()
    out := make([]executorCall, len(e.log))
    copy(out, e.log)
    return out
}

func (e *recordingExecutor) callCount() int {
    return int(atomic.LoadInt32(&e.counter))
}
