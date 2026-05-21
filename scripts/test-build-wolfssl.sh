#!/bin/sh
# scripts/test-build-wolfssl.sh - TDD gate for PLAN.md task 1.2.
#
# Verifies that scripts/build-wolfssl.sh:
#   - exists and is executable
#   - declares the CI server profile (TLS 1.3 on, oldtls off,
#     static lib, modern AEAD + ECC)
#   - refuses WOLFSSL_FIPS=1 until the FIPS profile is wired up
#   - actually builds wolfSSL and installs libwolfssl.a plus headers
#   - produces a library that exports at least one TLS 1.3 server
#     symbol
#
# Written before scripts/build-wolfssl.sh existed (red phase).

set -eu

SCRIPT="scripts/build-wolfssl.sh"
INSTALL_PREFIX="build/wolfssl-install"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

# 1. Script must exist and be executable.
if [ ! -x "$SCRIPT" ]; then
    fail "$SCRIPT not executable (or does not exist)"
fi

# 2. Source must reference the required CI-profile configure flags.
for flag in \
    --enable-tls13 \
    --disable-sslv3 \
    --disable-oldtls \
    --enable-static \
    --disable-shared \
    --enable-aesgcm \
    --enable-chacha \
    --enable-ecc \
    --enable-curve25519 \
    --enable-ed25519 \
    --enable-sni \
    --enable-alpn \
    --enable-keygen \
    --enable-certgen \
    --enable-certext
do
    if ! grep -qF -- "$flag" "$SCRIPT"; then
        fail "$SCRIPT is missing required configure flag $flag"
    fi
done

# 3. WOLFSSL_FIPS=1 must be refused (FIPS profile not yet wired).
if WOLFSSL_FIPS=1 "$SCRIPT" >/dev/null 2>&1; then
    fail "WOLFSSL_FIPS=1 should be refused until the FIPS profile lands"
fi

# 4. Plain run must succeed.
if ! "$SCRIPT"; then
    fail "$SCRIPT exited non-zero on a plain build"
fi

# 5. Static library must be installed.
if [ ! -f "$INSTALL_PREFIX/lib/libwolfssl.a" ]; then
    fail "$INSTALL_PREFIX/lib/libwolfssl.a not produced"
fi

# 6. Library must export a TLS 1.3 server method.
if ! nm "$INSTALL_PREFIX/lib/libwolfssl.a" 2>/dev/null | grep -q 'wolfTLSv1_3_server_method'; then
    fail "libwolfssl.a does not export wolfTLSv1_3_server_method"
fi

# 7. Public header must be installed.
if [ ! -f "$INSTALL_PREFIX/include/wolfssl/ssl.h" ]; then
    fail "$INSTALL_PREFIX/include/wolfssl/ssl.h not installed"
fi

echo "test-build-wolfssl.sh: PASS"
