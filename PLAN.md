# PLAN.md - wolfCI Working Plan

This is the durable, machine-readable plan for building wolfCI. It is
the source of truth for what to do next. PLAN.md contains ONLY
unfinished work; completed work lives in PLAN.historic.

Every /loop iteration:

1. Finds the first unchecked task under "## Current Phase".
2. Completes it (TDD: failing test first, then implementation).
3. MOVES the completed task (entire bullet, including its
   sub-bullets) from PLAN.md to PLAN.historic, appending to the
   matching Phase section there (creating it if absent).
4. When the active phase has no unchecked top-level tasks left,
   moves the phase header and any remaining phase-level context to
   PLAN.historic and advances "## Current Phase" to the next phase
   that still has open work.
5. Commits, merges to main, rebuilds, pushes.

Format conventions:

- `[ ]` open, `[~]` blocked or punted (with a note on the same
  line explaining why and what unblocks it). `[x]` is reserved
  for sub-bullets that exist as historical context for an OPEN
  parent item (e.g. completed sub-checkpoints of an open task).
  Free-standing `[x]` items do not appear in PLAN.md; they move
  to PLAN.historic the moment they are finished.
- Each task names a test file or describes the failing test that
  gates the implementation.
- ASCII only. No emdash. No fancy quotes.
- Sub-bullets are allowed for detail but the top-level numbered line
  is the unit of progress.

## Current Phase

Phase 18 - Pipeline DSL + GHPRB master-job execution.

## Phase 18 - Pipeline DSL + GHPRB master-job execution

Goal: parse and execute a declarative-pipeline Jenkinsfile of the
shape used in
third_party/testing/Jenkins/master-job/PRB.Jenkinsfile, drive it
from a GHPRB trigger that polls GitHub for new PRs, and report
status back to GitHub. By the end of this phase the master-job
Jenkinsfile must run against a fake GitHub server and fake child
jobs in an integration test, and every .Jenkinsfile under
third_party/testing/Jenkins must at least PARSE without error.

Decisions locked in for Phase 18 (confirmed with the project
owner at the start of the phase):

- DSL approach: hand-rolled Groovy subset lexer + parser +
  interpreter in Go, targeting the subset actually used in
  third_party/testing/Jenkins. The existing Jenkinsfiles run
  verbatim - no translation.
- Credential storage: config-files/credentials/<id>.sealed,
  each file an AES-256-GCM ciphertext over a small JSON record
  ({type, payload, created_at, label}). Per-credential nonce
  (random 12 bytes), authentication tag appended. The seal key
  is derived via HKDF-SHA256 from a server master secret stored
  under config-files/server.yaml ("credential_master_secret:")
  with the credential id as the HKDF info parameter. Cred types
  supported by 18: secret-text, ssh-private-key,
  username-password. wolfCrypt-native primitives only (wc_AesGcm*
  + wc_HKDF), no OpenSSL-compat anywhere.
- ssh-agent: wolfssh-based SSH agent protocol implementation.
  Before any sshagent step work, inspect third_party/wolfssh for
  agent-protocol support; if missing, ADD the wrapper to
  third_party/go-wolfssl/wolfssh/ (and to the patch series under
  third_party/go-wolfssl-patches/). No fallback to OpenSSH's
  ssh-agent binary - wolfCI must be self-contained.
- GHPRB poll rate: per-job, with pollIntervalSeconds in the job
  YAML, default 300. Polling is the only trigger source this
  phase (webhooks deferred to backlog).
- Plugin-provided steps used only by non-master Jenkinsfiles
  (docker.image, copyArtifacts) are DEFERRED. They live in a
  follow-on phase. Phase 18 ships enough DSL to run the
  master-job and to PARSE every other Jenkinsfile in
  third_party/testing/Jenkins; execute-able coverage of the
  downstream files comes later.
