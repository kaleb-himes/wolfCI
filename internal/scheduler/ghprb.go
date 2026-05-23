package scheduler

/* internal/scheduler/ghprb.go - PLAN.md 18.8 GHPRB enqueue path.
 *
 * EnqueueGHPRB takes a Job whose GitHubPRB config is populated
 * and a TriggerEvent from the poller (or an "ok to test"
 * comment receiver, when that lands), assembles the ghprb*
 * env map via ghprb.EnvForEvent, and enqueues a fresh build
 * whose Spec.Env carries those vars.
 *
 * The Job is cloned before Env is populated so the shared
 * in-memory spec the caller holds is not mutated.
 */

import (
    "errors"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

/* EnqueueGHPRB enqueues a build triggered by a GitHub PR
 * event. The returned BuildResult channel fires when the
 * dispatcher finishes the build (same as Enqueue).
 *
 * Required: job.GitHubPRB must be non-nil with at least
 * APICredentialsID set; otherwise this returns an error
 * without enqueuing anything.
 */
func (s *Scheduler) EnqueueGHPRB(job *storage.Job,
    event ghprb.TriggerEvent) (int, <-chan BuildResult, error) {

    if job == nil {
        return 0, nil, errors.New(
            "scheduler.EnqueueGHPRB: nil Job")
    }
    if job.GitHubPRB == nil {
        return 0, nil, errors.New(
            "scheduler.EnqueueGHPRB: " +
                "Job.GitHubPRB is nil; no PRB config")
    }
    env := ghprb.EnvForEvent(event,
        job.GitHubPRB.APICredentialsID)

    /* Clone the job and overlay the env map onto its own Env.
     * A nil Env on the original is fine; we always replace it
     * with the freshly-computed ghprb env so a previous build's
     * vars cannot leak into this one.
     */
    clone := *job
    clone.Env = env
    return s.Enqueue(&clone)
}
