#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# score-plan.sh — orchestrate a 3-judge cross-vendor LLM panel to score
# a plan.md file against the default rubric.
#
# Usage:
#   bash score-plan.sh <plan.md> [--rubric <path>] [--planner-model <id>]
#                                [--dry-run]
#
# Environment:
#   YAKOS_PLAN_JUDGE_MOCK=<dir>   Use canned JSON verdicts instead of
#                                  dispatching real judge agents. Each
#                                  file: <dir>/claude.json, codex.json,
#                                  gemini.json. See SKILL.md for schema.
#   YAKOS_PLANNER_MODEL=<id>      The model that produced this plan.
#                                  Used for family-drop logic. If unset,
#                                  family-drop is skipped.
#   YAKOS_PLAN_EVAL_MAX_COST_USD  Cost ceiling (default 0.15). Hard-fail
#                                  if estimated cost exceeds this.
#   YAKOS_ROOT                    Framework root (auto-detected if unset).
#
# Exits:
#   0 = pass (aggregate >= threshold)
#   1 = fail (aggregate < threshold) or dissent flagged
#   2 = plan extraction error
#   3 = cost ceiling exceeded
#   4 = judge dispatch failed

set -eu

# ---- resolve YAKOS_ROOT ------------------------------------------------------
if [ -z "${YAKOS_ROOT:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname -- "${BASH_SOURCE[0]:-$0}")" && pwd -P)"
    YAKOS_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd -P)"
fi
YAKOS_LIB="${YAKOS_LIB:-$YAKOS_ROOT/cli/lib}"
SKILL_DIR="$YAKOS_ROOT/lib/skills/plan-quality-eval"

# shellcheck source=../../../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"

# ---- args -------------------------------------------------------------------
PLAN_FILE=""
RUBRIC_FILE="$SKILL_DIR/rubrics/default.yaml"
PLANNER_MODEL="${YAKOS_PLANNER_MODEL:-}"
DRY_RUN=0
MAX_COST_USD="${YAKOS_PLAN_EVAL_MAX_COST_USD:-0.15}"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --rubric)       shift; RUBRIC_FILE="$1" ;;
        --rubric=*)     RUBRIC_FILE="${1#--rubric=}" ;;
        --planner-model) shift; PLANNER_MODEL="$1" ;;
        --planner-model=*) PLANNER_MODEL="${1#--planner-model=}" ;;
        --dry-run)      DRY_RUN=1 ;;
        --*)            ct_die "score-plan.sh: unknown flag '$1'" ;;
        *)
            if [ -z "$PLAN_FILE" ]; then
                PLAN_FILE="$1"
            else
                ct_die "score-plan.sh: too many positional args"
            fi
            ;;
    esac
    shift
done

[ -n "$PLAN_FILE" ] || { echo "Usage: score-plan.sh <plan.md> [flags]" >&2; exit 1; }
[ -f "$PLAN_FILE" ] || ct_die "score-plan.sh: plan file not found: $PLAN_FILE"
[ -f "$RUBRIC_FILE" ] || ct_die "score-plan.sh: rubric file not found: $RUBRIC_FILE"

# ---- timestamps + hashes ----------------------------------------------------
TS_START="$(date -u +%s 2>/dev/null || echo 0)"
TS_ISO="$(ct_iso_now_z)"

# Hash the rubric (first 12 hex chars)
RUBRIC_HASH=""
if command -v shasum >/dev/null 2>&1; then
    RUBRIC_HASH="$(shasum -a 256 "$RUBRIC_FILE" | awk '{print substr($1,1,12)}')"
elif command -v sha256sum >/dev/null 2>&1; then
    RUBRIC_HASH="$(sha256sum "$RUBRIC_FILE" | awk '{print substr($1,1,12)}')"
elif command -v openssl >/dev/null 2>&1; then
    RUBRIC_HASH="$(openssl dgst -sha256 "$RUBRIC_FILE" | awk '{h=$NF; print substr(h,1,12)}')"
fi

# Hash the plan file
PLAN_HASH=""
if command -v shasum >/dev/null 2>&1; then
    PLAN_HASH="$(shasum -a 256 "$PLAN_FILE" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
    PLAN_HASH="$(sha256sum "$PLAN_FILE" | awk '{print $1}')"
