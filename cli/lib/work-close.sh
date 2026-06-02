#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: work-close.sh — close the current work session and capture outcome
#          telemetry for the plan-quality feedback loop.
#
# Usage:
#   yakos work close [--plan-id <id>] [--no-prompt] [--project <path>]
#
# What it does:
#   1. Locates the most recent plan_scored record in plan-quality-log.ndjson
#      (or uses --plan-id if given).
#   2. Computes outcome fields: git diff stats, dispatch-log sums, rework count.
#   3. Optionally prompts the operator for a human_signal (skip with --no-prompt).
#   4. Appends a plan_outcome record to ~/.yakos-state/plan-quality-log.ndjson.
#
# Non-blocking contract: any missing data field → null, log WARN, keep going.

set -eu

: "${YAKOS_LIB:?work-close.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/paths.sh"

PLAN_QUALITY_LOG="${YAKOS_PLAN_QUALITY_LOG:-$HOME/.yakos-state/plan-quality-log.ndjson}"
DISPATCH_LOG="${YAKOS_DISPATCH_LOG:-$HOME/.yakos-state/dispatch-log.ndjson}"
SESSIONS_LOG=""  # resolved below after paths.sh loads

usage() {
    cat <<'EOF'
yakos work close [options]

Capture outcome telemetry for the current work session and append a
plan_outcome record to the plan-quality log. Used to close the
feedback loop between plan scores and real execution outcomes.

Options:
  --plan-id <id>    Explicitly name the plan_id to close. Defaults to the
                    most recent plan_scored record in the log.
  --no-prompt       Skip the interactive human_signal prompt.
  --project <path>  Project root for git diff (defaults to cwd).
  -h, --help        Show this help.

The plan_outcome record is appended to:
  ~/.yakos-state/plan-quality-log.ndjson

Record type: plan_outcome (joined to plan_scored via plan_id).
EOF
}

# ---- arg parsing ------------------------------------------------------------

PLAN_ID_OVERRIDE=""
NO_PROMPT=0
PROJECT_DIR=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --plan-id)   shift; PLAN_ID_OVERRIDE="$1" ;;
        --plan-id=*) PLAN_ID_OVERRIDE="${1#--plan-id=}" ;;
        --no-prompt) NO_PROMPT=1 ;;
        --project)   shift; PROJECT_DIR="$1" ;;
        --project=*) PROJECT_DIR="${1#--project=}" ;;
        -h|--help)   usage; exit 0 ;;
        *) ct_die "work close: unknown option '$1'" ;;
    esac
    shift
done

[ -z "$PROJECT_DIR" ] && PROJECT_DIR="$(pwd -P)"

# ---- require jq -------------------------------------------------------------

if ! command -v jq >/dev/null 2>&1; then
    ct_die "work close: jq is required"
fi

# ---- resolve plan_id --------------------------------------------------------

resolve_plan_id() {
    local override="$1"
    if [ -n "$override" ]; then
        printf '%s' "$override"
        return
    fi
    if [ ! -f "$PLAN_QUALITY_LOG" ] || [ ! -s "$PLAN_QUALITY_LOG" ]; then
        printf ''
        return
    fi
    # Most recent plan_scored record
    grep '"type"' "$PLAN_QUALITY_LOG" 2>/dev/null \
        | grep '"plan_scored"' \
        | tail -1 \
        | jq -r '.plan_id // empty' 2>/dev/null \
        || true
}

PLAN_ID="$(resolve_plan_id "$PLAN_ID_OVERRIDE")"
if [ -z "$PLAN_ID" ]; then
    echo "work close: no plan_scored record found in $PLAN_QUALITY_LOG" >&2
    echo "  Run 'yakos skill plan-quality-eval <plan.md>' first, or pass --plan-id." >&2
    exit 1
fi

ct_log "work close: closing plan_id=$PLAN_ID"

# ---- helper: best-effort field (logs WARN + returns null on error) ----------

_warn_null() {
    local field="$1"
    ct_log "WARN: work close: could not compute $field; recording null"
    printf 'null'
}

# ---- session window ---------------------------------------------------------
# Resolve the session start commit from sessions.ndjson or session-started file.

SESSION_START_COMMIT="null"
SESSION_START_TS="null"

