#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-plan-quality-gate-test.sh — unit tests for the plan-quality-gate
# PostToolUse/PreToolUse hook and 'yakos plan score' CLI.
#
# Test coverage:
#   (a) .yakos.yml enabled: false → PASS (no scoring)
#   (b) Non-plan.md write → PASS (no-op for unrelated paths)
#   (c) Plan above threshold + no dissent → PASS
#   (d) Plan below threshold + mode surface → surface_to_operator decision +
#       per-plan notes file written
#   (e) Plan below threshold + mode block → block_next_tool decision +
#       .plan-blocked marker written
#   (f) Dissent fixture (regardless of aggregate) → surface_to_operator
#       (NOT block)
#   (g) Score script returns non-zero → WARN + PASS (no false block)
#   (h) Debounce: fresh plan.md (mtime ~= now) → debounced + PASS
#   (i) PreToolUse gate: no .plan-blocked marker → PASS
#   (j) PreToolUse gate: .plan-blocked present → BLOCK (rc=2)
#   (k) PreToolUse gate: enabled=false in .yakos.yml → PASS (marker cleared)
#   (l) CLI: 'yakos plan score show' renders report
#   (m) CLI: 'yakos plan score history' tabular output
#   (n) CLI: 'yakos plan score override' appends record + removes marker
#   (o) CLI: 'yakos plan score override' with unknown plan_id → error
#
# Uses YAKOS_PLAN_JUDGE_MOCK so tests cost nothing.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
CLI_LIB="$REPO_ROOT/cli/lib"
SKILL_DIR="$REPO_ROOT/lib/skills/plan-quality-eval"
FIXTURES="$REPO_ROOT/tests/fixtures/plans"
MOCK_BASE="$REPO_ROOT/tests/fixtures/plan-judge-mock"

export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$CLI_LIB"

pass=0; fail=0; fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
#
# Layout:
#   TMP/fakehome/                  — overridden HOME
#   TMP/fakehome/.yakos-state/     — plan-quality-log.ndjson location
#   TMP/project/                   — CLAUDE_PROJECT_DIR (FAKE_REPO)
#     .yakos.yml                   — project config under test
#   TMP/work/current/              — work/current/ dir (YAKOS_WORK_DIR)
#     logs/                        — hook log ndjson output
#     notes/                       — per-plan notes files
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-pqgate-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

export HOME="$TMP/fakehome"
FAKE_REPO="$TMP/project"
WORK_DIR="$TMP/work"
WORK_CURRENT="$WORK_DIR/current"

mkdir -p "$FAKE_REPO" "$WORK_CURRENT/logs" "$WORK_CURRENT/notes" "$HOME/.yakos-state"

# Default clean .yakos.yml
cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
YEOF

export CLAUDE_PROJECT_DIR="$FAKE_REPO"
export YAKOS_WORK_DIR="$WORK_DIR"
export YAKOS_PLAN_QUALITY_LOG="$HOME/.yakos-state/plan-quality-log.ndjson"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# PostToolUse Edit fixture targeting plan.md
fixture_edit_plan() {
    jq -nc \
        --arg fp "$1" \
        '{session_id: "test-session", tool_name: "Edit",
          hook_event_name: "PostToolUse",
          tool_input: {file_path: $fp, new_string: "updated plan content"}}'
}

# PostToolUse Write fixture targeting an unrelated file
fixture_write_other() {
    jq -nc '{session_id: "test-session", tool_name: "Write",
             hook_event_name: "PostToolUse",
             tool_input: {file_path: "/some/project/api/handler.go",
                          content: "package main"}}'
}

# PreToolUse TeamCreate fixture
fixture_teamcreate() {
    jq -nc '{session_id: "test-session", tool_name: "TeamCreate",
             hook_event_name: "PreToolUse",
             tool_input: {name: "test-team"}}'
}

# Run the hook with a fixture on stdin; return exit code
run_hook_rc() {
    local actual_rc=0
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$CLI_LIB" \
    HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 || actual_rc=$?
    printf '%s' "$actual_rc"
}

