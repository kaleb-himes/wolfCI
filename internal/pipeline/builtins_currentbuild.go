/* internal/pipeline/builtins_currentbuild.go - PLAN.md 18.25.
 *
 * Exposes the Jenkinsfile-facing `currentBuild` global and the
 * matching .getPreviousBuild() chain. The shape mirrors the
 * Jenkins surface jenkinsUtils.groovy + the master-job reach
 * for:
 *
 *   currentBuild.getDisplayName()           -> "#<num>" by default
 *   currentBuild.getNumber()                -> uint
 *   currentBuild.getResult()                -> "SUCCESS"/"FAILURE"/...
 *   currentBuild.getPreviousBuild()         -> previousBuild | null
 *   previousBuild.getBuildVariables()       -> Map<String, String>
 *   previousBuild.rawBuild.getEnvironment() -> Map<String, String>
 *
 * The seam is the BuildInfoProvider interface: a fresh
 * scriptRuntime resolves "currentBuild" by asking the provider
 * for the in-flight build's BuildInfo, and getPreviousBuild
 * walks back through the same provider. Production wiring
 * (server / scheduler) supplies an adapter that reads
 * builds/<job>/<n>/ on disk; the gating test in
 * builtins_currentbuild_test.go feeds a tiny in-memory map.
 *
 * Why a separate interface (not just a struct on
 * LocalExecutor): currentBuild walks backward through history
 * (build n -> n-1 -> n-2 -> ...) so the runtime needs more
 * than the in-flight build's own metadata. The provider hides
 * that lookup behind a single seam so the pipeline package
 * stays free of internal/storage + internal/scheduler imports.
 *
 * Nil provider: the global stays absent (the IdentExpr
 * lookup surfaces the usual "undefined identifier
 * currentBuild" error). The provider is opt-in - tests that
 * exercise the rest of the step library do not have to wire
 * it.
 */
package pipeline

import (
    "context"
)

/* BuildInfo carries the metadata one build exposes to its
 * Jenkinsfile through currentBuild / previousBuild. Fields
 * are simple Go types so adapters across packages do not need
 * to import the pipeline package's value model.
 *
 *   JobName             - parent job name (e.g. "wolfssl-prb").
 *   Number              - the build number the scheduler
 *                         assigned.
 *   DisplayName         - the human-readable label
 *                         currentBuild.getDisplayName()
 *                         returns. Adapters often set this to
 *                         "#<num>", but Jenkins lets a job
 *                         override it (date stamps,
 *                         per-branch labels).
 *   Result              - the canonical Jenkins status the
 *                         build settled into ("SUCCESS",
 *                         "FAILURE", "ABORTED", "UNSTABLE",
 *                         "NOT_BUILT", ""). Empty string means
 *                         "still running" - the in-flight
 *                         build that owns currentBuild does
 *                         not have a settled status yet.
 *   Variables           - the equivalent of Jenkins'
 *                         getBuildVariables(): the
 *                         downstream-visible string KV pairs
 *                         a parent build saw on EXIT (env
 *                         vars the build's steps exported,
 *                         WOLFCI_INPUTS-style parameters from
 *                         the child-side, etc.).
 *   Environment         - the env-var snapshot
 *                         previousBuild.rawBuild.getEnvironment()
 *                         exposes. Adapters typically set
 *                         this to the same map a wolfci-agent
 *                         step ran with (job vars + ghprb*
 *                         vars + env-block vars).
 *   PreviousBuildNumber - the build number to look up when
 *                         the Jenkinsfile asks for
 *                         currentBuild.getPreviousBuild(). 0
 *                         means "no previous build" - the
 *                         getter returns sNull, mirroring
 *                         Jenkins' behavior on the first
 *                         build of a fresh job.
 */
type BuildInfo struct {
    JobName             string
    Number              int
    DisplayName         string
    Result              string
    Variables           map[string]string
    Environment         map[string]string
    PreviousBuildNumber int
}

/* BuildInfoProvider is the seam the currentBuild / previousBuild
 * builtins consult. Implementations live outside the pipeline
 * package: a future production adapter reads builds/<job>/<n>/
 * on disk and walks the chain backwards; tests pass a small
 * in-memory map.
 *
 *   CurrentBuild  - the build the runtime is executing for.
 *                   Returns nil if not configured (currentBuild
 *                   stays absent from the runtime globals).
 *   LookupBuild   - resolves (jobName, number) to a BuildInfo;
 *                   returns nil when no such build exists so
 *                   the caller can surface a clean sNull
 *                   through .getPreviousBuild().
 */
type BuildInfoProvider interface {
    CurrentBuild() *BuildInfo
    LookupBuild(jobName string, number int) *BuildInfo
}

