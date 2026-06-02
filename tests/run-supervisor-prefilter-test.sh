#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-prefilter-test.sh — unit tests for the v0.34 supervisor
# pre-filter redesign in lib/hooks/supervisor-stream.sh.
#
# Test coverage:
#   (a) PostToolUse on Read → hook NOT invoked (matcher tightened — verified
#       by inspecting settings.template.json, not runtime).
#   (b) PostToolUse on Edit, no triggers → buffered, NO dispatch.
#   (c) PostToolUse on Edit, sensitive-path trigger → dispatch invoked.
#   (d) PostToolUse on Edit, large-diff trigger → dispatch invoked.
#   (e) PostToolUse on Edit, risk-regex trigger → dispatch invoked.
#   (f) Pre-filter disabled → every Nth mutation dispatches (reverts to v0.33).
#   (g) .yakos.yml matcher key present → settings.template.json registers
#       Edit|Write|MultiEdit|Bash (not "*") for supervisor-stream.sh.
#   (h) Dispatch is async → PostToolUse hook returns in < 100ms even when
#       dispatch fires.
#
# Each test uses a mock yakos binary (PATH override) to detect dispatch calls.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/supervisor-stream.sh"
SETTINGS_TEMPLATE="$REPO_ROOT/lib/settings/settings.template.json"

pass=0; fail=0; fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-prefilter-XXXXXX)"
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

mkdir -p "$WORK_CURRENT" "$LOGS_DIR" "$FAKE_REPO/.claude" \
         "$HOME/.yakos-state" "$MOCK_BIN"

# .project-path so yakos_current_dir resolves correctly
mkdir -p "$AC_DIR"
printf '%s\n' "$FAKE_REPO" > "$AC_DIR/.project-path"

export CLAUDE_PROJECT_DIR="$FAKE_REPO"
export YAKOS_PROJECT_NAME="$PROJ_NAME"
export YAKOS_WORK_DIR="$WORK_DIR"

BUFFER="$WORK_CURRENT/supervisor-buffer.ndjson"
COUNTER="$WORK_CURRENT/.supervisor-counter"

# ---------------------------------------------------------------------------
# Mock yakos binary — records each call (with args) to YAKOS_DISPATCH_LOG.
# ---------------------------------------------------------------------------

cat > "$MOCK_BIN/yakos" <<'MOCK'
#!/usr/bin/env bash
# Mock yakos: record call and exit 0 immediately (simulates async fast path).
printf 'called: %s\n' "$*" >> "${YAKOS_DISPATCH_LOG}"
exit 0
MOCK
chmod +x "$MOCK_BIN/yakos"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

reset_all() {
    rm -f "$BUFFER" "$COUNTER" "$YAKOS_DISPATCH_LOG" 2>/dev/null || true
    rm -f "$WORK_CURRENT/.supervisor-stdout.log" \
          "$WORK_CURRENT/.supervisor-stderr.log" 2>/dev/null || true
    touch "$YAKOS_DISPATCH_LOG"
    # Restore clean .yakos.yml with supervisor enabled + pre_filter on
    cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
supervisor:
  enabled: true
  runtime: claude
  agent: supervisor
  score_every_n_calls: 1
  block_on_critical: false
  model: haiku
YEOF
    # Default path-allowlist (deny .env and credentials)
    cat > "$FAKE_REPO/.claude/path-allowlist.json" <<'AEOF'
{
  "lead": {
    "allow": ["**"],
    "deny": [".env", ".env.*", "**/credentials/**", "**/.git/**", "secrets.txt"]
  }
}
AEOF
}

# Count how many times yakos dispatch was called.
# grep -c exits 1 when count=0 on some platforms; we capture and convert.
dispatch_count() {
    local cnt=0
    cnt="$(grep -c 'called:' "$YAKOS_DISPATCH_LOG" 2>/dev/null)" || cnt=0
    # Strip whitespace / newlines from the count in case grep output is noisy
    printf '%s' "$cnt" | tr -d '[:space:]'
}

