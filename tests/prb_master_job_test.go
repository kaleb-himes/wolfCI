package agente2e_test

/* tests/prb_master_job_test.go - PLAN.md 18.30 end-to-end
 * gating test.
 *
 * Spins up:
 *
 *   - a fake GitHub API server (httptest.Server) that
 *     responds to /repos/<o>/<r>/pulls with one open PR and
 *     records every POST to /statuses/<sha>.
 *   - a local git fixture repo containing a simplified
 *     master-job Jenkinsfile (parallel-builds-three-children
 *     plus a trailing `sh curl` that POSTs the master status
 *     to the fake GitHub server) under
 *     Jenkins/master-job/PRB.Jenkinsfile, plus a stub
 *     jenkinsUtils.groovy.
 *   - a wolfCI storage.Storage with three child jobs
 *     ("Group1", "Group2", "Group3"), each running a single
 *     shell step that echoes its name to a per-build marker
 *     file.
 *   - a real scheduler.Scheduler wired to a real
 *     scheduler.LocalExecutor.
 *   - a test-local schedulerDispatcher adapter that maps
 *     pipeline.BuildDispatcher.EnqueueChildBuild calls onto
 *     the scheduler.
 *
 * The test drives the chain end to end:
 *
 *   1. ghprb.Poller.Poll() against the fake GitHub server
 *      returns one TriggerEvent.
 *   2. ghprb.EnvForEvent produces the ghprb* env map; the
 *      test layers in GH_STATUS_BASE pointing at the fake
 *      GitHub server so the Jenkinsfile's trailing
 *      `sh "curl ..."` posts back into the test's recorder.
 *   3. internal/jobspec.RunFromSCM clones the local git
 *      fixture and runs the master Jenkinsfile through
 *      pipeline.ExecDeclarative.
 *   4. The Jenkinsfile fans out via parallel { build job:
 *      'Group<N>' } to the three child jobs.
 *   5. The trailing sh step posts a JSON status to
 *      <fake>/repos/<owner>/<repo>/statuses/<sha> with
 *      context "PRB-master-job" and state "success".
 *
 * Assertions: poll observed exactly one event, each of the
 * three children ran exactly once with the ghprb env in
 * place (their per-child marker files carry the ghprbPullId
 * + their own name), the fake GitHub server received a
 * POST whose body contains the expected context + state.
 */

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/ghprb"
    "github.com/kaleb-himes/wolfCI/internal/jobspec"
    "github.com/kaleb-himes/wolfCI/internal/pipeline"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestPRB_MasterJobFanOut(t *testing.T) {
    /* ----- (1) Fake GitHub API server ------------------- */
    var (
        statusMu      sync.Mutex
        statusBodies  []string
        statusHeaders []http.Header
    )
    const (
        owner   = "wolfssl"
        repo    = "wolfssl"
        prSHA   = "abc123def4567890abc123def4567890abc123de"
        prID    = 42
        prLogin = "alice"
    )
    gh := httptest.NewServer(http.HandlerFunc(
        func(w http.ResponseWriter, r *http.Request) {
            switch {
            case r.Method == http.MethodGet &&
                strings.HasSuffix(r.URL.Path,
                    "/repos/"+owner+"/"+repo+"/pulls"):
                w.Header().Set("Content-Type",
                    "application/json")
                fmt.Fprintf(w, `[{
                    "number": %d, "state": "open",
                    "head": {"sha": %q},
                    "user": {"login": %q},
                    "base": {"ref": "master"}
                }]`, prID, prSHA, prLogin)
            case r.Method == http.MethodPost &&
                strings.Contains(r.URL.Path,
                    "/repos/"+owner+"/"+repo+"/statuses/"):
                body, _ := io.ReadAll(r.Body)
                statusMu.Lock()
                statusBodies = append(statusBodies,
                    string(body))
                statusHeaders = append(statusHeaders,
                    r.Header.Clone())
                statusMu.Unlock()
                w.WriteHeader(http.StatusCreated)
                fmt.Fprintln(w, `{"id": 1}`)
            default:
                http.NotFound(w, r)
            }
        }))
    defer gh.Close()

    /* ----- (2) Local git fixture ------------------------ */
    gitDir := t.TempDir()
    jenkinsfile := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('Run') {
            steps {
                script {
                    def jobs = ['Group1', 'Group2', 'Group3']
                    def tests = [:]
                    jobs.each { j ->
                        def name = j
                        tests[name] = {
                            def res = build(job: name, propagate: false)
                            echo "Child " + name + " result: " + res.getResult()
                        }
                    }
                    parallel tests
                    def sha = env.ghprbActualCommit
                    def repoStr = env.ghprbGhRepository
                    def base = env.GH_STATUS_BASE
                    def url = base + "/repos/" + repoStr + "/statuses/" + sha
                    def payload = '{"state":"success","context":"PRB-master-job"}'
                    sh "curl -fsS -X POST -H 'Content-Type: application/json' '" + url + "' --data-raw '" + payload + "'"
                }
            }
        }
    }
}`)
    jenkinsUtilsStub := []byte(`def cleanupName(name) { return name }
