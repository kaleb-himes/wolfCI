package pipeline_test

/* internal/pipeline/steps_catcherr_test.go - PLAN.md 18.23
 * gating test.
 *
 * TestStep_CatchErrorRecords wraps a failing `sh` in
 *
 *   catchError(buildResult: 'FAILURE',
 *              stageResult: 'FAILURE') {
 *       sh 'false'
 *   }
 *
 * and asserts:
 *
 *   1. The stage is recorded BuildFailure - the catchError
 *      verdict propagated up the script-step / stage chain.
 *   2. The build overall is BuildFailure - same path,
 *      stage failure rolls up.
 *   3. Subsequent statements inside the same script {} block
 *      still execute - the test follows the catchError with
 *      `sh 'echo after > marker.txt'` and verifies the file
 *      lands in the workspace.
 *
 * Test 2 (TestStep_CatchErrorPassesThroughSuccess) keeps the
 * verdict at SUCCESS when the wrapped closure does not throw:
 * a catchError around a clean sh leaves the stage / build
 * Success.
 */

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_CatchErrorRecords(t *testing.T) {
    workspace := t.TempDir()
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    catchError(buildResult: 'FAILURE',
                               stageResult: 'FAILURE') {
                        sh 'false'
                    }
                    sh 'echo after > marker.txt'
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    exe := &pipeline.LocalExecutor{Workspace: workspace}
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildFailure {
        t.Errorf("build.Status = %v, want BuildFailure",
            build.Status)
    }
    if len(build.Stages) == 0 ||
        build.Stages[0].Status != pipeline.BuildFailure {
        t.Errorf("stage[0].Status = %v, want BuildFailure",
            build.Stages[0].Status)
    }
    /* Subsequent statement executed: the marker file landed
     * in the workspace. */
    data, err := os.ReadFile(
        filepath.Join(workspace, "marker.txt"))
    if err != nil {
        t.Fatalf("read marker.txt (subsequent sh did not "+
            "run after catchError): %v", err)
    }
    if !strings.Contains(string(data), "after") {
        t.Errorf("marker.txt = %q, want it to contain "+
            "'after'", string(data))
    }
}

/* TestStep_CatchErrorPassesThroughSuccess confirms the
 * opposite path: a clean closure leaves build / stage status
 * at SUCCESS even though the catchError wrapper is present.
 */
func TestStep_CatchErrorPassesThroughSuccess(t *testing.T) {
    workspace := t.TempDir()
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    catchError(buildResult: 'FAILURE') {
                        sh 'true'
                    }
                    echo 'tail'
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    exe := &pipeline.LocalExecutor{Workspace: workspace}
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Errorf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
}
