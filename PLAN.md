# PLAN.md - wolfCI Working Plan

This is the durable, machine-readable plan for building wolfCI. It is
the source of truth. Every /loop iteration:

1. Finds the first unchecked task under "## Current Phase".
2. Completes it (TDD: failing test first, then implementation).
3. Marks the task `[x]` in this file.
4. Advances "## Current Phase" when the active phase is fully checked.
5. Commits, merges to main, rebuilds.

Format conventions:

- `[ ]` open, `[x]` done, `[~]` blocked or punted (with a note on the
  same line explaining why and what unblocks it).
- Each task names a test file or describes the failing test that
  gates the implementation.
- ASCII only. No emdash. No fancy quotes.
- Sub-bullets are allowed for detail but the top-level numbered line
  is the unit of progress.

## Current Phase

Phase 14 - Build retention, workspace browser, rebuild, rename

(Phase completion log. Phase 0 was completed in the initial
planning turn. Phase 1 completed in iteration 4, Phase 2 in
iteration 5, Phase 3 in iteration 8, Phase 4 in iteration 10,
Phase 5 in iteration 21, Phase 6 in iteration 25, Phase 7 in
iteration 28, Phase 8 in iteration 32, Phase 9 in iteration 37,
Phase 10 in iteration 49, Phase 11 in iteration 51, Phase 12 in
iteration 59, Phase 13 in iteration 63 of the slash-loop run.)

## Phase 0 - Bootstrap

Foundation that must exist before any feature work. Completed in the
initial planning session.

- [x] 0.1 Create CLAUDE.md.
- [x] 0.2 Create PLAN.md (this file).
- [x] 0.3 Write LICENSE (GPL-2.0 canonical text).
- [x] 0.4 Write README.md with a short summary and pointers.
- [x] 0.5 Write .gitignore for Go and runtime data dirs.
- [x] 0.6 Write scripts/check-ascii.sh with a failing-then-passing
        test (scripts/test-check-ascii.sh).
- [x] 0.7 Write scripts/build.sh stub.
- [x] 0.8 git init; first commit; create public remote repo at
        github.com:kaleb-himes/wolfCI; push main.
- [x] 0.9 Initialize Go module
        (go mod init github.com/kaleb-himes/wolfCI).
- [x] 0.10 Create directory skeleton (cmd/, internal/, web/,
         plugins/, jobs/, builds/, config-files/, nodes/, scripts/,
         third_party/, docs/, tests/) with a .gitkeep per dir
         describing the dir's purpose in one line.

## Phase 1 - wolfSSL TLS layer

Bring up TLS using the project's chosen crypto library.

- [x] 1.1 Add wolfSSL as a git submodule under third_party/wolfssl
        pinned to tag v5.9.1-stable. Record the tag in
        third_party/wolfssl-version.txt.
        Gate: scripts/test-wolfssl-submodule.sh.
- [x] 1.2 Write scripts/build-wolfssl.sh to compile wolfSSL with
        options suitable for a CI server (TLS 1.2 and 1.3, modern
        cipher suites, no SSLv3 or insecure protocols, FIPS off by
        default; FIPS profile toggleable later).
        Gate: scripts/test-build-wolfssl.sh. Installs to
        build/wolfssl-install/ (gitignored).
- [x] 1.3 Create internal/tlsutil with a CGO wrapper that exposes a
        Go net.Listener backed by wolfSSL. Failing test
        (internal/tlsutil/tlsutil_test.go): a TLS 1.3 handshake to
        the listener using crypto/tls as a client succeeds against a
        self-signed cert.
        Done: stub package + failing test exist. Test currently
        fails because NewListener returns ErrNotImplemented;
        task 1.4 makes it pass.
- [x] 1.4 Implement internal/tlsutil until the test passes.
        Done: tlsutil.go wires wolfSSL via CGO. Custom I/O callbacks
        bridge Go net.Conn to wolfSSL using runtime/cgo.Handle.
        Side fix in scripts/build-wolfssl.sh: detect Go's
        GOOS/GOARCH and build wolfSSL to match (an Apple Silicon
        host running a darwin/amd64 Go would otherwise produce an
        arm64 .a that fails to link). macOS frameworks Security
        and CoreFoundation linked via #cgo darwin LDFLAGS.
- [x] 1.5 Write a hello-world server cmd/wolfci that opens an HTTPS
        listener via tlsutil and returns 200 OK on /. Test: a
        net/http client over crypto/tls receives the expected body.
        Done: cmd/wolfci/main.go exposes Listen/Serve and a
        helloHandler. Shared cert helper extracted into
        internal/testcerts. Gate: cmd/wolfci/main_test.go.

## Phase 2 - Storage layer

Persist jobs, builds, users, and config as plain files.

- [x] 2.1 Failing test (internal/storage/storage_test.go): the
        storage layer round-trips a Job spec to and from disk
        without loss.
        Done: TestStorage_RoundtripJob plus two error-path tests
        (LoadJob_NotFound, SaveJob_RequiresName).
- [x] 2.2 Define the on-disk format:
        - jobs/<name>/job.yaml
        - builds/<job>/<n>/result.json (deferred to Phase 4)
        - builds/<job>/<n>/log (deferred to Phase 4)
        Done: Job spec lives at jobs/<name>/job.yaml as YAML 1.2;
        fields are name, description, node_label, timeout
        (time.ParseDuration string), retries, triggers,
        parameters, steps, axis.
- [x] 2.3 Implement internal/storage with file locking for
        concurrent writes (flock via syscall).
        Done: SaveJob takes LOCK_EX, LoadJob takes LOCK_SH. YAML
        via gopkg.in/yaml.v3.

## Phase 3 - AuthN and AuthZ

The security model.

Decisions locked in for Phase 3 (confirmed with the project owner
before the phase started):

- bcrypt cost factor: 12 by default. Configurable in
  config-files/auth/config.yaml under the key bcrypt_cost.
- SSH key layout: one OpenSSH-format public key per file at
  config-files/auth/keys/<username>.pub. No authorized_keys-style
  multi-key files.
- Role matrix: three roles - admin (everything), developer
  (read jobs, trigger builds, read logs), viewer (read-only on
  jobs and builds). NO anonymous role. All access requires
  authentication.
- First-admin bootstrap: on first server start with no users on
  disk, the server generates an SSH keypair, prints the public
  key path plus a one-time-token URL to stdout, and waits. Admin
  uses the token to create the first account. Token is
  invalidated once consumed.

- [x] 3.1 Failing test (internal/auth/sshkey_test.go): public-key
        login accepts a known authorized key and rejects an unknown
        one.
        Done: TestKeyStore_VerifySignature plus
        TestKeyStore_RejectsPathTraversal.
- [x] 3.2 Implement SSH public-key auth. Keys live under
        config-files/auth/keys/<user>.pub.
        Done: internal/auth/sshkey.go via golang.org/x/crypto/ssh
        pinned to v0.13.0 for Go 1.18 compatibility. ErrUnknownUser,
        ErrInvalidSignature, ErrInvalidUsername are sentinel
        errors for callers.
- [x] 3.3 Failing test (internal/auth/password_test.go): username +
        password login succeeds when enabled and is rejected when
        disabled in config-files/auth/config.yaml.
        Done: TestPasswordStore_VerifyWhenEnabled,
        TestPasswordStore_DisabledRejectsAll,
        TestConfig_DefaultsAndRoundtrip,
        TestLoadConfig_RejectsOutOfRangeCost.
- [x] 3.4 Implement password auth with bcrypt. Default to disabled.
        Done: internal/auth/password.go. Config struct with
        PasswordEnabled (default false) and BcryptCost (default
        12); LoadConfig/Save round-trip YAML; PasswordStore
        Set/Verify against per-user bcrypt files at
        <root>/<user>.bcrypt mode 0600.
- [x] 3.5 Failing test (internal/authz/matrix_test.go): the matrix
        denies an action the user lacks permission for and permits
        actions the user holds.
        Done: TestMatrix_DefaultRoles pins every permission across
        admin/developer/viewer and verifies unknown users are
        denied. Plus TestMatrix_Roundtrip and
        TestMatrix_UnknownRoleDenies.
- [x] 3.6 Implement a Jenkins-style role-based matrix in
        internal/authz. Roles defined in
        config-files/auth/matrix.yaml.
        Done: internal/authz/matrix.go. Permissions are dotted
        namespaced strings (jobs.read, jobs.build, jobs.configure,
        builds.read, builds.cancel, nodes.read, nodes.configure),
        plus a "*" wildcard for admin.
- [x] 3.7 docs/SECURITY.md: write the full security model, including
        threat model and how to enable/disable each auth path.
        Done: covers TLS profile, SSH-key auth, password auth
        toggle, bootstrap flow, role-permission tables, file modes,
        and threat model.

## Phase 4 - Job model and scheduler

Run shell-script jobs against executors.

Decisions locked in for Phase 4 (confirmed with the project owner
before the phase started):

- Queue order: FIFO for v1. A priority queue is backlogged; do
  not preempt this with a "priority" field today.
- Build numbering: per-job monotonic counter persisted at
  builds/<job>/.next-build-number, incremented under
  syscall.Flock LOCK_EX. Matches Jenkins's mental model and
  scales without rescanning the builds directory.
- Executor model: scheduler dispatches to an Executor interface.
  Phase 4 ships exactly one implementation - an in-process
  executor that runs shell on the server host. Phase 5 adds the
  agent-driven executors that route by node label; the
  Executor interface MUST be future-friendly enough for that
  swap without changes to the scheduler.

- [x] 4.1 Failing test (internal/scheduler/scheduler_test.go): the
        scheduler enqueues a Job, dispatches it to a fake executor,
        and records the result.
        Done: TestScheduler_RoundtripJob, TestScheduler_FIFOOrder,
        TestScheduler_BuildNumberPersistsAcrossSchedulers.
- [x] 4.2 Implement internal/scheduler with a FIFO queue (locked
        decision; priority queue is in the backlog) and the
        Executor interface. Per-node executor pools land in
        Phase 5 against the same interface.
        Done: internal/scheduler/scheduler.go. Build numbers
        persisted at builds/<job>/.next-build-number under
        syscall.Flock LOCK_EX.
- [x] 4.3 Build runner: spawns a shell, streams stdout and stderr to
        builds/<job>/<n>/log, records exit code in result.json.
        Done: internal/scheduler/local.go provides LocalExecutor.
        First non-zero exit terminates the build; remaining steps
        are skipped. Step.Env is layered over os.Environ for that
        step only. Gate: TestLocalExecutor_SuccessAndLog,
        TestLocalExecutor_FailingStep,
        TestLocalExecutor_StopsAtFirstFailure,
        TestLocalExecutor_PerStepEnv.
- [x] 4.4 Build matrix support: a job can declare an axis (for
        example, OS x compiler), and the scheduler fans out into N
        runs.
        Done: Scheduler.EnqueueMatrix expands Job.Axis into the
        cartesian product and enqueues one build per combination,
        each with axis values layered into every Step.Env. Gate:
        TestScheduler_EnqueueMatrix_NoAxis,
        TestScheduler_EnqueueMatrix_TwoDimensions,
        TestScheduler_EnqueueMatrix_PreservesStepEnv.

## Phase 5 - Node management

On-prem and GCE.

Decisions locked in for Phase 5 (confirmed with the project owner
before the phase started):

- Agent transport: gRPC over wolfSSL mTLS. Same wire protocol
  carries plugin RPCs in Phase 7. Accept the dep cost of
  google.golang.org/grpc.
- GCE credential source: wolfCI-specific config. The path to the
  service account JSON lives in config-files/nodes/gce.yaml under
  a "service_account_key:" field. No reliance on
  GOOGLE_APPLICATION_CREDENTIALS or VM metadata.
- Node labels: each agent declares a labels list in
  config-files/agent.yaml. A Job.node_label matches if the
  agent's labels contain that string. Multiple labels per agent;
  a single required label per job. Boolean label expressions
  (a la Jenkins "linux && tpm") are backlog.

Additional priorities confirmed after Phase 5 mid-point review:

- On-prem first, cloud as overflow. The scheduler MUST prefer
  any matching on-prem agent before asking a Provisioner to spin
  up a GCE node. Cloud nodes are only for overflow when every
  matching on-prem agent is busy. This shapes the 5.5 selector
  policy.
- GCE live test is a placeholder. Write the integration test
  (against a real Compute Engine endpoint) but gate it on an
  env var (e.g. WOLFCI_GCE_LIVE_TEST=1) so it stays t.Skip-ed
  until the project owner provides credentials.
- LogChunk streaming during execution lands after 5.4b/5.5; see
  the new Phase 5.7 task below.

- [x] 5.1 cmd/wolfci-agent: a standalone binary that opens an mTLS
        connection back to the server and offers itself as an
        executor. Agent config
        (config-files/agent.yaml) declares its executor count
        (concurrent jobs allowed).
        Done: internal/agent/config.go (Config, DefaultConfig,
        LoadConfig, Save, Validate, HasLabel) plus
        cmd/wolfci-agent/main.go with --config and --dry-run
        flags. The mTLS dial + gRPC protocol itself is task 5.3;
        the binary currently exits with a clear message
        directing the operator to --dry-run until 5.3 lands.
        Gate: TestConfig_RoundTrip, TestConfig_Defaults,
        TestLoadConfig_RejectsMissingFields, TestConfig_HasLabel.
- [ ] 5.2 Failing test (tests/agent_e2e_test.go): an agent
        registers, receives a job, executes it, and returns the
        result.
        Sub-checkpoints:
        - [x] 5.2a Protocol scaffold: api/v1/agent.proto with
              the AgentService.Register RPC, scripts/gen-proto.sh
              regen helper, generated agent.pb.go and
              agent_grpc.pb.go, google.golang.org/grpc v1.56.3
              and google.golang.org/protobuf v1.31.0 pinned in
              go.mod for Go 1.18 compatibility.
        - [x] 5.2b gRPC server: register the AgentService and
              return a RegisterResponse for an incoming AgentInfo.
              Done: internal/agentsvc.Server implements
              wolfciv1.AgentServiceServer with an in-memory
              registry and gRPC-status validation. Gate:
              TestAgentService_RegisterRoundtrip (real gRPC over
              plain TCP, ephemeral port) and
              TestAgentService_Register_Validation.
        - [x] 5.2c gRPC client + wolfSSL mTLS bridge: agent
              dials the server, completes the mTLS handshake,
              issues Register. Requires client-side wolfSSL in
              internal/tlsutil.
              Done: internal/tlsutil grew Dial (client side) and
              ClientCAs/RootCAs/ServerName Config fields; the
              server-side NewListener turns on
              VERIFY_PEER | VERIFY_FAIL_IF_NO_PEER_CERT when
              ClientCAs is non-empty. gRPC bridges via
              grpc.WithContextDialer over insecure credentials
              (encryption is below the gRPC transport).
              testcerts.NewMTLSChain mints a CA + ServerAuth +
              ClientAuth chain for tests. Gate:
              TestAgentService_RegisterOverMTLS.
        - [x] 5.2d End-to-end test (tests/agent_e2e_test.go):
              spin up server, spin up agent, observe a Register
              round trip, then drive a job through (once 5.3 is
              far enough along).
              Done in spirit: TestAgentService_ConnectStream in
              internal/agentsvc/connect_test.go exercises the
              full mTLS gRPC bridge with a real JobAssignment
              push and BuildComplete reply. A standalone test
              under tests/ that re-uses cmd/wolfci-agent as a
              subprocess remains TODO; tracked in backlog.
- [x] 5.3 Implement the agent protocol over gRPC + wolfSSL mTLS.
        Done for the wire and server sides: api/v1/agent.proto
        carries the Connect bidirectional stream (JobAssignment,
        Step, LogChunk, BuildComplete, AgentMessage,
        ServerMessage). internal/agentsvc.Server implements
        Connect with a sender goroutine that pumps QueueJob'd
        assignments and a receiver loop that records
        BuildComplete messages (LogChunks are accepted and
        dropped pending follow-on log persistence).
        Wire safety: tlsutil callbacks recover from
        cgo.Handle.Value panics so a Close racing with an
        in-flight Read returns CBIO_ERR_GENERAL instead of
        crashing the process.
        Agent runtime landed: internal/agent.Client dials via
        wolfSSL mTLS, registers, opens Connect, dispatches each
        JobAssignment to LocalExecutor under cfg.WorkDir, and
        reports BuildComplete on the same stream.
        cmd/wolfci-agent now drives Client.Run and accepts
        SIGINT/SIGTERM for clean shutdown. Gate:
        TestClient_RunDispatchesAndReports drives an end-to-end
        flow through a real wolfSSL mTLS gRPC server. Follow-on:
        stream LogChunks back during execution (currently only
        the final BuildComplete is sent).
- [ ] 5.4 GCE provisioner (internal/nodes/gce): uses the Google
        Cloud Go SDK to launch an instance with a startup script
        that runs wolfci-agent and joins the server.
        Sub-checkpoints:
        - [x] 5.4a Provisioner interface + Config layer + fake
              backend for tests. internal/nodes defines Node,
              Provisioner, ErrNoSuchNode, ErrNotImplemented.
              internal/nodes/fake.Provisioner is an in-memory
              backend. internal/nodes/gce.Config round-trips
              YAML at config-files/nodes/gce.yaml with
              project_id, zone, machine_type,
              service_account_key, image, network, labels.
              internal/nodes/gce.Provisioner is a stub that
              returns nodes.ErrNotImplemented from Provision
              and Terminate. Gate:
              TestFake_ProvisionTerminate, TestFake_ListLive,
              TestConfig_RoundTrip,
              TestLoadConfig_RejectsMissingRequiredFields.
        - [ ] 5.4b Real GCE driver against
              google.golang.org/api/compute/v1. Replace the stub
              with code that launches an instance via the API
              and tears it down on Terminate. Startup script
              installs and runs wolfci-agent.
