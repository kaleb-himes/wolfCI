package server

/* Phase 15.6 helpers: per-job upstream/downstream/sibling
 * relationships rendered on the detail page, and the
 * inbound/outbound counts rendered as badges on the /jobs
 * index.
 *
 * "Upstream" of a job J:
 *   - Explicit J.Upstream entries.
 *   - PLUS every other job whose TriggersDownstream names J
 *     (the live trigger graph - the source of truth for
 *     fan-out).
 * "Downstream" of J:
 *   - Every other job whose TriggersDownstream names J's own
 *     OUTGOING trigger entries pointed at it. Equivalent to:
 *     the names in J.TriggersDownstream, intersected with
 *     the set of jobs that actually exist on disk.
 *
 *     The "intersected with on-disk" step skips a ghost
 *     downstream so the section stays accurate while the
 *     operator is still typing the spec.
 * "Siblings":
 *   - Other jobs whose upstream set shares at least one
 *     entry with J's upstream set. Computed AFTER upstream;
 *     a job with no upstream has no siblings.
 */

import (
    "sort"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

type triggerLinks struct {
    Upstream   []string
    Downstream []string
    Siblings   []string
}

/* computeTriggerLinks walks the (jobName, all) pair and
 * returns the three lists ready for template iteration.
 * Each list is sorted alphabetically and de-duplicated.
 * Self-references are dropped so a job that names itself
 * does not appear in its own sidebar.
 */
func computeTriggerLinks(jobName string,
    all []*storage.Job) triggerLinks {

    onDisk := make(map[string]bool, len(all))
    var thisJob *storage.Job
    for _, j := range all {
        onDisk[j.Name] = true
        if j.Name == jobName {
            thisJob = j
        }
    }

    upstreamSet := make(map[string]bool)
    if thisJob != nil {
        for _, u := range thisJob.Upstream {
            if u != "" && u != jobName {
                upstreamSet[u] = true
            }
        }
    }
    for _, j := range all {
        if j.Name == jobName {
            continue
        }
        for _, ts := range j.TriggersDownstream {
            if ts.Name == jobName {
                upstreamSet[j.Name] = true
            }
        }
    }

    downstreamSet := make(map[string]bool)
    if thisJob != nil {
        for _, ts := range thisJob.TriggersDownstream {
            if ts.Name == "" || ts.Name == jobName {
                continue
            }
            if onDisk[ts.Name] {
                downstreamSet[ts.Name] = true
            }
        }
    }

    siblingSet := make(map[string]bool)
    for _, j := range all {
        if j.Name == jobName {
            continue
        }
        /* Walk this candidate's upstream set; if any entry
         * is also in our upstream set, it is a sibling.
         */
        candUp := jobUpstreamSet(j, all)
        for u := range candUp {
            if upstreamSet[u] {
                siblingSet[j.Name] = true
                break
            }
        }
    }

    return triggerLinks{
        Upstream:   sortedKeys(upstreamSet),
        Downstream: sortedKeys(downstreamSet),
        Siblings:   sortedKeys(siblingSet),
    }
}

/* jobUpstreamSet returns the upstream set of the given job
 * computed the same way as the main pass: explicit
 * job.Upstream entries plus implicit (other jobs that
 * trigger this one).
 */
func jobUpstreamSet(j *storage.Job,
    all []*storage.Job) map[string]bool {

    out := make(map[string]bool)
    for _, u := range j.Upstream {
        if u != "" {
            out[u] = true
        }
    }
    for _, other := range all {
        if other.Name == j.Name {
            continue
        }
        for _, ts := range other.TriggersDownstream {
            if ts.Name == j.Name {
                out[other.Name] = true
            }
        }
    }
    return out
}

func sortedKeys(set map[string]bool) []string {
    out := make([]string, 0, len(set))
    for k := range set {
        out = append(out, k)
    }
    sort.Strings(out)
    return out
}

/* triggerCounts pairs a job name with its inbound (other
 * jobs that trigger it) and outbound (this job's
 * TriggersDownstream length) counts. The /jobs index
 * renders these as a small badge next to each job name.
 */
type triggerCounts struct {
    Inbound  int
    Outbound int
}

/* computeAllTriggerCounts builds a map from job name to
 * triggerCounts in one pass over the job list.
 */
func computeAllTriggerCounts(
    all []*storage.Job) map[string]triggerCounts {

    counts := make(map[string]triggerCounts, len(all))
    for _, j := range all {
        c := counts[j.Name]
        c.Outbound = len(j.TriggersDownstream)
        counts[j.Name] = c
    }
    for _, j := range all {
        for _, ts := range j.TriggersDownstream {
            if ts.Name == "" || ts.Name == j.Name {
                continue
            }
            c := counts[ts.Name]
            c.Inbound++
            counts[ts.Name] = c
        }
    }
    return counts
}
