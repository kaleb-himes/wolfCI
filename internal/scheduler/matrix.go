package scheduler

import (
	"errors"

	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// MatrixRun is one element of a matrix fanout: the assigned
// build number, the channel that will receive the BuildResult,
// and the resolved axis values for this combination.
type MatrixRun struct {
	Number int
	Done   <-chan BuildResult
	Axis   map[string]string
}

// EnqueueMatrix expands job.Axis into the cartesian product of
// all dimensions and enqueues one build per combination. If
// job.Axis is empty it is equivalent to a single Enqueue.
//
// For each combination, axis values are layered on top of every
// Step.Env (axis keys override matching step keys). The cloned
// Job's Axis is cleared so the dispatched builds do not recurse.
//
// Build numbers come from the same per-job counter used by
// Enqueue, so a 4-way matrix on a fresh job claims numbers 1, 2,
// 3, 4.
func (s *Scheduler) EnqueueMatrix(job *storage.Job) ([]MatrixRun, error) {
	if job == nil {
		return nil, errors.New("scheduler.EnqueueMatrix: nil Job")
	}
	if job.Name == "" {
		return nil, errors.New("scheduler.EnqueueMatrix: Job.Name is required")
	}

	combos := cartesianProduct(job.Axis)
	runs := make([]MatrixRun, 0, len(combos))
	for _, combo := range combos {
		clone := cloneJobWithAxis(job, combo)
		num, done, err := s.Enqueue(clone)
		if err != nil {
			return runs, err
		}
		runs = append(runs, MatrixRun{Number: num, Done: done, Axis: combo})
	}
	return runs, nil
}

// cartesianProduct returns one map per combination across all
// axes. Each map has exactly one key per axis dimension. An
// empty axes slice yields a single empty map (so the no-axis
// path produces one run).
func cartesianProduct(axes []storage.AxisDimension) []map[string]string {
	if len(axes) == 0 {
		return []map[string]string{{}}
	}
	rest := cartesianProduct(axes[1:])
	out := make([]map[string]string, 0, len(axes[0].Values)*len(rest))
	for _, v := range axes[0].Values {
		for _, r := range rest {
			combined := make(map[string]string, len(r)+1)
			for k, val := range r {
				combined[k] = val
			}
			combined[axes[0].Name] = v
			out = append(out, combined)
		}
	}
	return out
}

// cloneJobWithAxis returns a shallow copy of job with Steps
// rebuilt so each Step.Env carries the axis combination, and
// Axis cleared so the dispatched run is treated as a leaf.
func cloneJobWithAxis(job *storage.Job, axis map[string]string) *storage.Job {
	clone := *job
	clone.Steps = make([]storage.Step, len(job.Steps))
	for i, step := range job.Steps {
		clone.Steps[i] = step
		merged := make(map[string]string, len(step.Env)+len(axis))
		for k, v := range step.Env {
			merged[k] = v
		}
		for k, v := range axis {
			merged[k] = v // axis wins
		}
		clone.Steps[i].Env = merged
	}
	clone.Axis = nil
	return &clone
}
