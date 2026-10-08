#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-dispatch-timeout-test.sh — K-169: cli/lib/dispatch.sh runs its adapter
# FUNCTION (yk_rt_dispatch) under a deadline, and GNU coreutils timeout(1) cannot
# run a function ("failed to run command", exit 127). On Linux, or macOS with
# coreutils, `timeout` is on PATH, so every bash dispatch failed before any
# runtime started.
#
# A `timeout` stub that behaves like GNU's (it execs its command, so a shell
# function is "not found", exit 127) is put first on PATH, which reproduces the
# Linux failure on a bare macOS. If a real GNU timeout is installed it is
# exercised too. Cases: success with the stub, the adapter's own exit status, a
# stdin pass-through, and the deadline (exit 124, descendants gone, quick).
#
# Self-contained: HOME is a temp directory, the runtime is a plugin function and
# nothing calls a model.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
unset YAKOS_ROOT YAKOS_LIB YAKOS_IMPL YAKOS_DISPATCH_LOG
export YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib"

pass=0; failn=0
ok()   { printf '  [ok]   %s\n' "$*"; pass=$((pass + 1)); }
bad()  { printf '  [FAIL] %s\n' "$*" >&2; failn=$((failn + 1)); }

WORKDIR="$(mktemp -d -t yakos-dtimeout.XXXXXX)"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT INT TERM

export HOME="$WORKDIR/home"
PROJ="$WORKDIR/project"
mkdir -p "$HOME/.yakos/plugins/mock-to" "$PROJ/.claude/agents" "$WORKDIR/stubbin"
cat > "$PROJ/.claude/agents/test-agent.md" <<'AGENTEOF'
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

Minimal agent for the dispatch timeout test.
AGENTEOF

# The adapter's behaviour is chosen by MOCK_TO_MODE so one plugin serves all cases.
cat > "$HOME/.yakos/plugins/mock-to/runtime.sh" <<'RUNTIMEEOF'
#!/usr/bin/env bash
set -eu
yk_rt_mock_to_id()                  { printf 'mock-to\n'; }
yk_rt_mock_to_capabilities()        { printf 'headless-print\n'; }
yk_rt_mock_to_check_cli()           { return 0; }
yk_rt_mock_to_check_auth()          { return 0; }
yk_rt_mock_to_materialize_agents()  { return 0; }
yk_rt_mock_to_cleanup_agents()      { return 0; }
yk_rt_mock_to_launch()              { return 0; }
yk_rt_mock_to_dispatch() {
    case "${MOCK_TO_MODE:-ok}" in
        ok)   printf 'adapter-ran\n' ;;
        fail) printf 'adapter-failing\n'; return 7 ;;
        stdin) cat ;;
        hang) printf '%s\n' "$$" > "$MOCK_TO_PIDFILE"
              ( exec sleep 300 ) &
              printf '%s\n' "$!" >> "$MOCK_TO_PIDFILE"
              wait ;;
    esac
}
RUNTIMEEOF
chmod +x "$HOME/.yakos/plugins/mock-to/runtime.sh"

# A timeout(1) that, like GNU's, execs its command: a shell function is not found.
cat > "$WORKDIR/stubbin/timeout" <<'STUBEOF'
#!/bin/sh
shift
exec "$@"
STUBEOF
chmod +x "$WORKDIR/stubbin/timeout"

OUT="$WORKDIR/out"; ERR="$WORKDIR/err"
# run_dispatch <secs> [stdin-file]: PATH starts with the stub dir.
run_dispatch() {
    local secs="$1" in="${2:-/dev/null}"
    PATH="$WORKDIR/stubbin:$PATH" bash "$YAKOS_LIB/dispatch.sh" test-agent "task" \
        --runtime mock-to --project "$PROJ" --timeout "$secs" <"$in" >"$OUT" 2>"$ERR"
}

echo "yakos dispatch timeout tests (K-169)"

echo "Test 1: a timeout(1) on PATH does not break the adapter function"
MOCK_TO_MODE=ok run_dispatch 30; rc=$?
[ "$rc" -eq 0 ] && ok "exit 0 with a timeout stub first on PATH" || bad "exit $rc: $(tail -2 "$ERR")"
grep -q adapter-ran "$OUT" && ok "the adapter ran and its output came back" || bad "adapter output missing: $(cat "$OUT")"
grep -q "failed to run command\|not found" "$ERR" && bad "stderr still shows the timeout(1) failure: $(cat "$ERR")" || ok "no timeout(1) failure on stderr"

echo "Test 2: the adapter's exit status passes through"
MOCK_TO_MODE=fail run_dispatch 30; rc=$?
[ "$rc" -eq 7 ] && ok "exit 7 propagated" || bad "exit $rc, want 7"

echo "Test 3: stdin reaches the adapter"
printf 'from-stdin\n' > "$WORKDIR/in"
MOCK_TO_MODE=stdin run_dispatch 30 "$WORKDIR/in"; rc=$?
grep -q from-stdin "$OUT" && ok "stdin passed through the deadline wrapper" || bad "stdin lost (rc $rc): $(cat "$OUT")"

echo "Test 4: the deadline kills a hung adapter and its children"
export MOCK_TO_PIDFILE="$WORKDIR/pids"
: > "$MOCK_TO_PIDFILE"
start=$SECONDS
MOCK_TO_MODE=hang run_dispatch 2; rc=$?
elapsed=$((SECONDS - start))
[ "$rc" -eq 124 ] && ok "exit 124 on expiry" || bad "exit $rc, want 124"
[ "$elapsed" -le 10 ] && ok "returned in ${elapsed}s, not the 300s sleep" || bad "took ${elapsed}s"
sleeper="$(sed -n 2p "$MOCK_TO_PIDFILE")"
sleep 1
if [ -n "$sleeper" ] && kill -0 "$sleeper" 2>/dev/null; then
    bad "the adapter's child ($sleeper) outlived the deadline"
    kill -KILL "$sleeper" 2>/dev/null || true
else
    ok "the adapter's child is gone"
fi

if command -v timeout >/dev/null 2>&1 && timeout --version 2>/dev/null | grep -qi coreutils; then
    echo "Test 5: a real GNU timeout on PATH"
    MOCK_TO_MODE=ok PATH="$PATH" bash "$YAKOS_LIB/dispatch.sh" test-agent task \
        --runtime mock-to --project "$PROJ" --timeout 30 >"$OUT" 2>"$ERR"; rc=$?
    [ "$rc" -eq 0 ] && grep -q adapter-ran "$OUT" && ok "dispatch works with GNU coreutils timeout" || bad "exit $rc: $(tail -2 "$ERR")"
fi

echo
echo "passed: $pass  failed: $failn"
[ "$failn" -eq 0 ]
