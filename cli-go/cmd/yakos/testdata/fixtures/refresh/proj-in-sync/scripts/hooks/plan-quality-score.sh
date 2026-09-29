#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: plan-quality-score.sh — PostToolUse hook that auto-scores plan.md
# on Edit|Write|MultiEdit writes.
#
# Split from plan-quality-gate.sh (K-81): the PreToolUse .plan-blocked gate
# now lives in plan-quality-gate.sh and FAILS CLOSED; this scorer keeps the
# deliberately conservative, never-block-a-save posture below.
#
# Fires when a write targets a path ending in work/current/plan.md.
#   1. Reads .yakos.yml plan_quality block (enabled, mode, threshold).
#   2. Debounces: skips if plan.md mtime changed within the last 5 s.
#   3. Invokes lib/skills/plan-quality-eval/scripts/score-plan.sh.
#   4. Routes on the aggregate score + dissent flag:
#        pass + no dissent → PASS (no surface)
#        fail + mode=surface → writes per-plan notes file, logs SURFACE
#        fail + mode=block   → writes .plan-blocked marker, logs BLOCK
#        dissent (any aggregate) → writes per-plan notes file, logs SURFACE
#
# Conservative failure mode: any infra error → ct_log WARN + exit 0.
# The hook NEVER prevents the operator from saving a plan because scoring
# infrastructure broke. (It deliberately does NOT set HOOK_FAIL_CLOSED.)
#
# Configuration in .yakos.yml:
#   plan_quality:
#     enabled: true           # default true
#     mode: surface           # surface | block
#     threshold: 0.75         # weighted aggregate (0..1)
#     cost_ceiling_usd: 0.15
#
# Emergency bypass:
#   YAKOS_PLAN_QUALITY_DISABLE=1
#
# Registered in settings.json under:
#   PostToolUse  Edit|Write|MultiEdit → scoring

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
. "$HOOK_DIR/lib/paths.sh"
# compat.sh is needed for ct_log; resolve via YAKOS_ROOT
if [ -z "${YAKOS_ROOT:-}" ]; then
    YAKOS_ROOT="$(cd "$HOOK_DIR/../.." && pwd -P)"
fi
YAKOS_LIB="${YAKOS_LIB:-$YAKOS_ROOT/cli/lib}"
if [ -f "$YAKOS_LIB/compat.sh" ]; then
    # shellcheck source=../../cli/lib/compat.sh
    . "$YAKOS_LIB/compat.sh"
