package server_test

/* Phase 14.2 - workspace browser at
 *   /jobs/<name>/builds/<n>/ws/         (root listing)
 *   /jobs/<name>/builds/<n>/ws/<path>   (subdir or file)
 *
 * Read-only. Operators can inspect what a build dropped into
 * its workspace without an SSH session on the runner.
 *
 * Contract:
 *   - Directory listings show immediate children only.
 *   - Path traversal ("..", absolute paths) is rejected with
 *     400; the handler must NEVER stat or open anything
 *     outside builds/<job>/<n>/workspace/.
 *   - Text files under workspaceTextPreviewMax bytes render
 *     inline with line numbers.
 *   - Everything else (large files, binaries) gets a
 *     "Download" link with the sniffed Content-Type.
 */

import (
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestWorkspace_ListsImmediateChildren(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "wsjob",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    ws := filepath.Join(st.Root(), "builds", "wsjob", "1",
        "workspace")
    if err := os.MkdirAll(filepath.Join(ws, "dist"), 0o755); err != nil {
        t.Fatalf("mkdir dist: %v", err)
    }
    if err := os.WriteFile(filepath.Join(ws, "README.md"),
        []byte("hello\n"), 0o644); err != nil {
        t.Fatalf("write README: %v", err)
    }
    if err := os.WriteFile(filepath.Join(ws, "Makefile"),
        []byte("all:\n\techo build\n"), 0o644); err != nil {
        t.Fatalf("write Makefile: %v", err)
    }
    if err := os.WriteFile(
        filepath.Join(ws, "dist", "bundle.tar.gz"),
        []byte("not really gzip"), 0o644); err != nil {
        t.Fatalf("write bundle: %v", err)
    }

    resp := mustGet(t, client,
        ts.URL+"/jobs/wsjob/builds/1/ws/")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    for _, want := range []string{
        "README.md", "Makefile", "dist",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("listing missing %q", want)
        }
    }
    /* Immediate children only - the nested bundle.tar.gz
     * must NOT show up in the root listing.
     */
    if strings.Contains(body, "bundle.tar.gz") {
        t.Errorf("root listing leaked nested file "+
            "bundle.tar.gz: body =\n%s", body)
    }
}

func TestWorkspace_RejectsPathTraversal(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    /* Follow redirects: net/http.ServeMux normalizes "/.."
     * in the URL with a 301 to the cleaned path before any
     * handler runs, so a strict "status must be 400" check
     * fails on the mux response rather than on our security
     * gate. The security property we actually care about is
     * "the secret never appears in the response body" - so
     * the test follows redirects and asserts on the final
     * body, regardless of how many hops it takes.
     */
    client := &http.Client{Jar: jar}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "guard",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    ws := filepath.Join(st.Root(), "builds", "guard", "1",
        "workspace")
    if err := os.MkdirAll(ws, 0o755); err != nil {
        t.Fatalf("mkdir ws: %v", err)
    }

    /* Drop a sibling file that the handler MUST NOT serve.
     * The unique sentinel string lets us assert leak-free
     * without coupling to the file's full contents.
     */
    sibling := filepath.Join(st.Root(), "builds", "guard", "1",
        "result.json")
    sentinel := "PHASE-14-2-SENTINEL"
    if err := os.WriteFile(sibling,
        []byte(`{"secret":"`+sentinel+`"}`), 0o644); err != nil {
        t.Fatalf("write sibling: %v", err)
    }

    badPaths := []string{
        "/jobs/guard/builds/1/ws/../result.json",
        "/jobs/guard/builds/1/ws/..%2Fresult.json",
        "/jobs/guard/builds/1/ws/%2e%2e/result.json",
        "/jobs/guard/builds/1/ws//etc/passwd",
    }
    for _, p := range badPaths {
        resp, err := client.Get(ts.URL + p)
        if err != nil {
            t.Errorf("GET %s: %v", p, err)
            continue
        }
        body := readBody(t, resp)
        if strings.Contains(body, sentinel) {
            t.Errorf("GET %s leaked the sibling file "+
                "(sentinel %q in body)", p, sentinel)
        }
    }
}

func TestWorkspace_RendersTextPreviewBelowThreshold(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "txt",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    ws := filepath.Join(st.Root(), "builds", "txt", "1",
        "workspace")
    if err := os.MkdirAll(ws, 0o755); err != nil {
        t.Fatalf("mkdir ws: %v", err)
    }
    body := "first line\nsecond line\nthird line\n"
    if err := os.WriteFile(filepath.Join(ws, "log.txt"),
        []byte(body), 0o644); err != nil {
        t.Fatalf("write log.txt: %v", err)
    }

    resp := mustGet(t, client,
        ts.URL+"/jobs/txt/builds/1/ws/log.txt")
    got := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* Line numbers + content present. */
    for _, want := range []string{
        "1", "first line",
        "2", "second line",
        "3", "third line",
    } {
        if !strings.Contains(got, want) {
            t.Errorf("preview missing %q", want)
        }
    }
}

func TestWorkspace_DownloadsBinaryWithSniff(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request, []*http.Request) error {
            return http.ErrUseLastResponse
        }}

    st := storageFromServer(ts)
    if err := st.SaveJob(&storage.Job{
        Name:  "bin",
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    ws := filepath.Join(st.Root(), "builds", "bin", "1",
        "workspace")
    if err := os.MkdirAll(ws, 0o755); err != nil {
        t.Fatalf("mkdir ws: %v", err)
    }
    /* PNG magic bytes so http.DetectContentType returns
     * image/png. The handler is supposed to stream the file
     * with the sniffed Content-Type rather than embed it in
     * an HTML preview.
     */
    pngHeader := []byte{
        0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
        0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
    }
    if err := os.WriteFile(filepath.Join(ws, "screenshot.png"),
        pngHeader, 0o644); err != nil {
        t.Fatalf("write png: %v", err)
    }

    /* The default direct GET should yield the file with a
     * non-HTML Content-Type and the original bytes.
     */
    resp := mustGet(t, client,
        ts.URL+"/jobs/bin/builds/1/ws/screenshot.png")
    got := resp.Header.Get("Content-Type")
    resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    if !strings.HasPrefix(got, "image/") {
        t.Errorf("Content-Type = %q, want image/*", got)
    }
}
