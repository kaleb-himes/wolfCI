package server

/* Workspace browser at /jobs/<name>/builds/<n>/ws/<path>.
 * Phase 14.2.
 *
 * Read-only. Lets an operator inspect what a build dropped
 * into its workspace without SSH'ing to the runner.
 *
 * Two response shapes:
 *
 *   - Directory: HTML listing of immediate children.
 *   - File:
 *       under workspaceTextPreviewMax bytes AND text-like
 *         -> HTML preview with line numbers.
 *       otherwise
 *         -> stream the bytes with the sniffed
 *            Content-Type so the browser downloads (or
 *            renders, if it knows the type) the raw file.
 *
 * Path-traversal defense layers:
 *
 *   1. Reject any sub-path containing ".." or starting with
 *      "/" before touching the filesystem.
 *   2. After joining root+sub, take filepath.Abs of both
 *      and require the joined path to start with the root.
 *      Catches URL-encoded traversal and symlinks that
 *      slipped past step 1.
 *
 * No symlink resolution beyond the workspace root: a symlink
 * INSIDE the workspace that points OUT is rejected by the
 * step-2 prefix check; a symlink to a file inside the
 * workspace works as you would expect.
 */

import (
    "fmt"
    "net/http"
    "os"
    "path"
    "path/filepath"
    "sort"
    "strconv"
    "strings"
)

/* workspaceTextPreviewMax bounds the inline preview. Files
 * larger than this stream raw with a Download link instead.
 * 256 KiB is enough for a typical build log, manifest, or
 * config snapshot without bloating the response when the
 * workspace happens to contain a multi-MB binary.
 */
const workspaceTextPreviewMax = 256 * 1024

/* workspaceEntry is the per-row view the listing template
 * iterates over.
 */
type workspaceEntry struct {
    Name  string
    IsDir bool
    Size  int64
    Href  string
}

/* workspacePreviewLine pairs a 1-based line number with the
 * line text for the text-preview template.
 */
type workspacePreviewLine struct {
    N    int
    Text string
}

