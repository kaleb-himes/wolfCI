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

# Phase 11 reality check: cmd/wolfci requires either --config or
# the positional <addr> <cert> <key> quick mode. The doc has to
# show the operator how to invoke the server.
if ! grep -qF -- '--config' "$DOC"; then
    fail "$DOC missing --config invocation (cmd/wolfci no longer runs with no args)"
fi
if ! grep -qF 'server.yaml' "$DOC"; then
    fail "$DOC missing server.yaml config example"
fi

# Phase 11 bootstrap is BYOK at /setup, not the older /bootstrap
# URL. The doc has to send the operator to the right place and
# spell out that they bring their own SSH pubkey.
if ! grep -qF '/setup' "$DOC"; then
    fail "$DOC missing /setup URL (Phase 11 first-admin path)"
fi
if ! grep -qE 'paste|bring your own|BYOK' "$DOC"; then
    fail "$DOC missing the BYOK rule (operator pastes their own pubkey at /setup)"
fi

# Day-2 user management lives in SECURITY.md per PLAN.md 11.7.
# GETTING-STARTED ends the 10-minute path; ongoing user mgmt
# (add/remove admins, role matrix updates) belongs in the
# security doc, not here. The doc must point readers there.
if ! grep -qF 'SECURITY.md' "$DOC"; then
    fail "$DOC missing pointer to docs/SECURITY.md for day-2 user management"
fi

echo "test-getting-started.sh: PASS"
