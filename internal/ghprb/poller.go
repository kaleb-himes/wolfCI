/* Package ghprb is the wolfCI GitHub Pull Request Builder.
 *
 * The poller queries GitHub's REST v3 /repos/{owner}/{repo}/pulls
 * endpoint on a per-job cadence (default 300s, configured via
 * the per-job triggers.github_prb.poll_interval_seconds Phase
 * 18.5 schema) and emits a TriggerEvent for each open PR whose
 * head_sha the debounce layer (Phase 18.7) has not already
 * fired. The scheduler integration (Phase 18.8) consumes these
 * events and enqueues builds with the ghprb* env vars (Phase
 * 18.9).
 *
 * Phase 18.6 ships only the scaffold: Poll() does one /pulls
 * fetch and returns the parsed event list. Debouncing,
 * scheduling, and env injection land in follow-on tasks.
 */
package ghprb

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
)

/* TriggerEvent is one open-PR signal the poller emits each time
 * GitHub reports the PR. The scheduler enqueues one build per
 * TriggerEvent (after the debounce in Phase 18.7).
 */
type TriggerEvent struct {
    PRID         int
    HeadSHA      string
    Author       string
    TargetBranch string
}

/* Poller queries one GitHub repository's open PR list. Each
 * Poller is bound to a single (owner, repo) and reuses the
 * provided http.Client. The token authenticates every request
 * via the Authorization: Bearer <token> header.
 *
 * baseURL points at the GitHub REST root (https://api.github.com
 * for github.com; an Enterprise install would point at
 * https://github.example.com/api/v3). Tests pass an httptest
 * server's URL.
 */
type Poller struct {
    baseURL string
    token   string
    owner   string
    repo    string
    client  *http.Client
}

/* New constructs a Poller. Passing a nil client defaults to
 * http.DefaultClient; production callers should pass a client
 * whose Transport is the wolfSSL-backed RoundTripper (TBD when
 * Phase 18.10 wires up the commit-status POST path that has the
 * same transport requirement).
 */
func New(baseURL, token, owner, repo string,
    client *http.Client) *Poller {

    if client == nil {
        client = http.DefaultClient
    }
    return &Poller{
        baseURL: strings.TrimRight(baseURL, "/"),
        token:   token,
        owner:   owner,
        repo:    repo,
        client:  client,
    }
}

/* githubPR is the subset of GitHub's PR JSON the poller cares
 * about. Unrelated fields are silently dropped by
 * encoding/json's default Unmarshal behavior.
 */
type githubPR struct {
    Number int    `json:"number"`
    State  string `json:"state"`
    Head   struct {
        SHA string `json:"sha"`
    } `json:"head"`
    User struct {
        Login string `json:"login"`
    } `json:"user"`
    Base struct {
        Ref string `json:"ref"`
    } `json:"base"`
}

/* Poll fetches the open PR list once and returns one
 * TriggerEvent per open PR. Closed/merged PRs are filtered out
 * server-side by the ?state=open query, but the poller also
 * skips any PR whose State is not "open" to defend against a
 * server that ignores the filter.
 *
 * Returns a non-nil error on transport failure, non-200
 * response, or unparseable JSON.
 */
func (p *Poller) Poll(ctx context.Context) ([]TriggerEvent,
    error) {

    url := fmt.Sprintf("%s/repos/%s/%s/pulls?state=open",
        p.baseURL, p.owner, p.repo)
    req, err := http.NewRequestWithContext(
        ctx, http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("ghprb.Poll: new request: %w",
            err)
    }
    if p.token != "" {
        req.Header.Set("Authorization", "Bearer "+p.token)
    }
    req.Header.Set("Accept", "application/vnd.github+json")

    resp, err := p.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("ghprb.Poll: do: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("ghprb.Poll: read body: %w", err)
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf(
            "ghprb.Poll: GitHub returned %d: %s",
            resp.StatusCode, truncate(string(body), 200))
    }

    var prs []githubPR
    if err := json.Unmarshal(body, &prs); err != nil {
        return nil, fmt.Errorf("ghprb.Poll: parse JSON: %w",
            err)
    }
    out := make([]TriggerEvent, 0, len(prs))
    for _, pr := range prs {
        if pr.State != "open" {
            continue
        }
        out = append(out, TriggerEvent{
            PRID:         pr.Number,
            HeadSHA:      pr.Head.SHA,
            Author:       pr.User.Login,
            TargetBranch: pr.Base.Ref,
        })
    }
    return out, nil
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