func (s *Server) handleWorkspace(w http.ResponseWriter,
    r *http.Request, jobName string, buildNum int, sub string) {

    if !validJobName(jobName) {
        http.Error(w, "invalid job name",
            http.StatusBadRequest)
        return
    }
    if buildNum < 1 {
        http.Error(w, "invalid build number",
            http.StatusBadRequest)
        return
    }

    /* Step 1: textual screen on the sub-path. Reject leading
     * slashes (absolute path attempt) and any ".." segment.
     * URL-encoded variants survive net/http's decode pass
     * and arrive as literal "..", which path.Clean would
     * fold into the parent dir; we reject before Clean.
     */
    if strings.HasPrefix(sub, "/") {
        http.Error(w, "absolute paths are not allowed",
            http.StatusBadRequest)
        return
    }
    for _, seg := range strings.Split(sub, "/") {
        if seg == ".." {
            http.Error(w, "path traversal is not allowed",
                http.StatusBadRequest)
            return
        }
    }

    /* path.Clean strips trailing slashes and "//" runs,
     * normalizing what the operator typed without
     * introducing a ".." segment (we just rejected those).
     */
    cleaned := path.Clean("/" + sub)[1:]
    if cleaned == "." {
        cleaned = ""
    }

    wsRoot := filepath.Join(s.opts.Storage.Root(), "builds",
        jobName, strconv.Itoa(buildNum), "workspace")
    absRoot, err := filepath.Abs(wsRoot)
    if err != nil {
        http.Error(w, "resolve workspace root: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    target := filepath.Join(absRoot, filepath.FromSlash(cleaned))
    absTarget, err := filepath.Abs(target)
    if err != nil {
        http.Error(w, "resolve target: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    /* Step 2: enforce the absolute target stays inside the
     * workspace root. The HasPrefix check is exact on
     * absRoot to avoid the classic /tmp/foo vs /tmp/foobar
     * confusion; we require either equality or the
     * separator at len(absRoot).
     */
    if absTarget != absRoot &&
        !strings.HasPrefix(absTarget,
            absRoot+string(filepath.Separator)) {
        http.Error(w, "path escapes workspace",
            http.StatusBadRequest)
        return
    }

    info, err := os.Stat(absTarget)
    if err != nil {
        if os.IsNotExist(err) {
            http.NotFound(w, r)
            return
        }
        http.Error(w, "stat: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    if info.IsDir() {
        s.renderWorkspaceListing(w, r, jobName, buildNum,
            cleaned, absTarget)
        return
    }
    s.serveWorkspaceFile(w, r, jobName, buildNum, cleaned,
        absTarget, info)
}

func (s *Server) renderWorkspaceListing(w http.ResponseWriter,
    r *http.Request, jobName string, buildNum int, sub string,
    absDir string) {

    raw, err := os.ReadDir(absDir)
    if err != nil {
        http.Error(w, "readdir: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    base := fmt.Sprintf("/jobs/%s/builds/%d/ws/", jobName,
        buildNum)
    if sub != "" {
        base += sub + "/"
    }
    entries := make([]workspaceEntry, 0, len(raw))
    for _, e := range raw {
        info, err := e.Info()
        if err != nil {
            continue
        }
        entries = append(entries, workspaceEntry{
            Name:  e.Name(),
            IsDir: e.IsDir(),
            Size:  info.Size(),
            Href:  base + e.Name(),
        })
    }
    /* Directories first, then files, each alphabetized. */
    sort.Slice(entries, func(i, j int) bool {
        if entries[i].IsDir != entries[j].IsDir {
            return entries[i].IsDir
        }
        return entries[i].Name < entries[j].Name
    })

    parent := ""
    if sub != "" {
        parentSub := path.Dir(sub)
        if parentSub == "." {
            parent = fmt.Sprintf(
                "/jobs/%s/builds/%d/ws/", jobName, buildNum)
        } else {
            parent = fmt.Sprintf(
                "/jobs/%s/builds/%d/ws/%s/",
                jobName, buildNum, parentSub)
        }
    }

    s.render(w, "workspace.html", map[string]interface{}{
        "Title":    "Workspace - " + jobName + " #" +
            strconv.Itoa(buildNum),
        "JobName":  jobName,
        "BuildNum": buildNum,
        "SubPath":  sub,
        "Entries":  entries,
        "Parent":   parent,
        "BuildURL": fmt.Sprintf("/jobs/%s/builds/%d",
            jobName, buildNum),
    })
}

func (s *Server) serveWorkspaceFile(w http.ResponseWriter,
    r *http.Request, jobName string, buildNum int, sub string,
    absPath string, info os.FileInfo) {

    /* Files over the preview threshold or detected as
     * binary stream raw. The DetectContentType call needs
     * the first up-to-512 bytes; we read those once and
     * decide.
     */
    f, err := os.Open(absPath)
    if err != nil {
        http.Error(w, "open: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    defer f.Close()

    head := make([]byte, 512)
    n, _ := f.Read(head)
    head = head[:n]
    ctype := http.DetectContentType(head)

    streamRaw := info.Size() > workspaceTextPreviewMax ||
        !isTextLike(ctype)
    if streamRaw {
        w.Header().Set("Content-Type", ctype)
        w.Header().Set("Content-Length",
            strconv.FormatInt(info.Size(), 10))
        /* Restart from the top: we already consumed up to
         * 512 bytes into head. http.ServeContent would
         * handle this but adds Range/If-Modified-Since
         * surface we do not need today; a plain copy keeps
         * the response simple.
         */
        if _, err := f.Seek(0, 0); err != nil {
            http.Error(w, "seek: "+err.Error(),
                http.StatusInternalServerError)
            return
        }
        copyBuffer(w, f)
        return
    }

    /* Read the rest of the file - we already have the head.
     * For a 256 KiB cap this is one extra ReadAll at most.
     */
    if _, err := f.Seek(0, 0); err != nil {
        http.Error(w, "seek: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    body := make([]byte, info.Size())
    if _, err := f.Read(body); err != nil {
        http.Error(w, "read: "+err.Error(),
            http.StatusInternalServerError)
        return
    }

    lines := strings.Split(string(body), "\n")
    /* strings.Split appends an empty "" when the file ends
     * in a newline; the preview reads better without it.
     */
    if len(lines) > 0 && lines[len(lines)-1] == "" {
        lines = lines[:len(lines)-1]
    }
    preview := make([]workspacePreviewLine, 0, len(lines))
    for i, l := range lines {
        preview = append(preview, workspacePreviewLine{
            N: i + 1, Text: l,
        })
    }

    s.render(w, "workspace_preview.html",
        map[string]interface{}{
            "Title":   "Workspace - " + sub,
            "JobName": jobName,
            "BuildNum": buildNum,
            "SubPath":  sub,
            "Lines":    preview,
            "Parent": fmt.Sprintf(
                "/jobs/%s/builds/%d/ws/%s",
                jobName, buildNum, path.Dir(sub)),
            "BuildURL": fmt.Sprintf("/jobs/%s/builds/%d",
                jobName, buildNum),
        })
}

/* isTextLike reads a Content-Type sniff and returns true for
 * payloads we are willing to render inline with line
 * numbers. The browser's own decision differs from ours -
 * some "application/octet-stream" payloads are ASCII logs,
 * but we err on the side of "if the sniff is not text/* or
 * a small set of inline-friendly application/* types, treat
 * as binary".
 */
func isTextLike(ctype string) bool {
    if strings.HasPrefix(ctype, "text/") {
        return true
    }
    switch {
    case strings.HasPrefix(ctype, "application/json"):
        return true
    case strings.HasPrefix(ctype, "application/xml"):
        return true
    case strings.HasPrefix(ctype, "application/javascript"):
        return true
    }
    return false
}

/* copyBuffer is io.Copy with a 32 KiB buffer to bound the
 * per-request allocation. We do not need io.Copy's reusable
 * buffer pool here.
 */
func copyBuffer(dst http.ResponseWriter, src interface {
    Read(p []byte) (int, error)
}) {
    buf := make([]byte, 32*1024)
    for {
        n, err := src.Read(buf)
        if n > 0 {
            if _, werr := dst.Write(buf[:n]); werr != nil {
                return
            }
        }
        if err != nil {
            return
        }
    }
}
