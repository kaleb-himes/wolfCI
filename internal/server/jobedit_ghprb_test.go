package server_test

/* internal/server/jobedit_ghprb_test.go - PLAN.md 18.29
 * gating tests.
 *
 * TestJobEdit_FormViewHasGHPRBSection is the spec-named gate:
 * GET /jobs/<name>/edit?view=form must include inputs for
 *
 *   api_credentials_id  (<select>)
 *   gh_project_url      (<input>)
 *   admin_users         (<textarea>)
 *   branches_to_build   (<textarea>)
 *   poll_interval_seconds (<input type="number">, default 300)
 *
 * plus the Pipeline-from-SCM panel:
 *
 *   repo_url
 *   credentials_id      (<select>)
 *   branch_specifier
 *   script_path
 *   lightweight_checkout (<input type="checkbox">)
 *
 * The credentials selects are populated from the wired
 * credstore. The test stands up a credstore with two
 * entries (an API token and an SSH key) and asserts both
 * appear in the rendered selects.
 *
 * TestJobEdit_FormViewGHPRBRoundtrip POSTs the form with
 * each GHPRB field set and asserts storage.Job carries the
 * matching GitHubPRBTrigger values; the SCM panel
 * round-trips the same way through storage.Job.Pipeline.
 */

import (
    "encoding/json"
    "net/http"
    "net/http/cookiejar"
    "net/http/httptest"
    "net/url"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/credstore"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobEdit_FormViewHasGHPRBSection(t *testing.T) {
    ts, jar := newAuthedUIWithCredstore(t, []credEntry{
        {ID: "wolfssl-bot-api-token",
            Type: credstore.TypeSecretText,
            Payload: secretTextJSON("ghp_xxxxxxxxxxxx")},
        {ID: "wolfssl-bot-ssh-key",
            Type: credstore.TypeSshPrivateKey,
            Payload: sshKeyJSON("-----BEGIN OPENSSH " +
                "PRIVATE KEY-----\n...")},
    })
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    /* Create a placeholder job so the edit form has
     * something to render against. */
    create := url.Values{
        "view":       {"form"},
        "name":       {"ghprb-job"},
        "steps_yaml": {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", create)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("create POST status = %d, want 303; "+
            "body:\n%s", resp.StatusCode, body)
    }
    resp = mustGet(t, client,
        ts.URL+"/jobs/ghprb-job/edit?view=form")
    body := readBody(t, resp)
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET edit form status = %d, want 200",
            resp.StatusCode)
    }
    /* GHPRB inputs. */
    for _, want := range []string{
        `name="api_credentials_id"`,
        `name="gh_project_url"`,
        `name="admin_users"`,
        `name="branches_to_build"`,
        `name="poll_interval_seconds"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form body missing GHPRB input %q",
                want)
        }
    }
    /* The api_credentials_id select must be populated from
     * credstore. */
    if !strings.Contains(body,
        `value="wolfssl-bot-api-token"`) {
        t.Errorf("api_credentials_id <select> missing the " +
            "wolfssl-bot-api-token option")
    }
    if !strings.Contains(body,
        `value="wolfssl-bot-ssh-key"`) {
        t.Errorf("credentials selects missing the " +
            "wolfssl-bot-ssh-key option")
    }
    /* Default poll interval 300 must appear pre-filled. */
    if !strings.Contains(body,
        `name="poll_interval_seconds"`) ||
        !strings.Contains(body, `value="300"`) {
        t.Errorf("poll_interval_seconds default not " +
            "pre-filled to 300")
    }
    /* SCM panel inputs. */
    for _, want := range []string{
        `name="repo_url"`,
        `name="credentials_id"`,
        `name="branch_specifier"`,
        `name="script_path"`,
        `name="lightweight_checkout"`,
        `type="checkbox"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form body missing SCM panel input %q",
                want)
        }
    }
}

