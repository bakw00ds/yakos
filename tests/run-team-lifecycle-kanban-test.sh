#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-team-lifecycle-kanban-test.sh — regression tests for team-lifecycle's
# kanban auto-move (K-90).
#
# K-90: kanban_move_first captured the first task under the source section but
# only "committed" the move when a later non-indented line ended the block. A
# task that was the literal last record of kanban.md (with or without a final
# newline) never hit that line, so it was silently DROPPED. The fix flushes the
# pending capture at end of input.
#
# Runs every case against the bash hook and, when a Go binary is available
# (YAKOS_GO_BINARY, default <repo>/bin/yakos), against `yakos hook run` too;
# both must produce byte-identical kanban.md files equal to the expectation.
#
# Exits 0 if every case passes.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/team-lifecycle.sh"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"

pass=0; fail=0
TMP="$(mktemp -d -t yakos-tl-kanban-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

WARN='# WARN: yakos kanban auto-update found src but no dst section'

payload() { # <tool>
    printf '{"session_id":"s-k90","hook_event_name":"PreToolUse","tool_name":"%s","tool_input":{"name":"t"}}\n' "$1"
}

# run_side <bash|go> <tool> <input-printf-fmt>  → prints resulting kanban.md path
run_side() {
    local side="$1" tool="$2" input="$3" d
    d="$(mktemp -d "$TMP/case.XXXXXX")"
    mkdir -p "$d/work/current" "$d/home"
    # shellcheck disable=SC2059
    printf "$input" > "$d/work/current/kanban.md"
    if [ "$side" = bash ]; then
        payload "$tool" | env HOME="$d/home" YAKOS_WORK_DIR="$d/work" CLAUDE_PROJECT_DIR="$d" \
            bash "$HOOK" >/dev/null 2>&1 || true
    else
        payload "$tool" | env HOME="$d/home" YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$d/work" \
            CLAUDE_PROJECT_DIR="$d" "$GO_BINARY" hook run team-lifecycle >/dev/null 2>&1 || true
    fi
    printf '%s' "$d/work/current/kanban.md"
}

# kcase <name> <tool> <input-printf-fmt> <expected-printf-fmt>
kcase() {
    local name="$1" tool="$2" input="$3" want="$4" side got wantf
    wantf="$TMP/want.$$"
    # shellcheck disable=SC2059
    printf "$want" > "$wantf"
    for side in bash go; do
        if [ "$side" = go ] && [ ! -x "$GO_BINARY" ]; then continue; fi
        got="$(run_side "$side" "$tool" "$input")"
        if cmp -s "$got" "$wantf"; then
            printf '  \033[32mOK\033[0m   %s [%s]\n' "$name" "$side"; pass=$((pass + 1))
        else
            printf '  \033[31mFAIL\033[0m %s [%s]\n    want: %s\n    got:  %s\n' "$name" "$side" \
                "$(od -c "$wantf" | head -8 | tr -s ' ' | tr '\n' '|')" \
                "$(od -c "$got" | head -8 | tr -s ' ' | tr '\n' '|')"
            fail=$((fail + 1))
        fi
    done
}

[ -x "$GO_BINARY" ] || printf '  (Go binary not found at %s; bash side only)\n' "$GO_BINARY"

# 1. Reported bug: last task, no trailing newline, no DONE section.
kcase "last task, no final newline, no DONE" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n- [-] K-1 task' \
    "## TODO\n\n## IN PROGRESS\n$WARN\n- [-] K-1 task\n"

# 2. Same with a final newline (also dropped before the fix).
kcase "last task, final newline, no DONE" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n- [-] K-1 task\n' \
    "## TODO\n\n## IN PROGRESS\n$WARN\n- [-] K-1 task\n"

# 3. Continuation lines are part of the block and stay attached.
kcase "last task with continuation lines" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n- [-] K-1 task\n  - notes: x\n  - blockers: none' \
    "## TODO\n\n## IN PROGRESS\n$WARN\n- [-] K-1 task\n  - notes: x\n  - blockers: none\n"

# 4. TeamCreate direction; destination section precedes the source section.
kcase "TeamCreate, TODO last, IN PROGRESS earlier" TeamCreate \
    '## IN PROGRESS\n\n## TODO\n- [ ] K-2 t2' \
    "## IN PROGRESS\n\n## TODO\n$WARN\n- [ ] K-2 t2\n"

# 5. Trailing spaces on the last task line survive verbatim.
kcase "last task with trailing spaces" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n- [-] K-1 task   ' \
    "## TODO\n\n## IN PROGRESS\n$WARN\n- [-] K-1 task   \n"

# 6. CRLF file, no final newline. \r is preserved on original lines.
kcase "CRLF, last task, no final newline" TeamDelete \
    '## TODO\r\n\r\n## IN PROGRESS\r\n- [-] K-1 task\r\n  - n: x\r' \
    "## TODO\r\n\r\n## IN PROGRESS\r\n$WARN\n- [-] K-1 task\r\n  - n: x\r\n"

# 7. Guard: empty DONE section as the final line still receives the task.
kcase "empty DONE last, task not last" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n- [-] K-1 task\n\n## DONE' \
    '## TODO\n\n## IN PROGRESS\n\n## DONE\n- [x] K-1 task\n'

# 8. Guard: normal board is unchanged in behavior.
kcase "normal board TeamCreate" TeamCreate \
    '## TODO\n- [ ] K-1 a\n  - n: 1\n- [ ] K-2 b\n\n## IN PROGRESS\n\n## DONE\n' \
    '## TODO\n- [ ] K-2 b\n\n## IN PROGRESS\n- [-] K-1 a\n  - n: 1\n\n## DONE\n'

# 9. Nothing to move: an empty source section leaves the file alone.
kcase "empty source section" TeamDelete \
    '## TODO\n\n## IN PROGRESS\n\n## DONE\n' \
    '## TODO\n\n## IN PROGRESS\n\n## DONE\n'

printf '\n  %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
