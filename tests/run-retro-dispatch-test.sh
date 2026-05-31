#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-retro-dispatch-test.sh — unit tests for lib/hooks/retro-dispatch.sh.
#
# Test coverage:
#   1.  No marker present                            → no-op (rc=0, no dispatch)
#   2.  Marker present + auto_dispatch disabled      → no-op (marker left)
#   3.  Marker present + prior dispatch in-flight    → skip (rc=0, marker left)
#   4.  Marker present + normal dispatch             → dispatch fired, marker removed
#   5.  Marker present + yakos binary not in PATH    → WARN + marker left (rc=0)
#   6.  Marker present + stale PID file              → dispatch fires (stale PID cleaned up)
#
# The 'yakos' binary is mocked via a fake script added to PATH. The fake
# records calls to a file so we can assert dispatch happened (or didn't).

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/retro-dispatch.sh"

pass=0; fail=0; fail_log=""

note()  { printf '  %s\n' "$1"; }
ok()    { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()   { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
#
# Layout:
#   TMP/fakehome/                      — overridden HOME
#   TMP/fakehome/.yakos-state/         — settings.json, pid files
#   TMP/fakehome/agent-control/testproject/work/
#       current/                       — .retro-due marker, logs/
#   TMP/fake-project/                  — CLAUDE_PROJECT_DIR
#   TMP/bin/yakos                      — mock yakos binary
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-retro-dispatch-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

ORIG_PATH="$PATH"

export HOME="$TMP/fakehome"
PROJ_NAME="testproject"
AC_DIR="$HOME/agent-control/$PROJ_NAME"
WORK_DIR="$AC_DIR/work"
WORK_CURRENT="$WORK_DIR/current"
LOGS_DIR="$WORK_CURRENT/logs"
FAKE_REPO="$TMP/fake-project"
MOCK_BIN="$TMP/bin"

# Dispatch call recorder — written by the mock yakos binary.
YAKOS_DISPATCH_LOG="$TMP/yakos-dispatch-calls.log"
export YAKOS_DISPATCH_LOG

mkdir -p "$WORK_CURRENT" "$LOGS_DIR" "$FAKE_REPO" "$HOME/.yakos-state" "$MOCK_BIN"

export CLAUDE_PROJECT_DIR="$FAKE_REPO"
export YAKOS_PROJECT_NAME="$PROJ_NAME"
export YAKOS_WORK_DIR="$WORK_DIR"
# Put mock bin first in PATH so 'yakos' resolves to our mock.
export PATH="$MOCK_BIN:$ORIG_PATH"

MARKER="$WORK_CURRENT/.retro-due"
SETTINGS="$HOME/.yakos-state/settings.json"
PID_FILE="$HOME/.yakos-state/retro-dispatch.pid"
HOOK_LOG="$LOGS_DIR/retro-dispatch.ndjson"

# ---------------------------------------------------------------------------
# Mock yakos binary — records each call (with args) to YAKOS_DISPATCH_LOG.
# ---------------------------------------------------------------------------

cat > "$MOCK_BIN/yakos" <<'MOCK'
#!/usr/bin/env bash
# Mock yakos: record call and exit 0.
printf 'called: %s\n' "$*" >> "${YAKOS_DISPATCH_LOG}"
exit 0
MOCK
chmod +x "$MOCK_BIN/yakos"

# ---------------------------------------------------------------------------
# Fixture: UserPromptSubmit event stdin payload
# ---------------------------------------------------------------------------

fixture_userprompt() {
    printf '{"session_id":"test-session","hook_event_name":"UserPromptSubmit","prompt":"test prompt"}\n'
}

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

reset_all() {
    rm -f "$MARKER" "$SETTINGS" "$PID_FILE" "$HOOK_LOG" "$YAKOS_DISPATCH_LOG" 2>/dev/null || true
    touch "$YAKOS_DISPATCH_LOG"
}

# Count how many times yakos dispatch was called in the mock log.
dispatch_count() {
    local cnt
    cnt="$(grep -c 'called:' "$YAKOS_DISPATCH_LOG" 2>/dev/null)" || cnt=0
    printf '%s' "$cnt"
}

run_hook() {
    local rc=0
    fixture_userprompt | bash "$HOOK" 2>/dev/null || rc=$?
    printf '%s' "$rc"
}

# ---------------------------------------------------------------------------
# Test 1: No marker → no-op, no dispatch
# ---------------------------------------------------------------------------
note ""
note "=== Test 1: No marker present → no-op ==="

reset_all
# Do NOT create the marker.

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 1: rc=0 (no-op)"
else
    bad "test 1: expected rc=0, got rc=$rc"
fi

cnt="$(dispatch_count)"
if [ "$cnt" -eq 0 ]; then
    ok "test 1: no dispatch fired"
else
    bad "test 1: expected 0 dispatches; got $cnt"
fi

# ---------------------------------------------------------------------------
# Test 2: Marker present + auto_dispatch disabled → no-op (marker left)
# ---------------------------------------------------------------------------
note ""
note "=== Test 2: Marker present + auto_dispatch=false → no-op ==="

reset_all
touch "$MARKER"
printf '{"retro":{"auto_dispatch":false}}\n' > "$SETTINGS"

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 2: rc=0"
else
    bad "test 2: expected rc=0, got rc=$rc"
fi

cnt="$(dispatch_count)"
if [ "$cnt" -eq 0 ]; then
    ok "test 2: no dispatch fired"
else
    bad "test 2: expected 0 dispatches; got $cnt"
fi

if [ -f "$MARKER" ]; then
    ok "test 2: marker left in place (auto_dispatch disabled)"
else
    bad "test 2: marker should remain when disabled; it was removed"
fi

# ---------------------------------------------------------------------------
# Test 3: Marker present + prior dispatch in-flight → skip (marker left)
# ---------------------------------------------------------------------------
note ""
note "=== Test 3: Marker present + in-flight PID → skip ==="

reset_all
touch "$MARKER"
# Use the current test process's PID — guaranteed alive.
printf '%s\n' "$$" > "$PID_FILE"

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 3: rc=0"
else
    bad "test 3: expected rc=0, got rc=$rc"
fi

cnt="$(dispatch_count)"
if [ "$cnt" -eq 0 ]; then
    ok "test 3: no dispatch fired (in-flight guard triggered)"
else
    bad "test 3: expected 0 dispatches; got $cnt"
fi

if [ -f "$MARKER" ]; then
    ok "test 3: marker left in place"
else
    bad "test 3: marker should remain when skipped; it was removed"
fi

# Verify log was written with an in-flight-related entry.
if [ -f "$HOOK_LOG" ] && grep -q 'in.flight\|in_flight\|skipped' "$HOOK_LOG" 2>/dev/null; then
    ok "test 3: in-flight skip reason logged"
else
    bad "test 3: expected in-flight log entry in $HOOK_LOG"
fi

# ---------------------------------------------------------------------------
# Test 4: Marker present + normal path → dispatch fired, marker removed
# ---------------------------------------------------------------------------
note ""
note "=== Test 4: Marker present + normal dispatch → dispatch fires ==="

reset_all
touch "$MARKER"

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 4: rc=0"
else
    bad "test 4: expected rc=0, got rc=$rc"
fi

# Give background subprocess a moment to complete and write the call log.
sleep 0.5

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test 4: dispatch fired ($cnt call(s) recorded)"
else
    bad "test 4: expected >=1 dispatch; got $cnt (log: $(cat "$YAKOS_DISPATCH_LOG" 2>/dev/null || echo empty))"
fi

if grep -q 'librarian' "$YAKOS_DISPATCH_LOG" 2>/dev/null; then
    ok "test 4: dispatch called with 'librarian' agent"
else
    bad "test 4: 'librarian' not found in dispatch call log (log: $(cat "$YAKOS_DISPATCH_LOG" 2>/dev/null))"
fi

if [ ! -f "$MARKER" ]; then
    ok "test 4: marker removed after dispatch"
else
    bad "test 4: marker should be removed after successful dispatch"
fi

# Verify hook log was written with 'dispatched' decision.
# Use grep to scan all NDJSON lines (background + main process writes may interleave).
if [ -f "$HOOK_LOG" ]; then
    if grep -q '"dispatched"' "$HOOK_LOG" 2>/dev/null; then
        ok "test 4: hook log records 'dispatched' decision"
    else
        bad "test 4: expected 'dispatched' in hook log; got: $(cat "$HOOK_LOG" 2>/dev/null)"
    fi
else
    bad "test 4: no hook log written"
fi

# ---------------------------------------------------------------------------
# Test 5: Marker present + yakos binary not in PATH → WARN + marker left (rc=0)
# ---------------------------------------------------------------------------
note ""
note "=== Test 5: yakos not in PATH → WARN + marker left ==="

reset_all
touch "$MARKER"

# Build a PATH with only system essentials (so bash internals and date/jq work)
# but without our mock yakos binary. Also unset YAKOS_ROOT so the fallback
# $YAKOS_ROOT/cli/yakos path is not found either.
SYSTEM_BIN_ONLY="/usr/bin:/bin"

rc=0
# shellcheck disable=SC2030,SC2031
fixture_userprompt | \
    PATH="$SYSTEM_BIN_ONLY" \
    YAKOS_ROOT="" \
    bash "$HOOK" 2>/dev/null || rc=$?
if [ "$rc" -eq 0 ]; then
    ok "test 5: rc=0 (UserPromptSubmit never blocked)"
else
    bad "test 5: expected rc=0 (hook must always exit 0); got rc=$rc"
fi

if [ -f "$MARKER" ]; then
    ok "test 5: marker left in place when yakos not found"
else
    bad "test 5: marker should remain when dispatch cannot run"
fi

# ---------------------------------------------------------------------------
# Test 6: Marker present + stale PID file → dispatch fires after cleanup
# ---------------------------------------------------------------------------
note ""
note "=== Test 6: Stale PID file → dispatch fires after cleanup ==="

reset_all
touch "$MARKER"

# Write a PID that is definitely not alive: use PID 2 (kthreadd on Linux, which
# is not our process) or find a genuinely dead PID. The safest cross-platform
# approach: start a short-lived subprocess, record its PID, wait for it to end.
(exit 0) &
dead_pid=$!
wait "$dead_pid" 2>/dev/null || true
# $dead_pid is now dead.
printf '%s\n' "$dead_pid" > "$PID_FILE"

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 6: rc=0"
else
    bad "test 6: expected rc=0, got rc=$rc"
fi

# Give background subprocess a moment.
sleep 0.5

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test 6: dispatch fired after stale PID cleanup"
else
    bad "test 6: expected dispatch after stale PID; got $cnt dispatches (log: $(cat "$YAKOS_DISPATCH_LOG" 2>/dev/null))"
fi

if [ ! -f "$MARKER" ]; then
    ok "test 6: marker removed"
else
    bad "test 6: marker should be removed after successful dispatch"
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
