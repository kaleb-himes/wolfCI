#!/bin/sh
# scripts/test-getting-started.sh - TDD gate for PLAN.md task 9.3.
#
# Asserts that docs/GETTING-STARTED.md exists and walks an operator
# from "git clone" to "first green build" by mentioning every step
# of the happy path:
#
#   1. clone + submodules
#   2. build the wolfSSL static lib (scripts/build-wolfssl.sh)
#   3. build the wolfCI binaries (scripts/build.sh)
#   4. install the systemd unit or launchd plist
#   5. bootstrap an admin via the one-time token printed at first
#      start (per Phase 3 decisions)
#   6. configure the CLI (wolfci-ctl login)
#   7. create a job
#   8. run the job (wolfci-ctl job run)
#   9. tail the build log (wolfci-ctl build log)
#
# The doc need not match these tokens verbatim, but it MUST mention
# each step in enough detail that an operator can follow it without
# reading source. The test enforces the tokens that uniquely
# identify each step.

set -eu

cd "$(dirname "$0")/.."

DOC="docs/GETTING-STARTED.md"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -f "$DOC" ]; then
    fail "$DOC does not exist"
fi

# Each grep pattern below covers one numbered step in the happy path.
for needle in \
    'git clone' \
    'git submodule' \
    'scripts/build-wolfssl.sh' \
    'scripts/build.sh' \
    'build/bin/' \
    'wolfci-ctl login' \
    'wolfci-ctl job' \
    'wolfci-ctl build log' \
    'config-files/auth/matrix.yaml' \
    'systemd' \
    'launchd'
do
    if ! grep -qF -- "$needle" "$DOC"; then
        fail "$DOC missing required step marker: $needle"
    fi
done

# Doc should also call out the first-admin one-time token bootstrap.
# Phase 3 SECURITY.md uses the phrase "one-time token"; check for
# either that exact phrase or "bootstrap token".
if ! grep -qE 'one-time token|bootstrap token' "$DOC"; then
    fail "$DOC missing first-admin bootstrap token step"
fi

# Doc should mention the 10-minute claim so the title and content
# stay in sync with PLAN.md.
if ! grep -qF '10' "$DOC"; then
    fail "$DOC does not reference the 10-minute path claim"
fi

echo "test-getting-started.sh: PASS"
