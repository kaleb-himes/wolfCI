#!/bin/sh
# scripts/test-wolfssl-submodule.sh - test gate for PLAN.md task 1.1.
#
# Verifies that the wolfSSL submodule is present, registered in
# .gitmodules, checked out at the pinned release tag, and that the
# version-marker file matches.
#
# Written TDD-first: this script exists and runs red before the
# submodule is added, then runs green after task 1.1 completes.

set -eu

EXPECTED_TAG="v5.9.1-stable"
EXPECTED_URL="https://github.com/wolfSSL/wolfssl.git"
SUBMODULE_PATH="third_party/wolfssl"
VERSION_FILE="third_party/wolfssl-version.txt"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

# 1. .gitmodules must exist and reference the wolfSSL path + URL.
if [ ! -f .gitmodules ]; then
    fail ".gitmodules missing"
fi
if ! grep -q "path = $SUBMODULE_PATH" .gitmodules; then
    fail ".gitmodules has no entry for path $SUBMODULE_PATH"
fi
if ! grep -q "url = $EXPECTED_URL" .gitmodules; then
    fail ".gitmodules does not point at $EXPECTED_URL"
fi

# 2. Submodule directory must exist and be a checked-out git work tree.
if [ ! -d "$SUBMODULE_PATH/.git" ] && [ ! -f "$SUBMODULE_PATH/.git" ]; then
    fail "$SUBMODULE_PATH is not a git work tree (run git submodule update --init)"
fi

# 3. Submodule HEAD must match the expected release tag.
actual_sha=$(git -C "$SUBMODULE_PATH" rev-parse HEAD)
expected_sha=$(git -C "$SUBMODULE_PATH" rev-list -n 1 "$EXPECTED_TAG" 2>/dev/null || true)
if [ -z "$expected_sha" ]; then
    fail "tag $EXPECTED_TAG not present in submodule"
fi
if [ "$actual_sha" != "$expected_sha" ]; then
    fail "submodule HEAD is $actual_sha, expected $expected_sha ($EXPECTED_TAG)"
fi

# 4. Version-marker file must record the same tag.
if [ ! -f "$VERSION_FILE" ]; then
    fail "$VERSION_FILE missing"
fi
if ! grep -q "^$EXPECTED_TAG\$" "$VERSION_FILE"; then
    fail "$VERSION_FILE does not contain the line '$EXPECTED_TAG'"
fi

echo "test-wolfssl-submodule.sh: PASS"