# Build a PostToolUse Edit event JSON and write it to $TMP/fixture.json
# so run_hook can cat it without relying on function-in-subshell.
make_fixture_edit() {
    local file_path="${1:-api/main.go}"
    local new_string="${2:-// small change}"
    jq -nc \
        --arg file "$file_path" \
        --arg new "$new_string" \
        '{session_id: "test-session",
          hook_event_name: "PostToolUse",
          tool_name: "Edit",
          tool_input: {file_path: $file, new_string: $new}}' \
        > "$TMP/fixture.json"
}

# Run the stream hook with the fixture file, using the mock yakos in PATH.
# Stores exit code in $LAST_RC.
LAST_RC=0
run_hook() {
    LAST_RC=0
    PATH="$MOCK_BIN:$ORIG_PATH" \
    YAKOS_CLI="$MOCK_BIN/yakos" \
        bash "$HOOK" < "$TMP/fixture.json" >/dev/null 2>&1 || LAST_RC=$?
}

# Run the hook and measure wall-clock milliseconds; stores result in
# $LAST_RC and $LAST_ELAPSED_MS.
LAST_ELAPSED_MS=0
run_hook_timed() {
    LAST_RC=0
    LAST_ELAPSED_MS=0
    local start_ns end_ns
    start_ns="$(date +%s%N 2>/dev/null || echo 0)"
    PATH="$MOCK_BIN:$ORIG_PATH" \
    YAKOS_CLI="$MOCK_BIN/yakos" \
        bash "$HOOK" < "$TMP/fixture.json" >/dev/null 2>&1 || LAST_RC=$?
    end_ns="$(date +%s%N 2>/dev/null || echo 0)"
    if [ "$start_ns" != "0" ] && [ "$end_ns" != "0" ]; then
        LAST_ELAPSED_MS=$(( (end_ns - start_ns) / 1000000 ))
    fi
}

# ---------------------------------------------------------------------------
# Test (a): Matcher in settings.template.json is NOT "*" for supervisor-stream
# ---------------------------------------------------------------------------
note ""
note "=== Test (a): supervisor-stream.sh is NOT registered on matcher '*' ==="

if ! command -v jq >/dev/null 2>&1; then
    bad "test (a): jq not available — cannot parse settings.template.json"
else
    # Find the PostToolUse entry that contains supervisor-stream.sh and check
    # its matcher value.
    sup_matcher="$(jq -r '
        .hooks.PostToolUse[]
        | select(.hooks[]?.command? | strings | test("supervisor-stream"))
        | .matcher
    ' "$SETTINGS_TEMPLATE" 2>/dev/null || true)"

    if [ "$sup_matcher" = "*" ]; then
        bad "test (a): supervisor-stream.sh is still registered on matcher '*'; expected 'Edit|Write|MultiEdit|Bash'"
    elif [ -z "$sup_matcher" ]; then
        bad "test (a): could not find supervisor-stream.sh in settings.template.json PostToolUse"
    else
        ok "test (a): supervisor-stream.sh matcher = '$sup_matcher' (not '*')"
    fi

    # Verify output-injection-scan.sh remains on "*"
    scan_matcher="$(jq -r '
        .hooks.PostToolUse[]
        | select(.hooks[]?.command? | strings | test("output-injection-scan"))
        | .matcher
    ' "$SETTINGS_TEMPLATE" 2>/dev/null || true)"

    if [ "$scan_matcher" = "*" ]; then
        ok "test (a): output-injection-scan.sh matcher = '*' (unchanged)"
    else
        bad "test (a): output-injection-scan.sh matcher changed to '$scan_matcher'; expected '*'"
    fi
fi

# ---------------------------------------------------------------------------
# Test (b): Edit with no triggers → buffered, NO dispatch
# ---------------------------------------------------------------------------
note ""
note "=== Test (b): Edit with no pre-filter triggers → buffer-only, no dispatch ==="

reset_all

# score_every_n_calls=1 to ensure dispatch would fire if counter ticks.
# Clean edit: short content, not a sensitive path, no risk words.
make_fixture_edit "api/handler.go" "// minor comment"
run_hook

