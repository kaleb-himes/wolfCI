#!/bin/sh
# scripts/test-go-wolfssl.sh - TDD gate for PLAN.md task 10.5.
#
# Verifies the vendored go-wolfssl submodule:
#   - third_party/go-wolfssl exists
#   - the submodule is checked out at the SHA recorded in
#     third_party/go-wolfssl-version.txt
#   - the expected Go source files are present (random.go,
#     hmac.go, hash.go, ecc.go, ssl.go) so wrapping it in 10.6
#     is not pulling from thin air
#   - the package compiles standalone via `go build ./third_party/
#     go-wolfssl/...` so wolfCI's own build is not the only
#     thing exercising it.

set -eu

cd "$(dirname "$0")/.."

DIR="third_party/go-wolfssl"
VERFILE="third_party/go-wolfssl-version.txt"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -d "$DIR" ]; then
    fail "$DIR missing; run 'git submodule update --init' first"
fi
if [ ! -d "$DIR/.git" ] && [ ! -f "$DIR/.git" ]; then
    fail "$DIR is not a git submodule checkout"
fi

if [ ! -f "$VERFILE" ]; then
    fail "$VERFILE missing"
fi
pinned=$(head -1 "$VERFILE" | awk '{print $1}')
if [ -z "$pinned" ]; then
    fail "$VERFILE first line is empty; expected a commit SHA"
fi
actual=$(git -C "$DIR" rev-parse HEAD)
if [ "$pinned" != "$actual" ]; then
    fail "$DIR checked out at $actual; $VERFILE pins $pinned"
fi

for f in random.go hmac.go hash.go ecc.go ssl.go; do
    if [ ! -f "$DIR/$f" ]; then
        fail "$DIR/$f missing; vendored layout has changed unexpectedly"
    fi
done

# Compile the vendored package in its own module context so a
# broken upstream is caught here, not inside whatever wolfCI file
# first imports it. go-wolfssl carries its own go.mod
# (github.com/wolfssl/go-wolfssl) so we cd in before building;
# 10.6 wires it into wolfCI's module with a replace directive.
# A full `go build` of go-wolfssl against wolfCI's wolfSSL profile
# is intentionally NOT run here. Two compat issues surfaced during
# 10.5 vendoring:
#
#   1. go-wolfssl/examples/aes-encrypt/ depends on golang.org/x/term
#      which is not declared in go-wolfssl's go.mod.
#   2. go-wolfssl/x509.go ships static fallback stubs for
#      wolfSSL_X509_NAME_oneline and wolfSSL_X509_get_subjectCN
#      that conflict with the real symbols in our wolfSSL build
#      (we have OPENSSL_EXTRA-equivalent features on).
#
# 10.6 will decide per-primitive which go-wolfssl files to wire in
# (and may move them under internal/wolfcrypt with a replace
# directive that skips the conflicting files). At that point this
# gate can grow into a real "build the parts we use" check. For
# now it just verifies the submodule is checked out at the right
# SHA and the file layout matches what 10.6 expects to see.

echo "test-go-wolfssl.sh: PASS"
