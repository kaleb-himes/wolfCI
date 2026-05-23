package pipeline_test

/* internal/pipeline/steps_node_test.go - PLAN.md 18.24 gating
 * test.
 *
 * TestStep_NestedNodeReallocates wires a two-executor router
 * keyed on label (A -> labelExecutor{}, B -> labelExecutor{})
 * and runs a pipeline whose outer agent is `label 'A'`,
 * inside a script block that calls
 *
 *   node('B') { sh 'hostname' }
 *
 * The assertion is that the sh ran on the label-B executor:
 * the labelExecutor records every Sh call's script text in a
 * thread-safe slice, and the test verifies that only the
 * B executor saw the hostname call.
 *
 * Mirrors the master-job's `node('macos') { ... }` /
 * `node('linux-arm64') { ... }` use inside parallel closures.
 *
 * A second test, TestStep_NestedNodeRestoresOnReturn, runs a
 * sequence node('B') { sh 'inside-B' } then sh 'outside'
 * inside the same script block to confirm the outer executor
 * is restored after the closure exits.
 */

import (
    "context"
    "fmt"
    "sync"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_NestedNodeReallocates(t *testing.T) {
    aExec := &labelExecutor{label: "A"}
    bExec := &labelExecutor{label: "B"}
    router := &mapRouter{m: map[string]pipeline.Executor{
        "A": aExec, "B": bExec,
    }}

    src := []byte(`pipeline {
    agent { label 'A' }
    stages {
        stage('Outer') {
            steps {
                script {
                    sh 'outer-hostname'
                    node('B') {
                        sh 'hostname'
                    }
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    /* The initial Executor is aExec (matches the outer
     * agent 'A'); router resolves nested labels. */
    aExecWired := &labelExecutor{label: "A",
        delegate: aExec, router: router}
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, aExecWired)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Fatalf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    aCalls := aExec.calls()
    bCalls := bExec.calls()
    if len(aCalls) != 1 ||
        aCalls[0] != "outer-hostname" {
        t.Errorf("A executor calls = %v, want "+
            "[outer-hostname]", aCalls)
    }
    if len(bCalls) != 1 || bCalls[0] != "hostname" {
        t.Errorf("B executor calls = %v, want [hostname]",
            bCalls)
    }
}

func TestStep_NestedNodeRestoresOnReturn(t *testing.T) {
    aExec := &labelExecutor{label: "A"}
    bExec := &labelExecutor{label: "B"}
    router := &mapRouter{m: map[string]pipeline.Executor{
        "A": aExec, "B": bExec,
    }}

    src := []byte(`pipeline {
    agent { label 'A' }
    stages {
        stage('Outer') {
            steps {
                script {
                    node('B') { sh 'inside-B' }
                    sh 'outside'
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    aExecWired := &labelExecutor{label: "A",
        delegate: aExec, router: router}
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, aExecWired)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Fatalf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    aCalls := aExec.calls()
    bCalls := bExec.calls()
    if len(bCalls) != 1 || bCalls[0] != "inside-B" {
        t.Errorf("B executor calls = %v, want "+
            "[inside-B]", bCalls)
    }
    if len(aCalls) != 1 || aCalls[0] != "outside" {
        t.Errorf("A executor calls = %v, want "+
            "[outside]; node('B') restoration may have "+
            "leaked", aCalls)
    }
}

/* labelExecutor is a fake pipeline.Executor that records every
 * Sh call's script text + the receiving label. Goroutine-safe.
 *
 * When delegate is set, the labelExecutor acts as the outer
 * (initial) executor wired into LocalExecutor-position: it
 * still records to its own slice (so the outer label sees the
 * calls), AND it carries the router so the runtime picks up
 * NodeRouter routing.
 */
type labelExecutor struct {
    label    string
    delegate *labelExecutor /* recordOnly target; nil = self */
    router   pipeline.NodeRouter

    mu  sync.Mutex
    log []string
}

func (le *labelExecutor) Sh(ctx context.Context,
    script string, extraEnv []string) (int, string, error) {
    target := le
    if le.delegate != nil {
        target = le.delegate
    }
    target.mu.Lock()
    target.log = append(target.log, script)
    target.mu.Unlock()
    return 0, "", nil
}

func (le *labelExecutor) calls() []string {
    le.mu.Lock()
    defer le.mu.Unlock()
    out := make([]string, len(le.log))
    copy(out, le.log)
    return out
}

/* NodeRouter satisfies the interface scriptRuntime checks for
 * when the Executor is not a *LocalExecutor; lets a test fake
 * carry a label->Executor map without dragging LocalExecutor
 * (and its /bin/sh path) into the test surface. */
func (le *labelExecutor) NodeRouter() pipeline.NodeRouter {
    return le.router
}

/* mapRouter resolves a label to a fixed Executor. */
type mapRouter struct {
    m map[string]pipeline.Executor
}

func (r *mapRouter) ExecutorForLabel(
    label string) (pipeline.Executor, error) {
    if e, ok := r.m[label]; ok {
        return e, nil
    }
    return nil, fmt.Errorf("unknown label %q", label)
}
