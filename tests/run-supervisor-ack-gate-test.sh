#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-ack-gate-test.sh — unit + integration tests for the
# supervisor-ack-gate PreToolUse hook and the 'yakos supervise' ack CLI.
#
# Test coverage:
#   1.  No findings file          → PASS (rc=0)
#   2.  Findings present but all 'continue' → PASS (rc=0)
#   3.  One unacked surface_to_operator → BLOCK (rc=2), message lists finding id
#   4.  One unacked block_next_tool    → BLOCK (rc=2)
#   5.  One unacked halt               → BLOCK (rc=2)
#   6.  Finding acked via CLI          → next dispatch PASS (rc=0)
#   7.  ack-all via CLI                → next dispatch PASS (rc=0)
#   8.  Per-project opt-out (gate_on_escalation: false) → PASS (rc=0)
#   9.  Malformed findings file (bad JSON line) → WARN + PASS (rc=0)
#  10.  YAKOS_SUPERVISOR_DISABLE=1     → PASS (rc=0)
#  11.  Non-TeamCreate/Agent tool      → PASS (rc=0, hook skips)
#  12.  Multiple pending; ack-all acks all → PASS (rc=0)
#  13.  CLI: 'ack' with unknown finding-id → error exit (rc!=0)
#  14.  'pending' shows pending, empty after ack-all

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
CLI_LIB="$REPO_ROOT/cli/lib"

pass=0; fail=0; fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
#
# Layout:
#   TMP/fakehome/                     — overridden HOME
#   TMP/fakehome/.yakos-state/        — ack file location
#   TMP/fakehome/agent-control/testproject/
#       .project-path                 — points to TMP/project (FAKE_REPO)
#       work/current/
#           supervisor-findings.ndjson  — CLI reads from here
#           logs/                       — CLI writes logs here
#
#   TMP/project/ (FAKE_REPO)          — project repo
#       .yakos.yml                    — project config
#
# The hook is run with YAKOS_WORK_DIR pointing at:
#   TMP/fakehome/agent-control/testproject/work
# so both the hook AND the CLI resolve findings to the same location.
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-ack-gate-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

export HOME="$TMP/fakehome"
PROJ_NAME="testproject"
AC_DIR="$HOME/agent-control/$PROJ_NAME"
WORK_DIR="$AC_DIR/work"
WORK_CURRENT="$WORK_DIR/current"

FAKE_REPO="$TMP/project"

mkdir -p "$FAKE_REPO" "$WORK_CURRENT/logs" "$HOME/.yakos-state"

# .project-path so resolve_project_paths in supervise.sh can work
mkdir -p "$AC_DIR"
printf '%s\n' "$FAKE_REPO" > "$AC_DIR/.project-path"

# Blank .yakos.yml in the project repo
cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
YEOF

export CLAUDE_PROJECT_DIR="$FAKE_REPO"
export YAKOS_PROJECT_NAME="$PROJ_NAME"
# Point hook work-dir resolution to the same tree the CLI uses
export YAKOS_WORK_DIR="$WORK_DIR"

FINDINGS="$WORK_CURRENT/supervisor-findings.ndjson"
ACK_FILE="$HOME/.yakos-state/supervisor-acks.ndjson"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Fixture: TeamCreate hook input
fixture_teamcreate() {
    jq -nc '{session_id: "test-session", tool_name: "TeamCreate",
             hook_event_name: "PreToolUse",
             tool_input: {name: "test-team"}}'
}

# Fixture: Agent hook input
fixture_agent() {
    jq -nc '{session_id: "test-session", tool_name: "Agent",
             hook_event_name: "PreToolUse",
             tool_input: {subagent_type: "backend"}}'
}

# Fixture: Edit hook input (should always pass through without inspection)
fixture_edit() {
    jq -nc '{session_id: "test-session", tool_name: "Edit",
             hook_event_name: "PreToolUse",
             tool_input: {file_path: "api/foo.go"}}'
}

