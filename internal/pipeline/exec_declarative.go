/* internal/pipeline/exec_declarative.go - PLAN.md 18.14.
 *
 * Walks a parsed declarative-pipeline AST (from
 * parser_declarative.go) and executes its stages and steps,
 * producing a Build record. The agent-dispatch story for
 * non-empty labels is a follow-on; for 18.14 the empty agent
 * label `''` resolves to the supplied Executor in tests, and
 * the only step implemented is `sh`. Subsequent phases extend
 * the step library (18.16+) and richer execution policies
 * (catchError, parallel, post {}, etc.).
 *
 * Sequential, fail-fast semantics: stages run in order; the
 * first failing stage stops the build and subsequent stages
 * do not execute. Inside a stage, the first failing step
 * stops the stage. Phase 18.23 introduces catchError so
 * individual steps can opt out of fail-fast.
 *
 * The Executor abstraction lets the test runner (and the
 * later GHPRB master-job integration test in 18.30) inject a
 * fake instead of /bin/sh. The default LocalExecutor invokes
 * /bin/sh -c on the host process; agent dispatch over the
 * existing wolfci-agent transport is a follow-on phase.
 */
package pipeline

import (
    "bytes"
    "context"
    "fmt"
    "os"
    "os/exec"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

/* BuildStatus enumerates the terminal states a build, stage,
 * or step can settle into. Pending/Running are interior
 * states the interpreter uses while a unit is in flight;
 * the externally visible terminal states are Success and
 * Failure (and, in a follow-on, Aborted / Unstable).
 */
type BuildStatus int

const (
    BuildPending BuildStatus = iota
    BuildRunning
    BuildSuccess
    BuildFailure
)

/* String returns a stable label for a BuildStatus. */
func (s BuildStatus) String() string {
    switch s {
    case BuildPending:
        return "PENDING"
    case BuildRunning:
        return "RUNNING"
    case BuildSuccess:
        return "SUCCESS"
    case BuildFailure:
        return "FAILURE"
    }
    return "UNKNOWN"
}

/* Build is the top-level record of a pipeline run. */
type Build struct {
    Status BuildStatus
    Stages []*StageRun
}

/* StageRun records one stage's execution. */
type StageRun struct {
    Name   string
    Status BuildStatus
    Steps  []*StepRun
}

/* StepRun records one step invocation's result. */
type StepRun struct {
    Name     string
    Status   BuildStatus
    ExitCode int
    Output   string
}

/* Executor is the abstraction over "the agent that runs the
 * step". Tests inject a fake; production wires a wolfci-agent
 * client. For 18.14 only Sh is needed; later phases extend
 * the interface (or split it) as new step kinds land.
 */
type Executor interface {
    /* Sh runs script under /bin/sh and returns its exit code
     * and combined stdout+stderr. extraEnv carries env
     * additions ("KEY=value" entries) that the 18.18+
     * withCredentials step uses to inject unsealed secrets
     * into the wrapped sh; the executor merges extraEnv on
     * top of its own environment so the shell sees them.
     * err is non-nil only on infrastructure failures
     * (process couldn't be spawned, killed by signal); a
     * non-zero exit code is reported via the int return,
     * not via err. */
    Sh(ctx context.Context, script string,
        extraEnv []string) (int, string, error)
}

/* LocalExecutor implements Executor by invoking /bin/sh on
 * the current process. Used in tests and as the default for
 * the empty-label agent. The optional Workspace / StashDir /
 * ArtifactsDir fields plumb the 18.17 workspace-step library:
 *
 *   Workspace    - shell commands run with this as the
 *                  working directory; cleanWs / dir / stash
 *                  / unstash / archiveArtifacts all read it
 *                  through the script runtime.
 *   StashDir     - per-build dir holding `stash` bundles
 *                  (kept outside Workspace so cleanWs does
 *                  not wipe them).
 *   ArtifactsDir - `archiveArtifacts` copies matching files
 *                  here, the moral equivalent of
 *                  `builds/<job>/<n>/artifacts/`.
 *
 * Each is optional; the empty-value default keeps the 18.14
 * test surface working (no workspace -> cmd.Dir unset -> sh
 * uses the test's current directory).
 */
type LocalExecutor struct {
    Workspace    string
    StashDir     string
    ArtifactsDir string

    /* Creds is the credential store the 18.18+
     * withCredentials step queries to unseal secrets at
     * binding time. The runtime inherits the pointer in
     * newScriptRuntime so the step-library natives can
     * reach it. Nil means "no credstore wired" - any step
     * that needs one errors out with an actionable message
     * rather than panicking. */
    Creds *credstore.Store

    /* Dispatcher is the seam the 18.22+ build step uses to
     * enqueue downstream jobs onto the wolfCI scheduler. nil
     * means "no dispatcher wired" - the build step errors
     * out with an actionable message rather than panicking,
     * which matches how Creds / Workspace handle the
     * no-runtime-wired case. */
    Dispatcher BuildDispatcher
}

/* Sh runs script under /bin/sh -c and captures combined
 * output. A non-zero shell exit surfaces as the int return,
 * not an error; only spawn/IO failures produce err != nil.
 * When Workspace is set the command runs with that as its
 * working directory; otherwise the process's cwd is used.
 * extraEnv ("KEY=value" entries) is appended on top of the
 * process environment so withCredentials-style secret
 * injection lands inside the shell without polluting the
 * parent process's env.
 */
func (e *LocalExecutor) Sh(ctx context.Context,
    script string, extraEnv []string) (int, string, error) {
    cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
    if e.Workspace != "" {
        cmd.Dir = e.Workspace
    }
    if len(extraEnv) > 0 {
        cmd.Env = append(os.Environ(), extraEnv...)
    }
    var buf bytes.Buffer
    cmd.Stdout = &buf
    cmd.Stderr = &buf
    err := cmd.Run()
    if err != nil {
        if ee, ok := err.(*exec.ExitError); ok {
            return ee.ExitCode(), buf.String(), nil
        }
        return -1, buf.String(), err
    }
    return 0, buf.String(), nil
}

/* ExecDeclarative runs the supplied PipelineFile's pipeline
 * block top to bottom and returns the resulting Build. The
 * returned err is non-nil ONLY on infrastructure failures
 * (nil pipeline, unknown step, unparseable args); a build
 * that ran fully but ended in failure surfaces via
 * Build.Status = BuildFailure with err == nil.
 */
func ExecDeclarative(ctx context.Context, file *PipelineFile,
    executor Executor) (*Build, error) {
    if file == nil || file.Pipeline == nil {
        return nil, fmt.Errorf(
            "pipeline.ExecDeclarative: nil pipeline block")
    }
    build := &Build{Status: BuildRunning}
    if file.Pipeline.Stages == nil {
        /* No stages: a pipeline with no work is trivially
         * successful. */
        build.Status = BuildSuccess
        return build, nil
    }
    for _, st := range file.Pipeline.Stages.Stages {
        sr, err := execStage(ctx, st, executor)
        build.Stages = append(build.Stages, sr)
        if err != nil {
            build.Status = BuildFailure
            return build, err
        }
        if sr.Status != BuildSuccess {
            build.Status = BuildFailure
            return build, nil
        }
    }
    build.Status = BuildSuccess
    return build, nil
}

/* execStage runs one stage's step list with the supplied
 * executor. */
func execStage(ctx context.Context, stage *Stage,
    executor Executor) (*StageRun, error) {
    sr := &StageRun{Name: stage.Name, Status: BuildRunning}
    if stage.Steps == nil {
        /* An empty stage (no steps {} body) is trivially
         * successful. Parallel-only and post-only stages
         * land here too until 18.15 wires them up. */
        sr.Status = BuildSuccess
        return sr, nil
    }
    for _, step := range stage.Steps.Steps {
        rr, err := execStep(ctx, step, executor)
        sr.Steps = append(sr.Steps, rr)
        if err != nil {
            sr.Status = BuildFailure
            return sr, err
        }
        if rr.Status != BuildSuccess {
            sr.Status = BuildFailure
            return sr, nil
        }
    }
    sr.Status = BuildSuccess
    return sr, nil
}

/* execStep dispatches one StepCall through the script
 * runtime. The 18.16 refactor unifies the declarative-step
 * path with the script-block path: every step becomes a
 * synthetic CallExpr that the runtime evaluates against its
 * native-step table (registered in steps_core.go). This lets
 * `sh 'true'`, `sh(returnStatus: true, script: '...')`,
 * `echo "hi"`, `sleep 50`, `error 'oops'`, and
 * `script { body }` all share one dispatch + arg-coercion
 * path. A throwSignal escaping the runtime turns into
 * BuildFailure without propagating as an infrastructure
 * error.
 */
func execStep(ctx context.Context, step StepCall,
    executor Executor) (*StepRun, error) {
    sr := &StepRun{Name: step.Name, Status: BuildRunning}
    call, err := stepCallToCallExpr(step)
    if err != nil {
        sr.Status = BuildFailure
        return sr, fmt.Errorf(
            "pipeline.ExecDeclarative: step %q at %d:%d: %w",
            step.Name, step.Pos.Line, step.Pos.Col, err)
    }
    rt := newScriptRuntime(executor)
    env := newEnv(rt.globals)
    v, evalErr := evalExpr(ctx, rt, env, call)
    sr.Output = rt.outputString()
    sr.ExitCode = rt.lastExitCode
    if evalErr != nil {
        if _, ok := evalErr.(*throwSignal); ok {
            sr.Status = BuildFailure
            return sr, nil
        }
        sr.Status = BuildFailure
        return sr, evalErr
    }
    /* Some steps return a usable value (sh returnStatus, for
     * example); a top-level integer return surfaces on the
     * StepRun's ExitCode for downstream visibility even when
     * the corresponding step didn't touch rt.lastExitCode.
     * 18.14's test only checks ExitCode for failing sh, but
     * future tests may want it for the returnStatus case
     * directly. */
    if n, ok := v.(*sNum); ok && sr.ExitCode == 0 {
        sr.ExitCode = int(n.v)
    }
    sr.Status = BuildSuccess
    return sr, nil
}

/* stepCallToCallExpr lifts a parsed StepCall (from the
 * declarative parser's captured token ranges) into the
 * script-subset CallExpr the runtime evaluates. The
 * conversion handles each ArgsKind plus the trailing-closure
 * case (script { body }):
 *
 *   ArgsNone:  CallExpr(IdentExpr(name), nil)
 *   ArgsNaked: CallExpr(IdentExpr(name), [{Value: expr}])
 *              with expr parsed from the captured token range
 *   ArgsParen: CallExpr(IdentExpr(name), parsed arg list)
 *
 * HasBlock=true appends a ClosureExpr whose body is the
 * captured block tokens parsed as a Block; the native
 * receiving the call sees the closure as the last argument.
 */
func stepCallToCallExpr(step StepCall) (*CallExpr, error) {
    fn := &IdentExpr{Name: step.Name,
        Pos: Position{Line: step.Pos.Line,
            Col: step.Pos.Col}}
    call := &CallExpr{Fn: fn,
        Pos: Position{Line: step.Pos.Line,
            Col: step.Pos.Col}}
    switch step.ArgsKind {
    case ArgsNone:
        /* no args */
    case ArgsNaked:
        e, err := ParseExprTokens(step.ArgTokens)
        if err != nil {
            return nil, fmt.Errorf(
                "parsing naked args: %w", err)
        }
        call.Args = []CallArg{{Value: e}}
    case ArgsParen:
        a, err := ParseArgListTokens(step.ArgTokens)
        if err != nil {
            return nil, fmt.Errorf(
                "parsing paren args: %w", err)
        }
        call.Args = a
    }
    if step.HasBlock {
        sf, err := ParseScriptTokens(step.Block)
        if err != nil {
            return nil, fmt.Errorf(
                "parsing block body: %w", err)
        }
        call.ClosureArg = &ClosureExpr{Body: sf.Block,
            Pos: Position{Line: step.Pos.Line,
                Col: step.Pos.Col}}
    }
    return call, nil
}