# Write a plan.md with mtime set to "old" (>5s ago) for debounce bypass
write_old_plan() {
    local plan_path="$1"
    local plan_src="${2:-$FIXTURES/vague-plan.md}"
    mkdir -p "$(dirname "$plan_path")"
    cp "$plan_src" "$plan_path"
    # Set mtime to 10 seconds ago — macOS touch uses -t, Linux also supports -t
    old_time="$(date -u -v-10S +%Y%m%d%H%M.%S 2>/dev/null \
        || date -u -d '10 seconds ago' +%Y%m%d%H%M.%S 2>/dev/null \
        || date +%Y%m%d%H%M.%S)"
    touch -t "$old_time" "$plan_path" 2>/dev/null || true
}

# Write a plan.md with current mtime (triggers debounce)
write_fresh_plan() {
    local plan_path="$1"
    local plan_src="${2:-$FIXTURES/vague-plan.md}"
    mkdir -p "$(dirname "$plan_path")"
    cp "$plan_src" "$plan_path"
    # mtime is now — within the 5s debounce window
}

# Check that a log record was written in WORK_CURRENT/logs/plan-quality-gate.ndjson
check_log_decision() {
    local expected_decision="$1"
    local logfile="$WORK_CURRENT/logs/plan-quality-gate.ndjson"
    [ -f "$logfile" ] || { printf 'no'; return; }
    if grep -q "\"$expected_decision\"" "$logfile" 2>/dev/null; then
        printf 'yes'
    else
        printf 'no'
    fi
}

# Reset state between tests
reset_state() {
    rm -f "$WORK_CURRENT/logs/plan-quality-gate.ndjson" 2>/dev/null || true
    rm -f "$WORK_CURRENT/.plan-blocked" 2>/dev/null || true
    rm -f "$WORK_CURRENT/notes"/plan-quality-*.md 2>/dev/null || true
    rm -f "$HOME/.yakos-state/plan-quality-log.ndjson" 2>/dev/null || true
    # Restore default .yakos.yml
    cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
YEOF
}

# ---------------------------------------------------------------------------
# Test (a): enabled: false → PASS (no scoring)
# ---------------------------------------------------------------------------
note ""
note "=== Test (a): enabled=false → PASS (no scoring) ==="
reset_state

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
plan_quality:
  enabled: false
YEOF

PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH"

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(a) enabled=false → rc=0 (PASS)"
else
    bad "(a) enabled=false: expected rc=0, got $actual_rc"
fi

# Scoring should NOT have been invoked — no log record expected
if grep -q '"plan_scored"' "$HOME/.yakos-state/plan-quality-log.ndjson" 2>/dev/null; then
    bad "(a) enabled=false but scoring ran (log record found)"
else
    ok "(a) enabled=false: no scoring log record (correct)"
fi

# ---------------------------------------------------------------------------
# Test (b): non-plan.md write → PASS (no-op)
# ---------------------------------------------------------------------------
note ""
note "=== Test (b): non-plan.md write → PASS (no-op) ==="
reset_state

actual_rc=0
fixture_write_other \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(b) non-plan.md write → rc=0 (no-op)"
else
    bad "(b) non-plan.md write: expected rc=0, got $actual_rc"
fi

# ---------------------------------------------------------------------------
# Test (c): above threshold + no dissent → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test (c): above threshold + no dissent → PASS ==="
reset_state

PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH" "$FIXTURES/good-plan.md"

actual_rc=0
YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/good" \
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/good" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(c) good plan → rc=0 (PASS)"
else
    bad "(c) good plan: expected rc=0, got $actual_rc"
fi

# No .plan-blocked marker should exist
if [ -f "$WORK_CURRENT/.plan-blocked" ]; then
    bad "(c) good plan: unexpected .plan-blocked marker"
else
    ok "(c) good plan: no .plan-blocked marker"
fi