return this
`)
    seedGitFixture(t, gitDir, map[string][]byte{
        "Jenkins/master-job/PRB.Jenkinsfile":           jenkinsfile,
        "Jenkins/jenkins-functions/jenkinsUtils.groovy": jenkinsUtilsStub,
    })

    /* ----- (3) wolfCI storage + child jobs -------------- */
    workspace := t.TempDir()
    storeRoot := t.TempDir()
    st, err := storage.New(storeRoot)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    childNames := []string{"Group1", "Group2", "Group3"}
    for _, name := range childNames {
        cap := name
        if err := st.SaveJob(&storage.Job{
            Name: cap,
            Steps: []storage.Step{{
                Shell: `echo "child=` + cap +
                    ` pr=$ghprbPullId" > marker.txt`,
            }},
        }); err != nil {
            t.Fatalf("SaveJob %s: %v", cap, err)
        }
    }

    /* ----- (4) Scheduler ------------------------------- */
    sched := scheduler.New(st,
        scheduler.NewLocalExecutor(st))
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sched.Start(ctx)
    defer sched.Stop()

    /* ----- (5) Pipeline BuildDispatcher adapter --------- */
    /* dispatcher gets its env set from the ghprb event
     * after the poll; each child build inherits these so
     * the child's $ghprbPullId / $ghprbActualCommit
     * expansions resolve to the PR's values. */
    dispatcher := &prbDispatcher{store: st, sched: sched}

    /* ----- (6) Poll the fake GitHub server -------------- */
    poller := ghprb.New(gh.URL,
        "fake-token", owner, repo, http.DefaultClient)
    events, err := poller.Poll(ctx)
    if err != nil {
        t.Fatalf("Poll: %v", err)
    }
    if len(events) != 1 {
        t.Fatalf("poller returned %d events, want 1",
            len(events))
    }
    event := events[0]
    if event.PRID != prID {
        t.Errorf("event PRID = %d, want %d",
            event.PRID, prID)
    }
    if event.HeadSHA != prSHA {
        t.Errorf("event HeadSHA = %q, want %q",
            event.HeadSHA, prSHA)
    }
    /* The poller only populates a subset of TriggerEvent;
     * the test fills in GHRepository so the
     * `${env.ghprbGhRepository}` template expansion in the
     * Jenkinsfile resolves to "owner/repo" - matching what
     * Jenkins itself emits for the variable. */
    event.GHRepository = owner + "/" + repo

    /* ----- (7) Build the env layer + run RunFromSCM ----- */
    envMap := ghprb.EnvForEvent(event,
        "fake-api-credentials")
    /* Inject the fake GitHub URL so the Jenkinsfile's
     * trailing curl posts into the test's recorder. */
    envMap["GH_STATUS_BASE"] = gh.URL
    /* Propagate the same env onto each child build through
     * the dispatcher so the children's shell steps can
     * read $ghprbPullId / $ghprbActualCommit / etc. */
    dispatcher.childEnv = envMap

    spec := &jobspec.JobSpec{
        Name: "master-job",
        Pipeline: &jobspec.PipelineBlock{
            Definition: jobspec.DefinitionFromSCM,
            SCM: &jobspec.SCMConfig{
                RepoURL:         "file://" + gitDir,
                CredentialsID:   "fake-api-credentials",
                BranchSpecifier: "*/master",
                ScriptPath:      "Jenkins/master-job/PRB.Jenkinsfile",
                LightweightCheckout: false,
            },
        },
    }
    exe := &pipeline.LocalExecutor{
        Workspace:  workspace,
        Dispatcher: dispatcher,
        InitialEnv: envMap,
    }
    build, err := jobspec.RunFromSCM(
        ctx, spec, workspace, exe)
    if err != nil {
        t.Fatalf("RunFromSCM: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        var out strings.Builder
        for _, stage := range build.Stages {
            for _, step := range stage.Steps {
                out.WriteString(step.Output)
                out.WriteString("\n")
            }
        }
        t.Fatalf("master build.Status = %v, want "+
            "BuildSuccess; step output:\n%s",
            build.Status, out.String())
    }

    /* ----- (8) Assertions ------------------------------ */

    /* Each child ran exactly once. The marker.txt path
     * inside each child's per-build workspace contains its
     * own name + the PR id from the inherited env. */
    dispatcher.assertChildRan(t, "Group1", prID)
    dispatcher.assertChildRan(t, "Group2", prID)
    dispatcher.assertChildRan(t, "Group3", prID)

    /* The fake GitHub server received exactly one status
     * POST with state=success and context=PRB-master-job. */
    statusMu.Lock()
    defer statusMu.Unlock()
    if len(statusBodies) != 1 {
        t.Fatalf("fake GitHub received %d status POSTs, "+
            "want 1; bodies=%v",
            len(statusBodies), statusBodies)
    }
    body := statusBodies[0]
    if !strings.Contains(body, `"state":"success"`) {
        t.Errorf("status body missing success state: %s",
            body)
    }
    if !strings.Contains(body,
        `"context":"PRB-master-job"`) {
        t.Errorf("status body missing PRB-master-job "+
            "context: %s", body)
    }
}

