package pipeline_test

/* internal/pipeline/steps_build_test.go - PLAN.md 18.22 gating
 * tests.
 *
 * TestStep_BuildTriggersDownstream wires a real
 * scheduler.Scheduler in front of a fake Executor, defines a
 * "child-1" job in a temp storage, and runs a parent pipeline
 * whose script block invokes `build job: 'child-1', parameters:
 * [string(name: 'P', value: 'v')], propagate: false`. The
 * assertions:
 *
 *   1. The child job appears in the storage AND a build is
 *      actually enqueued through the scheduler (the fake
 *      executor's seenChild flag captures this).
 *   2. The parent build's status is SUCCESS (the parent
 *      finishes cleanly because propagate:false short-circuits
 *      the throw path even if the child fails).
 *   3. The result object the build step returned exposes
 *      .getResult() == 'SUCCESS' - verified by writing the
 *      value through `sh` to a workspace file.
 *
 * TestStep_BuildPropagatesFailure exercises the propagate:true
 * default: a child that finishes FAILURE bubbles up and marks
 * the parent build FAILURE.
 *
 * The test-local schedulerDispatcher adapter wraps
 * scheduler.Scheduler + storage.Storage to satisfy
 * pipeline.BuildDispatcher. The same shape can move to a
 * shared package or to cmd/wolfci once a second caller needs
 * it; today the only callers are these two tests, so inlining
 * keeps the adapter close to the assertions it serves.
 */

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestStep_BuildTriggersDownstream(t *testing.T) {
    workspace := t.TempDir()

    /* Set up a real scheduler in front of a fake executor that
     * records whether the child job was actually dispatched. */
    store, err := storage.New(t.TempDir())
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    child := &storage.Job{
        Name:  "child-1",
        Steps: []storage.Step{{Shell: "true"}},
    }
    if err := store.SaveJob(child); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    var seenMu sync.Mutex
    var seen int
    exec := &fakeBuildExec{
        onExec: func(ctx context.Context, job *storage.Job,
            num int) scheduler.BuildResult {
            if job.Name == "child-1" {
                seenMu.Lock()
                seen++
                seenMu.Unlock()
            }
            return scheduler.BuildResult{
                JobName: job.Name,
                Number:  num,
                Status:  scheduler.StatusSuccess,
            }
        },
    }
    sched := scheduler.New(store, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sched.Start(ctx)
    defer sched.Stop()

    dispatcher := &schedulerDispatcher{
        store: store, sched: sched}

    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('Parent') {
            steps {
                script {
                    def outcome = build(
                        job: 'child-1',
                        parameters: [
                            string(name: 'P', value: 'v')
                        ],
                        propagate: false)
                    def res = outcome.getResult()
                    def num = outcome.getNumber()
                    def proj = outcome.getProjectName()
                    sh "echo " + res + " > status.txt"
                    sh "echo " + num + " > number.txt"
                    sh "echo " + proj + " > project.txt"
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    pe := &pipeline.LocalExecutor{
        Workspace:  workspace,
        Dispatcher: dispatcher,
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, pe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        var allOut strings.Builder
        for _, st := range build.Stages[0].Steps {
            allOut.WriteString(st.Output)
            allOut.WriteString("\n")
        }
        t.Fatalf("parent build.Status = %v, want "+
            "BuildSuccess; step output:\n%s",
            build.Status, allOut.String())
    }
    seenMu.Lock()
    got := seen
    seenMu.Unlock()
    if got != 1 {
        t.Errorf("child executor calls = %d, want 1", got)
    }
    expect := []struct {
        file string
        want string
    }{
        {"status.txt", "SUCCESS"},
        {"number.txt", "1"},
        {"project.txt", "child-1"},
    }
    for _, c := range expect {
        data, err := os.ReadFile(
            filepath.Join(workspace, c.file))
        if err != nil {
            t.Errorf("read %s: %v", c.file, err)
            continue
        }
        got := strings.TrimSpace(string(data))
        if got != c.want {
            t.Errorf("%s = %q, want %q",
                c.file, got, c.want)
        }
    }
}

func TestStep_BuildPropagatesFailure(t *testing.T) {
    workspace := t.TempDir()
    store, err := storage.New(t.TempDir())
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    child := &storage.Job{
        Name:  "child-fails",
        Steps: []storage.Step{{Shell: "false"}},
    }
    if err := store.SaveJob(child); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    exec := &fakeBuildExec{
        onExec: func(ctx context.Context, job *storage.Job,
            num int) scheduler.BuildResult {
            return scheduler.BuildResult{
                JobName:  job.Name,
                Number:   num,
                Status:   scheduler.StatusFailure,
                ExitCode: 1,
            }
        },
    }
    sched := scheduler.New(store, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sched.Start(ctx)
    defer sched.Stop()
    dispatcher := &schedulerDispatcher{
        store: store, sched: sched}

    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('Parent') {
            steps {
                script {
                    build job: 'child-fails'
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    pe := &pipeline.LocalExecutor{
        Workspace:  workspace,
        Dispatcher: dispatcher,
    }
    build, _ := pipeline.ExecDeclarative(
        context.Background(), file, pe)
    if build == nil ||
        build.Status != pipeline.BuildFailure {
        t.Errorf("expected BuildFailure when child fails "+
            "with propagate=true; got %+v", build)
    }
}

/* schedulerDispatcher is the test-local adapter wrapping a
 * scheduler.Scheduler + storage.Storage to satisfy the
 * pipeline.BuildDispatcher interface. It looks the job up,
 * enqueues it, and translates the scheduler.BuildResult
 * channel into pipeline.ChildBuildResult. */
type schedulerDispatcher struct {
    store *storage.Storage
    sched *scheduler.Scheduler
}

func (sd *schedulerDispatcher) EnqueueChildBuild(
    ctx context.Context, jobName string,
    _ []pipeline.BuildParam) (
    <-chan pipeline.ChildBuildResult, error) {

    job, err := sd.store.LoadJob(jobName)
    if err != nil {
        return nil, fmt.Errorf(
            "lookup job %q: %w", jobName, err)
    }
    _, done, err := sd.sched.Enqueue(job)
    if err != nil {
        return nil, fmt.Errorf(
            "enqueue %q: %w", jobName, err)
    }
    out := make(chan pipeline.ChildBuildResult, 1)
    go func() {
        defer close(out)
        select {
        case r := <-done:
            out <- pipeline.ChildBuildResult{
                Job:    r.JobName,
                Number: r.Number,
                Status: jenkinsStatus(r.Status),
            }
        case <-ctx.Done():
            out <- pipeline.ChildBuildResult{
                Job:    jobName,
                Status: "ABORTED",
            }
        case <-time.After(15 * time.Second):
            out <- pipeline.ChildBuildResult{
                Job:    jobName,
                Status: "ABORTED",
            }
        }
    }()
    return out, nil
}

/* jenkinsStatus translates a scheduler.Status (lowercase
 * "success" / "failure" / "cancelled" / "error") into the
 * UPPERCASE Jenkins label callers expect via .getResult(). */
func jenkinsStatus(s scheduler.Status) string {
    switch s {
    case scheduler.StatusSuccess:
        return "SUCCESS"
    case scheduler.StatusFailure:
        return "FAILURE"
    case scheduler.StatusCancelled:
        return "ABORTED"
    case scheduler.StatusError:
        return "FAILURE"
    }
    return strings.ToUpper(string(s))
}

/* fakeBuildExec is a one-call scheduler.Executor whose onExec
 * is run synchronously when the scheduler dispatches a job. It
 * is the same shape as the existing fakeExecutor in
 * scheduler_test.go but kept local to this test so the
 * pipeline test file does not depend on internal scheduler
 * test helpers. */
type fakeBuildExec struct {
    onExec func(ctx context.Context, job *storage.Job,
        num int) scheduler.BuildResult
}

func (f *fakeBuildExec) Execute(ctx context.Context,
    job *storage.Job, num int) scheduler.BuildResult {
    if f.onExec != nil {
        return f.onExec(ctx, job, num)
    }
    return scheduler.BuildResult{
        JobName: job.Name,
        Number:  num,
        Status:  scheduler.StatusSuccess,
    }
}
