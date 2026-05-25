#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-kanban-orphan-leak-test.sh — integration tests for the orphan-leak fix.
#
# Tests _kanban_find_live_pids (project-scoped), the cmd_serve scan guard,
# and the scan-backed cmd_serve_stop.  Spawns real python processes; always
# cleans up on EXIT.
#
# Cases:
#   1. serve guard fast path: state file present + live → no second bind
#   2. serve guard scan path: state file absent, server live → no second bind,
#      state file recovered, recovered pid correct
#   3. stop kills the state-file-recorded pid (normal path)
#   4. stop kills an orphan NOT in the state file (scan path)
#   5. stop kills two servers (state-file pid + injected orphan, same board)
#   6. cross-project isolation: stop for project A does NOT kill project B's
#      server (the decisive new case)
#
# Exits 0 if all cases pass.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
KANBAN="$REPO_ROOT/cli/lib/kanban.sh"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

# Load ct_realpath so tests can canonicalise paths the same way kanban.sh does.
# shellcheck source=/dev/null
. "$YAKOS_LIB/compat.sh"
# shellcheck source=/dev/null
. "$YAKOS_LIB/paths.sh"

pass=0
fail=0

# ── Cleanup registry ─────────────────────────────────────────────────────────
ALL_PIDS=""
ALL_WORK_DIRS=""

cleanup() {
    for _p in $ALL_PIDS; do
        kill "$_p" 2>/dev/null || true
    done
    for _wd in $ALL_WORK_DIRS; do
        rm -rf "$_wd" 2>/dev/null || true
    done
}
trap cleanup EXIT

register() { ALL_PIDS="$ALL_PIDS $1"; }

# ── Helpers ───────────────────────────────────────────────────────────────────

check() {
    local label="$1" ok="$2"
    if [ "$ok" = "1" ]; then
        printf 'PASS  %s\n' "$label"
        pass=$((pass + 1))
    else
        printf 'FAIL  %s\n' "$label"
        fail=$((fail + 1))
    fi
}

# make_work: create a fresh isolated work dir; return its path.
make_work() {
    local tmp
    tmp="$(mktemp -d -t yakos-orphan-XXXXXX)"
    ALL_WORK_DIRS="$ALL_WORK_DIRS $tmp"
    mkdir -p "$tmp/work/current"
    cat > "$tmp/work/current/kanban.md" << 'BOARD'
# Kanban — test
## TODO
## IN PROGRESS
## DONE
BOARD
    printf '%s' "$tmp"
}

# canon_board WORKDIR: print the canonical board file path for the given work
# dir, resolved the same way cmd_serve does (via ct_realpath).
canon_board() {
    local raw="$1/work/current/kanban.md"
    ct_realpath "$raw" 2>/dev/null || printf '%s' "$raw"
}

# start_server WORKDIR: launch kanban serve --no-open in background against an
# isolated work dir.  Waits up to 3s for the state file.  Prints
# "BASH_PID PY_PID PORT".
start_server() {
    local wd="$1"
    local state="$wd/work/current/.kanban-serve.json"

    YAKOS_WORK_DIR="$wd/work" CLAUDE_PROJECT_DIR="$wd" \
        bash "$KANBAN" serve --no-open >/dev/null 2>&1 &
    local bash_pid=$!
    register "$bash_pid"

    local i=0
    while [ "$i" -lt 30 ] && [ ! -f "$state" ]; do
        sleep 0.1; i=$((i+1))
    done
    [ -f "$state" ] || {
        printf 'FATAL: %s: state file not written within 3s\n' "$wd" >&2
        return 1
    }
    local py_pid port
    py_pid="$(pgrep -P "$bash_pid" 2>/dev/null | head -1)"
    port="$(sed -n 's/.*"port":[[:space:]]*\([0-9]*\).*/\1/p' "$state" | head -1)"
    printf '%s %s %s' "$bash_pid" "$py_pid" "$port"
}

