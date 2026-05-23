/* internal/pipeline/steps_catcherr.go - PLAN.md 18.23.
 *
 * `catchError` step: catches a throw inside its closure and
 * records a caller-chosen result on the surrounding step /
 * stage / build without re-raising. Lets a Jenkinsfile sustain
 * a partial failure while keeping subsequent statements in the
 * same script {} block alive.
 *
 * Usage shape (matches Jenkins exactly):
 *
 *   catchError(buildResult: 'FAILURE',
 *              stageResult: 'FAILURE',
 *              message:    'optional log line') {
 *       sh 'flaky-tests.sh'
 *   }
 *   echo "still runs"
 *
 * Recognised named args:
 *   buildResult  - status to record on the overall build when
 *                  the closure throws ("SUCCESS", "UNSTABLE",
 *                  "FAILURE"). Default "FAILURE".
 *   stageResult  - status to record on the enclosing stage.
 *                  Default mirrors buildResult.
 *   message      - optional log line written via the runtime's
 *                  echo buffer before the closure throw is
 *                  swallowed. Useful for downstream log greps.
 *
 * The wolfCI BuildStatus model carries Pending / Running /
 * Success / Failure today; Unstable and Aborted are backlog
 * states that surface as Failure for the moment. The native
 * normalises every recognised label so a Jenkinsfile written
 * against the richer status set still parses (and a future
 * patch can refine the mapping without churning the call
 * sites).
 */
package pipeline

import (
    "context"
    "fmt"
    "strings"
)

/* registerCatchErrorStep installs the 18.23 catchError native
 * on the runtime. Called from registerCoreSteps so the
 * step-library surface stays grouped. */
func registerCatchErrorStep(rt *scriptRuntime) {
    rt.globals.define("catchError",
        &sNative{name: "catchError", fn: nativeCatchError})
}

/* nativeCatchError invokes its trailing closure inside a
 * throw-shield. A throwSignal coming out of the body sets the
 * runtime's catchForcedBuild / catchForcedStage fields per the
 * named args; execStep reads those after the surrounding step
 * completes and marks the StepRun accordingly. */
func nativeCatchError(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "catchError: expected a closure body (and " +
                "optional named-args map)")
    }
    var named *sMap
    if m, ok := args[0].(*sMap); ok {
        named = m
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "catchError: last arg must be a closure (got %T)",
            args[len(args)-1])
    }
    buildResultName := "FAILURE"
    stageResultName := ""
    var message string
    if named != nil {
        if v, ok := named.values["buildResult"].(*sStr); ok {
            buildResultName = strings.ToUpper(v.v)
        }
        if v, ok := named.values["stageResult"].(*sStr); ok {
            stageResultName = strings.ToUpper(v.v)
        }
        if v, ok := named.values["message"].(*sStr); ok {
            message = v.v
        }
    }
    if stageResultName == "" {
        stageResultName = buildResultName
    }
    buildResult := normalizeCatchResult(buildResultName)
    stageResult := normalizeCatchResult(stageResultName)

    _, err := invokeClosure(ctx, rt, cl, nil)
    if err == nil {
        /* Closure completed normally - leave the surrounding
         * step's status untouched. */
        return &sNull{}, nil
    }
    if _, ok := err.(*throwSignal); !ok {
        /* Non-throw infra error (context cancel, unsupported
         * expression, ...) is not what catchError is for;
         * propagate. */
        return nil, err
    }
    if message != "" {
        rt.appendEcho(message)
    }
    /* Record the worst-of-existing-and-new for both forced
     * fields so nested catchError blocks compose naturally
     * (an inner UNSTABLE inside an outer FAILURE stays
     * FAILURE; an outer SUCCESS upgraded to FAILURE inside
     * does not regress). */
    rt.applyCatchForced(buildResult, stageResult)
    return &sNull{}, nil
}

/* normalizeCatchResult maps a Jenkins result label to the
 * wolfCI BuildStatus. The wolfCI status enum does not yet
 * include UNSTABLE / ABORTED; both downgrade to BuildFailure
 * for 18.23 (BuildSuccess only passes through SUCCESS).
 * Unknown labels also fall through to BuildFailure - the
 * caller is signalling something went wrong by reaching for
 * catchError, so erring on the failing side is the safe
 * default. */
func normalizeCatchResult(name string) BuildStatus {
    switch strings.ToUpper(name) {
    case "SUCCESS":
        return BuildSuccess
    case "", "NOT_BUILT":
        return BuildRunning
    }
    return BuildFailure
}
