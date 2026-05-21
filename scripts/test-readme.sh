#!/bin/sh
# scripts/test-readme.sh - TDD gate for PLAN.md task 9.5.
#
# README.md is the project's first-impression doc. After Phase 9 it
# must:
#   - drop the "early bootstrap" language from the Phase 0 stub
#   - name every binary and point at the per-binary docs
#   - link to GETTING-STARTED.md, ARCHITECTURE.md, SECURITY.md,
#     PLUGINS.md
#   - carry a short build recipe so a reader can reproduce a binary
#     without clicking through to the long-form docs
#   - keep the GPL-3.0 + Kaleb Himes attribution

set -eu

cd "$(dirname "$0")/.."

DOC="README.md"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -f "$DOC" ]; then
    fail "$DOC does not exist"
fi

# 1. Stub language must be gone.
if grep -qF 'early bootstrap' "$DOC"; then
    fail "$DOC still claims 'early bootstrap'; that language belongs to the Phase 0 stub"
fi

# 2. All three binaries named.
for bin in wolfci wolfci-agent wolfci-ctl; do
    if ! grep -qF -- "$bin" "$DOC"; then
        fail "$DOC missing binary reference: $bin"
    fi
done

# 3. Long-form docs linked.
for doc_link in \
    'docs/GETTING-STARTED.md' \
    'docs/ARCHITECTURE.md' \
    'docs/SECURITY.md' \
    'docs/PLUGINS.md'
do
    if ! grep -qF -- "$doc_link" "$DOC"; then
        fail "$DOC missing link to $doc_link"
    fi
done

# 4. Build recipe present.
for needle in \
    'scripts/build-wolfssl.sh' \
    'scripts/build.sh' \
    'git submodule'
do
    if ! grep -qF -- "$needle" "$DOC"; then
        fail "$DOC missing build-recipe token: $needle"
    fi
done

# 5. License + author retained.
for needle in 'GPL-3.0' 'Kaleb Himes'; do
    if ! grep -qF -- "$needle" "$DOC"; then
        fail "$DOC missing footer token: $needle"
    fi
done

# 6. Status line should reflect the current phase, not Phase 0.
if grep -qF 'Bootstrap (Phase 0) complete' "$DOC"; then
    fail "$DOC still says 'Bootstrap (Phase 0) complete'; update for Phase 9"
fi

echo "test-readme.sh: PASS"