_resolve_session_start() {
    local current_dir
    current_dir="$(yakos_current_dir)"
    SESSIONS_LOG="$(yakos_sessions_log_file)"
    local started_file
    started_file="$(yakos_session_started_file)"

    if [ -f "$started_file" ]; then
        SESSION_START_TS="$(head -1 "$started_file" 2>/dev/null || true)"
    fi

    # Try to find the git commit at session start via reflog (best effort)
    if [ -d "$PROJECT_DIR/.git" ] || git -C "$PROJECT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
        # Use the oldest commit since the session started timestamp when available
        if [ -n "$SESSION_START_TS" ] && [ "$SESSION_START_TS" != "null" ]; then
            local epoch_start
            epoch_start="$(ct_iso_to_epoch "$SESSION_START_TS" 2>/dev/null || echo 0)"
            if [ "$epoch_start" -gt 0 ]; then
                # Find the commit just before session start
                local commit_before
                commit_before="$(git -C "$PROJECT_DIR" log --format='%H %ct' --after="$((epoch_start - 1))" 2>/dev/null \
                    | awk -v es="$epoch_start" '$2 < es {print $1; exit}' || true)"
                [ -n "$commit_before" ] && SESSION_START_COMMIT="\"$commit_before\""
            fi
        fi

        # Fallback: use the parent of the first commit in session
        if [ "$SESSION_START_COMMIT" = "null" ]; then
            # Just use HEAD~N for a reasonable window; we'll diff HEAD vs HEAD~20
            local fallback_commit
            fallback_commit="$(git -C "$PROJECT_DIR" rev-parse HEAD 2>/dev/null || true)"
            [ -n "$fallback_commit" ] && SESSION_START_COMMIT="\"${fallback_commit}\""
        fi
    fi
}

_resolve_session_start 2>/dev/null || true

# ---- git diff stats ---------------------------------------------------------

FILES_TOUCHED="null"
LINES_CHANGED="null"

_compute_git_stats() {
    if ! ([ -d "$PROJECT_DIR/.git" ] || git -C "$PROJECT_DIR" rev-parse --git-dir >/dev/null 2>&1); then
        return
    fi
    # diff against the commit at session start (or use working tree if no commit)
    local stats
    stats="$(git -C "$PROJECT_DIR" diff --numstat HEAD 2>/dev/null || true)"
    if [ -z "$stats" ]; then
        # Try diff of all tracked changes since last commit
        stats="$(git -C "$PROJECT_DIR" diff --numstat 2>/dev/null || true)"
    fi
    if [ -n "$stats" ]; then
        FILES_TOUCHED="$(printf '%s\n' "$stats" | awk 'END {print NR}')"
        LINES_CHANGED="$(printf '%s\n' "$stats" | awk '{a+=$1; d+=$2} END {print a+d}')"
    fi
}

_compute_git_stats 2>/dev/null || true

# ---- files_touched_estimated (from plan.md) ---------------------------------

FILES_TOUCHED_ESTIMATED="null"

_parse_plan_estimate() {
    local plan_md="$PROJECT_DIR/work/current/plan.md"
    [ -f "$plan_md" ] || return
    # Look for "files_touched_estimate: N" in the Tasks block
    local total
    total="$(awk '/^## Tasks/,/^## /' "$plan_md" 2>/dev/null \
        | grep -i 'files_touched_estimate:' \
        | awk -F: '{sum+=$2} END {if(NR>0) print sum}' || true)"
    [ -n "$total" ] && FILES_TOUCHED_ESTIMATED="$total"
}

_parse_plan_estimate 2>/dev/null || true

# ---- scope_creep_ratio ------------------------------------------------------

SCOPE_CREEP_RATIO="null"
if [ "$FILES_TOUCHED" != "null" ] && [ "$FILES_TOUCHED_ESTIMATED" != "null" ] \
        && [ "$FILES_TOUCHED_ESTIMATED" -gt 0 ] 2>/dev/null; then
    SCOPE_CREEP_RATIO="$(awk -v a="$FILES_TOUCHED" -v b="$FILES_TOUCHED_ESTIMATED" \
        'BEGIN { printf "%.2f", a/b }' 2>/dev/null || echo "null")"
fi

# ---- dispatch-log stats (tokens + cost) -------------------------------------

TOKENS_SPENT="null"
COST_USD="null"