# Write a single finding line to the findings file (appends)
write_finding() {
    local ts="$1" overall="$2" rationale="$3" recommended="$4"
    jq -nc \
        --arg ts "$ts" \
        --arg overall "$overall" \
        --arg rationale "$rationale" \
        --arg recommended "$recommended" \
        '{ts: $ts, batch_size: 10, overall: $overall, rationale: $rationale,
          recommended_action: $recommended}' \
        >> "$FINDINGS"
}

# Run the hook (stdin from fixture)
run_hook_rc() {
    local actual_rc=0
    bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
    printf '%s' "$actual_rc"
}

# Derive the same finding ID the hook uses
derive_id() {
    local raw_ts="$1" proj="$2" linenum="$3"
    local safe_ts="${raw_ts//:/-}"
    printf 'f-%s-%s-%s' "$safe_ts" "$proj" "$linenum"
}

# Run a supervise subcommand through the CLI lib directly
run_supervise() {
    YAKOS_LIB="$CLI_LIB" \
    HOME="$HOME" \
        bash "$CLI_LIB/supervise.sh" "$@" 2>&1
}

# ---------------------------------------------------------------------------
# Reset state helpers
# ---------------------------------------------------------------------------

reset_findings() {
    rm -f "$FINDINGS"
}

reset_acks() {
    rm -f "$ACK_FILE"
}

reset_all() {
    reset_findings
    reset_acks
    # Restore clean .yakos.yml
    cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
YEOF
}

# ---------------------------------------------------------------------------
# Test 1: No findings file → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 1: No findings file → PASS ==="

reset_all

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 1: no findings file → rc=0 (PASS)"
else
    bad "test 1: expected rc=0, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 2: All findings have recommended_action: continue → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 2: All 'continue' findings → PASS ==="

reset_all
write_finding "2026-05-27T05:00:00Z" "PASS" "all good" "continue"
write_finding "2026-05-27T05:01:00Z" "PASS" "still good" "continue"

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 2: all continue → rc=0 (PASS)"
else
    bad "test 2: expected rc=0, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 3: One unacked surface_to_operator → BLOCK with finding id in message
# ---------------------------------------------------------------------------
note ""
note "=== Test 3: Unacked surface_to_operator → BLOCK ==="

reset_all
TS3="2026-05-27T05:31:02Z"
write_finding "$TS3" "WARN" "worktree-isolation violation" "surface_to_operator"
EXPECTED_ID3="$(derive_id "$TS3" "$PROJ_NAME" 1)"

actual_rc=0
stderr_out="$(fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" 2>&1)" || actual_rc=$?
if [ "$actual_rc" -eq 2 ]; then
    ok "test 3: rc=2 (BLOCK)"
else
    bad "test 3: expected rc=2, got rc=$actual_rc"
fi
if printf '%s' "$stderr_out" | grep -q "$EXPECTED_ID3"; then
    ok "test 3: finding id present in block message"
else
    bad "test 3: finding id '$EXPECTED_ID3' not in stderr: $stderr_out"
fi
if printf '%s' "$stderr_out" | grep -q "yakos supervise ack"; then
    ok "test 3: ack command instruction present in block message"
else
    bad "test 3: ack command not in stderr: $stderr_out"
fi

# ---------------------------------------------------------------------------
# Test 4: block_next_tool → BLOCK
# ---------------------------------------------------------------------------
note ""
note "=== Test 4: block_next_tool → BLOCK ==="

reset_all
TS4="2026-05-27T06:00:00Z"
write_finding "$TS4" "CRITICAL" "off-mission refactoring" "block_next_tool"

actual_rc=0
fixture_agent | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 2 ]; then
    ok "test 4: block_next_tool → rc=2 (BLOCK)"
else
    bad "test 4: expected rc=2, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 5: halt → BLOCK
# ---------------------------------------------------------------------------
note ""
note "=== Test 5: halt → BLOCK ==="

reset_all
TS5="2026-05-27T07:00:00Z"
write_finding "$TS5" "CRITICAL" "severe drift" "halt"

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 2 ]; then
    ok "test 5: halt → rc=2 (BLOCK)"
else
    bad "test 5: expected rc=2, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 6: Ack via CLI → next dispatch PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 6: Ack via CLI → next dispatch PASS ==="

