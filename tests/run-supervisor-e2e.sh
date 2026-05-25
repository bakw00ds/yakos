#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-e2e.sh — end-to-end tests for the supervisor system.
#
# Verifies (without actually invoking a real LLM dispatch — that would
# cost real API money) that:
#   1. supervisor-stream.sh appends to the buffer on every tool call
#   2. supervisor-stream.sh respects the YAKOS_SUPERVISOR_DISABLE env
#   3. supervisor-stream.sh respects .yakos.yml supervisor.enabled: false
#   4. supervisor-stream.sh increments the counter and forks at N
#   5. supervisor-gate.sh allows on PASS findings
#   6. supervisor-gate.sh surfaces WARN findings via stderr
#   7. supervisor-gate.sh blocks (exit 2) on CRITICAL findings
#   8. supervisor-gate.sh respects YAKOS_SUPERVISOR_DISABLE env
#   9. supervisor-gate.sh respects block_on_critical: false (passive)
#  10. supervisor-gate.sh honors hook-bypass.md scope=finding=<ts>

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"

pass=0; fail=0; fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# --- shared test fixtures ---------------------------------------------------

TMP="$(mktemp -d -t yakos-sup-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

FAKE_REPO="$TMP/project"
mkdir -p "$FAKE_REPO/work/current/logs"
export CLAUDE_PROJECT_DIR="$FAKE_REPO"
export YAKOS_INPLACE_WORK=1
export YAKOS_PROJECT_NAME=supe2e

BUFFER="$FAKE_REPO/work/current/supervisor-buffer.ndjson"
COUNTER="$FAKE_REPO/work/current/.supervisor-counter"
FINDINGS="$FAKE_REPO/work/current/supervisor-findings.ndjson"
MARKER="$FAKE_REPO/work/current/.supervisor-gate-last-surfaced"

fixture_edit() {
    jq -nc --arg sid "test-session" --arg agent "$1" --arg file "$2" \
        '{session_id: $sid, tool_name: "Edit", agent_type: $agent,
          hook_event_name: "PostToolUse",
          tool_input: {file_path: $file, old_string: "x", new_string: "y"}}'
}
fixture_pretool() {
    jq -nc --arg sid "test-session" --arg agent "$1" --arg file "$2" \
        '{session_id: $sid, tool_name: "Edit", agent_type: $agent,
          hook_event_name: "PreToolUse",
          tool_input: {file_path: $file, old_string: "x", new_string: "y"}}'
}

write_finding() {
    # write_finding <ts> <overall> <rationale> <recommended_action>
    jq -nc --arg ts "$1" --arg overall "$2" --arg r "$3" --arg rec "$4" \
        '{ts: $ts, batch_size: 10, scores: {intent_alignment: $overall,
          factual_accuracy: "PASS", hard_control_respect: "PASS", scope_risk: "PASS"},
          overall: $overall, rationale: $r, recommended_action: $rec}' \
        >> "$FINDINGS"
}

# === Scenario 1: stream appends + counter increments ========================
note ""
note "=== Scenario 1: stream appends to buffer + increments counter ==="

# Ensure no .yakos.yml — hook should default to enabled (i.e., run)
rm -f "$FAKE_REPO/.yakos.yml" "$BUFFER" "$COUNTER" "$FINDINGS"

for i in 1 2 3; do
    fixture_edit go-api "api/file$i.go" | bash "$HOOKS/supervisor-stream.sh" >/dev/null 2>&1
done
if [ -f "$BUFFER" ] && [ "$(wc -l < "$BUFFER" | tr -d ' ')" = "3" ]; then
    ok "scenario 1: buffer has 3 entries"
else
    bad "scenario 1: buffer should have 3 entries; has $(wc -l < "$BUFFER" 2>/dev/null || echo 0)"
fi
if [ "$(cat "$COUNTER" 2>/dev/null)" = "3" ]; then
    ok "scenario 1: counter is 3"
else
    bad "scenario 1: counter should be 3; is $(cat "$COUNTER" 2>/dev/null)"
fi

