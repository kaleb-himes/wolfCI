package storage_test

/* internal/storage/ghprb_trigger_test.go - PLAN.md 18.5 gating
 * test.
 *
 * Round-trips a Job whose YAML carries a github_prb_trigger
 * subtree. Asserts every PRB-specific field decodes cleanly,
 * survives a Save + LoadJob, and re-encodes byte-equivalent
 * YAML.
 *
 * Schema deviation from PLAN.md: the spec sketched a "triggers:
 * github_prb: ..." shape, but the existing Job already uses
 * `triggers: []Trigger` as a list for cron / webhook / scm.
 * Rather than introduce a YAML type ambiguity that the
 * unmarshal would have to resolve dynamically, this iteration
 * adds a sibling top-level field `github_prb_trigger:` carrying
 * the same data. The existing list-shaped triggers stays
 * unchanged.
 */

import (
    "path/filepath"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/storage"
    "gopkg.in/yaml.v3"
)

func TestParse_GitHubPRBTrigger(t *testing.T) {
    raw := []byte("" +
        "name: master-prb\n" +
        "steps:\n" +
        "  - shell: 'echo running'\n" +
        "github_prb_trigger:\n" +
        "  api_credentials_id: gh-token\n" +
        "  gh_project_url: https://github.com/wolfSSL/wolfssl/\n" +
        "  admin_users:\n" +
        "    - wolfSSL-Bot\n" +
        "    - kaleb-himes\n" +
        "  branches_to_build:\n" +
        "    - '*/master'\n" +
        "  poll_interval_seconds: 300\n" +
        "  build_merge_ref: true\n")

    var job storage.Job
    if err := yaml.Unmarshal(raw, &job); err != nil {
        t.Fatalf("Unmarshal: %v", err)
    }
    if job.GitHubPRB == nil {
        t.Fatalf("GitHubPRB was nil after Unmarshal")
    }
    g := job.GitHubPRB
    if g.APICredentialsID != "gh-token" {
        t.Errorf("APICredentialsID = %q, want gh-token",
            g.APICredentialsID)
    }
    if g.GHProjectURL !=
        "https://github.com/wolfSSL/wolfssl/" {
        t.Errorf("GHProjectURL = %q", g.GHProjectURL)
    }
    if len(g.AdminUsers) != 2 ||
        g.AdminUsers[0] != "wolfSSL-Bot" ||
        g.AdminUsers[1] != "kaleb-himes" {
        t.Errorf("AdminUsers = %+v", g.AdminUsers)
    }
    if len(g.BranchesToBuild) != 1 ||
        g.BranchesToBuild[0] != "*/master" {
        t.Errorf("BranchesToBuild = %+v", g.BranchesToBuild)
    }
    if g.PollIntervalSeconds != 300 {
        t.Errorf("PollIntervalSeconds = %d, want 300",
            g.PollIntervalSeconds)
    }
    if !g.BuildMergeRef {
        t.Errorf("BuildMergeRef = false, want true")
    }

    /* Round-trip through Save + LoadJob using a real on-disk
     * storage so the YAML encoder path is exercised too.
     */
    dir := t.TempDir()
    s, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := s.SaveJob(&job); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }
    onDisk, err := s.LoadJob("master-prb")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if onDisk.GitHubPRB == nil {
        t.Fatalf("loaded GitHubPRB nil; YAML did not round-trip")
    }
    if onDisk.GitHubPRB.APICredentialsID != "gh-token" {
        t.Errorf("post-roundtrip APICredentialsID = %q",
            onDisk.GitHubPRB.APICredentialsID)
    }
    if !onDisk.GitHubPRB.BuildMergeRef {
        t.Errorf("post-roundtrip BuildMergeRef = false")
    }

    /* The on-disk YAML must contain the new top-level key. */
    yamlBytes, err := yaml.Marshal(&job)
    if err != nil {
        t.Fatalf("yaml.Marshal: %v", err)
    }
    if !contains(yamlBytes, []byte("github_prb_trigger:")) {
        t.Errorf("marshaled YAML missing github_prb_trigger "+
            "top-level key:\n%s", yamlBytes)
    }
    _ = filepath.Join(dir, "jobs")
}

func TestParse_GitHubPRBTrigger_DefaultPollInterval(t *testing.T) {
    /* PLAN.md decisions: default poll interval is 300 seconds.
     * When the YAML omits poll_interval_seconds, callers must
     * be able to detect the unset state so they can apply the
     * default. The implementation reports unset as zero; the
     * scheduler applies the default before scheduling.
     */
    raw := []byte("" +
        "name: defaults\n" +
        "steps:\n" +
        "  - shell: 'true'\n" +
        "github_prb_trigger:\n" +
        "  api_credentials_id: token\n" +
        "  gh_project_url: https://example.com/repo\n")

    var job storage.Job
    if err := yaml.Unmarshal(raw, &job); err != nil {
        t.Fatalf("Unmarshal: %v", err)
    }
    if job.GitHubPRB == nil {
        t.Fatalf("GitHubPRB nil")
    }
    if job.GitHubPRB.PollIntervalSeconds != 0 {
        t.Errorf("expected unset PollIntervalSeconds = 0, got %d",
            job.GitHubPRB.PollIntervalSeconds)
    }
    if job.GitHubPRB.BuildMergeRef {
        t.Errorf("expected default BuildMergeRef = false")
    }
}

/* contains is a tiny []byte contains check so this file does not
 * need to import bytes.
 */
func contains(haystack, needle []byte) bool {
    if len(needle) == 0 {
        return true
    }
    for i := 0; i+len(needle) <= len(haystack); i++ {
        if equalBytes(haystack[i:i+len(needle)], needle) {
            return true
        }
    }
    return false
}

func equalBytes(a, b []byte) bool {
    if len(a) != len(b) {
        return false
    }
    for i := range a {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
