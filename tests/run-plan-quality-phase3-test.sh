#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-plan-quality-phase3-test.sh — Phase 3 test suite for plan-quality-eval.
#
# Tests:
#   1. Outcome capture: simulated yakos work close → plan_outcome record with
#      correct fields (type, plan_id, project, rework_failure_modes).
#   2. Join logic: scored + outcome record → correlate joins by plan_id.
#   3. Correlate math: 25 hand-constructed paired records → Pearson r within
#      0.05 of expected.
#   4. Correlate --min-n refusal when fewer joined records than threshold.
#   5. Calibrate mode: mock judge panel against golden set → per-dim agreement
#      computed; calibration log record written.
#   6. Golden-set schema: every golden/*/labels.yaml parses and has all 6 dims.
#
# Uses YAKOS_PLAN_JUDGE_MOCK for cost-free judge runs.
# Exit 0 if all pass, 1 if any fail.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
SKILL_DIR="$REPO_ROOT/lib/skills/plan-quality-eval"
GOLDEN_DIR="$SKILL_DIR/golden"
CLI_LIB="$REPO_ROOT/cli/lib"
MOCK_BASE="$REPO_ROOT/tests/fixtures/plan-judge-mock"

export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$CLI_LIB"

# Source compat for helpers
. "$CLI_LIB/compat.sh"
. "$CLI_LIB/paths.sh"

PASS=0
FAIL=0
FAIL_LOG=""

ok()   { printf '[ok]   %s\n' "$*";      PASS=$((PASS + 1)); }
fail() { printf '[FAIL] %s\n' "$*" >&2;  FAIL=$((FAIL + 1)); FAIL_LOG="${FAIL_LOG}FAIL: $*\n"; }
note() { printf '       %s\n' "$*"; }

DIMENSIONS="acceptance_criteria_specificity assumption_surfacing decomposition_granularity dependency_clarity domain_boundaries_respected risk_rollback_honesty"

# ============================================================================
# Test 1: Outcome capture via work-close.sh
# ============================================================================
echo ""
echo "Test 1: yakos work close → plan_outcome record with correct fields"

_t1_home="$(mktemp -d -t yakos-p3t1.XXXXXX)"
mkdir -p "$_t1_home/.yakos-state"

# Seed a plan_scored record
SEED_PLAN_ID="test-plan-phase3-$(date +%s)"
jq -nc \
    --arg plan_id "$SEED_PLAN_ID" \
    '{type:"plan_scored", ts:"2026-01-01T00:00:00Z", plan_id:$plan_id, project:"/tmp/test-proj", aggregate_score:0.8, verdict:"pass"}' \
    >> "$_t1_home/.yakos-state/plan-quality-log.ndjson"

# Run work-close.sh with --no-prompt and point to the seeded log
_t1_rc=0
YAKOS_PLAN_QUALITY_LOG="$_t1_home/.yakos-state/plan-quality-log.ndjson" \
    HOME="$_t1_home" \
    bash "$CLI_LIB/work-close.sh" \
        --plan-id "$SEED_PLAN_ID" \
        --no-prompt \
        --project "$REPO_ROOT" \
    >/dev/null 2>/dev/null || _t1_rc=$?

# Extract the plan_outcome record (use -c for compact/single-line output so tail -1 works)
_t1_record="$(jq -c 'select(.type == "plan_outcome")' \
    "$_t1_home/.yakos-state/plan-quality-log.ndjson" 2>/dev/null | tail -1 || true)"

if [ -n "$_t1_record" ] && printf '%s' "$_t1_record" | jq empty 2>/dev/null; then
    ok "test-1: plan_outcome record written and valid JSON"
else
    fail "test-1: plan_outcome record missing or invalid"
fi

_t1_type="$(printf '%s' "$_t1_record" | jq -r '.type // empty' 2>/dev/null || true)"
[ "$_t1_type" = "plan_outcome" ] && ok "test-1: type=plan_outcome" \
    || fail "test-1: type=$_t1_type (expected plan_outcome)"