# === Scenario 2: YAKOS_SUPERVISOR_DISABLE skips entirely ====================
note ""
note "=== Scenario 2: YAKOS_SUPERVISOR_DISABLE skips stream ==="

rm -f "$BUFFER" "$COUNTER"
fixture_edit go-api "api/x.go" \
    | YAKOS_SUPERVISOR_DISABLE=1 bash "$HOOKS/supervisor-stream.sh" >/dev/null 2>&1
if [ ! -f "$BUFFER" ]; then
    ok "scenario 2: buffer not created when disabled via env"
else
    bad "scenario 2: buffer was created despite YAKOS_SUPERVISOR_DISABLE=1"
fi

# === Scenario 3: .yakos.yml supervisor.enabled: false skips =================
note ""
note "=== Scenario 3: .yakos.yml supervisor.enabled: false skips stream ==="

rm -f "$BUFFER" "$COUNTER"
cat > "$FAKE_REPO/.yakos.yml" <<'EOF'
yakos: 0.9
supervisor:
  enabled: false
EOF
fixture_edit go-api "api/x.go" | bash "$HOOKS/supervisor-stream.sh" >/dev/null 2>&1
if [ ! -f "$BUFFER" ]; then
    ok "scenario 3: buffer not created when supervisor.enabled: false"
else
    bad "scenario 3: buffer was created despite supervisor.enabled: false"
fi

# Re-enable for the rest of the test
cat > "$FAKE_REPO/.yakos.yml" <<'EOF'
yakos: 0.9
supervisor:
  enabled: true
  score_every_n_calls: 5
  block_on_critical: true
EOF

# === Scenario 4: counter modulo triggers fork attempt =======================
note ""
note "=== Scenario 4: counter modulo N forks supervisor dispatch ==="

rm -f "$BUFFER" "$COUNTER" "$FAKE_REPO/work/current/.supervisor-stdout.log" \
    "$FAKE_REPO/work/current/.supervisor-stderr.log"

# Use a fake yakos CLI that just logs its invocation; this lets us
# verify the fork happens without spending real API money.
FAKE_CLI="$TMP/fake-yakos"
cat > "$FAKE_CLI" <<'EOF'
#!/usr/bin/env bash
echo "FAKE: $*" >> "$YAKOS_FAKE_INVOCATIONS"
EOF
chmod +x "$FAKE_CLI"
export YAKOS_FAKE_INVOCATIONS="$TMP/fake-invocations.log"
: > "$YAKOS_FAKE_INVOCATIONS"
export YAKOS_CLI="$FAKE_CLI"

# Stream 5 events (score_every_n_calls=5, so the 5th should fork)
for i in 1 2 3 4 5; do
    fixture_edit go-api "api/f$i.go" | bash "$HOOKS/supervisor-stream.sh" >/dev/null 2>&1
done
sleep 1  # give the background fork a moment to run

if [ -s "$YAKOS_FAKE_INVOCATIONS" ]; then
    inv="$(cat "$YAKOS_FAKE_INVOCATIONS")"
    if printf '%s' "$inv" | grep -q 'dispatch supervisor'; then
        ok "scenario 4: supervisor dispatch was forked on the 5th call"
    else
        bad "scenario 4: a fork happened but didn't dispatch the supervisor agent: $inv"
    fi
else
    bad "scenario 4: no fork happened on the 5th call"
fi

# === Scenario 5: gate allows on PASS =======================================
note ""
note "=== Scenario 5: gate allows on PASS findings ==="

rm -f "$FINDINGS" "$MARKER"
write_finding "$(date -u +%Y-%m-%dT%H:%M:%SZ)" PASS "all good" continue

actual_rc=0
fixture_pretool go-api "api/y.go" | bash "$HOOKS/supervisor-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "scenario 5: gate allowed on PASS (rc=0)"
else
    bad "scenario 5: gate should have allowed PASS but got rc=$actual_rc"
fi

# === Scenario 6: gate surfaces WARN via stderr but allows ===================
note ""
note "=== Scenario 6: gate surfaces WARN but allows ==="

