package pipeline_test

/* internal/pipeline/exec_declarative_test.go - PLAN.md 18.14
 * gating test.
 *
 * TestExec_SingleStageSh is the spec's named gate: run a one-
 * stage pipeline whose only step is `sh 'true'`, assert that
 * the build resolves to SUCCESS with one stage and one step.
 * The empty agent label '' resolves to the local executor in
 * tests; the interpreter is invoked with a LocalExecutor that
 * runs the shell command in the test process via /bin/sh -c.
 */

import (
    "context"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestExec_SingleStageSh(t *testing.T) {
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps { sh 'true' }
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
    if len(build.Stages) != 1 {
        t.Fatalf("expected 1 stage run, got %d",
            len(build.Stages))
    }
    if build.Stages[0].Name != "S" {
        t.Errorf("stage[0].Name = %q, want S",
            build.Stages[0].Name)
    }
    if build.Stages[0].Status != pipeline.BuildSuccess {
        t.Errorf("stage[0].Status = %v, want BuildSuccess",
            build.Stages[0].Status)
    }
    if len(build.Stages[0].Steps) != 1 {
        t.Fatalf("expected 1 step run, got %d",
            len(build.Stages[0].Steps))
    }
    if build.Stages[0].Steps[0].Name != "sh" {
        t.Errorf("step[0].Name = %q, want sh",
            build.Stages[0].Steps[0].Name)
    }
    if build.Stages[0].Steps[0].Status != pipeline.BuildSuccess {
        t.Errorf("step[0].Status = %v, want BuildSuccess",
            build.Stages[0].Steps[0].Status)
    }
}

func TestExec_FailingShStepFailsBuild(t *testing.T) {
    /* A step that exits non-zero must surface as a failed
     * stage and a failed build so 18.16's behaviour tests
     * have a stable contract to build on. */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps { sh 'exit 7' }
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
    if len(build.Stages) != 1 || build.Stages[0].Status !=
        pipeline.BuildFailure {
        t.Errorf("expected one failed stage; got %+v",
            build.Stages)
    }
    if len(build.Stages[0].Steps) != 1 ||
        build.Stages[0].Steps[0].ExitCode != 7 {
        t.Errorf("expected step exit 7; got %+v",
            build.Stages[0].Steps)
    }
}

func TestExec_SubsequentStagesSkippedOnFailure(t *testing.T) {
    /* When stage A fails, stage B must not run. This locks in
     * the simple sequential-with-fail-fast policy 18.14 ships
     * with; richer policies (catchError, post {} cleanup) land
     * in their own follow-on tasks. */
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('A') {
            steps { sh 'exit 1' }
        }
        stage('B') {
            steps { sh 'true' }
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
    if len(build.Stages) != 1 {
        t.Errorf("expected 1 stage executed before fail-fast, "+
            "got %d", len(build.Stages))
    }
}
