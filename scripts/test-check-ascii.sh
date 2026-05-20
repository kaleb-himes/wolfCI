#!/bin/sh
# scripts/test-check-ascii.sh - failing-first test for check-ascii.sh.
#
# This test was written before check-ascii.sh existed (Phase 0.6, TDD).
# It exercises the script against ASCII and non-ASCII fixtures and
# expects exit 0 for clean input and non-zero for any byte > 0x7F.

set -eu

here=$(cd "$(dirname "$0")" && pwd)
script="$here/check-ascii.sh"

if [ ! -x "$script" ]; then
    echo "FAIL: $script not executable (or does not exist)" >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 1. A clean ASCII file must pass.
printf 'hello world\n' > "$tmp/clean.txt"
if ! "$script" "$tmp/clean.txt" >/dev/null; then
    echo "FAIL: clean file rejected" >&2
    exit 1
fi

# 2. An emdash (U+2014, UTF-8 0xE2 0x80 0x94) must trip the check.
printf 'hello \xe2\x80\x94 world\n' > "$tmp/emdash.txt"
if "$script" "$tmp/emdash.txt" >/dev/null 2>&1; then
    echo "FAIL: emdash file was accepted" >&2
    exit 1
fi

# 3. An endash (U+2013, UTF-8 0xE2 0x80 0x93) must trip the check.
printf 'hello \xe2\x80\x93 world\n' > "$tmp/endash.txt"
if "$script" "$tmp/endash.txt" >/dev/null 2>&1; then
    echo "FAIL: endash file was accepted" >&2
    exit 1
fi

# 4. Curly quotes (U+201C, U+201D) must trip the check.
printf '\xe2\x80\x9chello\xe2\x80\x9d\n' > "$tmp/quotes.txt"
if "$script" "$tmp/quotes.txt" >/dev/null 2>&1; then
    echo "FAIL: smart-quote file was accepted" >&2
    exit 1
fi

# 5. Mixed clean + dirty input set: at least one bad file must fail
# the whole run.
if "$script" "$tmp/clean.txt" "$tmp/emdash.txt" >/dev/null 2>&1; then
    echo "FAIL: mixed input set was accepted despite dirty member" >&2
    exit 1
fi

echo "test-check-ascii.sh: PASS"
