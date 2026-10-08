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
# stdin pass-through, the deadline (exit 124, descendants gone, quick), SIGINT /
# SIGTERM / SIGHUP to dispatch (exit 130, a double-forked grandchild gone), and the
# deadline syntax (leading zeros, suffixes, refusal of 0, negatives, fractions,
# junk and over-range before the job starts).
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
        dfork) # a double-forked grandchild (reparented to init) plus a blocking child
              printf '%s\n' "$$" > "$MOCK_TO_PIDFILE"
              ( ( exec sleep "$MOCK_TO_SLEEP" ) & printf '%s\n' "$!" >> "$MOCK_TO_PIDFILE" ) &
              sleep 0.3
              ( exec sleep "$MOCK_TO_SLEEP2" ) &
              printf '%s\n' "$!" >> "$MOCK_TO_PIDFILE"
              wait ;;
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

# A signal to dispatch itself. set -m keeps the background job from starting with
# SIGINT ignored (a non-interactive shell does that, and an ignored signal cannot
# be trapped); the signal goes to dispatch's pid alone, so only the wrapper's own
# kill can reach the adapter tree.
# A shell started in the background by a non-interactive parent has SIGINT/SIGHUP
# ignored, and a signal ignored at entry cannot be trapped; reset them so the case
# does not depend on how this suite was launched.
sigreset_exec() {
    if command -v perl >/dev/null 2>&1; then
        exec perl -e '$SIG{INT} = "DEFAULT"; $SIG{HUP} = "DEFAULT"; exec @ARGV' -- "$@"
    else
        exec "$@"
    fi
}
signal_case() {
    local sig="$1" n="$2" noPs="${3:-}" s1 s2 spath="$WORKDIR/mkt:$WORKDIR/stubbin:$PATH"
    [ -n "$noPs" ] && spath="$WORKDIR/nops:$spath"
    s1=$((40000 + n * 7 + $$ % 1000)); s2=$((50000 + n * 7 + $$ % 1000))
    echo "Test 6.$n: SIG$sig to dispatch kills the adapter tree and exits 130${noPs:+ (ps unavailable)}"
    rm -rf "$WORKDIR/dtmp"; mkdir -p "$WORKDIR/dtmp"
    export MOCK_TO_PIDFILE="$WORKDIR/pids$n" MOCK_TO_SLEEP="$s1" MOCK_TO_SLEEP2="$s2"
    : > "$MOCK_TO_PIDFILE"
    set -m
    MOCK_TO_MODE=dfork PATH="$spath" TMPDIR="$WORKDIR/dtmp" sigreset_exec bash "$YAKOS_LIB/dispatch.sh" test-agent task \
        --runtime mock-to --project "$PROJ" --timeout 120 </dev/null >"$OUT" 2>"$ERR" &
    local dpid=$!
    set +m
    local i=0
    while [ "$(wc -l < "$MOCK_TO_PIDFILE" | tr -d ' ')" -lt 3 ] && [ "$i" -lt 50 ]; do sleep 0.2; i=$((i + 1)); done
    [ "$(wc -l < "$MOCK_TO_PIDFILE" | tr -d ' ')" -ge 3 ] || { bad "adapter never started: $(tail -3 "$ERR")"; kill -KILL "$dpid" 2>/dev/null; return; }
    kill -"$sig" "$dpid"
    local rc=0
    # Bounded wait: a dispatch that ignores the signal must fail the test, not hang it.
    i=0
    while kill -0 "$dpid" 2>/dev/null && [ "$i" -lt 40 ]; do sleep 0.2; i=$((i + 1)); done
    if kill -0 "$dpid" 2>/dev/null; then rc=999; kill -KILL "$dpid" 2>/dev/null || true; fi
    local wrc=0
    wait "$dpid" 2>/dev/null || wrc=$?
    [ "$rc" -eq 999 ] || rc="$wrc"
    [ "$rc" -eq 130 ] && ok "dispatch exited 130" || bad "dispatch exited $rc, want 130"
    sleep 1
    local alive="" p
    while read -r p; do
        [ -n "$p" ] && kill -0 "$p" 2>/dev/null && alive="$alive $p"
    done < "$MOCK_TO_PIDFILE"
    if [ -z "$alive" ] && ! pgrep -f "sleep ($s1|$s2)\$" >/dev/null 2>&1; then
        ok "no adapter process survived (pidfile and pgrep)"
    else
        bad "survivors:$alive"
        pkill -KILL -f "sleep ($s1|$s2)\$" 2>/dev/null || true
    fi
    if [ -z "$(ls -A "$WORKDIR/dtmp")" ]; then
        ok "the trap removed the out/usage/stderr scratch files"
    else
        bad "scratch files left behind: $(ls "$WORKDIR/dtmp" | tr '\n' ' ')"
    fi
}
# macOS mktemp -t ignores TMPDIR, so a shim sends dispatch's scratch files to
# $WORKDIR/dtmp, where the cleanup assertions can see them.
mkdir -p "$WORKDIR/mkt"
REAL_MKTEMP="$(command -v mktemp)"
printf '#!/bin/sh\nexec "%s" "%s/dtmp/f.XXXXXX"\n' "$REAL_MKTEMP" "$WORKDIR" > "$WORKDIR/mkt/mktemp"
chmod +x "$WORKDIR/mkt/mktemp"
# ps gone: the group kill must not depend on it (and must not fail open).
mkdir -p "$WORKDIR/nops"
printf '#!/bin/sh\nexit 127\n' > "$WORKDIR/nops/ps"; chmod +x "$WORKDIR/nops/ps"
signal_case INT 1
signal_case TERM 2
signal_case HUP 3
signal_case INT 4 nops
signal_case TERM 5 nops

