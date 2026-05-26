#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-kanban-freeform-parser-test.sh — regression tests for freeform-board parsing.
#
# Verifies that `yakos kanban serve` /api/board correctly parses hand-authored
# freeform kanban.md boards (like pandaOS's) where tasks use `- [<id>] <title>`
# instead of the strict tool-authored `- [ ] K-N — title` grammar.
#
# Cases:
#   1. Freeform board counts (TODO/IN PROGRESS/DONE must be non-zero)
#   2. Freeform ids preserved verbatim (#219, 71.2, gov, CI)
#   3. Title extracted correctly: with separator (— or -) and without
#   4. Strict K-N board still parses correctly (backward compat)
#
# Requires python3 and curl.  Exits 0 if all cases pass.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
KANBAN="$REPO_ROOT/cli/lib/kanban.sh"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

# shellcheck source=/dev/null
. "$YAKOS_LIB/compat.sh"
# shellcheck source=/dev/null
. "$YAKOS_LIB/paths.sh"

command -v python3 >/dev/null 2>&1 || { printf 'SKIP: python3 not found\n'; exit 0; }
command -v curl    >/dev/null 2>&1 || { printf 'SKIP: curl not found\n';    exit 0; }

pass=0
fail=0
ALL_PIDS=""
ALL_WORK_DIRS=""

cleanup() {
    for _p in $ALL_PIDS; do kill "$_p" 2>/dev/null || true; done
    for _wd in $ALL_WORK_DIRS; do rm -rf "$_wd" 2>/dev/null || true; done
}
trap cleanup EXIT

register_pid() { ALL_PIDS="$ALL_PIDS $1"; }

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

# make_work_with_board <board-content-here-doc-path>: create isolated work dir,
# write the provided file as kanban.md, return the work dir path.
make_work() {
    local tmp
    tmp="$(mktemp -d -t yakos-freeform-XXXXXX)"
    ALL_WORK_DIRS="$ALL_WORK_DIRS $tmp"
    mkdir -p "$tmp/work/current"
    printf '%s' "$tmp"
}

# start_server WORKDIR: launch `yakos kanban serve --no-open` against the
# isolated work dir; wait up to 3s for state file; return PORT.
start_server() {
    local wd="$1"
    local state="$wd/work/current/.kanban-serve.json"

    YAKOS_WORK_DIR="$wd/work" CLAUDE_PROJECT_DIR="$wd" \
        bash "$KANBAN" serve --no-open >/dev/null 2>&1 &
    local bash_pid=$!
    register_pid "$bash_pid"

    local i=0
    while [ "$i" -lt 30 ] && [ ! -f "$state" ]; do
        sleep 0.1; i=$((i+1))
    done
    [ -f "$state" ] || {
        printf 'FATAL: state file not written within 3s for %s\n' "$wd" >&2
        return 1
    }
    sed -n 's/.*"port":[[:space:]]*\([0-9]*\).*/\1/p' "$state" | head -1
}

stop_server() {
    local wd="$1"
    YAKOS_WORK_DIR="$wd/work" CLAUDE_PROJECT_DIR="$wd" \
        bash "$KANBAN" stop >/dev/null 2>&1 || true
}

# api_board PORT: fetch /api/board and print JSON.
api_board() {
    curl -sf "http://127.0.0.1:${1}/api/board" 2>/dev/null
}

# ── case 1 & 2 & 3: freeform pandaOS-style board ─────────────────────────────

WD="$(make_work)"
cat > "$WD/work/current/kanban.md" << 'BOARD'
# Kanban — pandaos · session started 2026-05-24T14:20:29Z

## TODO

- [71.2] Client profile schema + unit pref (PHI-aware migrations) — pandaos-database + security review (blocked on CI-green)
- [71.3] Onboarding engine backend (config-driven, reuses System Audit types) — pandaos-backend (blocked on CI-green)
- [71.4] Web onboarding UI (full-screen renderer + projection + Ben video) — pandaos-frontend (blocked on CI-green)
- [71.5] Mobile onboarding parity (Flutter) — pandaos-mobile (blocked on CI-green)
- [71.6] Projection calc (deficit→weight-over-time on Tools TDEE) — pandaos-backend (blocked on CI-green)
- [71.7] Magic-link invite from Clients page — pandaos-backend + frontend (blocked on CI-green)
- [71.8] Follow-on nudges (scheduled email/push, admin-configurable) — pandaos-backend + frontend (blocked on CI-green)
- [71.9] Delight layer DEFERRED — home-screen widget + app-store hand-off — pandaos-mobile
- [gov] Reconcile env-branch model vs main-trunk reality (develop is 2089 commits behind main)

## IN PROGRESS