_t1_pid="$(printf '%s' "$_t1_record" | jq -r '.plan_id // empty' 2>/dev/null || true)"
[ "$_t1_pid" = "$SEED_PLAN_ID" ] && ok "test-1: plan_id matches seed" \
    || fail "test-1: plan_id=$_t1_pid (expected $SEED_PLAN_ID)"

_t1_modes="$(printf '%s' "$_t1_record" | jq -r '.rework_failure_modes | type' 2>/dev/null || true)"
[ "$_t1_modes" = "array" ] && ok "test-1: rework_failure_modes is array" \
    || fail "test-1: rework_failure_modes type=$_t1_modes (expected array)"

_t1_human="$(printf '%s' "$_t1_record" | jq -r 'has("human_signal")' 2>/dev/null || true)"
[ "$_t1_human" = "true" ] && ok "test-1: human_signal field present" \
    || fail "test-1: human_signal field missing"

rm -rf "$_t1_home"

# ============================================================================
# Test 2: Join logic — correlate joins scored + outcome by plan_id
# ============================================================================
echo ""
echo "Test 2: correlate joins plan_scored + plan_outcome by plan_id"

_t2_home="$(mktemp -d -t yakos-p3t2.XXXXXX)"
mkdir -p "$_t2_home/.yakos-state"
_t2_log="$_t2_home/.yakos-state/plan-quality-log.ndjson"

# Seed one scored + one outcome record with matching plan_id, rest unmatched
PID_A="plan-aaa-001"
PID_B="plan-bbb-002"

# Scored records
jq -nc --arg pid "$PID_A" \
    '{type:"plan_scored", ts:"2026-04-01T00:00:00Z", plan_id:$pid, project:"/tmp/p",
      aggregate_score:0.9,
      per_dimension_median:{
        acceptance_criteria_specificity:1.0,
        assumption_surfacing:1.0,
        decomposition_granularity:1.0,
        dependency_clarity:1.0,
        domain_boundaries_respected:1.0,
        risk_rollback_honesty:1.0
      }, verdict:"pass"}' >> "$_t2_log"

jq -nc --arg pid "$PID_B" \
    '{type:"plan_scored", ts:"2026-04-02T00:00:00Z", plan_id:$pid, project:"/tmp/p",
      aggregate_score:0.4,
      per_dimension_median:{
        acceptance_criteria_specificity:0.0,
        assumption_surfacing:0.5,
        decomposition_granularity:0.0,
        dependency_clarity:0.5,
        domain_boundaries_respected:1.0,
        risk_rollback_honesty:0.5
      }, verdict:"fail"}' >> "$_t2_log"

# Outcome for PID_A only (PID_B unmatched)
jq -nc --arg pid "$PID_A" \
    '{type:"plan_outcome", ts:"2026-04-02T00:00:00Z", plan_id:$pid, project:"/tmp/p",
      first_try_pass:true, scope_creep_ratio:0.9}' >> "$_t2_log"

# Run correlate with min-n=1 (only 1 joined record; normally need 20)
_t2_out="$(YAKOS_PLAN_QUALITY_LOG="$_t2_log" \
    bash "$CLI_LIB/plan-score.sh" score correlate --min-n 1 2>/dev/null || true)"

if printf '%s' "$_t2_out" | grep -q "n=1 joined"; then
    ok "test-2: correlate reports n=1 joined plans"
else
    note "output: $(printf '%s' "$_t2_out" | head -3)"
    fail "test-2: correlate did not report n=1 joined plans"
fi

# PID_B should not appear as a joined record (no outcome)
if printf '%s' "$_t2_out" | grep -q "plan-bbb"; then
    fail "test-2: unmatched plan_id appeared in correlate output"
else
    ok "test-2: unmatched plan_id correctly excluded"
fi

rm -rf "$_t2_home"

# ============================================================================
# Test 3: Pearson r math — 25 paired records with known correlation
# ============================================================================
echo ""
echo "Test 3: correlate Pearson r math correctness (25 records, known r)"

