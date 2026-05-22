package scheduler_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestScheduler_RoundtripJob is the gating test for PLAN.md
// tasks 4.1 (failing test) and 4.2 (implementation). It enqueues
// a Job, waits for the dispatched fake executor to return a
// result, and verifies that the build number starts at 1 and
// increments on the next enqueue (per-job monotonic counter).
func TestScheduler_RoundtripJob(t *testing.T) {
	store := mustStore(t)
	exec := &fakeExecutor{result: scheduler.BuildResult{Status: scheduler.StatusSuccess}}
	s := scheduler.New(store, exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	job := &storage.Job{
		Name:  "demo",
		Steps: []storage.Step{{Shell: "true"}},
	}

	num, done, err := s.Enqueue(job)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if num != 1 {
		t.Errorf("first build number = %d, want 1", num)
	}

	select {
	case got := <-done:
		if got.Status != scheduler.StatusSuccess {
			t.Errorf("Status = %q, want %q", got.Status, scheduler.StatusSuccess)
		}
		if got.JobName != "demo" {
			t.Errorf("JobName = %q, want demo", got.JobName)
		}
		if got.Number != 1 {
			t.Errorf("Number in result = %d, want 1", got.Number)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for build result")
	}

	num2, done2, err := s.Enqueue(job)
	if err != nil {
		t.Fatalf("Enqueue#2: %v", err)
	}
	if num2 != 2 {
		t.Errorf("second build number = %d, want 2", num2)
	}
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for second build result")
	}
}

// TestScheduler_FIFOOrder verifies the project's locked-in queue
// order: jobs come out in the order they went in.
func TestScheduler_FIFOOrder(t *testing.T) {
	store := mustStore(t)

	release := make(chan struct{})
	var mu sync.Mutex
	var order []int

	exec := &fakeExecutor{
		execFn: func(_ context.Context, job *storage.Job, num int) scheduler.BuildResult {
			<-release // hold here so all three are queued before any runs
			mu.Lock()
			order = append(order, num)
			mu.Unlock()
			return scheduler.BuildResult{Status: scheduler.StatusSuccess}
		},
	}
	s := scheduler.New(store, exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	job := &storage.Job{Name: "demo", Steps: []storage.Step{{Shell: "true"}}}

	var dones []<-chan scheduler.BuildResult
	for i := 0; i < 3; i++ {
		_, done, err := s.Enqueue(job)
		if err != nil {
			t.Fatalf("Enqueue #%d: %v", i+1, err)
		}
		dones = append(dones, done)
	}

	// Let them flow through, one at a time, in FIFO order.
	close(release)

	for i, d := range dones {
		select {
		case <-d:
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for build %d", i+1)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Errorf("execution order = %v, want [1 2 3]", order)
	}
}

// TestScheduler_BuildNumberPersistsAcrossSchedulers proves that
// the per-job counter at builds/<job>/.next-build-number survives
// a process restart, so build numbers do not collide if the
// server stops and starts.
func TestScheduler_BuildNumberPersistsAcrossSchedulers(t *testing.T) {
	store := mustStore(t)
	exec := &fakeExecutor{result: scheduler.BuildResult{Status: scheduler.StatusSuccess}}

	job := &storage.Job{Name: "demo", Steps: []storage.Step{{Shell: "true"}}}

	first := scheduler.New(store, exec)
	ctx1, cancel1 := context.WithCancel(context.Background())
	first.Start(ctx1)
	num, done, _ := first.Enqueue(job)
	<-done
	cancel1()
	first.Stop()
	if num != 1 {
		t.Errorf("first scheduler: build = %d, want 1", num)
	}

	second := scheduler.New(store, exec)
	ctx2, cancel2 := context.WithCancel(context.Background())
	second.Start(ctx2)
	num2, done2, _ := second.Enqueue(job)
	<-done2
	cancel2()
	second.Stop()
	if num2 != 2 {
		t.Errorf("second scheduler: build = %d, want 2 (counter must persist)", num2)
	}
}

// TestScheduler_DrainCompletesCleanly gates PLAN.md task 11.6.
// Scheduler.Drain stops the dispatch loop and waits up to d for
// the in-flight build to finish. When the executor respects ctx
// cancellation, Drain returns nil well within the budget.
func TestScheduler_DrainCompletesCleanly(t *testing.T) {
	store := mustStore(t)

	releaseExec := make(chan struct{})
	exec := &fakeExecutor{
		execFn: func(ctx context.Context, _ *storage.Job, _ int) scheduler.BuildResult {
			// Hold until ctx cancels, then return Cancelled. This
			// mirrors how LocalExecutor exits when exec.CommandContext
			// kills its sh subprocess on shutdown.
			close(releaseExec)
			<-ctx.Done()
			return scheduler.BuildResult{Status: scheduler.StatusCancelled}
		},
	}
	s := scheduler.New(store, exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	job := &storage.Job{Name: "drain-clean", Steps: []storage.Step{{Shell: "true"}}}
	if _, _, err := s.Enqueue(job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait until the executor has picked up the build so Drain
	// actually drains something, not just an idle scheduler.
	select {
	case <-releaseExec:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not start within 2s")
	}

	start := time.Now()
	if err := s.Drain(2 * time.Second); err != nil {
		t.Errorf("Drain returned %v, want nil (clean drain)", err)
	}
	elapsed := time.Since(start)
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Drain took %v; should return well under the 2s budget", elapsed)
	}
}

// TestScheduler_DrainTimesOut gates the timeout-anyway path: if
// the in-flight executor refuses to respond to ctx, Drain returns
// context.DeadlineExceeded after the budget. The caller is then
// free to abandon the leaked goroutine and force-close the rest
// of the server.
func TestScheduler_DrainTimesOut(t *testing.T) {
	store := mustStore(t)

	releaseExec := make(chan struct{})
	hangForever := make(chan struct{})
	exec := &fakeExecutor{
		execFn: func(_ context.Context, _ *storage.Job, _ int) scheduler.BuildResult {
			close(releaseExec)
			<-hangForever
			return scheduler.BuildResult{Status: scheduler.StatusSuccess}
		},
	}
	s := scheduler.New(store, exec)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Let the test goroutine leak out gracefully at the end.
	defer close(hangForever)
	s.Start(ctx)

	job := &storage.Job{Name: "drain-hang", Steps: []storage.Step{{Shell: "true"}}}
	if _, _, err := s.Enqueue(job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-releaseExec:
	case <-time.After(2 * time.Second):
		t.Fatal("executor did not start within 2s")
	}

	start := time.Now()
	err := s.Drain(200 * time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Error("Drain returned nil; want context.DeadlineExceeded against a hanging executor")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Drain err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("Drain returned in %v before the 200ms budget", elapsed)
	}
	if elapsed > 800*time.Millisecond {
		t.Errorf("Drain took %v; should return shortly after the 200ms budget", elapsed)
	}
}

func mustStore(tb testing.TB) *storage.Storage {
	tb.Helper()
	s, err := storage.New(tb.TempDir())
	if err != nil {
		tb.Fatalf("storage.New: %v", err)
	}
	return s
}

type fakeExecutor struct {
	result scheduler.BuildResult
	execFn func(ctx context.Context, job *storage.Job, num int) scheduler.BuildResult
}

func (f *fakeExecutor) Execute(ctx context.Context, job *storage.Job, num int) scheduler.BuildResult {
	if f.execFn != nil {
		return f.execFn(ctx, job, num)
	}
	r := f.result
	r.JobName = job.Name
	r.Number = num
	return r
}
