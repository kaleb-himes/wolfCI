#!/bin/sh
# scripts/test.sh - run the fast wolfCI test suite.
#
# Included:
#   - scripts/check-ascii.sh             ASCII invariant on tracked files
#   - scripts/test-check-ascii.sh        self-test for the ASCII gate
#   - scripts/test-wolfssl-submodule.sh  wolfSSL submodule pin
#   - scripts/test-wolfssh-submodule.sh  wolfssh submodule pin
#   - go test ./internal/... ./cmd/... ./plugins/email-on-failure   Go unit tests
#   - scripts/test-build.sh              host-platform release build
#   - scripts/test-install.sh            systemd unit + launchd plist
#   - scripts/test-getting-started.sh    docs/GETTING-STARTED.md gate
#   - scripts/test-architecture.sh       docs/ARCHITECTURE.md gate
#   - scripts/test-readme.sh             README.md final-pass gate
#   - scripts/test-security.sh           docs/SECURITY.md gate
#   - scripts/test-go-wolfssl.sh         vendored go-wolfssl submodule
#   - scripts/test-examples.sh           examples/jobs/*.yaml round-trip
#
# Intentionally NOT included:
#   - scripts/test-build-wolfssl.sh: full wolfSSL build (minutes).
#     Run it explicitly when scripts/build-wolfssl.sh or the
#     wolfSSL pin changes.
#   - scripts/test-build-wolfssh.sh: full wolfssh build (minutes).
#     Same idea - run it explicitly when wolfssh changes.
#
# Why scope go test to ./internal/... rather than ./...:
#   The wolfSSL submodule under third_party/wolfssl contains paths
#   (IDE project files, header trees) that confuse Go's package
#   discovery. We restrict the Go test surface to wolfCI's own code
#   under internal/ and cmd/.

set -eu

cd "$(dirname "$0")/.."

scripts/check-ascii.sh
scripts/test-check-ascii.sh
scripts/test-wolfssl-submodule.sh
scripts/test-wolfssh-submodule.sh

# Apply local patches to the go-wolfssl submodule worktree (no-op
# if the patches are already applied). Patches carry the
# wolfCI-specific #cgo directives that point go-wolfssl at the
# vendored wolfSSL build, so we do NOT need to set CGO_CFLAGS or
# CGO_LDFLAGS globally (which would leak into non-cgo test binaries
# and trip macOS dyld's missing-LC_UUID check).
if [ -d third_party/go-wolfssl ] && [ -d third_party/go-wolfssl-patches ]; then
    if [ -z "$(git -C third_party/go-wolfssl diff --name-only)" ]; then
        for patch in third_party/go-wolfssl-patches/*.patch; do
            [ -e "$patch" ] || continue
            git -C third_party/go-wolfssl apply "../../$patch"
        done
    fi
fi

go test ./internal/... ./cmd/... ./plugins/email-on-failure

scripts/test-build.sh
scripts/test-install.sh
scripts/test-getting-started.sh
scripts/test-architecture.sh
scripts/test-readme.sh
scripts/test-security.sh
scripts/test-go-wolfssl.sh
scripts/test-examples.sh

echo "scripts/test.sh: all fast checks passed."