reset_all
TS6="2026-05-27T08:00:00Z"
write_finding "$TS6" "WARN" "minor scope drift" "surface_to_operator"
EXPECTED_ID6="$(derive_id "$TS6" "$PROJ_NAME" 1)"

ack_exit=0
run_supervise ack "$EXPECTED_ID6" "$PROJ_NAME" || ack_exit=$?

if [ "$ack_exit" -eq 0 ] && [ -f "$ACK_FILE" ]; then
    ok "test 6: ack CLI exited 0 and wrote ack file"
else
    bad "test 6: ack failed (exit=$ack_exit) or no ack file at $ACK_FILE"
fi

# Verify the ack record contains correct fields
if [ -f "$ACK_FILE" ]; then
    ack_rec="$(jq -r --arg fid "$EXPECTED_ID6" 'select(.finding_id == $fid) | .project' \
        "$ACK_FILE" 2>/dev/null || true)"
    if [ "$ack_rec" = "$PROJ_NAME" ]; then
        ok "test 6: ack record has correct project field"
    else
        bad "test 6: ack record missing or wrong project field; got '$ack_rec'"
    fi
fi

# Now the hook should PASS
actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 6: after ack, dispatch PASS (rc=0)"
else
    bad "test 6: expected rc=0 after ack, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 7: ack-all via CLI → next dispatch PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 7: ack-all → next dispatch PASS ==="

reset_all
TS7a="2026-05-27T09:00:00Z"
TS7b="2026-05-27T09:01:00Z"
write_finding "$TS7a" "WARN" "first escalation" "surface_to_operator"
write_finding "$TS7b" "CRITICAL" "second escalation" "halt"

# Both should cause a block before ack-all
actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 2 ]; then
    ok "test 7: two escalations → BLOCK (pre-ack check)"
else
    bad "test 7: expected BLOCK before ack-all; got rc=$actual_rc"
fi

ack_all_exit=0
run_supervise ack-all "$PROJ_NAME" --note "operator confirmed" || ack_all_exit=$?
if [ "$ack_all_exit" -eq 0 ]; then
    ok "test 7: ack-all exited 0"
else
    bad "test 7: ack-all failed (exit=$ack_all_exit)"
fi

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 7: after ack-all, dispatch PASS (rc=0)"
else
    bad "test 7: expected rc=0 after ack-all, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 8: Per-project opt-out (gate_on_escalation: false) → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 8: gate_on_escalation: false → PASS ==="

reset_all
write_finding "2026-05-27T10:00:00Z" "CRITICAL" "severe violation" "halt"

cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
supervise:
  gate_on_escalation: false
YEOF

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 8: gate_on_escalation=false → rc=0 (PASS)"
else
    bad "test 8: expected rc=0 with opt-out, got rc=$actual_rc"
fi

# Restore blank .yakos.yml
cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
YEOF

# ---------------------------------------------------------------------------
# Test 9: Malformed findings file (bad JSON line mixed with good) → WARN + PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 9: Malformed JSON line → WARN + PASS ==="

reset_all
# One good continue finding + one malformed line
write_finding "2026-05-27T11:00:00Z" "PASS" "fine" "continue"
printf 'THIS IS NOT JSON\n' >> "$FINDINGS"

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 9: malformed JSON line → rc=0 (PASS, no false block)"
else
    bad "test 9: expected rc=0 with malformed line, got rc=$actual_rc"
fi

# Verify log was written with a WARN entry for the malformed line
log_file="$WORK_CURRENT/logs/supervisor-ack-gate.ndjson"
if [ -f "$log_file" ]; then
    if jq -e 'select(.severity == "WARN")' "$log_file" >/dev/null 2>&1; then
        ok "test 9: WARN logged for malformed line"
    else
        bad "test 9: expected WARN log entry; log=$log_file"
    fi
else
    bad "test 9: log file not written at $log_file"
fi

# ---------------------------------------------------------------------------
# Test 10: YAKOS_SUPERVISOR_DISABLE=1 → PASS even with unacked escalation
# ---------------------------------------------------------------------------
note ""
note "=== Test 10: YAKOS_SUPERVISOR_DISABLE=1 → PASS ==="

reset_all
write_finding "2026-05-27T12:00:00Z" "CRITICAL" "severe drift" "halt"