- Out of scope for Phase 18 (per the screenshot review): block
  build if certain jobs are running, do-not-allow-concurrent-
  builds, do-not-resume-on-controller-restart, use-default-
  load-balancer, permission-to-copy-artifact, pipeline-speed
  override, use-snapshots, preserve-stashes, rebuild-options
  (any), parameterized projects, throttle-builds, override-
  build-parameters, properties-file/content, script-file/content,
  groovy-script and groovy-sandbox (any inline Groovy outside the
  pipeline {} top level), additional-classpath, load-from-
  controller, build-after-other-projects, build-periodically,
  github-hook-for-GITScm-polling, poll-SCM, trigger-builds-
  remotely.

- [ ] 18.15 Pipeline interpreter - script {} block execution
        (internal/pipeline/exec_script.go). Failing test:
        TestExec_ScriptParallel runs a script block that builds
        a map of three named closures and calls
        `parallel <map>`; all three closures execute (echo
        captures their messages), and the build succeeds. A
        second test asserts that throwing inside one closure
        fails the build and the other closures still finish.
- [ ] 18.16 Step library: sh, echo, sleep, error
        (internal/pipeline/steps_core.go). Failing test:
        TestStep_ShReturnStatus exercises
        `sh(returnStatus: true, script: 'exit 7')` and asserts
        the integer return value. TestStep_ShReturnStdout
        captures `sh(returnStdout: true, script: 'echo hi')`.
        TestStep_Error makes the build fail. TestStep_Sleep
        sleeps for a short duration and continues.
- [ ] 18.17 Step library: cleanWs, dir, stash, unstash,
        archiveArtifacts (internal/pipeline/steps_workspace.go).
        Failing test: TestStep_StashUnstashRoundTrip stashes a
        file in stage A, runs an intervening cleanWs, unstashes
        in stage B, asserts the file is back.
        TestStep_ArchiveArtifacts saves a file under
        builds/<job>/<n>/artifacts/.
- [ ] 18.18 Step library: withCredentials (secret-text only for
        this task) (internal/pipeline/steps_creds.go). Failing
        test: TestStep_WithCredentialsSecretText loads a
        secret-text cred from internal/credstore (created
        in-memory), wraps a sh step in
        `withCredentials([string(credentialsId: 'c1',
        variable: 'TOKEN')]) { sh 'echo $TOKEN' }`, asserts the
        sh sees the unsealed secret in its environment and
        that the secret is masked in the log output.
- [ ] 18.19 wolfssh SSH agent investigation. Failing test
        (third_party/go-wolfssl/wolfssh/agent_test.go in the
        patch series): TestAgent_AddListSign loads an Ed25519
        key into a wolfssh-backed agent, lists identities over
        the SSH agent protocol (RFC 4253-style framed messages),
        signs a challenge. If wolfssh does not yet implement
        agent protocol message handling, ADD the wrapper to
        the vendored copy and capture as
        third_party/go-wolfssl-patches/0NNN-wolfssh-agent.patch.
- [ ] 18.20 Step library: sshagent
        (internal/pipeline/steps_ssh.go). Failing test:
        TestStep_SshagentGitClone loads an ssh-private-key cred
        from credstore, wraps `sh "git clone git@..."` in
        sshagent { ... }, asserts that the wrapped git call
        connects via wolfssh's agent socket (a temp
        SSH_AUTH_SOCK in the build workspace) and succeeds
        against a local SSH server fixture. Uses the wolfssh
        agent from 18.19. NO openssh ssh-agent binary.
- [ ] 18.21 Step library: load
        (internal/pipeline/steps_load.go). Failing test:
        TestStep_LoadHelperGroovy loads
        third_party/testing/Jenkins/jenkins-functions/
        jenkinsUtils.groovy from a build workspace, then calls
        each of its top-level helpers (cleanupName,
        getJobResultName, commitHashForBuild, getLastBuild,
        checkIfPassed, shouldTestRetry) with synthesized
        inputs and asserts the right outputs.