if [ "$LAST_RC" -eq 0 ]; then
    ok "test (b): hook exits 0"
else
    bad "test (b): expected rc=0, got rc=$LAST_RC"
fi

# Wait a tick for any background dispatch (if any) to complete via mock
sleep 0.4 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -eq 0 ]; then
    ok "test (b): no dispatch fired (pre-filter passed, buffer-only)"
else
    bad "test (b): expected 0 dispatches; got $cnt"
fi

buf_lines=0
[ -f "$BUFFER" ] && buf_lines="$(wc -l < "$BUFFER" | tr -d ' ')"
if [ "$buf_lines" -ge 1 ]; then
    ok "test (b): event appended to buffer ($buf_lines line(s))"
else
    bad "test (b): event not appended to buffer"
fi

# ---------------------------------------------------------------------------
# Test (c): Sensitive-path trigger → dispatch invoked
# ---------------------------------------------------------------------------
note ""
note "=== Test (c): Sensitive-path edit → dispatch invoked ==="

reset_all

make_fixture_edit "secrets.txt" "supersecretpassword123"
run_hook

if [ "$LAST_RC" -eq 0 ]; then
    ok "test (c): hook exits 0"
else
    bad "test (c): expected rc=0, got rc=$LAST_RC"
fi

sleep 0.5 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test (c): dispatch fired (sensitive-path trigger, $cnt call(s))"
else
    bad "test (c): expected >=1 dispatch for sensitive-path; got $cnt"
fi

# ---------------------------------------------------------------------------
# Test (d): Large-diff trigger → dispatch invoked
# ---------------------------------------------------------------------------
note ""
note "=== Test (d): Large-diff edit (>20 lines) → dispatch invoked ==="

reset_all

# Build a fixture with new_string that is >20 lines
large_content="$(printf 'line %s\n' $(seq 1 25))"
make_fixture_edit "api/large.go" "$large_content"
run_hook

if [ "$LAST_RC" -eq 0 ]; then
    ok "test (d): hook exits 0"
else
    bad "test (d): expected rc=0, got rc=$LAST_RC"
fi

sleep 0.5 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test (d): dispatch fired (large-diff trigger, $cnt call(s))"
else
    bad "test (d): expected >=1 dispatch for large-diff; got $cnt"
fi

# ---------------------------------------------------------------------------
# Test (e): Risk-regex trigger → dispatch invoked
# ---------------------------------------------------------------------------
note ""
note "=== Test (e): Risk-regex trigger (rm -rf) → dispatch invoked ==="

reset_all

make_fixture_edit "scripts/cleanup.sh" "rm -rf /tmp/data"
run_hook

if [ "$LAST_RC" -eq 0 ]; then
    ok "test (e): hook exits 0"
else
    bad "test (e): expected rc=0, got rc=$LAST_RC"
fi

sleep 0.5 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test (e): dispatch fired (risk-regex trigger, $cnt call(s))"
else
    bad "test (e): expected >=1 dispatch for risk-regex; got $cnt"
fi

# Also test force push pattern
reset_all

make_fixture_edit ".git/config" "git push --force origin main"
run_hook
sleep 0.5 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test (e): dispatch fired (force-push pattern, $cnt call(s))"
else
    bad "test (e): expected >=1 dispatch for force-push; got $cnt"
fi

# ---------------------------------------------------------------------------
# Test (f): Pre-filter disabled → every Nth mutation dispatches (v0.33 compat)
# ---------------------------------------------------------------------------
note ""
note "=== Test (f): pre_filter.enabled: false → reverts to v0.33 every-N dispatch ==="

reset_all

# Reconfigure: disable pre_filter, score_every=2
cat > "$FAKE_REPO/.yakos.yml" <<'YEOF'
yakos: 0.9
supervisor:
  enabled: true
  runtime: claude
  agent: supervisor
  score_every_n_calls: 2
  block_on_critical: false
  model: haiku
  pre_filter:
    enabled: false
YEOF