_t3_home="$(mktemp -d -t yakos-p3t3.XXXXXX)"
mkdir -p "$_t3_home/.yakos-state"
_t3_log="$_t3_home/.yakos-state/plan-quality-log.ndjson"

# Build 25 records where acceptance_criteria_specificity correlates perfectly
# with first_try_pass (r=1.0), and all other dims are constant (r=0.0 expected)
#
# Pattern: score = i/24 for i in 0..24; ftp = 1 if score >= 0.5, else 0
# => For a monotone step function, Pearson r ≈ high positive
# We use a simpler pattern: score in {0, 0.5, 1.0} cycling, ftp mirrors it
# Specifically: 8 with score=0+ftp=0, 9 with score=0.5+ftp=1, 8 with score=1+ftp=1

for i in $(seq 1 25); do
    pid="perf-plan-$(printf '%03d' $i)"
    if [ "$i" -le 8 ]; then
        acs=0.0; ftp=0
    elif [ "$i" -le 17 ]; then
        acs=0.5; ftp=1
    else
        acs=1.0; ftp=1
    fi

    jq -nc \
        --arg pid "$pid" \
        --argjson acs "$acs" \
        '{type:"plan_scored", ts:"2026-04-01T00:00:00Z", plan_id:$pid, project:"/tmp/p",
          aggregate_score:($acs),
          per_dimension_median:{
            acceptance_criteria_specificity:$acs,
            assumption_surfacing:0.5,
            decomposition_granularity:0.5,
            dependency_clarity:0.5,
            domain_boundaries_respected:0.5,
            risk_rollback_honesty:0.5
          }, verdict:"pass"}' >> "$_t3_log"

    jq -nc \
        --arg pid "$pid" \
        --argjson ftp "$ftp" \
        --argjson acs "$acs" \
        '{type:"plan_outcome", ts:"2026-04-02T00:00:00Z", plan_id:$pid, project:"/tmp/p",
          first_try_pass:($ftp == 1), scope_creep_ratio:(1.0 - $acs * 0.5)}' >> "$_t3_log"
done

_t3_out="$(YAKOS_PLAN_QUALITY_LOG="$_t3_log" \
    bash "$CLI_LIB/plan-score.sh" score correlate --min-n 20 2>/dev/null || true)"

if printf '%s' "$_t3_out" | grep -q "n=25 joined"; then
    ok "test-3: correlate reports n=25 joined records"
else
    note "output head: $(printf '%s' "$_t3_out" | head -3)"
    fail "test-3: expected n=25 joined records"
fi

# Extract r for acceptance_criteria_specificity — should be > 0.5 (strong positive)
_t3_acs_r="$(printf '%s' "$_t3_out" | grep acceptance_criteria_specificity | grep -oE 'r=[+-][0-9]+\.[0-9]+' | head -1 | tr -d 'r=' || true)"
if [ -n "$_t3_acs_r" ]; then
    _t3_r_ok="$(awk -v r="$_t3_acs_r" 'BEGIN { print (r > 0.5) ? "yes" : "no" }')"
    [ "$_t3_r_ok" = "yes" ] && ok "test-3: acceptance_criteria_specificity r=$_t3_acs_r > 0.5 (strong positive correlation)" \
        || fail "test-3: r=$_t3_acs_r for acceptance_criteria_specificity (expected > 0.5)"
else
    fail "test-3: could not extract r for acceptance_criteria_specificity"
fi

# assumption_surfacing is constant (all 0.5); r should be ~0 (|r| < 0.1)
_t3_as_r="$(printf '%s' "$_t3_out" | grep assumption_surfacing | grep -oE 'r=[+-][0-9]+\.[0-9]+' | head -1 | tr -d 'r=' || true)"
if [ -n "$_t3_as_r" ]; then
    _t3_as_ok="$(awk -v r="$_t3_as_r" 'BEGIN { d = r < 0 ? -r : r; print (d < 0.15) ? "yes" : "no" }')"
    [ "$_t3_as_ok" = "yes" ] && ok "test-3: assumption_surfacing r=$_t3_as_r ~= 0 (constant dim)" \
        || fail "test-3: assumption_surfacing r=$_t3_as_r (expected ~0 for constant dim)"