- [CI] Triage main's red CI (gates all code-phase merges) — pandaos-troubleshoot
- [#219] Self-hosted CI migration — 15 pass / 11 fail; infra fixes in, app-layer failures under triage; NOT merged

## DONE

- [#235] Post-LKE docs refresh MERGED → main (1c2c1b38)
- [infra] Mac mini registered as self-hosted runner pandaos-macos-1 (online) — build-ios runs
- [71.0] Phase 71 plan committed → docs/architecture/phase-71-onboarding-overhaul-plan.md — PR #237 (open, docs-only)
- [71.1] Auth quick-wins (landing→/login + login restyle w/ Sign-Up button) authored + verified — PR #238 (open; merge pending CI-green)
BOARD

PORT="$(start_server "$WD")"

board_json="$(api_board "$PORT")"

# Column counts.
todo_count="$(  printf '%s' "$board_json" | python3 -c "import json,sys; b=json.load(sys.stdin); print(len(b['TODO']))"        2>/dev/null || printf '0')"
inp_count="$(   printf '%s' "$board_json" | python3 -c "import json,sys; b=json.load(sys.stdin); print(len(b['IN PROGRESS']))" 2>/dev/null || printf '0')"
done_count="$(  printf '%s' "$board_json" | python3 -c "import json,sys; b=json.load(sys.stdin); print(len(b['DONE']))"        2>/dev/null || printf '0')"

check "freeform: TODO has 9 tasks"        "$( [ "$todo_count"  = "9" ] && echo 1 || echo 0 )"
check "freeform: IN PROGRESS has 2 tasks" "$( [ "$inp_count"   = "2" ] && echo 1 || echo 0 )"
check "freeform: DONE has 4 tasks"        "$( [ "$done_count"  = "4" ] && echo 1 || echo 0 )"

# Id preservation.
ids_json="$(printf '%s' "$board_json" | python3 -c "
import json, sys
b = json.load(sys.stdin)
all_ids = [t['id'] for col in b.values() for t in col]
print(json.dumps(all_ids))
" 2>/dev/null || printf '[]')"

check "freeform: id #219 preserved"  "$( printf '%s' "$ids_json" | python3 -c "import json,sys; ids=json.load(sys.stdin); print('1' if '#219' in ids else '0')" 2>/dev/null )"
check "freeform: id 71.2 preserved"  "$( printf '%s' "$ids_json" | python3 -c "import json,sys; ids=json.load(sys.stdin); print('1' if '71.2' in ids else '0')" 2>/dev/null )"
check "freeform: id gov preserved"   "$( printf '%s' "$ids_json" | python3 -c "import json,sys; ids=json.load(sys.stdin); print('1' if 'gov'  in ids else '0')" 2>/dev/null )"
check "freeform: id CI preserved"    "$( printf '%s' "$ids_json" | python3 -c "import json,sys; ids=json.load(sys.stdin); print('1' if 'CI'   in ids else '0')" 2>/dev/null )"

# Title extraction: with separator.
title_ci="$(printf '%s' "$board_json" | python3 -c "
import json, sys
b = json.load(sys.stdin)
t = next((t for col in b.values() for t in col if t['id'] == 'CI'), None)
print(t['title'] if t else '')
" 2>/dev/null)"
check "freeform: CI title has separator stripped" \
    "$( printf '%s' "$title_ci" | grep -q '^Triage' && echo 1 || echo 0 )"

# Title extraction: WITHOUT separator (gov line has no em-dash/hyphen).
title_gov="$(printf '%s' "$board_json" | python3 -c "
import json, sys
b = json.load(sys.stdin)
t = next((t for col in b.values() for t in col if t['id'] == 'gov'), None)
print(t['title'] if t else '')
" 2>/dev/null)"
check "freeform: gov title correct (no separator in source)" \
    "$( printf '%s' "$title_gov" | grep -q '^Reconcile' && echo 1 || echo 0 )"

stop_server "$WD"

# ── case 4: strict K-N board backward compat ─────────────────────────────────

WD2="$(make_work)"
cat > "$WD2/work/current/kanban.md" << 'BOARD'
# Kanban — yakOS · session started 2026-05-24T13:16:08Z

## TODO
- [ ] K-3 — Broaden kanban serve parser to accept freeform [id] tokens
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
- [ ] K-2 — Wire supervisor PostToolUse hook into session launch
  - category: bug
  - assigned: unassigned
  - blockers: none
  - notes:
BOARD

PORT2="$(start_server "$WD2")"
board2_json="$(api_board "$PORT2")"

todo2="$(  printf '%s' "$board2_json" | python3 -c "import json,sys; b=json.load(sys.stdin); print(len(b['TODO']))"  2>/dev/null || printf '0')"
done2="$(  printf '%s' "$board2_json" | python3 -c "import json,sys; b=json.load(sys.stdin); print(len(b['DONE']))"  2>/dev/null || printf '0')"
id_k3="$(  printf '%s' "$board2_json" | python3 -c "
import json, sys
b = json.load(sys.stdin)
t = next((t for col in b.values() for t in col if t['id'] == 'K-3'), None)
print(t['id'] if t else 'MISSING')
" 2>/dev/null)"

check "strict: TODO has 1 task"          "$( [ "$todo2" = "1" ] && echo 1 || echo 0 )"
check "strict: DONE has 1 task"          "$( [ "$done2" = "1" ] && echo 1 || echo 0 )"
check "strict: K-3 id preserved"         "$( [ "$id_k3" = "K-3" ] && echo 1 || echo 0 )"

stop_server "$WD2"

# ── summary ───────────────────────────────────────────────────────────────────

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" = "0" ]
