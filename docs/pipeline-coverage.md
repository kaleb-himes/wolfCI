# Pipeline coverage

This document tracks which Jenkinsfiles in the
`third_party/testing/Jenkins/` corpus wolfCI can do what with:

- **Parse**: every Jenkinsfile in the corpus parses cleanly
  through `internal/pipeline.Parse`. Gated by
  `TestJenkinsfile_ParseSmoke` in
  `tests/jenkinsfile_parse_smoke_test.go` (PLAN.md 18.31).
- **Execute**: a subset of Jenkinsfiles run end to end
  through `internal/jobspec.RunFromSCM` against a real
  scheduler with fake child jobs. Gated by per-pipeline
  integration tests under `tests/`.

The split exists because the parser accepts forward-
compatible shapes (sections wolfCI does not yet evaluate,
plugin steps without runtime support) so the corpus is one
stable suite; the executable subset grows phase by phase as
runtime support catches up.

## Parse-smoke (18.31)

Every file matching `*.Jenkinsfile` or `Jenkinsfile` under
`third_party/testing/Jenkins/` parses without error.
Currently that's 79 files. The smoke test walks the tree on
every run, so adding a new file to the corpus is enough -
no allowlist to maintain.

The parser accepts the following forward-compatible shapes
(parsed as opaque blocks, runtime support pending):

- `environment { ... }` pipeline section.
- `triggers { ... }` pipeline section.
- `parameters { ... }` pipeline section.
- `tools { ... }` pipeline section.
- `libraries { ... }` pipeline section.
- `when { anyOf | allOf | triggeredBy | branch | ... }`
  conditions (only `when { expression { ... } }` is
  evaluated today; other forms parse and the stage runs
  unconditionally).
- `agent { label <ident> }` (the ident form, in addition
  to the already-supported `label '<string>'`).

Plugin-provided step names (e.g. `docker.image`,
`copyArtifacts`, `junit`, `emailext`, `lock`, `timestamps`)
parse as generic function calls; runtime no-op stubs are
the lowest-cost path to executable coverage when needed.

## Executable (18.30 + follow-ons)

| Jenkinsfile | Integration test | Status |
| --- | --- | --- |
| `master-job/PRB.Jenkinsfile` (simplified inline copy) | `tests/prb_master_job_test.go::TestPRB_MasterJobFanOut` | Executable end to end via `RunFromSCM` + fake GitHub server + 3 fake children. |

The simplified Jenkinsfile is a stand-in for the real
`master-job/PRB.Jenkinsfile` (vendored under
`third_party/testing/Jenkins/master-job/`). Running the
real file through `RunFromSCM` end to end is tracked in
the Phase 19 backlog - it needs additional step
implementations (`load`-resolved helper chains across many
groups, retries, the preflight stage's
`withCredentials + sh + currentBuild.result` mutation),
plus a way to dispatch ~50 downstream jobs concurrently
without overwhelming the test harness.

## Adding a new pipeline to the executable subset

1. Vendor (or copy + simplify) the Jenkinsfile into a test
   fixture under `tests/<name>/`.
2. Write a `tests/<name>_test.go` that wires fake child
   jobs, a credstore (when the Jenkinsfile uses
   `withCredentials`), and any fake external services the
   pipeline reaches for (GitHub, Slack, email).
3. Use `jobspec.RunFromSCM` + a test-local
   `pipeline.BuildDispatcher` adapter to drive execution.
4. Document the addition in the **Executable** table above.

## Backlog (sections not yet executable)

- `environment { KEY = 'value' }` - export job-level env
  variables. Stub parses; runtime currently ignores.
- `triggers { cron('@daily') }` - in-pipeline trigger
  declarations. Stub parses; runtime currently ignores
  (operators configure triggers via the job YAML's
  `triggers:` array instead).
- `parameters { string(name: 'X', defaultValue: 'y') }` -
  declarative param block. Stub parses; runtime currently
  ignores.
- `tools { ... }` - tool-installer hooks. Stub parses;
  runtime currently ignores.
- `libraries { lib('shared-lib') }` - shared-library
  references. Stub parses; runtime currently ignores.
- Plugin steps: `docker.image()`, `copyArtifacts`,
  `junit`, `emailext`, `lock`, `timestamps`. Parse;
  runtime would need per-step natives.
- `when { anyOf | allOf | triggeredBy | branch | ... }` -
  non-expression conditions. Parses; runtime treats as
  "always true" for now.
- Pipeline `post { ... }` block execution. Parses; runtime
  is wired for `post` in the AST but does not yet route
  per-result handlers (always / success / failure /
  unstable / cleanup).

Each backlog item lands when a phase needs it - the
parse-smoke gate stays green either way.