- [ ] 18.22 Step library: build (downstream job dispatch)
        (internal/pipeline/steps_build.go). Failing test:
        TestStep_BuildTriggersDownstream invokes
        `build job: 'child-1', parameters: [string(name: 'P',
        value: 'v')]` with propagate: false, asserts a child
        Build is enqueued under the wolfCI scheduler, runs to
        completion, and the returned object exposes
        .getResult() = 'SUCCESS'. Second test: propagate: true
        + child fails -> parent build fails.
- [ ] 18.23 Step library: catchError
        (internal/pipeline/steps_catcherr.go). Failing test:
        TestStep_CatchErrorRecords wraps a failing sh in
        catchError(buildResult: 'FAILURE', stageResult:
        'FAILURE'), asserts the stage is recorded FAILURE,
        the build overall is FAILURE, but subsequent steps in
        the same script block still execute.
- [ ] 18.24 Step library: nested node()
        (internal/pipeline/steps_node.go). Failing test:
        TestStep_NestedNodeReallocates runs a pipeline whose
        outer agent is label 'A' and whose script block calls
        node('B') { sh 'hostname' }; asserts the sh inside the
        node('B') block ran on the label-B executor (using a
        fake two-executor scheduler in the test). Mirrors how
        master-job's parallel closures call node('macos') etc.
- [ ] 18.25 currentBuild + previous-build API surface
        (internal/pipeline/builtins_currentbuild.go). Failing
        test: TestBuiltin_CurrentBuildPrev exposes
        currentBuild.getDisplayName(),
        currentBuild.getPreviousBuild(),
        previousBuild.getBuildVariables(),
        previousBuild.rawBuild.getEnvironment(). Same shape as
        jenkinsUtils.groovy uses (getLastBuild,
        commitHashForBuild). Backed by Phase 4's job/build store.
- [ ] 18.26 Pipeline-script-from-SCM job definition. Failing
        test (internal/jobspec/scm_test.go):
        TestJob_PipelineScriptFromSCM loads a job whose
        pipeline.definition is "from_scm" with repo URL,
        credentials id (ssh-private-key), branch_specifier
        */master, script_path "Jenkins/master-job/PRB.Jenkinsfile",
        lightweight_checkout: true. At build start the runner
        does a SHALLOW fetch of just the script path (or full
        clone if lightweight is false), parses, runs.
        TestJob_PipelineScriptFromSCM_BadBranch exercises the
        no-such-branch error path.
- [ ] 18.27 General job options form fields. Failing test
        (internal/server/jobedit_test.go):
        TestJobEdit_GeneralOptionsRoundtrip POSTs a form with
        description (free text), discard_old_builds.strategy
        = log_rotation, discard_old_builds.max_builds = 30,
        discard_old_builds.days_to_keep = "" (blank),
        github_project_url, asserts the job YAML matches.
- [ ] 18.28 Build-environment toggles. Failing test:
        TestJobEdit_BuildEnvRoundtrip POSTs a form with
        prepare_environment_for_run = true,
        keep_jenkins_environment_variables = true,
        keep_jenkins_build_variables = true; the corresponding
        BuildEnv flags appear in the job YAML and cause the
        running build to inherit server-level env vars +
        previous build vars accordingly. (Build vars
        inheritance: a build started after a successful prior
        build sees that build's exported env entries unless
        the new build sets the same key.)
- [ ] 18.29 UI: form fields for GHPRB trigger and Pipeline-
        from-SCM definition. Failing test:
        TestJobEdit_FormViewHasGHPRBSection asserts the
        rendered form has inputs for api_credentials_id (a
        <select> populated from credstore), gh_project_url,
        admin_users (multi-line textarea, one user per line),
        branches_to_build (multi-line), poll_interval_seconds
        (number, default 300). Also asserts a Pipeline-from-
        SCM panel: repo_url, credentials_id, branch_specifier,
        script_path, lightweight_checkout (checkbox).
