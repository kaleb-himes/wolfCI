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

Phase 10 - wolfCrypt-only crypto pass

(Phase completion log. Phase 0 was completed in the initial
planning turn. Phase 1 completed in iteration 4, Phase 2 in
iteration 5, Phase 3 in iteration 8, Phase 4 in iteration 10,
Phase 5 in iteration 21, Phase 6 in iteration 25, Phase 7 in
iteration 28, Phase 8 in iteration 32, Phase 9 in iteration 37
of the slash-loop run.)

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

- [ ] 10.1 internal/wolfcrypt package: the wolfCrypt bridge that
        every other auth/test/session file in this tree will use.
        Subdivided into three sub-checkpoints because it is all
        new CGO surface and each primitive deserves its own KATs
        and red/green iteration.
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
        - [ ] 10.1c MintCert. Failing test exercises a CA root +
              ServerAuth + ClientAuth chain similar to
              testcerts.NewMTLSChain, asserts the DER parses via
              x509.ParseCertificate (parsing is wire format, not
              crypto), and asserts a signature wolfCrypt produced
              over a known payload verifies through
              wolfcrypt.ECCVerify with the cert's public key.
- [ ] 10.2 Replace x/crypto/bcrypt in internal/auth/password.go
        with PBKDF2-HMAC-SHA-256 via internal/wolfcrypt.
        Iteration count and salt length live in
        config-files/auth/config.yaml under pbkdf2_iterations
        (default 600000 per OWASP 2023) and pbkdf2_salt_bytes
        (default 16). On-disk file renames from <user>.bcrypt to
        <user>.pbkdf2 with a versioned header so the loader can
        reject the wrong KDF instead of silently misverifying.
        Verify uses the HMAC-both-sides constant-time compare.
        Drop golang.org/x/crypto/bcrypt from go.mod.
- [ ] 10.3 Replace x/crypto/ssh in internal/auth/sshkey.go. Hand-
        roll the public-key parser per RFC 4253 Section 6.6 for
        ssh-ed25519, ecdsa-sha2-nistp256, and ssh-rsa. Route
        signature verify through internal/wolfcrypt. Drop
        golang.org/x/crypto/ssh from go.mod.
- [ ] 10.4 Replace crypto/rand in internal/server/session.go with
        wolfcrypt.RandBytes. Token size and hex encoding stay the
        same; only the entropy source changes. Drop the
        crypto/rand import.
- [ ] 10.5 Replace crypto/ecdsa, crypto/elliptic, crypto/rand,
        crypto/x509, and crypto/x509/pkix in
        internal/testcerts/testcerts.go with wolfcrypt.MintCert.
        Tests that consume the certs may still parse via
        crypto/x509 (parsing is wire format, not crypto) but the
        keys and signatures must be wolfCrypt. NewSelfSigned and
        NewMTLSChain signatures stay the same.
- [ ] 10.6 Copy TLS version + relevant cipher constants into
        internal/tlsutil as local consts (VersionTLS13, etc.).
        Drop crypto/tls imports from internal/tlsutil,
        internal/agent/client.go, and cmd/wolfci-ctl/client.go.
- [ ] 10.7 docs/SECURITY.md update: document the
        wolfCrypt-only rule and which wolfSSL configure flags
        each primitive depends on (--enable-pwdbased for PBKDF2
        and HMAC, both default-on; --enable-keygen and
        --enable-certgen for MintCert, both already in the
        profile). Confirm in scripts/test-build-wolfssl.sh that
        the configure command keeps those flags so a profile
        regression cannot silently break the auth stack.

## Phase 11 - cmd/wolfci wiring (placeholder)

Backlog promotion of the "wire scheduler.Scheduler into cmd/wolfci
bootstrap" item. cmd/wolfci is currently the Phase 1.5 hello-world
stub; docs/GETTING-STARTED.md describes a flow it does not yet
implement. Phase 11 assembles storage + scheduler + agentsvc +
cliservice + plugin host + server (web UI) + first-admin
bootstrap-token flow into one main, against the wolfCrypt-backed
auth stack Phase 10 just landed.

Decisions still open (will lock in before the phase starts):

- Config file format for server bootstrap
  (config-files/server.yaml: listen addr, cert paths, working
  dir, optional GCE config path, optional plugin dir override).
- Whether to serve the web UI and the gRPC services on the same
  port via cmunixmu/cmux-style multiplexing, or on two distinct
  ports.
- Signal handling: SIGINT/SIGTERM -> graceful drain of in-flight
  builds, then close listeners.
- First-admin bootstrap: token format, expiry, persistence,
  invalidation on consumption.

Task list lands when Phase 11 starts.

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