actual_rc=0
fixture_teamcreate | \
    YAKOS_SUPERVISOR_DISABLE=1 \
    bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 10: YAKOS_SUPERVISOR_DISABLE=1 → rc=0 (PASS)"
else
    bad "test 10: expected rc=0 with env bypass, got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 11: Non-TeamCreate/Agent tool (Edit) → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 11: Non-TeamCreate/Agent tool (Edit) → PASS ==="

reset_all
write_finding "2026-05-27T13:00:00Z" "CRITICAL" "severe drift" "halt"

actual_rc=0
fixture_edit | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 11: Edit tool → rc=0 (hook skips non-dispatch tools)"
else
    bad "test 11: expected rc=0 for Edit (skipped), got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 12: Multiple pending; ack-all acks all → PASS
# ---------------------------------------------------------------------------
note ""
note "=== Test 12: Multiple pending; ack-all writes N ack records ==="

reset_all
write_finding "2026-05-28T01:00:00Z" "WARN" "first" "surface_to_operator"
write_finding "2026-05-28T01:01:00Z" "CRITICAL" "second" "block_next_tool"
write_finding "2026-05-28T01:02:00Z" "CRITICAL" "third" "halt"

run_supervise ack-all "$PROJ_NAME" >/dev/null || true

# Count ack records for this project
ack_count=0
if [ -f "$ACK_FILE" ]; then
    ack_count="$(jq -r --arg p "$PROJ_NAME" 'select(.project == $p) | .finding_id' \
        "$ACK_FILE" 2>/dev/null | wc -l | tr -d ' ')"
fi
if [ "$ack_count" -ge 3 ]; then
    ok "test 12: ack-all wrote $ack_count ack records (>=3)"
else
    bad "test 12: expected >=3 ack records; got $ack_count"
fi

actual_rc=0
fixture_teamcreate | bash "$HOOKS/supervisor-ack-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "test 12: after ack-all of 3 findings → PASS (rc=0)"
else
    bad "test 12: expected rc=0 after ack-all; got rc=$actual_rc"
fi

# ---------------------------------------------------------------------------
# Test 13: CLI 'ack' with unknown finding-id → error exit
# ---------------------------------------------------------------------------
note ""
note "=== Test 13: ack with unknown finding-id → error ==="

reset_all
write_finding "2026-05-28T02:00:00Z" "WARN" "known finding" "surface_to_operator"

ack_exit=0
run_supervise ack "f-NONEXISTENT-testproject-99" "$PROJ_NAME" || ack_exit=$?
if [ "$ack_exit" -ne 0 ]; then
    ok "test 13: ack with unknown id → non-zero exit ($ack_exit)"
else
    bad "test 13: expected non-zero exit for unknown finding-id; got 0"
fi

# ---------------------------------------------------------------------------
# Test 14: CLI 'pending' shows output before ack, empty after ack-all
# ---------------------------------------------------------------------------
note ""
note "=== Test 14: 'pending' shows findings; empty after ack-all ==="

reset_all
TS14="2026-05-28T03:00:00Z"
write_finding "$TS14" "WARN" "pending test finding" "surface_to_operator"
EXPECTED_ID14="$(derive_id "$TS14" "$PROJ_NAME" 1)"

pending_out="$(run_supervise pending "$PROJ_NAME" 2>&1)"
if printf '%s' "$pending_out" | grep -q "$EXPECTED_ID14"; then
    ok "test 14: 'pending' shows the unacked finding id"
else
    bad "test 14: expected $EXPECTED_ID14 in pending output; got: $pending_out"
fi

# Ack all
run_supervise ack-all "$PROJ_NAME" >/dev/null 2>&1 || true

pending_after="$(run_supervise pending "$PROJ_NAME" 2>&1)"
if printf '%s' "$pending_after" | grep -q "No unacknowledged"; then
    ok "test 14: 'pending' shows empty after ack-all"
else
    bad "test 14: expected 'No unacknowledged' in pending after ack-all; got: $pending_after"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
note ""
note "=== Summary ==="
printf '  %d passed, %d failed\n' "$pass" "$fail"
if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    printf '%b' "$fail_log"
    exit 1
fi
exit 0
