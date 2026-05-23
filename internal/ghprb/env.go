package ghprb

/* internal/ghprb/env.go - PLAN.md 18.9 ghprb* env var assembly.
 *
 * EnvForEvent translates a TriggerEvent + the credentials id
 * from the job's GitHubPRB config into the env map a build
 * needs. The key set matches the variables the master-job
 * Jenkinsfile references (PLAN.md 18.9 source list, captured
 * verbatim from
 * third_party/testing/Jenkins/master-job/PRB.Jenkinsfile).
 *
 * Each call generates a fresh random_string; the rest of the
 * keys are deterministic given the input event.
 */

import (
    "encoding/hex"
    "strconv"

    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

/* EnvForEvent returns the env map. credentialsID is the cred
 * store id of the secret-text holding the GitHub API token; it
 * appears verbatim in the ghprbCredentialsId env var so the
 * pipeline withCredentials step can resolve it.
 *
 * random_string is 16 hex chars (8 random bytes) drawn from
 * wolfCrypt's CSPRNG. On a wolfCrypt failure (should be
 * impossible in practice) the key is set to the empty string;
 * we never panic from this path because that would take down
 * the poller mid-emit.
 */
func EnvForEvent(e TriggerEvent, credentialsID string) map[string]string {
    rs := ""
    if b, err := wolfcrypt.RandBytes(8); err == nil {
        rs = hex.EncodeToString(b)
    }

    triggerLogin := e.TriggerAuthorLogin
    if triggerLogin == "" {
        triggerLogin = e.Author
    }

    return map[string]string{
        "ghprbActualCommit":              e.HeadSHA,
        "ghprbActualCommitAuthor":        e.CommitAuthor,
        "ghprbActualCommitAuthorEmail":   e.CommitAuthorEmail,
        "ghprbAuthorRepoGitUrl":          e.AuthorRepoGitURL,
        "ghprbCommentBody":               e.CommentBody,
        "ghprbCredentialsId":             credentialsID,
        "ghprbGhRepository":              e.GHRepository,
        "ghprbPullAuthorEmail":           e.AuthorEmail,
        "ghprbPullAuthorLogin":           e.Author,
        "ghprbPullAuthorLoginMention":    mention(e.Author),
        "ghprbPullDescription":           e.PullDescription,
        "ghprbPullId":                    strconv.Itoa(e.PRID),
        "ghprbPullLink":                  e.PullLink,
        "ghprbPullLongDescription":       e.PullLongDescription,
        "ghprbPullTitle":                 e.PullTitle,
        "ghprbSourceBranch":              e.SourceBranch,
        "ghprbTargetBranch":              e.TargetBranch,
        "ghprbTriggerAuthor":             e.TriggerAuthor,
        "ghprbTriggerAuthorEmail":        e.TriggerAuthorEmail,
        "ghprbTriggerAuthorLogin":        triggerLogin,
        "ghprbTriggerAuthorLoginMention": mention(triggerLogin),
        "sha1":                           e.HeadSHA,
        "random_string":                  rs,
    }
}

/* mention returns "@" + login, or empty string when login is
 * empty. The Jenkins ghprb plugin uses the mention form in PR
 * comment templates so build notifications ping the right user.
 */
func mention(login string) string {
    if login == "" {
        return ""
    }
    return "@" + login
}