# spawn_orphan_server WORKDIR: start a minimal python loopback TCP server that
# looks exactly like a real kanban server for WORKDIR's board to the scan:
#   - parent bash process has "kanban.sh" in its argv
#   - own python argv contains this board's canonical path as a token
#   - listening on 127.0.0.1
# Does NOT write a state file (simulates a leaked orphan).
# Prints the python PID.
spawn_orphan_server() {
    local wd="$1"
    local board_canon
    board_canon="$(canon_board "$wd")"

    # Wrapper script: named fake-kanban.sh so "bash .../fake-kanban.sh" appears
    # in the parent's ps command, satisfying the kanban\.sh parent-cmd check.
    local wrapper="$wd/fake-kanban.sh"

    # Write the wrapper, injecting the canonical board path as a quoted literal.
    # The python process will be launched as:
    #   python3 - <board_canon>
    # so ps -o command= shows the path, matching _kanban_find_live_pids' filter.
    cat > "$wrapper" << WRAP_EOF
#!/usr/bin/env bash
# Simulates the parent-command check in _kanban_find_live_pids.
python3 - '$board_canon' <<'PYEOF'
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
print(srv.server_address[1], flush=True)
srv.serve_forever()
PYEOF
WRAP_EOF
    chmod +x "$wrapper"

    local port_out
    port_out="$(mktemp -t yakos-port-XXXXXX)"
    bash "$wrapper" > "$port_out" &
    local wrap_pid=$!
    register "$wrap_pid"

    local i=0 port=""
    while [ "$i" -lt 30 ]; do
        port="$(head -1 "$port_out" 2>/dev/null)"
        [ -n "$port" ] && break
        sleep 0.1; i=$((i+1))
    done
    rm -f "$port_out"

    local py_pid
    py_pid="$(pgrep -P "$wrap_pid" 2>/dev/null | head -1)"
    [ -n "$py_pid" ] || { printf 'FATAL: orphan server did not start\n' >&2; return 1; }
    register "$py_pid"
    printf '%s' "$py_pid"
}

# port_listening PID: exit 0 if the pid is bound on 127.0.0.1 TCP LISTEN.
port_listening() {
    lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP -a -p "$1" >/dev/null 2>&1
}

# run_kanban WORKDIR [args...]: run kanban.sh with the given work dir env.
run_kanban() {
    local wd="$1"; shift
    YAKOS_WORK_DIR="$wd/work" CLAUDE_PROJECT_DIR="$wd" bash "$KANBAN" "$@"
}

# ── Case 1: fast path — state file present and live ──────────────────────────

wd1="$(make_work)"
result="$(start_server "$wd1")"
read -r bp1 pp1 _ <<< "$result"

ports_before="$(lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP 2>/dev/null | wc -l | tr -d ' ')"
out="$(run_kanban "$wd1" serve --no-open 2>&1)"
rc=$?
ports_after="$(lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP 2>/dev/null | wc -l | tr -d ' ')"

check "case1: second serve exits 0" \
    "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "case1: second serve prints 'already running'" \
    "$(printf '%s' "$out" | grep -q 'already running' && echo 1 || echo 0)"
check "case1: no additional loopback port opened" \
    "$([ "$ports_after" -le "$ports_before" ] && echo 1 || echo 0)"

kill "$pp1" 2>/dev/null || true; kill "$bp1" 2>/dev/null || true; sleep 0.2

# ── Case 2: scan path — state file absent, server live ───────────────────────

wd2="$(make_work)"
result="$(start_server "$wd2")"
read -r bp2 pp2 _ <<< "$result"
state2="$wd2/work/current/.kanban-serve.json"

check "case2: state file present after first serve" \
    "$([ -f "$state2" ] && echo 1 || echo 0)"

rm -f "$state2"

ports_before="$(lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP 2>/dev/null | wc -l | tr -d ' ')"
out="$(run_kanban "$wd2" serve --no-open 2>&1)"
rc=$?
ports_after="$(lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP 2>/dev/null | wc -l | tr -d ' ')"

check "case2: second serve exits 0" \
    "$([ "$rc" = "0" ] && echo 1 || echo 0)"
check "case2: second serve prints 'already running'" \
    "$(printf '%s' "$out" | grep -q 'already running' && echo 1 || echo 0)"
check "case2: no additional loopback port opened" \
    "$([ "$ports_after" -le "$ports_before" ] && echo 1 || echo 0)"
check "case2: state file recovered after scan" \
    "$([ -f "$state2" ] && echo 1 || echo 0)"
recovered_pid="$(sed -n 's/.*"pid":[[:space:]]*\([0-9]*\).*/\1/p' "$state2" | head -1)"
check "case2: recovered pid matches live python pid" \
    "$([ "$recovered_pid" = "$pp2" ] && echo 1 || echo 0)"

kill "$pp2" 2>/dev/null || true; kill "$bp2" 2>/dev/null || true; sleep 0.2

# ── Case 3: stop kills the state-file-recorded pid ───────────────────────────

wd3="$(make_work)"
result="$(start_server "$wd3")"
read -r _ pp3 _ <<< "$result"

run_kanban "$wd3" stop >/dev/null 2>&1
sleep 0.3

check "case3: stop terminates the python pid" \
    "$(kill -0 "$pp3" 2>/dev/null && echo 0 || echo 1)"
check "case3: state file removed after stop" \
    "$([ -f "$wd3/work/current/.kanban-serve.json" ] && echo 0 || echo 1)"

# ── Case 4: stop kills an orphan NOT in the state file ───────────────────────

