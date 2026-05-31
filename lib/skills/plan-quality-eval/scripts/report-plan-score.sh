#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# report-plan-score.sh — render the most recent plan_scored record from
# ~/.yakos-state/plan-quality-log.ndjson as a human-readable markdown report.
#
# Usage:
#   bash report-plan-score.sh [<log-file>]
#
# If <log-file> is omitted, defaults to ~/.yakos-state/plan-quality-log.ndjson.
# Reads the last record in the file (most recent scoring run).
# Emits markdown to stdout.

set -eu

LOG_FILE="${1:-$HOME/.yakos-state/plan-quality-log.ndjson}"

if [ ! -f "$LOG_FILE" ]; then
    echo "# Plan Quality Report" >&2
    echo "" >&2
    echo "No log file found at $LOG_FILE" >&2
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "report-plan-score.sh: jq is required" >&2
    exit 1
fi

# Read the last plan_scored record.
# The log is NDJSON (one compact JSON object per line). grep for the type field
# whether written with or without a space after the colon.
RECORD="$(grep '"type"' "$LOG_FILE" 2>/dev/null | grep '"plan_scored"' | tail -1 || true)"
if [ -z "$RECORD" ]; then
    # Fallback: last non-empty line of the file
    RECORD="$(grep -v '^[[:space:]]*$' "$LOG_FILE" 2>/dev/null | tail -1 || true)"
fi

if [ -z "$RECORD" ] || ! printf '%s' "$RECORD" | jq empty 2>/dev/null; then
    echo "report-plan-score.sh: no valid record found in $LOG_FILE" >&2
    exit 1
fi

# Extract fields
PLAN_ID="$(printf '%s' "$RECORD" | jq -r '.plan_id')"
PLAN_PATH="$(printf '%s' "$RECORD" | jq -r '.plan_path')"
TS="$(printf '%s' "$RECORD" | jq -r '.ts')"
AGGREGATE="$(printf '%s' "$RECORD" | jq -r '.aggregate_score')"
THRESHOLD="$(printf '%s' "$RECORD" | jq -r '.threshold')"
VERDICT="$(printf '%s' "$RECORD" | jq -r '.verdict')"
RECOMMENDED="$(printf '%s' "$RECORD" | jq -r '.recommended_action')"
DISSENT="$(printf '%s' "$RECORD" | jq -r '.dissent')"
PANEL_SIZE="$(printf '%s' "$RECORD" | jq -r '.panel_size')"
DROPPED_FAMILY="$(printf '%s' "$RECORD" | jq -r '.dropped_family')"
PLANNER_MODEL="$(printf '%s' "$RECORD" | jq -r '.planner_model')"
RUBRIC_HASH="$(printf '%s' "$RECORD" | jq -r '.rubric_hash')"
PLAN_HASH="$(printf '%s' "$RECORD" | jq -r '.plan_hash // "n/a"')"
DURATION="$(printf '%s' "$RECORD" | jq -r '.duration_s')"
COST="$(printf '%s' "$RECORD" | jq -r '.cost_usd')"
TASK_COUNT="$(printf '%s' "$RECORD" | jq -r '.task_count')"
ASSUMPTION_COUNT="$(printf '%s' "$RECORD" | jq -r '.assumption_count')"
RISK_COUNT="$(printf '%s' "$RECORD" | jq -r '.risk_count')"

# Verdict emoji-free status marker
case "$VERDICT" in
    pass) VERDICT_MARK="PASS" ;;
    warn) VERDICT_MARK="WARN (dissent)" ;;
    fail) VERDICT_MARK="FAIL" ;;
    *)    VERDICT_MARK="$VERDICT" ;;
esac