else
    # Minimal inline fallback so the hook can still emit diagnostics
    ct_log() { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
fi

hi_init

# ---- emergency disable --------------------------------------------------------
if [ "${YAKOS_PLAN_QUALITY_DISABLE:-0}" = "1" ]; then
    exit 0
fi

# ---- route on event type ------------------------------------------------------
tool="$(hi_tool)"
event="$(hi_event)"

# ============================================================
# PostToolUse path: score on plan.md write
# ============================================================
if [ "$event" = "PostToolUse" ]; then
    case "$tool" in
        Edit|Write|MultiEdit) ;;
        *) exit 0 ;;
    esac

    # Check whether this write targets work/current/plan.md
    file_path="$(hi_file_path)"
    case "$file_path" in
        */work/current/plan.md) ;;
        *) exit 0 ;;
    esac

    # Resolved project + work dirs
    project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
    current_dir="$(yakos_current_dir)"
    yakos_yml="$project_dir/.yakos.yml"

    # ---- read plan_quality config from .yakos.yml ----------------------------
    # Defaults
    PQ_ENABLED="true"
    PQ_MODE="surface"
    PQ_THRESHOLD="0.75"
    PQ_COST_CEILING="${YAKOS_PLAN_EVAL_MAX_COST_USD:-0.15}"

    if [ -f "$yakos_yml" ]; then
        # Parse plan_quality: block using awk (avoids complex quoting in bash)
        _pq_raw="$(awk '
            /^[[:space:]]*plan_quality[[:space:]]*:/ { in_block=1; next }
            in_block && /^[^[:space:]]/ && !/^[[:space:]]/ { exit }
            in_block && /^[[:space:]]/ {
                line = $0
                # strip leading whitespace
                sub(/^[[:space:]]+/, "", line)
                # strip trailing whitespace
                sub(/[[:space:]]+$/, "", line)
                # strip inline comments
                sub(/[[:space:]]+#.*$/, "", line)
                if (line ~ /^enabled[[:space:]]*:/) {
                    sub(/^enabled[[:space:]]*:[[:space:]]*/, "", line)
                    gsub(/["'"'"']/, "", line)
                    print "enabled=" line
                } else if (line ~ /^mode[[:space:]]*:/) {
                    sub(/^mode[[:space:]]*:[[:space:]]*/, "", line)
                    gsub(/["'"'"']/, "", line)
                    print "mode=" line
                } else if (line ~ /^threshold[[:space:]]*:/) {
                    sub(/^threshold[[:space:]]*:[[:space:]]*/, "", line)
                    gsub(/["'"'"']/, "", line)
                    print "threshold=" line
                } else if (line ~ /^cost_ceiling_usd[[:space:]]*:/) {
                    sub(/^cost_ceiling_usd[[:space:]]*:[[:space:]]*/, "", line)
                    gsub(/["'"'"']/, "", line)
                    print "cost_ceiling_usd=" line
                }
            }
        ' "$yakos_yml" 2>/dev/null || true)"

        # Apply parsed values
        while IFS='=' read -r _k _v; do
            case "$_k" in
                enabled)         [ -n "$_v" ] && PQ_ENABLED="$_v" ;;
                mode)            [ -n "$_v" ] && PQ_MODE="$_v" ;;
                threshold)       [ -n "$_v" ] && PQ_THRESHOLD="$_v" ;;
                cost_ceiling_usd) [ -n "$_v" ] && PQ_COST_CEILING="$_v" ;;
            esac
        done <<EOF
