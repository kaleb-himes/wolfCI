package pipeline_test

/* internal/pipeline/steps_load_test.go - PLAN.md 18.21 gating
 * test.
 *
 * TestStep_LoadHelperGroovy is the spec-named gate: it copies
 * the real jenkinsUtils.groovy file from
 * third_party/testing/Jenkins/jenkins-functions/ into a fresh
 * build workspace, loads it through the load step, then invokes
 * each of its top-level helpers (cleanupName,
 * getJobResultName, commitHashForBuild, getLastBuild,
 * checkIfPassed, shouldTestRetry) with synthesized inputs and
 * asserts the right outputs.
 *
 * The synthesized inputs use Groovy map literals as object
 * substitutes - jenkinsUtils calls methods like
 * `build.rawBuild.getEnvironment().ghprbActualCommit`, and
 * our script interpreter routes `obj.method(args)` through the
 * memberAccess + invokeCallable pair which accepts an sMap-of-
 * closures as the receiver. For helpers that take a Build, we
 * pass a small sMap with the field / method it actually
 * accesses; for the recursive getLastBuild we pass a build
 * whose getPreviousBuild() returns null so the recursion
 * terminates immediately.
 *
 * Outputs are written to per-helper files inside the workspace
 * by the pipeline's `sh "echo ... > rN.txt"` steps, then read
 * back from disk and asserted. Writing files instead of
 * relying on stdout keeps the test order-independent and
 * mask-safe (the secret-text masking from 18.18 runs on echo
 * paths, not on shell stdout that lands in a file).
 */

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_LoadHelperGroovy(t *testing.T) {
    workspace := t.TempDir()

    src, err := os.ReadFile(
        "../../third_party/testing/Jenkins/" +
            "jenkins-functions/jenkinsUtils.groovy")
    if err != nil {
        t.Fatalf("read jenkinsUtils.groovy: %v", err)
    }
    if err := os.WriteFile(
        filepath.Join(workspace, "jenkinsUtils.groovy"),
        src, 0o644); err != nil {
        t.Fatalf("copy jenkinsUtils.groovy: %v", err)
    }

    pipelineSrc := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('Run') {
            steps {
                script {
                    env = [JOB_NAME: 'demo-job']
                    def utils = load('jenkinsUtils.groovy')

                    /* 1: cleanupName. */
                    def r1 = utils.cleanupName('a/b-c d')
                    sh "echo " + r1 + " > r1.txt"

                    /* 2: getJobResultName uses env.JOB_NAME. */
                    def r2 = utils.getJobResultName('step1')
                    sh "echo " + r2 + " > r2.txt"

                    /* 3: commitHashForBuild walks
                     *    build.rawBuild.getEnvironment().
                     *      ghprbActualCommit. */
                    def fakeRawEnv = [ghprbActualCommit: 'abc123']
                    def fakeRaw = [getEnvironment: { -> fakeRawEnv }]
                    def fakeBuild = [rawBuild: fakeRaw]
                    def r3 = utils.commitHashForBuild(fakeBuild)
                    sh "echo " + r3 + " > r3.txt"

                    /* 4: getLastBuild with no previous build
                     *    returns null. */
                    def cur = [getPreviousBuild: { -> null }]
                    def r4 = utils.getLastBuild(cur, 'hash')
                    def r4s = (r4 == null ? 'null' : 'notnull')
                    sh "echo " + r4s + " > r4.txt"

                    /* 5: checkIfPassed(null, ...) short-circuits
                     *    to false. */
                    def r5 = utils.checkIfPassed(null, 'jobX')
                    sh "echo " + r5 + " > r5.txt"

                    /* 6: shouldTestRetry returns true when the
                     *    exception text contains one of the
                     *    canned retry triggers. */
                    def ex = new Exception(
                        'hudson.remoting.RequestAbortedException: ' +
                        'java.io.StreamCorruptedException: ' +
                        'invalid stream header: 4561626f')
                    def r6 = utils.shouldTestRetry(ex)
                    sh "echo " + r6 + " > r6.txt"
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(pipelineSrc)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    exe := &pipeline.LocalExecutor{Workspace: workspace}
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        var allOut strings.Builder
        for _, st := range build.Stages[0].Steps {
            allOut.WriteString(st.Output)
            allOut.WriteString("\n")
        }
        t.Fatalf("build.Status = %v, want BuildSuccess; "+
            "step output:\n%s", build.Status, allOut.String())
    }

    expect := []struct {
        file string
        want string
    }{
        {"r1.txt", "a_b_c_d"},
        {"r2.txt", "RESULT_demo_job_step1"},
        {"r3.txt", "abc123"},
        {"r4.txt", "null"},
        {"r5.txt", "false"},
        {"r6.txt", "true"},
    }
    for _, c := range expect {
        data, err := os.ReadFile(
            filepath.Join(workspace, c.file))
        if err != nil {
            t.Errorf("read %s: %v", c.file, err)
            continue
        }
        got := strings.TrimSpace(string(data))
        if got != c.want {
            t.Errorf("%s = %q, want %q", c.file, got, c.want)
        }
    }
}
