#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: end-to-end verification of the CLI↔daemon build handshake (S-6
# WP-C, work/current/reports/s6-structural-plan-2026-09-23.md §4).
#
# Builds two Go binaries from the SAME VERSION file but with different
# injected -X internal/buildinfo.Commit values — the exact case the
# handshake exists to catch (a dev rebuild that bumps no version number).
# Starts a daemon from binary A, then drives CLI commands from binary B
# against it:
#
#   1. binary B (different commit) → daemon started by A: refused, exit 1,
#      actionable message on stderr naming both build ids and the fix.
#   2. binary A (same binary) → daemon started by A: routes normally
#      (kanban add's success line prints to STDOUT only when routed through
#      the daemon — cmd_daemonroute.go's routeKanbanViaDaemon; the in-process
#      fallback in cmd_kanban.go's kanbanAdd prints the same text to STDERR —
#      so which stream carries "kanban: added" is the routing discriminator
#      used throughout this script).
#   3. binary B with --restart-stale-daemon → daemon started by A: the
#      stale daemon is replaced and the command then succeeds via the fresh
#      daemon (STDOUT again).
#
# Exit 0 when every step passes.
set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
CLI_GO_DIR="$REPO_ROOT/cli-go"
TMP="${TMPDIR:-/tmp}/yakos-daemon-handshake.$$"
mkdir -p "$TMP"

PASS=0; FAIL=0
ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

DAEMON_PID=""
cleanup() {
    if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        kill -TERM "$DAEMON_PID" 2>/dev/null || true
        for _ in $(seq 1 20); do
            kill -0 "$DAEMON_PID" 2>/dev/null || break
            sleep 0.2
        done
        kill -KILL "$DAEMON_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP" 2>/dev/null || true
}
trap cleanup EXIT

# Isolated $HOME so this never touches the real ~/.yakos-state or
# ~/.claude — same pattern as tests/run-e2e.sh.
export HOME="$TMP/home"
mkdir -p "$HOME"

WORKSPACE="$TMP/workspace"
mkdir -p "$WORKSPACE"

BUILDINFO_PKG="github.com/bakw00ds/yakos/internal/buildinfo"
# internal/version.Version (the LEGACY display-string package, deliberately
# untouched everywhere else in this PR — see internal/buildinfo's doc
# comment) is also injected here, for a reason specific to this test
# script only: yakos.version's RPC handler (internal/serve/methods.go)
# calls version.Read(cfg.YakosRoot) and returns a hard RPC error if that
# fails. A binary run from a throwaway location (not the usual
# <repo>/bin/yakos, where version.Read falls back to reading <repo>/VERSION
# from disk) has no VERSION file reachable from wherever its "dev build"
# auto-materialization resolves YakosRoot to, so version.Read errors, so
# yakos.version errors, so every daemon-routed CLI call in this script
# would silently fall through to in-process execution — reproduced and
# root-caused via a Docker ubuntu:24.04 container matching CI: the
# failure was "yakos: daemon ping failed: ... version: reading
# .../dev/VERSION: no such file or directory", not a timing issue at all.
# Injecting Version here matches what an installed/released binary already
# gets for free from the file-based fallback — it does not change
# `yakos --version`'s own output (that stays governed by VERSION-file
# presence, per version.Read's precedence order) since this script never
# inspects that command's output.
VERSION_PKG="github.com/bakw00ds/yakos/internal/version"

echo "== building binary A (commit aaaaaaaaaaaa) =="
( cd "$CLI_GO_DIR" && go build \
    -ldflags "-X $BUILDINFO_PKG.Version=0.0.0-handshake-test -X $BUILDINFO_PKG.Commit=aaaaaaaaaaaa -X $VERSION_PKG.Version=0.0.0-handshake-test" \
    -o "$TMP/yakos-a" ./cmd/yakos )
ok "built binary A"

echo "== building binary B (commit bbbbbbbbbbbb — same version, different commit) =="
( cd "$CLI_GO_DIR" && go build \
    -ldflags "-X $BUILDINFO_PKG.Version=0.0.0-handshake-test -X $BUILDINFO_PKG.Commit=bbbbbbbbbbbb -X $VERSION_PKG.Version=0.0.0-handshake-test" \
    -o "$TMP/yakos-b" ./cmd/yakos )
ok "built binary B"

