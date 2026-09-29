#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-dispatch-log-perms-test.sh — K-86 / S-2 R12: the bash dispatch writer
# must leave ~/.yakos-state 0700 and dispatch-log.ndjson 0600, including on an
# install whose directory and log already exist with permissive modes (the Go
# writer's MkdirAll/O_CREATE modes are no-ops on existing paths).
#
# Self-contained; mirrors tests/run-runtime-stderr-capture-test.sh's mock
# plugin runtime setup.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

pass=0; fail=0
ok()   { printf '  [ok]   %s\n' "$*"; pass=$((pass + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; fail=$((fail + 1)); }

WORKDIR="$(mktemp -d -t yakos-logperms-test.XXXXXX)"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT INT TERM

export HOME="$WORKDIR/fakehome"
mkdir -p "$HOME/.yakos/plugins/mock-perms"

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

Minimal agent for the dispatch-log permission test.
AGENTEOF

cat > "$HOME/.yakos/plugins/mock-perms/runtime.sh" <<'RUNTIMEEOF'
#!/usr/bin/env bash
set -eu
yk_rt_mock_perms_id()                  { printf 'mock-perms\n'; }
yk_rt_mock_perms_capabilities()        { printf 'headless-print\n'; }
yk_rt_mock_perms_check_cli()           { return 0; }
yk_rt_mock_perms_check_auth()          { return 0; }
yk_rt_mock_perms_materialize_agents()  { return 0; }
yk_rt_mock_perms_cleanup_agents()      { return 0; }
yk_rt_mock_perms_launch()              { return 0; }
yk_rt_mock_perms_dispatch()            { printf 'ok\n'; }
RUNTIMEEOF
chmod +x "$HOME/.yakos/plugins/mock-perms/runtime.sh"

STATE="$HOME/.yakos-state"
LOG="$STATE/dispatch-log.ndjson"

# mode_of <path> — octal permission bits, portable across GNU and BSD stat.
mode_of() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"; }

run_dispatch() {
    YAKOS_ROOT="$REPO_ROOT" HOME="$WORKDIR/fakehome" \
    bash "$YAKOS_LIB/dispatch.sh" test-agent "dummy task" \
        --runtime mock-perms --project "$FAKE_PROJECT" --timeout 30 \
        >/dev/null 2>&1 || true
}

echo "yakos dispatch-log permission tests (S-2 R12)"
echo

echo "Test 1: fresh install creates 0700 dir and 0600 log"
rm -rf "$STATE"
run_dispatch
[ "$(mode_of "$STATE")" = "700" ] && ok "fresh state dir is 0700" || fail "fresh state dir mode = $(mode_of "$STATE"), want 700"
[ -f "$LOG" ] && [ "$(mode_of "$LOG")" = "600" ] && ok "fresh log is 0600" || fail "fresh log mode = $(mode_of "$LOG" 2>/dev/null || echo missing), want 600"

echo "Test 2: existing permissive install is tightened"
rm -rf "$STATE"
mkdir -p "$STATE"
chmod 755 "$STATE"
printf '{"type":"old"}\n' > "$LOG"
chmod 644 "$LOG"
run_dispatch
[ "$(mode_of "$STATE")" = "700" ] && ok "existing 0755 dir tightened to 0700" || fail "existing dir mode = $(mode_of "$STATE"), want 700"
[ "$(mode_of "$LOG")" = "600" ] && ok "existing 0644 log tightened to 0600" || fail "existing log mode = $(mode_of "$LOG"), want 600"
grep -q '"dispatch_started"' "$LOG" && ok "dispatch events still appended" || fail "dispatch_started not appended to the log"

echo
echo "passed: $pass  failed: $fail"
[ "$fail" -eq 0 ]
