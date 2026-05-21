#!/bin/sh
# scripts/test-wolfssh-submodule.sh - test gate for PLAN.md task 10.7.
#
# Verifies the wolfssh submodule is registered, checked out at the
# pinned release tag, and that the version-marker file matches.

set -eu

EXPECTED_TAG="v1.5.0-stable"
EXPECTED_URL="https://github.com/wolfSSL/wolfssh.git"
SUBMODULE_PATH="third_party/wolfssh"
VERSION_FILE="third_party/wolfssh-version.txt"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -f .gitmodules ]; then
    fail ".gitmodules missing"
fi
if ! grep -q "path = $SUBMODULE_PATH" .gitmodules; then
    fail ".gitmodules has no entry for path $SUBMODULE_PATH"
fi
if ! grep -q "url = $EXPECTED_URL" .gitmodules; then
    fail ".gitmodules does not point at $EXPECTED_URL"
fi

if [ ! -d "$SUBMODULE_PATH/.git" ] && [ ! -f "$SUBMODULE_PATH/.git" ]; then
    fail "$SUBMODULE_PATH is not a git work tree (run git submodule update --init)"
fi

actual_sha=$(git -C "$SUBMODULE_PATH" rev-parse HEAD)
expected_sha=$(git -C "$SUBMODULE_PATH" rev-list -n 1 "$EXPECTED_TAG" 2>/dev/null || true)
if [ -z "$expected_sha" ]; then
    fail "tag $EXPECTED_TAG not present in submodule"
fi
if [ "$actual_sha" != "$expected_sha" ]; then
    fail "submodule HEAD is $actual_sha, expected $expected_sha ($EXPECTED_TAG)"
fi

if [ ! -f "$VERSION_FILE" ]; then
    fail "$VERSION_FILE missing"
fi
if ! grep -q "^$EXPECTED_TAG " "$VERSION_FILE"; then
    fail "$VERSION_FILE does not start with the line '$EXPECTED_TAG '"
fi

echo "test-wolfssh-submodule.sh: PASS"
