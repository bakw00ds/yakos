#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-nojq-nonblocking-test.sh — K-81 rider (a) regression tests.
#
# Non-blocking (telemetry / report-only) hooks must never block a tool call
# just because jq is missing. Before the guard, several of them died with
# exit 127 ("jq: command not found") from an unguarded `$(jq ...)` under
# `set -e`, which Claude Code treats as a hook error.
#
# Blocking / security hooks are the opposite: with jq missing they must stay
# FAIL CLOSED (exit 2). This script asserts both directions so the guard can
# never be copied onto a security hook by accident.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
FIXT="$REPO_ROOT/tests/fixtures/hooks"

pass=0; fail=0
TMP="$(mktemp -d -t yakos-nojq-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# PATH containing every binary except jq.
NOJQ="$TMP/nojq-bin"; mkdir -p "$NOJQ"
for d in /usr/bin /bin /usr/local/bin /opt/homebrew/bin; do
    [ -d "$d" ] || continue
    for b in "$d"/*; do
        [ -x "$b" ] || continue
        n="$(basename -- "$b")"
        [ "$n" = jq ] && continue
        ln -sf "$b" "$NOJQ/$n" 2>/dev/null || true
    done
done
if PATH="$NOJQ" command -v jq >/dev/null 2>&1; then
    echo "SKIP: cannot build a jq-free PATH on this machine"; exit 0
fi

run_hook() { # <hook.sh> <payload-file> [extra env...] → sets rc, err
    local hook="$1" payload="$2"; shift 2
    local d; d="$(mktemp -d "$TMP/run.XXXXXX")"
    mkdir -p "$d/work/current" "$d/home"
    rc=0
    sed "s|__CLAUDE_PROJECT_DIR__|$d|g" "$payload" \
        | env HOME="$d/home" YAKOS_WORK_DIR="$d/work" CLAUDE_PROJECT_DIR="$d" PATH="$NOJQ" "$@" \
            bash "$HOOKS/$hook" >"$d/out" 2>"$d/err" || rc=$?
    err="$(cat "$d/err")"
}

# SendMessage payload for mailbox-mirror (no shipped fixture).
cat > "$TMP/sendmessage.json" <<'JSON'
{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"SendMessage","tool_input":{"to":"backend","summary":"hi","message":"body"}}
JSON

cat > "$TMP/teamdelete.json" <<'JSON'
{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"TeamDelete","tool_input":{"name":"t"}}
JSON

expect_rc() { # <label> <want> 
    if [ "$rc" = "$2" ]; then
        printf '  \033[32mOK\033[0m   %s (rc=%s)\n' "$1" "$rc"; pass=$((pass + 1))
    else
        printf '  \033[31mFAIL\033[0m %s: want rc=%s got rc=%s\n    stderr: %s\n' "$1" "$2" "$rc" "$err"
        fail=$((fail + 1))
    fi
}

echo "=== non-blocking hooks: missing jq must exit 0 ==="
run_hook mailbox-mirror.sh        "$TMP/sendmessage.json";                 expect_rc "mailbox-mirror (SendMessage)" 0
run_hook team-lifecycle.sh        "$FIXT/teamcreate.json";                 expect_rc "team-lifecycle (TeamCreate)" 0
run_hook team-lifecycle.sh        "$TMP/teamdelete.json";                  expect_rc "team-lifecycle (TeamDelete)" 0
run_hook team-lifecycle.sh        "$FIXT/agent-spawn.json";                expect_rc "team-lifecycle (Agent)" 0
run_hook session-end-check.sh     "$FIXT/sessionend-clean.json";           expect_rc "session-end-check (clean)" 0
run_hook session-end-check.sh     "$FIXT/sessionend-with-team.json";       expect_rc "session-end-check (with team)" 0
run_hook task-dependency-gate.sh  "$FIXT/taskcompleted-backend.json";      expect_rc "task-dependency-gate (backend)" 0
run_hook task-dependency-gate.sh  "$FIXT/taskcompleted-blocked.json";      expect_rc "task-dependency-gate (blocked)" 0
run_hook task-complete-dispatch.sh "$FIXT/taskcompleted-backend.json";     expect_rc "task-complete-dispatch (backend)" 0
run_hook task-complete-dispatch.sh "$FIXT/taskcompleted-frontend.json";    expect_rc "task-complete-dispatch (frontend)" 0

echo "=== guard message is emitted (not silent) ==="
run_hook task-dependency-gate.sh "$FIXT/taskcompleted-backend.json"
case "$err" in
    *jq*) printf '  \033[32mOK\033[0m   stderr mentions jq\n'; pass=$((pass + 1)) ;;
    *)    printf '  \033[31mFAIL\033[0m   stderr does not mention jq: %s\n' "$err"; fail=$((fail + 1)) ;;
esac

echo "=== blocking hooks: missing jq must still FAIL CLOSED (exit 2) ==="
run_hook path-allowlist.sh "$FIXT/pretooluse-edit-web-blocked.json"; expect_rc "path-allowlist (no jq)" 2
run_hook secret-scan.sh    "$FIXT/pretooluse-write-secret.json";     expect_rc "secret-scan (no jq)" 2

printf '\n  %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
