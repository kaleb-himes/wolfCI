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
	"log"
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

	// TriggeredBy attributes a downstream build to the
	// (upstream job, build number) that caused its enqueue.
	// nil means "root build" - operator clicked Run or a
	// non-cascade trigger fired. Pointer (not value) so the
	// json encoder's omitempty actually fires for root
	// builds: encoding/json only treats a struct-typed field
	// as empty when it is a nil pointer. Phase 15.2.
	TriggeredBy *BuildRef `json:"triggered_by,omitempty"`
}

// BuildRef identifies a specific build of a named job. Used
// by BuildResult.TriggeredBy to link downstream builds back
// to their upstream parent. Phase 15.2.
type BuildRef struct {
	Job   string `json:"job"`
	Build int    `json:"build"`
}

// triggeredByCtxKey is the unexported context.WithValue key
// used to thread a TriggeredBy ref from scheduler.Enqueue (a
// future Phase 15.4 EnqueueChild) into the Executor. Phase
// 15.3 needs it so LocalExecutor can populate WOLFCI_INPUTS
// without an interface change on Executor.Execute.
type triggeredByCtxKey struct{}

// WithTriggeredBy attaches a TriggeredBy ref to ctx. The
// Executor reads it with TriggeredByFrom. nil ref is a
// no-op so callers do not have to check before chaining.
func WithTriggeredBy(ctx context.Context, ref *BuildRef) context.Context {
	if ref == nil {
		return ctx
	}
	return context.WithValue(ctx, triggeredByCtxKey{}, ref)
}

// TriggeredByFrom returns the TriggeredBy ref attached to
// ctx by WithTriggeredBy, or nil if none. Executors use it
// to decide whether to populate WOLFCI_INPUTS.
func TriggeredByFrom(ctx context.Context) *BuildRef {
	v, _ := ctx.Value(triggeredByCtxKey{}).(*BuildRef)
	return v
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
	job    *storage.Job
	num    int
	done   chan BuildResult
	parent *BuildRef // non-nil when this is a fan-out enqueue (Phase 15.4)
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
	return s.enqueueWithParent(job, nil)
}

// EnqueueChild is the fan-out variant used by the dispatch loop
// (Phase 15.4) when a successful build's TriggersDownstream
// fires. parent identifies the upstream build and is plumbed
// through to the executor via the run-context's TriggeredBy
// value. Exported so a future external trigger source can
// stamp its own attribution; today the only caller is the
// internal loop.
func (s *Scheduler) EnqueueChild(job *storage.Job,
	parent *BuildRef) (int, <-chan BuildResult, error) {
	return s.enqueueWithParent(job, parent)
}

func (s *Scheduler) enqueueWithParent(job *storage.Job,
	parent *BuildRef) (int, <-chan BuildResult, error) {

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

	/* Phase 14.3: snapshot the spec as it looked at enqueue
	 * time. "Rebuild Last" reads this file to re-enqueue with
	 * the exact shape this build saw, even if the live spec
	 * has drifted since. Best-effort: a snapshot failure
	 * does not abort the build.
	 */
	if snapErr := s.store.SaveSpecSnapshot(job.Name, num, job); snapErr != nil {
		_ = snapErr
	}

	q := &queuedJob{
		job:    job,
		num:    num,
		done:   make(chan BuildResult, 1),
		parent: parent,
	}

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

		runCtx := ctx
		if q.parent != nil {
			runCtx = WithTriggeredBy(ctx, q.parent)
		}
		result := s.executor.Execute(runCtx, q.job, q.num)
		if result.JobName == "" {
			result.JobName = q.job.Name
		}
		if result.Number == 0 {
			result.Number = q.num
		}
		if q.parent != nil && result.TriggeredBy == nil {
			/* Executor did not stamp the ref (e.g. a fake
			 * executor in tests); preserve it so result.json
			 * still records the parentage.
			 */
			result.TriggeredBy = q.parent
		}

		s.mu.Lock()
		s.results[buildKey{job: q.job.Name, num: q.num}] = result
		s.mu.Unlock()

		q.done <- result

		/* Phase 15.4: on a successful build with downstream
		 * triggers, walk the list and enqueue each that
		 * exists. Missing specs are skipped without failing
		 * the loop so a half-deleted graph stays operable.
		 */
		if result.Status == StatusSuccess &&
			len(q.job.TriggersDownstream) > 0 {

			parent := &BuildRef{
				Job:   q.job.Name,
				Build: q.num,
			}
			for _, ts := range q.job.TriggersDownstream {
				if ts.Name == "" {
					continue
				}
				down, err := s.store.LoadJob(ts.Name)
				if err != nil {
					log.Printf("scheduler.fanout: "+
						"skipping missing downstream "+
						"%q (triggered by %s/#%d): %v",
						ts.Name, q.job.Name, q.num, err)
					continue
				}
				if _, _, err := s.enqueueWithParent(
					down, parent); err != nil {
					log.Printf("scheduler.fanout: "+
						"enqueue downstream %q "+
						"failed: %v", ts.Name, err)
				}
			}
		}
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