func TestJobEdit_FormViewGHPRBRoundtrip(t *testing.T) {
    ts, jar := newAuthedUIWithCredstore(t, []credEntry{
        {ID: "api-cred",
            Type: credstore.TypeSecretText,
            Payload: secretTextJSON("token")},
    })
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "view":                  {"form"},
        "name":                  {"prb-roundtrip"},
        "api_credentials_id":    {"api-cred"},
        "gh_project_url":        {"https://github.com/wolfssl/wolfssl/"},
        "admin_users":           {"alice\nbob"},
        "branches_to_build":     {"*/master\n*/release-*"},
        "poll_interval_seconds": {"120"},
        "repo_url":              {"git@github.com:wolfssl/wolfssl.git"},
        "credentials_id":        {"api-cred"},
        "branch_specifier":      {"*/master"},
        "script_path":           {"Jenkins/master-job/PRB.Jenkinsfile"},
        "lightweight_checkout":  {"on"},
        "steps_yaml":            {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 303; body:\n%s",
            resp.StatusCode, body)
    }
    st := storageFromServer(ts)
    job, err := st.LoadJob("prb-roundtrip")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if job.GitHubPRB == nil {
        t.Fatal("GitHubPRB is nil; form fields did not parse")
    }
    if job.GitHubPRB.APICredentialsID != "api-cred" {
        t.Errorf("APICredentialsID = %q, want api-cred",
            job.GitHubPRB.APICredentialsID)
    }
    if job.GitHubPRB.GHProjectURL !=
        "https://github.com/wolfssl/wolfssl/" {
        t.Errorf("GHProjectURL = %q",
            job.GitHubPRB.GHProjectURL)
    }
    if len(job.GitHubPRB.AdminUsers) != 2 ||
        job.GitHubPRB.AdminUsers[0] != "alice" ||
        job.GitHubPRB.AdminUsers[1] != "bob" {
        t.Errorf("AdminUsers = %v, want [alice bob]",
            job.GitHubPRB.AdminUsers)
    }
    if len(job.GitHubPRB.BranchesToBuild) != 2 {
        t.Errorf("BranchesToBuild = %v, want 2 entries",
            job.GitHubPRB.BranchesToBuild)
    }
    if job.GitHubPRB.PollIntervalSeconds != 120 {
        t.Errorf("PollIntervalSeconds = %d, want 120",
            job.GitHubPRB.PollIntervalSeconds)
    }
    if job.Pipeline == nil || job.Pipeline.SCM == nil {
        t.Fatal("Pipeline / SCM is nil; SCM form fields " +
            "did not parse")
    }
    if job.Pipeline.Definition != "from_scm" {
        t.Errorf("Pipeline.Definition = %q, want from_scm",
            job.Pipeline.Definition)
    }
    if job.Pipeline.SCM.RepoURL !=
        "git@github.com:wolfssl/wolfssl.git" {
        t.Errorf("SCM.RepoURL = %q",
            job.Pipeline.SCM.RepoURL)
    }
    if job.Pipeline.SCM.CredentialsID != "api-cred" {
        t.Errorf("SCM.CredentialsID = %q",
            job.Pipeline.SCM.CredentialsID)
    }
    if job.Pipeline.SCM.BranchSpecifier != "*/master" {
        t.Errorf("SCM.BranchSpecifier = %q",
            job.Pipeline.SCM.BranchSpecifier)
    }
    if job.Pipeline.SCM.ScriptPath !=
        "Jenkins/master-job/PRB.Jenkinsfile" {
        t.Errorf("SCM.ScriptPath = %q",
            job.Pipeline.SCM.ScriptPath)
    }
    if !job.Pipeline.SCM.LightweightCheckout {
        t.Errorf("LightweightCheckout = false, want true")
    }
}

/* credEntry / newAuthedUIWithCredstore / secretTextJSON /
 * sshKeyJSON are local test helpers that stand up a server
 * with a populated credstore. The existing newAuthedUI helper
 * does not wire a Credstore; the 18.29 tests need one, but
 * other server tests do not, so the helper lives here rather
 * than in the shared test scaffolding. */
type credEntry struct {
    ID      string
    Type    credstore.CredType
    Payload []byte
}

func newAuthedUIWithCredstore(t *testing.T,
    entries []credEntry) (*httptest.Server, http.CookieJar) {
    t.Helper()
    dir := t.TempDir()
    st, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* Master secret can be any non-empty bytes; the gating
     * test does not verify the seal, only that the store
     * lists each entry's ID through the form's <select>. */
    cs, err := credstore.NewStore(
        filepath.Join(dir, "credstore"),
        []byte("0123456789abcdef0123456789abcdef"))
    if err != nil {
        t.Fatalf("credstore.NewStore: %v", err)
    }
    for _, e := range entries {
        rec := &credstore.Record{
            Type:      e.Type,
            Payload:   e.Payload,
            CreatedAt: time.Now(),
        }
        if err := cs.Add(e.ID, rec); err != nil {
            t.Fatalf("credstore.Add %q: %v", e.ID, err)
        }
    }
    authCfg := &auth.Config{PasswordEnabled: true,
        PBKDF2Iterations: 1000}
    pwStore := auth.NewPasswordStore(
        filepath.Join(dir, "passwords"), authCfg)
    if err := pwStore.SetPassword("alice",
        "hunter2"); err != nil {
        t.Fatalf("SetPassword: %v", err)
    }
    sessions := server.NewSessionStore(
        filepath.Join(dir, "sessions"), time.Hour)
    srv := server.New(server.Options{
        Storage:      st,
        Auth:         authCfg,
        Passwords:    pwStore,
        Sessions:     sessions,
        Credstore:    cs,
        CookieSecure: false,
    })
    ts := httptest.NewServer(srv)
    testStorageByURL[ts.URL] = st
    jar, _ := cookiejar.New(nil)
    client := &http.Client{Jar: jar,
        CheckRedirect: func(*http.Request,
            []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    resp, err := client.PostForm(ts.URL+"/login",
        url.Values{
            "username": {"alice"},
            "password": {"hunter2"},
        })
    if err != nil {
        ts.Close()
        t.Fatalf("login: %v", err)
    }
    resp.Body.Close()
    if resp.StatusCode != http.StatusSeeOther {
        ts.Close()
        t.Fatalf("login status = %d, want 303",
            resp.StatusCode)
    }
    return ts, jar
}

func secretTextJSON(secret string) []byte {
    b, _ := json.Marshal(
        credstore.SecretTextPayload{Secret: secret})
    return b
}

func sshKeyJSON(pem string) []byte {
    b, _ := json.Marshal(
        credstore.SshPrivateKeyPayload{PrivateKey: pem})
    return b
}

