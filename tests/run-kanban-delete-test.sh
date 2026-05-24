#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-kanban-delete-test.sh — integration tests for `yakos kanban delete`.
#
# Exercises cmd_delete (and rm alias) against a scratch kanban.md:
#   1. delete a task from TODO; neighbours untouched
#   2. delete a task from IN PROGRESS (with notes)
#   3. rm alias works
#   4. error on non-existent id (non-zero exit, clear message)
#   5. delete from DONE column
#
# Exits 0 if all cases pass. Prints PASS / FAIL per case.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
KANBAN="$REPO_ROOT/cli/lib/kanban.sh"

# Need YAKOS_LIB for compat.sh and paths.sh sourced inside kanban.sh.
export YAKOS_LIB="$REPO_ROOT/cli/lib"

pass=0
fail=0
WORK_TMP=""

# ── helpers ──────────────────────────────────────────────────────────────────

# setup_work: create a fresh temp work dir, export env vars, set WORK_TMP.
setup_work() {
    WORK_TMP="$(mktemp -d -t yakos-kanban-delete-XXXXXX)"
    mkdir -p "$WORK_TMP/work/current"
    export YAKOS_WORK_DIR="$WORK_TMP/work"
    # CLAUDE_PROJECT_DIR must exist for yakos_current_dir (paths.sh).
    export CLAUDE_PROJECT_DIR="$WORK_TMP"
}

teardown_work() {
    [ -n "$WORK_TMP" ] && rm -rf "$WORK_TMP"
    WORK_TMP=""
}

kanban() { bash "$KANBAN" "$@"; }

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

# ── case 1: delete a task from TODO; neighbours untouched ───────────────────

setup_work
kanban add "keep me"   --category chore  >/dev/null 2>&1
kanban add "delete me" --category bug    >/dev/null 2>&1
kanban add "keep me 2" --category chore  >/dev/null 2>&1
# Board: K-1=keep me (chore), K-2=delete me (bug), K-3=keep me 2 (chore)

out="$(kanban delete K-2 2>&1)"
board="$WORK_TMP/work/current/kanban.md"
check "case1: task removed from file"       "$( grep -q 'K-2'     "$board" && echo 0 || echo 1 )"
check "case1: echo shows id and title"      "$( printf '%s' "$out" | grep -q 'K-2' && echo 1 || echo 0 )"
check "case1: neighbours untouched (K-1)"   "$( grep -q 'K-1'     "$board" && echo 1 || echo 0 )"
check "case1: neighbours untouched (K-3)"   "$( grep -q 'K-3'     "$board" && echo 1 || echo 0 )"
check "case1: sub-fields also removed"      "$( grep -q 'category: bug' "$board" && echo 0 || echo 1 )"
teardown_work

# ── case 2: delete a task from IN PROGRESS (with notes) ─────────────────────

setup_work
kanban add "task with notes" --category feature --notes "important note" >/dev/null 2>&1
kanban add "sibling task"    --category chore                             >/dev/null 2>&1
kanban move K-1 "IN PROGRESS" >/dev/null 2>&1
board="$WORK_TMP/work/current/kanban.md"

kanban delete K-1 >/dev/null 2>&1
check "case2: task removed from IN PROGRESS"  "$( grep -q 'K-1'           "$board" && echo 0 || echo 1 )"
check "case2: notes sub-field also removed"   "$( grep -q 'important note' "$board" && echo 0 || echo 1 )"
check "case2: sibling K-2 untouched"          "$( grep -q 'K-2'           "$board" && echo 1 || echo 0 )"
teardown_work

# ── case 3: rm alias ────────────────────────────────────────────────────────

setup_work
kanban add "rm alias test" --category chore >/dev/null 2>&1
board="$WORK_TMP/work/current/kanban.md"
kanban rm K-1 >/dev/null 2>&1
check "case3: rm alias removes task"  "$( grep -q 'K-1' "$board" && echo 0 || echo 1 )"
teardown_work

# ── case 4: error on non-existent id ────────────────────────────────────────

setup_work
kanban add "existing task" >/dev/null 2>&1
err_out="$(kanban delete K-99 2>&1 || true)"
actual_rc=0; kanban delete K-99 >/dev/null 2>&1 || actual_rc=$?
check "case4: non-zero exit for missing id"  "$( [ "$actual_rc" != "0" ] && echo 1 || echo 0 )"
check "case4: error message mentions id"     "$( printf '%s' "$err_out" | grep -q 'K-99' && echo 1 || echo 0 )"
teardown_work

# ── case 5: delete from DONE column ─────────────────────────────────────────

setup_work
kanban add "completed task" --category chore >/dev/null 2>&1
kanban done K-1 >/dev/null 2>&1
board="$WORK_TMP/work/current/kanban.md"
kanban delete K-1 >/dev/null 2>&1
check "case5: task removed from DONE column"  "$( grep -q 'K-1' "$board" && echo 0 || echo 1 )"
teardown_work

# ── summary ─────────────────────────────────────────────────────────────────

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" = "0" ]