- [ ] 18.30 End-to-end: master-job PRB.Jenkinsfile drives a
        fake-PR fan-out. Failing test
        (tests/prb_master_job_test.go): spins up a wolfCI
        server, a fake GitHub API server, a local git fixture
        repo that contains a vendored copy of the master-job
        Jenkinsfile under Jenkins/master-job/PRB.Jenkinsfile
        and a stub jenkinsUtils.groovy, three fake child jobs
        ("Group1", "Group2", "Group3") that each just echo
        their name. The fake GitHub server publishes one open
        PR. The wolfCI poller fires, the master job runs,
        fans out via parallel to all three children, all
        succeed, the master posts a "success" status under
        context "PRB-master-job" to the fake GitHub server,
        and tests/prb_master_job_test.go asserts every link in
        the chain (poll observed, three children dispatched
        with the right ghprb* env, master status posted).
- [ ] 18.31 Parse-smoke test across third_party/testing.
        Failing test (tests/jenkinsfile_parse_smoke_test.go):
        walks third_party/testing/Jenkins for every file
        named *.Jenkinsfile or Jenkinsfile, runs each through
        internal/pipeline.Parse, asserts zero parse errors.
        Files known to use plugin-provided steps that we
        intentionally defer (docker.image, copyArtifacts,
        etc.) are still expected to PARSE - the steps are
        registered as no-op stubs in this test only, so the
        parser sees a known step name. The list of "execute-
        able" pipelines (a subset that 18.30-style integration
        tests cover) is recorded in
        docs/pipeline-coverage.md.

## Backlog (not in main flow)

Items that came up but are not on the critical path. Promote into a
phase when they become relevant.

- Persistent metrics and a built-in Prometheus exporter.
- Multi-master HA (one process per install for v1).
- Windows agent support (Linux + macOS first).
- LDAP, SAML, OIDC SSO (after core auth lands).
- Quiet the macOS linker warnings about wolfSSL objects targeting a
  newer macOS than the Go stdlib link target. Likely fix: set
  -mmacosx-version-min in scripts/build-wolfssl.sh CFLAGS so both
  sides agree on a deployment target.
- Per-conn read/write serialization in internal/tlsutil. wolfSSL
  is not safe for concurrent wolfSSL_read + wolfSSL_write on the
  same WOLFSSL*; today the package assumes the caller serializes
  (which is fine for one reader / one writer net.Conn use, but
  should be made explicit).
- GCE startup-script fill-in: install wolfci-agent on the booted
  VM, distribute cert material (GCE Secret Manager preferred),
  drop config-files/agent.yaml with the spawn-time agent_id and
  label, start the agent under systemd. Until this lands the
  GCE Provisioner CREATES VMs but the VMs do not actually join
  the wolfCI cluster.
- FileLogSink in internal/agentsvc that persists LogChunks under
  builds/<job>/<n>/log.live so the Phase 6 UI can tail an actual
  file on disk. Today the server-side LogSink is just an
  interface and a capturing in-memory implementation lives in
  the test.
- GHPRB webhook receiver (HMAC-validated) as an alternative to
  per-job polling. Phase 18 ships polling only; webhooks come
  later when wolfCI has a stable public ingress story.
- Plugin steps not covered in Phase 18: docker.image() (with
  .pull / .inside), copyArtifacts (Copy Artifact plugin), junit,
  emailext, lock, timestamps. Promote into a phase when a
  Jenkinsfile we need to run actually uses them.
- Pipeline post {} blocks (always / success / failure / unstable
  / cleanup). The master-job does not use post {}; downstream
  files do.
- Pipeline parameters {} block (string / choice / boolean /
  password). Phase 18 passes parameters via the build step's
  `parameters:` argument only. Promote when needed.
- Boolean label expressions in agent { label '...' }. Phase 5
  decided these are backlog; the master-job uses
  "master_linux_group || linux-cloud-node" which Phase 18 must
  handle as the simplest form. Full Jenkins-style expressions
  (linux && tpm && !(arm)) come later.

End of PLAN.md.
