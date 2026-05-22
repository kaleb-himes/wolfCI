#!/bin/sh
# scripts/test-architecture.sh - TDD gate for PLAN.md task 9.4.
#
# Asserts that docs/ARCHITECTURE.md exists, contains an ASCII
# component diagram, and names every top-level wolfCI package +
# every runtime data directory so the doc cannot silently lag the
# implementation.

set -eu

cd "$(dirname "$0")/.."

DOC="docs/ARCHITECTURE.md"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

if [ ! -f "$DOC" ]; then
    fail "$DOC does not exist"
fi

# 1. Every top-level Go package wolfCI ships must appear in the
#    doc so a reader has a complete map.
for pkg in \
    internal/tlsutil \
    internal/storage \
    internal/auth \
    internal/authz \
    internal/scheduler \
    internal/agentsvc \
    internal/agent \
    internal/nodes \
    internal/nodeinfo \
    internal/plugin \
    internal/server \
    internal/cliservice \
    cmd/wolfci \
    cmd/wolfci-agent \
    cmd/wolfci-ctl
do
    if ! grep -qF -- "$pkg" "$DOC"; then
        fail "$DOC missing package reference: $pkg"
    fi
done

# 1b. Phase 12 Nodes view surface: master node, heartbeat
# protocol, detail page. The doc must trace each so an operator
# coming to wolfCI from Jenkins can map the concepts.
for phrase in \
    'wolfci-master' \
    'Heartbeat' \
    'NodeStatus' \
    '/nodes/'
do
    if ! grep -qF -- "$phrase" "$DOC"; then
        fail "$DOC missing Phase 12 Nodes phrase: $phrase"
    fi
done

# 2. Runtime data directories (CLAUDE.md "self-contained" rule).
for dir in \
    'jobs/' \
    'builds/' \
    'config-files/' \
    'plugins/' \
    'nodes/'
do
    if ! grep -qF -- "$dir" "$DOC"; then
        fail "$DOC missing runtime data dir: $dir"
    fi
done

# 3. An ASCII component diagram must be present. We detect it
#    structurally: at least one fenced code block that contains
#    multiple ASCII box edges built from + and - and at least one
#    vertical bar. This is the box-drawing dialect we use elsewhere
#    in docs (SECURITY.md, PLUGINS.md).
if ! awk '
    /^```/ { in_block = !in_block; next }
    in_block {
        if (index($0, "+--") || index($0, "--+")) plus_lines++
        if (index($0, "|"))                       bar_lines++
    }
    END {
        # Two plus-corner rows and at least two bar rows means a
        # multi-component diagram, not just a single horizontal rule.
        if (plus_lines < 2 || bar_lines < 2) exit 1
    }
' "$DOC"; then
    fail "$DOC has no ASCII component diagram (looked for +--/--+ + | inside a fenced block)"
fi

echo "test-architecture.sh: PASS"