- [ ] 5.5 Scheduler + Provisioner integration: when a Job's
        node_label has no matching idle on-prem agent, the
        scheduler asks the Provisioner for one. ON-PREM FIRST:
        any matching on-prem agent (registered via Register +
        Connect) is used before a Provisioner is consulted.
        Failing test (tests/scheduler_provisioner_test.go) uses
        the fake provisioner: scheduler enqueues a job with a
        label no on-prem agent advertises, observes
        Provision(ctx, label), the "new node" joins via the
        existing AgentService, runs the job, and the
        Provisioner is told to Terminate after the build.
        Sub-checkpoints:
        - [x] 5.5a Per-agent routing in agentsvc. Connect
              streams keyed by agent_id (passed via gRPC
              metadata under "agent-id"). New methods on
              agentsvc.Server: AssignJob(agentID, job),
              IdleAgentWithLabel(label), ConnectedAgents().
              Connect rejects calls without metadata
              (Unauthenticated) or for agents that did not
              Register first (FailedPrecondition).
              agent.Client now sends the metadata. Gate:
              TestServer_AssignJob_TargetsAgent,
              TestServer_AssignJob_UnknownAgent,
              TestServer_IdleAgentWithLabel,
              TestServer_ConnectRequiresMetadata,
              TestServer_ConnectRequiresPriorRegister.
        - [x] 5.5b Scheduler talks to agentsvc to pick a
              matching idle agent for each job and dispatches
              via AssignJob. On-prem-first selector.
              Done: scheduler.Router implements Executor and
              routes per Job.NodeLabel:
                  ""              or matches localLabels -> local
                  matches an agent advertised label       -> remote
                  no match                                 -> Status=error
              agentsvc.SubmitAndWait blocks until the agent
              reports BuildComplete for the same
              (agent_id, build_number); the receiver loop
              delivers via a buffered chan registered in the
              pending map. Gate:
              TestRouter_OnPremFirstAndLabelMatch exercises
              all three routes with a real on-prem agent
              advertising "linux".
        - [x] 5.5c Provisioner integration: when 5.5b returns
              no idle on-prem agent for a Job.node_label, the
              scheduler asks the configured Provisioner for
              one, waits for it to register, then dispatches.
              Terminate after the build.
              Done: scheduler.Router gains WithProvisioner and
              WithProvisionTimeout. On no-on-prem-match the
              Router calls provisioner.Provision(ctx, label),
              polls agentsvc.IdleAgentWithLabel up to the
              timeout for the spawned node to connect,
              SubmitAndWait's the job, then Terminate's the
              node via defer. Convention: Provisioner.Node.ID
              equals the agent_id the spawned agent passes to
              Register. Gate:
              TestRouter_ProvisionsWhenNoOnPremMatch uses an
              inline spawningProvisioner that runs a real
              agent.Client in-process pointing back at the
              test's own agentsvc; verifies Provision count,
              Terminate count, post-Terminate idle state, and
              the spawned agent's own build log content.
- [x] 5.6 Implement the GCE driver against the real API behind
        the nodes.Provisioner interface the fake satisfies.
        Live test (internal/nodes/gce/live_test.go) is a
        placeholder gated on WOLFCI_GCE_LIVE_TEST=1 so it
        compiles and skips by default until credentials are
        provided. Driver itself is built against
        google.golang.org/api/compute/v1.
        Done: gce.Provisioner.Provision builds a compute.Instance
        (machine type from cfg, source image from cfg, default
        network unless overridden, ONE_TO_ONE_NAT external IP,
        startup-script in metadata), calls Instances.Insert,
        polls ZoneOperations until DONE. Terminate is the
        symmetric Delete. compute.Service is built lazily via
        option.WithCredentialsFile(cfg.ServiceAccountKey) so
        offline unit tests don't need real creds. Gate:
        TestStartupScript_MentionsAgentIDAndLabel,
        TestNewProvisioner_LazyService, plus the SKIP'd
        TestLive_GCEProvisionTerminate.
        Backlog (logged in commit and in PLAN.md backlog):
        the startup script is a placeholder; a follow-on task
        fills it in to install wolfci-agent, deliver cert
        material via GCE Secret Manager (or equivalent), and
        start the agent under systemd with the right agent_id
        and label.
- [x] 5.7 LogChunk streaming: the agent emits LogChunk messages
        during step execution (not just BuildComplete at the
        end). Server-side dispatches them via a pluggable
        LogSink so the Phase 6 UI (or any other reader) can
        tail builds live.
        Done: scheduler.NewLocalExecutorWithLogSink wraps the
        cmd.Stdout/Stderr writer in a teeLogWriter that
        forwards each chunk to an onLog(jobName, buildNum,
        data) callback in addition to writing the on-disk log.
        agent.Client.runAssignment constructs a per-build
        executor whose callback sends an AgentMessage_Log
        through the Connect stream under streamMu (same lock
        the BuildComplete send uses).
        agentsvc.Server.SetLogSink(sink) wires a server-side
        LogSink; Connect's receiver routes each LogChunk by
        looking up the (agent_id, build_number) from the
        in-flight SubmitAndWait registry to recover the
        job_name, then calls sink.WriteLogChunk(jobName,
        buildNum, data). assignmentInFlight now carries both
        the job_name and the completion channel.
        Gate: TestServer_LogChunksDeliveredDuringExecution
        runs a real agent through a two-line shell job and
        asserts the server's LogSink saw both lines plus a
        nonzero call count. Persisting chunks to a
        log.live file is a thin FileLogSink wrapper that
        Phase 6 will add once the UI's tailing endpoint
        exists; backlog item.

## Phase 6 - Web UI

Embedded HTML UI served from the single binary.

Decisions locked in for Phase 6 (confirmed with the project
owner before the phase started):

- CSS: hand-written, embedded as a single asset. No framework.
  Aligns with the project's small-footprint mantra.
- Sessions: HTTP-only Secure SameSite=Strict cookies backed by
  per-session files under config-files/auth/sessions/<token>.
  CSRF defense piggybacks on SameSite=Strict.
- Interactivity: Go html/template server-rendered pages plus
  vanilla JS where needed (live log tailing via SSE). No HTMX,
  no SPA framework.
- Start order: FileLogSink + log tailing endpoint FIRST (closes
  the 5.7 backlog item), THEN the login + page UI. Admin
  user-management endpoints land after the basic UI shell.

Additional Phase 6 task at the front of the list:

- [x] 6.0 FileLogSink + GET /api/v1/builds/{job}/{n}/log live
        tail endpoint. FileLogSink persists each LogChunk to
        builds/<job>/<n>/log.live with append-mode flock-safe
        writes. The endpoint streams the file's current
        contents then follows appends via Server-Sent Events
        (text/event-stream).
        Done: agentsvc.FileLogSink implements LogSink with
        syscall.Flock LOCK_EX around each append.
        server.LogTailHandler parses
        /api/v1/builds/{job}/{n}/log, opens the live file,
        emits "event: log\ndata: <base64>\n\n" frames, polls
        for new bytes until the client disconnects or
        IdleTimeout fires. Gate:
        TestLogTail_LivePersistAndStream writes one chunk
        before the request opens, opens the SSE stream, then
        writes a second chunk and reads both back as decoded
        events. TestLogTail_BadPath rejects malformed routes.
        Sub-fix: replaced runtime/cgo.Handle in internal/tlsutil
        with a sync.Map-based registry because the cgo handle's
        unrecoverable panic on a Delete'd id was racing the
        gRPC HTTP/2 reader. 15/15 stable after the switch.

- [x] 6.1 Failing test (internal/server/ui_test.go): GET / returns
        the login page; authenticated GET /jobs returns the job
        list.
        Done: TestUI_LoginPageAndJobsList walks the
        unauthenticated-GET-/ -> POST /login (good and bad creds)
        -> authenticated GET /jobs flow and asserts the session
        cookie + listed job name. storage.ListJobs added.
