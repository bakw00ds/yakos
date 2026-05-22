#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# frontend-validate.sh — per-domain validator for web/frontend tasks.
#
# Runs lint + test if a package.json is present and scripts are defined.
# Skips with PASS if not.

set -eu

TARGET="${1:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

emit_json() {
    local decision="$1" reason="$2" extra="${3:-}"
    [ -z "$extra" ] && extra='{}'
    jq -nc \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg validator "frontend" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg target "$TARGET" \
        --argjson extra "$extra" \
        '{ts: $ts, validator: $validator, decision: $decision, reason: $reason, target: $target} + $extra'
}

cd "$PROJECT_DIR" 2>/dev/null || { emit_json "pass" "no project dir"; exit 0; }

# Find a package.json (project root or web/, frontend/, app/)
pkg_dir=""
for d in . web frontend app; do
    if [ -f "$d/package.json" ]; then pkg_dir="$d"; break; fi
done

if [ -z "$pkg_dir" ]; then
    emit_json "pass" "no package.json found in project root or web/frontend/app/"
    exit 0
fi

if ! command -v npm >/dev/null 2>&1; then
    emit_json "warn" "npm not installed; skipping frontend validation"
    exit 0
fi

cd "$pkg_dir"

has_script() {
    jq -e --arg name "$1" '.scripts[$name] // empty | length > 0' package.json >/dev/null 2>&1
}

lint_out=""
test_out=""
lint_rc=0
test_rc=0

if has_script lint; then
    lint_out="$(npm run -s lint 2>&1)" || lint_rc=$?
fi
if has_script test; then
    test_out="$(npm test --silent 2>&1)" || test_rc=$?
fi

if [ "$lint_rc" -ne 0 ] || [ "$test_rc" -ne 0 ]; then
    extra="$(jq -nc --arg dir "$pkg_dir" --argjson lint "$lint_rc" --argjson test "$test_rc" \
        --arg lint_out "$lint_out" --arg test_out "$test_out" \
        '{package_dir: $dir, lint_rc: $lint, test_rc: $test, lint_output: $lint_out, test_output: $test_out}')"
    emit_json "block" "npm lint/test failed" "$extra"
    exit 2
fi

emit_json "pass" "npm lint/test clean (or scripts not defined)"
exit 0
