package pipeline_test

/* internal/pipeline/steps_core_test.go - PLAN.md 18.16
 * gating tests.
 *
 * The 18.16 step library adds sh / echo / sleep / error as
 * native step functions registered on the script runtime.
 * Each native is exercised through a real declarative
 * pipeline so the dispatch path (StepCall token range -> AST
 * CallExpr -> script-runtime invocation) gets gated end to
 * end. The four named tests below match the names PLAN.md
 * 18.16 calls out.
 */

import (
    "context"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_ShReturnStatus(t *testing.T) {
    /* `sh(returnStatus: true, script: 'exit 7')` returns the
     * integer exit code (7) without failing the build. The
     * surrounding script block stores the value and echoes
     * it back so the test can assert on the captured output.
     */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def rc = sh(returnStatus: true,
                                script: 'exit 7')
                    echo 'rc=' + rc
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file,
        &pipeline.LocalExecutor{})
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Errorf("build.Status = %v, want BuildSuccess "+
            "(returnStatus must suppress the non-zero "+
            "failure)", build.Status)
    }
    out := build.Stages[0].Steps[0].Output
    if !strings.Contains(out, "rc=7") {
        t.Errorf("step output missing 'rc=7'; got %q", out)
    }
}

func TestStep_ShReturnStdout(t *testing.T) {
    /* `sh(returnStdout: true, script: 'echo hi')` returns the
     * shell's stdout (trimmed of trailing newline) without
     * echoing it into the build log directly. The wrapper
     * script echoes a labelled form so we can match on a
     * stable substring.
     */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def out = sh(returnStdout: true,
                                 script: 'echo hi')
                    echo 'got=' + out
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file,
        &pipeline.LocalExecutor{})
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Errorf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    out := build.Stages[0].Steps[0].Output
    if !strings.Contains(out, "got=hi") {
        t.Errorf("step output missing 'got=hi' (returnStdout "+
            "must capture shell stdout); got %q", out)
    }
}

func TestStep_Error(t *testing.T) {
    /* `error 'msg'` throws an abort exception and fails the
     * build. Statements after the error must NOT execute. */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    echo 'before'
                    error 'oops'
                    echo 'unreachable'
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file,
        &pipeline.LocalExecutor{})
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildFailure {
        t.Errorf("build.Status = %v, want BuildFailure",
            build.Status)
    }
    out := build.Stages[0].Steps[0].Output
    if !strings.Contains(out, "before") {
        t.Errorf("expected 'before' echo to fire; got %q", out)
    }
    if strings.Contains(out, "unreachable") {
        t.Errorf("statements after error must not execute; "+
            "got %q", out)
    }
}

func TestStep_Sleep(t *testing.T) {
    /* `sleep(time: 50, unit: 'MILLISECONDS')` pauses then
     * continues. The test bounds the duration loosely (>= 40
     * ms is plenty to catch a no-op implementation while not
     * flaking under load) and confirms the 'after' echo fired
     * after the sleep returned. */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    echo 'before'
                    sleep(time: 50, unit: 'MILLISECONDS')
                    echo 'after'
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    start := time.Now()
    build, err := pipeline.ExecDeclarative(
        context.Background(), file,
        &pipeline.LocalExecutor{})
    elapsed := time.Since(start)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Errorf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    if elapsed < 40*time.Millisecond {
        t.Errorf("sleep returned too fast (elapsed=%v); "+
            "want >= 40ms", elapsed)
    }
    out := build.Stages[0].Steps[0].Output
    if !strings.Contains(out, "before") ||
        !strings.Contains(out, "after") {
        t.Errorf("missing 'before' / 'after' echoes; got %q",
            out)
    }
    if strings.Index(out, "before") >
        strings.Index(out, "after") {
        t.Errorf("ordering wrong; got %q", out)
    }
}

/* TestStep_EchoAtSteps locks in declarative-level echo: the
 * `echo` step inside a steps {} block (without a script {}
 * wrapper) must still produce captured output. */
func TestStep_EchoAtSteps(t *testing.T) {
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                echo 'top-level echo'
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file,
        &pipeline.LocalExecutor{})
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Errorf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    out := build.Stages[0].Steps[0].Output
    if !strings.Contains(out, "top-level echo") {
        t.Errorf("declarative echo missing in output; got %q",
            out)
    }
}
