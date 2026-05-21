package scheduler

import (
	"context"
	"fmt"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// Router is the Executor that picks between the local in-process
// runner and a remote on-prem agent based on Job.NodeLabel. It
// enforces the project's "on-prem first" Phase 5 policy: a
// matching idle agent is preferred before any future Provisioner
// integration (PLAN.md task 5.5c).
//
// Routing rules:
//   - Job.NodeLabel == "" or matches one of localLabels   -> local
//   - otherwise the agent returned by IdleAgentWithLabel  -> remote
//   - no match anywhere                                   -> Status=error
type Router struct {
	svc         *agentsvc.Server
	local       Executor
	localLabels []string
}

// NewRouter returns a Router that uses local for unlabeled or
// locally-matched jobs and svc for everything else.
func NewRouter(svc *agentsvc.Server, local Executor, localLabels []string) *Router {
	return &Router{svc: svc, local: local, localLabels: append([]string(nil), localLabels...)}
}

// Execute satisfies the Executor interface.
func (r *Router) Execute(ctx context.Context, job *storage.Job, num int) BuildResult {
	if r.matchesLocal(job.NodeLabel) {
		return r.local.Execute(ctx, job, num)
	}

	agentID := r.svc.IdleAgentWithLabel(job.NodeLabel)
	if agentID == "" {
		return BuildResult{
			JobName: job.Name,
			Number:  num,
			Status:  StatusError,
			Error:   fmt.Sprintf("scheduler.Router: no agent available for node_label=%q", job.NodeLabel),
		}
	}

	bc, err := r.svc.SubmitAndWait(ctx, agentID, jobToProto(job, num))
	if err != nil {
		return BuildResult{
			JobName: job.Name,
			Number:  num,
			Status:  StatusError,
			Error:   fmt.Sprintf("scheduler.Router: SubmitAndWait(%s): %v", agentID, err),
		}
	}
	return protoToBuildResult(bc, job.Name)
}

func (r *Router) matchesLocal(label string) bool {
	if label == "" {
		return true
	}
	for _, l := range r.localLabels {
		if l == label {
			return true
		}
	}
	return false
}

func jobToProto(job *storage.Job, num int) *wolfciv1.JobAssignment {
	pj := &wolfciv1.JobAssignment{
		JobName:     job.Name,
		BuildNumber: int32(num),
	}
	for _, s := range job.Steps {
		pj.Steps = append(pj.Steps, &wolfciv1.Step{
			Name:  s.Name,
			Shell: s.Shell,
			Env:   s.Env,
		})
	}
	return pj
}

func protoToBuildResult(bc *wolfciv1.BuildComplete, jobName string) BuildResult {
	return BuildResult{
		JobName:  jobName,
		Number:   int(bc.BuildNumber),
		Status:   Status(bc.Status),
		ExitCode: int(bc.ExitCode),
		Error:    bc.Error,
	}
}