# Per-dimension weights (from the record or hardcoded fallback)
declare -A DIM_WEIGHTS
DIM_WEIGHTS["acceptance_criteria_specificity"]="0.25"
DIM_WEIGHTS["assumption_surfacing"]="0.15"
DIM_WEIGHTS["decomposition_granularity"]="0.20"
DIM_WEIGHTS["dependency_clarity"]="0.15"
DIM_WEIGHTS["domain_boundaries_respected"]="0.15"
DIM_WEIGHTS["risk_rollback_honesty"]="0.10"

DIMENSIONS=(
    "acceptance_criteria_specificity"
    "assumption_surfacing"
    "decomposition_granularity"
    "dependency_clarity"
    "domain_boundaries_respected"
    "risk_rollback_honesty"
)

# ---- emit the report --------------------------------------------------------
cat <<HEADER
# Plan Quality Report

**Plan ID:** ${PLAN_ID}
**Plan file:** ${PLAN_PATH}
**Scored at:** ${TS}
**Verdict:** ${VERDICT_MARK}
**Aggregate score:** ${AGGREGATE} / 1.0 (threshold: ${THRESHOLD})
**Recommended action:** ${RECOMMENDED}

---

## Pin block

| Field | Value |
|---|---|
| plan_hash | \`${PLAN_HASH:0:16}...\` |
| rubric_hash | \`${RUBRIC_HASH}\` |
| planner_model | ${PLANNER_MODEL} |
| panel_size | ${PANEL_SIZE} |
| dropped_family | ${DROPPED_FAMILY} |
| dissent | ${DISSENT} |
| duration_s | ${DURATION} |
| cost_usd (est) | ${COST} |

---

## Per-dimension breakdown

| Dimension | Weight | Median score | Status |
|---|---|---|---|
HEADER

for dim in "${DIMENSIONS[@]}"; do
    median="$(printf '%s' "$RECORD" | jq -r --arg d "$dim" '.per_dimension_median[$d] // 0')"
    weight="${DIM_WEIGHTS[$dim]:-0}"
    # Status label
    status=""
    result="$(awk -v m="$median" 'BEGIN { if (m >= 1.0) print "pass"; else if (m >= 0.5) print "partial"; else print "fail" }')"
    case "$result" in
        pass)    status="pass" ;;
        partial) status="partial" ;;
        fail)    status="FAIL" ;;
    esac
    printf '| %-42s | %-6s | %-12s | %-7s |\n' "$dim" "$weight" "$median" "$status"
done

echo ""
echo "---"
echo ""
echo "## Judge panel"
echo ""

# Emit per-judge scores
printf '%s' "$RECORD" | jq -r '
    .panel[] |
    "### Judge: \(.model_id)\n" +
    "Notes: \(.notes)\n\n" +
    "| Dimension | Score |\n|---|---|\n" +
    (
        .scores | to_entries[] |
        "| \(.key) | \(.value) |"
    )
' 2>/dev/null || printf '%s' "$RECORD" | jq -c '.panel[]' | while IFS= read -r judge; do
    mid="$(printf '%s' "$judge" | jq -r '.model_id')"
    notes="$(printf '%s' "$judge" | jq -r '.notes')"
    echo "### Judge: $mid"
    echo "Notes: $notes"
    echo ""
    echo "| Dimension | Score |"
    echo "|---|---|"
    for dim in "${DIMENSIONS[@]}"; do
        s="$(printf '%s' "$judge" | jq -r --arg d "$dim" '.scores[$d] // 0')"
        echo "| $dim | $s |"
    done
    echo ""
done

# Dissent warning
if [ "$DISSENT" = "true" ]; then
    echo "---"
    echo ""
    echo "## Dissent warning"
    echo ""
    echo "At least one dimension shows max-min spread >= 0.5 across judges."
    echo "Recommended action: surface to operator before dispatching specialists."
    echo ""
fi

# Plan structure summary
echo "---"
echo ""
echo "## Plan structure"
echo ""
echo "- Tasks: ${TASK_COUNT}"
echo "- Assumptions: ${ASSUMPTION_COUNT}"
echo "- Risks: ${RISK_COUNT}"
