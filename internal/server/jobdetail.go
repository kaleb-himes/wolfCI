package server

/* Per-job detail page at /jobs/<name>. Phase 13.1.
 *
 * Loads the Job from storage, scans builds/<name>/* for the
 * most-recent 100 builds, and renders the detail template with
 * the operator sidebar + description + build history. Phase
 * 13.2-13.4 layer on permalinks, the /jobs/<name>/builds index,
 * and the Delete action.
 */

import (
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
)

/* maxDetailBuilds caps the build-history panel on the detail
 * page. A "see all" link points at /jobs/<name>/builds (Phase
 * 13.3) for the full paginated history.
 */
const maxDetailBuilds = 100

/* buildHistoryRow is the per-row view model the jobdetail
 * template iterates over. Status is the scheduler.Status string
 * ("success", "failure", "cancelled", "error") or "running" for
 * an in-flight build whose result.json hasn't landed yet.
 *
 * When carries the raw mtime that produced RelativeTime; the
 * builds-index handler needs it to apply the ?since= filter
 * without re-parsing the formatted "5m ago" string. Templates
 * read RelativeTime, not When.
 */
type buildHistoryRow struct {
    Number       int
    Status       string
    RelativeTime string
    When         time.Time
}

/* permalinks holds the "Last X build" pointers the detail page
 * shows in its header. A nil pointer means "no such build yet"
 * and the template renders "none" instead of a link.
 *
 *   LastBuild        - most recent build of any status, including
 *                      an in-flight "running" build.
 *   LastStable       - most recent successful build. Phase 14
 *                      tightens this once Rebuild Last lands and
 *                      we can distinguish a first-try success
 *                      from a successful retry; until then,
 *                      stable == successful.
 *   LastSuccessful   - most recent build whose Status is success.
 *   LastUnsuccessful - most recent build that completed without
 *                      success (failure, error, cancelled). An
 *                      in-flight "running" build does NOT count.
 *   LastCompleted    - most recent build that has a result.json
 *                      on disk, regardless of outcome. Excludes
 *                      "running" entries.
 */
