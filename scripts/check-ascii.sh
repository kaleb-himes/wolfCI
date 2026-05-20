#!/bin/sh
# scripts/check-ascii.sh - fail if any file contains non-ASCII bytes.
#
# Usage:
#   scripts/check-ascii.sh                 # scan all tracked files
#   scripts/check-ascii.sh file1 file2     # scan the given files
#
# Exits 0 if every checked file is pure ASCII (bytes 0x00-0x7F).
# Exits 1 if any file contains a byte >= 0x80.
# Exits 2 on usage errors.
#
# Exemptions:
#   - third_party/* (vendored code is not ours)
#   - */testdata/*  (test fixtures may need non-ASCII on purpose)

set -eu

if [ "$#" -gt 0 ]; then
    files="$*"
else
    if [ ! -d .git ]; then
        echo "scripts/check-ascii.sh: must be run from the repo root when called with no args" >&2
        exit 2
    fi
    files=$(git ls-files)
fi

fail=0
for f in $files; do
    case "$f" in
        third_party/*) continue ;;
        */testdata/*) continue ;;
    esac

    if [ ! -f "$f" ]; then
        continue
    fi

    # tr in C locale removes every ASCII byte (octal 000-177 = 0-127).
    # If anything remains, the file holds a byte >= 0x80.
    if [ -n "$(LC_ALL=C tr -d '\000-\177' < "$f" 2>/dev/null | head -c 1)" ]; then
        printf 'non-ASCII bytes in %s\n' "$f" >&2
        fail=1
    fi
done

if [ "$fail" -ne 0 ]; then
    echo "ASCII check failed." >&2
    exit 1
fi
echo "All checked files are ASCII."