elif command -v openssl >/dev/null 2>&1; then
    PLAN_HASH="$(openssl dgst -sha256 "$PLAN_FILE" | awk '{print $NF}')"
fi

# ---- extract canonical plan JSON --------------------------------------------
ct_log "score-plan: extracting plan from $PLAN_FILE"
EXTRACT_SCRIPT="$SKILL_DIR/scripts/extract-plan.sh"

PLAN_JSON=""
if ! PLAN_JSON="$(bash "$EXTRACT_SCRIPT" "$PLAN_FILE" 2>&1)"; then
    ct_log "ERROR: extract-plan.sh failed:"
    printf '%s\n' "$PLAN_JSON" >&2
    exit 2
fi
# Validate it's valid JSON
if ! printf '%s' "$PLAN_JSON" | jq empty 2>/dev/null; then
    ct_log "ERROR: extract-plan.sh produced invalid JSON"
    exit 2
fi

PLAN_ID="$(printf '%s' "$PLAN_JSON" | jq -r '.plan_id')"
TASK_COUNT="$(printf '%s' "$PLAN_JSON" | jq '.tasks | length')"
ASSUMPTION_COUNT="$(printf '%s' "$PLAN_JSON" | jq '.assumptions | length')"
RISK_COUNT="$(printf '%s' "$PLAN_JSON" | jq '.risks | length')"

ct_log "score-plan: plan_id=$PLAN_ID tasks=$TASK_COUNT assumptions=$ASSUMPTION_COUNT risks=$RISK_COUNT"

# ---- cost estimate (tokens × per-model rate) --------------------------------
PLAN_CHARS="$(wc -c < "$PLAN_FILE" | tr -d ' ')"
RUBRIC_CHARS="$(wc -c < "$RUBRIC_FILE" | tr -d ' ')"
# Rough estimate: ~4 chars/token; haiku=$0.00025/1k in, gpt-5-nano=$0.0001/1k in
# We'll be conservative and use $0.001/1k tokens per judge (covers mixed families)
TOTAL_CHARS=$((PLAN_CHARS + RUBRIC_CHARS))
TOKENS_EST=$((TOTAL_CHARS / 4))
# Three judges × input + small output
COST_EST="$(awk -v t="$TOKENS_EST" 'BEGIN { printf "%.4f", (t * 3 * 0.001) / 1000 }')"
MAX_OK="$(awk -v c="$COST_EST" -v m="$MAX_COST_USD" 'BEGIN { print (c <= m) ? "ok" : "over" }')"

if [ "$MAX_OK" != "ok" ]; then
    ct_log "ERROR: estimated cost \$$COST_EST exceeds ceiling \$$MAX_COST_USD"
    ct_log "       Plan is ${PLAN_CHARS} chars + rubric ${RUBRIC_CHARS} chars = ~${TOKENS_EST} tokens"
    ct_log "       Override: YAKOS_PLAN_EVAL_MAX_COST_USD=<value>"
    exit 3
fi

ct_log "score-plan: estimated cost \$$COST_EST (ceiling \$$MAX_COST_USD) — proceeding"

# ---- read rubric text for prompt injection ----------------------------------
RUBRIC_TEXT="$(cat "$RUBRIC_FILE")"
PLAN_TEXT="$(cat "$PLAN_FILE")"

# ---- dispatch judges --------------------------------------------------------
TMPDIR_JUDGES="$(mktemp -d -t yakos-plan-judge.XXXXXX)"
trap 'rm -rf "$TMPDIR_JUDGES" 2>/dev/null || true' EXIT

JUDGE_PROMPT="$(cat <<PROMPT
You are a plan quality judge. Score the following plan against the rubric.

## Plan to score

