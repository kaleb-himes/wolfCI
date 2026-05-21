# wolfCI Architecture

This doc is the map. It names every component, the wire between them,
and the on-disk data each one owns. Use it as the first thing to read
after CLAUDE.md and PLAN.md.

## Goals (recap)

1. All CI capabilities wolfSSL needs (jobs, agents, matrix, security
   model, plugins).
2. One process, one static binary, one directory tree.
3. Simple enough that one engineer can hold the whole thing in their
   head.
4. Extensible via out-of-process plugins.
5. Latest stable wolfSSL for every byte of TLS and crypto.

Every architectural choice below is in service of those five.

## Deployment

```
                                       wolfSSL mTLS gRPC
   +--------------------+   <----------------------------+
   |   wolfci server    |                                |
   |   (cmd/wolfci)     |                                |
   |                    |     +----------------------+   |
   |                    |     |  wolfci-agent        |---+
   |   HTTPS (web UI)   |     |  on-prem node        |
   |     <----------+   |     |  (cmd/wolfci-agent)  |
   |                |   |     +----------------------+
   |                |   |                                |
   |   wolfSSL mTLS |   |     +----------------------+   |
   |   gRPC (CLI)   |   |     |  wolfci-agent        |---+
   |     <--------+ |   |     |  GCE overflow node   |
   |              | |   |     |  (cmd/wolfci-agent)  |
   +--------------+-+---+     +----------------------+
                  | |
   +--------------+-+---+
   |    operator        |
   |  (web UI + ctl)    |
   |                    |
   |  wolfci-ctl on    |
   |  laptop or jump    |
   |  host              |
   +--------------------+
```

- **wolfci** is the only durable process. One binary, one directory
  tree under whatever `WorkingDirectory` the systemd unit or launchd
  plist names (`/var/lib/wolfci` on Linux, `/usr/local/var/wolfci` on
  macOS dev boxes).
- **wolfci-agent** is the executor. Stateless beyond its own
  `config-files/agent.yaml`. Reconnects to the server on every start.
  On-prem agents run as long-lived services; GCE overflow agents are
  provisioned per build and torn down via `Provisioner.Terminate`.
- **wolfci-ctl** is the admin CLI. Speaks the same wolfSSL mTLS gRPC
  transport the agent does, but to a different service
  (`CLIService`). The server takes the client cert CN as the
  username and gates every call against `config-files/auth/matrix.yaml`.

Every wire in the diagram is wolfSSL TLS 1.3, mTLS for the gRPC
links, server-auth-only for the browser HTTPS. wolfSSL is vendored
as a git submodule at `third_party/wolfssl/` pinned to v5.9.1-stable
and compiled by `scripts/build-wolfssl.sh` into a static lib that
links into every binary via CGO.

## Server internals

```
+------------------------------- cmd/wolfci -------------------------------+
|                                                                          |
|  +-------------------+   +-----------------+   +---------------------+   |
|  | internal/auth     |   | internal/authz  |   | internal/server     |   |
|  |  sshkey.go        |-->|  matrix.go      |   |  HTTP UI            |   |
|  |  password.go      |   |  3 roles        |   |  templates + SSE    |   |
|  +-------------------+   +-----------------+   +----------+----------+   |
|         ^                       ^                         |              |
|         |                       |                         |              |
|         +-----------+ +---------+                         v              |
|                     | |                          +-----------------+     |
|             +-------+-+----------+               | internal/       |     |
|             | internal/scheduler |               | cliservice      |     |
|             |  FIFO queue        |<--------------+  ListJobs       |     |
|             |  Router (label)    |               |  ListNodes      |     |
|             |  LocalExecutor     |               |  StreamBuildLog |     |
|             +---------+----------+               |  RunJob         |     |
|                       |                          +--------+--------+     |
|                       v                                   ^              |
|             +---------+----------+   +-------------+      |              |
|             | internal/agentsvc  |-->| internal/   |      |              |
|             |  Connect stream    |   | nodes       |      |              |
|             |  AssignJob         |   |  fake / gce |      |              |
|             |  LogSink           |   |  Provisioner|      |              |
|             +---------+----------+   +-------------+      |              |
|                       ^                                   |              |
|                       |                                   |              |
|             +---------+----------+   +-----------------+  |              |
|             | internal/storage   |   | internal/plugin |  |              |
|             |  jobs/             |   |  go-plugin host |  |              |
|             |  builds/           |   |  plugins/       |--+              |
|             |  config-files/     |   |  installed/     |                 |
|             |  nodes/            |   +-----------------+                 |
|             +--------------------+                                       |
|                                                                          |
|   wolfSSL via internal/tlsutil underneath every transport above:         |
|   mTLS gRPC to agents (internal/agentsvc) and to CLIs (cliservice),      |
|   server-auth-only HTTPS to browsers (internal/server).                  |
+--------------------------------------------------------------------------+
```

