package jobspec_test

/* internal/jobspec/scm_test.go - PLAN.md 18.26 gating tests.
 *
 * TestJob_PipelineScriptFromSCM is the spec-named gate: it
 * loads a job whose pipeline.definition is "from_scm" with
 * repo_url, credentials_id (ssh-private-key),
 * branch_specifier "*\/master",
 * script_path "Jenkins/master-job/PRB.Jenkinsfile", and
 * lightweight_checkout: true. The test then calls
 * RunFromSCM(ctx, spec, workspace, executor) and asserts:
 *
 *   1. The loader accepted the YAML and produced a JobSpec
 *      with the expected fields.
 *   2. The runner cloned the local file:// repo into the
 *      build workspace.
 *   3. The fetched Jenkinsfile parsed and executed - the
 *      pipeline's `sh` step landed an observable file in the
 *      workspace.
 *
 * The "local file:// repo" is a tiny git repo the test
 * initialises inline: an empty workspace, then `git init` +
 * `git checkout -b master` + a Jenkinsfile containing the
 * declarative shape we need + commit. RunFromSCM then clones
 * THAT repo using a file:// URL pointing at the test's git
 * dir. This keeps the test self-contained on a CI machine
 * with no network and exercises every code path
 * (clone, sparse-checkout, read, parse, exec) end to end.
 *
 * TestJob_PipelineScriptFromSCM_BadBranch reuses the same
 * fixture but asks RunFromSCM for a branch that does not
 * exist; asserts an actionable error containing the offending
 * branch name.
 */

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/jobspec"
    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestJob_PipelineScriptFromSCM(t *testing.T) {
    workspace := t.TempDir()
    /* Build the upstream "remote" - a local git repo
     * containing the Jenkinsfile we want to fetch. */
    remote := newGitFixture(t, "Jenkins/master-job",
        "PRB.Jenkinsfile",
        []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                sh 'echo scm-driven > marker.txt'
            }
        }
    }
}`))

    yamlBlob := []byte(fmt.Sprintf(`name: master-job
description: from-scm gating test
pipeline:
  definition: from_scm
  scm:
    repo_url: file://%s
    credentials_id: wolfssl-bot-credentials-with-private-key
    branch_specifier: "*/master"
    script_path: Jenkins/master-job/PRB.Jenkinsfile
    lightweight_checkout: true
`, remote))

    spec, err := jobspec.LoadSpec(yamlBlob)
    if err != nil {
        t.Fatalf("LoadSpec: %v", err)
    }
    if spec.Name != "master-job" {
        t.Errorf("spec.Name = %q, want master-job", spec.Name)
    }
    if spec.Pipeline == nil ||
        spec.Pipeline.Definition !=
            jobspec.DefinitionFromSCM {
        t.Fatalf("spec.Pipeline.Definition = %v, want %v",
            spec.Pipeline, jobspec.DefinitionFromSCM)
    }
    if spec.Pipeline.SCM == nil {
        t.Fatal("spec.Pipeline.SCM is nil")
    }
    if spec.Pipeline.SCM.CredentialsID !=
        "wolfssl-bot-credentials-with-private-key" {
        t.Errorf("CredentialsID = %q, want bot creds",
            spec.Pipeline.SCM.CredentialsID)
    }
    if !spec.Pipeline.SCM.LightweightCheckout {
        t.Errorf("LightweightCheckout = false, want true")
    }

    exe := &pipeline.LocalExecutor{Workspace: workspace}
    build, err := jobspec.RunFromSCM(
        context.Background(), spec, workspace, exe)
    if err != nil {
        t.Fatalf("RunFromSCM: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Fatalf("build.Status = %v, want BuildSuccess",
            build.Status)
    }

    /* The pipeline's sh step landed marker.txt in the
     * workspace. Reading it confirms the fetched script
     * actually executed end to end. */
    markerPath := filepath.Join(workspace, "marker.txt")
    data, err := os.ReadFile(markerPath)
    if err != nil {
        t.Fatalf("read marker.txt: %v", err)
    }
    if !strings.Contains(string(data), "scm-driven") {
        t.Errorf("marker.txt = %q, want it to contain "+
            "scm-driven", string(data))
    }
}

func TestJob_PipelineScriptFromSCM_BadBranch(t *testing.T) {
    workspace := t.TempDir()
    remote := newGitFixture(t, "Jenkins/master-job",
        "PRB.Jenkinsfile",
        []byte(`pipeline { agent { label '' } stages { stage('S') { steps { sh 'true' } } } }`))

    yamlBlob := []byte(fmt.Sprintf(`name: master-job
pipeline:
  definition: from_scm
  scm:
    repo_url: file://%s
    branch_specifier: nonexistent
    script_path: Jenkins/master-job/PRB.Jenkinsfile
    lightweight_checkout: true
`, remote))
    spec, err := jobspec.LoadSpec(yamlBlob)
    if err != nil {
        t.Fatalf("LoadSpec: %v", err)
    }
    exe := &pipeline.LocalExecutor{Workspace: workspace}
    _, err = jobspec.RunFromSCM(
        context.Background(), spec, workspace, exe)
    if err == nil {
        t.Fatal("expected error for nonexistent branch, " +
            "got nil")
    }
    if !strings.Contains(err.Error(), "nonexistent") {
        t.Errorf("error %q does not mention the missing "+
            "branch name", err.Error())
    }
}

/* newGitFixture creates a tiny git repo at t.TempDir(),
 * writes content to <relDir>/<filename> at the master branch,
 * commits, and returns the absolute path of the repo
 * (suitable for use as the body of a file:// URL). The
 * function fails the test if git is not on PATH or any of
 * the init / add / commit steps fail.
 */
func newGitFixture(t *testing.T, relDir, filename string,
    content []byte) string {
    t.Helper()
    dir := t.TempDir()
    runGit := func(args ...string) {
        cmd := exec.Command("git", args...)
        cmd.Dir = dir
        /* Configure committer identity through env so the
         * fixture works on machines without a global gitconfig. */
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
    sub := filepath.Join(dir, relDir)
    if err := os.MkdirAll(sub, 0o755); err != nil {
        t.Fatalf("mkdir %s: %v", sub, err)
    }
    if err := os.WriteFile(
        filepath.Join(sub, filename),
        content, 0o644); err != nil {
        t.Fatalf("write %s: %v", filename, err)
    }
    runGit("add", ".")
    runGit("commit", "-m", "wolfci fixture")
    return dir
}
