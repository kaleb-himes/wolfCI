package scheduler

import (
	"context"
	"fmt"
	"time"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/nodes"
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

	provisioner      nodes.Provisioner
	provisionTimeout time.Duration
}

// NewRouter returns a Router that uses local for unlabeled or
// locally-matched jobs and svc for everything else.
func NewRouter(svc *agentsvc.Server, local Executor, localLabels []string) *Router {
	return &Router{
		svc:              svc,
		local:            local,
		localLabels:      append([]string(nil), localLabels...),
		provisionTimeout: 30 * time.Second,
	}
}

// WithProvisioner registers an overflow Provisioner. When no
// on-prem agent advertises the Job's node_label, Execute asks p
// for a node, waits up to provisionTimeout for that node to
// register and open its Connect stream, dispatches the build,
// then Terminate's the node. Per the locked-in Phase 5 policy
// the provisioner is consulted ONLY after on-prem options are
// exhausted.
func (r *Router) WithProvisioner(p nodes.Provisioner) *Router {
	r.provisioner = p
	return r
}

// WithProvisionTimeout overrides the default 30s wait for a
// freshly-provisioned node to connect.
func (r *Router) WithProvisionTimeout(d time.Duration) *Router {
	if d > 0 {
		r.provisionTimeout = d
	}
	return r
}

// Execute satisfies the Executor interface.
func (r *Router) Execute(ctx context.Context, job *storage.Job, num int) BuildResult {
	if r.matchesLocal(job.NodeLabel) {
		return r.local.Execute(ctx, job, num)
	}

	agentID := r.svc.IdleAgentWithLabel(job.NodeLabel)
	if agentID == "" {
		// No on-prem match. Try the overflow Provisioner if one
		// is configured; the locked-in policy is on-prem first.
		if r.provisioner == nil {
			return BuildResult{
				JobName: job.Name,
				Number:  num,
				Status:  StatusError,
				Error:   fmt.Sprintf("scheduler.Router: no agent available for node_label=%q", job.NodeLabel),
			}
		}
		spawned, err := r.provisionAndWait(ctx, job.NodeLabel)
		if err != nil {
			return BuildResult{
				JobName: job.Name,
				Number:  num,
				Status:  StatusError,
				Error:   fmt.Sprintf("scheduler.Router: provision: %v", err),
			}
		}
		defer func() {
			tctx, cancel := context.WithTimeout(context.Background(), r.provisionTimeout)
			defer cancel()
			_ = r.provisioner.Terminate(tctx, spawned.nodeID)
		}()
		agentID = spawned.agentID
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

// spawnedNode is the tuple a successful provision yields: the
// nodes.Node ID (passed to Terminate later) and the agent_id
// that registered with agentsvc (passed to SubmitAndWait now).
type spawnedNode struct {
	nodeID  string
	agentID string
}

// provisionAndWait asks the Provisioner for a node advertising
// label, then polls IdleAgentWithLabel up to provisionTimeout
// until an agent shows up. v1 assumes Provisioner.Provision
// returns a Node whose ID matches the agent_id the spawned
// agent uses with Register.
func (r *Router) provisionAndWait(ctx context.Context, label string) (*spawnedNode, error) {
	node, err := r.provisioner.Provision(ctx, label)
	if err != nil {
		return nil, fmt.Errorf("Provision: %w", err)
	}
	deadline := time.Now().Add(r.provisionTimeout)
	for time.Now().Before(deadline) {
		if id := r.svc.IdleAgentWithLabel(label); id != "" {
			return &spawnedNode{nodeID: node.ID, agentID: id}, nil
		}
		select {
		case <-ctx.Done():
			_ = r.provisioner.Terminate(context.Background(), node.ID)
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = r.provisioner.Terminate(context.Background(), node.ID)
	return nil, fmt.Errorf("provisioned node %q did not register within %s", node.ID, r.provisionTimeout)
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
