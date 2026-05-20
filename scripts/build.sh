#!/bin/sh
# scripts/build.sh - build wolfCI binaries.
#
# This is a stub until Phase 9.1 implements the full build. Once the
# Go code lands in Phase 1, this will compile cmd/wolfci,
# cmd/wolfci-agent, and cmd/wolfci-ctl into a single static binary
# each, linked against the wolfSSL submodule.

set -eu

echo "scripts/build.sh: stub. Implemented in Phase 9.1." >&2
echo "Until then, once Go code lands in Phase 1, use: go build ./..." >&2
exit 0
