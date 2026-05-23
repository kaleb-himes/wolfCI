package server_test

/* internal/server/jobedit_buildenv_test.go - PLAN.md 18.28
 * gating tests.
 *
 * TestJobEdit_BuildEnvRoundtrip POSTs a job-create form with
 * the three Jenkins-aligned build-environment toggles
 * (prepare_environment_for_run,
 * keep_jenkins_environment_variables,
 * keep_jenkins_build_variables) all set, asserts the
 * resulting storage.Job carries a BuildEnv block with each
 * field true, and asserts the marshalled YAML emits the
 * matching keys under build_env.
 *
 * TestJobEdit_BuildEnv_BuildVarsInheritFromPriorBuild
 * exercises the runtime half: it stands up a real
 * scheduler.Scheduler against an on-disk storage layout, seeds
 * builds/<job>/1/exported-env.json with a sentinel env entry
 * (simulating a successful prior build), then enqueues a
 * fresh build of a job with KeepJenkinsBuildVars=true and a
 * step that echoes the inherited variable to a marker file
 * inside the build's workspace. The assertion: the marker
 * file contains the sentinel value, proving the executor
 * actually read the prior build's exported env.
 */

import (
    "context"
    "encoding/json"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestJobEdit_BuildEnvRoundtrip(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "view":                                {"form"},
        "name":                                {"buildenv-job"},
        "prepare_environment_for_run":         {"on"},
        "keep_jenkins_environment_variables":  {"on"},
        "keep_jenkins_build_variables":        {"on"},
        "steps_yaml":                          {"- shell: 'true'\n"},
    }
    resp := mustPostForm(t, client, ts.URL+"/jobs/new", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 303; body:\n%s",
            resp.StatusCode, body)
    }
    st := storageFromServer(ts)
    job, err := st.LoadJob("buildenv-job")
    if err != nil {
        t.Fatalf("LoadJob: %v", err)
    }
    if job.BuildEnv == nil {
        t.Fatalf("BuildEnv is nil; expected populated block")
    }
    if !job.BuildEnv.PrepareEnvForRun {
        t.Errorf("PrepareEnvForRun = false, want true")
    }
    if !job.BuildEnv.KeepJenkinsEnvVars {
        t.Errorf("KeepJenkinsEnvVars = false, want true")
    }
    if !job.BuildEnv.KeepJenkinsBuildVars {
        t.Errorf("KeepJenkinsBuildVars = false, want true")
    }
    resp = mustGet(t, client,
        ts.URL+"/jobs/buildenv-job/edit?view=raw")
    body := readBody(t, resp)
    for _, want := range []string{
        "build_env:",
        "prepare_environment_for_run: true",
        "keep_jenkins_environment_variables: true",
        "keep_jenkins_build_variables: true",
    } {
        if !strings.Contains(body, want) {
            t.Errorf("edit raw view missing %q\nbody:\n%s",
                want, body)
        }
    }
}

func TestJobEdit_BuildEnv_BuildVarsInheritFromPriorBuild(t *testing.T) {
    root := t.TempDir()
    st, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* Seed a "prior successful build" (#1) with an
     * exported-env.json that 18.28's
     * KeepJenkinsBuildVars=true path is supposed to read. */
    priorDir := filepath.Join(root, "builds", "envjob", "1")
    if err := os.MkdirAll(priorDir, 0o755); err != nil {
        t.Fatalf("mkdir prior: %v", err)
    }
    exported := map[string]string{
        "WOLFCI_FROM_PRIOR": "from-build-1",
    }
    exportedBytes, _ := json.Marshal(exported)
    if err := os.WriteFile(
        filepath.Join(priorDir, "exported-env.json"),
        exportedBytes, 0o644); err != nil {
        t.Fatalf("write exported-env.json: %v", err)
    }
    /* Advance the per-job build counter so the next Enqueue
     * lands as build #2 (not #1, which would collide with
     * the seeded prior). The counter file's stored value is
     * "the last issued number"; the scheduler increments it
     * before returning. So write 1 - the next Enqueue reads
     * 1, increments to 2, returns 2. */
    nbnDir := filepath.Join(root, "builds", "envjob")
    if err := os.WriteFile(
        filepath.Join(nbnDir, ".next-build-number"),
        []byte("1\n"), 0o644); err != nil {
        t.Fatalf("write counter: %v", err)
    }

    /* Job runs one step that echoes the inherited env var
     * into a marker file under the build's workspace. */
    job := &storage.Job{
        Name: "envjob",
        BuildEnv: &storage.BuildEnv{
            KeepJenkinsBuildVars: true,
            KeepJenkinsEnvVars:   true,
        },
        Steps: []storage.Step{{
            Shell: `printf '%s' "$WOLFCI_FROM_PRIOR" > marker.txt`,
        }},
    }
    if err := st.SaveJob(job); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    exec := &scheduler.LocalExecutor{}
    /* The scheduler-package symbol for the local executor
     * needs a store reference; the production constructor
     * NewLocalExecutor sets it. */
    exec = scheduler.NewLocalExecutor(st)
    sched := scheduler.New(st, exec)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sched.Start(ctx)
    defer sched.Stop()

    num, done, err := sched.Enqueue(job)
    if err != nil {
        t.Fatalf("Enqueue: %v", err)
    }
    if num != 2 {
        t.Errorf("build number = %d, want 2", num)
    }
    select {
    case res := <-done:
        if res.Status != scheduler.StatusSuccess {
            t.Fatalf("Status = %q, want %q (err=%q)",
                res.Status, scheduler.StatusSuccess,
                res.Error)
        }
    case <-time.After(5 * time.Second):
        t.Fatal("timeout waiting for build result")
    }
    /* Marker lives under
     * builds/envjob/<num>/workspace/marker.txt. */
    markerPath := filepath.Join(root, "builds", "envjob",
        strconv.Itoa(num), "workspace", "marker.txt")
    data, err := os.ReadFile(markerPath)
    if err != nil {
        t.Fatalf("read marker: %v", err)
    }
    if string(data) != "from-build-1" {
        t.Errorf("marker = %q, want %q (prior build's "+
            "exported env did not propagate)",
            string(data), "from-build-1")
    }
}
