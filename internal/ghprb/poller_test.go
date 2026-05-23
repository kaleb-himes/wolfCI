package ghprb_test

/* internal/ghprb/poller_test.go - PLAN.md 18.6 gating test.
 *
 * Stands up a fake GitHub HTTP server with one open PR and
 * asserts that the poller queries /repos/{owner}/{repo}/pulls,
 * emits exactly one TriggerEvent, and that the event carries
 * the right PR id, head SHA, author, and target branch.
 *
 * No real network calls: every test uses httptest.Server.
 */

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
)

func TestPoller_DiscoversNewPR(t *testing.T) {
    var observedPath, observedAuth string

    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            observedPath = r.URL.Path
            observedAuth = r.Header.Get("Authorization")
            w.Header().Set("Content-Type", "application/json")
            fmt.Fprintln(w, `[
              {
                "number": 4242,
                "state": "open",
                "head": { "sha": "deadbeefcafe1234" },
                "user": { "login": "octocat" },
                "base": { "ref": "master" }
              }
            ]`)
        }))
    defer srv.Close()

    poller := ghprb.New(srv.URL, "fake-token", "wolfSSL",
        "wolfssl", srv.Client())

    events, err := poller.Poll(context.Background())
    if err != nil {
        t.Fatalf("Poll: %v", err)
    }
    if observedPath != "/repos/wolfSSL/wolfssl/pulls" {
        t.Errorf("observed path = %q, want /repos/wolfSSL/wolfssl/pulls",
            observedPath)
    }
    if observedAuth != "Bearer fake-token" {
        t.Errorf("observed auth = %q, want Bearer fake-token",
            observedAuth)
    }
    if len(events) != 1 {
        t.Fatalf("got %d events, want 1", len(events))
    }
    e := events[0]
    if e.PRID != 4242 {
        t.Errorf("PRID = %d, want 4242", e.PRID)
    }
    if e.HeadSHA != "deadbeefcafe1234" {
        t.Errorf("HeadSHA = %q", e.HeadSHA)
    }
    if e.Author != "octocat" {
        t.Errorf("Author = %q", e.Author)
    }
    if e.TargetBranch != "master" {
        t.Errorf("TargetBranch = %q", e.TargetBranch)
    }
}

func TestPoller_SkipsClosedPRs(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            w.Header().Set("Content-Type", "application/json")
            fmt.Fprintln(w, `[
              {
                "number": 1,
                "state": "closed",
                "head": { "sha": "a" },
                "user": { "login": "u" },
                "base": { "ref": "master" }
              },
              {
                "number": 2,
                "state": "open",
                "head": { "sha": "b" },
                "user": { "login": "u" },
                "base": { "ref": "master" }
              }
            ]`)
        }))
    defer srv.Close()

    poller := ghprb.New(srv.URL, "t", "o", "r", srv.Client())
    events, err := poller.Poll(context.Background())
    if err != nil {
        t.Fatalf("Poll: %v", err)
    }
    if len(events) != 1 || events[0].PRID != 2 {
        t.Fatalf("events = %+v, want one event PRID=2", events)
    }
}

func TestPoller_ErrorOnNon200(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            http.Error(w, "rate limited", http.StatusTooManyRequests)
        }))
    defer srv.Close()

    poller := ghprb.New(srv.URL, "t", "o", "r", srv.Client())
    if _, err := poller.Poll(context.Background()); err == nil {
        t.Fatalf("Poll returned nil error on 429 response")
    }
}
