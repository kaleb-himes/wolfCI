package scheduler_test

import (
	"context"
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