### Package responsibilities

- **internal/tlsutil** wraps wolfSSL via CGO. Owns conn lifecycle
  (sync.RWMutex + sync.Once Close), the custom I/O callbacks that
  bridge Go `net.Conn` to wolfSSL via a sync.Map-keyed registry, and
  the listener/dialer factories the server and agent both call.
- **internal/storage** persists everything. Jobs as YAML
  (`jobs/<name>/job.yaml`), build state as JSON
  (`builds/<job>/<n>/result.json`), build logs as plain text
  (`builds/<job>/<n>/log`, `log.live` while streaming). Concurrent
  writes are gated by `syscall.Flock` (LOCK_EX for writes, LOCK_SH
  for reads, plus an LOCK_EX-guarded `.next-build-number` counter
  for per-job monotonic numbering).
- **internal/auth** implements SSH-key login (the preferred path,
  keys under `config-files/auth/keys/<user>.pub`) and bcrypt
  username+password (default off, cost 12, toggle in
  `config-files/auth/config.yaml`).
- **internal/authz** owns the Jenkins-style role matrix. Three
  roles: admin (everything), developer (read jobs, trigger builds,
  read logs), viewer (read-only). Permissions are dotted strings
  (`jobs.read`, `jobs.build`, `nodes.configure`, ...) plus an
  admin wildcard.
- **internal/scheduler** owns the FIFO queue and the Executor
  interface. `LocalExecutor` runs steps on the server host;
  `Router` chooses between local and remote execution based on
  `Job.NodeLabel` (on-prem first; cloud only when no on-prem agent
  matches), and falls back to a `Provisioner` for overflow.
- **internal/agentsvc** is the server-side of the agent gRPC
  service. Routes `Connect` streams by `agent_id` (passed via gRPC
  metadata), holds an in-memory registry of connected agents, owns
  `AssignJob`, `IdleAgentWithLabel`, `ConnectedAgents`, and the
  pluggable `LogSink` that the scheduler's `Router` joins on for
  `SubmitAndWait`.
- **internal/agent** is the agent runtime. Dials the server via
  wolfSSL mTLS, registers, opens `Connect`, and dispatches each
  `JobAssignment` to a `LocalExecutor` under the agent's
  `WorkDir`. Log chunks stream back through the same `Connect`
  stream under a shared mutex with `BuildComplete`.
- **internal/nodes** is the `Provisioner` abstraction
  (`Provision(ctx, label) -> Node`, `Terminate(ctx, node)`). The
  `fake/` subpackage is the test backend. The `gce/` subpackage is
  the real Google Compute Engine driver built against
  `google.golang.org/api/compute/v1`, with the live test gated by
  `WOLFCI_GCE_LIVE_TEST=1`.
- **internal/plugin** is the plugin host. Scans
  `plugins/installed/<name>/<name>` on startup, spawns each via
  `hashicorp/go-plugin`, dispatches `OnBuildComplete` to every
  loaded plugin after each build, and kills every subprocess on
  shutdown.
- **internal/server** is the web UI. Embedded Go `html/template`
  pages over the storage layer, hand-written CSS, vanilla JS for
  the live log tail (Server-Sent Events). Sessions are
  `HttpOnly`/`Secure`/`SameSite=Strict` cookies backed by per-token
  files under `config-files/auth/sessions/`.
- **internal/cliservice** is the gRPC service the CLI talks to.
  `ListJobs`/`ListNodes`/`StreamBuildLog`/`RunJob`. Decoupled from
  `internal/scheduler` via an `Enqueuer` interface so bootstrap
  code in `cmd/wolfci` can plumb the two together without an
  import cycle.

### Binaries