type permalinks struct {
    LastBuild        *buildHistoryRow
    LastStable       *buildHistoryRow
    LastSuccessful   *buildHistoryRow
    LastUnsuccessful *buildHistoryRow
    LastCompleted    *buildHistoryRow
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request,
    name string) {

    job, err := s.opts.Storage.LoadJob(name)
    if err != nil {
        http.NotFound(w, r)
        return
    }

    all, err := scanAllBuilds(
        s.opts.Storage.Root(), name, time.Now())
    if err != nil {
        http.Error(w, "scan builds: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    perms := computePermalinks(all)

    visible := all
    truncated := false
    if len(visible) > maxDetailBuilds {
        visible = visible[:maxDetailBuilds]
        truncated = true
    }

    /* Phase 15.6: trigger graph relationships. ListJobs is
     * a directory scan; for the per-job page this is fine
     * since the count is bounded by the operator's job
     * inventory. If it grows enough to matter, an in-memory
     * cache is the obvious follow-up.
     */
    var links triggerLinks
    if jobs, err := s.opts.Storage.ListJobs(); err == nil {
        links = computeTriggerLinks(name, jobs)
    }

    s.render(w, "jobdetail.html", map[string]interface{}{
        "Title":       job.Name,
        "Name":        job.Name,
        "Description": job.Description,
        "Builds":      visible,
        "Truncated":   truncated,
        "Permalinks":  perms,
        "Upstream":    links.Upstream,
        "Downstream":  links.Downstream,
        "Siblings":    links.Siblings,
        "CanRun":      s.opts.JobRunner != nil,
    })
}

/* handleJobDelete removes jobs/<name>/ via storage.DeleteJob
 * and redirects to /jobs. The build history under
 * builds/<name>/ is left in place so the operator can re-
 * create the job under the same name later (Phase 13.4's
 * locked decision; a separate destructive "wipe history"
 * flow is reserved for a future backlog item).
 *
 * Permission gate today is requireSession, mirroring the
 * /jobs/<name>/edit handler. The proper jobs.configure
 * matrix check is the same matrix-driven HTTP authz
 * follow-up tracked under the Phase 12.7 nodes.configure
 * note - it lands once for every per-resource action.
 */
func (s *Server) handleJobDelete(w http.ResponseWriter,
    r *http.Request, name string) {

    if err := s.opts.Storage.DeleteJob(name); err != nil {
        if errors.Is(err, os.ErrNotExist) {
            http.NotFound(w, r)
            return
        }
        http.Error(w, "delete job: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

/* scanAllBuilds walks builds/<jobName>/ and returns every row
 * sorted newest-first. The caller chooses how to slice the
 * result for the visible history table (handleJobDetail caps
 * at maxDetailBuilds) and feeds the same full slice into
 * computePermalinks so the "Last successful build" pointer
 * stays correct even when the table is truncated.
 *
 * Reading result.json is best-effort: a directory with no
 * result.json yet (build still running, or executor crashed
 * before writing) renders as "running" rather than being
 * dropped. Ordering uses the result.json mtime when present,
 * otherwise the build dir's mtime - the executor creates the
 * dir at build start and writes result.json at the end, so
 * either is a useful proxy for "when this build last did
 * something". Ties break on the higher build number so a
 * freshly-restored backup with identical mtimes still gives
 * a deterministic order.
 */
func scanAllBuilds(root, jobName string, now time.Time) (
    []buildHistoryRow, error) {

    dir := filepath.Join(root, "builds", jobName)
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("read %s: %w", dir, err)
    }

    type rawRow struct {
        num    int
        status string
        mtime  time.Time
    }
    raws := make([]rawRow, 0, len(entries))
    for _, e := range entries {
        if !e.IsDir() {
            continue
        }
        num, err := strconv.Atoi(e.Name())
        if err != nil || num < 1 {
            continue
        }
        buildDir := filepath.Join(dir, e.Name())
        resultPath := filepath.Join(buildDir, "result.json")

        status := "running"
        var mtime time.Time
        if info, err := os.Stat(resultPath); err == nil {
            mtime = info.ModTime()
            if data, err := os.ReadFile(resultPath); err == nil {
                var br scheduler.BuildResult
                if json.Unmarshal(data, &br) == nil &&
                    br.Status != "" {
                    status = string(br.Status)
                }
            }
        } else if info, err := os.Stat(buildDir); err == nil {
            mtime = info.ModTime()
        }
        raws = append(raws, rawRow{
            num: num, status: status, mtime: mtime,
        })
    }

    sort.Slice(raws, func(i, j int) bool {
        if !raws[i].mtime.Equal(raws[j].mtime) {
            return raws[i].mtime.After(raws[j].mtime)
        }
        return raws[i].num > raws[j].num
    })

    rows := make([]buildHistoryRow, 0, len(raws))
    for _, r := range raws {
        rows = append(rows, buildHistoryRow{
            Number:       r.num,
            Status:       r.status,
            RelativeTime: formatRelative(r.mtime, now),
            When:         r.mtime,
        })
    }
    return rows, nil
}

/* computePermalinks scans the (newest-first) build history and
 * returns the "Last X build" pointers the detail page header
 * exposes. Each pointer aliases the corresponding row inside
 * rows, so the caller must not mutate rows after the call (the
 * template only reads). A nil pointer means "no such build
 * yet" and the template renders "none".
 *
 * Rule for "stable": treat success as stable for the initial
 * implementation. Phase 14 introduces Rebuild Last and a
 * first-try-vs-retry distinction; at that point stable
 * tightens to "success AND not a rebuild-last reattempt" per
 * the PLAN.md 13.2 spec.
 */
func computePermalinks(rows []buildHistoryRow) permalinks {
    var p permalinks
    successStr := string(scheduler.StatusSuccess)
    for i := range rows {
        r := &rows[i]
        if p.LastBuild == nil {
            p.LastBuild = r
        }
        if r.Status == "running" {
            continue
        }
        if p.LastCompleted == nil {
            p.LastCompleted = r
        }
        if r.Status == successStr {
            if p.LastSuccessful == nil {
                p.LastSuccessful = r
            }
            if p.LastStable == nil {
                p.LastStable = r
            }
            continue
        }
        if p.LastUnsuccessful == nil {
            p.LastUnsuccessful = r
        }
    }
    return p
}

/* formatRelative renders t as a short "5m ago" / "2h ago" /
 * "3d ago" string relative to now. Zero or future timestamps
 * render as an empty string so the template's muted column
 * stays empty rather than showing a confusing "0s ago".
 */
func formatRelative(t, now time.Time) string {
    if t.IsZero() {
        return ""
    }
    d := now.Sub(t)
    if d < 0 {
        return ""
    }
    switch {
    case d < time.Minute:
        return fmt.Sprintf("%ds ago", int(d.Seconds()))
    case d < time.Hour:
        return fmt.Sprintf("%dm ago", int(d.Minutes()))
    case d < 24*time.Hour:
        return fmt.Sprintf("%dh ago", int(d.Hours()))
    default:
        return fmt.Sprintf("%dd ago", int(d.Hours()/24))
    }
}
