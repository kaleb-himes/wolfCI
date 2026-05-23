package scheduler_test

/* internal/scheduler/ghprb_test.go - PLAN.md 18.8 gating test.
 *
 * TestScheduler_GHPRBEnqueuesBuild stands up a real scheduler.
 * Scheduler with an in-memory storage and a no-op executor.
 * Feeds it a Job whose GitHubPRB config is populated, plus a
 * fake-poller TriggerEvent, via the new
 * scheduler.EnqueueGHPRB entry point. Asserts:
 *
 *   1. Exactly one Build is enqueued.
 *   2. The build's Spec.Env (read back via
 *      storage.LoadSpecSnapshot) carries every ghprb* var
 *      named in PLAN.md 18.9.
 */

import (
    "context"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestScheduler_GHPRBEnqueuesBuild(t *testing.T) {
    store, err := storage.New(t.TempDir())
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    job := &storage.Job{
        Name: "master-prb",
        GitHubPRB: &storage.GitHubPRBTrigger{
            APICredentialsID: "gh-token-id",
            GHProjectURL:     "https://github.com/wolfSSL/wolfssl/",
        },
        Steps: []storage.Step{
            {Shell: "echo $ghprbPullId"},
        },
    }
    if err := store.SaveJob(job); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    exec := &nopExecutor{}
    s := scheduler.New(store, exec)

    event := ghprb.TriggerEvent{
        PRID:               4242,
        HeadSHA:            "deadbeefcafe1234",
        Author:             "octocat",
        SourceBranch:       "feature/x",
        TargetBranch:       "master",
        GHRepository:       "wolfSSL/wolfssl",
        TriggerAuthor:      "octocat",
        TriggerAuthorLogin: "octocat",
    }
    num, _, err := s.EnqueueGHPRB(job, event)
    if err != nil {
        t.Fatalf("EnqueueGHPRB: %v", err)
    }
    if num != 1 {
        t.Errorf("first build number = %d, want 1", num)
    }

    /* Read back the spec snapshot the scheduler wrote at
     * enqueue time; its Env must carry the ghprb* vars.
     */
    spec, err := store.LoadSpecSnapshot(job.Name, num)
    if err != nil {
        t.Fatalf("LoadSpecSnapshot: %v", err)
    }
    if spec.Env == nil {
        t.Fatalf("spec.Env nil; expected ghprb* vars populated")
    }
    wantKeys := []string{
        "ghprbActualCommit",
        "ghprbCredentialsId",
        "ghprbGhRepository",
        "ghprbPullAuthorLogin",
        "ghprbPullAuthorLoginMention",
        "ghprbPullId",
        "ghprbSourceBranch",
        "ghprbTargetBranch",
        "ghprbTriggerAuthorLogin",
        "sha1",
        "random_string",
    }
    for _, k := range wantKeys {
        if _, ok := spec.Env[k]; !ok {
            t.Errorf("spec.Env missing key %q", k)
        }
    }
    if spec.Env["ghprbPullId"] != "4242" {
        t.Errorf("ghprbPullId = %q, want 4242",
            spec.Env["ghprbPullId"])
    }
    if spec.Env["ghprbCredentialsId"] != "gh-token-id" {
        t.Errorf("ghprbCredentialsId = %q, want gh-token-id",
            spec.Env["ghprbCredentialsId"])
    }
}

/* nopExecutor satisfies scheduler.Executor with a no-op so the
 * test focuses on the enqueue path; we never let the dispatcher
 * actually run the queued build.
 */
type nopExecutor struct{}

func (n *nopExecutor) Execute(_ context.Context,
    job *storage.Job,
    buildNum int) scheduler.BuildResult {
    return scheduler.BuildResult{
        Status: scheduler.StatusSuccess,
    }
}
