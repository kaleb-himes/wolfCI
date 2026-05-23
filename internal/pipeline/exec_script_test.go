package pipeline_test

/* internal/pipeline/exec_script_test.go - PLAN.md 18.15
 * gating tests.
 *
 * TestExec_ScriptParallel is the spec's named gate: a script
 * block builds a map of three named closures and calls
 * `parallel <map>`. The interpreter must run all three
 * closures (echo captures their messages) and the overall
 * build succeeds.
 *
 * TestExec_ScriptParallel_OneThrows asserts that when one
 * closure throws, the build is marked FAILURE but the other
 * closures still finish - their echos must be visible in the
 * step output, proving the parallel runner does not short-
 * circuit the entire group on a single branch's exception.
 */

import (
    "context"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestExec_ScriptParallel(t *testing.T) {
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def tests = [:]
                    tests['A'] = { -> echo 'A ran' }
                    tests['B'] = { -> echo 'B ran' }
                    tests['C'] = { -> echo 'C ran' }
                    parallel tests
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
    if len(build.Stages) != 1 ||
        len(build.Stages[0].Steps) != 1 {
        t.Fatalf("expected 1 stage with 1 step; got %+v",
            build.Stages)
    }
    out := build.Stages[0].Steps[0].Output
    for _, want := range []string{"A ran", "B ran", "C ran"} {
        if !strings.Contains(out, want) {
            t.Errorf("step output missing %q; got %q",
                want, out)
        }
    }
}

func TestExec_ScriptParallel_OneThrows(t *testing.T) {
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def tests = [:]
                    tests['ok'] = { -> echo 'ok ran' }
                    tests['bad'] = { -> throw new Exception('oops') }
                    tests['ok2'] = { -> echo 'ok2 ran' }
                    parallel tests
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
    /* The throwing branch surfaces as a build failure, NOT as
     * an executor-level error from ExecDeclarative. */
    if err != nil {
        t.Fatalf("ExecDeclarative returned err = %v; throw "+
            "should be a graceful BuildFailure", err)
    }
    if build.Status != pipeline.BuildFailure {
        t.Errorf("build.Status = %v, want BuildFailure",
            build.Status)
    }
    out := build.Stages[0].Steps[0].Output
    /* The other branches must still have run. */
    for _, want := range []string{"ok ran", "ok2 ran"} {
        if !strings.Contains(out, want) {
            t.Errorf("step output missing %q (other branches "+
                "should still finish); got %q", want, out)
        }
    }
}

/* TestExec_ScriptSequential locks in the simpler script flow:
 * a non-parallel script block runs its statements top to
 * bottom, captures echo, and resolves to SUCCESS. */
func TestExec_ScriptSequential(t *testing.T) {
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    echo 'one'
                    echo 'two'
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
    if !strings.Contains(out, "one") ||
        !strings.Contains(out, "two") {
        t.Errorf("missing echo output; got %q", out)
    }
    /* Order must be preserved in sequential evaluation. */
    if strings.Index(out, "one") >
        strings.Index(out, "two") {
        t.Errorf("output order wrong: %q", out)
    }
}