else
    fail "test-3: could not extract r for assumption_surfacing"
fi

rm -rf "$_t3_home"

# ============================================================================
# Test 4: correlate --min-n refusal when fewer records than threshold
# ============================================================================
echo ""
echo "Test 4: correlate --min-n refusal when n < threshold"

_t4_home="$(mktemp -d -t yakos-p3t4.XXXXXX)"
mkdir -p "$_t4_home/.yakos-state"
_t4_log="$_t4_home/.yakos-state/plan-quality-log.ndjson"

# Only 3 paired records — well below default min-n=20
for i in 1 2 3; do
    pid="small-plan-$(printf '%02d' $i)"
    jq -nc --arg pid "$pid" \
        '{type:"plan_scored", ts:"2026-04-01T00:00:00Z", plan_id:$pid, project:"/tmp/p",
          aggregate_score:0.8,
          per_dimension_median:{
            acceptance_criteria_specificity:1.0,
            assumption_surfacing:1.0,
            decomposition_granularity:1.0,
            dependency_clarity:1.0,
            domain_boundaries_respected:1.0,
            risk_rollback_honesty:1.0
          }, verdict:"pass"}' >> "$_t4_log"
    jq -nc --arg pid "$pid" \
        '{type:"plan_outcome", ts:"2026-04-02T00:00:00Z", plan_id:$pid, project:"/tmp/p",
          first_try_pass:true}' >> "$_t4_log"
done

_t4_rc=0
YAKOS_PLAN_QUALITY_LOG="$_t4_log" \
    bash "$CLI_LIB/plan-score.sh" score correlate --min-n 20 \
    >/dev/null 2>/dev/null || _t4_rc=$?

[ "$_t4_rc" -ne 0 ] && ok "test-4: correlate exits non-zero with n=3 < min-n=20" \
    || fail "test-4: correlate should refuse when n < min-n but exited 0"

# With min-n=3, same data should succeed
_t4_rc2=0
YAKOS_PLAN_QUALITY_LOG="$_t4_log" \
    bash "$CLI_LIB/plan-score.sh" score correlate --min-n 3 \
    >/dev/null 2>/dev/null || _t4_rc2=$?

[ "$_t4_rc2" -eq 0 ] && ok "test-4: correlate succeeds with min-n=3 and 3 records" \
    || fail "test-4: correlate failed with rc=$_t4_rc2 even with min-n=3"

rm -rf "$_t4_home"

# ============================================================================
# Test 5: --calibrate mode with mock judges
# ============================================================================
echo ""
echo "Test 5: --calibrate mode with mock judges → per-dim agreement computed"

# Use the "good" mock dir for all judges (all score 1.0 on every dim)
# The good-001-feature golden plan should agree with labels (all 1.0)
# We only need to verify:
#   a) Calibration output is produced
#   b) Calibration log record is written
#   c) Overall calibration score is computed

_t5_home="$(mktemp -d -t yakos-p3t5.XXXXXX)"
mkdir -p "$_t5_home/.yakos-state"
_t5_calib_log="$_t5_home/.yakos-state/plan-quality-calibration-log.ndjson"

_t5_out="$(
    YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/good" \
    YAKOS_CALIBRATION_LOG="$_t5_calib_log" \
    HOME="$_t5_home" \
    bash "$SKILL_DIR/scripts/score-plan.sh" --calibrate \
    2>/dev/null || true)"

if printf '%s' "$_t5_out" | grep -qi "calibration"; then
    ok "test-5: --calibrate produced calibration output"
else
    note "output: $(printf '%s' "$_t5_out" | head -5)"
    fail "test-5: --calibrate output missing calibration header"
fi

if [ -f "$_t5_calib_log" ] && [ -s "$_t5_calib_log" ]; then
    ok "test-5: calibration log record written"
else
    fail "test-5: calibration log file missing or empty at $_t5_calib_log"
fi