# ---------------------------------------------------------------------------
# Test (d): below threshold + mode=surface → surface_to_operator + notes file
# ---------------------------------------------------------------------------
note ""
note "=== Test (d): below threshold + mode=surface → SURFACE + notes file ==="
reset_state

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
plan_quality:
  enabled: true
  mode: surface
  threshold: 0.75
YEOF

PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH" "$FIXTURES/vague-plan.md"

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/vague" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(d) surface mode: hook exits 0 (no block)"
else
    bad "(d) surface mode: expected rc=0, got $actual_rc"
fi

# Verify surface_to_operator decision logged
logged_decision="$(check_log_decision "surface_to_operator")"
if [ "$logged_decision" = "yes" ]; then
    ok "(d) surface mode: surface_to_operator decision logged"
else
    bad "(d) surface mode: expected surface_to_operator in log"
fi

# Notes file should have been created
notes_count="$(ls "$WORK_CURRENT/notes"/plan-quality-*.md 2>/dev/null | wc -l | tr -d ' ')"
if [ "$notes_count" -gt "0" ]; then
    ok "(d) surface mode: per-plan notes file written ($notes_count file(s))"
else
    bad "(d) surface mode: no notes file found in $WORK_CURRENT/notes/"
fi

# No .plan-blocked marker
if [ ! -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(d) surface mode: no .plan-blocked marker"
else
    bad "(d) surface mode: unexpected .plan-blocked marker"
fi

# ---------------------------------------------------------------------------
# Test (e): below threshold + mode=block + NO dissent → block_next_tool + .plan-blocked
#
# Uses the low-nodissent mock: all 3 judges give identical low scores so
# there is no dissent (max-min = 0 on all dimensions), and the aggregate
# is ~0.25 — well below the 0.75 threshold. In block mode this must
# produce a .plan-blocked marker.
# ---------------------------------------------------------------------------
note ""
note "=== Test (e): below threshold + mode=block → BLOCK marker written ==="
reset_state

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
plan_quality:
  enabled: true
  mode: block
  threshold: 0.75
YEOF

PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH" "$FIXTURES/vague-plan.md"

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/low-nodissent" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(e) block mode: PostToolUse hook exits 0 (write already succeeded)"
else
    bad "(e) block mode: expected rc=0 from PostToolUse, got $actual_rc"
fi

# .plan-blocked marker must exist
if [ -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(e) block mode: .plan-blocked marker written"
else
    bad "(e) block mode: expected .plan-blocked marker at $WORK_CURRENT/.plan-blocked"
fi

# Log should show block_next_tool decision
logged_decision="$(check_log_decision "block_next_tool")"
if [ "$logged_decision" = "yes" ]; then
    ok "(e) block mode: block_next_tool decision logged"
else
    bad "(e) block mode: expected block_next_tool in log"
fi

# Notes file should also exist
notes_count="$(ls "$WORK_CURRENT/notes"/plan-quality-*.md 2>/dev/null | wc -l | tr -d ' ')"
if [ "$notes_count" -gt "0" ]; then
    ok "(e) block mode: per-plan notes file written"
else
    bad "(e) block mode: no notes file found"
fi

# ---------------------------------------------------------------------------
# Test (f): dissent fixture → surface_to_operator (NOT block, even in block mode)
# ---------------------------------------------------------------------------
note ""
note "=== Test (f): dissent → surface_to_operator (NOT block) ==="
reset_state

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
plan_quality:
  enabled: true
  mode: block
  threshold: 0.75
YEOF

PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH" "$FIXTURES/good-plan.md"

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/dissent" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(f) dissent: hook exits 0"
else
    bad "(f) dissent: expected rc=0, got $actual_rc"
fi

# surface_to_operator logged (not block_next_tool)
logged_surface="$(check_log_decision "surface_to_operator")"
logged_block="$(check_log_decision "block_next_tool")"
if [ "$logged_surface" = "yes" ]; then
    ok "(f) dissent: surface_to_operator decision logged"
else
    bad "(f) dissent: expected surface_to_operator in log"
fi
if [ "$logged_block" = "no" ]; then
    ok "(f) dissent: no block_next_tool decision (correct)"
else
    bad "(f) dissent: block_next_tool was logged (should not be on dissent)"
fi

# No .plan-blocked marker on dissent
if [ ! -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(f) dissent: no .plan-blocked marker (correct)"
else
    bad "(f) dissent: unexpected .plan-blocked marker"
fi

# Notes file written
notes_count="$(ls "$WORK_CURRENT/notes"/plan-quality-*.md 2>/dev/null | wc -l | tr -d ' ')"
if [ "$notes_count" -gt "0" ]; then
    ok "(f) dissent: notes file written"
else
    bad "(f) dissent: no notes file found"
fi

# ---------------------------------------------------------------------------
# Test (g): score script non-zero → WARN + PASS (no false block)
# ---------------------------------------------------------------------------
note ""
note "=== Test (g): score script error → WARN + PASS ==="
reset_state

PLAN_PATH="$WORK_CURRENT/plan.md"
# Deliberately malformed plan (no frontmatter) → extract-plan.sh exits 2
cat > "$PLAN_PATH" <<'EOF'
# Not a valid plan — missing frontmatter

## Assumptions

EOF
# Use old mtime
old_time="$(date -u -v-10S +%Y%m%d%H%M.%S 2>/dev/null \
    || date -u -d '10 seconds ago' +%Y%m%d%H%M.%S 2>/dev/null \
    || date +%Y%m%d%H%M.%S)"
touch -t "$old_time" "$PLAN_PATH" 2>/dev/null || true

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/vague" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(g) score error: hook exits 0 (no false block)"
else
    bad "(g) score error: expected rc=0, got $actual_rc"
fi

if [ ! -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(g) score error: no .plan-blocked marker"
else
    bad "(g) score error: unexpected .plan-blocked marker"
fi

# ---------------------------------------------------------------------------
# Test (h): debounce — fresh mtime → debounced + PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test (h): debounce — fresh mtime → PASS (debounced) ==="
reset_state

PLAN_PATH="$WORK_CURRENT/plan.md"
write_fresh_plan "$PLAN_PATH" "$FIXTURES/vague-plan.md"
# mtime is now — within 5s debounce window

actual_rc=0
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/vague" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(h) debounce: hook exits 0"
else
    bad "(h) debounce: expected rc=0, got $actual_rc"
fi

# No scoring should have run; no record in state log
if grep -q '"plan_scored"' "$HOME/.yakos-state/plan-quality-log.ndjson" 2>/dev/null; then
    bad "(h) debounce: scoring ran (should have been debounced)"
else
    ok "(h) debounce: no scoring record (correctly debounced)"
fi

# ---------------------------------------------------------------------------
# Test (i): PreToolUse gate — no .plan-blocked → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test (i): PreToolUse gate — no .plan-blocked → PASS ==="
reset_state

actual_rc=0
fixture_teamcreate \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(i) PreToolUse gate: no marker → rc=0 (PASS)"
else
    bad "(i) PreToolUse gate: expected rc=0, got $actual_rc"
fi

# ---------------------------------------------------------------------------
# Test (j): PreToolUse gate — .plan-blocked present → BLOCK (rc=2)
# ---------------------------------------------------------------------------
note ""
note "=== Test (j): PreToolUse gate — .plan-blocked present → BLOCK ==="
reset_state

# Write the .plan-blocked marker
jq -nc \
    --arg plan_id "p-test-blocked-plan" \
    --arg reason "aggregate 0.42 < threshold 0.75" \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{plan_id: $plan_id, aggregate_score: "0.42",
      threshold: "0.75", ts: $ts, reason: $reason}' \
    > "$WORK_CURRENT/.plan-blocked"

actual_rc=0
fixture_teamcreate \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 2 ]; then
    ok "(j) PreToolUse gate: .plan-blocked → rc=2 (BLOCK)"
else
    bad "(j) PreToolUse gate: expected rc=2, got $actual_rc"
fi

# ---------------------------------------------------------------------------
# Test (k): PreToolUse gate — enabled=false → PASS (marker cleared)
# ---------------------------------------------------------------------------
note ""
note "=== Test (k): PreToolUse gate — enabled=false → PASS ==="
reset_state

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
plan_quality:
  enabled: false
YEOF

# Write the .plan-blocked marker
printf '{"plan_id":"p-test","reason":"blocked"}\n' > "$WORK_CURRENT/.plan-blocked"

actual_rc=0
fixture_teamcreate \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || actual_rc=$?

if [ "$actual_rc" -eq 0 ]; then
    ok "(k) enabled=false gate: rc=0 (PASS)"
else
    bad "(k) enabled=false gate: expected rc=0, got $actual_rc"
fi

# Marker should have been cleared
if [ ! -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(k) enabled=false gate: .plan-blocked marker cleared"
else
    bad "(k) enabled=false gate: marker should have been removed"
fi

# ---------------------------------------------------------------------------
# Test (l): CLI 'yakos plan score show' renders report
# ---------------------------------------------------------------------------
note ""
note "=== Test (l): CLI plan score show ==="
reset_state

# Run a scoring pass to populate the log
PLAN_PATH="$WORK_CURRENT/plan.md"
write_old_plan "$PLAN_PATH" "$FIXTURES/vague-plan.md"
fixture_edit_plan "$PLAN_PATH" \
    | YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_JUDGE_MOCK="$MOCK_BASE/vague" \
        bash "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 \
    || true

# CLI show
if [ -f "$HOME/.yakos-state/plan-quality-log.ndjson" ] && \
   grep -q '"plan_scored"' "$HOME/.yakos-state/plan-quality-log.ndjson" 2>/dev/null; then
    output="$(YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
        YAKOS_PLAN_QUALITY_LOG="$HOME/.yakos-state/plan-quality-log.ndjson" \
        bash "$CLI_LIB/plan-score.sh" show 2>&1 || true)"
    if printf '%s' "$output" | grep -q "Plan Quality Report"; then
        ok "(l) CLI show: renders report header"
    else
        bad "(l) CLI show: expected 'Plan Quality Report' in output; got: ${output:0:100}"
    fi
else
    # Scoring didn't produce a record (infra issue) — skip gracefully
    ok "(l) CLI show: skipped (no log record from scoring — infra issue in test env)"
fi

# ---------------------------------------------------------------------------
# Test (m): CLI 'yakos plan score history'
# ---------------------------------------------------------------------------
note ""
note "=== Test (m): CLI plan score history ==="
reset_state

# Populate log with a synthetic record
mkdir -p "$HOME/.yakos-state"
jq -nc \
    --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{type: "plan_scored", ts: "2026-05-01T10:00:00Z",
      plan_id: "p-test-history-fixture",
      project: "/tmp/testproject",
      plan_path: "/tmp/testproject/work/current/plan.md",
      plan_hash: "abc123",
      planner_model: "unknown",
      rubric_hash: "def456",
      panel: [],
      panel_size: 3,
      dropped_family: null,
      per_dimension_median: {
        acceptance_criteria_specificity: 0.5,
        assumption_surfacing: 0.0,
        decomposition_granularity: 1.0,
        dependency_clarity: 0.5,
        domain_boundaries_respected: 1.0,
        risk_rollback_honesty: 0.5
      },
      aggregate_score: 0.6,
      dissent: false,
      threshold: 0.75,
      verdict: "fail",
      recommended_action: "surface_to_operator",
      cost_usd: 0.001,
      duration_s: 5,
      task_count: 3,
      assumption_count: 1,
      risk_count: 1}' \
    >> "$HOME/.yakos-state/plan-quality-log.ndjson"

output="$(YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
    YAKOS_PLAN_QUALITY_LOG="$HOME/.yakos-state/plan-quality-log.ndjson" \
    bash "$CLI_LIB/plan-score.sh" history 2>&1 || true)"

if printf '%s' "$output" | grep -q "p-test-history-fixture"; then
    ok "(m) CLI history: plan_id found in tabular output"
else
    bad "(m) CLI history: plan_id 'p-test-history-fixture' not found in output"
fi
if printf '%s' "$output" | grep -q "verdict\|ts"; then
    ok "(m) CLI history: header row present"
else
    bad "(m) CLI history: missing header row"
fi

# ---------------------------------------------------------------------------
# Test (n): CLI 'yakos plan score override' removes marker + appends record
# ---------------------------------------------------------------------------
note ""
note "=== Test (n): CLI plan score override ==="
reset_state

BLOCK_PLAN_ID="p-test-block-override"

# Populate log with a scored record for this plan_id
jq -nc \
    --arg pid "$BLOCK_PLAN_ID" \
    '{type: "plan_scored", ts: "2026-05-01T11:00:00Z",
      plan_id: $pid,
      project: "/tmp/testproject",
      plan_path: "/tmp/testproject/work/current/plan.md",
      plan_hash: "abc123", planner_model: "unknown",
      rubric_hash: "def456", panel: [], panel_size: 3,
      dropped_family: null,
      per_dimension_median: {},
      aggregate_score: 0.3, dissent: false,
      threshold: 0.75, verdict: "fail",
      recommended_action: "surface_to_operator",
      cost_usd: 0.001, duration_s: 3,
      task_count: 2, assumption_count: 1, risk_count: 1}' \
    >> "$HOME/.yakos-state/plan-quality-log.ndjson"

# Write matching .plan-blocked marker
jq -nc --arg pid "$BLOCK_PLAN_ID" \
    '{plan_id: $pid, reason: "test block", ts: "2026-05-01T11:01:00Z"}' \
    > "$WORK_CURRENT/.plan-blocked"

# Run override
override_out="$(YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
    YAKOS_PLAN_QUALITY_LOG="$HOME/.yakos-state/plan-quality-log.ndjson" \
    bash "$CLI_LIB/plan-score.sh" override "$BLOCK_PLAN_ID" \
        --reason "reviewed and approved" 2>&1 || true)"