wd4="$(make_work)"
result="$(start_server "$wd4")"
read -r _ pp4 _ <<< "$result"
rm -f "$wd4/work/current/.kanban-serve.json"

check "case4: orphan pid alive before stop" \
    "$(kill -0 "$pp4" 2>/dev/null && echo 1 || echo 0)"

out="$(run_kanban "$wd4" stop 2>&1)"
sleep 0.3

check "case4: orphan pid dead after stop" \
    "$(kill -0 "$pp4" 2>/dev/null && echo 0 || echo 1)"
check "case4: stop output mentions 'stopped'" \
    "$(printf '%s' "$out" | grep -q 'stopped' && echo 1 || echo 0)"

# ── Case 5: stop kills two servers (state-file pid + same-board orphan) ───────
# spawn_orphan_server mimics a real kanban server for wd5's board: it embeds
# the canonical board path in its python argv and names its parent wrapper
# "fake-kanban.sh", satisfying all three conditions in _kanban_find_live_pids.

wd5="$(make_work)"
result="$(start_server "$wd5")"
read -r _ pp5 _ <<< "$result"

orphan_py="$(spawn_orphan_server "$wd5")"
sleep 0.3   # let lsof register the new listener

check "case5: recorded pid alive before stop" \
    "$(kill -0 "$pp5" 2>/dev/null && echo 1 || echo 0)"
check "case5: orphan pid alive before stop" \
    "$(kill -0 "$orphan_py" 2>/dev/null && echo 1 || echo 0)"
check "case5: orphan pid is listening on 127.0.0.1" \
    "$(port_listening "$orphan_py" && echo 1 || echo 0)"

out="$(run_kanban "$wd5" stop 2>&1)"
sleep 0.5

check "case5: recorded pid dead after stop" \
    "$(kill -0 "$pp5" 2>/dev/null && echo 0 || echo 1)"
check "case5: orphan pid dead after stop" \
    "$(kill -0 "$orphan_py" 2>/dev/null && echo 0 || echo 1)"
killed_count="$(printf '%s' "$out" | grep -c 'stopped (pid' || true)"
check "case5: stop reports 2 pids killed" \
    "$([ "$killed_count" -ge 2 ] && echo 1 || echo 0)"

# ── Case 6: cross-project isolation ──────────────────────────────────────────
# Decisive safety case: `stop` for project A must NOT kill project B's server.
# Also: `serve` in project A with stale state must NOT adopt project B's server.
#
# Both boards have their own distinct canonical kanban.md paths.  The scan
# filters by board path, so project B's server is invisible from project A.

wdA="$(make_work)"
wdB="$(make_work)"

resultA="$(start_server "$wdA")"
read -r _ ppA portA <<< "$resultA"

resultB="$(start_server "$wdB")"
read -r bpB ppB portB <<< "$resultB"

check "case6: project A server alive" \
    "$(kill -0 "$ppA" 2>/dev/null && echo 1 || echo 0)"
check "case6: project B server alive" \
    "$(kill -0 "$ppB" 2>/dev/null && echo 1 || echo 0)"
check "case6: A and B on different ports" \
    "$([ "$portA" != "$portB" ] && echo 1 || echo 0)"

# Remove project A's state file so it has to use the scan path.
rm -f "$wdA/work/current/.kanban-serve.json"

# serve for project A (stale state) must NOT adopt project B's server.
out_serve="$(run_kanban "$wdA" serve --no-open 2>&1)"
check "case6: serve A with stale state finds A's server (not B's)" \
    "$(printf '%s' "$out_serve" | grep -q 'already running' && echo 1 || echo 0)"
# The recovered URL must contain A's port, not B's.
check "case6: serve A recovered URL matches A's port" \
    "$(printf '%s' "$out_serve" | grep -q ":${portA}" && echo 1 || echo 0)"
check "case6: serve A did NOT report B's port" \
    "$(printf '%s' "$out_serve" | grep -qv ":${portB}" && echo 1 || echo 0)"

# Now stop project A — project B's server must survive.
run_kanban "$wdA" stop >/dev/null 2>&1
sleep 0.3

check "case6: project A server dead after stop A" \
    "$(kill -0 "$ppA" 2>/dev/null && echo 0 || echo 1)"
check "case6: project B server STILL ALIVE after stop A" \
    "$(kill -0 "$ppB" 2>/dev/null && echo 1 || echo 0)"
check "case6: project B still listening on its port after stop A" \
    "$(port_listening "$ppB" && echo 1 || echo 0)"

kill "$ppB" 2>/dev/null || true; kill "$bpB" 2>/dev/null || true; sleep 0.1

# ── Summary ──────────────────────────────────────────────────────────────────

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" = "0" ]