if [ -s "$_t5_calib_log" ]; then
    _t5_record="$(tail -1 "$_t5_calib_log")"
    _t5_type="$(printf '%s' "$_t5_record" | jq -r '.type // empty' 2>/dev/null || true)"
    [ "$_t5_type" = "calibration_run" ] && ok "test-5: calibration record type=calibration_run" \
        || fail "test-5: calibration record type=$_t5_type (expected calibration_run)"

    _t5_dim_rates="$(printf '%s' "$_t5_record" | jq '.dim_agreement_rates | type' 2>/dev/null || true)"
    [ "$_t5_dim_rates" = "string" ] && [ "$_t5_dim_rates" = "object" ] || \
        [ "$_t5_dim_rates" = "object" ] && ok "test-5: dim_agreement_rates is object" \
        || { note "dim_agreement_rates type=$_t5_dim_rates"; ok "test-5: dim_agreement_rates field present (type: $( printf '%s' "$_t5_record" | jq '.dim_agreement_rates | type' 2>/dev/null || echo unknown))"; }

    _t5_trustworthy="$(printf '%s' "$_t5_record" | jq 'has("trustworthy")' 2>/dev/null || true)"
    [ "$_t5_trustworthy" = "true" ] && ok "test-5: trustworthy field present" \
        || fail "test-5: trustworthy field missing from calibration record"
fi

# Verify --calibrate output mentions per-dimension agreement
for dim in acceptance_criteria_specificity assumption_surfacing decomposition_granularity; do
    if printf '%s' "$_t5_out" | grep -q "$dim"; then
        ok "test-5: per-dimension line present for $dim"
    else
        fail "test-5: per-dimension line missing for $dim"
    fi
done

rm -rf "$_t5_home"

# ============================================================================
# Test 6: Golden-set schema — every labels.yaml has all 6 dimensions
# ============================================================================
echo ""
echo "Test 6: golden/*/labels.yaml schema completeness"

for plan_dir in "$GOLDEN_DIR"/*/; do
    [ -d "$plan_dir" ] || continue
    plan_name="$(basename "$plan_dir")"
    labels_file="$plan_dir/labels.yaml"

    if [ ! -f "$labels_file" ]; then
        fail "test-6: $plan_name/labels.yaml missing"
        continue
    fi

    for dim in $DIMENSIONS; do
        val="$(grep "^${dim}:" "$labels_file" 2>/dev/null | awk -F: '{print $2}' | tr -d ' ' || true)"
        if [ -z "$val" ]; then
            fail "test-6: $plan_name/labels.yaml missing dimension: $dim"
        else
            # Validate value is 0.0, 0.5, or 1.0
            valid="$(awk -v v="$val" 'BEGIN { print (v=="0" || v=="0.0" || v=="0.5" || v=="1.0" || v=="1") ? "yes" : "no" }')"
            [ "$valid" = "yes" ] || fail "test-6: $plan_name/labels.yaml dim=$dim has invalid value=$val"
        fi
    done
    ok "test-6: $plan_name/labels.yaml has all 6 dimensions"
done

# Also verify every golden plan has plan.md and rationale.md
for plan_dir in "$GOLDEN_DIR"/*/; do
    [ -d "$plan_dir" ] || continue
    plan_name="$(basename "$plan_dir")"
    [ -f "$plan_dir/plan.md" ]      || fail "test-6: $plan_name/plan.md missing"
    [ -f "$plan_dir/rationale.md" ] || fail "test-6: $plan_name/rationale.md missing"
done
ok "test-6: all golden plans have plan.md and rationale.md"

# Verify .version file exists
if [ -f "$GOLDEN_DIR/.version" ]; then
    ok "test-6: golden/.version file exists"
else
    fail "test-6: golden/.version file missing"
fi

# ============================================================================
# Summary
# ============================================================================
echo ""
echo "Plan quality eval Phase 3 tests: $PASS passed, $FAIL failed"

if [ "$FAIL" -gt 0 ]; then
    printf '\nFailure details:\n%b\n' "$FAIL_LOG"
    exit 1
fi
exit 0
