# wolfCI

A small, self-contained Jenkins replacement built around wolfSSL.

- One static Go binary per role (server, agent, CLI).
- All TLS and crypto via wolfSSL (vendored, pinned to v5.9.1-stable).
- mTLS gRPC between server, agents, and the admin CLI.
- SSH-key authentication for humans; optional username + password
  (bcrypt, configurable cost, default disabled).
- Jenkins-style role-based authorization matrix
  (`admin`, `developer`, `viewer`).
- On-prem agents (long-running) plus ephemeral GCE overflow nodes
  (on-prem matched first; cloud only when no on-prem agent fits).
- Build matrix (e.g. OS x compiler) fans out to N parallel runs.
- Embedded web UI with live build log tailing via Server-Sent
  Events.
- Out-of-process gRPC plugins (HashiCorp go-plugin); ships an
  `email-on-failure` reference plugin.
- Self-contained: every config, job, build, and plugin lives under
  one directory tree. No external database.

## Status

Phases 0 through 9 are complete. See PLAN.md for the per-task log
and `## Current Phase` for what is open next. See
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the package map
and a build-flow walk-through.

## Binaries

| Binary          | Role                                            |
|-----------------|-------------------------------------------------|
| `wolfci`        | Server: scheduler, web UI, agent + CLI gRPC.    |
| `wolfci-agent`  | Executor that runs on each worker node.         |
| `wolfci-ctl`    | Admin CLI: `login`, `job list`, `job run`,      |
|                 | `node list`, `build log`.                       |

All three are produced by `scripts/build.sh` into
`build/bin/<goos>-<goarch>/`.

## Quick build

Prereqs: Go 1.18+, a C toolchain with autoconf/automake/libtool,
and git. See [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md)
for the full walk-through.

```sh
git clone https://github.com/kaleb-himes/wolfCI.git
cd wolfCI
git submodule update --init --recursive
scripts/build-wolfssl.sh    # one-time, ~3 min
scripts/build.sh            # ~30 sec
```

Stamp a release tag into the version output by setting
`WOLFCI_BUILD_VERSION` before `scripts/build.sh`.

## Docs

- [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md) - 10-minute
  path from `git clone` to first green build, including the
  systemd unit and launchd plist install recipes.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) - component map,
  deployment + internals ASCII diagrams, end-to-end build trace.
- [docs/SECURITY.md](docs/SECURITY.md) - TLS profile, auth paths,
  role matrix, threat model.
- [docs/PLUGINS.md](docs/PLUGINS.md) - plugin protocol, install
  layout, authoring guide.
- [docs/CREDITS.md](docs/CREDITS.md) - contributors.

## Layout

```
cmd/           server, agent, and CLI main packages
internal/      private Go packages (tlsutil, storage, auth, authz,
               scheduler, agentsvc, agent, nodes, plugin, server,
               cliservice)
web/           embedded HTML, CSS, JS
plugins/       plugin SDK + built-in plugins (email-on-failure)
jobs/          job definitions (runtime data)
builds/        build outputs (runtime data)
config-files/  server config (auth, sessions, node drivers)
nodes/         per-node state
scripts/       build, install, test helpers
  install/     systemd unit + launchd plist
third_party/   vendored deps (wolfSSL submodule)
docs/          design notes, security model, runbooks
tests/         integration tests
```

Runtime data dirs (`jobs/`, `builds/`, `config-files/`, `nodes/`,
`plugins/installed/`) live under the server's working directory at
runtime, NOT inside the source tree. The git repo just contains
their `.gitkeep` placeholders.

## License

GPL-3.0. See LICENSE.

## Author

Kaleb Himes, wolfSSL Inc. See [docs/CREDITS.md](docs/CREDITS.md)
for full contributor list.