_compute_dispatch_stats() {
    [ -f "$DISPATCH_LOG" ] || return
    # Filter by session window if we have timestamps; otherwise sum all
    local filter_expr='.tokens_total // 0'
    local cost_expr='.cost_usd // 0'

    if [ -n "$SESSION_START_TS" ] && [ "$SESSION_START_TS" != "null" ]; then
        filter_expr='select(.ts >= $start) | .tokens_total // 0'
        local result
        result="$(jq -r \
            --arg start "$SESSION_START_TS" \
            'select(.ts >= $start) | [.tokens_total // 0, .cost_usd // 0] | @tsv' \
            "$DISPATCH_LOG" 2>/dev/null \
            | awk 'BEGIN{t=0;c=0} {t+=$1; c+=$2} END{printf "%d\t%.4f", t, c}' || true)"
        if [ -n "$result" ]; then
            TOKENS_SPENT="$(printf '%s' "$result" | cut -f1)"
            COST_USD="$(printf '%s' "$result" | cut -f2)"
        fi
    else
        local result
        result="$(jq -r '[.tokens_total // 0, .cost_usd // 0] | @tsv' \
            "$DISPATCH_LOG" 2>/dev/null \
            | awk 'BEGIN{t=0;c=0} {t+=$1; c+=$2} END{printf "%d\t%.4f", t, c}' || true)"
        if [ -n "$result" ]; then
            TOKENS_SPENT="$(printf '%s' "$result" | cut -f1)"
            COST_USD="$(printf '%s' "$result" | cut -f2)"
        fi
    fi
}

_compute_dispatch_stats 2>/dev/null || true

# ---- rework_cycles ----------------------------------------------------------
# Count dispatches where agent was already marked done in kanban (re-dispatched
# to the same task assignee for a task already in DONE column).

REWORK_CYCLES="null"

_compute_rework() {
    local dispatch_log="$DISPATCH_LOG"
    [ -f "$dispatch_log" ] || return
    # Simple heuristic: count dispatch records with repeat agent+task combos
    local count
    count="$(jq -r 'select(.type == "dispatch" or .agent != null) | .agent // empty' \
        "$dispatch_log" 2>/dev/null \
        | sort | uniq -c | awk '$1 > 1 {sum += ($1 - 1)} END {print sum+0}' || true)"
    [ -n "$count" ] && REWORK_CYCLES="$count"
}

_compute_rework 2>/dev/null || true
[ "$REWORK_CYCLES" = "" ] && REWORK_CYCLES="null"

# ---- rework_failure_modes ---------------------------------------------------

REWORK_FAILURE_MODES="[]"

_compute_failure_modes() {
    local current_dir
    current_dir="$(yakos_current_dir)"
    local modes="[]"
    local logs_dir="$current_dir/logs"

    # Check for hook bypass events
    if [ -d "$logs_dir" ]; then
        local bypass_found
        bypass_found="$(grep -l 'hook_bypass\|bypass' "$logs_dir"/*.ndjson 2>/dev/null | head -1 || true)"
        [ -n "$bypass_found" ] && modes="$(jq -nc --argjson m "$modes" '$m + ["hook_bypass"]')"
    fi

    # Check for block_overridden (plan score override records)
    if [ -f "$PLAN_QUALITY_LOG" ]; then
        local overridden
        overridden="$(grep '"plan_score_override"' "$PLAN_QUALITY_LOG" 2>/dev/null | grep "\"$PLAN_ID\"" | head -1 || true)"
        [ -n "$overridden" ] && modes="$(jq -nc --argjson m "$modes" '$m + ["block_overridden"]')"
    fi

    # Test rerun: check for test_rerun markers or repeated test invocations in dispatch log
    if [ -f "$DISPATCH_LOG" ]; then
        local test_reruns
        test_reruns="$(jq -r 'select(.agent == "test-runner" or (.task // "" | test("test"; "i"))) | .agent' \
            "$DISPATCH_LOG" 2>/dev/null | wc -l | tr -d ' ' || true)"
        [ "${test_reruns:-0}" -gt 1 ] && modes="$(jq -nc --argjson m "$modes" '$m + ["test_rerun"]')"
    fi

    REWORK_FAILURE_MODES="$modes"
}

_compute_failure_modes 2>/dev/null || true

# ---- first_try_pass ---------------------------------------------------------

FIRST_TRY_PASS="null"

