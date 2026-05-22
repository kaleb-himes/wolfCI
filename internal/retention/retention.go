/* Package retention enforces per-job build-history retention.
 *
 * A job spec's optional Retention block sets two thresholds:
 *
 *   MaxBuilds: keep at most N most-recent COMPLETED builds.
 *   MaxAge:    keep anything newer than this age (time.
 *              ParseDuration string, e.g. "720h" = 30 days).
 *
 * Either or both may be set. With both set, EITHER condition
 * protects a build (the more lenient of the two), so an
 * operator's "at least 30 builds and at least 30 days" intent
 * is honored as one would expect rather than minified to the
 * stricter rule.
 *
 * Nil Retention means "keep forever" - the sweeper skips the
 * job entirely. A Retention block with both fields zero is
 * equivalent to nil.
 *
 * In-flight (no result.json) builds are NEVER swept; the
 * executor may still be writing to them. Phase 14's workspace
 * browser will respect the same rule.
 */
package retention

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

/* buildEntry is the per-build view the sweeper sorts and
 * filters against. completed=false means result.json is
 * absent (still running or executor crashed before write);
 * the sweeper treats those as protected.
 */
type buildEntry struct {
    num       int
    mtime     time.Time
    completed bool
}

/* SweepJob enforces the named job's Retention policy by
 * removing the build directories that fall outside the rules
 * and returns the build numbers it removed. Nil Retention
 * (or both fields zero) is a no-op.
 *
 * "now" is injected so tests don't have to deal with
 * wall-clock drift; production callers pass time.Now().
 *
 * Errors during a single build's removal short-circuit the
 * call - the sweeper goroutine in cmd/wolfci logs the error
 * and tries again on the next tick rather than silently
 * leaving the on-disk state inconsistent with what the
 * function returned.
 */
func SweepJob(store *storage.Storage, jobName string,
    now time.Time) ([]int, error) {

    job, err := store.LoadJob(jobName)
    if err != nil {
        return nil, fmt.Errorf("retention.SweepJob: load %q: %w",
            jobName, err)
    }
    if job.Retention == nil ||
        (job.Retention.MaxBuilds == 0 && job.Retention.MaxAge == "") {
        return nil, nil
    }

    var maxAge time.Duration
    if job.Retention.MaxAge != "" {
        d, err := time.ParseDuration(job.Retention.MaxAge)
        if err != nil {
            return nil, fmt.Errorf("retention.SweepJob: "+
                "parse max_age %q: %w",
                job.Retention.MaxAge, err)
        }
        maxAge = d
    }

    buildsDir := filepath.Join(store.Root(), "builds", jobName)
    entries, err := listBuilds(buildsDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }

    /* Newest-first by mtime so the "top N" rule is a slice
     * head and the age rule reads naturally.
     */
    sort.Slice(entries, func(i, j int) bool {
        if !entries[i].mtime.Equal(entries[j].mtime) {
            return entries[i].mtime.After(entries[j].mtime)
        }
        return entries[i].num > entries[j].num
    })

    keep := composeKeep(entries, job.Retention.MaxBuilds,
        maxAge, now)

    removed := make([]int, 0, len(entries))
    for i, e := range entries {
        if keep[i] || !e.completed {
            continue
        }
        buildDir := filepath.Join(buildsDir, strconv.Itoa(e.num))
        if err := os.RemoveAll(buildDir); err != nil {
            return removed, fmt.Errorf("retention.SweepJob: "+
                "remove %s: %w", buildDir, err)
        }
        removed = append(removed, e.num)
    }
    return removed, nil
}

/* composeKeep returns the per-entry survives-the-sweep mask
 * given the entries (newest-first) and the active rules. The
 * rules:
 *
 *   - An in-flight build (completed=false) is always kept.
 *   - A completed build survives if ANY active rule protects
 *     it (MaxBuilds top-N OR MaxAge cutoff).
 *   - If both rules are off (maxBuilds<=0 && maxAge<=0), the
 *     caller short-circuits earlier; this function defends
 *     by returning all-true.
 *
 * Caller passes the pre-parsed maxAge so the time.
 * ParseDuration error path stays at the SweepJob boundary.
 */
func composeKeep(entries []buildEntry, maxBuilds int,
    maxAge time.Duration, now time.Time) []bool {

    keep := make([]bool, len(entries))
    if maxBuilds <= 0 && maxAge <= 0 {
        for i := range keep {
            keep[i] = true
        }
        return keep
    }

    /* "completedSeen" counts only completed builds against
     * the MaxBuilds budget so an in-flight build does not
     * push a legitimate finished build out of the top N.
     */
    completedSeen := 0
    cutoff := now.Add(-maxAge)
    for i, e := range entries {
        if !e.completed {
            keep[i] = true
            continue
        }
        var protect bool
        if maxBuilds > 0 && completedSeen < maxBuilds {
            protect = true
        }
        if maxAge > 0 && !e.mtime.Before(cutoff) {
            protect = true
        }
        completedSeen++
        keep[i] = protect
    }
    return keep
}

/* listBuilds reads buildsDir and returns one buildEntry per
 * numerically-named subdirectory. mtime is taken from
 * result.json when present (so the executor's write-at-end
 * stamp wins over the mkdir-at-start stamp) and falls back
 * to the directory's mtime otherwise.
 */
func listBuilds(buildsDir string) ([]buildEntry, error) {
    raw, err := os.ReadDir(buildsDir)
    if err != nil {
        return nil, err
    }
    entries := make([]buildEntry, 0, len(raw))
    for _, e := range raw {
        if !e.IsDir() {
            continue
        }
        num, err := strconv.Atoi(e.Name())
        if err != nil || num < 1 {
            continue
        }
        buildDir := filepath.Join(buildsDir, e.Name())
        resultPath := filepath.Join(buildDir, "result.json")
        var entry buildEntry
        entry.num = num
        if info, err := os.Stat(resultPath); err == nil {
            entry.mtime = info.ModTime()
            entry.completed = validResult(resultPath)
        } else if info, err := os.Stat(buildDir); err == nil {
            entry.mtime = info.ModTime()
            entry.completed = false
        }
        entries = append(entries, entry)
    }
    return entries, nil
}

/* validResult returns true iff result.json parses and has a
 * non-empty Status. An empty file (executor crashed mid-
 * write) counts as not-completed so the sweeper leaves it
 * alone - the operator can clean it up by hand once they
 * know the executor isn't coming back.
 */
func validResult(path string) bool {
    data, err := os.ReadFile(path)
    if err != nil {
        return false
    }
    var br scheduler.BuildResult
    if err := json.Unmarshal(data, &br); err != nil {
        return false
    }
    return br.Status != ""
}

/* SweepAll runs SweepJob across every job currently in
 * storage and returns the removed-builds map for logging.
 * Errors from individual jobs are NOT fatal: the sweeper
 * keeps going so one misconfigured retention block does not
 * starve every other job on the same server.
 */
func SweepAll(store *storage.Storage, now time.Time) (
    map[string][]int, map[string]error) {

    removed := make(map[string][]int)
    errs := make(map[string]error)
    jobs, err := store.ListJobs()
    if err != nil {
        errs[""] = fmt.Errorf(
            "retention.SweepAll: ListJobs: %w", err)
        return removed, errs
    }
    for _, j := range jobs {
        nums, err := SweepJob(store, j.Name, now)
        if err != nil {
            errs[j.Name] = err
            continue
        }
        if len(nums) > 0 {
            removed[j.Name] = nums
        }
    }
    return removed, errs
}
