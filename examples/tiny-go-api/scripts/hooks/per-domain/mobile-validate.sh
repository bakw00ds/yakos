#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# mobile-validate.sh — per-domain validator for Flutter / mobile tasks.
#
# Runs `flutter analyze` and `flutter test` (timeout-wrapped per the
# flutter-tester-hang incident). Skips with PASS when no flutter project.

set -eu

TARGET="${1:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
HOOK_DIR_PARENT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
# shellcheck source=../../../cli/lib/compat.sh
COMPAT="$HOOK_DIR_PARENT/../../cli/lib/compat.sh"
[ -f "$COMPAT" ] && . "$COMPAT" || true

emit_json() {
    local decision="$1" reason="$2" extra="${3:-}"
    [ -z "$extra" ] && extra='{}'
    jq -nc \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg validator "mobile" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg target "$TARGET" \
        --argjson extra "$extra" \
        '{ts: $ts, validator: $validator, decision: $decision, reason: $reason, target: $target} + $extra'
}

cd "$PROJECT_DIR" 2>/dev/null || { emit_json "pass" "no project dir"; exit 0; }

mobile_dir=""
for d in . mobile flutter; do
    if [ -f "$d/pubspec.yaml" ]; then mobile_dir="$d"; break; fi
done
if [ -z "$mobile_dir" ]; then
    emit_json "pass" "no pubspec.yaml found"
    exit 0
fi

if ! command -v flutter >/dev/null 2>&1; then
    emit_json "warn" "flutter not installed; skipping mobile validation"
    exit 0
fi

cd "$mobile_dir"

# `flutter analyze` is fast; `flutter test` is the one that's been seen
# to hang per the flutter-tester-hang incident. Wrap test in a timeout.
analyze_out=""
test_out=""
analyze_rc=0
test_rc=0

analyze_out="$(flutter analyze 2>&1)" || analyze_rc=$?

if command -v ct_timeout >/dev/null 2>&1; then
    test_out="$(ct_timeout 120 flutter test 2>&1)" || test_rc=$?
elif command -v gtimeout >/dev/null 2>&1; then
    test_out="$(gtimeout 120 flutter test 2>&1)" || test_rc=$?
elif command -v timeout >/dev/null 2>&1; then
    test_out="$(timeout 120 flutter test 2>&1)" || test_rc=$?
else
    test_out="$(flutter test 2>&1)" || test_rc=$?
fi

if [ "$analyze_rc" -ne 0 ] || [ "$test_rc" -ne 0 ]; then
    extra="$(jq -nc --arg dir "$mobile_dir" --argjson a "$analyze_rc" --argjson t "$test_rc" \
        --arg a_out "$analyze_out" --arg t_out "$test_out" \
        '{mobile_dir: $dir, analyze_rc: $a, test_rc: $t, analyze_output: $a_out, test_output: $t_out}')"
    emit_json "block" "flutter analyze/test failed" "$extra"
    exit 2
fi

emit_json "pass" "flutter analyze + test clean"
exit 0
