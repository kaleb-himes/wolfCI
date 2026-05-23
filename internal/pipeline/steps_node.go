/* internal/pipeline/steps_node.go - PLAN.md 18.24.
 *
 * `node` step: pins the wrapped closure to a specific node /
 * agent label, re-routing every `sh` (and every other
 * executor-touching step) inside the body to whichever Executor
 * the runtime's NodeRouter assigns for that label. Mirrors how
 * Jenkins' master-job uses `node('macos') { ... }` inside
 * parallel branches to fan stages onto labeled agents.
 *
 * Usage shape:
 *
 *   pipeline {
 *       agent { label 'master_linux_group' }
 *       stages { stage('Fanout') { steps { script {
 *           parallel(
 *               linuxA: { node('linux-a') { sh '...' } },
 *               linuxB: { node('linux-b') { sh '...' } },
 *           )
 *       }}}}
 *   }
 *
 * The seam is the NodeRouter interface declared in this file:
 * routes a label string to an Executor for the duration of the
 * closure body. Production wiring lives outside the pipeline
 * package (a future agent-router adapter wraps the scheduler's
 * label resolver); test-local fakes feed the gating tests with
 * fixed label-to-executor maps.
 *
 * Restoration is exception-safe: nativeNode captures the
 * current executor, swaps in the new one, runs the closure
 * inside a defer that restores the original even on a throw or
 * a context cancel.
 */
package pipeline

import (
    "context"
    "fmt"
)

/* NodeRouter resolves a node label to the Executor that should
 * run steps targeted at that label. Implementations are
 * supplied via LocalExecutor.NodeRouter; nil means "no router
 * wired", and the node step surfaces an actionable error
 * rather than silently routing all labels to the runtime's
 * default executor. */
type NodeRouter interface {
    /* ExecutorForLabel returns the Executor matching label.
     * An error is returned only when the label is genuinely
     * unknown to the router (a typo, a fleet that no longer
     * exists, an empty label string against a router that
     * does not define a default). */
    ExecutorForLabel(label string) (Executor, error)
}

/* registerNodeStep installs the 18.24 node native on the
 * runtime. Called from registerCoreSteps so the step-library
 * surface stays grouped. */
func registerNodeStep(rt *scriptRuntime) {
    rt.globals.define("node",
        &sNative{name: "node", fn: nativeNode})
}

/* nativeNode pins the trailing closure to the executor the
 * runtime's NodeRouter returns for the supplied label.
 * Expected shape:
 *
 *   node('<label>') { <closure body> }
 *
 * The bare-string variant (`node '<label>'` without parens)
 * also works thanks to the parser's command-call form. The
 * closure body sees the same scriptRuntime instance (so the
 * step library's echo buffer, secret stack, catchError
 * verdicts, etc. continue to compose); only the
 * executor used by sh / withCredentials / sshagent /
 * archiveArtifacts gets swapped. */
func nativeNode(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) < 2 {
        return nil, fmt.Errorf(
            "node: expected (label, closure)")
    }
    labelStr, ok := args[0].(*sStr)
    if !ok {
        return nil, fmt.Errorf(
            "node: first arg must be a string label (got %T)",
            args[0])
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "node: last arg must be a closure (got %T)",
            args[len(args)-1])
    }
    if rt.nodeRouter == nil {
        return nil, fmt.Errorf(
            "node: NodeRouter not configured on executor")
    }
    next, err := rt.nodeRouter.ExecutorForLabel(labelStr.v)
    if err != nil {
        return nil, fmt.Errorf(
            "node: resolve label %q: %w", labelStr.v, err)
    }
    /* Swap the runtime's executor for the closure duration.
     * The defer restores the prior executor even if the
     * closure throws or the context cancels mid-step, so a
     * sibling node('A') invocation after a failed node('B')
     * still sees its expected executor. */
    prev := rt.executor
    rt.executor = next
    defer func() { rt.executor = prev }()
    return invokeClosure(ctx, rt, cl, nil)
}