# Ctrl-C on a real terminal: dispatch runs as the foreground job of a pty session
# (python pty.fork, no new session of our own), the Ctrl-C character is typed into
# the master, and the double-forked grandchild must die with the rest.
echo "Test 6.6: Ctrl-C on a pty kills the adapter tree and exits 130"
if command -v python3 >/dev/null 2>&1; then
    s1=$((41000 + $$ % 1000)); s2=$((51000 + $$ % 1000))
    export MOCK_TO_PIDFILE="$WORKDIR/pids6" MOCK_TO_SLEEP="$s1" MOCK_TO_SLEEP2="$s2"
    : > "$MOCK_TO_PIDFILE"
    rm -rf "$WORKDIR/dtmp"; mkdir -p "$WORKDIR/dtmp"
    cat > "$WORKDIR/pty-ctrlc.py" <<'PYEOF'
import os, pty, sys, time
pidfile = os.environ["MOCK_TO_PIDFILE"]
argv = sys.argv[1:]
pid, fd = pty.fork()
if pid == 0:
    os.execvp(argv[0], argv)
import select
deadline = time.time() + 20
seen = b""
while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 0.2)
    if r:
        try:
            seen += os.read(fd, 4096)
        except OSError:
            break
    with open(pidfile) as f:
        if len(f.read().split()) >= 3:
            break
try:
    os.write(fd, b"\x03")
except OSError:
    sys.stderr.write("child gone before Ctrl-C; output: " + seen.decode("utf-8", "replace")[-600:] + "\n")
    print(998)
    sys.exit(0)
end = time.time() + 15
status = None
while time.time() < end:
    r, _, _ = select.select([fd], [], [], 0.1)
    if r:
        try:
            os.read(fd, 4096)
        except OSError:
            pass
    done, st = os.waitpid(pid, os.WNOHANG)
    if done:
        status = st
        break
    time.sleep(0.1)
if status is None:
    os.kill(pid, 9)
    print(999)
elif os.WIFEXITED(status):
    print(os.WEXITSTATUS(status))
else:
    print(128 + os.WTERMSIG(status))