BIN_A="$TMP/yakos-a"
BIN_B="$TMP/yakos-b"

# kanban_add runs `<bin> kanban add <title> [extra...]` with YAKOS_DAEMON=on,
# capturing stdout and stderr to separate files (global STDOUT_FILE /
# STDERR_FILE) and returning the exit code via $?. Whether "kanban: added"
# appears on stdout (routed through the daemon) or stderr (in-process
# fallback) is the routing discriminator — see the file header.
STDOUT_FILE="$TMP/stdout.log"
STDERR_FILE="$TMP/stderr.log"
kanban_add() {
    local bin="$1"; shift
    local title="$1"; shift
    set +e
    YAKOS_IMPL=go YAKOS_DAEMON=on "$bin" kanban add "$title" "$@" \
        >"$STDOUT_FILE" 2>"$STDERR_FILE"
    local rc=$?
    set -e
    return "$rc"
}

# ---- start a daemon from binary A -----------------------------------------

cd "$WORKSPACE"
YAKOS_IMPL=go YAKOS_DAEMON=off "$BIN_A" serve --no-console --no-perf --detach \
    > "$TMP/daemon.log" 2>&1 &
DAEMON_PID=$!

# Poll a real kanban add routed through the daemon (STDOUT carries the
# success line only once routed — see kanban_add's doc comment) rather than
# a fixed sleep. 150 * 0.2s = a 30s budget (plus each kanban_add's own
# subprocess overhead on top) — generous margin for a loaded/shared CI
# runner, where daemon cold-start (first exec of a freshly built ~45MB
# binary, competing with other concurrent jobs) can take meaningfully
# longer than on a quiet local machine; 10s was observed to be too tight
# on GitHub's ubuntu-latest runner.
ready=0
for i in $(seq 1 150); do
    if kanban_add "$BIN_A" "readiness-probe-$i"; then :; fi
    if grep -q "kanban: added" "$STDOUT_FILE" 2>/dev/null; then
        ready=1
        break
    fi
    sleep 0.2
done
if [ "$ready" -eq 1 ]; then
    ok "daemon started from binary A and is reachable"
else
    fail "daemon from binary A never became reachable"
    echo "---- daemon.log ----" >&2
    cat "$TMP/daemon.log" >&2
    echo "---- last readiness probe's stdout ----" >&2
    cat "$STDOUT_FILE" >&2
    echo "---- last readiness probe's stderr ----" >&2
    cat "$STDERR_FILE" >&2
    echo "---- socket dir listing ----" >&2
    ls -la "$(dirname "$(grep "yakos serve: socket at " "$TMP/daemon.log" | head -1 | sed 's/^yakos serve: socket at //')" 2>/dev/null)" >&2 2>&1 || true
    echo "yakos daemon-handshake: $PASS passed, $FAIL failed"
    exit 1
fi

