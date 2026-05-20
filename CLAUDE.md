# CLAUDE.md - wolfCI Project Instructions

This file is loaded into every Claude Code session that works in this
repo. It is intentionally compact. The full working plan lives in
PLAN.md and is the source of truth for what to do next.

## Mission

wolfCI is a Jenkins replacement built for wolfSSL Inc. Goals:

1. Provide all CI functionality wolfSSL needs (job runs, agents,
   build matrices, security model, plugins).
2. Be small, fast, and self-contained. One process, one static binary,
   one directory tree.
3. Be simple enough that any single engineer can fully understand it.
4. Be extensible through a plugin system (out-of-process plugins).
5. Use the latest stable wolfSSL release for all TLS and crypto.

Author: Kaleb Himes (kaleb-himes on GitHub). Claude Code (Anthropic) is
contributing to the implementation; see docs/CREDITS.md.

## Hard Rules (do not violate)

Read these every session before acting. They come from the project
owner and override defaults.

1. Operate from PLAN.md on disk. Each /loop iteration picks the next
   unchecked task, completes it, marks it done, commits, and ends.
   Resumability is the point: a fresh session with no chat context
   must be able to pick up by reading CLAUDE.md and PLAN.md alone.
2. Test-Driven Development. For every new feature, write a test that
   FAILS first, then implement until it passes. No exceptions.
3. ASCII only. No emdash, no endash, no smart quotes, no UTF-8 bytes
   outside the printable ASCII range. scripts/check-ascii.sh is the
   gate; run it before every commit.
4. Commits are authored only by kaleb-himes. NEVER add a
   "Co-Authored-By: Claude" trailer. If credit is appropriate it goes
   in docs/CREDITS.md, not the commit metadata.
5. Always merge work to local main and rebuild before declaring a
   task complete. No long-lived feature branches.
6. Keep CLAUDE.md under 40000 bytes. If it grows beyond that, move
   the older 30000 bytes (the 3/4 history) to
   LEGACY-PROJECT-CLAUDE-HISTORY.md and reference it from CLAUDE.md.
7. Self-contained: everything lives under wolfCI/. No system-wide
   config files. Runtime data goes in jobs/, builds/, config-files/,
   plugins/, nodes/.
8. Ask only when a decision cannot be made from existing context.
   Never ask for the sake of asking.
9. Minimize the number of root-level config files. New config goes
   inside config-files/ unless there is a strong tooling reason
   otherwise (go.mod, .gitignore, Makefile, etc.).
10. Prefer editing existing files to creating new ones. Prefer
    deleting code that becomes unused to leaving it commented out.

## Operating Procedure (every session)

1. Read CLAUDE.md (this file) and PLAN.md.
2. In PLAN.md, find the line marked "## Current Phase" and the next
   unchecked task under it.
3. Verify the previous task still passes: `make test` (once a
   Makefile lands) or `scripts/build.sh && scripts/test.sh`.
4. Write the failing test for the next task. Run it; confirm it fails.
5. Implement the smallest change that makes the test pass.
6. Run the full test suite.
7. Run scripts/check-ascii.sh.
8. Update PLAN.md: change `[ ]` to `[x]` for the completed task.
   If the phase is now fully checked, update "## Current Phase" to
   point at the next phase.
9. Stage and commit with a single-author trailer (kaleb-himes only).
   No "Co-Authored-By" lines.
10. Merge to main locally (already there if you have not branched)
    and rebuild.
11. Check CLAUDE.md size:
    `wc -c CLAUDE.md`. If over 40000, rotate per rule 6.

## Architecture Summary

- Language: Go (single static binary).
- TLS and crypto: wolfSSL via CGO. Vendored as a git submodule in
  third_party/wolfssl, built from a pinned latest-stable tag.
- HTTP: Go net/http stack wrapped over wolfSSL TLS sockets.
- Storage: plain files on disk. YAML for config, JSON for state. No
  external database.
- AuthN: SSH public key (preferred). Optional username + password
  (bcrypt). Password auth can be disabled per deployment.
- AuthZ: Jenkins-style role-based authorization matrix.
- Nodes: on-prem agents (long-running) and Google Compute Engine
  (ephemeral, provisioned on demand). Configurable executors per node.
- Plugins: out-of-process gRPC plugins, modeled on HashiCorp
  go-plugin.
- License: GPL-3.0 (see LICENSE). If wolfSSL Inc. later needs
  different terms, update LICENSE and this paragraph in the same
  commit.

## Directory Layout

```
wolfCI/
  CLAUDE.md              this file
  PLAN.md                durable plan; updated every loop
  README.md
  LICENSE                GPL-3.0
  go.mod                 Go module (path: github.com/kaleb-himes/wolfCI)
  cmd/                   main packages
    wolfci/              the server
    wolfci-agent/        the executor that runs on nodes
    wolfci-ctl/          the admin CLI
  internal/              private Go packages
    tlsutil/             CGO wrapper around wolfSSL
    storage/             on-disk persistence
    auth/                authentication
    authz/               authorization matrix
    scheduler/           job queue and dispatch
    nodes/               node drivers (on-prem, gce)
    plugin/              plugin host
    server/              HTTP handlers
    ui/                  template rendering
  web/                   embedded HTML, CSS, JS for the UI
  plugins/               plugin SDK and built-in plugins
  jobs/                  job definitions (runtime data; gitignored)
  builds/                build outputs (runtime data; gitignored)
  config-files/          server config (partly gitignored)
  nodes/                 node configs and credentials
  scripts/               build, install, lint helpers
  third_party/wolfssl/   wolfSSL submodule
  docs/                  design notes, security model, runbooks
  tests/                 integration tests
```

## Pointers

- Next work to do: see PLAN.md, section "Current Phase".
- Build instructions: scripts/build.sh.
- ASCII gate: scripts/check-ascii.sh.
- Security model: docs/SECURITY.md (written in Phase 3).
- Credits: docs/CREDITS.md.

End of CLAUDE.md.
