// Package scheduler enqueues Jobs and dispatches them to an
// Executor in FIFO order.
//
// FIFO is a locked-in v1 decision; a priority queue is in the
// backlog. Build numbers are persisted per-job at
// builds/<job>/.next-build-number and incremented under an
// exclusive flock, so the counter survives process restarts and
// concurrent enqueues.
//
// Executor is an interface. Phase 4 ships only the in-process
// runner (internal/scheduler/local.go in a follow-on commit);
// Phase 5 will add agent-driven executors that route by node
// label. The scheduler does not change for that swap.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// Status is the outcome of a build run.
type Status string

const (
	StatusSuccess   Status = "success"
	StatusFailure   Status = "failure"   // job ran, exit code != 0
	StatusCancelled Status = "cancelled" // ctx cancelled before completion
	StatusError     Status = "error"     // executor-level failure (could not run)
)

// BuildResult captures the outcome of one job run.
type BuildResult struct {
	JobName  string
	Number   int
	Status   Status
	ExitCode int
	Error    string // executor-level error, empty on success/failure
}

// Executor runs a Job and returns its result. Implementations
// must be safe for concurrent calls (the scheduler dispatches one
// at a time today but the interface contract does not promise
// serialization).
type Executor interface {
	Execute(ctx context.Context, job *storage.Job, buildNum int) BuildResult
}

// Scheduler enqueues Jobs in FIFO order and dispatches them to an
// Executor. Construct with New and start with Start; call Stop
// for a clean shutdown.
type Scheduler struct {
	executor Executor
	store    *storage.Storage

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []*queuedJob
	results map[buildKey]BuildResult
	stopped bool

	bgCtx    context.Context
	bgCancel context.CancelFunc
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type queuedJob struct {
	job  *storage.Job
	num  int
	done chan BuildResult
}

type buildKey struct {
	job string
	num int
}

// New constructs a Scheduler. It does not start the dispatch
// loop; call Start.
func New(store *storage.Storage, exec Executor) *Scheduler {
	s := &Scheduler{
		executor: exec,
		store:    store,
		results:  make(map[buildKey]BuildResult),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Start begins the dispatch loop. The loop exits when ctx is
// cancelled or Stop is called.
func (s *Scheduler) Start(ctx context.Context) {
	s.bgCtx, s.bgCancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.loop(s.bgCtx)
	// Watch ctx; if it fires, call Stop. Not part of the WaitGroup
	// so Stop does not deadlock waiting for this goroutine.
	go func() {
		<-s.bgCtx.Done()
		s.Stop()
	}()
}

// Drain signals the dispatch loop to exit and waits up to d for
// the in-flight build (and any state cleanup) to finish.
// Returns nil if the scheduler stopped within the budget, or
// context.DeadlineExceeded if the timeout fired first. Callers
// that see DeadlineExceeded should treat the dispatch goroutine
// as leaked and proceed to force-close the rest of the server;
// production executors (LocalExecutor + exec.CommandContext)
// respect ctx cancellation via SIGKILL and never hit this path.
// Drain is idempotent with Stop().
func (s *Scheduler) Drain(d time.Duration) error {
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(d):
		return context.DeadlineExceeded
	}
}

// Stop signals the dispatch loop to exit and blocks until it
// does. Idempotent.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cond.Broadcast()
		s.mu.Unlock()
		if s.bgCancel != nil {
			s.bgCancel()
		}
	})
	s.wg.Wait()
}

// Enqueue assigns the next build number for job.Name and adds
// the job to the FIFO queue. The returned channel receives the
// BuildResult once the dispatcher runs it.
func (s *Scheduler) Enqueue(job *storage.Job) (int, <-chan BuildResult, error) {
	if job == nil {
		return 0, nil, errors.New("scheduler.Enqueue: nil Job")
	}
	if job.Name == "" {
		return 0, nil, errors.New("scheduler.Enqueue: Job.Name is required")
	}

	num, err := s.nextBuildNumber(job.Name)
	if err != nil {
		return 0, nil, err
	}

	q := &queuedJob{job: job, num: num, done: make(chan BuildResult, 1)}

	s.mu.Lock()
	s.queue = append(s.queue, q)
	s.cond.Signal()
	s.mu.Unlock()

	return num, q.done, nil
}

// Result returns the recorded BuildResult for the given build
// number, or false if the build is unknown or still queued.
func (s *Scheduler) Result(jobName string, num int) (BuildResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[buildKey{job: jobName, num: num}]
	return r, ok
}

func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.stopped {
			s.cond.Wait()
		}
		if s.stopped {
			s.mu.Unlock()
			return
		}
		q := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()

		result := s.executor.Execute(ctx, q.job, q.num)
		if result.JobName == "" {
			result.JobName = q.job.Name
		}
		if result.Number == 0 {
			result.Number = q.num
		}

		s.mu.Lock()
		s.results[buildKey{job: q.job.Name, num: q.num}] = result
		s.mu.Unlock()

		q.done <- result
	}
}

// nextBuildNumber reads, increments, and writes back the
// per-job build counter under an exclusive flock.
func (s *Scheduler) nextBuildNumber(jobName string) (int, error) {
	dir := filepath.Join(s.store.Root(), "builds", jobName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: mkdir: %w", err)
	}
	path := filepath.Join(dir, ".next-build-number")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: open: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	data, err := io.ReadAll(f)
	if err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: read: %w", err)
	}

	current := 0
	if s := strings.TrimSpace(string(data)); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("scheduler.nextBuildNumber: parse %q: %w", s, err)
		}
		current = n
	}
	next := current + 1

	if err := f.Truncate(0); err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: truncate: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: seek: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", next); err != nil {
		return 0, fmt.Errorf("scheduler.nextBuildNumber: write: %w", err)
	}

	return next, nil
}
