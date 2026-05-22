#!/bin/sh
# scripts/test-build.sh - TDD gate for PLAN.md task 9.1.
#
# Verifies that scripts/build.sh:
#   - exists and is executable
#   - declares the host-platform default and the GOOS/GOARCH override
#   - builds wolfci, wolfci-agent, wolfci-ctl for the host platform
#   - drops each binary under build/bin/<goos>-<goarch>/<binary>
#   - the produced binaries are executable on the host
#   - injects a non-default version via -ldflags so release tags can
#     be stamped at build time
#   - leaves the working tree clean (no stray binaries in the repo
#     root from a missing -o flag)
#
# Skipped if scripts/build-wolfssl.sh has not been run yet (no static
# lib to link against). Run scripts/build-wolfssl.sh explicitly first.

set -eu

cd "$(dirname "$0")/.."

SCRIPT="scripts/build.sh"
WOLFSSL_LIB="build/wolfssl-install/lib/libwolfssl.a"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

# 1. Script must exist and be executable.
if [ ! -x "$SCRIPT" ]; then
    fail "$SCRIPT not executable (or does not exist)"
fi

# 2. Source must reference the binary targets and the layout.
for needle in \
    cmd/wolfci \
    cmd/wolfci-agent \
    cmd/wolfci-ctl \
    build/bin \
    GOOS \
    GOARCH \
    -ldflags
do
    if ! grep -qF -- "$needle" "$SCRIPT"; then
        fail "$SCRIPT is missing required token $needle"
    fi
done

# 3. Without wolfSSL pre-built, skip the live build portion; the
#    source-level checks above still gate the change.
if [ ! -f "$WOLFSSL_LIB" ]; then
    echo "test-build.sh: SKIP live build (wolfSSL not built; run scripts/build-wolfssl.sh)"
    exit 0
fi

# 4. Capture pre-build state of repo-root files. We must not leave
#    stray binaries from a missing -o flag.
pre_root_files=$(ls -1)

# 5. Plain run must succeed.
WOLFCI_BUILD_VERSION=test-9.1 "$SCRIPT" >/tmp/wolfci-build.log 2>&1 || {
    cat /tmp/wolfci-build.log >&2
    fail "$SCRIPT exited non-zero on a host-platform build"
}

# 6. Binaries must land under build/bin/<goos>-<goarch>/.
goos=$(go env GOOS)
goarch=$(go env GOARCH)
bindir="build/bin/$goos-$goarch"
for bin in wolfci wolfci-agent wolfci-ctl; do
    if [ ! -x "$bindir/$bin" ]; then
        fail "$bindir/$bin not produced or not executable"
    fi
done

# 7. wolfci-ctl version must reflect the injected -ldflags value.
out=$("$bindir/wolfci-ctl" version 2>&1)
case "$out" in
    *test-9.1*) ;;
    *) fail "wolfci-ctl version did not reflect WOLFCI_BUILD_VERSION (got: $out)" ;;
esac

# 7b. wolfci-agent --version must also reflect the stamped version
# (PLAN.md 12.8: NodeStatus.agent_version comes from the same
# -ldflags-injected main.version variable, surfaced for operators
# via --version on the agent binary).
out=$("$bindir/wolfci-agent" --version 2>&1)
case "$out" in
    *test-9.1*) ;;
    *) fail "wolfci-agent --version did not reflect WOLFCI_BUILD_VERSION (got: $out)" ;;
esac

# 8. Repo-root file set must be unchanged (no stray go-build outputs).
post_root_files=$(ls -1)
if [ "$pre_root_files" != "$post_root_files" ]; then
    printf 'pre :\n%s\npost:\n%s\n' "$pre_root_files" "$post_root_files" >&2
    fail "scripts/build.sh left stray files in the repo root"
fi

echo "test-build.sh: PASS"