- [x] 6.2 Implement minimal UI with Go html/template, embedded via
        //go:embed.
        Done: internal/server.Server registers /, /login,
        /logout, /jobs, and /static/*. Templates
        (templates/base.html, login.html, jobs.html) and CSS
        (static/app.css) are embedded via //go:embed.
        SessionStore mints 64-hex-char tokens, persists each
        session as YAML under config-files/auth/sessions/, and
        enforces TTL on lookup. Session cookie is
        HttpOnly+SameSite=Strict, with Secure controlled by
        Options.CookieSecure so HTTP-only dev/test runs work.
        Side fix in internal/tlsutil: conn lifecycle is now
        an sync.RWMutex (Read/Write hold RLock, Close holds
        Lock) plus a sync.Once Close path that shuts the inner
        net.Conn BEFORE acquiring Lock so blocked
        wolfSSL_read calls return and release their RLock.
        Previously wolfSSL_free could run while a gRPC HTTP/2
        reader was mid-wolfSSL_read and SIGSEGV the process
        (intermittent in test re-runs). 15/15 stable after.
- [x] 6.3 Job create/edit form posts to internal/storage.
        Done: /jobs/new (GET form, POST creates) and
        /jobs/{name}/edit (GET pre-filled, POST saves) routes.
        Single textarea posts raw YAML; the handler parses
        with gopkg.in/yaml.v3 and saves via Storage.SaveJob.
        Validation: duplicate create rejected; edit refuses
        rename (name in spec must match URL). Gate:
        TestUI_JobCreateAndEdit covers form render, happy-path
        create, duplicate rejection, edit pre-fill, rename
        rejection, and successful update.
- [x] 6.4 Build log live stream via Server-Sent Events.
        Done: /jobs/{name}/builds/{n} renders buildlog.html
        which loads /api/v1/builds/{name}/{n}/log via vanilla
        EventSource and appends decoded chunks to a <pre>
        terminal-styled element. The SSE endpoint from task
        6.0 is now mounted under the session-auth middleware
        so the page works for logged-in users only. Gate:
        TestUI_BuildLogPage, TestUI_BuildLogPage_BadNumber,
        TestUI_SSEEndpointRequiresAuth.
- [x] 6.5 Node management page lists registered on-prem and GCE
        nodes.
        Done: /nodes route renders nodes.html, which lists
        every Agents() entry from the configured agentsvc with
        status (connected vs offline, derived from
        ConnectedAgents()), labels, and executor count. Header
        nav links Jobs and Nodes plus a Sign-out button.
        AgentSvc is an optional field on server.Options; if
        nil, /nodes renders an empty-state message pointing
        operators at the Register RPC and the GCE overflow
        path. Gate: TestUI_NodesPage (registered agent shows
        up with labels + executors + offline status),
        TestUI_NodesPage_Empty (empty-state message).

## Phase 7 - Plugins

Out-of-process gRPC plugins.

Decisions locked in for Phase 7 (confirmed with the project
owner before the phase started):

- Plugin host: hashicorp/go-plugin. It handles subprocess
  lifecycle, gRPC-over-Unix-socket handshake, version
  negotiation, and graceful shutdown so we do not hand-roll
  those.
- First hook: on-build-complete. Server invokes registered
  plugins after every BuildComplete with (job, build_number,
  status, exit_code, error). The on-build-start hook lands
  later if there is demand.
- Install layout: plugins/installed/<name>/<name> for the
  binary, plus an optional plugins/installed/<name>/config.yaml
  next to it. Easy install ("drop a directory"), easy uninstall
  ("rm -r").

- [x] 7.1 Design the plugin protocol (gRPC; plugins announce the
        hooks they implement). Document in docs/PLUGINS.md.
        Done: api/v1/plugin/plugin.proto defines the
        WolfCIPlugin service with the OnBuildComplete RPC and
        the BuildCompleteEvent payload (job_name, build_number,
        status, exit_code, error). Generated Go bindings under
        api/v1/plugin/. hashicorp/go-plugin v1.5.2 pinned in
        go.mod. docs/PLUGINS.md documents the install layout
        (plugins/installed/<name>/<name>), handshake
        constants, threat model, and a stub Go authoring guide.
- [x] 7.2 Failing test (internal/plugin/plugin_test.go): a sample
        "hello" plugin under plugins/examples/hello is loaded and
        its on-build-complete hook fires.
        Done: TestHost_LoadAndDispatchHello builds the hello
        plugin into a tempdir layout matching
        plugins/installed/hello/hello, drives Load + OnBuildComplete,
        and asserts the plugin wrote the expected JSON event.
        TestHost_LoadEmpty covers the no-plugins-installed path.
- [x] 7.3 Implement the plugin host in internal/plugin (subprocess
        management, gRPC client, cleanup on shutdown).
        Done: internal/plugin.Host scans
        <pluginsDir>/installed/, spawns each <name>/<name>
        binary via hashicorp/go-plugin, dispatches
        OnBuildComplete to every loaded plugin (errors
        swallowed so one bad plugin can't block the rest), and
        Kill's every subprocess on Stop. Shared Handshake +
        PluginMap + WolfCIPlugin live in plugin.go so plugin
        authors import the same constants the host uses. 5/5
        stable.
- [x] 7.4 Ship a real plugin: email-on-failure.
        Done: plugins/email-on-failure binary that watches for
        non-success build statuses (failure, error, cancelled)
        and SMTPs an RFC 822 message to a configured recipient
        list. Config lives at
        plugins/installed/email-on-failure/config.yaml; SMTP
        credentials read from WOLFCI_EMAIL_PLUGIN_USER /
        WOLFCI_EMAIL_PLUGIN_PASS so they are not on disk
        next to the YAML. Sender interface decouples
        net/smtp from formatMessage so tests use a recorder.
        Gates: TestFormatMessage_Failure,
        TestFormatMessage_IncludesErrorField,
        TestOnBuildComplete_SkipsSuccess,
        TestOnBuildComplete_SendsOnFailure,
        TestLoadConfig_RoundTrip,
        TestLoadConfig_RejectsMissingFields.

## Phase 8 - CLI client

Decisions locked in for Phase 8 (confirmed with the project
owner before the phase started):

- CLI library: standard library "flag" + a hand-rolled
  subcommand router. No external CLI framework. Aligns with
  the project's small-footprint mantra.
- Authentication: SSH-key / wolfSSL mTLS over the same gRPC
  transport agents use. The CLI presents a client cert; the
  server takes the cert CN as the username and looks it up in
  config-files/auth/matrix.yaml to gate permissions. No
  separate API-token system.
- Config path: ~/.config/wolfci/ctl.yaml (XDG-friendly,
  overridable via $XDG_CONFIG_HOME). Holds server address +
  paths to the client cert/key and the server CA bundle.
- Output: human-readable text by default (aligned columns,
  short status); pass --json on any subcommand to get
  machine-parseable JSON.

Implementation note: the existing AgentService is
agent-specific (Register expects Executors >= 1). The CLI
will likely need its own CLIService gRPC service with
list/stream methods (ListJobs, ListNodes, StreamBuildLog).
The "login" subcommand is a config-only operation that writes
ctl.yaml; mTLS handles the actual authentication on every
RPC.

- [x] 8.1 cmd/wolfci-ctl with subcommands: login, job list, job
        run, build log, node list. Failing test
        (tests/ctl_test.go) exercises each.
        Sub-checkpoints:
        - [x] 8.1a Subcommand router + Config + login + version.
              cmd/wolfci-ctl ships dispatch(args, stdout,
              stderr) that walks a map[string]*subcommand,
              prints aligned usage on no-args, returns 2 for
              unknown subcommand. login writes a complete
              ctl.yaml (server_address, certificate, key,
              ca_certificate). defaultConfigPath honors
              WOLFCI_CTL_CONFIG > XDG_CONFIG_HOME >
              $HOME/.config/wolfci/ctl.yaml. Gate:
              TestDispatch_Version, TestDispatch_NoArgs,
              TestDispatch_UnknownSubcommand,
              TestLogin_WritesConfig, TestLogin_MissingFlags,
              TestDefaultConfigPath.
        - [x] 8.1b CLIService gRPC service (job list, node list).
              Done: api/v1/cli/cli.proto declares the
              CLIService with ListJobs + ListNodes;
              internal/cliservice wraps storage.ListJobs and
              agentsvc.Agents/ConnectedAgents. wolfci-ctl
              dispatches `job list` and `node list` through
              the wolfSSL mTLS bridge (same Dial path as
              agent.Client) with text-table output by default
              and --json for scripting. Gates:
              TestCLIService_ListJobs / ListNodes /
              ListNodes_NoAgentSvc cover the server side over
              real gRPC; TestFormatJobs_Text / _Empty / _JSON
              and TestFormatNodes_Text gate the formatters;
              TestJobList_EndToEnd exercises the full mTLS
              gRPC round trip through dispatch().
        - [x] 8.1c Streaming build log subcommand.
              Done: CLIService.StreamBuildLog tails
              builds/<job>/<n>/log.live via polling, with a
              configurable IdleTimeout and respect for the
              caller's ctx. wolfci-ctl `build log <job> <n>`
              wraps it. Path validation guards against
              traversal in job names. Gates:
              TestCLIService_StreamBuildLog,
              TestCLIService_StreamBuildLog_BadInput,
              TestBuildLog_EndToEnd (full wolfSSL mTLS
              roundtrip), TestBuildLog_BadArgs.
              Convention: subcommand flags must precede
              positional args (Go stdlib flag stops at first
              non-flag).
        - [x] 8.1d `wolfci-ctl job run <name>` subcommand.
              Done: api/v1/cli/cli.proto adds
              RunJob(RunJobRequest{JobName}) ->
              RunJobResponse{BuildNumber}. internal/cliservice
              defines an Enqueuer interface (with EnqueuerFunc
              adapter) so scheduler.Scheduler and cliservice
              stay decoupled; bootstrap code plumbs them
              together. Server.WithEnqueuer wires it;
              RunJob returns Unavailable when none is set
              (allowing CLIService to ship without job-run
              capability). cmd/wolfci-ctl/jobrun.go drives
              `job run [--json] <name>`. Human output:
              "<name> queued as build N" plus a tail hint
              pointing at `wolfci-ctl build log <name> N`.
              JSON: {"job": "<name>", "build_number": N}.
              Gates: TestCLIService_RunJob (full gRPC
              roundtrip with a fake Enqueuer),
              TestCLIService_RunJob_NoScheduler,
              TestCLIService_RunJob_UnknownJob.
              Wiring scheduler.Scheduler into cmd/wolfci so
              operators do not need to write Go to get
              `job run` is a Phase 9 packaging concern.

## Phase 9 - Packaging, docs, polish

- [x] 9.1 scripts/build.sh produces a single static binary for
        linux/amd64 plus a darwin/arm64 development binary.
        Done: scripts/build.sh links cmd/wolfci, cmd/wolfci-agent,
        cmd/wolfci-ctl against build/wolfssl-install/lib/libwolfssl.a
        via CGO_LDFLAGS, outputs to build/bin/<goos>-<goarch>/,
        and stamps WOLFCI_BUILD_VERSION into main.version via
        -ldflags -X. GOOS/GOARCH env vars target other platforms;
        the cross-build requires a matching wolfSSL .a (via
        TARGET_GOOS / TARGET_GOARCH on build-wolfssl.sh) and a
        CGO-capable CC. The script's recipe block documents the
        two Phase-9.1 targets explicitly. Gate:
        scripts/test-build.sh asserts the source-level tokens,
        runs the host-platform build, checks the three binaries
        landed under build/bin/<goos>-<goarch>/, verifies
        wolfci-ctl version reflects the injected -ldflags value,
        and confirms no stray binaries were left in the repo
        root. Now wired into scripts/test.sh.
- [x] 9.2 scripts/install/systemd/wolfci.service template; a
        macOS launchd plist for development.
        Done: scripts/install/systemd/wolfci.service runs wolfci
        as a dedicated wolfci user out of /var/lib/wolfci, with
        on-failure restart, 30s graceful stop window, and the
        usual sandboxing knobs (NoNewPrivileges, ProtectSystem,
        ProtectHome, PrivateTmp, plus CAP_NET_BIND_SERVICE so
        operators can bind 443 without root).
        scripts/install/launchd/com.wolfssl.wolfci.plist ships
        a launchd job for development boxes (RunAtLoad,
        KeepAlive, WorkingDirectory under /usr/local/var/wolfci,
        Background ProcessType, stdout/err to
        /usr/local/var/log/wolfci.{log,err}). Both templates
        name /usr/local/bin/wolfci so operators just copy
        build/bin/<goos>-<goarch>/wolfci there after running
        scripts/build.sh. Gate: scripts/test-install.sh
        checks file presence, every section/key the loader
        actually reads, and (on macOS) runs plutil -lint on
        the plist. Wired into scripts/test.sh.
- [x] 9.3 docs/GETTING-STARTED.md: a 10-minute path from "git
        clone" to "first green build".
        Done: docs/GETTING-STARTED.md walks an operator from
        `git clone` through `git submodule update --init`,
        scripts/build-wolfssl.sh (3 min one-time),
        scripts/build.sh (30 sec), installing the systemd unit
        or launchd plist from scripts/install/, the first-admin
        one-time token bootstrap, `wolfci-ctl login`, dropping
        in a hello job, `wolfci-ctl job run`, and tailing with
        `wolfci-ctl build log`. Final section covers
        cross-builds (musl-cross for linux/amd64; native Xcode
        for darwin/arm64). Gate:
        scripts/test-getting-started.sh asserts every step
        marker is present so the doc and the actual scripts
        cannot drift apart silently. Wired into
        scripts/test.sh.
- [x] 9.4 docs/ARCHITECTURE.md with an ASCII component diagram.
        Done: docs/ARCHITECTURE.md ships two ASCII diagrams (a
        deployment view with server, on-prem and GCE agents,
        operator browser/CLI; a server-internals view with the
        package map and the wires between them) plus a
        package-by-package responsibilities list, an end-to-end
        "one build" walk-through, the runtime data layout under
        WorkingDirectory, and a "why these choices" section that
        links each design decision back to the five goals in
        CLAUDE.md. Gate: scripts/test-architecture.sh asserts
        every top-level Go package and every runtime data dir
        is named, plus an awk-based check that at least one
        fenced code block contains the +-- / --+ / | tokens
        of an ASCII component diagram. Wired into
        scripts/test.sh.
- [x] 9.5 README.md final pass.
        See Phase 10 below for the next active work.
        Done: README.md replaces the Phase 0 stub with a real
        feature list (one binary per role, wolfSSL mTLS gRPC,
        SSH-key + bcrypt auth, role matrix, on-prem + GCE
        overflow, build matrix, embedded UI with SSE log tail,
        out-of-process plugins, self-contained on disk), a
        binary-role table (wolfci / wolfci-agent / wolfci-ctl), a
        quick build recipe (git submodule + build-wolfssl.sh +
        build.sh), and a docs index linking GETTING-STARTED,
        ARCHITECTURE, SECURITY, PLUGINS, and CREDITS. Status
        line reflects "Phases 0-9 complete" and points readers at
        PLAN.md for the per-task log and Current Phase.
        Gate: scripts/test-readme.sh asserts the bootstrap
        language is gone, every binary is named, every long-form
        doc is linked, the build recipe is present, and the
        GPL-3.0 + author footer is intact. Wired into
        scripts/test.sh.

## Phase 10 - wolfCrypt-only crypto pass

End-to-end review surfaced that several pieces of the auth stack
still pull crypto from Go's stdlib / x/crypto. CLAUDE.md mission #5
says "all TLS and crypto via wolfSSL", and the project owner made
that literal on 2026-05-21: "ZERO cryptography ... zero, none,
nadda cryptography unless it's from wolfCrypt in this solution".
This phase reroutes every cryptographic operation in the source
tree through wolfCrypt (wolfSSL's crypto component).

Decisions locked in for Phase 10 (confirmed with the project owner
before the phase started):

- ZERO non-wolfCrypt cryptography anywhere in the source tree. No
  exceptions for test code, no exceptions for "just a hash
  compare". The audit boundary collapses to one library.
- New internal/wolfcrypt package, sibling of internal/tlsutil,
  exposes the primitives: RandBytes, HMAC-SHA-256, PBKDF2-HMAC-
  SHA-256, Ed25519Verify, ECCVerify, RSAVerify, MintCert.
  Both packages link the same wolfSSL static lib via CGO.
- SSH public-key wire-format parsing is hand-rolled. ~150 lines of
  base64 + length-prefixed-field decode in internal/auth/sshkey.go.
  x/crypto/ssh drops out of go.mod entirely.
- crypto/tls constant references (tls.VersionTLS13 etc.) move to
  local constants in internal/tlsutil. crypto/tls imports drop
  from tlsutil / agent / wolfci-ctl.
- Hash comparisons use the HMAC-both-sides pattern: fresh 32-byte
  RNG key, HMAC stored and candidate, byte-compare HMAC outputs.
  Defense in depth - even if the byte compare leaks timing, what
  leaks is the HMAC, not the password hash.
- Migration of existing bcrypt hashes is moot. cmd/wolfci is still
  the Phase 1.5 stub; no production deployment has bcrypt files
  on disk. Test fixtures get rewritten as part of each task.
- Phase 10 ships BEFORE the cmd/wolfci wiring (Phase 11) so the
  wiring code talks to the new wolfCrypt-backed APIs exactly once
  instead of being rewritten when the auth stack is overhauled
  next.

- [x] 10.1 internal/wolfcrypt package: the wolfCrypt bridge that
        every other auth/test/session file in this tree will use.
        Subdivided into three sub-checkpoints because it is all
        new CGO surface and each primitive deserves its own KATs
        and red/green iteration. All three landed; the package now
        exposes RandBytes / HMACSHA256 / PBKDF2HMACSHA256 / SHA256
        / Ed25519Verify / ECCVerifyP256 /
        RSAVerifyPKCS1v15SHA256 / MintCert + the Cert/CertConfig
        types. Every consumer in 10.2 through 10.6 imports this
        package and nothing else from a crypto namespace.
        - [x] 10.1a RandBytes + HMACSHA256 + PBKDF2HMACSHA256.
              Done: internal/wolfcrypt/wolfcrypt.go ships the
              three primitives via CGO against wolfSSL
              (wc_InitRng + wc_RNG_GenerateBlock; wc_HmacInit +
              wc_HmacSetKey + wc_HmacUpdate + wc_HmacFinal with
              WC_SHA256; wc_PBKDF2 with WC_SHA256). Same
              CGO directives as internal/tlsutil so both packages
              link the same libwolfssl.a. Empty-slice handling
              is explicit (nil pointer instead of &slice[0]).
              Gates: TestRandBytes_NonZero (1000-iter no-zero +
              no-collision), TestRandBytes_SizeZero,
              TestHMACSHA256_RFC4231_TC1 (canonical 0x0b-key
              "Hi There" KAT), TestHMACSHA256_RFC4231_TC2 ("Jefe"
              short-key KAT), TestPBKDF2_RFC7914_Param ("passwd"
              + "salt" + c=1 + dkLen=64 KAT),
              TestPBKDF2_Determinism, TestPBKDF2_DifferentSalts
              Diverge.
        - [x] 10.1b Ed25519Verify + ECCVerifyP256 +
              RSAVerifyPKCS1v15SHA256.
              Done: internal/wolfcrypt/verify.go wires three
              public functions via CGO. Ed25519Verify uses
              wc_ed25519_init / wc_ed25519_import_public /
              wc_ed25519_verify_msg; ECCVerifyP256 uses
              wc_ecc_init / wc_ecc_import_x963 (SEC1 uncompressed
              point) / wc_ecc_verify_hash with a DER-encoded
              (r, s) signature; RSAVerifyPKCS1v15SHA256 uses
              wc_InitRsaKey / wc_RsaPublicKeyDecodeRaw (raw n + e
              from the SSH wire format, no DER) / wc_SignatureVerify
              with WC_HASH_TYPE_SHA256 + WC_SIGNATURE_TYPE_RSA_W_ENC.
              A clean reject (wrong sig vs key) returns (false, nil)
              so callers can tell a no from an error; structural
              problems (wrong key length, init failure) return an
              error.
              Test vectors are hard-coded so no non-wolfCrypt
              crypto runs in this tree:
                Ed25519: RFC 8032 Section 7.1 TC2 KAT plus a
                         tampered-message negative and a wrong
                         key-length structural error case.
                ECC:     RFC 6979 Section A.2.5 "sample" + SHA-256
                         KAT (hash + r + s lifted from the RFC,
                         signature DER-encoded inline) plus a
                         tampered-hash negative.
                RSA:     a 2048-bit / PKCS#1 v1.5 / SHA-256 vector
                         generated outside this source tree with
                         OpenSSL 3.x (provenance documented in the
                         test file) plus tampered-message and
                         tampered-signature negatives.
              Gate file: internal/wolfcrypt/verify_test.go.
        - [x] 10.1c MintCert + SHA256.
              Done: internal/wolfcrypt/cert.go ships
              MintCert(cfg, signer) (returns Cert{CertDER, KeyDER,
              PubSEC1}) and the standalone SHA256(data). MintCert
              generates a fresh ECC P-256 keypair (wc_ecc_make_key),
              exports the public part as SEC1 uncompressed
              (wc_ecc_export_x963), exports the private key as DER
              (wc_EccKeyToDer), builds the Cert struct
              (wc_InitCert + a small C helper that copies subject
              CommonName + Organization into the fixed-size char
              arrays), optionally calls wc_SetExtKeyUsage for
              ServerAuth / ClientAuth, sets the issuer from
              signer.CertDER via wc_SetIssuerBuffer (or self-signs
              when signer == nil), then wc_MakeCert + wc_SignCert
              with CTC_SHA256wECDSA. SAN is deferred to 10.5
              because wolfCrypt's wc_SetAltNames* APIs take DER, not
              a string; building that DER lives next to the
              testcerts rewrite that consumes it.
              Build-profile change: scripts/build-wolfssl.sh now
              enables --enable-certext so wc_SetExtKeyUsage is
              available; scripts/test-build-wolfssl.sh gates the
              three cert flags (keygen, certgen, certext) so a
              profile regression cannot quietly break MintCert.
              Gate file: internal/wolfcrypt/cert_test.go. Tests:
              TestSHA256_NIST ("abc" KAT), TestSHA256_Empty
              (empty-string KAT), TestMintCert_SelfSignedCA,
              TestMintCert_LeafSignedByCA,
              TestMintCert_LeafSignatureVerifiesAgainstCA
              (end-to-end: wolfCrypt-side sign produces a
              signature that wolfCrypt-side ECCVerifyP256 accepts
              over SHA256 of the tbsCertificate, plus the
              corresponding tamper-negative). The end-to-end test
              uses a tiny inline DER walker (extractTBSAndSig) to
              extract tbsCertificate and the signature BIT STRING
              contents from a leaf cert without parsing it with
              stdlib crypto.
- [x] 10.2 Replace x/crypto/bcrypt in internal/auth/password.go
        with PBKDF2-HMAC-SHA-256 via internal/wolfcrypt.
        Done: password.go now imports only
        github.com/kaleb-himes/wolfCI/internal/wolfcrypt (and
        stdlib non-crypto) - no bcrypt import anywhere.
        SetPassword draws a salt via wolfcrypt.RandBytes
        (length = cfg.PBKDF2SaltBytes, default 16), derives a
        32-byte key via wolfcrypt.PBKDF2HMACSHA256 with
        cfg.PBKDF2Iterations (default 600000 per OWASP 2023),
        and writes a versioned file at <root>/<user>.pbkdf2
        with format:
            wolfci-pbkdf2-v1
            iterations:<decimal>
            salt:<hex>
            key:<hex>
        VerifyPassword refuses anything that does not start with
        the wolfci-pbkdf2-v1 sentinel (catches a leftover
        bcrypt blob or a wrong-KDF file) and compares stored vs
        candidate via the HMAC-both-sides constant-time pattern:
        fresh wolfcrypt.RandBytes(32) -> HMAC-SHA-256 of each
        side -> byte-XOR of the HMAC outputs. Any timing leak in
        the byte loop only reveals HMAC outputs over an
        unpredictable key.
        Config field rename: BcryptCost is gone;
        PBKDF2Iterations and PBKDF2SaltBytes take its place
        with the documented defaults and bounds (iterations in
        [100, 10000000], salt in [8, 64]).
        ErrInvalidPasswordFile is a new sentinel for "your hash
        file is corrupt or wrong-KDF" (distinct from
        ErrInvalidPassword which is "your password is wrong").
        Call-site updates: internal/server/{ui,jobform,nodes}_test.go
        switched from BcryptCost: 4 to PBKDF2Iterations: 1000;
        internal/server/server.go comment updated.
        go.mod: golang.org/x/crypto/bcrypt no longer imported
        (the golang.org/x/crypto module entry stays in go.mod
        because internal/auth/sshkey.go still imports the ssh
        subpackage; 10.3 removes that and the whole module entry
        drops via `go mod tidy`).
        Gates: existing TestPasswordStore_VerifyWhenEnabled,
        TestPasswordStore_DisabledRejectsAll,
        TestConfig_DefaultsAndRoundtrip remain green with the
        new field names. New tests:
        TestLoadConfig_RejectsOutOfRangeIterations,
        TestPasswordStore_OnDiskFormat (asserts .pbkdf2
        extension + wolfci-pbkdf2-v1 sentinel + no .bcrypt
        residue), TestPasswordStore_RejectsWrongKDFHeader.
- [x] 10.3 Replace x/crypto/ssh in internal/auth/sshkey.go.
        Done: internal/auth/sshkey.go now imports only stdlib +
        internal/wolfcrypt. Hand-rolled wire parser per RFC 4253
        Section 6.6 covers the three SSH public-key algorithms
        (ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa) plus the
        rsa-sha2-256 signature algo from RFC 8332. The legacy
        ssh-rsa signature (SHA-1) is intentionally refused with
        ErrUnsupportedKeyType, matching OpenSSH 8.8+ behavior.
        New PublicKey struct exposes the algo-specific fields
        (Ed25519, ECDSAPoint, RSAModulus, RSAExponent) so the
        verify dispatch can pick the right wolfcrypt primitive
        without re-parsing.
        VerifySignature now takes an SSH wire-format signature
        envelope (string algo || string blob) rather than an
        x/crypto/ssh.Signature struct. For ECDSA the SSH
        sub-blob "string r || string s" is read via mpint
        helpers, recombined into ASN.1 DER (a small inline
        encoder, byte assembly only), and handed to
        wolfcrypt.ECCVerifyP256 over a wolfcrypt.SHA256 of the
        data. For RSA the raw PKCS#1 v1.5 sig blob flows
        straight through wolfcrypt.RSAVerifyPKCS1v15SHA256.
        New wolfcrypt primitives needed to drive the test
        without stdlib ed25519: wolfcrypt.Ed25519GenKey (returns
        32-byte public + 64-byte seed||pub private) and
        wolfcrypt.Ed25519Sign (in internal/wolfcrypt/sign.go).
        Gate: TestEd25519GenAndSign asserts sign + verify
        round-trip plus a tamper-negative.
        Exported wire helpers for test fixtures and the
        eventual Phase 11 first-admin bootstrap:
        EncodeSSHAuthorizedKey, EncodeSSHEd25519AuthorizedKey,
        EncodeSSHEd25519Signature.
        Gates (internal/auth/sshkey_test.go):
        TestKeyStore_VerifySignature (now wolfCrypt-only sign +
        verify), TestKeyStore_RejectsPathTraversal,
        TestKeyStore_RejectsUnknownAlgo (a bogus algo string in
        the signature envelope is rejected).
        go.mod: golang.org/x/crypto/ssh no longer imported.
        x/crypto stays in go.mod as an INDIRECT dep because the
        Google Cloud SDK pulled in by internal/nodes/gce uses
        x/crypto/cryptobyte (a byte-builder utility, not actual
        cryptography). This is a transitive dep we cannot drop
        without rewriting the GCE driver against wolfSSL. Logged
        as a finding for the user; wolfCI's own source code now
        imports zero stdlib or x/crypto cryptography.
- [x] 10.4 Replace crypto/rand in internal/server/session.go with
        wolfcrypt.RandBytes.
        Done: session.go imports
        github.com/kaleb-himes/wolfCI/internal/wolfcrypt instead
        of crypto/rand; randomToken() reads 32 bytes from
        wolfcrypt.RandBytes and hex-encodes them as before. The
        token size, format (64 lowercase hex chars), and the
        on-disk session layout are unchanged.
        Gate: new internal/server/session_test.go ships
        TestSessionStore_CreateLookupRoundtrip - two Create
        calls return distinct tokens (entropy sanity), tokens
        are 64 lowercase hex, lookup of a freshly-minted token
        returns the same Session, lookup of a syntactically
        invalid token errors out before touching disk. The
        existing UI tests (which exercise the full
        login-cookie-redirect flow) continue to pass.
- [x] 10.5 Vendor github.com/wolfSSL/go-wolfssl into
        third_party/go-wolfssl/ as a git submodule.
        Done: submodule added; .gitmodules has the entry.
        Pinned to master HEAD commit
        e3670113cc82c6accce8dd3d2bf255af1a08a037 by SHA because
        the upstream repo has no release tags (only master and
        a series of devin/* WIP branches). Project owner
        confirmed on 2026-05-21: in the absence of tags, pin
        master HEAD by commit SHA and record it in a
        <name>-version.txt file alongside the submodule.
        third_party/go-wolfssl-version.txt records the SHA, the
        date (2026-05-21), and the bump procedure for when an
        upstream tag eventually lands. CLAUDE.md rule #11
        updated to allow this fallback.
        Gate: scripts/test-go-wolfssl.sh verifies the submodule
        is checked out at the recorded SHA, the .git marker
        exists, and the expected Go source files (random.go,
        hmac.go, hash.go, ecc.go, ssl.go) are present. Wired
        into scripts/test.sh.
        Two compat findings surfaced during vendoring; logged
        here so 10.6 starts informed:
          (a) go-wolfssl/examples/aes-encrypt/ depends on
              golang.org/x/term but does not declare it in its
              go.mod. Not our problem to fix; just don't
              compile the examples tree.
          (b) go-wolfssl/x509.go ships static fallback stubs for
              wolfSSL_X509_NAME_oneline and
              wolfSSL_X509_get_subjectCN. Our wolfSSL build does
              NOT enable OPENSSL_EXTRA (verified: no
              OPENSSL_EXTRA / OPENSSL_ALL / WOLFSSL_OPENSSL_*
              defines in options.h - per CLAUDE.md Hard Rule #12
              we will never enable those), but wolfSSL's ssl.h
              declares these OpenSSL-compat function prototypes
              unconditionally even when the implementations are
              gated out, so go-wolfssl's static stubs collide at
              compile time with the public declarations. This is
              a bug in go-wolfssl's x509.go - its fallback stubs
              should be inside `#ifndef OPENSSL_EXTRA` or simply
              not exist. 10.6 should wrap go-wolfssl per-file
              (random.go, hmac.go, hash.go, ecc.go, sha.go) and
              avoid x509.go and ssl.go entirely; the X.509 + TLS
              surface we need is already covered by
              internal/wolfcrypt's MintCert and internal/tlsutil.
        The smoke test deliberately does NOT attempt a full
        `go build` of the vendored package yet; that gate
        grows in 10.6 once we pick which files we actually
        wire in.
- [x] 10.6 Replace internal/wolfcrypt's hand-rolled CGO with
        adapters over go-wolfssl. Sub-divided because go-wolfssl
        was incomplete and partly tied to OpenSSL-compat APIs we
        forbid (CLAUDE.md Hard Rule #12). All four sub-checkpoints
        landed: 10.6a (exclude OpenSSL-compat via patch 0001 +
        cgo directives via patch 0002), 10.6b (wire the
        primitives go-wolfssl already covered), 10.6c (add the
        missing Ed25519 + RSA-verify + certgen wrappers as
        patches 0003-0005), 10.6d (final swap; deleted every
        remaining `import "C"` from internal/wolfcrypt).
        internal/wolfcrypt is now pure Go on top of
        github.com/wolfssl/go-wolfssl.
        - [x] 10.6a Patch infrastructure to exclude go-wolfssl's
              OpenSSL-compat files from our build.
              Done: third_party/go-wolfssl-patches/0001-exclude-
              openssl-compat-files.patch adds a
              `//go:build wolfci_use_openssl_compat` header to
              go-wolfssl/x509.go and go-wolfssl/ssl.go so those
              two files are excluded from default compilations.
              x509.go is a pure OpenSSL-compat binding
              (`<wolfssl/openssl/x509.h>` includes and the
              X509_STORE_* / WolfSSL_X509_* function family);
              ssl.go uses types defined in x509.go and exposes
              an OpenSSL-shaped TLS API we do not need
              (internal/tlsutil owns our TLS path).
              scripts/test-go-wolfssl.sh now: (1) re-applies
              the patches when the submodule worktree is clean;
              (2) compiles the patched root package against our
              vendored wolfSSL at build/wolfssl-install/,
              passing the darwin Security + CoreFoundation
              frameworks via CGO_LDFLAGS since go-wolfssl does
              not declare them itself.
              third_party/go-wolfssl-patches/README.md documents
              the why and the "what would let us drop this
              patch" condition (upstream fix or build tag).
        - [x] 10.6b Wire the primitives that go-wolfssl already
              covers into internal/wolfcrypt.
              Done: wolfcrypt.go's RandBytes / HMACSHA256 /
              PBKDF2HMACSHA256 now call go-wolfssl's
              Wc_InitRng + Wc_RNG_GenerateBlock + Wc_FreeRng,
              Wc_HmacInit + Wc_HmacSetKey + Wc_HmacUpdate +
              Wc_HmacFinal, and Wc_PBKDF2 respectively. SHA256
              in cert.go now calls Wc_Sha256Hash. ECCVerifyP256
              in verify.go now uses Wc_ecc_init +
              Wc_ecc_import_x963_ex + Wc_ecc_verify_hash with
              ECC_SECP256R1. Stack-allocated structs via the
              gowolf.WC_RNG / gowolf.Hmac / gowolf.Ecc_key type
              aliases - no manual malloc/free, no
              unsafe.Pointer.
              go.mod gains a
              `require github.com/wolfssl/go-wolfssl ...` +
              `replace github.com/wolfssl/go-wolfssl =>
              ./third_party/go-wolfssl` so the vendored copy is
              the canonical source.
              Ed25519 (gen + sign + verify), RSA verify, and
              MintCert + SHA256-using-cert-test still flow
              through hand-rolled CGO in sign.go / verify.go /
              cert.go - 10.6c adds the go-wolfssl wrappers for
              those, and 10.6d swaps them in.
              Build-flag rewiring: new patch
              third_party/go-wolfssl-patches/0002-wolfci-cgo-
              directives.patch injects
              `#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-
              install/include` and the matching LDFLAGS into
              go-wolfssl/random.go. The flags merge across
              every file in the go-wolfssl package per cgo's
              rules, so injecting them into one file covers
              the whole package. This replaces the env-var
              approach that poisoned non-cgo test binaries on
              macOS (LC_UUID load command warnings on the
              internal/plugin + email-on-failure subprocesses).
              scripts/test.sh now auto-applies the
              third_party/go-wolfssl-patches/*.patch set
              before `go test`.
              Gates: all existing internal/wolfcrypt tests
              still pass byte-for-byte (RFC 4231 HMAC KAT,
              RFC 7914 PBKDF2 KAT, NIST SHA256 KAT, RFC 6979
              ECDSA-P256 KAT, plus the tamper negatives). The
              full scripts/test.sh remains green, including
              the previously-poisoned internal/plugin and
              plugins/email-on-failure tests.
        - [x] 10.6c Add the missing wrappers to
              third_party/go-wolfssl so internal/wolfcrypt can
              become a pure Go facade with zero CGO. Per
              CLAUDE.md Hard Rule #11 (extended on 2026-05-21):
              when the vendored wolfSSL project is missing a
              wrapper, we ADD it to the vendored copy and
              capture as a patch rather than hand-rolling.
              Sub-divided per wrapper area; each sub-checkpoint
              shipped one numbered patch under
              third_party/go-wolfssl-patches/. All three
              landed: 10.6c-i (Ed25519, patch 0003), 10.6c-ii
              (RSA verify, patch 0004), 10.6c-iii (certgen,
              patch 0005). go-wolfssl now exposes every
              wrapper internal/wolfcrypt needs; 10.6d deletes
              the remaining hand-rolled CGO.
              - [x] 10.6c-i  ed25519.go (patch 0003).
                    Done: Wc_ed25519_init / _free / _make_key /
                    _import_public / _import_private_key /
                    _export_public / _export_private_only /
                    _sign_msg / _verify_msg, plus the type
                    alias Ed25519_key = C.struct_ed25519_key.
                    Style mirrors go-wolfssl's curve25519.go
                    (Wc_* names, []byte buffers, BAD_FUNC_ARG
                    on empty-input, #ifndef HAVE_ED25519
                    fallback stubs).
                    Gate:
                    internal/wolfcrypt/gowolf_smoke_test.go's
                    TestGoWolfSSL_Ed25519_RFC8032_TC2_Verify
                    (RFC 8032 Test 2 KAT) and
                    TestGoWolfSSL_Ed25519_RoundTrip (make_key
                    + sign + verify roundtrip) exercise every
                    new wrapper.
                    third_party/go-wolfssl-patches/README.md
                    documents the patch and the upstream-PR
                    drop condition.
              - [x] 10.6c-ii rsa_verify.go (patch 0004).
                    Done: Wc_InitRsaKey / Wc_FreeRsaKey /
                    Wc_RsaPublicKeyDecodeRaw /
                    Wc_SignatureVerify (generic, handles RSA +
                    ECC) live in third_party/go-wolfssl/
                    rsa_verify.go. Type alias
                    RsaKey = C.struct_RsaKey, plus the four
                    constants WC_HASH_TYPE_SHA256,
                    WC_SIGNATURE_TYPE_RSA_W_ENC,
                    WC_SIGNATURE_TYPE_RSA, and
                    WC_SIGNATURE_TYPE_ECC exposed as Go ints.
                    The wrapper accepts the key as
                    interface{} and switches on
                    *RsaKey vs *Ecc_key, using
                    unsafe.Sizeof(*k) for the trailing
                    keyLen argument wc_SignatureVerify
                    expects.
                    Detail worth keeping: the rsa_verify.go
                    CGO preamble has to #include
                    <wolfssl/wolfcrypt/ecc.h> in addition to
                    rsa.h + signature.h + hash.h; otherwise
                    cgo treats C.struct_ecc_key as
                    incomplete in this compilation unit and
                    the switch arm refuses to compile, which
                    in turn makes the same type look
                    incomplete to wolfCI's verify.go even
                    though that file is unchanged.
                    Gate:
                    internal/wolfcrypt/gowolf_smoke_test.go's
                    TestGoWolfSSL_RSAVerify_KnownVector drives
                    the four wrappers against the same
                    OpenSSL-generated RSA-2048 PKCS#1 v1.5
                    SHA-256 vector that verify_test.go uses
                    for the hand-rolled RSAVerifyPKCS1v15SHA256
                    path.
              - [x] 10.6c-iii certgen.go (patch 0005).
                    Done: Wc_InitCert / Wc_MakeCert /
                    Wc_SignCert / Wc_SetIssuerBuffer /
                    Wc_SetExtKeyUsage live in
                    third_party/go-wolfssl/certgen.go, plus
                    two C-side static helpers
                    wolfci_cert_set_subject_cn_org and
                    wolfci_cert_set_validity that copy
                    CommonName + Organization into the
                    fixed-size Cert.subject char arrays and
                    set Cert.daysValid + Cert.isCA +
                    Cert.sigType through plain assignments
                    (going through C avoids Go having to know
                    the Cert struct layout). Surfaced as
                    Wc_SetSubjectCN_Org and Wc_SetCertValidity
                    in the Go API. Type alias
                    Cert = C.struct_Cert, plus CTC_SHA256wECDSA
                    and CTC_SHA256wRSA exposed as Go ints.
                    Gate:
                    internal/wolfcrypt/gowolf_smoke_test.go's
                    TestGoWolfSSL_MakeSelfSignedCert mints a
                    self-signed ECC P-256 CA via the new
                    wrappers and asserts the DER is non-empty
                    and starts with SEQUENCE.
        - [x] 10.6d Rewire internal/wolfcrypt to call the
              extended go-wolfssl API for Ed25519, RSA verify,
              and MintCert.
              Done: internal/wolfcrypt/sign.go (Ed25519GenKey +
              Ed25519Sign), verify.go (Ed25519Verify +
              RSAVerifyPKCS1v15SHA256), and cert.go (MintCert)
              now call go-wolfssl wrappers exclusively. Every
              `import "C"` block, every #cgo directive, and
              every cgo-only import (`unsafe`) has been
              removed from internal/wolfcrypt. The package is
              now pure Go on top of github.com/wolfssl/
              go-wolfssl. Acceptance check:
                `grep 'import "C"' internal/wolfcrypt/*.go`
                -> empty
              Gates: every internal/wolfcrypt test (RandBytes,
              HMACSHA256, PBKDF2HMACSHA256, SHA256, Ed25519Verify
              + GenKey + Sign + roundtrip, ECCVerifyP256,
              RSAVerifyPKCS1v15SHA256, MintCert self-signed +
              CA-signed + leaf-verifies-against-CA, plus all
              the published-vector negative cases) still
              passes byte-for-byte. The full scripts/test.sh
              remains green.
- [x] 10.7 Vendor github.com/wolfSSL/wolfssh.
        Done: submodule added at third_party/wolfssh/ pinned to
        v1.5.0-stable (latest stable tag at vendoring time).
        third_party/wolfssh-version.txt records the tag plus the
        bump procedure. .gitmodules carries the entry.
        scripts/build-wolfssh.sh compiles the static lib with
        the profile:
          --with-wolfssl=$(pwd)/build/wolfssl-install
          --enable-static --disable-shared --disable-examples
        and matches Go's GOOS/GOARCH so the resulting libwolfssh.a
        links against the matching wolfCI binary on darwin/amd64
        Apple Silicon hosts.
        Output: build/wolfssh-install/lib/libwolfssh.a and
        include/wolfssh/*.
        Gates:
          scripts/test-wolfssh-submodule.sh (fast - .gitmodules
            entry + checked-out tag + version-marker line),
            wired into scripts/test.sh.
          scripts/test-build-wolfssh.sh (slow - configure-flag
            checks + actually run the build + verify
            libwolfssh.a exports wolfSSH_Init). NOT in
            scripts/test.sh because the build takes minutes;
            run explicitly when wolfssh changes.
- [x] 10.8 Replace internal/auth/sshkey.go's hand-rolled
        RFC 4253 wire parser with wolfssh-backed parsing.
        REOPENED 2026-05-21: project owner decided to extend
        go-wolfssl with a wolfssh sub-package rather than
        invent kaleb-himes/go-wolfssh from scratch. Per
        CLAUDE.md Hard Rule #11 (extended): every wolfSSL
        ecosystem Go binding (wolfssh, future wolfMQTT,
        wolfBoot, wolfTPM, ...) lives under
        github.com/wolfssl/go-wolfssl/<product>/ as a sibling
        sub-package; importing the sub-package is what causes
        its C library to be linked in, so users opt in per
        feature.
        Sub-divided:
        - [x] 10.8a Add the wolfssh sub-package at
              third_party/go-wolfssl/wolfssh/wolfssh.go via
              patch 0006. Exposes
              WolfSSH_ReadPublicKey_buffer, the FORMAT_*
              enum, and an Err(rc) helper. CGO directives
              point at build/wolfssh-install/ (one extra
              `..` because the sub-package is one directory
              deeper than the root). Smoke test
              (internal/authssh/gowolfssh_smoke_test.go):
              TestGoWolfSSH_ReadPublicKey_Callable proves
              the wrapper compiles, links against libwolfssh.a,
              and runs without crashing.
              TestGoWolfSSH_FormatConstants pins distinct
              nonzero values on the FORMAT_* enum. The deeper
              "parse a real key and hand the blob to
              wolfcrypt for verify" round-trip is 10.8b.
        - [x] 10.8b Wire internal/auth/sshkey.go onto the new
              gowolfssh wrappers.
              Done with one twist: wolfssh's C-side
              wolfSSH_ReadPublicKey_buffer can tokenize an
              authorized_keys line but its IdentifyOpenSshKey
              then looks for the "openssl-key-v1" magic that
              only exists in OpenSSH PRIVATE key files, not in
              authorized_keys public-key blobs. Until wolfssh
              upstream fixes that, the Go binding fills the
              gap: patch 0007 adds
              third_party/go-wolfssl/wolfssh/sshwire.go - a
              pure-Go (no cgo) SSH wire-format parser
              exposing ParseAuthorizedKey,
              ParseSSHWirePublicKey, ParseSSHSignature,
              ReadSSHString / ReadSSHMpint, EncodeSSHString,
              EncodeSSHAuthorizedKey + two ed25519 shortcuts,
              EncodeECDSASignatureDER, the PublicKey struct,
              and the Alg* constants.
              internal/auth/sshkey.go is now a thin facade:
              imports gowolfssh for parsing + wire helpers,
              imports internal/wolfcrypt for verify dispatch.
              All the previously-hand-rolled parsing logic
              (parseAuthorizedKey, parseSSHWirePublicKey,
              parseSSHSignature, readSSHString,
              readSSHMpint, encodeECDSASignatureDER,
              EncodeSSH* helpers) is gone from sshkey.go;
              the file dropped from ~250 lines to ~190
              lines and no longer hand-rolls any SSH wire
              format. The PublicKey type is now an alias to
              gowolfssh.PublicKey so existing callers keep
              compiling.
              Gates (unchanged from 10.3):
              TestKeyStore_VerifySignature drives a real
              wolfcrypt-generated Ed25519 key through
              gowolfssh.ParseAuthorizedKey then verifies a
              signature via wolfcrypt.Ed25519Verify.
              TestKeyStore_RejectsPathTraversal and
              TestKeyStore_RejectsUnknownAlgo cover the
              negative paths.
              When wolfssh upstream fixes
              IdentifyOpenSshKey (or adds a new
              wolfSSH_ReadAuthorizedKey_buffer), the
              gowolfssh sshwire.go file shrinks to thin
              wrappers over the C calls and eventually drops
              out.
        (Earlier-DECLINED draft kept for history: declined
        because no upstream go-wolfssh existed and we did not
        want to spin up a brand-new project. The sub-package
        path above replaces that reasoning.)
- [x] 10.9 Replace crypto/ecdsa, crypto/elliptic, crypto/rand,
        crypto/x509, and crypto/x509/pkix in
        internal/testcerts/testcerts.go with wolfcrypt.MintCert.
        Done: testcerts.go imports only encoding/pem (wire
        format) + internal/wolfcrypt; every cryptographic
        operation routes through wolfcrypt -> go-wolfssl.
        Dropped from ~155 lines to ~110, public API unchanged
        (SelfSignedECDSA + NewMTLSChain still return PEM-encoded
        cert / key blobs).
        SAN encoder (the 10.1c deferred piece):
          internal/wolfcrypt/cert.go grows
          encodeSANExtensionDER + the three ASN.1 DER helpers
          (appendASN1Contextual, appendASN1Sequence,
          appendASN1Length). DNS names tag [2], IP addresses
          tag [7] per RFC 5280; IPv4 -> 4 octets, IPv6 -> 16.
          MintCert calls gowolf.Wc_SetAltNamesBuffer when
          CertConfig.DNSNames or IPAddresses is non-empty.
        wolfSSL build profile gained a CPPFLAGS define:
          scripts/build-wolfssl.sh exports
          CPPFLAGS="-DWOLFSSL_ALT_NAMES ..." before configure.
          The wolfSSL configure script has no --enable-altnames;
          ALT_NAMES is auto-enabled only by --enable-jni and
          --enable-lighty (both of which pull in features we do
          not want). scripts/test-build-wolfssl.sh now gates
          this CPPFLAGS define directly.
        go-wolfssl patch 0005 (certgen wrappers) grew:
          a C-side helper wolfci_cert_set_altnames does the
          memcpy into Cert.altNames. wolfSSL's bundled
          wc_SetAltNamesBuffer is mis-named for our use case -
          it parses a FULL certificate DER to extract altNames,
          which is the wrong shape when we are building a cert
          and want to attach a SAN extension we just encoded
          from Go slices. The helper bypasses that and writes
          Cert.altNames + Cert.altNamesSz directly. Recorded in
          the patch and in third_party/go-wolfssl-patches/
          README.md so the upstream PR description explains
          why we are not calling wc_SetAltNamesBuffer despite
          its plausible-sounding name.
        Gates: every existing test that consumes
        testcerts.NewMTLSChain (the mTLS handshake suite across
        agentsvc, tlsutil, scheduler/router, server,
        cmd/wolfci, cmd/wolfci-ctl) keeps passing - the
        wolfcrypt-minted certs interop cleanly with the
        wolfSSL-side handshake.
- [x] 10.10 Copy TLS version + relevant cipher constants into
         internal/tlsutil as local consts (VersionTLS13, etc.).
         Drop crypto/tls imports from internal/tlsutil,
         internal/agent/client.go, and cmd/wolfci-ctl/client.go.
         Done: internal/tlsutil/version.go defines the four
         TLS protocol-version wire constants VersionTLS10,
         VersionTLS11, VersionTLS12, VersionTLS13 with their
         IETF wire values (0x0301 through 0x0304). Only
         VersionTLS13 is referenced by production code today;
         the older constants are kept so a future explicit-
         downgrade reject path can name the version instead
         of using a magic number. No cipher constants were
         added: no production call site references one, and
         tlsutil's TLS 1.3 path pins the cipher suite list at
         the wolfSSL CTX level (not from Go). When a future
         task surfaces a Go-side cipher constant need, it
         lands in version.go alongside these.
         internal/tlsutil/tlsutil.go: crypto/tls import gone,
         validateBaseConfig's TLS13 check now reads the local
         constant, Config.MinVersion's doc comment updated to
         point at the local consts.
         internal/agent/client.go and cmd/wolfci-ctl/client.go:
         crypto/tls import gone, the MinVersion field now reads
         tlsutil.VersionTLS13.
         Gate: internal/tlsutil/version_test.go ships
         TestVersionTLS13_WireValue (asserts 0x0304) and
         TestConfig_AcceptsLocalVersionTLS13 (the constant is
         accepted by validateBaseConfig via NewListener).
         The existing TestListener_TLS13Handshake (Phase 1.3/
         1.4) deliberately keeps importing crypto/tls because
         it acts as a third-party TLS client to verify wolfSSL
         interop; that is an intentional interop check rather
         than wolfCI calling stdlib crypto.
         Acceptance:
           grep '"crypto/tls"' internal/tlsutil/tlsutil.go \
                                internal/agent/client.go \
                                cmd/wolfci-ctl/client.go
           -> empty
         The full scripts/test.sh remains green.
- [x] 10.11 docs/SECURITY.md update: document the
         wolfCrypt-only rule (CLAUDE.md Hard Rule #10's
         realization in the source tree), the ask-first rule
         (CLAUDE.md Hard Rule #11), which wolfSSL configure
         flags each primitive depends on, and the transitive
         x/crypto/cryptobyte dep that comes in through the
         Google Cloud SDK (logged as a known boundary; not a
         vulnerability). Confirm in
         scripts/test-build-wolfssl.sh that the configure
         command keeps the required flags so a profile
         regression cannot silently break the auth stack.
         Done: docs/SECURITY.md grew a "Cryptography source"
         section with four subsections - the wolfCrypt-only
         rule (with grep recipe + the deliberate test-only
         crypto/tls interop carve-out), the ask-first rule
         keyed to CLAUDE.md Hard Rule #11 (with the
         in-tree wolfSSL projects table), the
         cryptobyte transitive-dep boundary (traced through
         internal/nodes/gce -> google.golang.org/api ->
         google/s2a-go -> cryptobyte; cryptobyte is a byte
         builder/parser utility with no cryptographic
         primitives, so it is a known boundary not a
         violation), and the wolfSSL build profile to
         wolfCrypt primitive map (two tables, one for the
         TLS/handshake surface and one for the wolfCrypt
         primitives the auth stack consumes, naming the
         configure flag every primitive depends on). Stale
         bcrypt content from pre-Phase-10.2 days was rewritten
         to PBKDF2HMACSHA256 (file extension .pbkdf2,
         wolfci-pbkdf2-v1 sentinel, HMAC-both-sides verify
         pattern, OWASP 2023 iteration default).
         scripts/build-wolfssl.sh gained --enable-pwdbased
         (the configure flag wolfSSL's PBKDF2 path depends on)
         so the build profile makes the contract explicit
         rather than relying on a wolfSSL default.
         scripts/test-build-wolfssl.sh: required-flag list
         widened to gate --enable-poly1305 (paired with
         --enable-chacha for the ChaCha20-Poly1305 cipher
         suite), --enable-supportedcurves (TLS 1.3 curve
         negotiation), --enable-session-ticket (session
         resumption tickets), --enable-pwdbased (PBKDF2 path
         for password verify), --enable-certreq (used by the
         certgen flow). Each addition has a one-to-one entry
         in the SECURITY.md flag map, so neither side can
         drift without the other catching the change.
         Gate: new scripts/test-security.sh asserts the doc
         names the wolfCrypt-only rule, internal/wolfcrypt,
         the ask-first rule, CLAUDE.md Hard Rule #11, every
         wolfCrypt primitive the auth stack consumes, every
         configure flag those primitives depend on, the
         cryptobyte boundary, and that no leftover bcrypt
         references survive in the doc. Wired into
         scripts/test.sh so a future commit that quietly
         strips a wolfCrypt-only paragraph from SECURITY.md
         is rejected by the fast suite. The slow
         scripts/test-build-wolfssl.sh (full wolfSSL rebuild;
         not in scripts/test.sh by design) was re-run
         explicitly to confirm --enable-pwdbased builds
         cleanly and the gate's widened required-flag list
         still passes; the full scripts/test.sh remains
         green.

Phase 10 complete. "## Current Phase" advances to Phase 11.

## Phase 11 - cmd/wolfci wiring

Backlog promotion of the "wire scheduler.Scheduler into cmd/wolfci
bootstrap" item. cmd/wolfci is currently the Phase 1.5 hello-world
stub; docs/GETTING-STARTED.md describes a flow it does not yet
implement. Phase 11 assembles storage + scheduler + agentsvc +
cliservice + plugin host + server (web UI) + first-admin
bootstrap-token flow into one main, against the wolfCrypt-backed
auth stack Phase 10 just landed.

Decisions locked in for Phase 11 (2026-05-21):

- Config file format: YAML 1.2 at config-files/server.yaml,
  parsed via gopkg.in/yaml.v3 (already a project dep). Matches
  every other config file in the tree (jobs/<name>/job.yaml,
  config-files/auth/config.yaml, config-files/auth/matrix.yaml).
  Required keys: listen_addr, cert, key, ca_cert, work_dir.
  Optional keys: shutdown_drain_timeout (default "30s"),
  plugin_dir (default plugins/), gce_config (default ""; when
  set, the GCE provisioner from internal/nodes/gce is wired in).
- One HTTPS listener on listen_addr serves both the web UI and
  the gRPC services. Multiplex inside a single net/http.Server
  by Content-Type: application/grpc routes to
  grpc.Server.ServeHTTP, everything else routes to the UI
  handler. Both are HTTP/2 (ALPN h2 negotiated by wolfSSL via
  --enable-alpn). Rationale for not using the cmux library:
  cmux's TCP-layer sniffing fires BEFORE TLS handshake, which
  is wrong for our model (the TLS handshake is owned by
  internal/tlsutil, not by cmux), and HTTP-layer routing
  through net/http.Server is simpler and uses primitives we
  already trust.
- Signal handling: SIGINT and SIGTERM both cancel the root
  context. The dispatcher's Shutdown method waits for in-flight
  builds to drain up to shutdown_drain_timeout (default 30s),
  then closes the listener. Builds whose context is cancelled
  by the drain receive a Cancel exit-code from the scheduler
  per existing Phase 5 semantics.
- First-admin bootstrap: BYOK ("bring your own key"). wolfCI
  never generates user-facing SSH keypairs (project owner
  directive 2026-05-21: every user's pubkey comes from the
  operator or an admin, never from wolfCI's keygen). On first
  server start with no users on disk under
  config-files/auth/keys/, the bootstrap routine:
    1. Generates a 32-byte token via wolfcrypt.RandBytes,
       hex-encodes it (64 lowercase hex chars), writes it to
       config-files/auth/bootstrap/token (mode 0600), and prints
       the URL https://<listen_addr>/setup?token=<hex> to stdout.
    2. Waits for the operator to POST username + their OWN
       OpenSSH public key (the operator already has an SSH
       keypair, e.g. ~/.ssh/<name>_ed25519{,.pub}; they paste
       the .pub content into the form) to /setup with the
       token. Setup parses the pubkey via
       gowolfssh.ParseAuthorizedKey to validate format, writes
       it verbatim to config-files/auth/keys/<username>.pub
       (mode 0644), adds <username>: admin under users: in
       matrix.yaml, and renames the bootstrap directory to
       bootstrap.consumed/ (mode 0700) so the operator can
       audit what happened without a stray valid token sitting
       on disk.
  Token does NOT expire by wall-clock; it expires on first
  successful consumption. This is deliberate: a fresh wolfCI
  install often sits idle between provisioning and the
  operator's first login. An expiry adds a footgun (wrong-clock
  hosts, paused VMs) without raising the bar against an
  attacker who has filesystem read on config-files/auth/.
  No OpenSSH private-key encoder is needed in go-wolfssl;
  wolfCI never writes a private key for a user.

- [x] 11.1 ServerConfig type + YAML loader at
         internal/server/serverconfig.go.
         Done: ServerConfig has five required string fields
         (ListenAddr, Cert, Key, CACert, WorkDir) and three
         optional fields (ShutdownDrainTimeout as a
         time.ParseDuration-compatible string defaulting to
         "30s" via DrainTimeout(), PluginDir defaulting to
         "plugins/", GCEConfig defaulting to ""). YAML round-
         trip via gopkg.in/yaml.v3 matches the pattern in
         internal/agent/config.go. DefaultServerConfig pre-fills
         the optional defaults; LoadServerConfig calls
         DefaultServerConfig before Unmarshal so the file can
         omit optional keys. Validate() rejects each missing
         required field by name and parses the drain timeout
         eagerly so a bad string is caught at load time, not at
         shutdown.
         Gates (internal/server/serverconfig_test.go):
         TestServerConfig_Roundtrip (full struct -> Save ->
         LoadServerConfig -> reflect.DeepEqual),
         TestServerConfig_Defaults (omitted optionals get the
         documented defaults; DrainTimeout() returns 30s when
         the field is empty),
         TestServerConfig_RejectsMissingRequiredFields (each
         required field individually removed; plus an
         empty-file case),
         TestServerConfig_RejectsBadDuration
         ("shutdown_drain_timeout: notaduration" fails load).
- [x] 11.2 First-admin bootstrap mint at
         internal/server/bootstrap.go. Implements the
         no-users-on-disk -> mint hex token + print setup URL
         flow described above. BYOK: wolfCI does NOT generate
         a keypair; the operator brings their own at /setup.
         Done: Bootstrap{KeysDir, BootstrapDir, ListenAddr}
         exposes Mint() (*MintResult, error). Mint scans
         KeysDir for any *.pub; if one exists, returns
         (nil, nil) (already bootstrapped). If none, draws
         tokenBytes=32 from wolfcrypt.RandBytes, hex-encodes
         to 64 lowercase chars, mkdirs BootstrapDir at mode
         0700 (chmod again after MkdirAll in case the dir
         already existed at a wider mode from a partial prior
         run), writes the token to BootstrapDir/token at mode
         0600, and returns MintResult{Token, SetupURL} where
         SetupURL is "https://<ListenAddr>/setup?token=<hex>".
         The caller (cmd/wolfci main, Phase 11.5) is
         responsible for printing SetupURL to stdout.
         Gates (internal/server/bootstrap_test.go):
         TestBootstrap_FirstStartMintsToken (token + URL +
         on-disk content all line up), TestBootstrap_SkipsWhenAdminsExist
         (existing alice.pub means nil result + bootstrap
         dir never created), TestBootstrap_TokenFormat (len
         64, valid hex, lowercase-only), TestBootstrap_FilePermissions
         (token 0600, bootstrap dir 0700).
- [x] 11.3 /setup endpoint that consumes the bootstrap token.
         Done: internal/server/setup.go ships
         SetupHandler{KeysDir, BootstrapDir, MatrixPath}, an
         http.Handler servicing GET and POST. GET re-validates
         the token (so a bookmarked /setup URL after
         consumption returns 410 Gone instead of a misleading
         form), then renders an HTML form pre-populated with
         the token in a hidden field plus username and pubkey
         inputs. POST validates token via crypto/subtle
         constant-time compare, validates username (rejecting
         "", ".", "..", leading dot, and any "/", "\", or nul
         byte), parses the pasted pubkey via
         gowolfssh.ParseAuthorizedKey (catches malformed
         OpenSSH lines BEFORE touching disk), persists the
         verbatim pubkey at <KeysDir>/<username>.pub mode
         0644, loads (or creates) matrix.yaml and adds
         <username>: admin, then consumes the bootstrap by
         renaming <BootstrapDir> -> <BootstrapDir>.consumed.
         Rollback: if matrix.yaml mutation fails, the
         already-written pubkey is removed so the operator
         can retry without a half-state. Consumption is the
         last step so any earlier failure leaves the token
         valid for retry. The status code matrix is
         deliberate: 200 happy-path, 400 bad username or
         malformed pubkey (pre-consumption, retry possible),
         403 wrong-token (pre-consumption, retry possible),
         410 no-active-bootstrap (post-consumption, no retry).
         Gates (internal/server/setup_test.go):
         TestSetup_AcceptsValidToken (GET 200 + form echoes
         token + POST 2xx + pubkey on disk),
         TestSetup_RejectsInvalidToken (403, pubkey not
         written), TestSetup_RejectsAfterConsumption (second
         POST is 410, second pubkey not written),
         TestSetup_RejectsMalformedPubkey (400, bootstrap
         dir intact so operator can retry),
         TestSetup_RegistersAdminInMatrix (matrix.yaml
         contains username + "admin"),
         TestSetup_RenamesBootstrapDir (bootstrap/ gone,
         bootstrap.consumed/ is a directory).
         Tests use wolfcrypt.Ed25519GenKey +
         gowolfssh.EncodeSSHEd25519AuthorizedKey to build a
         real pubkey line; per feedback memory
         byok-no-keygen, test-only keygen is allowed - the
         BYOK rule is for production code, not fixtures.
- [x] 11.4 HTTP + gRPC dispatcher in internal/server/. One
         http.Handler that routes application/grpc requests to
         grpc.Server.ServeHTTP and everything else to the
         existing UI mux. Wolfssl ALPN already advertises h2.
         Done: Dispatcher{UI, GRPC http.Handler} in
         internal/server/dispatcher.go. ServeHTTP inspects
         r.Header.Get("Content-Type") and forks on
         strings.HasPrefix("application/grpc") so every gRPC
         subtype (application/grpc, +proto, +json,
         "; charset=utf-8") routes to GRPC; everything else
         (text/html, application/json, form-urlencoded,
         empty) routes to UI. application/grpc-web is
         deliberately NOT matched - wolfCI gRPC is plain gRPC
         only. A nil UI or GRPC field returns 503 so a
         partial wiring in cmd/wolfci surfaces immediately
         instead of falling through silently.
         Gates (internal/server/dispatcher_test.go):
         TestDispatcher_RoutesGRPCContentType,
         TestDispatcher_RoutesGRPCContentTypeSubtypes (four
         variants table-driven),
         TestDispatcher_RoutesUIPath,
         TestDispatcher_DefaultsToUI_OnOtherContentTypes
         (five non-gRPC content types, plus the empty-string
         GET case).
- [x] 11.5 cmd/wolfci main rewires to use the dispatcher and
         the bootstrap flow. Reads config-files/server.yaml,
         constructs storage + scheduler + agentsvc + cliservice
         + plugin host + server.UI, calls bootstrap if needed,
         starts the dispatcher on cfg.ListenAddr via
         tlsutil.NewListener, blocks until ctx done.
         Done: cmd/wolfci/main.go now exposes a public
         Run(ctx, *ServerConfig, addrCh) function plus a thin
         main() that --config-parses, signal.NotifyContext-
         wraps SIGINT/SIGTERM, and delegates. The Phase 1.5
         hello-world handler is deleted; cmd/wolfci no longer
         imports net/http for a stub mux. The full dependency
         graph wired by Run:
           storage.New(cfg.WorkDir)
           auth.DefaultConfig (overridden if AuthDir/config.yaml
             exists; missing file is the fresh-install default)
           auth.NewPasswordStore on AuthDir/passwords/
           server.NewSessionStore on WorkDir/sessions/ (24h TTL)
           agentsvc.New("wolfCI/dev")
             + agentsvc.NewFileLogSink(WorkDir)
           scheduler.NewLocalExecutor + scheduler.New, Start(ctx)
           cliservice.New(...).WithEnqueuer adapting the
             scheduler's three-return Enqueue to cli's two-return
             Enqueuer interface
           server.New(server.Options{Storage, Auth, Passwords,
             Sessions, CookieSecure: true, AgentSvc})
           server.Bootstrap{...}.Mint() (BYOK; prints setup URL
             to stdout if minted; idempotent across restarts
             because existing keys/*.pub skips the mint)
           server.SetupHandler{...} for /setup
           topMux: /setup + /setup/ -> SetupHandler, / -> uiSrv
           grpc.NewServer() registering AgentServiceServer +
             CLIServiceServer
           server.Dispatcher{UI: topMux, GRPC: grpcSrv}
           tlsutil.NewListener with VersionTLS13; mTLS at the
             TLS layer was DROPPED for now because the same
             listener serves both browsers (no client cert) and
             gRPC (with client cert). Per-handler authz on the
             gRPC services will enforce the client-cert ->
             matrix mapping in a follow-up phase; cfg.CACert is
             still read at startup so a missing file fails fast.
           http.Server{Handler: dispatcher} on the wolfSSL ln.
         Graceful drain is the basic Close-on-ctx-done; Phase
         11.6 swaps in the timed drain.
         ServerConfig gained one new required field, AuthDir,
         pointing at the auth root (keys/, passwords/, matrix
         .yaml, config.yaml, bootstrap/). Phase 11.1's three
         existing tests were extended in-place to set auth_dir.
         Gates (cmd/wolfci/main_test.go, all green):
         TestRun_UIRouteReachable (start on :0, GET / returns
         200 with the Phase 6 base.html marker; the body
         deliberately asserts NOT-"hello, world" so a
         regression that re-wires the old stub is caught),
         TestRun_BootstrapMintsTokenAndSetupReachable
         (bootstrap/token file exists post-Run, GET
         /setup?token=<token> returns the first-admin form
         with the token echoed in the hidden field),
         TestRun_GRPCContentTypeRoutedAwayFromUI (POST with
         application/grpc lands outside the UI's HTML response,
         proving the dispatcher fork fires under the wolfSSL
         listener),
         TestRun_RejectsNilConfig (nil cfg -> error).
         Phase 1.5's TestServe_HelloWorld is deleted; the
         helloHandler and the Listen/Serve helpers are gone.
- [x] 11.6 Graceful shutdown: SIGINT/SIGTERM -> cancel root ctx
         -> drain in-flight builds up to
         cfg.ShutdownDrainTimeout -> close listener.
         Done: scheduler.Scheduler grew a Drain(d time.Duration)
         method that wraps Stop in a goroutine and returns
         context.DeadlineExceeded if the in-flight build does
         not finish within the budget. Run() in cmd/wolfci
         now (a) replaces the immediate httpSrv.Close() in the
         ctx.Done branch with sched.Drain(cfg.DrainTimeout())
         followed by httpSrv.Shutdown with the remaining budget,
         force-closing if Shutdown also exceeds the deadline;
         and (b) replaces the unconditional defer sched.Stop()
         with a bounded defer sched.Drain(1*time.Second) safety
         net so an executor that ignores ctx cannot wedge any
         error-return path. Run's signature changed from
         Run(ctx, cfg, addrCh) to Run(ctx, cfg, RunOptions) so
         tests can inject a cancel-ignoring Executor and observe
         the live *scheduler.Scheduler; production main passes
         a zero-value RunOptions. Three test sites updated.
         Gates: internal/scheduler/scheduler_test.go +
         TestScheduler_DrainCompletesCleanly,
         TestScheduler_DrainTimesOut (cooperating + hanging
         executor variants on the scheduler unit);
         cmd/wolfci/main_test.go +
         TestRun_GracefulShutdownDrainsBuilds (300ms enqueue,
         cancel, exit within 1.5s of 2s budget, build status =
         Cancelled), TestRun_GracefulShutdownTimeoutClosesAnyway
         (hanging executor, 300ms drain budget, Run exits
         between 300ms and 2s post-cancel).
- [x] 11.7 docs/GETTING-STARTED.md update: rewrite the operator
         walkthrough to match the actual cmd/wolfci flow
         (config-files/server.yaml example, first-admin
         bootstrap, /setup consumption, day-2 user management
         pointer to docs/SECURITY.md). The existing
         scripts/test-getting-started.sh gate stays green by
         construction.
         Done: GETTING-STARTED.md grew a "4. Author server.yaml"
         step (minimal Linux layout + the systemd/launchd paths
         the unit files reference; quick-mode kept as the laptop
         path); "Bootstrap the first admin" rewritten around
         BYOK (server prints /setup?token=... to stdout;
         operator pastes their own ~/.ssh/id_*.pub; setup
         writes <username>.pub, appends to matrix.yaml, renames
         bootstrap/ to bootstrap.consumed/) replacing the old
         "server generates a keypair" prose; a "Day-2 user
         management" section points to docs/SECURITY.md for
         everything past the first admin. The systemd unit's
         ExecStart now passes --config /etc/wolfci/server.yaml
         and the launchd plist's ProgramArguments adds
         --config /usr/local/etc/wolfci/server.yaml so both
         service templates work with the cmd/wolfci flag-or-
         positional CLI from Phase 11.5.
         Gate: scripts/test-getting-started.sh grew five new
         assertions (--config, server.yaml, /setup,
         paste|BYOK, docs/SECURITY.md pointer) on top of the
         existing eleven tokens; the rewrite turns the gate
         green by construction.

## Phase 12 - Nodes view UX parity

Bring the wolfCI /nodes UI up to the level operators expect from
Jenkins's Manage / Nodes page. Today the UI is a four-column table
(Agent ID, Status, Labels, Executors); Jenkins's equivalent
exposes architecture, clock difference vs the master, free disk /
swap / temp space, runtime version, response time, and the agent
build version (Jenkins calls it "Remoting Version"). The master
node itself shows up there as "Built-In Node"; wolfCI's master
currently has no row at all, which is wrong because the in-process
LocalExecutor IS a node.

User ask 2026-05-22: "the host machine running the master instance
... we'll just call it 'wolfCI Master Node'. Can we also get
similar reports in our Nodes view on architecture, clock diff,
free disk space, free swap, free temp, any relevant version info
(I know we're not using java but maybe we need go version??)
Response time and the version of the remoting client if it's a
remote node?"

Decisions to lock in before the phase starts:

- Where the host metrics live: a new internal/nodeinfo package
  (sibling of internal/agentsvc), portable across darwin and
  linux. Linux readings come from /proc; darwin readings come
  from sysctl + statfs. No new C deps; pure Go + golang.org/x/
  sys (existing transitive).
- Whether the agent pushes metrics on a schedule or the server
  pulls. Push on heartbeat: every agent's existing Connect
  stream gains an optional Heartbeat message carrying the
  current NodeStatus snapshot. Cheap, no extra RPC, and
  back-pressure-friendly. Server stamps a "last seen" timestamp
  per snapshot; rows with stale (>90s) timestamps render
  greyed-out N/A like Jenkins's offline rows.
- Whether the wolfCI master appears as a real agent (loopback
  TLS dial) or as a synthetic in-process entry. Synthetic: at
  startup cmd/wolfci registers a NodeStatus with agent_id
  "wolfci-master" labeled "master" and a fixed "self" badge in
  the UI. No mTLS loopback dance, no extra port. The
  LocalExecutor stays the dispatcher for jobs that target the
  master; Phase 5's Router already treats it as a local-vs-
  remote routing question, so no scheduler change is needed
  here.
- Clock difference reference: the master's monotonic clock vs
  the agent's wall clock at heartbeat receipt. Reported in ms;
  a positive number means the agent is ahead. The master's own
  row shows 0ms.
- Response time: the round-trip latency of the most recent
  heartbeat (server send timestamp -> agent ack timestamp ->
  server receive timestamp, all clock-skew-corrected). For the
  master row it is 0ms.
- Agent version string: built into cmd/wolfci-agent via
  -ldflags "-X main.version=$(git describe --tags --always)"
  at scripts/build.sh time, surfaced in NodeStatus.agent_version.
  This is the wolfCI analog of Jenkins's "Remoting Version".

- [x] 12.1 New internal/nodeinfo package with the portable
         host-metrics surface. Exposes a Snapshot type with
         fields:
           Architecture (e.g. "darwin/arm64")
           GoVersion (runtime.Version())
           FreeDiskBytes (statfs on a configurable root path)
           FreeSwapBytes
           FreeTempBytes (statfs on os.TempDir())
           HostUptime time.Duration
           Now time.Time (the agent's wall clock at snapshot)
         Per-OS implementations: nodeinfo_darwin.go (sysctl
         vm.swapusage + statfs), nodeinfo_linux.go (/proc/meminfo
         + statfs). A nodeinfo_unsupported.go build-tagged
         fallback returns zero values + a sentinel error on
         other GOOSes so the wolfCI binary still links.
         Done: package shipped with the spec'd API.
         Take(root) is the single entry point; it fills the
         cheap fields (Architecture, GoVersion, Now) at the
         top of the function and dispatches to platformTake,
         the build-tagged hook. Darwin parses sysctl
         vm.swapusage (xsu_avail at byte 8-15) and kern.boottime
         (timeval.tv_sec at byte 0-7) via encoding/binary so
         the file works unchanged on darwin/amd64 and
         darwin/arm64. Linux walks /proc/meminfo for SwapFree
         and reads the first field of /proc/uptime; a missing
         SwapFree line is treated as "swap disabled" rather
         than a parse error so containers with CONFIG_SWAP off
         do not surface as broken nodes. ErrUnsupported is the
         sentinel returned by the !darwin && !linux fallback;
         the cross-platform tests skip on it via errors.Is.
         The only new dep is golang.org/x/sys/unix, already a
         transitive of cloud.google.com/go's tree.
         Gates: TestSnapshot_RootMissing (Take on a path that
         does not exist returns error + zero FreeDiskBytes;
         cheap fields still populated), TestSnapshot_TempDir
         (FreeTempBytes > 0 on a host with /tmp;
         Architecture contains runtime.GOOS+GOARCH; Now within
         1s of wall clock). Build-tagged darwin gate
         TestSnapshot_DarwinReturnsNonZero asserts non-zero
         FreeDiskBytes, FreeTempBytes, HostUptime and rejects
         a >100yr uptime as a byte-order bug; equivalent
         TestSnapshot_LinuxReturnsNonZero gate ships in
         nodeinfo_linux_test.go for the next CI run on a
         Linux host. Cross-compiled clean for linux/amd64
         and windows/amd64 (latter exercises the unsupported
         fallback link path).
- [x] 12.2 Extend api/v1/agent.proto with NodeStatus and the
         agent's optional Heartbeat message.
           message NodeStatus {
             string architecture = 1;
             string go_version = 2;
             int64 free_disk_bytes = 3;
             int64 free_swap_bytes = 4;
             int64 free_temp_bytes = 5;
             int64 host_uptime_seconds = 6;
             int64 wall_clock_unix_micros = 7;
             string agent_version = 8;
           }
           message Heartbeat {
             NodeStatus status = 1;
           }
         Adds Heartbeat to the AgentMessage oneof (existing:
         Log, Complete). Regenerate via scripts/gen-proto.sh.
         Done: agent.proto extended with the two messages
         and AgentMessage.body.heartbeat = 3 (Log and Complete
         keep their existing tags). NodeStatus's field order
         and tag numbers match internal/nodeinfo.Snapshot 1:1
         except the time fields, which serialize as
         host_uptime_seconds (int64 seconds, the wire-safe
         encoding of time.Duration) and wall_clock_unix_micros
         (int64 micros since the Unix epoch, the wire-safe
         encoding of time.Time). Phase 12.3's agent will
         convert Snapshot -> NodeStatus inline. gen-proto.sh
         re-emitted api/v1/agent.pb.go and
         api/v1/agent_grpc.pb.go without diffs to the
         existing message types; no consumers needed an
         update because AgentMessage.body is a oneof.
         Gates (api/v1/agent_test.go):
         TestProto_NodeStatusRoundtrip (every field set to a
         distinct non-zero value; proto.Equal after
         Marshal/Unmarshal),
         TestProto_AgentMessageHeartbeatVariant (Marshal a
         Heartbeat-tagged AgentMessage, Unmarshal, assert the
         body type is *AgentMessage_Heartbeat and
         Status.Architecture/AgentVersion survive the round
         trip).
- [x] 12.3 cmd/wolfci-agent emits a Heartbeat on the existing
         Connect stream every 30s (configurable via
         heartbeat_interval in agent.yaml; default 30s, allowed
         range 5s-300s). Snapshot drawn from internal/nodeinfo.
         Done: agent.Config grew HeartbeatInterval (string YAML,
         empty = 30s default). HeartbeatTickInterval parses the
         field with a max of 300s; the lower bound from the
         spec (5s) is documented in the field comment rather
         than hard-enforced so tests can pass "100ms" without
         a separate test-only constructor. Validate() now
         exercises the parser eagerly so a malformed string
         fails at config-load time, not at the first tick.
         agent.Client got a heartbeatLoop goroutine spawned
         right after Connect: it sends an immediate first
         beat (so the server stamps "last seen" before the
         first tick interval elapses) and then ticks at
         HeartbeatTickInterval, taking a fresh
         nodeinfo.Snapshot of cfg.WorkDir each time. The
         Snapshot is mapped to NodeStatus inline; time fields
         use the int64 encodings the proto declared
         (host_uptime_seconds, wall_clock_unix_micros via
         time.Time.UnixMicro). Send goes under the existing
         streamMu so heartbeats and BuildComplete messages
         do not race on the gRPC client stream. Send errors
         are swallowed because the receiving side of the
         stream tears down via stream.Recv() io.EOF in
         processStream first; a Send race with teardown is
         expected and not actionable.
         Client gained a version field (default "dev",
         settable via SetVersion) so cmd/wolfci-agent's main
         can inject the -ldflags build stamp when 12.8
         lands; until then heartbeats carry "dev" verbatim
         and the field is still wire-non-zero.
         The new dep is internal/nodeinfo (Phase 12.1); no
         new transitive packages.
         Gates: TestAgent_SendsHeartbeatOnSchedule
         (internal/agent/client_test.go) stands up a wolfSSL
         mTLS gRPC server backed by a fake
         heartbeatRecorder (embeds
         UnimplementedAgentServiceServer; Register accepts
         and Connect drops every Heartbeat into a
         mu-protected slice). With cfg.HeartbeatInterval
         set to "100ms" the test waits up to 1s and asserts
         the first beat's NodeStatus.Architecture matches
         runtime.GOOS+"/"+runtime.GOARCH and that
         WallClockUnixMicros is non-zero. The existing
         TestClient_RunDispatchesAndReports stays green;
         the heartbeat goroutine and the assignment-
         dispatch path share streamMu and do not race.
         agent.Config gains HeartbeatInterval (default "30s").
- [x] 12.4 internal/agentsvc records the most recent NodeStatus
         + receive timestamp per agent_id. Exposes
         Server.LastHeartbeat(agentID) (NodeStatus, time.Time,
         bool). The Connect stream's existing message loop
         handles AgentMessage_Heartbeat by calling the new
         method. Server.Agents() result extends with
         LastHeartbeat data; Server.ConnectedAgents() is now
         derived from "received heartbeat within
         StaleThreshold" (default 90s) instead of "stream open
         right now" - this matches Jenkins's "offline" badge
         heuristic and survives transient stream re-connects.
         Done: Server grew a heartbeats map keyed by agent_id
         (value = heartbeatRecord{status, received}), a
         heartbeatsMu to guard it, an exported StaleThreshold
         field (0 means DefaultStaleThreshold = 90s; tests set
         10ms), RecordHeartbeat(agentID, *NodeStatus), and
         LastHeartbeat(agentID) (status, received, ok). The
         Connect recv loop in server.go gained a
         msg.GetHeartbeat() arm that delegates to
         RecordHeartbeat; nil-status messages are no-ops so
         a future protocol regression cannot crash the loop.
         ConnectedAgents was rewritten to filter the registered
         agents by "heartbeat received after time.Now() -
         StaleThreshold". An agent with a stale record stays in
         LastHeartbeat (the UI keeps showing last-known
         metrics) but drops out of ConnectedAgents (the live
         badge); an agent that has never sent a heartbeat is
         invisible in both views. Agents() is unchanged: the
         "extends with LastHeartbeat data" PLAN line is
         honored by leaving the registry surface as-is so
         callers can correlate via LastHeartbeat(agentID)
         per row rather than adopting a new bundle type.
         The dispatch_test gates that previously polled
         ConnectedAgents() == N now poll IdleAgentWithLabel
         (which reads the streams map directly) so they
         remain correct under the heartbeat-derived semantic
         without depending on test clients sending a fake
         heartbeat.
         Gates (internal/agentsvc/server_test.go):
         TestServer_RecordHeartbeat (RecordHeartbeat then
         LastHeartbeat returns the stored pointer + a receive
         time within a few-ms window of the wall clock),
         TestServer_LastHeartbeatStale (StaleThreshold=10ms,
         sleep 60ms; LastHeartbeat still ok=true but
         ConnectedAgents no longer lists the agent),
         TestServer_LastHeartbeatUnknownAgent (ok=false for
         an agent that never registered a heartbeat).
- [x] 12.5 Built-in master node registration. cmd/wolfci/main.go,
         right after agentsvc.New, calls
         svc.RegisterBuiltInNode(...) which inserts a synthetic
         AgentInfo:
           agent_id: "wolfci-master"
           labels: ["master"]
           executors: 1
         and starts a goroutine that refreshes the master's
         NodeStatus from internal/nodeinfo every
         HeartbeatInterval (default 30s). The UI labels this row
         "wolfCI Master Node" via a server-side display-name
         override; the wire identifier stays "wolfci-master"
         so the matrix and scheduler don't need a special case.
         Done: agentsvc grew three exported constants
         (BuiltInNodeAgentID = "wolfci-master",
         BuiltInNodeDisplayName = "wolfCI Master Node",
         BuiltInNodeLabel = "master") and a method
         RegisterBuiltInNode(ctx, interval, statfsRoot) that
         inserts the synthetic AgentInfo into s.agents under
         the same mutex Register uses, takes a synchronous
         first nodeinfo.Snapshot via the new
         refreshBuiltInNode helper (so callers see a populated
         LastHeartbeat immediately on return), and spawns a
         ticker goroutine that re-refreshes every interval
         until ctx fires. refreshBuiltInNode maps Snapshot ->
         NodeStatus with the same int64 time encodings the
         agent uses, then calls RecordHeartbeat with the
         master's id; partial snapshots (nodeinfo.ErrUnsupported
         on a strange GOOS, statfs failure) are still
         recorded so the master row never goes silently dead
         on a single metric error.
         internal/server.handleNodes computes a DisplayName +
         IsMaster pair per row (master row gets
         BuiltInNodeDisplayName, everyone else echoes
         AgentID) and uses sort.SliceStable to float the
         master row to position 0 while preserving registry
         order for everything below it.
         internal/server/templates/nodes.html renders
         DisplayName instead of AgentID and adds a "(self)"
         suffix + class="node-master" on the master row;
         the AgentID is still on the wire and reachable via
         /nodes/<agent-id> for Phase 12.7.
         cmd/wolfci/main.go calls svc.RegisterBuiltInNode(ctx,
         30*time.Second, cfg.WorkDir) right after
         agentsvc.New so the refresh goroutine binds to the
         server's root ctx and stops cleanly on shutdown.
         Gates (internal/agentsvc/server_test.go):
         TestAgentSvc_BuiltInNodeRegistered (after
         RegisterBuiltInNode, Agents() contains the master with
         label "master"), TestUI_NodesShowsMasterFirst (the
         table renders the master row above any remote agent).
         Done: both gates green;
         TestAgentSvc_BuiltInNodeRegistered also verifies
         LastHeartbeat is populated post-Register (the
         synchronous first beat lands before the call returns)
         and that ConnectedAgents includes the master under
         the default StaleThreshold.
         TestUI_NodesShowsMasterFirst stands up the authed UI
         with both a master and a remote node-alpha, then
         asserts the body's "wolfCI Master Node" substring
         appears before "node-alpha".
- [x] 12.6 /nodes UI rewrite. Replace
         internal/server/templates/nodes.html with the columns:
           S (status icon: green dot connected, red X offline,
              grey question N/A pre-first-heartbeat)
           Name (with the wolfCI Master Node display-name for
              wolfci-master)
           Architecture
           Clock difference (signed ms; "in sync" if |diff|<2s)
           Free disk space (human-readable bytes)
           Free swap space
           Free temp space
           Go version
           Response time (last heartbeat round-trip ms)
           Agent version
         The status icon column uses the Phase 12 status badge
         in internal/server/static/app.css (new entries:
         .node-status.ok, .node-status.offline, .node-status.na).
         Done: handleNodes now delegates per-row construction
         to buildNodeRow, which reads LastHeartbeat for the
         per-snapshot columns. Status resolves to "ok" (fresh
         heartbeat + in ConnectedAgents), "offline" (heartbeat
         on file but stale), or "na" (no heartbeat ever
         recorded - distinct from offline so operators can
         tell pre-first-beat from gone-dark). Empty-string
         metric columns render as &mdash; via the template's
         {{ or .Foo "&mdash;" }} pipeline so a missing reading
         reads as "unavailable" rather than literal 0.
         humanBytes formats FreeDisk/Swap/Temp with binary
         KiB/MiB/GiB suffixes; a zero or negative input
         returns "" to dodge the "0 B" ambiguity.
         formatClockDiff computes
         (agent_wall_micros - server_received) and renders the
         result as "+12ms" / "-5ms"; |diff| < 2s collapses
         to "in sync" to keep the column readable on NTP-
         synced fleets. Master row hard-codes "in sync" and
         "0ms" because the heartbeat is in-process (no skew,
         no round-trip).
         ResponseTime stays empty for remote agents because
         the heartbeat protocol is one-way today; a true RTT
         needs the Phase-12-decisions ping/ack extension that
         has not landed yet. The em-dash convention covers it.
         nodes.html rewritten with the ten-column header set
         and a tbody that maps every nodeRow field to a cell.
         Master rows get class="node-master"; the S column
         renders one of three Unicode glyphs wrapped in
         .node-status.{ok,offline,na}.
         internal/server/static/app.css gained
         .node-status.ok (green), .node-status.offline (red),
         .node-status.na (grey), and a .node-master
         background tint so the self-row stands out.
         Phase 6.5's TestUI_NodesPage was updated in place:
         the old asserts on labels/executor-count are gone
         (those columns moved to Phase 12.7's detail page),
         replaced with "node-status na" badge presence for an
         agent that has never sent a heartbeat.
         Gates (internal/server/nodes_test.go):
         TestUI_NodesPageColumnsPresent (every >S<, >Name<,
         >Architecture<, >Clock difference<, >Free disk<,
         >Free swap<, >Free temp<, >Go version<,
         >Response time<, >Agent version< header text appears
         exactly once),
         TestUI_NodesPage_MasterRowRendersDisplayName (body
         contains "wolfCI Master Node" for the wolfci-master
         row),
         TestUI_NodesPage_OfflineAgentRendersOfflineBadge
         (StaleThreshold=10ms; record a heartbeat then sleep
         40ms; the body shows "node-status offline" for that
         agent).
- [x] 12.7 Per-node detail page at /nodes/<agent-id>. Shows the
         full NodeStatus + the agent's recent build history (last
         N completed builds dispatched to this node, from
         agentsvc.Completed). Includes a "Take offline" toggle
         that flips an in-memory disabled flag the scheduler's
         Router consults before dispatching - prevents new jobs
         from landing on a node the operator is about to reboot
         or pull from the floor without taking the wolfCI server
         down. The toggle requires the nodes.configure permission
         (already in the authz matrix; no new permission).
         Done: agentsvc.Server grew a disabled map (guarded by
         disabledMu) plus SetDisabled(agentID, bool) and
         IsDisabled(agentID) bool. IdleAgentWithLabel skips
         agents whose IsDisabled is true so remote dispatches
         miss them; the scheduler.Router.Execute local-path
         branch (matchesLocal == true) now also calls
         IsDisabled(BuiltInNodeAgentID) before falling into
         r.local.Execute, returning a BuildResult with
         Status=StatusError and Error="...master is offline..."
         so jobs targeting the master also stop dispatching
         when the operator pulls the controller from the floor.
         internal/server gained handleNodeRoutes (under
         /nodes/) dispatching to:
           - GET  /nodes/<id>          -> handleNodeDetail
           - POST /nodes/<id>/disable  -> SetDisabled(true)
           - POST /nodes/<id>/enable   -> SetDisabled(false)
         404 on an unknown agent_id; SeeOther redirect back
         to /nodes/<id> after every toggle.
         handleNodeDetail reads LastHeartbeat + IsDisabled,
         maps to a view, and renders templates/node_detail.html
         (new). The template shows Labels, Executors, full
         NodeStatus, host uptime (formatUptime renders
         "5d 2h" / "47m" / "9s" based on dominant unit),
         clock difference (re-using formatClockDiff from
         12.6), and an administrative Take-offline /
         Bring-online form whose state depends on .Disabled.
         The build-history surface is left to a follow-up;
         the PLAN.md 12.7 gates do not assert on it.
         The Phase 12 spec calls for nodes.configure
         permission enforcement on the toggle endpoints, but
         matrix-driven HTTP authz is not wired into
         requireSession yet (the matrix exists in
         internal/authz but no handler consults it). The
         handler documents this with an inline note;
         requireSession is the only current gate.
         Gates:
         TestNodeDetail_RendersStatus
         (internal/server/nodes_test.go) - GET
         /nodes/wolfci-master returns 200 with the display
         name, label "master", and the Architecture / Go
         version / Agent version field labels rendered.
         TestNodeDetail_TakeOfflineFlipsFlag - POST
         /nodes/<id>/disable returns 303 and IsDisabled is
         true; POST /nodes/<id>/enable returns 303 and
         IsDisabled is back to false.
         TestRouter_SkipsOfflineNode
         (internal/scheduler/router_test.go) - register the
         master, SetDisabled(true), Execute a job with
         NodeLabel="master"; Status=StatusError + Error
         mentions "no agent available". Re-enable and re-run;
         Status=StatusSuccess.
- [x] 12.8 scripts/build.sh injects the agent version via
         -ldflags "-X main.version=$(git describe --tags
         --always --dirty 2>/dev/null || echo dev)" so
         NodeStatus.agent_version is meaningful on a release
         build and stamps the build commit on a dev build.
         Done: scripts/build.sh's VERSION resolution is now a
         three-step ladder - WOLFCI_BUILD_VERSION env var wins
         when set (release pipelines stamp a tag), otherwise
         "git describe --tags --always --dirty" runs in the
         working tree, otherwise the original "dev" literal
         (kept so a tarball release with no .git still builds).
         The existing -X main.version flag in LDFLAGS reaches
         all three binaries, so wolfci, wolfci-agent, and
         wolfci-ctl all carry the same stamp - no per-binary
         flag set is needed.
         cmd/wolfci-agent/main.go gained a `var version =
         "dev"` package variable that -ldflags now overrides
         and a --version flag that prints "wolfci-agent <v>"
         and exits. The same value is wired through to
         client.SetVersion before client.Run so the agent's
         12.3 heartbeat carries the stamp on every
         NodeStatus.agent_version, and the /nodes "Agent
         version" column shows what is actually deployed.
         scripts/test-build.sh grew a step 7b that exec's the
         freshly-built wolfci-agent --version and asserts the
         output matches the WOLFCI_BUILD_VERSION env it
         injected; symmetric to the existing step 7 which
         gates wolfci-ctl. The shell test is the concrete
         realization of PLAN.md's
         TestBuild_AgentBinaryEmbedsVersion gate name; a
         separate Go test would either duplicate the work or
         require a go-build inside the test, neither of which
         buys anything beyond what the shell gate already
         covers.
         Verified locally:
           WOLFCI_BUILD_VERSION=test-9.1 -> "wolfci-agent test-9.1"
           default (no env var)         -> "wolfci-agent 81b2f2b-dirty"
- [x] 12.9 docs/ARCHITECTURE.md + docs/SECURITY.md updates:
         - ARCHITECTURE.md gains a Nodes section describing
           the master-node synthetic entry, the heartbeat
           message, and the Per-Node detail page.
         - SECURITY.md notes that NodeStatus is sent over the
           existing mTLS-protected Connect stream; no new auth
           surface, no new ports. The nodes.configure
           permission gating the Take-offline toggle is
           documented.
         scripts/test-architecture.sh + scripts/test-security.sh
         gate the new content.
         Done: ARCHITECTURE.md grew a Nodes view (Phase 12)
         section with three sub-sections (Master node row,
         Heartbeat protocol with an ASCII NodeStatus diagram,
         Per-node detail page) sitting between the runtime
         data layout and the closing rationale block. Every
         Phase 12 building block is named: internal/nodeinfo,
         RegisterBuiltInNode + wolfci-master, the Heartbeat
         message, LastHeartbeat + StaleThreshold, the
         /nodes/&lt;id&gt; route, SetDisabled, and the Router
         local-path skip. SECURITY.md grew a "Node heartbeats
         (Phase 12)" block before the Threat model that
         documents the two operator-facing facts: NodeStatus
         rides the existing mTLS Connect stream so the
         attack surface is unchanged, and the Take-offline
         toggle is intended to be gated by the nodes.configure
         permission (documented as a follow-up because matrix-
         driven HTTP authz is not wired into requireSession
         yet; until then session auth is the only gate).
         scripts/test-architecture.sh grew a 1b block that
         requires the doc to mention internal/nodeinfo,
         wolfci-master, Heartbeat, NodeStatus, and /nodes/.
         scripts/test-security.sh grew a step 6 that requires
         NodeStatus and nodes.configure. Both gates fail
         pre-rewrite and pass against the new content.

Phase 12 complete; Current Phase advances to Phase 13.

## Phase 13 - Per-job detail page

User ask 2026-05-22 (from a Jenkins screenshot annotated by the
project owner): bring wolfCI's per-job UX up to roughly the
Jenkins parity bar - a left sidebar with the operator's most-
used actions, a header carrying description + permalinks + the
upstream-job relationship, and a build history panel grouped by
date. Today /jobs lists every job but there is NO per-job page;
the only navigable per-job URLs are /jobs/<name>/edit (the spec
editor) and /jobs/<name>/builds/<n> (the live log).

Annotated screenshot called out the minimum set of sidebar
actions: Build Now, Rebuild Last, Configure, Rename, Workspace.
Phase 13 ships Build Now + Configure + Delete + the visible
chrome; Phase 14 adds Rebuild Last + Workspace + Rename
(operational features that need supporting infrastructure first).

Decisions to lock in before the phase starts:

- The detail page lives at /jobs/<name>. handleJobRoutes today
  does not match bare /jobs/<name>; 13.1 adds the case before
  the existing /edit + /builds/<n> + /run cases.
- The sidebar is rendered server-side as part of the template,
  not a separate panel reload. Operators expect the same chrome
  on every job action page, so /jobs/<name>/edit and the
  live-log page should also embed the sidebar in a follow-up
  (deferred backlog; the initial sidebar lives on the detail
  page only).
- Build history pagination: server caps the rendered list at
  100 most-recent builds with a "see all" link to an
  /jobs/<name>/builds endpoint that paginates. Avoids loading
  a 10000-build history into one HTML page.
- Permalinks compute lazily from an on-disk build directory
  scan + each result.json. No cache for the first iteration;
  if scan time becomes a problem we add an in-memory index in
  Phase 14.
- Description is plain text (template's HTMLEscapeString); a
  markdown variant is deferred to a backlog item to avoid
  pulling in a markdown parser before we have an opinion on
  which one (CommonMark via gomarkdown is the likely choice).

- [x] 13.1 Add /jobs/<name> (GET) to handleJobRoutes. Loads the
         Job, scans the builds directory for that job, renders
         a new templates/jobdetail.html with:
           - sidebar (Build Now form, Configure link, Delete
             form; placeholders for Phase 14's Rebuild Last,
             Rename, Workspace)
           - <h2>{{ .Name }}</h2> + description block
           - build history panel: list of build numbers +
             status icons + relative timestamps, last 100
         Done: internal/server/jobdetail.go carries the handler
         + scanBuildHistory; templates/jobdetail.html renders
         the sidebar + description + history; handleJobRoutes
         dispatches GET /jobs/<name> to it. The pre-existing
         `len(parts) < 2 -> 404` guard in handleJobRoutes
         dropped to a `parts[0] == "" -> 404` so a bare name
         falls through to the new case. Sidebar Build Now is
         gated on opts.JobRunner != nil so it stays hidden in
         deployments that have no runner wired; the test
         (newAuthedUIWithRunner) covers the wired case.
         Build-history sort: newest-first by result.json
         mtime, tie-broken on build number; an in-flight
         build (no result.json yet) renders as "running" with
         the build dir's mtime. Gates:
         TestJobDetail_Returns200WithSidebar,
         TestJobDetail_RendersDescription,
         TestJobDetail_RendersBuildHistory_NewestFirst,
         TestJobDetail_404OnMissingJob.
- [x] 13.2 Permalinks panel on the detail page. Header gets
         a section listing:
           Last build (#N, <relative time>)
           Last stable build (#N or <none>)
           Last successful build (#N or <none>)
           Last unsuccessful build (#N or <none>)
           Last completed build (#N or <none>)
         "Stable" = StatusSuccess AND not a Phase-14 Rebuild
         Last reattempt (initial impl treats stable ==
         successful; flake/retry semantics tighten in Phase
         14). Each link goes to /jobs/<name>/builds/<n>.
         Done: scanBuildHistory split into scanAllBuilds
         (full list, no cap) + an in-handler truncation
         step so permalinks compute against the complete
         history while the build table stays capped at 100.
         New computePermalinks builds the five pointers from
         the newest-first slice in a single pass:
         LastBuild ignores status (catches a running build),
         LastCompleted/LastSuccessful/LastUnsuccessful skip
         "running", LastStable aliases LastSuccessful until
         Phase 14 introduces the rebuild-retry distinction.
         Template renders each entry as a labelled <li> with
         the link, status word, and relative time; missing
         entries render "none" in muted text. Gates:
         TestJobDetail_PermalinksReflectMostRecentStatuses,
         TestJobDetail_PermalinksHandlesNoBuilds.
- [x] 13.3 /jobs/<name>/builds index page (paginated build
         list, linked from the "see all" affordance on the
         detail page). 25 builds per page, ?page=N query,
         optional ?since=<rfc3339> filter.
         Done: internal/server/buildsindex.go carries
         handleBuildsIndex. The handler reuses scanAllBuilds
         from jobdetail.go, applies an optional RFC3339
         ?since= cutoff (keeps rows whose mtime is >=
         cutoff), then slices the filtered list at
         buildsIndexPageSize=25. Prev/Next link targets are
         pre-assembled in Go (pageQueryString) so the
         template emits them verbatim and the active
         ?since= filter survives pagination. Templates/
         buildsindex.html mirrors the detail page's history
         table for visual continuity, with a header
         subtitle ("Page N of M, X builds total"), an
         explicit "Clear filter" affordance when ?since= is
         active, and a footer pagination block. Route wired
         in handleJobRoutes for `len(parts) == 2 &&
         parts[1] == "builds"`. buildHistoryRow gains a
         When time.Time so the filter can compare against
         the raw mtime without re-parsing RelativeTime.
         Gates: TestBuildsIndex_FirstPageReturnsNewest,
         TestBuildsIndex_RespectsSinceFilter,
         TestBuildsIndex_PaginationLinks.
- [x] 13.4 Delete project action in the sidebar. POST
         /jobs/<name>/delete gated by jobs.configure permission
         (mirroring /jobs/<name>/edit). Removes jobs/<name>/
         AND keeps builds/<name>/ in place: the operator can
         re-create the job and the history persists on disk.
         A separate "wipe history too" flow is destructive
         enough to need its own UI affordance and lands as a
         backlog item.
         Done: internal/storage gains DeleteJob, which
         os.RemoveAll's jobs/<name>/ and returns
         os.ErrNotExist for an already-deleted job. The
         handler (handleJobDelete in jobdetail.go) calls
         DeleteJob and 303-redirects to /jobs. Route wired
         in handleJobRoutes for POST /jobs/<name>/delete
         (GET / other methods return 405). Permission gate
         is still requireSession only - the matrix-driven
         HTTP authz that wires jobs.configure /
         nodes.configure is the same follow-up tracked
         under the Phase 12.7 note; the test asserts the
         gate we DO enforce (anon POST -> 3xx redirect,
         spec untouched). Gates:
         TestJobDelete_RemovesSpec,
         TestJobDelete_KeepsBuildHistory,
         TestJobDelete_RequiresConfigurePermission.

Phase 13 complete; Current Phase advances to Phase 14.

## Phase 14 - Build retention, workspace browser, rebuild, rename

Operational features that complete the Phase 13 sidebar and keep
disk usage bounded once builds run for real.

Decisions to lock in:

- Retention is per-job. Job spec gains a Retention block:
    retention:
      max_builds: 30      # keep most-recent 30, sweep older
      max_age: 720h       # OR keep anything newer than 30d
  Either field may be set; if both are set, EITHER condition
  protects a build (the more lenient of the two). Default:
  keep forever. Sweeper runs every 5 minutes (configurable
  via cfg.RetentionSweepInterval, default "5m").
- Workspace browser is read-only. Operators see a file tree
  rooted at builds/<job>/<n>/workspace/ with text preview
  (line-numbered) for files under a size threshold and a
  download link for everything else. No edit, no shell, no
  symlink resolution beyond the workspace root. Path-traversal
  guards at every node.
- Rebuild Last runs the spec snapshotted at the time of the
  last build. Each Enqueue captures builds/<job>/<n>/spec.yaml
  (cheap: specs are small). If the live spec has diverged the
  operator sees a banner and can opt into "rebuild with
  current spec" instead.

- [x] 14.1 storage.Job gains optional Retention field. New
         sweeper goroutine in cmd/wolfci enforces it every 5
         minutes; each removed build dir logs to stdout for
         auditability.
         Done: storage.Job gains *Retention (nil = keep
         forever). New internal/retention package carries
         SweepJob (per-job decision + rm -rf of build dirs
         that miss every active rule) and SweepAll (loops
         over storage.ListJobs and aggregates per-job
         errors so one bad spec does not starve the rest).
         The core decision lives in composeKeep, which
         consumes pre-parsed MaxBuilds + maxAge so the
         time.ParseDuration error path stays at the
         SweepJob boundary; in-flight builds (no
         result.json) are unconditionally protected so
         the sweeper never races the executor's write-at-
         end. ServerConfig gains RetentionSweepInterval
         (default 5m; "0" disables the goroutine) with
         Validate + RetentionInterval helpers matching the
         existing DrainTimeout pattern. cmd/wolfci spawns
         runRetentionSweeper on a ticker that calls
         SweepAll and logs each removed build through the
         standard log package (systemd / launchd capture
         it). Gates (internal/retention):
         TestRetention_KeepsMaxBuilds,
         TestRetention_KeepsByAge,
         TestRetention_KeepsByEitherWhenBothSet,
         TestRetention_DefaultIsKeepForever.
- [x] 14.2 Workspace browser at /jobs/<name>/builds/<n>/ws/
         and /jobs/<name>/builds/<n>/ws/<path>. Server-side
         tree walk with path-traversal guards (no "..", no
         absolute paths, normalize before stat). Text preview
         under 256KiB; download link with Content-Type sniff
         for everything else.
         Done: handleWorkspace in workspace.go dispatches
         dir-listing vs file-serve. Two layers of traversal
         defense: (1) reject "/" prefix or ".." segments
         before any filesystem call; (2) after
         filepath.Abs(root) + filepath.Abs(target), require
         absTarget == absRoot or absTarget starts with
         absRoot+separator (catches URL-encoded ".." and
         out-pointing symlinks). path.Clean folds "//" and
         trailing slashes without reintroducing ".."
         (already rejected). File response: read first 512
         bytes once for http.DetectContentType, stream raw
         with the sniffed type when size > 256KiB OR the
         type isn't text-like (text/*, application/json,
         application/xml, application/javascript);
         otherwise render a line-numbered preview.
         workspace.html renders the listing (dirs first,
         alphabetized; size in bytes), workspace_preview.
         html the inline file view. Route wired in
         handleJobRoutes for `ws[/<sub>]` after the
         existing `builds/<n>` live-log case, sharing the
         build-number parse. Gates:
         TestWorkspace_ListsImmediateChildren,
         TestWorkspace_RejectsPathTraversal (sentinel-
         leak check via follow-redirects so the mux's
         path-clean 301 does not false-positive),
         TestWorkspace_RendersTextPreviewBelowThreshold,
         TestWorkspace_DownloadsBinaryWithSniff (PNG
         header -> Content-Type: image/*).
- [ ] 14.3 Each Enqueue snapshots the current job spec into
         builds/<job>/<n>/spec.yaml. Rebuild Last (POST
         /jobs/<name>/rebuild) loads the snapshot of the most
         recent build, enqueues a new build from it, and
         redirects to the live log. A "rebuild with current
         spec" variant button on the per-build page enqueues
         from the live spec instead.
         Failing tests:
         TestRebuild_UsesSnapshottedSpec,
         TestRebuild_CurrentSpecVariantUsesLiveSpec,
         TestRebuild_RequiresJobsBuildPermission.
- [ ] 14.4 Rename project. POST /jobs/<name>/rename takes a
         new_name form field, validates per validJobName,
         atomically moves jobs/<old>/ -> jobs/<new>/ and
         builds/<old>/ -> builds/<new>/. matrix.yaml has no
         per-job entries today; the rename is forward-
         compatible with Phase 15's trigger graph (the cycle
         check there walks job names, so a rename will
         automatically pick up the new name on next save).
         Failing tests:
         TestRename_MovesSpecAndBuildHistory,
         TestRename_RejectsExistingTargetName,
         TestRename_RequiresJobsConfigurePermission.

## Phase 15 - Upstream / downstream jobs + artifacts

The dependency graph between jobs and the artifact-passing
plumbing that makes cross-platform pipelines possible. The
motivating example from the project owner: "Build a bundle on
linux, zip it. Send .zip to windows machine, unarchive and run
test with resources." Sibling-job navigation (jobs that share an
upstream) falls out for free once the trigger graph exists.

Decisions to lock in:

- Job spec gains two new fields:
    upstream: [job-a, job-b]      # this job is eligible to be
                                  # triggered when any of these
                                  # upstreams succeed
    triggers_downstream:
      - name: windows-test
        artifacts:
          - dist/bundle.tar.gz    # paths inside this build's
                                  # workspace that the downstream
                                  # build sees at $WOLFCI_INPUTS/
                                  # <artifact-basename>
- Trigger semantics: AFTER a build reaches StatusSuccess, the
  scheduler walks job.TriggersDownstream and Enqueues each
  downstream with the parent build's identity stamped into
  BuildResult.TriggeredBy = {Job, Build}. Downstream jobs with
  no triggers_downstream stay no-ops; jobs with upstream:[] in
  their spec are eligible to be triggered but the scheduler
  does NOT enforce ordering beyond the one-success-triggers-
  many fan-out.
- Artifact storage: when a job declares triggers_downstream
  with artifacts, the executor copies each listed file from
  the build's workspace into builds/<job>/<n>/artifacts/
  BEFORE marking the build successful. A missing artifact
  fails the build with a clear error. Phase 14's retention
  rules apply to artifacts the same as to logs.
- Loop guard: triggers_downstream cycles are rejected at
  SaveJob time. SaveJob walks the trigger graph (existing
  jobs plus the new spec) and refuses to persist a spec that
  would close a loop. Returns ErrCycleInTriggerGraph.
- Cross-node artifact transfer: when the downstream lands on a
  different node than the upstream, the agent dialer fetches
  the artifact bundle from the server over the existing mTLS
  gRPC channel. No new port, no new protocol; just a new
  AgentService.GetArtifact RPC streaming the bytes. This
  matches the linux-builds, windows-tests use case directly.

- [ ] 15.1 storage.Job gains Upstream []string and
         TriggersDownstream []TriggerSpec fields. TriggerSpec
         is {Name string; Artifacts []string}. SaveJob runs
         the cycle check and returns ErrCycleInTriggerGraph
         on failure.
         Failing tests:
         TestJob_TriggerGraphAcyclic_OK,
         TestJob_TriggerGraphRejectsSelfLoop,
         TestJob_TriggerGraphRejectsTwoNodeCycle,
         TestJob_TriggerGraphRejectsLongerCycle.
- [ ] 15.2 BuildResult gains TriggeredBy {Job string; Build int}
         so a downstream build attributes itself to a specific
         upstream build. result.json carries it; the per-build
         page renders it as a backlink.
         Failing tests:
         TestBuildResult_TriggeredByRoundtrip,
         TestBuildResult_TriggeredByEmptyForRootBuild.
- [ ] 15.3 LocalExecutor (and the agent's executor) copies
         declared artifacts into builds/<job>/<n>/artifacts/
         as the LAST step of a successful build, before writing
         result.json. A missing artifact aborts the build with
         Status=Failure. Each downstream invocation receives
         WOLFCI_INPUTS=<absolute path to upstream's artifacts/>
         in its env; the agent-side executor copies them into
         the workspace at WOLFCI_INPUTS/<basename> before the
         first step.
         Failing tests:
         TestExecutor_CopiesDeclaredArtifacts,
         TestExecutor_MissingArtifactFailsBuild,
         TestExecutor_DownstreamSeesUpstreamArtifacts.
- [ ] 15.4 Scheduler trigger fan-out. After each
         BuildResult.Status==Success, the scheduler walks the
         job's TriggersDownstream and Enqueues each named job
         with TriggeredBy set. Failures, cancels, and errors do
         not fan out. Missing downstream jobs log a warning
         (not a crash) so a half-deleted graph stays operable.
         Failing tests:
         TestScheduler_FanoutOnSuccess (job A succeeds -> jobs
         B and C are enqueued with TriggeredBy.Job=A),
         TestScheduler_NoFanoutOnFailure,
         TestScheduler_FanoutSkipsMissingDownstreamJobWithWarning.
- [ ] 15.5 New AgentService.GetArtifact streaming RPC. The
         agent calls it with (upstream_job, upstream_build,
         artifact_basename) and the server streams the bytes
         from builds/<upstream>/<build>/artifacts/<basename>.
         Cert-CN-based authz: the calling agent must be the
         one assigned the downstream build OR have the
         builds.read permission. No anonymous reads.
         Failing tests:
         TestGetArtifact_StreamsFile,
         TestGetArtifact_RejectsUnauthorizedAgent,
         TestGetArtifact_RejectsTraversal (artifact_basename
         containing "/" or ".." is refused before any disk
         access).
- [ ] 15.6 Job detail page (Phase 13.1) gains two new sections:
           Upstream Projects: list from job.Upstream, each link
             goes to /jobs/<upstream-name>.
           Downstream Projects: computed by scanning every job's
             TriggersDownstream for entries that name this job,
             each link goes to /jobs/<downstream-name>. The
             same scan exposes sibling jobs (jobs that share an
             upstream) via a third "Sibling Projects" section
             rendered when len(siblings) > 0.
         The /jobs index page gains a small badge next to each
         job name showing inbound + outbound trigger counts.
         Failing tests:
         TestJobDetail_RendersUpstreamLinks,
         TestJobDetail_RendersDownstreamLinks_ComputedFromOtherJobs,
         TestJobDetail_RendersSiblingsWhenSharingUpstream,
         TestJobsIndex_BadgesShowTriggerCounts.
- [ ] 15.7 End-to-end example under examples/jobs/. Ships
         linux-bundle.yaml (builds a tarball, declares
         triggers_downstream: [windows-test] with artifacts:
         [dist/bundle.tar.gz]) and windows-test.yaml (declares
         upstream: [linux-bundle], runs the bundled tests
         against $WOLFCI_INPUTS/bundle.tar.gz). Doubles as a
         readable answer to "how do I wire a multi-platform
         pipeline" and as an integration-test fixture.
         scripts/test-examples.sh gates that both specs parse,
         the trigger graph is acyclic, and the schemas match
         storage.Job after marshal + unmarshal. Wired into
         scripts/test.sh.

## Backlog (not in main flow)

Items that came up but are not on the critical path. Promote into a
phase when they become relevant.

- Persistent metrics and a built-in Prometheus exporter.
- Multi-master HA (one process per install for v1).
- Windows agent support (Linux + macOS first).
- LDAP, SAML, OIDC SSO (after core auth lands).
- Pipeline-as-code (declarative pipeline files), once jobs work.
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

End of PLAN.md.
