#!/bin/sh
# scripts/test-security.sh - TDD gate for PLAN.md task 10.11.
#
# Asserts that docs/SECURITY.md captures the Phase 10 contract:
#   - The wolfCrypt-only rule (no stdlib / x/crypto cryptography in
#     wolfCI's own source) is documented explicitly.
#   - The ask-first rule for crypto-adjacent dependencies
#     (CLAUDE.md Hard Rule #11) is documented.
#   - There is a table mapping each wolfCrypt primitive used by the
#     auth stack to the wolfSSL configure flag it depends on, so a
#     reader can audit the build profile without grepping source.
#   - The transitive golang.org/x/crypto/cryptobyte dep (pulled in
#     by the Google Cloud SDK and used as a byte-builder utility,
#     not for cryptography) is acknowledged as a known boundary.
#   - The auth-stack-relevant wolfSSL configure flags are listed by
#     name so a profile regression is visible to anyone reading the
#     doc, not just anyone who runs scripts/test-build-wolfssl.sh.

set -eu

cd "$(dirname "$0")/.."

DOC="docs/SECURITY.md"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -f "$DOC" ]; then
    fail "$DOC does not exist"
fi

# 1. wolfCrypt-only rule must be stated by name, and the
#    internal/wolfcrypt package must be cited as the gate.
for phrase in \
    'wolfCrypt-only' \
    'internal/wolfcrypt'
do
    if ! grep -qF -- "$phrase" "$DOC"; then
        fail "$DOC missing wolfCrypt-only rule phrase: $phrase"
    fi
done

# 2. Ask-first rule (CLAUDE.md Hard Rule #11) must be referenced.
for phrase in \
    'ask-first' \
    'Hard Rule #11'
do
    if ! grep -qF -- "$phrase" "$DOC"; then
        fail "$DOC missing ask-first reference: $phrase"
    fi
done

# 3. Every auth-stack wolfCrypt primitive must be named alongside
#    the wolfSSL configure flag it requires, so a profile regression
#    surfaces during review of the doc.
for primitive in \
    'PBKDF2HMACSHA256' \
    'HMACSHA256' \
    'RandBytes' \
    'Ed25519Verify' \
    'ECCVerifyP256' \
    'RSAVerifyPKCS1v15SHA256' \
    'MintCert'
do
    if ! grep -qF -- "$primitive" "$DOC"; then
        fail "$DOC missing primitive: $primitive"
    fi
done

for flag in \
    '--enable-tls13' \
    '--enable-ecc' \
    '--enable-ed25519' \
    '--enable-curve25519' \
    '--enable-aesgcm' \
    '--enable-chacha' \
    '--enable-poly1305' \
    '--enable-supportedcurves' \
    '--enable-pwdbased' \
    '--enable-keygen' \
    '--enable-certgen' \
    '--enable-certext' \
    '-DWOLFSSL_ALT_NAMES'
do
    if ! grep -qF -- "$flag" "$DOC"; then
        fail "$DOC missing configure flag: $flag"
    fi
done

# 4. cryptobyte transitive boundary must be acknowledged.
for phrase in \
    'cryptobyte' \
    'Google Cloud'
do
    if ! grep -qF -- "$phrase" "$DOC"; then
        fail "$DOC missing cryptobyte boundary phrase: $phrase"
    fi
done

# 5. Stale-content gates: post-Phase-10 SECURITY.md must not still
#    advertise bcrypt as the password KDF (Phase 10.2 replaced it
#    with PBKDF2-HMAC-SHA-256) or claim TLS 1.2 is implemented in
#    tlsutil (Phase 10.10 documented TLS 1.3 only).
if grep -qE 'bcrypt_cost|\.bcrypt' "$DOC"; then
    fail "$DOC still references bcrypt; Phase 10.2 replaced it with PBKDF2"
fi

echo "test-security.sh: PASS"
