#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-runtime-stderr-capture-test.sh — unit tests for the dispatch.sh
# stderr capture feature (K-26).
#
# Architecture: Option B — dispatch.sh owns the capture; adapters are
# oblivious. Each test exercises the capture pipeline by running dispatch.sh
# with a mock plugin runtime that delegates to a fixture script under
# tests/fixtures/runtime-stderr/.
#
# Tests:
#   1. Failing dispatch populates stderr_tail in dispatch_finished event.
#   2. Successful dispatch leaves stderr_tail: null.
#   3. Large stderr (>4KB) is truncated; stderr_truncated: true.
#   4. ANSI escapes are stripped from stderr before logging.
#   5. JSON-special characters in stderr round-trip through jq cleanly.
#
# DO NOT extend run-runtime-fixtures.sh — that file is edited by K-27.
# This test is self-contained and uses a new file.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"
FIXTURES_DIR="$REPO_ROOT/tests/fixtures/runtime-stderr"

pass=0; fail=0

ok()   { printf '  [ok]   %s\n' "$*"; pass=$((pass + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; fail=$((fail + 1)); }

# ---------------------------------------------------------------------------
# Setup — one shared temp environment for all tests.
#
# Layout:
#   WORKDIR/fakehome/                    — HOME override
#   WORKDIR/fakehome/.yakos/plugins/mock-stderr/runtime.sh — plugin adapter
#   WORKDIR/fakehome/.yakos-state/dispatch-log.ndjson      — captured log
#   WORKDIR/fake-project/                — minimal project with one agent
#   WORKDIR/fake-project/.claude/agents/test-agent.md
# ---------------------------------------------------------------------------

WORKDIR="$(mktemp -d -t yakos-stderr-test.XXXXXX)"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT INT TERM

ORIG_HOME="$HOME"
export HOME="$WORKDIR/fakehome"
mkdir -p "$HOME/.yakos-state" "$HOME/.yakos/plugins/mock-stderr"

# Fake project with a minimal agent (no runtime: in frontmatter; we use
# --runtime override in each dispatch call so the agent runtime field
# doesn't matter).
FAKE_PROJECT="$WORKDIR/fake-project"
mkdir -p "$FAKE_PROJECT/.claude/agents"
cat > "$FAKE_PROJECT/.claude/agents/test-agent.md" <<'AGENTEOF'
---
id: test-agent
role: specialist
domain: test
mode: [feature]
tools: [Read]
model: sonnet
references: []
version: "1.0"
---

# Test Agent

## Purpose

Minimal agent used by stderr-capture tests. Does nothing; the mock runtime
controls the actual behaviour.
AGENTEOF

# ---- Plugin runtime adapter -------------------------------------------------
# The "mock-stderr" plugin runtime is loaded by dispatch.sh via the plugin
# mechanism (runtime-resolve.sh reads ~/.yakos/plugins/<id>/runtime.sh).
# Each test sets YAKOS_MOCK_FIXTURE to the path of the fixture script to run.
# The adapter's dispatch function execs that script directly.
# All other verbs are stubs.
cat > "$HOME/.yakos/plugins/mock-stderr/runtime.sh" <<'RUNTIMEEOF'
#!/usr/bin/env bash
set -eu

yk_rt_mock_stderr_id()                  { printf 'mock-stderr\n'; }
yk_rt_mock_stderr_capabilities()        { printf 'headless-print\n'; }
yk_rt_mock_stderr_check_cli()           { return 0; }
yk_rt_mock_stderr_check_auth()          { return 0; }
yk_rt_mock_stderr_materialize_agents()  { return 0; }
yk_rt_mock_stderr_cleanup_agents()      { return 0; }
yk_rt_mock_stderr_launch()              { return 0; }

yk_rt_mock_stderr_dispatch() {
    # YAKOS_MOCK_FIXTURE must be set to the fixture script path.
    local fixture="${YAKOS_MOCK_FIXTURE:-}"
    if [ -z "$fixture" ] || [ ! -x "$fixture" ]; then
        printf 'mock-stderr: YAKOS_MOCK_FIXTURE not set or not executable\n' >&2
        return 2
    fi
    # Run the fixture as a subprocess (NOT exec) so the dispatch function can
    # return with the fixture's exit code. Using exec would replace the
    # dispatch.sh process itself and bypass the rc capture + event_end write.
    "$fixture"
}
RUNTIMEEOF
chmod +x "$HOME/.yakos/plugins/mock-stderr/runtime.sh"

# ---- Dispatch helper ---------------------------------------------------------
# run_dispatch <fixture-name> → sets LAST_DISPATCH_LOG to parsed JSON of the
# dispatch_finished event from the most recent dispatch.
DISPATCH_LOG="$HOME/.yakos-state/dispatch-log.ndjson"

run_dispatch() {
    local fixture_name="$1"
    local fixture_path="$FIXTURES_DIR/$fixture_name"
    # Clear log to isolate events.
    rm -f "$DISPATCH_LOG" 2>/dev/null || true

    YAKOS_MOCK_FIXTURE="$fixture_path" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    HOME="$WORKDIR/fakehome" \
    bash "$YAKOS_LIB/dispatch.sh" \
        test-agent "dummy task" \
        --runtime mock-stderr \
        --project "$FAKE_PROJECT" \
        --timeout 30 \
        >/dev/null 2>/dev/null || true   # rc checked via log entry

    # Extract the dispatch_finished event.
    LAST_DISPATCH_LOG="$(grep '"type":"dispatch_finished"' "$DISPATCH_LOG" 2>/dev/null | tail -1 || true)"
}

echo "yakos runtime stderr capture tests"
echo

# ---- Test 1: Failing dispatch populates stderr_tail --------------------------
echo "Test 1: failing dispatch populates stderr_tail"
run_dispatch "fail-with-error"

if [ -z "$LAST_DISPATCH_LOG" ]; then
    fail "Test 1: no dispatch_finished event written"
else
    exit_code="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.exit_code')"
    stderr_tail="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_tail // "null"')"
    if [ "$exit_code" = "1" ]; then
        ok "Test 1: exit_code = 1 (failure captured)"
    else
        fail "Test 1: exit_code = $exit_code (expected 1)"
    fi
    if [ "$stderr_tail" = "null" ] || [ -z "$stderr_tail" ]; then
        fail "Test 1: stderr_tail is null/empty (should contain error text)"
    else
        ok "Test 1: stderr_tail is non-null: $(printf '%s' "$stderr_tail" | head -c 80)"
    fi
    if printf '%s' "$LAST_DISPATCH_LOG" | jq -e '.stderr_tail | type == "string"' >/dev/null 2>&1; then
        ok "Test 1: stderr_tail is a JSON string type"
    else
        fail "Test 1: stderr_tail is not a JSON string"
    fi
    if printf '%s' "$stderr_tail" | grep -q "gpt-5.5"; then
        ok "Test 1: stderr_tail contains the expected error message"
    else
        fail "Test 1: stderr_tail does not contain 'gpt-5.5' (got: $stderr_tail)"
    fi
fi
echo

# ---- Test 2: Successful dispatch leaves stderr_tail null ---------------------
echo "Test 2: successful dispatch leaves stderr_tail null"
run_dispatch "succeed-with-output"

if [ -z "$LAST_DISPATCH_LOG" ]; then
    fail "Test 2: no dispatch_finished event written"
else
    exit_code="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.exit_code')"
    stderr_tail_type="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_tail | type')"
    if [ "$exit_code" = "0" ]; then
        ok "Test 2: exit_code = 0"
    else
        fail "Test 2: exit_code = $exit_code (expected 0)"
    fi
    if [ "$stderr_tail_type" = "null" ]; then
        ok "Test 2: stderr_tail is null on success"
    else
        fail "Test 2: stderr_tail should be null on success (got type=$stderr_tail_type)"
    fi
fi
echo

# ---- Test 3: Large stderr is truncated; stderr_truncated: true ---------------
echo "Test 3: large stderr (>4KB) is truncated"
run_dispatch "fail-with-large-stderr"

if [ -z "$LAST_DISPATCH_LOG" ]; then
    fail "Test 3: no dispatch_finished event written"
else
    stderr_tail="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_tail // ""')"
    stderr_truncated="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_truncated')"
    stderr_tail_len="${#stderr_tail}"
    if [ "$stderr_truncated" = "true" ]; then
        ok "Test 3: stderr_truncated = true"
    else
        fail "Test 3: stderr_truncated = $stderr_truncated (expected true)"
    fi
    # The tail should be at most 4096 bytes (JSON encoding may add a little
    # overhead but the raw content before encoding is bounded).
    if [ "$stderr_tail_len" -le 4200 ]; then
        ok "Test 3: stderr_tail length $stderr_tail_len is within limit"
    else
        fail "Test 3: stderr_tail length $stderr_tail_len exceeds limit (want ≤ 4200)"
    fi
    if [ -n "$stderr_tail" ] && [ "$stderr_tail" != "null" ]; then
        ok "Test 3: stderr_tail is non-empty (contains last lines)"
    else
        fail "Test 3: stderr_tail is empty or null"
    fi
fi
echo

# ---- Test 4: ANSI escape codes are stripped ----------------------------------
echo "Test 4: ANSI escape codes are stripped"
run_dispatch "fail-with-ansi"

if [ -z "$LAST_DISPATCH_LOG" ]; then
    fail "Test 4: no dispatch_finished event written"
else
    stderr_tail="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_tail // ""')"
    # Should contain the text without the escape sequences.
    if printf '%s' "$stderr_tail" | grep -q "red error"; then
        ok "Test 4: stderr_tail contains the error message text"
    else
        fail "Test 4: stderr_tail missing expected text (got: $stderr_tail)"
    fi
    # Should NOT contain ESC character (0x1b).
    if printf '%s' "$stderr_tail" | grep -qP '\x1b' 2>/dev/null || \
       printf '%s' "$stderr_tail" | LC_ALL=C grep -q $'\033' 2>/dev/null; then
        fail "Test 4: stderr_tail still contains ANSI escape codes"
    else
        ok "Test 4: ANSI escape codes stripped from stderr_tail"
    fi
fi
echo

# ---- Test 5: JSON-special characters round-trip safely -----------------------
echo "Test 5: JSON-special characters in stderr round-trip cleanly"
run_dispatch "fail-with-json-chars"

if [ -z "$LAST_DISPATCH_LOG" ]; then
    fail "Test 5: no dispatch_finished event written"
else
    # The whole log line must be parseable JSON.
    if printf '%s' "$LAST_DISPATCH_LOG" | jq empty 2>/dev/null; then
        ok "Test 5: dispatch_finished event is valid JSON"
    else
        fail "Test 5: dispatch_finished event is not valid JSON"
    fi
    # stderr_tail should contain the expected content.
    stderr_tail="$(printf '%s' "$LAST_DISPATCH_LOG" | jq -r '.stderr_tail // ""')"
    if printf '%s' "$stderr_tail" | grep -q 'gpt-5.5'; then
        ok "Test 5: stderr_tail contains expected content with JSON special chars"
    else
        fail "Test 5: stderr_tail missing expected content (got: $stderr_tail)"
    fi
    # Verify stderr_tail itself is a valid string (jq already parsed it above,
    # but let's confirm its type explicitly).
    if printf '%s' "$LAST_DISPATCH_LOG" | jq -e '.stderr_tail | type == "string"' >/dev/null 2>&1; then
        ok "Test 5: stderr_tail is a JSON string type after round-trip"
    else
        fail "Test 5: stderr_tail is not a valid JSON string type"
    fi
fi
echo

# ---- Summary -----------------------------------------------------------------
echo "yakos runtime stderr capture: $pass passed, $fail failed"
[ "$fail" -eq 0 ] && exit 0 || exit 1