$_pq_raw
EOF
    fi

    # ---- check enabled -------------------------------------------------------
    if [ "$PQ_ENABLED" = "false" ]; then
        ho_log "plan-quality-score" "REPORT" "pass" \
            "plan_quality.enabled=false; skipping" \
            "$(jq -nc --arg f "$file_path" '{file_path: $f}')"
        exit 0
    fi

    # ---- debounce: skip if plan.md mtime changed within last 5 s ------------
    # Uses stat with macOS (-f %m) and Linux (-c %Y) compat
    plan_file="$file_path"
    mtime1=0
    # Portable mtime
    if stat -f "%m" "$plan_file" >/dev/null 2>&1; then
        mtime1="$(stat -f "%m" "$plan_file" 2>/dev/null || echo 0)"
    elif stat -c "%Y" "$plan_file" >/dev/null 2>&1; then
        mtime1="$(stat -c "%Y" "$plan_file" 2>/dev/null || echo 0)"
    fi
    now_s="$(date -u +%s 2>/dev/null || echo 0)"
    age_s=$((now_s - mtime1))
    if [ "$age_s" -lt 5 ] && [ "$age_s" -ge 0 ]; then
        ho_log "plan-quality-score" "REPORT" "pass" \
            "debounced: plan.md mtime age=${age_s}s < 5s; skipping this fire" \
            "$(jq -nc --arg f "$file_path" --argjson age "$age_s" '{file_path: $f, age_s: $age}')"
        exit 0
    fi

    # ---- verify skill script exists ------------------------------------------
    SKILL_SCORE="$YAKOS_ROOT/lib/skills/plan-quality-eval/scripts/score-plan.sh"
    if [ ! -f "$SKILL_SCORE" ]; then
        ct_log "WARN: plan-quality-score: score-plan.sh not found at $SKILL_SCORE; passing"
        ho_log "plan-quality-score" "WARN" "pass" \
            "score-plan.sh not found; skipping scoring" \
            "$(jq -nc --arg p "$SKILL_SCORE" '{script_path: $p}')"
        exit 0
    fi

    # ---- invoke score-plan.sh -----------------------------------------------
    tmp_home="$(mktemp -d -t yakos-pqscore.XXXXXX 2>/dev/null || mktemp -d /tmp/yakos-pqscore.XXXXXX)"
    mkdir -p "$tmp_home/.yakos-state"
    trap 'rm -rf "$tmp_home" 2>/dev/null || true' EXIT

    score_rc=0
    HOME="$tmp_home" \
    YAKOS_PLAN_EVAL_MAX_COST_USD="$PQ_COST_CEILING" \
    YAKOS_ROOT="$YAKOS_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
        bash "$SKILL_SCORE" "$plan_file" >/dev/null 2>&1 || score_rc=$?

    # score-plan.sh exit codes: 0=pass, 1=fail/dissent, 2=extract error,
    # 3=cost exceeded, 4=judge failure.
    if [ "$score_rc" -eq 2 ] || [ "$score_rc" -eq 3 ] || [ "$score_rc" -eq 4 ]; then
        ct_log "WARN: plan-quality-score: score-plan.sh exited $score_rc (infra error); passing"
        ho_log "plan-quality-score" "WARN" "pass" \
            "score-plan.sh exited $score_rc (infra error); no gate action" \
            "$(jq -nc --argjson rc "$score_rc" '{score_rc: $rc}')"
        exit 0
    fi

    # ---- read the log record -------------------------------------------------
    log_file="$tmp_home/.yakos-state/plan-quality-log.ndjson"
    if [ ! -f "$log_file" ] || [ ! -s "$log_file" ]; then
        ct_log "WARN: plan-quality-score: log record not found after scoring; passing"
        ho_log "plan-quality-score" "WARN" "pass" \
            "no log record after scoring; no gate action" "{}"
        exit 0
    fi

    RECORD="$(tail -1 "$log_file" 2>/dev/null || true)"
    if [ -z "$RECORD" ] || ! printf '%s' "$RECORD" | jq empty 2>/dev/null; then
        ct_log "WARN: plan-quality-score: invalid log record JSON; passing"
        ho_log "plan-quality-score" "WARN" "pass" \
            "invalid log record JSON; no gate action" "{}"
        exit 0
    fi

    # Extract key fields
    AGGREGATE="$(printf '%s' "$RECORD" | jq -r '.aggregate_score // 0')"
    DISSENT="$(printf '%s' "$RECORD" | jq -r '.dissent // false')"
    PLAN_ID="$(printf '%s' "$RECORD" | jq -r '.plan_id // "unknown"')"
    # VERDICT extracted but not used in gate logic (aggregate+dissent determine outcome)."

    # Also copy the record to the real state log for persistence
    real_log="$HOME/.yakos-state/plan-quality-log.ndjson"
    mkdir -p "$(dirname "$real_log")" 2>/dev/null || true
    printf '%s\n' "$RECORD" >> "$real_log" 2>/dev/null || true

    # ---- decision tree -------------------------------------------------------
    # Check: aggregate >= threshold AND dissent=false → PASS
    above_threshold="$(awk -v agg="$AGGREGATE" -v thr="$PQ_THRESHOLD" \
        'BEGIN { print (agg + 0 >= thr + 0) ? "yes" : "no" }')"

    # Build a human-readable summary
    summary_lines="$(printf '%s' "$RECORD" | jq -r '
        "plan_id:          " + .plan_id,
        "aggregate_score:  " + (.aggregate_score | tostring),
        "threshold:        " + (.threshold | tostring),
        "verdict:          " + .verdict,
        "dissent:          " + (.dissent | tostring),
        "panel_size:       " + (.panel_size | tostring)
    ' 2>/dev/null || true)"

    # ---- dissent path (takes priority over threshold path) ------------------
    if [ "$DISSENT" = "true" ]; then
        # Always surface on dissent, never block
        notes_dir="$current_dir/notes"
        mkdir -p "$notes_dir" 2>/dev/null || true
        notes_file="$notes_dir/plan-quality-${PLAN_ID}.md"

        {
            printf '# Plan Quality Surface — %s\n\n' "$PLAN_ID"
            printf '**Reason:** Judge panel dissent (max-min spread >= 0.5 on at least one dimension).\n\n'
            printf '## Score summary\n\n'
            printf '```\n%s\n```\n\n' "$summary_lines"
            printf '## Full report\n\n'
            # Emit the full report from the real log
            bash "$YAKOS_ROOT/lib/skills/plan-quality-eval/scripts/report-plan-score.sh" \
                "$real_log" 2>/dev/null || printf '(report unavailable)\n'
        } > "$notes_file" 2>/dev/null || true

        ho_log "plan-quality-score" "WARN" "surface_to_operator" \
            "plan-quality-score: dissent detected; surfaced to operator" \
            "$(jq -nc \
                --arg pid "$PLAN_ID" \
                --arg agg "$AGGREGATE" \
                --arg notes "$notes_file" \
                '{plan_id: $pid, aggregate_score: $agg, decision: "surface_to_operator", notes_file: $notes}')"
        exit 0
    fi

    # ---- threshold pass path ------------------------------------------------
    if [ "$above_threshold" = "yes" ]; then
        ho_log "plan-quality-score" "REPORT" "pass" \
            "plan-quality-score: aggregate=${AGGREGATE} >= threshold=${PQ_THRESHOLD}; PASS" \
            "$(jq -nc \
                --arg pid "$PLAN_ID" \
                --arg agg "$AGGREGATE" \
                --arg thr "$PQ_THRESHOLD" \
                '{plan_id: $pid, aggregate_score: $agg, threshold: $thr, decision: "pass"}')"
        exit 0
    fi

    # ---- below threshold path -----------------------------------------------
    # Build notes file regardless of mode
    notes_dir="$current_dir/notes"
    mkdir -p "$notes_dir" 2>/dev/null || true
    notes_file="$notes_dir/plan-quality-${PLAN_ID}.md"

    {
        printf '# Plan Quality Surface — %s\n\n' "$PLAN_ID"
        printf '**Reason:** Aggregate score %.4f is below threshold %s.\n\n' \
            "$(printf '%s' "$AGGREGATE" | awk '{printf "%.4f", $1}')" "$PQ_THRESHOLD"
        printf '## Score summary\n\n'
        printf '```\n%s\n```\n\n' "$summary_lines"
        printf '## Full report\n\n'
        bash "$YAKOS_ROOT/lib/skills/plan-quality-eval/scripts/report-plan-score.sh" \
            "$real_log" 2>/dev/null || printf '(report unavailable)\n'
    } > "$notes_file" 2>/dev/null || true

    case "$PQ_MODE" in
        block)
            # Write .plan-blocked marker
            blocked_marker="$current_dir/.plan-blocked"
            jq -nc \
                --arg plan_id "$PLAN_ID" \
                --arg agg "$AGGREGATE" \
                --arg thr "$PQ_THRESHOLD" \
                --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
                '{plan_id: $plan_id,
                  aggregate_score: $agg,
                  threshold: $thr,
                  ts: $ts,
                  reason: ("aggregate " + $agg + " < threshold " + $thr + "; plan quality below bar")}' \
                > "$blocked_marker" 2>/dev/null || true

            ho_log "plan-quality-score" "WARN" "block_next_tool" \
                "plan-quality-score: aggregate=${AGGREGATE} < threshold=${PQ_THRESHOLD}; mode=block; .plan-blocked written" \
                "$(jq -nc \
                    --arg pid "$PLAN_ID" \
                    --arg agg "$AGGREGATE" \
                    --arg thr "$PQ_THRESHOLD" \
                    --arg marker "$blocked_marker" \
                    --arg notes "$notes_file" \
                    '{plan_id: $pid, aggregate_score: $agg, threshold: $thr, decision: "block_next_tool", marker: $marker, notes_file: $notes}')"
            ;;

        surface|*)
            # surface mode: write notes file, log SURFACE, do not block
            ho_log "plan-quality-score" "WARN" "surface_to_operator" \
                "plan-quality-score: aggregate=${AGGREGATE} < threshold=${PQ_THRESHOLD}; mode=surface; surfaced" \
                "$(jq -nc \
                    --arg pid "$PLAN_ID" \
                    --arg agg "$AGGREGATE" \
                    --arg thr "$PQ_THRESHOLD" \
                    --arg notes "$notes_file" \
                    '{plan_id: $pid, aggregate_score: $agg, threshold: $thr, decision: "surface_to_operator", notes_file: $notes}')"
            ;;
    esac

    exit 0
fi

# ---- unexpected event (no-op) -----------------------------------------------
exit 0