\`\`\`markdown
${PLAN_TEXT}
\`\`\`

## Rubric

\`\`\`yaml
${RUBRIC_TEXT}
\`\`\`

## Required output

Emit exactly one valid JSON object on stdout — no other text, no markdown
fences. Use this schema:

{
  "model_id": "<concrete model id you are running on>",
  "scores": {
    "acceptance_criteria_specificity": 0 or 0.5 or 1.0,
    "assumption_surfacing": 0 or 0.5 or 1.0,
    "decomposition_granularity": 0 or 0.5 or 1.0,
    "dependency_clarity": 0 or 0.5 or 1.0,
    "domain_boundaries_respected": 0 or 0.5 or 1.0,
    "risk_rollback_honesty": 0 or 0.5 or 1.0
  },
  "notes": "<1-3 sentence summary of the most significant finding>"
}

Valid score values: 0, 0.5, 1.0 only. Score each dimension independently.
PROMPT
)"

dispatch_judge() {
    local vendor="$1"   # claude | codex | gemini
    local out_file="$2"

    # Mock path — substitute canned output if env var is set
    local mock_dir="${YAKOS_PLAN_JUDGE_MOCK:-}"
    if [ -n "$mock_dir" ]; then
        local mock_file="$mock_dir/${vendor}.json"
        if [ -f "$mock_file" ]; then
            ct_log "score-plan: [MOCK] using $mock_file for $vendor judge"
            cp "$mock_file" "$out_file"
            return 0
        else
            ct_log "WARN: mock dir set but $mock_file not found; falling back to real dispatch"
        fi
    fi

    if [ "$DRY_RUN" = "1" ]; then
        ct_log "score-plan: [DRY RUN] would dispatch $vendor judge"
        # Emit a neutral verdict for dry-run
        jq -n \
            --arg mid "$vendor-dry-run" \
            '{model_id: $mid, scores: {
                acceptance_criteria_specificity: 0.5,
                assumption_surfacing: 0.5,
                decomposition_granularity: 0.5,
                dependency_clarity: 0.5,
                domain_boundaries_respected: 0.5,
                risk_rollback_honesty: 0.5
            }, notes: "dry-run verdict"}' > "$out_file"
        return 0
    fi

    # Real dispatch via yakos dispatch
    local yakos_bin="$YAKOS_ROOT/cli/yakos"
    local judge_agent="plan-judge"
    local runtime_flag=""

    case "$vendor" in
        claude)  runtime_flag="claude" ;;
        codex)   runtime_flag="codex" ;;
        gemini)  runtime_flag="gemini" ;;
    esac

    local project_path="${PROJECT:-$(pwd)}"

    # Dispatch with timeout; capture JSON verdict
    local tmp_out="$TMPDIR_JUDGES/${vendor}_raw.txt"
    local rc=0

    if command -v "$yakos_bin" >/dev/null 2>&1 || [ -f "$yakos_bin" ]; then
        ct_timeout 120 bash "$yakos_bin" dispatch "$judge_agent" \
            "$JUDGE_PROMPT" \
            --runtime "$runtime_flag" \
            --project "$project_path" \
            --timeout 90 > "$tmp_out" 2>/dev/null || rc=$?
    else
        ct_log "WARN: yakos CLI not found at $yakos_bin; treating vendor $vendor as failed"
        rc=1
    fi

    if [ "$rc" -ne 0 ]; then
        ct_log "WARN: $vendor judge dispatch exited $rc"
        # Emit a zero-score verdict so the panel can still compute (with a note)
        jq -n --arg mid "$vendor-failed" \
            '{model_id: $mid, scores: {
                acceptance_criteria_specificity: 0,
                assumption_surfacing: 0,
                decomposition_granularity: 0,
                dependency_clarity: 0,
                domain_boundaries_respected: 0,
                risk_rollback_honesty: 0
            }, notes: "judge dispatch failed — scores zeroed"}' > "$out_file"
        return 1
    fi

    # Extract the JSON object from the raw output (may have leading prose)
    if jq -e . "$tmp_out" > "$out_file" 2>/dev/null; then
        return 0
    fi
    # Try to extract JSON from within the output
    if grep -o '{.*}' "$tmp_out" | jq . > "$out_file" 2>/dev/null; then
        return 0
    fi

    ct_log "WARN: $vendor judge output was not valid JSON"
    jq -n --arg mid "$vendor-parse-error" \
        '{model_id: $mid, scores: {
            acceptance_criteria_specificity: 0,
            assumption_surfacing: 0,
            decomposition_granularity: 0,
            dependency_clarity: 0,
            domain_boundaries_respected: 0,
            risk_rollback_honesty: 0
        }, notes: "judge output could not be parsed as JSON"}' > "$out_file"
    return 1
}

# Dispatch all three judges in parallel
ct_log "score-plan: dispatching 3-judge panel in parallel"
CLAUDE_OUT="$TMPDIR_JUDGES/claude.json"
CODEX_OUT="$TMPDIR_JUDGES/codex.json"
GEMINI_OUT="$TMPDIR_JUDGES/gemini.json"

dispatch_judge claude "$CLAUDE_OUT" &
CLAUDE_PID=$!
dispatch_judge codex  "$CODEX_OUT"  &
CODEX_PID=$!
dispatch_judge gemini "$GEMINI_OUT" &
GEMINI_PID=$!

# Wait for all three (collect failures)
JUDGE_FAIL=0
wait "$CLAUDE_PID" || JUDGE_FAIL=$((JUDGE_FAIL + 1))
wait "$CODEX_PID"  || JUDGE_FAIL=$((JUDGE_FAIL + 1))
wait "$GEMINI_PID" || JUDGE_FAIL=$((JUDGE_FAIL + 1))

# If all three failed (and not mocked), bail
if [ "$JUDGE_FAIL" -ge 3 ] && [ -z "${YAKOS_PLAN_JUDGE_MOCK:-}" ] && [ "$DRY_RUN" = "0" ]; then
    ct_log "ERROR: all 3 judge dispatches failed"
    exit 4
fi

# ---- validate and load verdicts --------------------------------------------
load_verdict() {
    local file="$1" vendor="$2"
    if [ ! -f "$file" ] || ! jq -e '.scores' "$file" >/dev/null 2>&1; then
        ct_log "WARN: $vendor verdict missing or invalid; using zero scores"
        jq -n --arg mid "$vendor-missing" \
            '{model_id: $mid, scores: {
                acceptance_criteria_specificity: 0,
                assumption_surfacing: 0,
                decomposition_granularity: 0,
                dependency_clarity: 0,
                domain_boundaries_respected: 0,
                risk_rollback_honesty: 0
            }, notes: "verdict missing"}' > "$file"
    fi
    cat "$file"
}

CLAUDE_VERDICT="$(load_verdict "$CLAUDE_OUT" claude)"
CODEX_VERDICT="$(load_verdict "$CODEX_OUT"  codex)"
GEMINI_VERDICT="$(load_verdict "$GEMINI_OUT" gemini)"

# ---- family-drop ------------------------------------------------------------
# Determine which family the planner model belongs to.
planner_family() {
    local m="$1"
    case "$m" in
        claude*|haiku*|sonnet*|opus*) echo "claude" ;;
        gpt*|o1*|o3*|o4*|codex*)      echo "codex" ;;
        gemini*)                        echo "gemini" ;;
        *)                              echo "unknown" ;;
    esac
}

PANEL_VERDICTS=("$CLAUDE_VERDICT" "$CODEX_VERDICT" "$GEMINI_VERDICT")
PANEL_VENDORS=("claude" "codex" "gemini")
DROPPED_FAMILY="null"
PANEL_SIZE=3

if [ -n "$PLANNER_MODEL" ]; then
    PLANNER_FAM="$(planner_family "$PLANNER_MODEL")"
    if [ "$PLANNER_FAM" != "unknown" ]; then
        # Find and drop the matching judge
        NEW_VERDICTS=()
        NEW_VENDORS=()
        for i in 0 1 2; do
            v="${PANEL_VENDORS[$i]}"
            fam="$(planner_family "$v")"
            if [ "$fam" = "$PLANNER_FAM" ] && [ "$DROPPED_FAMILY" = "null" ]; then
                ct_log "score-plan: dropping $v judge (same family as planner model $PLANNER_MODEL)"
                DROPPED_FAMILY="\"$PLANNER_FAM\""
            else
                NEW_VERDICTS+=("${PANEL_VERDICTS[$i]}")
                NEW_VENDORS+=("$v")
            fi
        done
        PANEL_VERDICTS=("${NEW_VERDICTS[@]}")
        PANEL_VENDORS=("${NEW_VENDORS[@]}")
        PANEL_SIZE="${#PANEL_VERDICTS[@]}"
    fi
fi

ct_log "score-plan: panel_size=$PANEL_SIZE dropped_family=$DROPPED_FAMILY"

# ---- compute per-dimension medians + weighted aggregate --------------------
# We use Python3 for median computation (bash can't do float arithmetic cleanly).

DIMENSIONS=(
    "acceptance_criteria_specificity"
    "assumption_surfacing"
    "decomposition_granularity"
    "dependency_clarity"
    "domain_boundaries_respected"
    "risk_rollback_honesty"
)

# Build panel JSON array
PANEL_JSON_ARRAY="["
first=1
for verdict in "${PANEL_VERDICTS[@]}"; do
    [ "$first" = "1" ] || PANEL_JSON_ARRAY="${PANEL_JSON_ARRAY},"
    PANEL_JSON_ARRAY="${PANEL_JSON_ARRAY}${verdict}"
    first=0
done
PANEL_JSON_ARRAY="${PANEL_JSON_ARRAY}]"

SCORING_OUTPUT="$(python3 - "$PANEL_JSON_ARRAY" "$RUBRIC_FILE" <<'PYEOF'
import sys
import json

panel_json = sys.argv[1]
rubric_file = sys.argv[2]

def _parse_rubric_fallback(rubric_file):
    """Line-by-line weight extractor — no yaml dependency."""
    rubric = {"dimensions": [], "pass_threshold": 0.75}
    current_dim = None
    with open(rubric_file) as f:
        for line in f:
            line = line.rstrip()
            if line.startswith("  - name:"):
                name = line.split(":", 1)[1].strip()
                current_dim = {"name": name, "weight": 0.0}
                rubric["dimensions"].append(current_dim)
            elif current_dim and line.strip().startswith("weight:"):
                try:
                    w = float(line.split(":", 1)[1].strip())
                    current_dim["weight"] = w
                except ValueError:
                    pass
            elif line.startswith("pass_threshold:"):
                try:
                    rubric["pass_threshold"] = float(line.split(":", 1)[1].strip())
                except ValueError:
                    pass
    return rubric

try:
    import yaml
    with open(rubric_file) as f:
        rubric = yaml.safe_load(f)
    # yaml may be installed but rubric may be malformed; validate minimally
    if not isinstance(rubric, dict) or "dimensions" not in rubric:
        raise ValueError("rubric missing dimensions key")
except Exception:
    # Fallback: parse weights from YAML manually (no yaml lib needed)
    rubric = _parse_rubric_fallback(rubric_file)

panel = json.loads(panel_json)

dims = {d["name"]: d["weight"] for d in rubric.get("dimensions", [])}
threshold = rubric.get("pass_threshold", 0.75)

# Compute per-dimension medians
dim_scores = {}
for dim in dims:
    scores_for_dim = []
    for judge in panel:
        s = judge.get("scores", {}).get(dim, 0)
        scores_for_dim.append(float(s))
    sorted_s = sorted(scores_for_dim)
    n = len(sorted_s)
    if n == 0:
        median = 0.0
    elif n % 2 == 1:
        median = sorted_s[n // 2]
    else:
        median = (sorted_s[n // 2 - 1] + sorted_s[n // 2]) / 2.0
    dim_scores[dim] = round(median, 4)

# Weighted aggregate
aggregate = 0.0
weight_sum = 0.0
for dim, weight in dims.items():
    aggregate += dim_scores.get(dim, 0.0) * weight
    weight_sum += weight
if weight_sum > 0:
    aggregate = aggregate / weight_sum

aggregate = round(aggregate, 4)

# Dissent check: max - min >= 0.5 on any dimension
dissent = False
for dim in dims:
    scores_for_dim = [float(j.get("scores", {}).get(dim, 0)) for j in panel]
    if len(scores_for_dim) >= 2:
        if max(scores_for_dim) - min(scores_for_dim) >= 0.5:
            dissent = True
            break

result = {
    "per_dimension_median": dim_scores,
    "aggregate_score": aggregate,
    "dissent": dissent,
    "threshold": threshold,
    "verdict": "pass" if (aggregate >= threshold and not dissent) else ("warn" if dissent else "fail"),
    "recommended_action": "surface_to_operator" if (dissent or aggregate < threshold) else "continue"
}

print(json.dumps(result))
PYEOF
)"

# Validate scoring output
if ! printf '%s' "$SCORING_OUTPUT" | jq empty 2>/dev/null; then
    ct_log "ERROR: scoring computation produced invalid JSON"
    exit 1
fi

AGGREGATE="$(printf '%s' "$SCORING_OUTPUT" | jq -r '.aggregate_score')"
DISSENT="$(printf '%s' "$SCORING_OUTPUT" | jq -r '.dissent')"
VERDICT="$(printf '%s' "$SCORING_OUTPUT" | jq -r '.verdict')"
RECOMMENDED="$(printf '%s' "$SCORING_OUTPUT" | jq -r '.recommended_action')"
THRESHOLD="$(printf '%s' "$SCORING_OUTPUT" | jq -r '.threshold')"

ct_log "score-plan: aggregate=$AGGREGATE threshold=$THRESHOLD dissent=$DISSENT verdict=$VERDICT"

# ---- compute duration -------------------------------------------------------
TS_END="$(date -u +%s 2>/dev/null || echo 0)"
DURATION_S=$((TS_END - TS_START))
[ "$DURATION_S" -lt 0 ] && DURATION_S=0

# ---- write log record -------------------------------------------------------
LOG_FILE="$HOME/.yakos-state/plan-quality-log.ndjson"
mkdir -p "$(dirname "$LOG_FILE")"

# Rotate if needed (via compat.sh helper)
ct_rotate_log "$LOG_FILE"

# Build the panel array for the log (all original panel verdicts)
PANEL_LOG_JSON="$PANEL_JSON_ARRAY"

RECORD="$(jq -cn \
    --arg type "plan_scored" \
    --arg ts "$TS_ISO" \
    --arg plan_id "$PLAN_ID" \
    --arg project "$(pwd)" \
    --arg plan_path "$PLAN_FILE" \
    --arg plan_hash "$PLAN_HASH" \
    --arg planner_model "${PLANNER_MODEL:-unknown}" \
    --arg rubric_hash "$RUBRIC_HASH" \
    --argjson panel "$PANEL_LOG_JSON" \
    --argjson panel_size "$PANEL_SIZE" \
    --argjson dropped_family "$DROPPED_FAMILY" \
    --argjson scoring "$SCORING_OUTPUT" \
    --argjson cost_est "$(printf '%s' "$COST_EST" | jq -R 'tonumber | . * 10000 | round / 10000')" \
    --argjson duration "$DURATION_S" \
    --argjson task_count "$TASK_COUNT" \
    --argjson assumption_count "$ASSUMPTION_COUNT" \
    --argjson risk_count "$RISK_COUNT" \
    '{
        type: $type,
        ts: $ts,
        plan_id: $plan_id,
        project: $project,
        plan_path: $plan_path,
        plan_hash: $plan_hash,
        planner_model: $planner_model,
        rubric_hash: $rubric_hash,
        panel: $panel,
        panel_size: $panel_size,
        dropped_family: $dropped_family,
        per_dimension_median: $scoring.per_dimension_median,
        aggregate_score: $scoring.aggregate_score,
        dissent: $scoring.dissent,
        threshold: $scoring.threshold,
        verdict: $scoring.verdict,
        recommended_action: $scoring.recommended_action,
        cost_usd: $cost_est,
        duration_s: $duration,
        task_count: $task_count,
        assumption_count: $assumption_count,
        risk_count: $risk_count
    }'
)"

printf '%s\n' "$RECORD" >> "$LOG_FILE"
ct_log "score-plan: record written to $LOG_FILE"

# ---- emit human-readable report ---------------------------------------------
REPORT_SCRIPT="$SKILL_DIR/scripts/report-plan-score.sh"
bash "$REPORT_SCRIPT" "$LOG_FILE"

# ---- exit code reflects verdict ---------------------------------------------
case "$VERDICT" in
    pass)  exit 0 ;;
    warn)  exit 1 ;;  # dissent → operator must review
    fail)  exit 1 ;;
    *)     exit 1 ;;
esac