PYEOF
    prc="$(MOCK_TO_MODE=dfork PATH="$WORKDIR/mkt:$WORKDIR/stubbin:$PATH" TMPDIR="$WORKDIR/dtmp" python3 -I "$WORKDIR/pty-ctrlc.py" \
        bash "$YAKOS_LIB/dispatch.sh" test-agent task --runtime mock-to --project "$PROJ" --timeout 120 2>"$WORKDIR/pty.err" | tail -1)"
    [ "$prc" = 130 ] && ok "dispatch exited 130 on Ctrl-C" || bad "dispatch exited ${prc:-?}, want 130: $(tail -12 "$WORKDIR/pty.err" 2>/dev/null)"
    sleep 1
    alive=""
    while read -r p; do
        [ -n "$p" ] && kill -0 "$p" 2>/dev/null && alive="$alive $p"
    done < "$MOCK_TO_PIDFILE"
    if [ -z "$alive" ] && ! pgrep -f "sleep ($s1|$s2)\$" >/dev/null 2>&1; then
        ok "no adapter process survived the pty Ctrl-C (grandchild included)"
    else
        bad "survivors after pty Ctrl-C:$alive"
        pkill -KILL -f "sleep ($s1|$s2)\$" 2>/dev/null || true
    fi
    [ -z "$(ls -A "$WORKDIR/dtmp")" ] && ok "pty: scratch files removed" || bad "pty: scratch files left: $(ls "$WORKDIR/dtmp" | tr '\n' ' ')"
else
    echo "  [skip] python3 not available"
fi

echo "Test 7: deadline syntax"
deadline_ok() {   # <value> <expected-exit>: --timeout value runs the adapter
    MOCK_TO_MODE=ok run_dispatch "$1"; local rc=$?
    [ "$rc" -eq "$2" ] && ok "--timeout $1 -> exit $rc" || bad "--timeout $1 -> exit $rc, want $2: $(tail -4 "$ERR")"
}
deadline_refused() {   # <value>: refused before the job starts
    MOCK_TO_MODE=ok run_dispatch "$1"; local rc=$?
    if [ "$rc" -ne 0 ] && ! grep -q adapter-ran "$OUT" && ! grep -q "dispatch: agent=" "$ERR"; then
        ok "--timeout '$1' refused before the job (exit $rc)"
    else
        bad "--timeout '$1' was not refused (exit $rc): $(tail -2 "$ERR")"
    fi
}
deadline_ok 08 0
deadline_ok 30s 0
deadline_ok 2m 0
deadline_ok 0030 0
for v in 0 00 -5 abc 1.5 "" 30x 99999999999999999999 604801 8d; do deadline_refused "$v"; done

echo "Test 8: agent max-duration-s"
sed 's/^model: sonnet$/model: sonnet\nmax-duration-s: 0/' "$PROJ/.claude/agents/test-agent.md" > "$WORKDIR/md0.md"
cp "$PROJ/.claude/agents/test-agent.md" "$WORKDIR/agent.orig"
cp "$WORKDIR/md0.md" "$PROJ/.claude/agents/test-agent.md"
deadline_refused 30
sed 's/^max-duration-s: 0$/max-duration-s: 08/' "$WORKDIR/md0.md" > "$PROJ/.claude/agents/test-agent.md"
MOCK_TO_MODE=ok run_dispatch 30; rc=$?
[ "$rc" -eq 0 ] && ok "max-duration-s: 08 is read as 8" || bad "max-duration-s: 08 -> exit $rc: $(tail -1 "$ERR")"
cp "$WORKDIR/agent.orig" "$PROJ/.claude/agents/test-agent.md"

if command -v timeout >/dev/null 2>&1 && timeout --version 2>/dev/null | grep -qi coreutils; then
    echo "Test 5: a real GNU timeout on PATH"
    MOCK_TO_MODE=ok PATH="$PATH" bash "$YAKOS_LIB/dispatch.sh" test-agent task \
        --runtime mock-to --project "$PROJ" --timeout 30 >"$OUT" 2>"$ERR"; rc=$?
    [ "$rc" -eq 0 ] && grep -q adapter-ran "$OUT" && ok "dispatch works with GNU coreutils timeout" || bad "exit $rc: $(tail -2 "$ERR")"
fi

echo
echo "passed: $pass  failed: $failn"
[ "$failn" -eq 0 ]