rm -f "$FINDINGS" "$MARKER"
write_finding "$(date -u +%Y-%m-%dT%H:%M:%SZ)" WARN "minor scope expansion" surface_to_operator

stderr_out="$(fixture_pretool go-api "api/y.go" | bash "$HOOKS/supervisor-gate.sh" 2>&1)"
rc=$?
if [ "$rc" -eq 0 ] && printf '%s' "$stderr_out" | grep -q WARN; then
    ok "scenario 6: gate surfaced WARN to stderr + allowed (rc=0)"
else
    bad "scenario 6: rc=$rc, stderr=$stderr_out"
fi

# === Scenario 7: gate BLOCKS on CRITICAL (block_on_critical: true) ==========
note ""
note "=== Scenario 7: gate blocks (exit 2) on CRITICAL ==="

rm -f "$FINDINGS" "$MARKER"
write_finding "$(date -u +%Y-%m-%dT%H:%M:%SZ)" CRITICAL "off-mission refactoring" halt

actual_rc=0
fixture_pretool go-api "api/y.go" | bash "$HOOKS/supervisor-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 2 ]; then
    ok "scenario 7: gate blocked on CRITICAL (rc=2 as expected)"
else
    bad "scenario 7: gate should have rc=2 (block); got rc=$actual_rc"
fi

# === Scenario 8: YAKOS_SUPERVISOR_DISABLE bypasses gate ====================
note ""
note "=== Scenario 8: YAKOS_SUPERVISOR_DISABLE bypasses gate even on CRITICAL ==="

actual_rc=0
fixture_pretool go-api "api/y.go" \
    | YAKOS_SUPERVISOR_DISABLE=1 bash "$HOOKS/supervisor-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "scenario 8: env override allowed despite CRITICAL"
else
    bad "scenario 8: env override should rc=0; got $actual_rc"
fi

# === Scenario 9: block_on_critical: false → surface but don't block ========
note ""
note "=== Scenario 9: block_on_critical: false → CRITICAL surfaces but allows ==="

cat > "$FAKE_REPO/.yakos.yml" <<'EOF'
yakos: 0.9
supervisor:
  enabled: true
  block_on_critical: false
EOF

actual_rc=0
stderr_out="$(fixture_pretool go-api "api/y.go" | bash "$HOOKS/supervisor-gate.sh" 2>&1)" || actual_rc=$?
if [ "$actual_rc" -eq 0 ] && printf '%s' "$stderr_out" | grep -q CRITICAL; then
    ok "scenario 9: passive mode allowed CRITICAL with stderr"
else
    bad "scenario 9: rc=$actual_rc, stderr=$stderr_out"
fi

# Restore active mode
cat > "$FAKE_REPO/.yakos.yml" <<'EOF'
yakos: 0.9
supervisor:
  enabled: true
  block_on_critical: true
EOF

# === Scenario 10: hook-bypass.md scope=finding=<ts> bypasses block ==========
note ""
note "=== Scenario 10: hook-bypass.md with finding=<ts> bypasses block ==="

rm -f "$FINDINGS" "$MARKER"
finding_ts="2026-05-22T20:00:00Z"
write_finding "$finding_ts" CRITICAL "drift detected" halt

cat > "$FAKE_REPO/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:test-finding-override

**Hook:** supervisor
**Reason:** test scenario 10
**Approved by:** test
**Created:** 2026-05-22T19:00:00Z
**Expires:** 2026-05-22T22:00:00Z
**Scope:** finding=$finding_ts
**Follow-up:** delete after test
EOF

actual_rc=0
fixture_pretool go-api "api/y.go" | bash "$HOOKS/supervisor-gate.sh" >/dev/null 2>&1 || actual_rc=$?
if [ "$actual_rc" -eq 0 ]; then
    ok "scenario 10: bypass entry allowed past CRITICAL block"
else
    bad "scenario 10: bypass should have allowed; got rc=$actual_rc"
fi

# === Summary ================================================================
note ""
note "=== Summary ==="
printf '  %d passed, %d failed\n' "$pass" "$fail"
if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    printf '%b' "$fail_log"
    exit 1
fi
exit 0
