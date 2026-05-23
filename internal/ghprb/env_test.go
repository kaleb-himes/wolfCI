package ghprb_test

/* internal/ghprb/env_test.go - PLAN.md 18.9 gating test.
 *
 * TestBuild_GHPRBEnvVars exhaustively asserts that every
 * ghprb* env var the master-job Jenkinsfile references is
 * populated by EnvForEvent. Values come from a fully-populated
 * TriggerEvent fixture so the assertion can pin both the keys
 * and the values.
 *
 * Source list (PLAN.md 18.9, mirroring
 * third_party/testing/Jenkins/master-job/PRB.Jenkinsfile):
 *   ghprbActualCommit, ghprbActualCommitAuthor,
 *   ghprbActualCommitAuthorEmail, ghprbAuthorRepoGitUrl,
 *   ghprbCommentBody, ghprbCredentialsId,
 *   ghprbGhRepository, ghprbPullAuthorEmail,
 *   ghprbPullAuthorLogin, ghprbPullAuthorLoginMention,
 *   ghprbPullDescription, ghprbPullId, ghprbPullLink,
 *   ghprbPullLongDescription, ghprbPullTitle,
 *   ghprbSourceBranch, ghprbTargetBranch,
 *   ghprbTriggerAuthor, ghprbTriggerAuthorEmail,
 *   ghprbTriggerAuthorLogin, ghprbTriggerAuthorLoginMention,
 *   sha1, random_string.
 */

import (
    "strconv"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
)

func TestBuild_GHPRBEnvVars(t *testing.T) {
    event := ghprb.TriggerEvent{
        PRID:                4242,
        HeadSHA:             "deadbeefcafe1234",
        Author:              "octocat",
        AuthorEmail:         "octo@example.com",
        SourceBranch:        "feature/x",
        TargetBranch:        "master",
        AuthorRepoGitURL:    "git@github.com:octocat/wolfssl.git",
        PullTitle:           "Add foo",
        PullDescription:     "First line of body",
        PullLongDescription: "First line of body\n\nMore detail.",
        PullLink:            "https://github.com/wolfSSL/wolfssl/pull/4242",
        GHRepository:        "wolfSSL/wolfssl",
        CommentBody:         "ok to test",
        CommitAuthor:        "Octo Cat",
        CommitAuthorEmail:   "octo@example.com",
        TriggerAuthor:       "kaleb-himes",
        TriggerAuthorLogin:  "kaleb-himes",
        TriggerAuthorEmail:  "kaleb@wolfssl.com",
    }
    env := ghprb.EnvForEvent(event, "gh-token-id")

    wantValues := map[string]string{
        "ghprbActualCommit":              "deadbeefcafe1234",
        "ghprbActualCommitAuthor":        "Octo Cat",
        "ghprbActualCommitAuthorEmail":   "octo@example.com",
        "ghprbAuthorRepoGitUrl":          "git@github.com:octocat/wolfssl.git",
        "ghprbCommentBody":               "ok to test",
        "ghprbCredentialsId":             "gh-token-id",
        "ghprbGhRepository":              "wolfSSL/wolfssl",
        "ghprbPullAuthorEmail":           "octo@example.com",
        "ghprbPullAuthorLogin":           "octocat",
        "ghprbPullAuthorLoginMention":    "@octocat",
        "ghprbPullDescription":           "First line of body",
        "ghprbPullId":                    "4242",
        "ghprbPullLink":                  "https://github.com/wolfSSL/wolfssl/pull/4242",
        "ghprbPullLongDescription": "First line of body\n\nMore detail.",
        "ghprbPullTitle":                 "Add foo",
        "ghprbSourceBranch":              "feature/x",
        "ghprbTargetBranch":              "master",
        "ghprbTriggerAuthor":             "kaleb-himes",
        "ghprbTriggerAuthorEmail":        "kaleb@wolfssl.com",
        "ghprbTriggerAuthorLogin":        "kaleb-himes",
        "ghprbTriggerAuthorLoginMention": "@kaleb-himes",
        "sha1":                           "deadbeefcafe1234",
    }
    for k, want := range wantValues {
        got, ok := env[k]
        if !ok {
            t.Errorf("env missing key %q", k)
            continue
        }
        if got != want {
            t.Errorf("env[%q] = %q, want %q", k, got, want)
        }
    }
    /* random_string is non-deterministic; just require it to be
     * present and non-empty. The 8-byte hex form gives 16 ASCII
     * chars; assert at least one char so an empty default is
     * still rejected.
     */
    rs, ok := env["random_string"]
    if !ok || rs == "" {
        t.Errorf("env[random_string] = %q, want non-empty", rs)
    }

    /* Two calls produce different random_string values. */
    env2 := ghprb.EnvForEvent(event, "gh-token-id")
    if env2["random_string"] == rs {
        t.Errorf("random_string not fresh per call: both = %q",
            rs)
    }
}

/* TestEnvForEvent_PullID stringifies the int PR id correctly. */
func TestEnvForEvent_PullID(t *testing.T) {
    env := ghprb.EnvForEvent(
        ghprb.TriggerEvent{PRID: 17}, "cid")
    if env["ghprbPullId"] != strconv.Itoa(17) {
        t.Errorf("ghprbPullId = %q, want 17",
            env["ghprbPullId"])
    }
}
