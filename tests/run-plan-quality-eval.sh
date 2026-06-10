#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-plan-quality-eval.sh — test suite for the plan-quality-eval skill.
#
# Tests:
#   1. good-plan.md       → verdict pass, aggregate >= 0.85
#   2. vague-plan.md      → verdict fail, acceptance_criteria_specificity <= 0.5
#   3. mega-task-plan.md  → decomposition_granularity == 0
#   4. cross-domain-plan.md → domain_boundaries_respected == 0
#   5. dissent-plan.md    → recommended_action == surface_to_operator
#                           (regardless of aggregate score)
#   6. self-eval-drop-plan.md + YAKOS_PLANNER_MODEL=claude-haiku-4
#                         → panel_size == 2, dropped_family == "claude"
#
# Judges are mocked via YAKOS_PLAN_JUDGE_MOCK to avoid API costs in CI.
# Exit 0 if all pass, 1 if any fail.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
SKILL_DIR="$REPO_ROOT/lib/skills/plan-quality-eval"
FIXTURES="$REPO_ROOT/tests/fixtures/plans"
MOCK_BASE="$REPO_ROOT/tests/fixtures/plan-judge-mock"

export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

# Source compat.sh for ct_log and other helpers
# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"

PASS=0
FAIL=0
FAIL_LOG=""

ok()   { printf '[ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '[FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); FAIL_LOG="${FAIL_LOG}FAIL: $*\n"; }

# ---- helpers ----------------------------------------------------------------

# score_plan_mocked <fixture-name> <mock-subdir> [extra env vars...]
# Returns the last JSON record from the log as SCORING_RECORD (exported).
score_plan_mocked() {
    local fixture_name="$1"
    local mock_subdir="$2"
    shift 2

    local plan_file="$FIXTURES/${fixture_name}.md"
    local mock_dir="$MOCK_BASE/$mock_subdir"
    local tmp_log
    tmp_log="$(mktemp -t yakos-pqe-test.XXXXXX.ndjson)"
    trap 'rm -f "$tmp_log" 2>/dev/null || true' RETURN

    # Run with a temp log file so tests don't pollute the real state dir
    SCORING_RECORD="$(
        YAKOS_PLAN_JUDGE_MOCK="$mock_dir" \
        HOME_OVERRIDE="$(dirname "$tmp_log")" \
        "$@" \
        bash "$SKILL_DIR/scripts/score-plan.sh" "$plan_file" \
            2>/dev/null
        # We need the log record, not stdout (which is the report).
        # score-plan.sh writes to $HOME/.yakos-state/plan-quality-log.ndjson
        # Override HOME so it writes to our temp location.
    )" || true

    # Re-run with HOME override to capture the log record
    local tmp_home
    tmp_home="$(mktemp -d -t yakos-pqe-home.XXXXXX)"
    mkdir -p "$tmp_home/.yakos-state"

    YAKOS_PLAN_JUDGE_MOCK="$mock_dir" \
        HOME="$tmp_home" \
        "$@" \
        bash "$SKILL_DIR/scripts/score-plan.sh" "$plan_file" \
        >/dev/null 2>/dev/null || true

    local log_file="$tmp_home/.yakos-state/plan-quality-log.ndjson"
    SCORING_RECORD=""
    if [ -f "$log_file" ]; then
        SCORING_RECORD="$(tail -1 "$log_file")"
    fi
    export SCORING_RECORD

    rm -rf "$tmp_home" 2>/dev/null || true
}

