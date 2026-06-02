#!/usr/bin/env bash
# kanban-stop.sh — SessionEnd hook: stop the kanban web UI if it was
# auto-started by `yakos start`.
#
# This is a TEARDOWN HOOK. It NEVER blocks. SessionEnd hooks cannot block
# exit, and a failed teardown must not prevent the session from ending
# cleanly. Every operation is guarded with `|| true`.
#
# Behavior:
#   - Skipped silently when YAKOS_KANBAN_AUTOSERVE=0 (symmetric with
#     start.sh's auto-start opt-out).
#   - No-ops gracefully when no server is running (idempotent: the state
#     file is absent or the recorded PID is dead).
#   - Sends SIGTERM to the python server process and removes the state
#     file. The python server's own signal handler removes the state file
#     too; the rm -f here covers the case where it was already gone.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

# Respect the operator opt-out. If auto-start was disabled, there is no
# server to stop — skip silently.
if [ "${YAKOS_KANBAN_AUTOSERVE:-1}" = "0" ]; then
    exit 0
fi

# Resolve the state file path via the canonical path resolver.
state_file=""
if command -v yakos_current_dir >/dev/null 2>&1; then
    state_file="$(yakos_current_dir 2>/dev/null || true)/.kanban-serve.json"
fi

# Nothing to do if the state file is absent.
if [ -z "$state_file" ] || [ ! -f "$state_file" ]; then
    exit 0
fi

# Extract pid from state file (single-line JSON: {"pid": N, ...}).
pid="$(sed -n 's/.*"pid":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$state_file" 2>/dev/null | head -1 || true)"

if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    # Belt-and-suspenders: only kill if the pid still looks like a python
    # process (guards against pid recycling in long sessions).
    if ps -o command= -p "$pid" 2>/dev/null | grep -q '[p]ython'; then
        kill "$pid" 2>/dev/null || true
        ho_log "kanban-stop" "REPORT" "pass" "stopped kanban web UI (pid $pid)" \
            "$(jq -nc --argjson pid "$pid" '{pid: $pid}' 2>/dev/null || printf '{}')" 2>/dev/null || true
    else
        ho_log "kanban-stop" "REPORT" "pass" "kanban pid $pid is not our server (stale); clearing state" \
            "$(jq -nc --argjson pid "$pid" '{pid: $pid, stale: true}' 2>/dev/null || printf '{}')" 2>/dev/null || true
    fi
else
    ho_log "kanban-stop" "REPORT" "pass" "kanban web UI was not running at session end" \
        '{}' 2>/dev/null || true
fi

# Always clear the state file — prevents stale entries accumulating.
rm -f "$state_file" 2>/dev/null || true

exit 0