/* prbDispatcher is the test-local BuildDispatcher that
 * enqueues children through the real scheduler and records
 * the per-child build number so the assertions can read
 * each child's marker.txt. */
type prbDispatcher struct {
    store *storage.Storage
    sched *scheduler.Scheduler

    /* childEnv is layered onto every dispatched job's Env
     * (via a job clone) so the child shell steps see the
     * ghprb* / GH_STATUS_BASE vars the parent build saw. */
    childEnv map[string]string

    mu   sync.Mutex
    dispatched map[string]int /* name -> last build number */
}

func (d *prbDispatcher) EnqueueChildBuild(
    ctx context.Context, jobName string,
    _ []pipeline.BuildParam) (
    <-chan pipeline.ChildBuildResult, error) {

    job, err := d.store.LoadJob(jobName)
    if err != nil {
        return nil, fmt.Errorf(
            "prbDispatcher: lookup %q: %w", jobName, err)
    }
    /* Clone the job and overlay childEnv onto its Env so the
     * shared on-disk spec is not mutated and the next build
     * starts from the same baseline. */
    clone := *job
    if d.childEnv != nil {
        clone.Env = map[string]string{}
        for k, v := range d.childEnv {
            clone.Env[k] = v
        }
    }
    num, done, err := d.sched.Enqueue(&clone)
    if err != nil {
        return nil, fmt.Errorf(
            "prbDispatcher: enqueue %q: %w", jobName, err)
    }
    d.mu.Lock()
    if d.dispatched == nil {
        d.dispatched = map[string]int{}
    }
    d.dispatched[jobName] = num
    d.mu.Unlock()

    out := make(chan pipeline.ChildBuildResult, 1)
    go func() {
        defer close(out)
        select {
        case r := <-done:
            out <- pipeline.ChildBuildResult{
                Job:    r.JobName,
                Number: r.Number,
                Status: prbStatusToJenkins(r.Status),
            }
        case <-ctx.Done():
            out <- pipeline.ChildBuildResult{
                Job: jobName, Status: "ABORTED",
            }
        case <-time.After(15 * time.Second):
            out <- pipeline.ChildBuildResult{
                Job: jobName, Status: "ABORTED",
            }
        }
    }()
    return out, nil
}