# PIDFILE: derived from the daemon's own startup banner ("yakos serve:
# socket at <path>", cmd_serve.go) rather than recomputed independently —
# internal/jsonrpc.PIDPath/SocketPath hash the workspace root with no
# symlink resolution or case-folding rules a shell script can cheaply
# reproduce byte-for-byte (macOS's $TMPDIR going through /var ->
# /private/var, and the darwin/windows-only lowercasing step in
# workspaceHash, both bit us here in earlier iterations of this script).
# Reading it back from the process that actually computed it sidesteps the
# whole class of parity bugs. WORKSPACE never changes for the rest of this
# script, so this same path stays valid across the restart in step 3 below
# (the restart's fresh daemon binds the *same* socket/pidfile pair).
DAEMON_SOCKET_LINE=$(grep "yakos serve: socket at " "$TMP/daemon.log" | head -1)
DAEMON_SOCKET_PATH=${DAEMON_SOCKET_LINE#yakos serve: socket at }
PIDFILE="${DAEMON_SOCKET_PATH%.sock}.pid"
if [ -z "$DAEMON_SOCKET_PATH" ] || [ ! -f "$PIDFILE" ]; then
    fail "could not derive pidfile path from daemon.log's startup banner"
    echo "---- daemon.log ----" >&2
    cat "$TMP/daemon.log" >&2
    echo "yakos daemon-handshake: $PASS passed, $FAIL failed"
    exit 1
fi

# ---- 1. binary B (different commit) is refused -----------------------------

if kanban_add "$BIN_B" "should be refused"; then rc_b=0; else rc_b=$?; fi
out_b="$(cat "$STDOUT_FILE" 2>/dev/null || true)$(cat "$STDERR_FILE" 2>/dev/null || true)"

if [ "$rc_b" -eq 1 ]; then
    ok "binary B refused with exit 1 on build mismatch"
else
    fail "binary B: expected exit 1 on build mismatch, got $rc_b (output: $out_b)"
fi

if printf '%s' "$out_b" | grep -q "daemon build mismatch"; then
    ok "refusal message names the mismatch"
else
    fail "refusal message missing 'daemon build mismatch' banner (output: $out_b)"
fi

if printf '%s' "$out_b" | grep -q "aaaaaaaaaaaa" && printf '%s' "$out_b" | grep -q "bbbbbbbbbbbb"; then
    ok "refusal message names both build ids"
else
    fail "refusal message missing one or both commit ids (output: $out_b)"
fi

if printf '%s' "$out_b" | grep -q -- "--restart-stale-daemon"; then
    ok "refusal message names the opt-in restart fix"
else
    fail "refusal message missing the --restart-stale-daemon hint (output: $out_b)"
fi

if [ -s "$STDOUT_FILE" ] && grep -q "kanban: added" "$STDOUT_FILE"; then
    fail "binary B's refused request must not fall through and add the task anyway"
else
    ok "refused request did not fall through to an in-process add"
fi

# ---- 2. same binary (A) still routes normally ------------------------------

if kanban_add "$BIN_A" "same binary works"; then rc_a=0; else rc_a=$?; fi

if [ "$rc_a" -eq 0 ] && grep -q "kanban: added" "$STDOUT_FILE"; then
    ok "same-binary CLI still routes kanban add through the daemon (STDOUT)"
else
    fail "same-binary CLI failed to route (rc=$rc_a, stdout: $(cat "$STDOUT_FILE"), stderr: $(cat "$STDERR_FILE"))"
fi

# ---- 3. binary B with --restart-stale-daemon restarts and succeeds --------

if kanban_add "$BIN_B" "restart me" --restart-stale-daemon; then rc_restart=0; else rc_restart=$?; fi
restart_stderr="$(cat "$STDERR_FILE" 2>/dev/null || true)"

if [ "$rc_restart" -eq 0 ]; then
    ok "binary B with --restart-stale-daemon exits 0"
else
    fail "binary B with --restart-stale-daemon: expected exit 0, got $rc_restart (stdout: $(cat "$STDOUT_FILE"), stderr: $restart_stderr)"
fi

if printf '%s' "$restart_stderr" | grep -q "restarted stale daemon"; then
    ok "restart path reported the restart"
else
    fail "restart path did not report restarting the daemon (stderr: $restart_stderr)"
fi

if grep -q "kanban: added" "$STDOUT_FILE"; then
    ok "kanban add succeeded via the fresh (post-restart) daemon (STDOUT)"
else
    fail "kanban add did not route through the fresh daemon after restart (stdout: $(cat "$STDOUT_FILE"))"
fi

# The daemon that answers now should be a fresh process started by B's
# spawnDaemonFn. WORKSPACE never changes, so PIDFILE (derived above from
# binary A's own startup banner) is still the right path — the restart
# writes a fresh PID/build-id into the exact same file. Confirm the
# build-id line now reflects binary B, then swap DAEMON_PID so cleanup
# signals the right (post-restart) process.
if [ -n "$PIDFILE" ] && [ -f "$PIDFILE" ]; then
    NEW_PID=$(head -1 "$PIDFILE" 2>/dev/null || true)
    BUILD_LINE=$(sed -n '2p' "$PIDFILE" 2>/dev/null || true)
    if printf '%s' "$BUILD_LINE" | grep -q "bbbbbbbbbbbb"; then
        ok "pidfile build-id line reflects the fresh (B) daemon after restart"
    else
        fail "pidfile build-id line does not reflect binary B after restart (got: $BUILD_LINE)"
    fi
    case "$NEW_PID" in
        ''|*[!0-9]*) ;;
        *) DAEMON_PID="$NEW_PID" ;;
    esac
else
    fail "could not locate pidfile after restart to verify build id"
fi

echo
echo "yakos daemon-handshake: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
