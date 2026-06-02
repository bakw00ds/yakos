#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: plan-outcome-capture.sh — SessionEnd hook fallback for outcome
#          capture when the operator does not run 'yakos work close'.
#
# This is a TELEMETRY HOOK. It NEVER blocks. Always exits 0.
# Best-effort: if it cannot identify an open plan_id it logs and exits 0.
#
# What it does:
#   1. Looks for the most recent plan_scored record that does NOT yet have
#      a corresponding plan_outcome record (same plan_id).
#   2. If found, computes best-effort outcome fields (git diff stats,
#      dispatch-log sums) and appends a plan_outcome record.
#   3. human_signal is always null (no interactive prompt in SessionEnd path).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
[ -f "$HOOK_DIR/lib/compat.sh" ] && . "$HOOK_DIR/lib/compat.sh"

hi_init

PLAN_QUALITY_LOG="${YAKOS_PLAN_QUALITY_LOG:-$HOME/.yakos-state/plan-quality-log.ndjson}"
DISPATCH_LOG="${YAKOS_DISPATCH_LOG:-$HOME/.yakos-state/dispatch-log.ndjson}"

_safe_exit() {
    ho_log "plan-outcome-capture" "REPORT" "pass" "$1" "${2:-null}" 2>/dev/null || true
    exit 0
}

# ---- require jq (best-effort) -----------------------------------------------
if ! command -v jq >/dev/null 2>&1; then
    _safe_exit "plan-outcome-capture: jq not available; skipping"
fi

# ---- find unmatched plan_id -------------------------------------------------
if [ ! -f "$PLAN_QUALITY_LOG" ] || [ ! -s "$PLAN_QUALITY_LOG" ]; then
    _safe_exit "plan-outcome-capture: no plan-quality-log found; nothing to capture"
fi

# Find the most recent plan_scored plan_id that has no plan_outcome record yet
PLAN_ID=""
PLAN_ID="$(jq -r 'select(.type == "plan_scored") | .plan_id' "$PLAN_QUALITY_LOG" 2>/dev/null \
    | while IFS= read -r pid; do
        [ -n "$pid" ] || continue
        matched="$(jq -r --arg pid "$pid" 'select(.type == "plan_outcome" and .plan_id == $pid) | .plan_id' \
            "$PLAN_QUALITY_LOG" 2>/dev/null | head -1 || true)"
        if [ -z "$matched" ]; then
            printf '%s\n' "$pid"
        fi
    done | tail -1)" 2>/dev/null || true

if [ -z "$PLAN_ID" ]; then
    _safe_exit "plan-outcome-capture: all scored plans already have outcome records"
fi

ct_log "plan-outcome-capture: capturing best-effort outcome for plan_id=$PLAN_ID" 2>/dev/null || true

# ---- project path -----------------------------------------------------------
# Try to find the project path from the scored record
PROJECT_DIR="$(jq -r --arg pid "$PLAN_ID" \
    'select(.type == "plan_scored" and .plan_id == $pid) | .project // empty' \
    "$PLAN_QUALITY_LOG" 2>/dev/null | tail -1 || true)"
[ -z "$PROJECT_DIR" ] && PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd -P)}"

# ---- git diff stats (best-effort) -------------------------------------------
FILES_TOUCHED="null"
LINES_CHANGED="null"

if [ -n "$PROJECT_DIR" ] && \
   ([ -d "$PROJECT_DIR/.git" ] || git -C "$PROJECT_DIR" rev-parse --git-dir >/dev/null 2>&1); then
    stats="$(git -C "$PROJECT_DIR" diff --numstat HEAD 2>/dev/null || true)"
    if [ -n "$stats" ]; then
        FILES_TOUCHED="$(printf '%s\n' "$stats" | awk 'END {print NR}')"
        LINES_CHANGED="$(printf '%s\n' "$stats" | awk '{a+=$1; d+=$2} END {print a+d}')"
    fi
fi

# ---- dispatch-log stats (best-effort) ---------------------------------------
TOKENS_SPENT="null"
COST_USD="null"

if [ -f "$DISPATCH_LOG" ]; then
    result="$(jq -r '[.tokens_total // 0, .cost_usd // 0] | @tsv' \
        "$DISPATCH_LOG" 2>/dev/null \
        | awk 'BEGIN{t=0;c=0} {t+=$1; c+=$2} END{printf "%d\t%.4f", t, c}' || true)"
    if [ -n "$result" ]; then
        TOKENS_SPENT="$(printf '%s' "$result" | cut -f1)"
        COST_USD="$(printf '%s' "$result" | cut -f2)"
    fi
fi

# ---- assemble record --------------------------------------------------------
TS_NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

RECORD="$(jq -cn \
    --arg type "plan_outcome" \
    --arg ts "$TS_NOW" \
    --arg plan_id "$PLAN_ID" \
    --arg project "$PROJECT_DIR" \
    --argjson first_try_pass "null" \
    --argjson rework_cycles "null" \
    --argjson files_touched "$FILES_TOUCHED" \
    --argjson files_touched_estimated "null" \
    --argjson scope_creep_ratio "null" \
    --argjson lines_changed "$LINES_CHANGED" \
    --argjson tokens_spent_total "$TOKENS_SPENT" \
    --argjson cost_usd_actual "$COST_USD" \
    --argjson rework_failure_modes "[]" \
    --argjson human_signal "null" \
    '{
        type: $type,
        ts: $ts,
        plan_id: $plan_id,
        project: $project,
        first_try_pass: $first_try_pass,
        rework_cycles: $rework_cycles,
        files_touched: $files_touched,
        files_touched_estimated: $files_touched_estimated,
        scope_creep_ratio: $scope_creep_ratio,
        lines_changed: $lines_changed,
        tokens_spent_total: $tokens_spent_total,
        cost_usd_actual: $cost_usd_actual,
        rework_failure_modes: $rework_failure_modes,
        human_signal: $human_signal,
        capture_method: "session_end_fallback"
    }')" 2>/dev/null || {
    _safe_exit "plan-outcome-capture: failed to build record JSON; skipping"
}

# ---- write to log -----------------------------------------------------------
mkdir -p "$(dirname "$PLAN_QUALITY_LOG")" 2>/dev/null || true

# No ct_rotate_log here — telemetry hook, keep it minimal
printf '%s\n' "$RECORD" >> "$PLAN_QUALITY_LOG" 2>/dev/null || {
    _safe_exit "plan-outcome-capture: failed to write record; skipping"
}

_safe_exit "plan-outcome-capture: best-effort outcome captured for plan_id=$PLAN_ID" \
    "$(jq -nc --arg pid "$PLAN_ID" --argjson ft "$FILES_TOUCHED" '{plan_id: $pid, files_touched: $ft}')"
