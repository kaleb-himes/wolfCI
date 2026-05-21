#!/bin/sh
# scripts/test.sh - run the fast wolfCI test suite.
#
# Included:
#   - scripts/check-ascii.sh             ASCII invariant on tracked files
#   - scripts/test-check-ascii.sh        self-test for the ASCII gate
#   - scripts/test-wolfssl-submodule.sh  wolfSSL submodule pin
#   - go test ./internal/...             Go unit tests
#
# Intentionally NOT included:
#   - scripts/test-build-wolfssl.sh: full wolfSSL build (minutes).
#     Run it explicitly when scripts/build-wolfssl.sh or the
#     wolfSSL pin changes.
#
# Why scope go test to ./internal/... rather than ./...:
#   The wolfSSL submodule under third_party/wolfssl contains paths
#   (IDE project files, header trees) that confuse Go's package
#   discovery. We restrict the Go test surface to wolfCI's own code
#   under internal/ and cmd/. Add ./cmd/... here once cmd packages
#   exist.

set -eu

cd "$(dirname "$0")/.."

scripts/check-ascii.sh
scripts/test-check-ascii.sh
scripts/test-wolfssl-submodule.sh

go test ./internal/...

echo "scripts/test.sh: all fast checks passed."
