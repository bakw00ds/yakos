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
# Test 3 (K-89): retro.auto_dispatch semantics at the cycle-10 boundary.
# jq's `//` treats boolean false as null, so the old
# '.retro.auto_dispatch // true' read never honored `false`. The hook now
# distinguishes null/absent (default true) from an explicit value.
#   false (bool)   -> disabled  (what `yakos retro disable` writes)
#   "false" string -> disabled
#   absent / null / true / "true" / non-object .retro -> enabled
# ---------------------------------------------------------------------------
note ""
note "=== Test 3: auto_dispatch settings matrix at cycle 10 ==="

# check_auto <label> <settings-json|NONE> <expect: marker|nomarker>
check_auto() {
    local label="$1" json="$2" expect="$3" rc
    reset_all
    printf '9\n' > "$COUNTER"
    if [ "$json" != "NONE" ]; then printf '%s\n' "$json" > "$SETTINGS"; fi
    rc="$(run_hook)"
    if [ "$rc" -ne 0 ]; then bad "test 3 [$label]: expected rc=0, got rc=$rc"; return; fi
    if [ "$expect" = "marker" ]; then
        if [ -f "$MARKER" ]; then ok "test 3 [$label]: marker created"; else bad "test 3 [$label]: expected .retro-due marker"; fi
    else
        if [ -f "$MARKER" ]; then bad "test 3 [$label]: marker must NOT exist (auto_dispatch disabled)"; else ok "test 3 [$label]: no marker"; fi
    fi
}

check_auto "bool false"        '{"retro":{"auto_dispatch":false}}'   nomarker
check_auto "string false"      '{"retro":{"auto_dispatch":"false"}}' nomarker
check_auto "bool true"         '{"retro":{"auto_dispatch":true}}'    marker
check_auto "absent key"        '{"retro":{"cycle_length":10}}'       marker
check_auto "null"              '{"retro":{"auto_dispatch":null}}'    marker
check_auto "no retro object"   '{}'                                  marker
check_auto "no settings file"  NONE                                  marker
check_auto "retro not object"  '{"retro":"oops"}'                    marker
check_auto "malformed json"    '{not json'                           marker

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
# Test 5 (K-106): retro.cycle_length guard. 0 / negative / non-integer /
# empty / absurdly large values used to hit `count % 0` (bash aborted with
# rc=1 under `set -eu`, silently stopping the retro cadence). Every unusable
# value must fall back to the default 10 with exactly ONE WARN naming the
# value, still count, still log, and exit 0. Valid values are honored.
#   check_len <label> <settings-json> <warn-substring|-> <effective-length>
# ---------------------------------------------------------------------------
note ""
note "=== Test 5: cycle_length guard (K-106) ==="

check_len() {
    local label="$1" json="$2" warn="$3" want="$4" rc=0 err warns logged
    reset_all
    rm -f "$LOGS_DIR/cycle-counter.ndjson"
    printf '%s\n' "$json" > "$SETTINGS"
    err="$(fixture_userprompt | bash "$HOOK" 2>&1 >/dev/null)" || rc=$?
    if [ "$rc" -ne 0 ]; then bad "test 5 [$label]: expected rc=0, got rc=$rc"; return; fi
    if [ "$(cat "$COUNTER" 2>/dev/null)" != "1" ]; then bad "test 5 [$label]: counter not written as 1"; return; fi
    warns="$(printf '%s\n' "$err" | grep -c 'WARN' || true)"
    if [ "$warn" = "-" ]; then
        if [ "$warns" != "0" ]; then bad "test 5 [$label]: unexpected WARN: $err"; return; fi
    else
        if [ "$warns" != "1" ]; then bad "test 5 [$label]: expected exactly 1 WARN, got $warns: $err"; return; fi
        case "$err" in
            *"cycle_length $warn"*) : ;;
            *) bad "test 5 [$label]: WARN must name '$warn': $err"; return ;;
        esac
    fi
    logged="$(tail -n 1 "$LOGS_DIR/cycle-counter.ndjson" 2>/dev/null | jq -r '.cycle_length' 2>/dev/null || true)"
    if [ "$logged" != "$want" ]; then bad "test 5 [$label]: logged cycle_length='$logged', want $want"; return; fi
    ok "test 5 [$label]: rc=0, counted, cycle_length=$want, WARN=$warn"
}

check_len "zero"              '{"retro":{"cycle_length":0}}'        0          10
check_len "negative"          '{"retro":{"cycle_length":-1}}'       -1         10
check_len "non-integer str"   '{"retro":{"cycle_length":"abc"}}'   abc        10
check_len "empty string"      '{"retro":{"cycle_length":""}}'       '""'       10
check_len "1e9"               '{"retro":{"cycle_length":1e9}}'      1000000000 10
check_len "above max"         '{"retro":{"cycle_length":100001}}'   100001     10
check_len "fractional"        '{"retro":{"cycle_length":2.5}}'      2.5        10
check_len "bool false"        '{"retro":{"cycle_length":false}}'    false      10
check_len "string zero"       '{"retro":{"cycle_length":"0"}}'      0          10
check_len "null"              '{"retro":{"cycle_length":null}}'     -          10
check_len "missing key"       '{"retro":{}}'                        -          10
check_len "valid 10"          '{"retro":{"cycle_length":10}}'       -          10
check_len "valid 1"           '{"retro":{"cycle_length":1}}'        -          1
check_len "max"               '{"retro":{"cycle_length":100000}}'   -          100000
check_len "leading zeros"     '{"retro":{"cycle_length":"010"}}'    -          10
check_len "octal-looking 08"  '{"retro":{"cycle_length":"08"}}'     -          8

# Cadence stays at the default after a bad value: marker at prompt 10.
reset_all
printf '{"retro":{"cycle_length":0}}\n' > "$SETTINGS"
printf '9\n' > "$COUNTER"
rc="$(run_hook)"
if [ "$rc" = "0" ] && [ -f "$MARKER" ]; then
    ok "test 5 [cadence]: cycle_length=0 keeps default; marker at prompt 10"
else
    bad "test 5 [cadence]: rc=$rc marker=$([ -f "$MARKER" ] && echo yes || echo no)"
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