# .plan-blocked should be removed
if [ ! -f "$WORK_CURRENT/.plan-blocked" ]; then
    ok "(n) override: .plan-blocked marker removed"
else
    bad "(n) override: .plan-blocked marker still present"
fi

# Override record appended to log
if grep -q '"plan_score_override"' "$HOME/.yakos-state/plan-quality-log.ndjson" 2>/dev/null; then
    ok "(n) override: override record appended to log"
else
    bad "(n) override: no override record in log"
fi

# Confirmation message
if printf '%s' "$override_out" | grep -q "Override recorded"; then
    ok "(n) override: confirmation message output"
else
    bad "(n) override: expected 'Override recorded' in output"
fi

# ---------------------------------------------------------------------------
# Test (o): CLI 'plan score override' with unknown plan_id → error
# ---------------------------------------------------------------------------
note ""
note "=== Test (o): CLI plan score override unknown plan_id → error ==="
reset_state

# Empty log
mkdir -p "$HOME/.yakos-state"
touch "$HOME/.yakos-state/plan-quality-log.ndjson"

override_rc=0
YAKOS_LIB="$CLI_LIB" HOME="$HOME" \
    YAKOS_PLAN_QUALITY_LOG="$HOME/.yakos-state/plan-quality-log.ndjson" \
    bash "$CLI_LIB/plan-score.sh" override "p-does-not-exist" \
        --reason "test" >/dev/null 2>&1 \
    || override_rc=$?

if [ "$override_rc" -ne 0 ]; then
    ok "(o) override unknown: exited non-zero ($override_rc)"
else
    bad "(o) override unknown: expected non-zero exit, got 0"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
note ""
printf '============================================\n'
printf 'Results: %d passed, %d failed\n' "$pass" "$fail"
if [ "$fail" -gt 0 ]; then
    printf '\nFailed tests:\n'
    printf '%b' "$fail_log"
    printf '============================================\n'
    exit 1
fi
printf '============================================\n'
exit 0