func (d *prbDispatcher) assertChildRan(t *testing.T,
    name string, expectedPRID int) {
    t.Helper()
    d.mu.Lock()
    num, ok := d.dispatched[name]
    d.mu.Unlock()
    if !ok {
        t.Errorf("child %q was not dispatched", name)
        return
    }
    /* Each child's step writes marker.txt into its build
     * workspace at builds/<name>/<num>/workspace/marker.txt.
     * The marker should carry the child's own name AND the
     * PR id we passed through ghprb env injection - proves
     * both the dispatch AND the env-var propagation
     * landed end to end. */
    markerPath := filepath.Join(d.store.Root(), "builds",
        name, fmt.Sprintf("%d", num), "workspace",
        "marker.txt")
    data, err := os.ReadFile(markerPath)
    if err != nil {
        t.Errorf("child %q marker: %v", name, err)
        return
    }
    line := strings.TrimSpace(string(data))
    if !strings.Contains(line, "child="+name) {
        t.Errorf("child %q marker = %q, missing child=%s",
            name, line, name)
    }
    if !strings.Contains(line,
        fmt.Sprintf("pr=%d", expectedPRID)) {
        t.Errorf("child %q marker = %q, missing pr=%d "+
            "(ghprb env did not propagate)",
            name, line, expectedPRID)
    }
}

func prbStatusToJenkins(s scheduler.Status) string {
    switch s {
    case scheduler.StatusSuccess:
        return "SUCCESS"
    case scheduler.StatusFailure:
        return "FAILURE"
    case scheduler.StatusCancelled:
        return "ABORTED"
    }
    return "FAILURE"
}

/* seedGitFixture inits a tiny git repo at dir, writes each
 * (path -> content) entry into dir, commits, and leaves
 * master as the only branch. Used to build the local
 * file:// repo the RunFromSCM cloner reads from. */
func seedGitFixture(t *testing.T, dir string,
    files map[string][]byte) {
    t.Helper()
    runGit := func(args ...string) {
        cmd := exec.Command("git", args...)
        cmd.Dir = dir
        cmd.Env = append(os.Environ(),
            "GIT_AUTHOR_NAME=wolfci-test",
            "GIT_AUTHOR_EMAIL=test@wolfci.local",
            "GIT_COMMITTER_NAME=wolfci-test",
            "GIT_COMMITTER_EMAIL=test@wolfci.local",
        )
        out, err := cmd.CombinedOutput()
        if err != nil {
            t.Fatalf("git %s: %v\n%s",
                strings.Join(args, " "), err, out)
        }
    }
    runGit("init", "-b", "master")
    for rel, content := range files {
        full := filepath.Join(dir, rel)
        if err := os.MkdirAll(filepath.Dir(full),
            0o755); err != nil {
            t.Fatalf("mkdir %s: %v", full, err)
        }
        if err := os.WriteFile(full, content,
            0o644); err != nil {
            t.Fatalf("write %s: %v", full, err)
        }
    }
    runGit("add", ".")
    runGit("commit", "-m", "wolfci PRB fixture")
}

/* Compile-time assertion that prbDispatcher satisfies
 * pipeline.BuildDispatcher. Caught at build time so a future
 * interface tweak surfaces immediately. */
var _ pipeline.BuildDispatcher = (*prbDispatcher)(nil)
var _ = json.Marshal
