package server_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/server"
)

// TestLogTail_LivePersistAndStream is the gating test for
// PLAN.md task 6.0. It writes two chunks through FileLogSink
// and reads them back as SSE events from the LogTailHandler.
func TestLogTail_LivePersistAndStream(t *testing.T) {
	root := t.TempDir()
	sink := agentsvc.NewFileLogSink(root)

	handler := &server.LogTailHandler{
		Root:         root,
		PollInterval: 25 * time.Millisecond,
		IdleTimeout:  3 * time.Second,
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Pre-write one chunk so the tailer has something to read
	// immediately. The second chunk lands AFTER the request
	// opens, exercising the polling-tail behavior.
	sink.WriteLogChunk("demo-job", 7, []byte("first-half"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/builds/demo-job/7/log", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	// Append the second chunk shortly after the request opens.
	go func() {
		time.Sleep(150 * time.Millisecond)
		sink.WriteLogChunk("demo-job", 7, []byte("-second-half"))
	}()

	collected := readSSEUntil(t, resp.Body, "first-half-second-half", 4*time.Second)
	if collected != "first-half-second-half" {
		t.Errorf("collected = %q, want %q", collected, "first-half-second-half")
	}
}

// TestLogTail_FallsBackToLocalExecutorLog covers the user-reported
// "[wolfci: stream closed]" symptom: the in-process LocalExecutor
// writes the build's output to <build>/log (no .live suffix),
// while the tail handler historically only looked at log.live (the
// agent-streamed name). Without the fallback the EventSource sees
// an empty 2s stream and the buildlog page renders the error
// message.
func TestLogTail_FallsBackToLocalExecutorLog(t *testing.T) {
    root := t.TempDir()
    /* Seed the LocalExecutor-style "log" file with content. No
     * log.live ever exists.
     */
    buildDir := root + "/builds/echo/4"
    if err := os.MkdirAll(buildDir, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    if err := os.WriteFile(buildDir+"/log",
        []byte("hello from LocalExecutor\n"), 0o644); err != nil {
        t.Fatalf("write log: %v", err)
    }

    handler := &server.LogTailHandler{
        Root:         root,
        PollInterval: 25 * time.Millisecond,
        IdleTimeout:  500 * time.Millisecond,
    }
    ts := httptest.NewServer(handler)
    defer ts.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
        ts.URL+"/api/v1/builds/echo/4/log", nil)
    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        t.Fatalf("Do: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d, want 200", resp.StatusCode)
    }
    /* The handler reads existing bytes immediately, encodes as
     * one SSE event, then idles until ctx or IdleTimeout. Read
     * the event and assert the content.
     */
    body, _ := io.ReadAll(resp.Body)
    s := string(body)
    if !strings.Contains(s, "event: log") {
        t.Errorf("SSE response missing 'event: log': %q", s)
    }
    /* The chunk is base64-encoded; we decode and check the
     * payload contains our marker.
     */
    if !sseBodyContains(s, "hello from LocalExecutor") {
        t.Errorf("decoded SSE content missing marker; raw = %q", s)
    }
}

func sseBodyContains(sseBody, want string) bool {
    for _, line := range strings.Split(sseBody, "\n") {
        if !strings.HasPrefix(line, "data: ") {
            continue
        }
        encoded := strings.TrimPrefix(line, "data: ")
        decoded, err := base64.StdEncoding.DecodeString(encoded)
        if err != nil {
            continue
        }
        if strings.Contains(string(decoded), want) {
            return true
        }
    }
    return false
}

// TestLogTail_BadPath rejects malformed routes with 400.
func TestLogTail_BadPath(t *testing.T) {
	root := t.TempDir()
	handler := &server.LogTailHandler{Root: root}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	cases := []string{
		"/api/v1/builds/../etc/passwd/log",
		"/api/v1/builds//1/log",
		"/api/v1/builds/job/zero/log",
		"/api/v1/builds/job/-1/log",
		"/wrong/prefix",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			resp, err := http.Get(ts.URL + p)
			if err != nil {
				t.Fatalf("Get(%s): %v", p, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("Get(%s) status = %d, want 400", p, resp.StatusCode)
			}
		})
	}
}

// readSSEUntil reads SSE "event: log\ndata: <base64>\n\n"
// frames from r, base64-decodes the data, concatenates, and
// returns once want is fully contained or the deadline fires.
func readSSEUntil(t *testing.T, r io.Reader, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	scanner := bufio.NewScanner(r)
	// Use a custom split function to read SSE frames separated
	// by blank lines.
	scanner.Split(splitSSEFrames)

	var sb strings.Builder
	done := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			frame := scanner.Text()
			data := extractData(frame)
			if data == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				continue
			}
			sb.Write(raw)
			if strings.Contains(sb.String(), want) {
				done <- sb.String()
				return
			}
		}
		done <- sb.String()
	}()
	select {
	case got := <-done:
		return got
	case <-time.After(time.Until(deadline)):
		return sb.String()
	}
}

func splitSSEFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i := 0; i+1 < len(data); i++ {
		if data[i] == '\n' && data[i+1] == '\n' {
			return i + 2, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func extractData(frame string) string {
	for _, line := range strings.Split(frame, "\n") {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	return ""
}
