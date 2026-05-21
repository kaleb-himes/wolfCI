#!/bin/sh
# scripts/test-install.sh - TDD gate for PLAN.md task 9.2.
#
# Verifies the OS-level install templates:
#
#   scripts/install/systemd/wolfci.service     Linux unit file
#   scripts/install/launchd/com.wolfssl.wolfci.plist
#                                              macOS launchd job
#
# Both are templates that an operator copies into place after running
# scripts/build.sh; this test makes sure they exist, name the
# expected binary path, and contain the structural sections each
# loader requires.

set -eu

cd "$(dirname "$0")/.."

UNIT="scripts/install/systemd/wolfci.service"
PLIST="scripts/install/launchd/com.wolfssl.wolfci.plist"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

# 1. Both files exist.
for f in "$UNIT" "$PLIST"; do
    if [ ! -f "$f" ]; then
        fail "$f does not exist"
    fi
done

# 2. systemd unit must declare every section systemd parses.
for section in '[Unit]' '[Service]' '[Install]'; do
    if ! grep -qF -- "$section" "$UNIT"; then
        fail "$UNIT missing systemd section $section"
    fi
done

# 3. systemd unit must wire the binary, working dir, restart policy,
#    and at least one hardening knob.
for needle in \
    'Description=' \
    'ExecStart=/usr/local/bin/wolfci' \
    'WorkingDirectory=' \
    'User=' \
    'Restart=' \
    'NoNewPrivileges=' \
    'WantedBy=multi-user.target'
do
    if ! grep -qF -- "$needle" "$UNIT"; then
        fail "$UNIT missing token $needle"
    fi
done

# 4. launchd plist must be well-formed XML with the keys launchd
#    actually reads.
for needle in \
    '<?xml version="1.0"' \
    '<!DOCTYPE plist' \
    '<plist version="1.0">' \
    '<key>Label</key>' \
    '<string>com.wolfssl.wolfci</string>' \
    '<key>ProgramArguments</key>' \
    '/usr/local/bin/wolfci' \
    '<key>RunAtLoad</key>' \
    '<key>KeepAlive</key>' \
    '<key>WorkingDirectory</key>'
do
    if ! grep -qF -- "$needle" "$PLIST"; then
        fail "$PLIST missing token $needle"
    fi
done

# 5. If plutil is available (macOS), use it as the strict
#    well-formedness gate. Skip on Linux where plutil is absent.
if command -v plutil >/dev/null 2>&1; then
    if ! plutil -lint "$PLIST" >/dev/null 2>&1; then
        plutil -lint "$PLIST" >&2 || true
        fail "$PLIST failed plutil -lint"
    fi
fi

echo "test-install.sh: PASS"
