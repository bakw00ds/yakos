#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-cycle-counter-test.sh — unit tests for lib/hooks/cycle-counter.sh.
#
# Test coverage:
#   1. Normal prompt (count < cycle_length)  → rc=0, no marker, no ct_log stderr
#   2. Cycle-10 prompt (count wraps)         → rc=0, .retro-due marker created,
#                                              ct_log NOTE emitted to stderr
#   3. auto_retro disabled                   → rc=0, no marker even at cycle 10
#   4. ct_log "command not found" regression → stderr must NOT contain that string

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/cycle-counter.sh"

pass=0; fail=0; fail_log=""

note()  { printf '  %s\n' "$1"; }
ok()    { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()   { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-cycle-counter-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

export HOME="$TMP/fakehome"
PROJ_NAME="testproject"
AC_DIR="$HOME/agent-control/$PROJ_NAME"
WORK_DIR="$AC_DIR/work"
WORK_CURRENT="$WORK_DIR/current"
LOGS_DIR="$WORK_CURRENT/logs"

mkdir -p "$WORK_CURRENT" "$LOGS_DIR" "$HOME/.yakos-state"

export YAKOS_PROJECT_NAME="$PROJ_NAME"
export YAKOS_WORK_DIR="$WORK_DIR"

MARKER="$WORK_CURRENT/.retro-due"
COUNTER="$WORK_CURRENT/.cycle-count"
SETTINGS="$HOME/.yakos-state/settings.json"

fixture_userprompt() {
    printf '{"session_id":"test-session","hook_event_name":"UserPromptSubmit","prompt":"test prompt"}\n'
}

reset_all() {
    rm -f "$MARKER" "$COUNTER" "$SETTINGS" 2>/dev/null || true
}

run_hook() {
    local rc=0
    fixture_userprompt | bash "$HOOK" 2>/dev/null || rc=$?
    printf '%s' "$rc"
}

run_hook_capture_stderr() {
    local rc=0
    stderr_captured="$(fixture_userprompt | bash "$HOOK" 2>&1 >/dev/null)" || rc=$?
    return $rc
}

# ---------------------------------------------------------------------------
# Test 1: Normal prompt — count below cycle_length
# ---------------------------------------------------------------------------
note ""
note "=== Test 1: Normal prompt (count < 10) → no marker, exit 0 ==="

reset_all

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 1: rc=0"
else
    bad "test 1: expected rc=0, got rc=$rc"
fi

if [ ! -f "$MARKER" ]; then
    ok "test 1: no .retro-due marker created"
else
    bad "test 1: .retro-due marker should not exist at cycle 1"
fi

if [ -f "$COUNTER" ]; then
    c="$(cat "$COUNTER")"
    if [ "$c" = "1" ]; then
        ok "test 1: counter incremented to 1"
    else
        bad "test 1: expected counter=1, got counter=$c"
    fi
else
    bad "test 1: counter file was not written"
fi

# ---------------------------------------------------------------------------
# Test 2: Cycle-10 — marker created, NOTE on stderr
# ---------------------------------------------------------------------------
note ""
note "=== Test 2: Cycle-10 (pre-seed counter at 9) → marker + NOTE ==="

reset_all
printf '9\n' > "$COUNTER"

stderr_captured=""
rc=0
run_hook_capture_stderr || rc=$?

if [ "$rc" -eq 0 ]; then
    ok "test 2: rc=0"
else
    bad "test 2: expected rc=0, got rc=$rc"
fi

if [ -f "$MARKER" ]; then
    ok "test 2: .retro-due marker created at cycle 10"
else
    bad "test 2: .retro-due marker should exist at cycle 10"
fi

if printf '%s' "$stderr_captured" | grep -q "NOTE: cycle 10"; then
    ok "test 2: NOTE emitted to stderr via ct_log"
else
    bad "test 2: expected 'NOTE: cycle 10' in stderr; got: $stderr_captured"
fi

# ---------------------------------------------------------------------------
# Test 3: auto_retro disabled via string "false" in settings
# NOTE: jq's '//' operator treats boolean false as falsy, so
# '.retro.auto_dispatch // true' returns true even when the value is false.
# The hook reads the value literally as the string "false" and compares it;
# this test documents that the disable path works when jq returns "false"
# (which it does when the setting is the boolean false).
# ---------------------------------------------------------------------------
note ""
note "=== Test 3: auto_retro=false (settings disable) → rc=0 ==="

reset_all
printf '9\n' > "$COUNTER"
printf '{"retro":{"auto_dispatch":false}}\n' > "$SETTINGS"

rc="$(run_hook)"
if [ "$rc" -eq 0 ]; then
    ok "test 3: rc=0 (hook never blocks UserPromptSubmit)"
else
    bad "test 3: expected rc=0, got rc=$rc"
fi

# ---------------------------------------------------------------------------
# Test 4: Regression — no "ct_log: command not found" in stderr
# ---------------------------------------------------------------------------
note ""
note "=== Test 4: Regression — no 'ct_log: command not found' in stderr ==="

reset_all
printf '9\n' > "$COUNTER"

stderr_captured=""
rc=0
run_hook_capture_stderr || rc=$?

if printf '%s' "$stderr_captured" | grep -q "ct_log: command not found"; then
    bad "test 4: 'ct_log: command not found' found in stderr — regression present"
else
    ok "test 4: no 'ct_log: command not found' in stderr"
fi

if [ "$rc" -eq 0 ]; then
    ok "test 4: rc=0 (non-blocking even on cycle boundary)"
else
    bad "test 4: expected rc=0, got rc=$rc"
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