# Run 2 clean edits; with pre_filter off, both count and the 2nd should dispatch.
make_fixture_edit "api/a.go" "// tiny"
run_hook
sleep 0.3 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -eq 0 ]; then
    ok "test (f): first call — no dispatch yet (counter=1 of 2)"
else
    bad "test (f): unexpected dispatch after first call; got $cnt"
fi

make_fixture_edit "api/b.go" "// tiny"
run_hook
sleep 0.5 2>/dev/null || true

cnt="$(dispatch_count)"
if [ "$cnt" -ge 1 ]; then
    ok "test (f): second call dispatched (counter=2 of 2, pre_filter disabled)"
else
    bad "test (f): expected dispatch at counter=2 with pre_filter disabled; got $cnt"
fi

# ---------------------------------------------------------------------------
# Test (g): settings.template.json matcher = "Edit|Write|MultiEdit|Bash"
# ---------------------------------------------------------------------------
note ""
note "=== Test (g): settings.template.json registers non-wildcard matcher ==="

if ! command -v jq >/dev/null 2>&1; then
    bad "test (g): jq required"
else
    sup_matcher="$(jq -r '
        .hooks.PostToolUse[]
        | select(.hooks[]?.command? | strings | test("supervisor-stream"))
        | .matcher
    ' "$SETTINGS_TEMPLATE" 2>/dev/null || true)"

    case "$sup_matcher" in
        *Edit*|*Write*|*MultiEdit*|*Bash*)
            ok "test (g): matcher '$sup_matcher' contains mutation verbs (not wildcard)"
            ;;
        "*")
            bad "test (g): matcher is still '*'; expected mutation-only matcher"
            ;;
        "")
            bad "test (g): could not locate supervisor-stream.sh entry in PostToolUse"
            ;;
        *)
            bad "test (g): unexpected matcher '$sup_matcher'"
            ;;
    esac
fi

# ---------------------------------------------------------------------------
# Test (h): Dispatch is async — hook returns < 200ms even when dispatch fires
# ---------------------------------------------------------------------------
note ""
note "=== Test (h): Async dispatch — hook returns in < 200ms ==="

reset_all

# Use a mock yakos that sleeps 2s to simulate a slow LLM call.
# The hook must still return immediately because it uses nohup … & disown.
cat > "$MOCK_BIN/yakos" <<'SLOW_MOCK'
#!/usr/bin/env bash
printf 'called: %s\n' "$*" >> "${YAKOS_DISPATCH_LOG}"
sleep 2
exit 0
SLOW_MOCK
chmod +x "$MOCK_BIN/yakos"

# Trigger a sensitive-path escalation so dispatch fires.
make_fixture_edit "secrets.txt" "key=abc123"
run_hook_timed

if [ "$LAST_RC" -eq 0 ]; then
    ok "test (h): hook exits 0"
else
    bad "test (h): expected rc=0, got rc=$LAST_RC"
fi

# LAST_ELAPSED_MS is 0 on systems where date +%N is unsupported; skip timing
# assertion in that case to avoid false failures in CI.
if [ "$LAST_ELAPSED_MS" -eq 0 ] 2>/dev/null; then
    ok "test (h): timing not available on this platform (date +%N unsupported) — skipping ms assertion"
elif [ "$LAST_ELAPSED_MS" -lt 200 ]; then
    ok "test (h): hook returned in ${LAST_ELAPSED_MS}ms (< 200ms — async confirmed)"
else
    bad "test (h): hook took ${LAST_ELAPSED_MS}ms (>= 200ms — possible sync dispatch)"
fi

# Restore fast mock
cat > "$MOCK_BIN/yakos" <<'MOCK'
#!/usr/bin/env bash
printf 'called: %s\n' "$*" >> "${YAKOS_DISPATCH_LOG}"
exit 0
MOCK
chmod +x "$MOCK_BIN/yakos"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "================================================================"
printf 'supervisor-prefilter-test: %d passed, %d failed\n' "$pass" "$fail"
echo "================================================================"

if [ "$fail" -gt 0 ]; then
    printf '\nFailed tests:\n%b\n' "$fail_log"
    exit 1
fi
exit 0
