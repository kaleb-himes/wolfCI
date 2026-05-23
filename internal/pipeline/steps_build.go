/* internal/pipeline/steps_build.go - PLAN.md 18.22.
 *
 * `build` step: dispatches a downstream job through the
 * runtime's BuildDispatcher, waits for the child to finish,
 * and returns an object exposing the bits the Jenkins step
 * surfaces (`.getResult()`, `.getNumber()`, `.getProjectName()`).
 *
 * Usage shape (matches the Jenkinsfile master-job verbatim):
 *
 *   def childOutcome = build(
 *       job: 'child-1',
 *       parameters: [string(name: 'P', value: 'v')],
 *       propagate: false,
 *       wait: true)
 *
 *   if (childOutcome.getResult() != 'SUCCESS') { ... }
 *
 * Propagate behavior:
 *   propagate: true  (default) - a non-SUCCESS child throws
 *                                an Error which bubbles up to
 *                                the surrounding declarative
 *                                stage handler and marks the
 *                                parent build FAILURE.
 *   propagate: false           - the parent keeps running; the
 *                                returned object's
 *                                .getResult() lets the script
 *                                decide what to do.
 *
 * Wait behavior:
 *   wait: true  (default) - the step blocks until the child
 *                           emits its result on the
 *                           BuildDispatcher channel.
 *   wait: false           - the step returns immediately with
 *                           a result whose .getResult() is
 *                           "NOT_BUILT" and .getNumber() is 0;
 *                           the child still runs to
 *                           completion in the background per
 *                           the dispatcher's semantics.
 */
package pipeline

import (
    "context"
    "fmt"
)

/* registerBuildStep installs the 18.22 build step on the
 * runtime. Called from registerCoreSteps so the native surface
 * stays grouped in one place. */
func registerBuildStep(rt *scriptRuntime) {
    rt.globals.define("build",
        &sNative{name: "build", fn: nativeBuild})
}

/* nativeBuild is the body of the `build` step. Expected
 * shape: a single named-args sMap collapsing 'job',
 * 'parameters', 'propagate', and 'wait' from the call site. */
func nativeBuild(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "build: missing arguments (need at least job: ...)")
    }
    m, ok := args[0].(*sMap)
    if !ok {
        /* The plain-string form `build 'job-name'` is rare
         * but supported by Jenkins; surface it as the same
         * shape by promoting the string into a one-entry
         * map. */
        if s, ok := args[0].(*sStr); ok {
            promoted := newMap()
            promoted.set("job", &sStr{v: s.v})
            m = promoted
        } else {
            return nil, fmt.Errorf(
                "build: first arg must be a map of named "+
                    "args (got %T)", args[0])
        }
    }
    jobName, ok := m.values["job"].(*sStr)
    if !ok || jobName == nil {
        return nil, fmt.Errorf(
            "build: missing 'job' (string) argument")
    }
    propagate := true
    if b, ok := m.values["propagate"].(*sBool); ok {
        propagate = b.v
    }
    wait := true
    if b, ok := m.values["wait"].(*sBool); ok {
        wait = b.v
    }
    if rt.dispatcher == nil {
        return nil, fmt.Errorf(
            "build: dispatcher not configured on executor")
    }
    params, err := unrollBuildParams(m.values["parameters"])
    if err != nil {
        return nil, fmt.Errorf("build: %w", err)
    }
    ch, err := rt.dispatcher.EnqueueChildBuild(ctx,
        jobName.v, params)
    if err != nil {
        return nil, fmt.Errorf(
            "build: enqueue %q: %w", jobName.v, err)
    }
    if !wait {
        return buildResultObject(jobName.v, 0, "NOT_BUILT"), nil
    }
    var result ChildBuildResult
    select {
    case r, ok := <-ch:
        if !ok {
            return nil, fmt.Errorf(
                "build: dispatcher closed result channel "+
                    "without emitting a result for %q",
                jobName.v)
        }
        result = r
    case <-ctx.Done():
        return nil, ctx.Err()
    }
    obj := buildResultObject(result.Job, result.Number,
        result.Status)
    if propagate && result.Status != "SUCCESS" {
        return nil, &throwSignal{value: &sExcept{
            typ: "Error",
            msg: fmt.Sprintf(
                "build: downstream job %q finished with "+
                    "status %q (propagate=true)",
                jobName.v, result.Status),
        }}
    }
    return obj, nil
}

