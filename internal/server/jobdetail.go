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
 */
type buildHistoryRow struct {
    Number       int
    Status       string
    RelativeTime string
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request,
    name string) {

    job, err := s.opts.Storage.LoadJob(name)
    if err != nil {
        http.NotFound(w, r)
        return
    }

    rows, truncated, err := scanBuildHistory(
        s.opts.Storage.Root(), name, maxDetailBuilds, time.Now())
    if err != nil {
        http.Error(w, "scan builds: "+err.Error(),
            http.StatusInternalServerError)
        return
    }

    s.render(w, "jobdetail.html", map[string]interface{}{
        "Title":       job.Name,
        "Name":        job.Name,
        "Description": job.Description,
        "Builds":      rows,
        "Truncated":   truncated,
        "CanRun":      s.opts.JobRunner != nil,
    })
}

/* scanBuildHistory walks builds/<jobName>/ and returns up to
 * limit rows sorted newest-first. Reading the result.json is
 * best-effort: a directory with no result.json yet (build still
 * running, or executor crashed before writing) renders as
 * "running" rather than dropping the row.
 *
 * Ordering: by the directory's mtime - the executor creates the
 * dir at build start and writes result.json at the end, so the
 * dir's mtime tracks the latest activity for that build. Tests
 * force-set the mtime on result.json; that updates the parent
 * dir's mtime on most filesystems, but we sort on the dir
 * itself to keep the rule explicit.
 *
 * The "truncated" return is true when there are more builds on
 * disk than fit in limit; the template renders a "see all" link
 * in that case.
 */
func scanBuildHistory(root, jobName string, limit int,
    now time.Time) ([]buildHistoryRow, bool, error) {

    dir := filepath.Join(root, "builds", jobName)
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, false, nil
        }
        return nil, false, fmt.Errorf("read %s: %w", dir, err)
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
            /* No result.json yet: an in-flight build or one
             * the executor never finished. Use the dir mtime so
             * an in-flight build still sorts correctly relative
             * to completed neighbors.
             */
            mtime = info.ModTime()
        }
        raws = append(raws, rawRow{
            num: num, status: status, mtime: mtime,
        })
    }

    /* Newest-first by mtime. Ties break on the higher build
     * number so a freshly-restored backup with identical mtimes
     * still gives a deterministic order.
     */
    sort.Slice(raws, func(i, j int) bool {
        if !raws[i].mtime.Equal(raws[j].mtime) {
            return raws[i].mtime.After(raws[j].mtime)
        }
        return raws[i].num > raws[j].num
    })

    truncated := false
    if limit > 0 && len(raws) > limit {
        raws = raws[:limit]
        truncated = true
    }

    rows := make([]buildHistoryRow, 0, len(raws))
    for _, r := range raws {
        rows = append(rows, buildHistoryRow{
            Number:       r.num,
            Status:       r.status,
            RelativeTime: formatRelative(r.mtime, now),
        })
    }
    return rows, truncated, nil
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