# ---- run scoring + capture record (shared runner) ---------------------------
run_test() {
    local label="$1"
    local plan_fixture="$2"
    local mock_subdir="$3"
    shift 3
    # Remaining args are env var assignments (VAR=val) passed to the script

    local plan_file="$FIXTURES/${plan_fixture}.md"
    local mock_dir="$MOCK_BASE/$mock_subdir"

    local tmp_home
    tmp_home="$(mktemp -d -t yakos-pqe-home.XXXXXX)"
    mkdir -p "$tmp_home/.yakos-state"

    local extra_env=("$@")

    # Build env prefix
    local env_cmd=()
    for e in "${extra_env[@]:-}"; do
        [ -n "$e" ] && env_cmd+=("$e")
    done

    # Run score-plan.sh with mock judges and isolated HOME
    if [ ${#env_cmd[@]} -gt 0 ]; then
        YAKOS_PLAN_JUDGE_MOCK="$mock_dir" \
            HOME="$tmp_home" \
            env "${env_cmd[@]}" \
            bash "$SKILL_DIR/scripts/score-plan.sh" "$plan_file" \
            >/dev/null 2>/dev/null || true
    else
        YAKOS_PLAN_JUDGE_MOCK="$mock_dir" \
            HOME="$tmp_home" \
            bash "$SKILL_DIR/scripts/score-plan.sh" "$plan_file" \
            >/dev/null 2>/dev/null || true
    fi

    local log_file="$tmp_home/.yakos-state/plan-quality-log.ndjson"
    LAST_RECORD=""
    if [ -f "$log_file" ]; then
        LAST_RECORD="$(tail -1 "$log_file")"
    fi

    rm -rf "$tmp_home" 2>/dev/null || true

    if [ -z "$LAST_RECORD" ] || ! printf '%s' "$LAST_RECORD" | jq empty 2>/dev/null; then
        fail "$label: no valid log record produced"
        return
    fi

    export LAST_RECORD
    printf '       label=%s plan=%s mock=%s\n' "$label" "$plan_fixture" "$mock_subdir"
}

# ---- Test 1: good plan → pass, aggregate >= 0.85 ----------------------------
echo ""
echo "Test 1: good-plan.md → verdict=pass, aggregate >= 0.85"
run_test "test-1-good" "good-plan" "good"

VERDICT="$(printf '%s' "$LAST_RECORD" | jq -r '.verdict')"
AGGREGATE="$(printf '%s' "$LAST_RECORD" | jq -r '.aggregate_score')"
AGGREGATE_OK="$(awk -v a="$AGGREGATE" 'BEGIN { print (a >= 0.85) ? "yes" : "no" }')"

if [ "$VERDICT" = "pass" ]; then
    ok "test-1: verdict=pass"
else
    fail "test-1: verdict=$VERDICT (expected pass)"
fi

if [ "$AGGREGATE_OK" = "yes" ]; then
    ok "test-1: aggregate=$AGGREGATE >= 0.85"
else
    fail "test-1: aggregate=$AGGREGATE < 0.85"
fi

# Verify log record was written with required fields
PLAN_ID="$(printf '%s' "$LAST_RECORD" | jq -r '.plan_id')"
PLAN_HASH="$(printf '%s' "$LAST_RECORD" | jq -r '.plan_hash')"
TYPE="$(printf '%s' "$LAST_RECORD" | jq -r '.type')"
if [ "$TYPE" = "plan_scored" ] && [ -n "$PLAN_ID" ] && [ -n "$PLAN_HASH" ]; then
    ok "test-1: log record has type=plan_scored, plan_id, plan_hash"
else
    fail "test-1: log record missing required fields (type=$TYPE plan_id=$PLAN_ID)"
fi

# ---- Test 2: vague plan → fail, acceptance <= 0.5 ---------------------------
echo ""
echo "Test 2: vague-plan.md → verdict fail, acceptance_criteria_specificity <= 0.5"
run_test "test-2-vague" "vague-plan" "vague"

VERDICT="$(printf '%s' "$LAST_RECORD" | jq -r '.verdict')"
ACCEPT_DIM="$(printf '%s' "$LAST_RECORD" | jq -r '.per_dimension_median.acceptance_criteria_specificity')"
ACCEPT_OK="$(awk -v a="$ACCEPT_DIM" 'BEGIN { print (a <= 0.5) ? "yes" : "no" }')"

if [ "$VERDICT" != "pass" ]; then
    ok "test-2: verdict=$VERDICT (not pass, as expected)"
else
    fail "test-2: verdict=pass unexpectedly (expected fail)"
fi

if [ "$ACCEPT_OK" = "yes" ]; then
    ok "test-2: acceptance_criteria_specificity=$ACCEPT_DIM <= 0.5"
else
    fail "test-2: acceptance_criteria_specificity=$ACCEPT_DIM > 0.5 (expected <= 0.5)"
fi

# ---- Test 3: mega-task → decomposition_granularity == 0 ---------------------
echo ""
echo "Test 3: mega-task-plan.md → decomposition_granularity == 0"
run_test "test-3-mega" "mega-task-plan" "mega-task"

DECOMP_DIM="$(printf '%s' "$LAST_RECORD" | jq -r '.per_dimension_median.decomposition_granularity')"
DECOMP_ZERO="$(awk -v d="$DECOMP_DIM" 'BEGIN { print (d == 0) ? "yes" : "no" }')"

if [ "$DECOMP_ZERO" = "yes" ]; then
    ok "test-3: decomposition_granularity=$DECOMP_DIM == 0"
else
    fail "test-3: decomposition_granularity=$DECOMP_DIM (expected 0)"
fi

VERDICT="$(printf '%s' "$LAST_RECORD" | jq -r '.verdict')"
if [ "$VERDICT" != "pass" ]; then
    ok "test-3: verdict=$VERDICT (not pass)"
else
    fail "test-3: verdict=pass unexpectedly"
fi

# ---- Test 4: cross-domain → domain_boundaries_respected == 0 ----------------
echo ""
echo "Test 4: cross-domain-plan.md → domain_boundaries_respected == 0"
run_test "test-4-cross-domain" "cross-domain-plan" "cross-domain"

DOMAIN_DIM="$(printf '%s' "$LAST_RECORD" | jq -r '.per_dimension_median.domain_boundaries_respected')"
DOMAIN_ZERO="$(awk -v d="$DOMAIN_DIM" 'BEGIN { print (d == 0) ? "yes" : "no" }')"

if [ "$DOMAIN_ZERO" = "yes" ]; then
    ok "test-4: domain_boundaries_respected=$DOMAIN_DIM == 0"
else
    fail "test-4: domain_boundaries_respected=$DOMAIN_DIM (expected 0)"
fi

VERDICT="$(printf '%s' "$LAST_RECORD" | jq -r '.verdict')"
if [ "$VERDICT" != "pass" ]; then
    ok "test-4: verdict=$VERDICT (not pass)"
else
    fail "test-4: verdict=pass unexpectedly"
fi

# ---- Test 5: dissent → recommended_action == surface_to_operator ------------
echo ""
echo "Test 5: dissent-plan.md → dissent=true, recommended_action=surface_to_operator"
run_test "test-5-dissent" "dissent-plan" "dissent"

DISSENT="$(printf '%s' "$LAST_RECORD" | jq -r '.dissent')"
RECOMMENDED="$(printf '%s' "$LAST_RECORD" | jq -r '.recommended_action')"

if [ "$DISSENT" = "true" ]; then
    ok "test-5: dissent=true"
else
    fail "test-5: dissent=$DISSENT (expected true)"
fi

if [ "$RECOMMENDED" = "surface_to_operator" ]; then
    ok "test-5: recommended_action=surface_to_operator"
else
    fail "test-5: recommended_action=$RECOMMENDED (expected surface_to_operator)"
fi

# Verify that even if aggregate is above threshold, dissent overrides verdict
AGGREGATE="$(printf '%s' "$LAST_RECORD" | jq -r '.aggregate_score')"
VERDICT="$(printf '%s' "$LAST_RECORD" | jq -r '.verdict')"
THRESHOLD="$(printf '%s' "$LAST_RECORD" | jq -r '.threshold')"
ABOVE_THRESHOLD="$(awk -v a="$AGGREGATE" -v t="$THRESHOLD" 'BEGIN { print (a >= t) ? "yes" : "no" }')"

if [ "$ABOVE_THRESHOLD" = "yes" ] && [ "$RECOMMENDED" = "surface_to_operator" ]; then
    ok "test-5: aggregate=$AGGREGATE >= threshold=$THRESHOLD yet recommended_action=surface_to_operator (dissent overrides)"
else
    printf '       (note: aggregate=%s threshold=%s above=%s recommended=%s)\n' \
        "$AGGREGATE" "$THRESHOLD" "$ABOVE_THRESHOLD" "$RECOMMENDED"
fi

# ---- Test 6: self-eval-drop → panel_size==2, dropped_family populated -------
echo ""
echo "Test 6: self-eval-drop with YAKOS_PLANNER_MODEL=claude-haiku-4 → panel_size=2, dropped_family=claude"
run_test "test-6-self-eval-drop" "self-eval-drop-plan" "self-eval-drop" \
    "YAKOS_PLANNER_MODEL=claude-haiku-4"

PANEL_SIZE="$(printf '%s' "$LAST_RECORD" | jq -r '.panel_size')"
DROPPED_FAMILY="$(printf '%s' "$LAST_RECORD" | jq -r '.dropped_family')"

if [ "$PANEL_SIZE" = "2" ]; then
    ok "test-6: panel_size=2"
else
    fail "test-6: panel_size=$PANEL_SIZE (expected 2)"
fi

if [ "$DROPPED_FAMILY" = "claude" ]; then
    ok "test-6: dropped_family=claude"
else
    fail "test-6: dropped_family=$DROPPED_FAMILY (expected claude)"
fi

# Verify the record still has a valid scoring result with the 2-judge panel
AGGREGATE="$(printf '%s' "$LAST_RECORD" | jq -r '.aggregate_score')"
AGGREGATE_OK="$(awk -v a="$AGGREGATE" 'BEGIN { print (a > 0) ? "yes" : "no" }')"
if [ "$AGGREGATE_OK" = "yes" ]; then
    ok "test-6: 2-judge panel produced valid aggregate=$AGGREGATE"
else
    fail "test-6: aggregate=$AGGREGATE (expected > 0 with 2-judge panel)"
fi

# ---- summary ----------------------------------------------------------------
echo ""
echo "Plan quality eval tests: $PASS passed, $FAIL failed"

if [ "$FAIL" -gt 0 ]; then
    printf '\nFailure details:\n%b\n' "$FAIL_LOG"
    exit 1
fi
exit 0