- **cmd/wolfci** is the server.
- **cmd/wolfci-agent** is the agent.
- **cmd/wolfci-ctl** is the CLI.

All three are produced by `scripts/build.sh` into
`build/bin/<goos>-<goarch>/`, statically linked against the vendored
wolfSSL, and stampable with `WOLFCI_BUILD_VERSION` via `-ldflags -X`.

## Data flow: one build, end to end

1. Operator drops `jobs/hello/job.yaml` (via the web UI or directly
   on disk).
2. Operator runs `wolfci-ctl job run hello`. The CLI dials the
   server over wolfSSL mTLS, presents its client cert, and calls
   `CLIService.RunJob`.
3. `cliservice.Server` looks the job up via `internal/storage`,
   hands it to the configured `Enqueuer` (set up at bootstrap to
   point at `scheduler.Scheduler.Enqueue`), and returns the
   assigned build number.
4. The scheduler's `Router` dequeues. If `Job.NodeLabel` is empty
   or matches the server's local labels, it dispatches to the
   in-process `LocalExecutor`. Otherwise it asks
   `agentsvc.IdleAgentWithLabel(label)` for a matching on-prem
   agent. If none is connected, it calls
   `provisioner.Provision(ctx, label)` (the GCE driver in
   production, `fake.Provisioner` in tests) and polls until the
   spawned node registers.
5. The chosen executor runs the steps. The `LocalExecutor` tees
   stdout+stderr through a `LogSink` callback (which on an agent
   sends each chunk as an `AgentMessage.Log` over the `Connect`
   stream; on the server it routes through `FileLogSink` into
   `builds/<job>/<n>/log.live`).
6. The browser tails the same `log.live` via Server-Sent Events
   (`/api/v1/builds/<job>/<n>/log`) until the build completes;
   `wolfci-ctl build log <job> <n>` does the same over gRPC.
7. `BuildComplete` arrives. The server writes `result.json`, the
   plugin host fans out `OnBuildComplete` to every installed
   plugin (the `email-on-failure` plugin is the shipped reference),
   and if the build was a GCE overflow, the scheduler calls
   `provisioner.Terminate` on the spawned node.

## Runtime data layout

Everything is under one tree (CLAUDE.md "self-contained" rule):

```
<WorkingDirectory>/
  jobs/<name>/job.yaml                Job specs
  builds/<job>/<n>/result.json        Build outcome
  builds/<job>/<n>/log                Final log
  builds/<job>/<n>/log.live           Live stream while running
  builds/<job>/.next-build-number     Per-job counter (flock-guarded)
  config-files/
    server.yaml                       Server config
    auth/
      config.yaml                     bcrypt cost, password toggle
      keys/<user>.pub                 SSH public keys
      <user>.bcrypt                   bcrypt password hash, 0600
      matrix.yaml                     role assignments
      sessions/<token>                Active browser sessions
    nodes/gce.yaml                    GCE provisioner config
  plugins/
    installed/<name>/<name>           Plugin binary
    installed/<name>/config.yaml      Optional per-plugin config
  nodes/<id>/                         Reserved for per-node state
```

## Why these choices

- **One binary**: every package lives in the same Go module, links
  the same wolfSSL `.a`, ships in the same artifact. Operators move
  one file plus a templated unit, not a fleet of services.
- **No external database**: the storage layer fits on disk and is
  flock-safe. Removing PostgreSQL/MySQL from the bill of materials
  removes the largest piece of operational surface area Jenkins ever
  had.
- **wolfSSL everywhere**: the whole point. There is no path on or off
  the server that does not go through wolfSSL.
- **gRPC for agents and the CLI**: one wire format, generated
  bindings, server-streaming for logs, easy to add new methods
  without breaking compatibility. Plugins also speak gRPC, but
  scoped to the in-host hashicorp/go-plugin Unix socket - the same
  generated tooling, no new transport.
- **On-prem first, cloud as overflow**: aligns with how wolfSSL
  actually runs CI. The `Provisioner` abstraction means future
  drivers (AWS, Azure, bare metal PXE) plug in without touching the
  scheduler.
- **Plugins out-of-process**: a misbehaving plugin cannot take the
  server down. `hashicorp/go-plugin` handles process lifecycle and
  the version-handshake, so we don't hand-roll either.

End of ARCHITECTURE.md.
