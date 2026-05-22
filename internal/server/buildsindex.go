package server

/* Per-job builds index page at /jobs/<name>/builds. Phase 13.3.
 *
 * The detail page caps its build-history table at 100; the
 * "see all" affordance links here for the complete list. 25
 * builds per page, newest-first, with optional ?since=<rfc3339>
 * filtering and ?page=N pagination.
 *
 * The handler reuses scanAllBuilds + buildHistoryRow from
 * jobdetail.go so a row in the index table looks identical to a
 * row in the detail page's history block.
 */

import (
    "net/http"
    "net/url"
    "strconv"
    "time"
)

/* buildsIndexPageSize is how many rows the index renders per
 * page. 25 fits comfortably above the fold on a 1080p screen
 * while still showing a useful chunk of history without
 * pagination. Phase 14 retention will keep most jobs well
 * below the first page.
 */
const buildsIndexPageSize = 25

func (s *Server) handleBuildsIndex(w http.ResponseWriter,
    r *http.Request, name string) {

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

    sinceParam := r.URL.Query().Get("since")
    var sinceCutoff time.Time
    if sinceParam != "" {
        t, err := time.Parse(time.RFC3339, sinceParam)
        if err != nil {
            http.Error(w,
                "invalid ?since= (want RFC3339): "+err.Error(),
                http.StatusBadRequest)
            return
        }
        sinceCutoff = t
    }

    filtered := all
    if !sinceCutoff.IsZero() {
        kept := make([]buildHistoryRow, 0, len(all))
        for _, row := range all {
            if !row.When.Before(sinceCutoff) {
                kept = append(kept, row)
            }
        }
        filtered = kept
    }

    page := 1
    if p := r.URL.Query().Get("page"); p != "" {
        n, err := strconv.Atoi(p)
        if err == nil && n > 0 {
            page = n
        }
    }
    totalPages := (len(filtered) + buildsIndexPageSize - 1) /
        buildsIndexPageSize
    if totalPages < 1 {
        totalPages = 1
    }

    start := (page - 1) * buildsIndexPageSize
    end := start + buildsIndexPageSize
    if start > len(filtered) {
        start = len(filtered)
    }
    if end > len(filtered) {
        end = len(filtered)
    }
    visible := filtered[start:end]

    /* Build the Prev/Next query strings so the template can
     * emit them verbatim without recomputing the active
     * filter on each link. Empty string means "no link".
     */
    prevQuery := ""
    if page > 1 {
        prevQuery = pageQueryString(page-1, sinceParam)
    }
    nextQuery := ""
    if page < totalPages {
        nextQuery = pageQueryString(page+1, sinceParam)
    }

    s.render(w, "buildsindex.html", map[string]interface{}{
        "Title":      "Builds - " + job.Name,
        "Name":       job.Name,
        "Builds":     visible,
        "Page":       page,
        "TotalPages": totalPages,
        "Since":      sinceParam,
        "PrevQuery":  prevQuery,
        "NextQuery":  nextQuery,
        "Total":      len(filtered),
    })
}

/* pageQueryString assembles a "?page=N&since=..." query suffix
 * for the Prev/Next links. since is included only when
 * non-empty so the canonical "no filter" link stays simple
 * ?page=N rather than ?page=N&since=.
 */
func pageQueryString(page int, since string) string {
    v := url.Values{}
    v.Set("page", strconv.Itoa(page))
    if since != "" {
        v.Set("since", since)
    }
    return "?" + v.Encode()
}
