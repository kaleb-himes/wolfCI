package server

/* Rename project action. Phase 14.4.
 *
 * POST /jobs/<old>/rename
 *   new_name=<new>
 *
 *   -> storage.RenameJob(old, new):
 *      move jobs/<old>/ -> jobs/<new>/
 *      rewrite spec.Name to <new>
 *      move builds/<old>/ -> builds/<new>/
 *   -> 303 to /jobs/<new>
 *
 * Permission gate today is requireSession, mirroring the
 * Phase 13.4 Delete action; the proper jobs.configure
 * matrix check lands with the broader matrix-driven HTTP
 * authz follow-up.
 */

import (
    "errors"
    "fmt"
    "net/http"
    "os"
    "strings"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func (s *Server) handleJobRename(w http.ResponseWriter,
    r *http.Request, name string) {

    if err := r.ParseForm(); err != nil {
        http.Error(w, "bad request",
            http.StatusBadRequest)
        return
    }
    newName := strings.TrimSpace(r.FormValue("new_name"))
    if newName == "" {
        http.Error(w, "new_name is required",
            http.StatusBadRequest)
        return
    }
    if !validJobName(newName) {
        http.Error(w, "invalid new_name",
            http.StatusBadRequest)
        return
    }
    if newName == name {
        /* Idempotent no-op. Redirect to the detail page so
         * the UI flow is consistent with the success path.
         */
        http.Redirect(w, r, "/jobs/"+name,
            http.StatusSeeOther)
        return
    }

    err := s.opts.Storage.RenameJob(name, newName)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            http.NotFound(w, r)
            return
        }
        if errors.Is(err, storage.ErrJobExists) {
            http.Error(w, fmt.Sprintf(
                "rename refused: a job named %q already "+
                    "exists", newName),
                http.StatusConflict)
            return
        }
        http.Error(w, "rename: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    http.Redirect(w, r, "/jobs/"+newName,
        http.StatusSeeOther)
}