/* registerCurrentBuild installs the `currentBuild` global on
 * the runtime when a provider is wired. Without a provider the
 * global is omitted and the usual undefined-identifier error
 * surfaces for any code that reaches for it - which matches
 * the rest of the runtime's "no seam wired = clear error"
 * pattern (NodeRouter, BuildDispatcher, Creds, ...). */
func registerCurrentBuild(rt *scriptRuntime) {
    if rt.builds == nil {
        return
    }
    info := rt.builds.CurrentBuild()
    if info == nil {
        return
    }
    rt.globals.define("currentBuild",
        buildInfoToValue(rt, info))
}

/* buildInfoToValue renders a BuildInfo into the sMap the
 * Jenkinsfile-facing API expects. The map is populated with:
 *
 *   number / displayName / result / projectName / fullProjectName
 *       - bare-field aliases, so the property-access form
 *         (`currentBuild.number`) works alongside the method
 *         form.
 *   getNumber / getDisplayName / getResult / getProjectName
 *   getFullProjectName / getBuildVariables / getEnvironment
 *       - sNative method dispatchers; same payload as the
 *         bare-field aliases, just wrapped in a callable
 *         envelope so .getNumber() and .number both yield the
 *         same value.
 *   getPreviousBuild
 *       - looks up the previous BuildInfo through the
 *         provider and recurses through buildInfoToValue so
 *         the returned object exposes the same surface. Nil
 *         provider OR no previous build OR provider lookup
 *         miss all yield sNull.
 *   rawBuild
 *       - mirrors Jenkins' `build.rawBuild` indirection by
 *         exposing the SAME object: callers can chain
 *         `previousBuild.rawBuild.getEnvironment()` and get
 *         the environment map without the rawBuild
 *         indirection costing extra plumbing.
 */
func buildInfoToValue(rt *scriptRuntime,
    info *BuildInfo) *sMap {
    out := newMap()
    /* Bare-field aliases. */
    out.set("number", &sNum{v: int64(info.Number)})
    out.set("displayName", &sStr{v: info.DisplayName})
    out.set("result", &sStr{v: info.Result})
    out.set("projectName", &sStr{v: info.JobName})
    out.set("fullProjectName", &sStr{v: info.JobName})

    /* Method getters. Captured info pointer is fine - the
     * BuildInfo struct is immutable from the runtime's
     * perspective. */
    addMethod(out, "getNumber",
        func() scriptValue {
            return &sNum{v: int64(info.Number)}
        })
    addMethod(out, "getDisplayName",
        func() scriptValue {
            return &sStr{v: info.DisplayName}
        })
    addMethod(out, "getResult",
        func() scriptValue { return &sStr{v: info.Result} })
    addMethod(out, "getProjectName",
        func() scriptValue { return &sStr{v: info.JobName} })
    addMethod(out, "getFullProjectName",
        func() scriptValue { return &sStr{v: info.JobName} })
    addMethod(out, "getBuildVariables",
        func() scriptValue {
            return stringMapToScript(info.Variables)
        })
    addMethod(out, "getEnvironment",
        func() scriptValue {
            return stringMapToScript(info.Environment)
        })

    /* getPreviousBuild walks the chain via the provider. */
    job := info.JobName
    prevNum := info.PreviousBuildNumber
    out.set("getPreviousBuild", &sNative{name: "getPreviousBuild",
        fn: func(ctx context.Context, r *scriptRuntime,
            _ []scriptValue) (scriptValue, error) {
            if r.builds == nil || prevNum <= 0 {
                return &sNull{}, nil
            }
            prev := r.builds.LookupBuild(job, prevNum)
            if prev == nil {
                return &sNull{}, nil
            }
            return buildInfoToValue(r, prev), nil
        }})

    /* rawBuild mirrors Jenkins' indirection: the same object
     * is the raw-side handle. Method chains like
     * previousBuild.rawBuild.getEnvironment() resolve through
     * memberAccess(rawBuild) -> the same sMap, then
     * getEnvironment() -> the env map. */
    out.set("rawBuild", out)
    return out
}

/* addMethod registers an sNative on the supplied map whose
 * payload is computed by produce. Keeps each method-getter
 * registration to one line at the call site so
 * buildInfoToValue reads as a flat list of fields + accessors.
 */
func addMethod(m *sMap, name string,
    produce func() scriptValue) {
    m.set(name, &sNative{name: name,
        fn: func(ctx context.Context, r *scriptRuntime,
            _ []scriptValue) (scriptValue, error) {
            return produce(), nil
        }})
}

/* stringMapToScript renders a Go map[string]string as the
 * pipeline sMap the Jenkinsfile API expects. Iteration order
 * follows Go's standard map randomisation - callers that need
 * a deterministic order should pre-sort the keys before
 * materialising. */
func stringMapToScript(m map[string]string) *sMap {
    out := newMap()
    for k, v := range m {
        out.set(k, &sStr{v: v})
    }
    return out
}
