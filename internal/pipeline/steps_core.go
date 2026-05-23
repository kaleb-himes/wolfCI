/* internal/pipeline/steps_core.go - PLAN.md 18.16.
 *
 * Native step functions registered on the script runtime so
 * Jenkinsfile-style step calls work uniformly in both
 * contexts: directly inside a steps {} block (the declarative
 * step path dispatched by exec_declarative.go) AND inside a
 * script {} block (where the script runtime evaluates them as
 * ordinary expressions).
 *
 * Step surface added in 18.16:
 *
 *   sh(script: '<bash>', returnStatus: <bool>,
 *      returnStdout: <bool>)
 *   sh('<bash>')                    naked form
 *   echo '<message>'                 also `echo(arg)`
 *   sleep(time: <int>, unit: '<u>')  also `sleep <int>` (ms)
 *   error '<message>'                also `error(msg)`
 *   script { <body> }                executes the closure body
 *
 * Subsequent step-library phases (18.17-18.24) extend this
 * file with workspace / cred / load / build / catchError /
 * nested-node steps.
 *
 * The named-arg conversion in exec_script.go's
 * collectCallArgs() coalesces any labelled call args into a
 * single map prepended as args[0]; native functions in this
 * file therefore see either:
 *   args[0] = sStr / sNum / ...    (naked / single positional)
 *   args[0] = sMap with the named  (paren'd named-args)
 *   args[0] = sMap, args[1] = ...  (named + positional)
 *   args[N-1] = sClosure           (when a trailing closure
 *                                   was present)
 *
 * Each native asserts the shape it expects and surfaces a
 * clean error otherwise; the surrounding evaluator turns the
 * error into a throwSignal (or returns it directly for
 * infrastructure failures) so the step-run record's Status
 * stays accurate.
 */
package pipeline

import (
    "context"
    "fmt"
    "strings"
    "time"
)

/* registerCoreSteps installs the 18.16 step library on the
 * supplied runtime. Called from scriptRuntime.registerNatives
 * during runtime construction. Subsequent step-library phases
 * (18.17+) chain into their own registerXxxSteps from this
 * single entry point so the native surface stays discoverable
 * in one place. */
func registerCoreSteps(rt *scriptRuntime) {
    rt.globals.define("sh",
        &sNative{name: "sh", fn: nativeSh})
    rt.globals.define("echo",
        &sNative{name: "echo", fn: nativeEcho})
    rt.globals.define("sleep",
        &sNative{name: "sleep", fn: nativeSleep})
    rt.globals.define("error",
        &sNative{name: "error", fn: nativeError})
    rt.globals.define("script",
        &sNative{name: "script", fn: nativeScript})
    /* 18.17 workspace step library. */
    registerWorkspaceSteps(rt)
    /* 18.18 credential bindings step library. */
    registerCredsSteps(rt)
    /* 18.20 sshagent step library. */
    registerSshSteps(rt)
}

/* ----- echo ------------------------------------------------- */

/* nativeEcho appends a formatted line to the runtime's echo
 * buffer (which the declarative dispatcher hands back as the
 * StepRun's Output). Accepts any number of positional args
 * and stringifies each; lands in the buffer as a single line. */
func nativeEcho(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        rt.appendEcho("")
        return &sNull{}, nil
    }
    parts := make([]string, 0, len(args))
    for _, a := range args {
        parts = append(parts, stringify(a))
    }
    rt.appendEcho(strings.Join(parts, " "))
    return &sNull{}, nil
}

/* ----- sh --------------------------------------------------- */

/* nativeSh dispatches to the runtime's executor. Recognises
 * three flag combinations: default (echo stdout + throw on
 * non-zero), returnStatus (return the exit code without
 * throwing, output still echoed), returnStdout (return
 * captured stdout, do NOT echo it into the build log).
 *
 * Always stores the last exit code on the runtime so the
 * declarative dispatcher can copy it onto the StepRun, even
 * when sh threw without surfacing a return value.
 */
