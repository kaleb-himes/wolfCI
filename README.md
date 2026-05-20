# wolfCI

A small, self-contained Jenkins replacement for wolfSSL Inc.

- Single static Go binary.
- TLS and crypto via wolfSSL.
- SSH-key authentication; optional username + password.
- Jenkins-style role-based authorization matrix.
- On-prem agents and ephemeral Google Compute Engine nodes.
- Extensible through out-of-process gRPC plugins.

This repository is in early bootstrap. See PLAN.md for the active
plan and CLAUDE.md for project rules.

## Status

Bootstrap (Phase 0) complete. Active phase: see "## Current Phase"
in PLAN.md.

## Layout

```
cmd/           server, agent, and CLI main packages
internal/      private Go packages
web/           embedded HTML, CSS, JS
plugins/       plugin SDK and built-in plugins
jobs/          job definitions (runtime data)
builds/        build outputs (runtime data)
config-files/  server config
nodes/         node configs
scripts/       build, install, lint helpers
third_party/   vendored deps (wolfSSL)
docs/          design notes
tests/         integration tests
```

## License

GPL-2.0. See LICENSE.

## Author

Kaleb Himes, wolfSSL Inc. See docs/CREDITS.md for contributors.
