/* internal/pipeline/steps_load.go - PLAN.md 18.21.
 *
 * `load` step: reads a Groovy file from the build workspace,
 * parses it with the script parser, executes the script body in
 * a fresh env, and returns the loaded script's top-level
 * definitions as an sMap the caller can method-call on. The
 * shape matches Jenkins' load step closely enough that
 * jenkinsUtils.groovy ("def cleanupName(...) {...} ... return
 * this") works verbatim:
 *
 *   def utils = load 'jenkinsUtils.groovy'
 *   def cleaned = utils.cleanupName('a/b-c d')   // -> "a_b_c_d"
 *
 * The loaded script runs as a child env of rt.globals so the
 * script sees every native already registered on the runtime
 * (sh, echo, sleep, ...) plus any caller-defined root binding
 * (`env = [...]` at the script-block level lands in
 * rt.globals because assignment-without-`def` walks to root).
 * Closures defined inside the loaded script capture loadedEnv
 * as their lexical scope, so when the caller invokes
 * `utils.cleanupName(...)` LATER, the closure's name
 * resolution still walks loadedEnv -> rt.globals and reaches
 * helpers + caller-defined env -> JOB_NAME / etc.
 *
 * `return this` at the bottom of the loaded script: we
 * pre-define "this" in loadedEnv to a sentinel so the script
 * can refer to it without surfacing an "undefined identifier"
 * error. The returned value is discarded; the load step builds
 * its own result sMap by walking loadedEnv's local closures.
 * This is enough to support the master-job style helpers and
 * lets the loaded script keep its idiomatic `return this`
 * trailer.
 */
package pipeline

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
)

/* registerLoadSteps installs the 18.21 load step on the
 * runtime. Called from registerCoreSteps so the native surface
 * stays discoverable in one place. */
func registerLoadSteps(rt *scriptRuntime) {
    rt.globals.define("load",
        &sNative{name: "load", fn: nativeLoad})
}

/* nativeLoad is the body of the load step. Expected shape:
 *
 *   load <path-string>
 *
 * The path is interpreted relative to the runtime's workspace
 * when relative, absolute when it begins with '/'. Returns an
 * sMap whose keys are the loaded script's top-level def names
 * and whose values are the matching closures. */
func nativeLoad(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf("load: missing path argument")
    }
    pathStr, ok := args[0].(*sStr)
    if !ok {
        return nil, fmt.Errorf(
            "load: path must be a string (got %T)", args[0])
    }
    full := pathStr.v
    if !filepath.IsAbs(full) {
        if rt.workspace == "" {
            return nil, fmt.Errorf(
                "load: relative path %q but no workspace "+
                    "configured", full)
        }
        full = filepath.Join(rt.workspace, full)
    }
    data, err := os.ReadFile(full)
    if err != nil {
        return nil, fmt.Errorf("load: read %s: %w", full, err)
    }
    script, err := ParseScript(data)
    if err != nil {
        return nil, fmt.Errorf(
            "load: parse %s: %w", full, err)
    }

    /* loadedEnv is a child of rt.globals so the loaded script
     * sees runtime natives and any root-level bindings the
     * caller set (the script's `env.JOB_NAME` resolves
     * through this chain). Pre-define `this` so the loaded
     * script's trailing `return this` does not error on an
     * undefined identifier; the value is unused, since we
     * snapshot closures directly from loadedEnv below. */
    loadedEnv := newEnv(rt.globals)
    loadedEnv.define("this", &sNull{})

    if err := evalBlock(ctx, rt, loadedEnv,
        script.Block); err != nil {
        /* A `return <expr>` statement at the script's top
         * level surfaces as *retSignal. That is the loaded
         * script's normal exit (jenkinsUtils.groovy ends with
         * `return this`); only non-return errors propagate. */
        if _, ok := err.(*retSignal); !ok {
            return nil, fmt.Errorf(
                "load: execute %s: %w", full, err)
        }
    }

    /* Snapshot the loaded script's top-level closures into a
     * fresh sMap so the caller can do `utils.method(args)`
     * via the existing memberAccess + invokeCallable path.
     * Non-closure top-level bindings are ignored - the
     * jenkinsUtils style declares helpers as `def name(...)`
     * functions, and treating raw values as map members
     * would expose internal implementation state the script
     * did not opt-in to. */
    out := newMap()
    loadedEnv.mu.Lock()
    for name, v := range loadedEnv.vars {
        if _, ok := v.(*sClosure); ok {
            out.set(name, v)
        }
    }
    loadedEnv.mu.Unlock()
    return out, nil
}