func nativeSh(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    var script string
    var returnStatus, returnStdout bool
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "sh: no arguments")
    }
    switch a := args[0].(type) {
    case *sStr:
        script = a.v
    case *sMap:
        s, ok := a.values["script"].(*sStr)
        if !ok {
            return nil, fmt.Errorf(
                "sh: missing or non-string 'script' arg")
        }
        script = s.v
        if b, ok := a.values["returnStatus"].(*sBool); ok {
            returnStatus = b.v
        }
        if b, ok := a.values["returnStdout"].(*sBool); ok {
            returnStdout = b.v
        }
    default:
        return nil, fmt.Errorf(
            "sh: arg must be string or map (got %T)", args[0])
    }
    code, out, err := rt.executor.Sh(ctx, script,
        rt.snapshotEnv())
    if err != nil {
        return nil, err
    }
    rt.lastExitCode = code
    /* returnStdout suppresses the build-log echo - the caller
     * is capturing the bytes. The captured value is masked
     * only when it's about to land in the echo buffer; the
     * raw value flows back to the script so a subsequent
     * `echo` of it (or assignment) sees the real bytes. */
    if returnStdout {
        return &sStr{v: strings.TrimRight(out, "\n")}, nil
    }
    if out != "" {
        rt.appendEcho(rt.maskOutput(
            strings.TrimRight(out, "\n")))
    }
    if returnStatus {
        return &sNum{v: int64(code)}, nil
    }
    if code != 0 {
        return nil, &throwSignal{value: &sExcept{
            typ: "Error",
            msg: fmt.Sprintf(
                "sh: script returned exit code %d", code),
        }}
    }
    return &sNull{}, nil
}

/* ----- sleep ------------------------------------------------ */

/* nativeSleep pauses execution. Accepts the named-arg form
 * `sleep(time: <int>, unit: '<unit>')` (matching Jenkins'
 * sleep step exactly) or the bare numeric form `sleep <ms>`
 * (defaults to milliseconds, useful in tests). Honours the
 * caller's ctx so a cancelled build short-circuits.
 */
func nativeSleep(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf("sleep: no arguments")
    }
    var d time.Duration
    switch a := args[0].(type) {
    case *sNum:
        /* Bare numeric form defaults to milliseconds. The
         * Jenkins step defaults to seconds; we deviate so
         * tests can sleep for short durations without macros. */
        d = time.Duration(a.v) * time.Millisecond
    case *sMap:
        var amount int64
        unit := "MILLISECONDS"
        if n, ok := a.values["time"].(*sNum); ok {
            amount = n.v
        } else {
            return nil, fmt.Errorf(
                "sleep: missing or non-integer 'time' arg")
        }
        if s, ok := a.values["unit"].(*sStr); ok {
            unit = s.v
        }
        u, err := sleepUnitDuration(unit)
        if err != nil {
            return nil, err
        }
        d = time.Duration(amount) * u
    default:
        return nil, fmt.Errorf(
            "sleep: arg must be number or map (got %T)",
            args[0])
    }
    select {
    case <-time.After(d):
        return &sNull{}, nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

/* sleepUnitDuration converts a Jenkins-style unit token into
 * a Go time.Duration multiplier. The set matches Jenkins'
 * sleep step (NANOSECONDS through DAYS); anything else is an
 * actionable error. */
func sleepUnitDuration(unit string) (time.Duration, error) {
    switch strings.ToUpper(unit) {
    case "NANOSECONDS":
        return time.Nanosecond, nil
    case "MICROSECONDS":
        return time.Microsecond, nil
    case "MILLISECONDS":
        return time.Millisecond, nil
    case "SECONDS":
        return time.Second, nil
    case "MINUTES":
        return time.Minute, nil
    case "HOURS":
        return time.Hour, nil
    case "DAYS":
        return 24 * time.Hour, nil
    }
    return 0, fmt.Errorf("sleep: unknown unit %q", unit)
}

/* ----- error ------------------------------------------------ */

/* nativeError throws a script-level abort exception with the
 * caller's message. The surrounding script step downgrades
 * the throwSignal to BuildFailure; statements after the error
 * call do not execute. */
func nativeError(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    var msg string
    if len(args) > 0 {
        if s, ok := args[0].(*sStr); ok {
            msg = s.v
        } else {
            msg = stringify(args[0])
        }
    }
    return nil, &throwSignal{value: &sExcept{
        typ: "FlowInterruptedException",
        msg: msg,
    }}
}

/* ----- script ----------------------------------------------- */

/* nativeScript invokes a trailing closure. The declarative
 * dispatcher translates `script { body }` into
 * `script(closure { body })`, so the body is the last
 * argument (after any named args). Errors from the body
 * propagate unchanged so the script step in the declarative
 * dispatcher can decide whether to mark BuildFailure or pass
 * an infrastructure error through. */
func nativeScript(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "script: missing closure body")
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "script: last arg must be a closure (got %T)",
            args[len(args)-1])
    }
    return invokeClosure(ctx, rt, cl, nil)
}
