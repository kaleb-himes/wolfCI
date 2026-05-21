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

Phase 1 - wolfSSL TLS layer

(Update this line when a phase completes. Phase 0 was completed in
the initial planning turn.)

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
- [ ] 1.2 Write scripts/build-wolfssl.sh to compile wolfSSL with
        options suitable for a CI server (TLS 1.2 and 1.3, modern
        cipher suites, no SSLv3 or insecure protocols, FIPS off by
        default; FIPS profile toggleable later).
- [ ] 1.3 Create internal/tlsutil with a CGO wrapper that exposes a
        Go net.Listener backed by wolfSSL. Failing test
        (internal/tlsutil/tlsutil_test.go): a TLS 1.3 handshake to
        the listener using crypto/tls as a client succeeds against a
        self-signed cert.
- [ ] 1.4 Implement internal/tlsutil until the test passes.
- [ ] 1.5 Write a hello-world server cmd/wolfci that opens an HTTPS
        listener via tlsutil and returns 200 OK on /. Test: a
        net/http client over crypto/tls receives the expected body.

## Phase 2 - Storage layer

Persist jobs, builds, users, and config as plain files.

- [ ] 2.1 Failing test (internal/storage/storage_test.go): the
        storage layer round-trips a Job spec to and from disk
        without loss.
- [ ] 2.2 Define the on-disk format:
        - jobs/<name>/job.yaml
        - builds/<job>/<n>/result.json
        - builds/<job>/<n>/log
- [ ] 2.3 Implement internal/storage with file locking for
        concurrent writes (flock via syscall).

## Phase 3 - AuthN and AuthZ

The security model.

- [ ] 3.1 Failing test (internal/auth/sshkey_test.go): public-key
        login accepts a known authorized key and rejects an unknown
        one.
- [ ] 3.2 Implement SSH public-key auth. Keys live under
        config-files/auth/keys/<user>.pub.
- [ ] 3.3 Failing test (internal/auth/password_test.go): username +
        password login succeeds when enabled and is rejected when
        disabled in config-files/auth/config.yaml.
- [ ] 3.4 Implement password auth with bcrypt. Default to disabled.
- [ ] 3.5 Failing test (internal/authz/matrix_test.go): the matrix
        denies an action the user lacks permission for and permits
        actions the user holds.
- [ ] 3.6 Implement a Jenkins-style role-based matrix in
        internal/authz. Roles defined in
        config-files/auth/matrix.yaml.
- [ ] 3.7 docs/SECURITY.md: write the full security model, including
        threat model and how to enable/disable each auth path.

## Phase 4 - Job model and scheduler

Run shell-script jobs against executors.

- [ ] 4.1 Failing test (internal/scheduler/scheduler_test.go): the
        scheduler enqueues a Job, dispatches it to a fake executor,
        and records the result.
- [ ] 4.2 Implement internal/scheduler with a priority queue and
        per-node executor pool.
- [ ] 4.3 Build runner: spawns a shell, streams stdout and stderr to
        builds/<job>/<n>/log, records exit code in result.json.
- [ ] 4.4 Build matrix support: a job can declare an axis (for
        example, OS x compiler), and the scheduler fans out into N
        runs.

## Phase 5 - Node management

On-prem and GCE.

- [ ] 5.1 cmd/wolfci-agent: a standalone binary that opens an mTLS
        connection back to the server and offers itself as an
        executor. Agent config
        (config-files/agent.yaml) declares its executor count
        (concurrent jobs allowed).
- [ ] 5.2 Failing test (tests/agent_e2e_test.go): an agent
        registers, receives a job, executes it, and returns the
        result.
- [ ] 5.3 Implement the agent protocol over gRPC + wolfSSL mTLS.
- [ ] 5.4 GCE provisioner (internal/nodes/gce): uses the Google
        Cloud Go SDK to launch an instance with a startup script
        that runs wolfci-agent and joins the server.
- [ ] 5.5 Failing test (internal/nodes/gce/gce_test.go): with a
        faked GCE backend, the scheduler requests a node, the node
        "joins", runs a job, terminates.
- [ ] 5.6 Implement the GCE driver against the real API behind an
        interface the fake satisfies.

## Phase 6 - Web UI

Embedded HTML UI served from the single binary.

- [ ] 6.1 Failing test (internal/server/ui_test.go): GET / returns
        the login page; authenticated GET /jobs returns the job
        list.
- [ ] 6.2 Implement minimal UI with Go html/template, embedded via
        //go:embed.
- [ ] 6.3 Job create/edit form posts to internal/storage.
- [ ] 6.4 Build log live stream via Server-Sent Events.
- [ ] 6.5 Node management page lists registered on-prem and GCE
        nodes.

## Phase 7 - Plugins

Out-of-process gRPC plugins.

- [ ] 7.1 Design the plugin protocol (gRPC; plugins announce the
        hooks they implement). Document in docs/PLUGINS.md.
- [ ] 7.2 Failing test (internal/plugin/plugin_test.go): a sample
        "hello" plugin under plugins/examples/hello is loaded and
        its on-build-start hook fires.
- [ ] 7.3 Implement the plugin host in internal/plugin (subprocess
        management, gRPC client, cleanup on shutdown).
- [ ] 7.4 Ship a real plugin: email-on-failure.

## Phase 8 - CLI client

- [ ] 8.1 cmd/wolfci-ctl with subcommands: login, job list, job
        run, build log, node list. Failing test
        (tests/ctl_test.go) exercises each.

## Phase 9 - Packaging, docs, polish

- [ ] 9.1 scripts/build.sh produces a single static binary for
        linux/amd64 plus a darwin/arm64 development binary.
- [ ] 9.2 scripts/install/systemd/wolfci.service template; a
        macOS launchd plist for development.
- [ ] 9.3 docs/GETTING-STARTED.md: a 10-minute path from "git
        clone" to "first green build".
- [ ] 9.4 docs/ARCHITECTURE.md with an ASCII component diagram.
- [ ] 9.5 README.md final pass.

## Backlog (not in main flow)

Items that came up but are not on the critical path. Promote into a
phase when they become relevant.

- Persistent metrics and a built-in Prometheus exporter.
- Multi-master HA (one process per install for v1).
- Windows agent support (Linux + macOS first).
- LDAP, SAML, OIDC SSO (after core auth lands).
- Pipeline-as-code (declarative pipeline files), once jobs work.

End of PLAN.md.
