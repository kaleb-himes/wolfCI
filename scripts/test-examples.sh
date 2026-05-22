#!/bin/sh
# scripts/test-examples.sh - TDD gate for PLAN.md task 15.7.
#
# Verifies the examples/jobs/*.yaml specs that ship with the
# repo:
#   - parse as storage.Job,
#   - round-trip (marshal + unmarshal) without diff,
#   - persist via storage.SaveJob (which runs the Phase 15.1
#     trigger-graph cycle check) without rejection.
#
# Implementation lives in internal/storage/examples_test.go;
# this script just narrows go test to that single test so an
# operator can run it on its own.

set -eu

cd "$(dirname "$0")/.."

# The test walks examples/jobs/ via a runtime.Caller-derived
# absolute path, so it does not matter where 'go test' is
# invoked from - but cd to the repo root anyway keeps the
# script's behavior consistent with the other test-*.sh
# files.

if [ ! -d examples/jobs ]; then
    echo "test-examples.sh: examples/jobs/ does not exist" >&2
    exit 1
fi

go test -count=1 -run TestExamples_AllSpecsRoundTripAndPersist \
    ./internal/storage/

echo "test-examples.sh: PASS"
