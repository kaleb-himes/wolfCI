package server

/* Rebuild action. Phase 14.3.
 *
 * POST /jobs/<name>/rebuild
 *   -> loads the spec snapshot from the most-recent build,
 *      enqueues a new build with that spec, redirects to
 *      the new build's live log.
 *
 * POST /jobs/<name>/rebuild?spec=current
 *   -> ignores the snapshot and re-enqueues with the live
 *      spec on disk instead. The detail page surfaces this
 *      when the live spec has drifted from the snapshot.
 *
 * "Most-recent build" = the highest-mtime build under
 * builds/<name>/, mirroring how the per-job detail page
 * orders its history.
 */

import (
    "errors"
    "fmt"
    "net/http"
    "os"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func (s *Server) handleJobRebuild(w http.ResponseWriter,
    r *http.Request, name string) {

    if s.opts.JobRunner == nil {
        http.Error(w, "job runner not configured",
            http.StatusNotImplemented)
        return
    }

    useCurrent := r.URL.Query().Get("spec") == "current"

    var spec *storage.Job
    if useCurrent {
        loaded, err := s.opts.Storage.LoadJob(name)
        if err != nil {
            http.Error(w, "load job: "+err.Error(),
                http.StatusNotFound)
            return
        }
        spec = loaded
    } else {
        target, err := findMostRecentBuild(
            s.opts.Storage.Root(), name)
        if err != nil {
            http.Error(w, "find latest build: "+err.Error(),
                http.StatusInternalServerError)
            return
        }
        if target == 0 {
            http.Error(w, "no previous build to rebuild",
                http.StatusBadRequest)
            return
        }
        loaded, err := s.opts.Storage.LoadSpecSnapshot(name,
            target)
        if err != nil {
            if errors.Is(err, os.ErrNotExist) {
                /* Older builds may predate Phase 14.3 and
                 * have no snapshot on disk. Tell the
                 * operator they need to use the
                 * "current spec" variant.
                 */
                http.Error(w, fmt.Sprintf(
                    "build %d has no spec snapshot; "+
                        "use ?spec=current to rebuild "+
                        "with the live spec", target),
                    http.StatusConflict)
                return
            }
            http.Error(w, "load snapshot: "+err.Error(),
                http.StatusInternalServerError)
            return
        }
        spec = loaded
    }

    num, err := s.opts.JobRunner.Enqueue(spec)
    if err != nil {
        http.Error(w, "enqueue: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    http.Redirect(w, r,
        fmt.Sprintf("/jobs/%s/builds/%d", name, num),
        http.StatusSeeOther)
}

/* findMostRecentBuild returns the build number of the
 * newest build under builds/<name>/, or 0 if no builds
 * exist. Reuses scanAllBuilds from jobdetail.go so the
 * "newest" definition is consistent with what the detail
 * page shows.
 */
func findMostRecentBuild(root, jobName string) (int, error) {
    rows, err := scanAllBuilds(root, jobName, time.Now())
    if err != nil {
        return 0, err
    }
    if len(rows) == 0 {
        return 0, nil
    }
    return rows[0].Number, nil
}