/* unrollBuildParams converts the Jenkinsfile
 * `parameters: [string(name: '...', value: '...'), ...]` list
 * into the dispatcher's []BuildParam wire shape. Each list
 * entry is the descriptor sMap nativeStringCred-style
 * constructors emit. For 18.22 only the string(...)
 * constructor is supported; richer typed params land in a
 * follow-on. */
func unrollBuildParams(raw scriptValue) ([]BuildParam, error) {
    if raw == nil {
        return nil, nil
    }
    if _, ok := raw.(*sNull); ok {
        return nil, nil
    }
    lst, ok := raw.(*sList)
    if !ok {
        return nil, fmt.Errorf(
            "parameters must be a list (got %T)", raw)
    }
    var out []BuildParam
    for _, item := range lst.items {
        m, ok := item.(*sMap)
        if !ok {
            return nil, fmt.Errorf(
                "parameters entry must be a descriptor map "+
                    "(got %T)", item)
        }
        nameVal, _ := m.values["name"].(*sStr)
        if nameVal == nil {
            return nil, fmt.Errorf(
                "parameters entry missing 'name'")
        }
        var valStr string
        if v, ok := m.values["value"].(*sStr); ok {
            valStr = v.v
        } else if v, ok := m.values["value"].(*sNum); ok {
            valStr = fmt.Sprintf("%d", v.v)
        } else if v, ok := m.values["value"].(*sBool); ok {
            if v.v {
                valStr = "true"
            } else {
                valStr = "false"
            }
        } else if v, ok := m.values["value"]; ok && v != nil {
            valStr = stringify(v)
        }
        out = append(out, BuildParam{
            Name: nameVal.v, Value: valStr})
    }
    return out, nil
}

/* buildResultObject constructs the Jenkinsfile-facing return
 * object for the build step: an sMap whose method-named keys
 * are sNative closures that report the child build's outcome.
 *
 * Surface covered:
 *
 *   .getResult()        -> the "SUCCESS" / "FAILURE" / ...
 *                          status string.
 *   .getNumber()        -> the child build number assigned by
 *                          the wolfCI scheduler.
 *   .getProjectName()   -> the child job's name.
 *   .getFullProjectName -> same as getProjectName for the
 *                          single-tenant repo layout 18.22
 *                          ships; folder-aware naming arrives
 *                          if multi-tenant routing ever does.
 *   .result / .number   -> bare-field aliases for the
 *                          .getX() variants so Jenkinsfile
 *                          property-access (`outcome.result`)
 *                          works alongside method calls.
 */
func buildResultObject(job string, number int,
    status string) *sMap {
    out := newMap()
    out.set("result", &sStr{v: status})
    out.set("number", &sNum{v: int64(number)})
    out.set("projectName", &sStr{v: job})
    out.set("fullProjectName", &sStr{v: job})
    out.set("getResult", &sNative{name: "getResult",
        fn: func(ctx context.Context, rt *scriptRuntime,
            _ []scriptValue) (scriptValue, error) {
            return &sStr{v: status}, nil
        }})
    out.set("getNumber", &sNative{name: "getNumber",
        fn: func(ctx context.Context, rt *scriptRuntime,
            _ []scriptValue) (scriptValue, error) {
            return &sNum{v: int64(number)}, nil
        }})
    out.set("getProjectName", &sNative{name: "getProjectName",
        fn: func(ctx context.Context, rt *scriptRuntime,
            _ []scriptValue) (scriptValue, error) {
            return &sStr{v: job}, nil
        }})
    out.set("getFullProjectName",
        &sNative{name: "getFullProjectName",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sStr{v: job}, nil
            }})
    return out
}
