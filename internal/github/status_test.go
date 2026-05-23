package github_test

/* internal/github/status_test.go - PLAN.md 18.10 gating test.
 *
 * Drives an httptest server, posts each of the four commit-
 * status states (pending / success / failure / error) under a
 * chosen context with a target_url, and asserts:
 *
 *   - the POST URL is /repos/{owner}/{repo}/statuses/{sha}
 *   - the Authorization: Bearer <token> header is sent
 *   - the JSON body carries state, context, target_url, and
 *     description
 *
 * Per PLAN.md 18.10, production callers must use a wolfSSL-
 * backed HTTP transport rather than net/http defaults; the
 * test runs against httptest (plain HTTP) by injecting the
 * test server's http.Client.
 */

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "testing"

    gh "github.com/kaleb-himes/wolfCI/internal/github"
)

func TestPostStatus_RoundTrip(t *testing.T) {
    type observed struct {
        path        string
        auth        string
        contentType string
        body        map[string]any
    }
    var got observed

    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            got.path = r.URL.Path
            got.auth = r.Header.Get("Authorization")
            got.contentType = r.Header.Get("Content-Type")
            body, _ := io.ReadAll(r.Body)
            _ = json.Unmarshal(body, &got.body)
            w.WriteHeader(http.StatusCreated)
            fmt.Fprintln(w, `{"id":1}`)
        }))
    defer srv.Close()

    client := gh.NewStatusClient(srv.URL, "fake-token",
        srv.Client())

    cases := []gh.StatusState{
        gh.StatePending, gh.StateSuccess,
        gh.StateFailure, gh.StateError,
    }
    for _, st := range cases {
        t.Run(string(st), func(t *testing.T) {
            err := client.PostStatus(context.Background(),
                gh.StatusRequest{
                    Owner:       "wolfSSL",
                    Repo:        "wolfssl",
                    SHA:         "deadbeef",
                    State:       st,
                    Context:     "PRB-master-job",
                    TargetURL:   "https://ci.example.com/job/x/1",
                    Description: "running",
                })
            if err != nil {
                t.Fatalf("PostStatus: %v", err)
            }
            if got.path !=
                "/repos/wolfSSL/wolfssl/statuses/deadbeef" {
                t.Errorf("path = %q", got.path)
            }
            if got.auth != "Bearer fake-token" {
                t.Errorf("auth = %q", got.auth)
            }
            if got.contentType != "application/json" {
                t.Errorf("Content-Type = %q", got.contentType)
            }
            if got.body["state"] != string(st) {
                t.Errorf("body.state = %v, want %s",
                    got.body["state"], st)
            }
            if got.body["context"] != "PRB-master-job" {
                t.Errorf("body.context = %v",
                    got.body["context"])
            }
            if got.body["target_url"] !=
                "https://ci.example.com/job/x/1" {
                t.Errorf("body.target_url = %v",
                    got.body["target_url"])
            }
            if got.body["description"] != "running" {
                t.Errorf("body.description = %v",
                    got.body["description"])
            }
        })
    }
}

func TestPostStatus_RejectsBadState(t *testing.T) {
    client := gh.NewStatusClient("http://example.invalid", "t",
        nil)
    err := client.PostStatus(context.Background(),
        gh.StatusRequest{
            Owner: "o", Repo: "r", SHA: "s",
            State: gh.StatusState("bogus"),
        })
    if err == nil {
        t.Fatalf("PostStatus accepted invalid state")
    }
}

func TestPostStatus_PropagatesNon2xx(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            http.Error(w, "rate limited",
                http.StatusTooManyRequests)
        }))
    defer srv.Close()
    client := gh.NewStatusClient(srv.URL, "t", srv.Client())
    err := client.PostStatus(context.Background(),
        gh.StatusRequest{
            Owner: "o", Repo: "r", SHA: "s",
            State: gh.StateSuccess,
        })
    if err == nil {
        t.Fatalf("PostStatus returned nil on 429 response")
    }
}
