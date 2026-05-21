#!/bin/sh
# scripts/test-go-wolfssl.sh - TDD gate for PLAN.md tasks 10.5 +
# 10.6 (patch infrastructure).
#
# Verifies the vendored go-wolfssl submodule:
#   - third_party/go-wolfssl exists
#   - the submodule is checked out at the SHA recorded in
#     third_party/go-wolfssl-version.txt
#   - the expected top-level Go source files are present
#   - the local patches under third_party/go-wolfssl-patches/ are
#     applied to the submodule worktree (re-applied if missing)
#   - the patched root package compiles against our vendored
#     wolfSSL install at build/wolfssl-install/. This is the
#     real-build smoke check that 10.5 deliberately deferred.

set -eu

cd "$(dirname "$0")/.."

DIR="third_party/go-wolfssl"
VERFILE="third_party/go-wolfssl-version.txt"
PATCH_DIR="third_party/go-wolfssl-patches"

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

for f in random.go hmac.go hash.go ecc.go ssl.go x509.go; do
    if [ ! -f "$DIR/$f" ]; then
        fail "$DIR/$f missing; vendored layout has changed unexpectedly"
    fi
done

# Apply any local patches. Each patch is a `git diff` against the
# pinned SHA; if the submodule worktree is clean (no diff against
# HEAD), the patches need to be re-applied. If a diff is already
# present we trust it matches the patch set and skip re-apply.
if [ -d "$PATCH_DIR" ]; then
    have_diff=$(git -C "$DIR" diff --name-only)
    if [ -z "$have_diff" ]; then
        for patch in "$PATCH_DIR"/*.patch; do
            [ -e "$patch" ] || continue
            if ! git -C "$DIR" apply "../../$patch" 2>/tmp/wolfci-gw-patch.log; then
                cat /tmp/wolfci-gw-patch.log >&2
                fail "applying $patch failed"
            fi
        done
    fi
fi

# Real build of the root package against our vendored wolfSSL.
# go-wolfssl does not declare the darwin frameworks (Security,
# CoreFoundation) that libwolfssl.a needs on macOS, so we add them
# via CGO_LDFLAGS here. On Linux those flags are a no-op.
WOLFSSL_INSTALL="$(pwd)/build/wolfssl-install"
if [ ! -f "$WOLFSSL_INSTALL/lib/libwolfssl.a" ]; then
    echo "test-go-wolfssl.sh: SKIP smoke build (wolfSSL not built; run scripts/build-wolfssl.sh)"
    echo "test-go-wolfssl.sh: PASS (source + patch checks only)"
    exit 0
fi
darwin_frameworks=""
case "$(uname -s)" in
    Darwin) darwin_frameworks="-framework Security -framework CoreFoundation" ;;
esac
export CGO_CFLAGS="-I$WOLFSSL_INSTALL/include ${CGO_CFLAGS:-}"
# shellcheck disable=SC2089
export CGO_LDFLAGS="-L$WOLFSSL_INSTALL/lib -lwolfssl $darwin_frameworks ${CGO_LDFLAGS:-}"
if ! ( cd "$DIR" && go build . ) 2>/tmp/wolfci-go-wolfssl.log; then
    cat /tmp/wolfci-go-wolfssl.log >&2
    fail "go build inside $DIR (root package) failed"
fi

echo "test-go-wolfssl.sh: PASS"
