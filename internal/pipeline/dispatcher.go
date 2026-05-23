/* internal/pipeline/dispatcher.go - PLAN.md 18.22.
 *
 * BuildDispatcher is the seam the `build` step uses to enqueue
 * downstream jobs into the wolfCI scheduler. It exists as an
 * interface (not a direct scheduler dependency) so the pipeline
 * package keeps a narrow surface: every concrete adapter
 * (production wiring in cmd/wolfci, test-local fakes that
 * synthesize child builds) lives outside this package and
 * supplies the BuildDispatcher implementation through
 * LocalExecutor.Dispatcher. Decoupling the step library from
 * scheduler internals also keeps the pipeline package free of
 * internal/scheduler + internal/storage imports it would
 * otherwise need just to type-thread the call.
 *
 * The interface intentionally hides Scheduler / Storage shapes
 * behind plain Go types (string job names, simple parameter
 * tuples, a small ChildBuildResult). Callers that need richer
 * routing (label routing, parameter typing, queue priority)
 * can grow the interface later without ripping the call sites
 * apart.
 */
package pipeline

import "context"

/* BuildParam is one (name, value) tuple from the Jenkinsfile's
 * `parameters: [string(name: 'P', value: 'v')]` list. For
 * 18.22 we only carry string-typed parameters; richer typing
 * (boolean, file, password) lands when downstream jobs
 * actually consume the difference. */
type BuildParam struct {
    Name  string
    Value string
}

/* ChildBuildResult is the wire shape the `build` step's
 * native dispatcher returns through the result channel. The
 * fields mirror the bits the Jenkinsfile actually inspects:
 *
 *   Job    - the downstream job's name, useful for diagnostic
 *            messages and for the `currentBuild.fullProjectName`
 *            -style accessors that future tasks plumb in.
 *   Number - the build number the scheduler assigned, exposed
 *            via the returned object's `.getNumber()`.
 *   Status - the canonical UPPERCASE label Jenkins emits
 *            ("SUCCESS", "FAILURE", "UNSTABLE", "ABORTED").
 *            The `build` native maps the dispatcher's status
 *            string straight through, so adapters should pre-
 *            translate scheduler-side statuses (lowercase
 *            "success" / "failure") into the Jenkins form
 *            before sending on the channel.
 */
type ChildBuildResult struct {
    Job    string
    Number int
    Status string
}

/* BuildDispatcher is the seam the build step uses to fan a
 * downstream job out to whatever scheduler the server wires
 * in. The Scheduler+Storage adapter implementations live
 * outside this package so internal/pipeline carries no
 * dependency on internal/scheduler.
 *
 * Contract:
 *   - jobName identifies the downstream job; the adapter is
 *     responsible for resolving it to a storage.Job (or
 *     equivalent) and surfacing "no such job" as a non-nil
 *     error before any channel is returned.
 *   - params is the unrolled `parameters:` list from the
 *     pipeline source. Adapters may translate these into
 *     scheduler-specific env-var prefixes (WOLFCI_INPUTS,
 *     etc.) or echo them straight into the child's env -
 *     18.22 does not constrain that.
 *   - The returned channel emits exactly one ChildBuildResult
 *     when the child finishes, then closes. Adapters should
 *     not buffer more than one result.
 *   - ctx cancellation surfaces as the child build being
 *     aborted; the channel still emits a result (Status
 *     "ABORTED").
 */
type BuildDispatcher interface {
    EnqueueChildBuild(ctx context.Context, jobName string,
        params []BuildParam) (<-chan ChildBuildResult, error)
}
