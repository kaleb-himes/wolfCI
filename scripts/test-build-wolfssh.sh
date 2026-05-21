#!/bin/sh
# scripts/test-build-wolfssh.sh - TDD gate for PLAN.md task 10.7.
#
# Verifies that scripts/build-wolfssh.sh:
#   - exists and is executable
#   - declares the configure flags wolfCI's profile needs
#     (--with-wolfssl, --enable-static, --disable-shared,
#     --disable-examples)
#   - builds wolfssh and installs libwolfssh.a plus headers
#
# Skipped by scripts/test.sh because the full wolfssh build takes
# minutes; run this explicitly when wolfssh changes.

set -eu

cd "$(dirname "$0")/.."

SCRIPT="scripts/build-wolfssh.sh"
INSTALL_PREFIX="build/wolfssh-install"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -x "$SCRIPT" ]; then
    fail "$SCRIPT not executable (or does not exist)"
fi

for flag in \
    --with-wolfssl \
    --enable-static \
    --disable-shared \
    --disable-examples
do
    if ! grep -qF -- "$flag" "$SCRIPT"; then
        fail "$SCRIPT is missing required configure flag $flag"
    fi
done

# wolfssh's build script must reference our vendored wolfSSL
# install at build/wolfssl-install/, not a system install.
if ! grep -qF -- "build/wolfssl-install" "$SCRIPT"; then
    fail "$SCRIPT does not point --with-wolfssl at our vendored wolfSSL"
fi

# Run the build. wolfSSL must already be built (libwolfssh links
# against libwolfssl); fail with a clear message rather than
# attempting and exploding.
if [ ! -f build/wolfssl-install/lib/libwolfssl.a ]; then
    fail "build/wolfssl-install/lib/libwolfssl.a missing; run scripts/build-wolfssl.sh first"
fi
if ! "$SCRIPT"; then
    fail "$SCRIPT exited non-zero on a plain build"
fi

if [ ! -f "$INSTALL_PREFIX/lib/libwolfssh.a" ]; then
    fail "$INSTALL_PREFIX/lib/libwolfssh.a not produced"
fi
if [ ! -f "$INSTALL_PREFIX/include/wolfssh/ssh.h" ]; then
    fail "$INSTALL_PREFIX/include/wolfssh/ssh.h not installed"
fi
if ! nm "$INSTALL_PREFIX/lib/libwolfssh.a" 2>/dev/null | grep -q 'wolfSSH_Init'; then
    fail "libwolfssh.a does not export wolfSSH_Init"
fi

echo "test-build-wolfssh.sh: PASS"
