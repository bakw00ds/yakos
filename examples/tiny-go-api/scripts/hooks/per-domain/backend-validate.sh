#!/usr/bin/env bash
# backend-validate.sh — per-domain validator for backend (Go) tasks.
#
# Invocation contract:
#   backend-validate.sh [<project-relative-path>]
#
# When invoked by task-complete-dispatch.sh, the path argument is the
# task's primary file. Without an argument, runs project-wide checks.
#
# Exit codes:
#   0   PASS   (clean run; emits structured JSON to stdout)
#   2   BLOCK  (test/lint/vet failure; emits structured JSON to stdout)
#
# In v0.1 this is invoked by a REPORT-only dispatcher, so the exit code
# is captured but does not gate task completion. The validator is fully
# functional for manual use and for v0.2's blocking dispatcher.

set -eu

TARGET="${1:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

emit_json() {
    # emit_json <decision> <reason> [extra-jq-object]
    local decision="$1" reason="$2" extra="${3:-}"
    [ -z "$extra" ] && extra='{}'
    jq -nc \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg validator "backend" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg target "$TARGET" \
        --argjson extra "$extra" \
        '{ts: $ts, validator: $validator, decision: $decision, reason: $reason, target: $target} + $extra'
}

cd "$PROJECT_DIR" 2>/dev/null || { emit_json "pass" "no project dir"; exit 0; }

# Detect Go workspace
if [ ! -f "go.mod" ] && ! find . -maxdepth 3 -name 'go.mod' -print -quit | grep -q .; then
    emit_json "pass" "no Go workspace detected"
    exit 0
fi

if ! command -v go >/dev/null 2>&1; then
    emit_json "warn" "go toolchain not installed; skipping backend validation"
    exit 0
fi

# Run go vet + go test in a captured-output mode
vet_out="$(go vet ./... 2>&1)" || vet_rc=$?
vet_rc=${vet_rc:-0}
test_out="$(go test ./... -count=1 2>&1)" || test_rc=$?
test_rc=${test_rc:-0}

if [ "$vet_rc" -ne 0 ] || [ "$test_rc" -ne 0 ]; then
    extra="$(jq -nc --argjson vet "$vet_rc" --argjson test "$test_rc" \
        --arg vet_out "$vet_out" --arg test_out "$test_out" \
        '{vet_rc: $vet, test_rc: $test, vet_output: $vet_out, test_output: $test_out}')"
    emit_json "block" "go vet/test failed" "$extra"
    exit 2
fi

emit_json "pass" "go vet + go test ./... clean"
exit 0
