// Package server contains the wolfCI HTTP handlers. Phase 6.0
// ships the log-tailing endpoint; the rest of the UI (login,
// job pages, node management) lands in 6.1+.
package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// LogTailHandler serves SSE streams of build logs written by an
// agentsvc.FileLogSink. URL shape (clients pass values via the
// router's path matching):
//
//	GET /api/v1/builds/{job}/{n}/log
//
// The handler streams the file's current contents and then
// follows appends until the client disconnects or the context
// is cancelled. Format: "event: log\ndata: <base64>\n\n".
//
// Base64 is used so binary or newline-laden output round-trips
// cleanly inside one SSE event without the field-splitting
// rules of plain text payloads.
type LogTailHandler struct {
	// Root is the storage root (typically *storage.Storage.Root());
	// the file path is <Root>/builds/<job>/<n>/log.live.
	Root string

	// PollInterval is how long to sleep between size checks when
	// the file is currently at EOF. Defaults to 100ms.
	PollInterval time.Duration

	// IdleTimeout closes the stream if no new bytes arrive for
	// this long. Zero disables the idle close.
	IdleTimeout time.Duration
}

// LogPath returns the file path the handler reads for
// (jobName, buildNum). Exported for tests.
func (h *LogTailHandler) LogPath(jobName string, buildNum int) string {
	return path.Join(h.Root, "builds", jobName, strconv.Itoa(buildNum), "log.live")
}

func (h *LogTailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	job, num, err := parseLogTailPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "log tail requires a flushing ResponseWriter", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	poll := h.PollInterval
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}

	path := h.LogPath(job, num)

	// Wait briefly for the file to exist; agents may stream
	// LogChunks before the first reader connects.
	if err := waitForFile(r.Context(), path, poll, 2*time.Second); err != nil {
		// File never appeared; return an empty stream so the
		// client sees no events and can decide what to do.
		flusher.Flush()
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, fmt.Sprintf("open log: %v", err), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	lastActivity := time.Now()
	buf := make([]byte, 4096)
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		n, err := f.Read(buf)
		if n > 0 {
			if writeErr := writeLogEvent(w, buf[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
			lastActivity = time.Now()
			continue
		}
		if err != nil && !errors.Is(err, os.ErrClosed) && err.Error() != "EOF" {
			return
		}

		if h.IdleTimeout > 0 && time.Since(lastActivity) > h.IdleTimeout {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(poll):
		}
	}
}

// parseLogTailPath extracts {job} and {n} from URL paths shaped
// /api/v1/builds/<job>/<n>/log. We hand-parse so the handler
// stays mux-agnostic; any router (net/http v1, gorilla,
// chi, etc.) can mount this at the matching pattern.
func parseLogTailPath(p string) (string, int, error) {
	const prefix = "/api/v1/builds/"
	if !strings.HasPrefix(p, prefix) {
		return "", 0, fmt.Errorf("path %q missing %q prefix", p, prefix)
	}
	tail := strings.TrimPrefix(p, prefix)
	tail = strings.TrimSuffix(tail, "/log")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("path %q does not match /api/v1/builds/{job}/{n}/log", p)
	}
	job := parts[0]
	if job == "" || strings.ContainsAny(job, "/\\\x00") || job == "." || job == ".." {
		return "", 0, fmt.Errorf("invalid job name %q", job)
	}
	num, err := strconv.Atoi(parts[1])
	if err != nil || num < 1 {
		return "", 0, fmt.Errorf("invalid build number %q", parts[1])
	}
	return job, num, nil
}

func writeLogEvent(w http.ResponseWriter, chunk []byte) error {
	encoded := base64.StdEncoding.EncodeToString(chunk)
	_, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", encoded)
	return err
}

func waitForFile(ctx interface{ Done() <-chan struct{} }, path string, poll, deadline time.Duration) error {
	until := time.Now().Add(deadline)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(until) {
			return fmt.Errorf("waitForFile: %s never appeared", path)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waitForFile: context done")
		case <-time.After(poll):
		}
	}
}