_compute_first_try() {
    local rework="${REWORK_CYCLES:-null}"
    # TRUE iff rework_cycles == 0 AND no hook_bypass AND no block_overridden
    if [ "$rework" = "null" ]; then
        return
    fi
    local has_bypass has_override
    has_bypass="$(printf '%s' "$REWORK_FAILURE_MODES" | jq 'contains(["hook_bypass"])' 2>/dev/null || echo "false")"
    has_override="$(printf '%s' "$REWORK_FAILURE_MODES" | jq 'contains(["block_overridden"])' 2>/dev/null || echo "false")"

    if [ "$rework" = "0" ] && [ "$has_bypass" = "false" ] && [ "$has_override" = "false" ]; then
        FIRST_TRY_PASS="true"
    else
        FIRST_TRY_PASS="false"
    fi
}

_compute_first_try 2>/dev/null || true

# ---- human_signal -----------------------------------------------------------

HUMAN_SATISFIED="null"
HUMAN_NOTE="null"

if [ "$NO_PROMPT" = "0" ]; then
    # Only prompt if stdin is a terminal
    if [ -t 0 ]; then
        printf '\n'
        printf 'Work session closed for plan_id: %s\n' "$PLAN_ID"
        printf '\n'
        printf 'Optional: capture a human signal for the feedback loop.\n'
        printf 'Was the outcome satisfactory? [y/n/skip]: '
        read -r _satisfied_input </dev/tty || _satisfied_input="skip"
        case "$_satisfied_input" in
            y|Y|yes|Yes|YES) HUMAN_SATISFIED="true" ;;
            n|N|no|No|NO)    HUMAN_SATISFIED="false" ;;
            *)               HUMAN_SATISFIED="null" ;;
        esac

        if [ "$HUMAN_SATISFIED" != "null" ]; then
            printf 'Optional note (press Enter to skip): '
            read -r _note_input </dev/tty || _note_input=""
            [ -n "$_note_input" ] && HUMAN_NOTE="$(jq -Rn --arg n "$_note_input" '$n')"
        fi
    fi
fi

# ---- build human_signal JSON ------------------------------------------------

HUMAN_SIGNAL="null"
if [ "$HUMAN_SATISFIED" != "null" ]; then
    HUMAN_SIGNAL="$(jq -nc \
        --argjson sat "$HUMAN_SATISFIED" \
        --argjson note "$HUMAN_NOTE" \
        '{satisfied: $sat, note: $note}')"
fi

# ---- assemble the record ----------------------------------------------------

TS_NOW="$(ct_iso_now_z)"

# Build JSON safely with jq, handling null fields
RECORD="$(jq -cn \
    --arg type "plan_outcome" \
    --arg ts "$TS_NOW" \
    --arg plan_id "$PLAN_ID" \
    --arg project "$PROJECT_DIR" \
    --argjson first_try_pass "$FIRST_TRY_PASS" \
    --argjson rework_cycles "$REWORK_CYCLES" \
    --argjson files_touched "$FILES_TOUCHED" \
    --argjson files_touched_estimated "$FILES_TOUCHED_ESTIMATED" \
    --argjson scope_creep_ratio "$SCOPE_CREEP_RATIO" \
    --argjson lines_changed "$LINES_CHANGED" \
    --argjson tokens_spent_total "$TOKENS_SPENT" \
    --argjson cost_usd_actual "$COST_USD" \
    --argjson rework_failure_modes "$REWORK_FAILURE_MODES" \
    --argjson human_signal "$HUMAN_SIGNAL" \
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
        human_signal: $human_signal
    }')"

# ---- write to log -----------------------------------------------------------

mkdir -p "$(dirname "$PLAN_QUALITY_LOG")" 2>/dev/null || true
ct_rotate_log "$PLAN_QUALITY_LOG"
printf '%s\n' "$RECORD" >> "$PLAN_QUALITY_LOG"

ct_log "work close: plan_outcome record appended to $PLAN_QUALITY_LOG"

echo ""
echo "Work closed for plan_id: $PLAN_ID"
echo "  files_touched:   ${FILES_TOUCHED}"
echo "  lines_changed:   ${LINES_CHANGED}"
echo "  rework_cycles:   ${REWORK_CYCLES}"
echo "  first_try_pass:  ${FIRST_TRY_PASS}"
echo "  cost_usd_actual: ${COST_USD}"
echo ""
echo "Record type plan_outcome appended to: $PLAN_QUALITY_LOG"
