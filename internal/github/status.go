/* Package github carries the wolfCI -> GitHub API client paths.
 *
 * Phase 18.10 ships the commit-status POST: when a GHPRB-
 * triggered build starts, succeeds, fails, or errors, wolfCI
 * tells GitHub via a POST to
 * /repos/{owner}/{repo}/statuses/{sha}. The status appears in
 * the PR's "checks" UI and gates merge-protected branches.
 *
 * The package is transport-agnostic: NewStatusClient takes a
 * *http.Client so production wiring can supply a client whose
 * Transport is the wolfSSL-backed RoundTripper (PLAN.md 18.10
 * rule: HTTPS transport is the wolfSSL TLS http client, NOT
 * net/http defaults). The httptest-based unit tests inject the
 * test server's own Client.
 */
package github

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
)

/* StatusState is one of the four GitHub commit-status values.
 * Strongly typed at the Go layer so a typo at the call site
 * fails to compile instead of silently producing a 422 from
 * GitHub.
 */
type StatusState string

const (
    StatePending StatusState = "pending"
    StateSuccess StatusState = "success"
    StateFailure StatusState = "failure"
    StateError   StatusState = "error"
)

/* validStates is the set StatusState may legally hold. Looked
 * up in PostStatus to fail fast on a bogus literal a caller
 * cast to StatusState.
 */
var validStates = map[StatusState]struct{}{
    StatePending: {},
    StateSuccess: {},
    StateFailure: {},
    StateError:   {},
}

/* StatusRequest is the input to PostStatus. Every field except
 * Description is required.
 */
type StatusRequest struct {
    Owner       string
    Repo        string
    SHA         string
    State       StatusState
    Context     string
    TargetURL   string
    Description string
}

/* StatusClient posts commit statuses to one GitHub install.
 * The token authenticates every request via the
 * Authorization: Bearer header.
 */
type StatusClient struct {
    baseURL string
    token   string
    client  *http.Client
}

/* NewStatusClient constructs a StatusClient. A nil http.Client
 * falls back to http.DefaultClient (test-only convenience;
 * production callers must pass a wolfSSL-backed client).
 */
func NewStatusClient(baseURL, token string,
    client *http.Client) *StatusClient {

    if client == nil {
        client = http.DefaultClient
    }
    return &StatusClient{
        baseURL: strings.TrimRight(baseURL, "/"),
        token:   token,
        client:  client,
    }
}

/* statusBody is the JSON payload GitHub's
 * /repos/{owner}/{repo}/statuses/{sha} endpoint expects.
 */
type statusBody struct {
    State       string `json:"state"`
    Context     string `json:"context,omitempty"`
    TargetURL   string `json:"target_url,omitempty"`
    Description string `json:"description,omitempty"`
}

/* PostStatus sends one commit-status update. Returns an error
 * on transport failure, an invalid state, or a non-2xx
 * response. A 201 Created is the documented happy path; this
 * implementation accepts any 2xx for forward compatibility
 * with future API versions.
 */
func (c *StatusClient) PostStatus(ctx context.Context,
    req StatusRequest) error {

    if _, ok := validStates[req.State]; !ok {
        return fmt.Errorf(
            "github.PostStatus: invalid state %q "+
                "(want pending|success|failure|error)",
            req.State)
    }
    if req.Owner == "" || req.Repo == "" || req.SHA == "" {
        return fmt.Errorf(
            "github.PostStatus: owner, repo, sha are required")
    }

    url := fmt.Sprintf("%s/repos/%s/%s/statuses/%s",
        c.baseURL, req.Owner, req.Repo, req.SHA)

    body := statusBody{
        State:       string(req.State),
        Context:     req.Context,
        TargetURL:   req.TargetURL,
        Description: req.Description,
    }
    raw, err := json.Marshal(body)
    if err != nil {
        return fmt.Errorf("github.PostStatus: marshal: %w", err)
    }

    httpReq, err := http.NewRequestWithContext(ctx,
        http.MethodPost, url, bytes.NewReader(raw))
    if err != nil {
        return fmt.Errorf("github.PostStatus: new request: %w",
            err)
    }
    if c.token != "" {
        httpReq.Header.Set("Authorization", "Bearer "+c.token)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    httpReq.Header.Set("Accept", "application/vnd.github+json")

    resp, err := c.client.Do(httpReq)
    if err != nil {
        return fmt.Errorf("github.PostStatus: do: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode/100 != 2 {
        msg, _ := io.ReadAll(resp.Body)
        return fmt.Errorf(
            "github.PostStatus: %d: %s",
            resp.StatusCode, truncate(string(msg), 200))
    }
    return nil
}

/* truncate caps a debug string so an error message does not
 * dump a multi-kilobyte HTML error page into logs.
 */
func truncate(s string, max int) string {
    if len(s) <= max {
        return s
    }
    return s[:max] + "..."
}
