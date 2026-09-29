#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-hardening-test.sh — K-101 regression tests for the bash hook libs
# and plan-quality-gate.sh.
#
#   1. hi_init must not trust a lying jq. A jq that prints "garbage", `[1]`,
#      "object", or the payload's own event name for every query must make blocking hooks exit 2 and non-blocking
#      hooks exit 0 with a WARN and NO stdout. The blocking set must equal the
#      registry's FailClosed set.
#   2. plan-quality-gate's EXIT trap must still return 2 with stderr/stdout
#      closed, a broken pipe, or SIGTERM/SIGHUP mid-run (anything but 2 is
#      non-blocking in Claude Code).
#   3. A helper lib with a syntax error must not load "successfully": the
#      *_LOADED sentinels are set on the last line, and the gate checks them.
#
# Every case runs under `bash` (first on PATH) and /bin/bash (3.2 on macOS).
# Usage: bash tests/run-hook-hardening-test.sh
set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
unset YAKOS_ROOT YAKOS_LIB || true

REAL_JQ="$(command -v jq || true)"
[ -n "$REAL_JQ" ] || { echo "jq required" >&2; exit 1; }

TMP="$(mktemp -d -t yakos-hardening-XXXXXX)"
cleanup() { chmod -R u+rwx "$TMP" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT

pass=0; fail=0; fail_log=""
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# A copy of the hooks tree with symlinks resolved (lib/paths.sh and compat.sh
# are symlinks into cli/lib) so a case can corrupt one file without touching
# the repo.
copy_hooks() { rm -rf "$1"; cp -RL "$HOOKS" "$1"; }

# corrupt <file>: put a syntax error just BEFORE the file's final
# "must stay the last statement" sentinel line, i.e. where a real mid-file
# parse error would sit. (An error appended after the sentinel proves nothing:
# the sentinel has already run.)
corrupt() {
    awk '/^# Must stay the last statement/ && !d { print "if then fi )("; d = 1 } { print }' "$1" > "$1.new" && mv "$1.new" "$1"
    grep -q '^if then fi )($' "$1" || { echo "corrupt: marker line not found in $1" >&2; exit 1; }
}

# crash <file>: override yakos_current_dir so it fails; the hook's
# `current_dir="$(yakos_current_dir)"` then dies of `set -e` with status 1 on
# both bash 3.2 and 5. That is the path where the EXIT trap has to rewrite 1 -> 2 and
# print its message, so it is the case where a failing write inside the trap
# matters.
crash() {
    awk '/^# Must stay the last statement/ && !d { print "yakos_current_dir() { return 1; } # k101-crash"; d = 1 } { print }' "$1" > "$1.new" && mv "$1.new" "$1"
    grep -q 'k101-crash' "$1" || { echo "crash: marker line not found in $1" >&2; exit 1; }
}

# ---- fake jq PATHs ------------------------------------------------------------
mk_fakejq() { # <dir> <literal-line-to-print>
    mkdir -p "$1"
    local d b n
    for d in /usr/bin /bin /usr/local/bin /opt/homebrew/bin; do
        [ -d "$d" ] || continue
        for b in "$d"/*; do
            [ -x "$b" ] || continue
            n="$(basename -- "$b")"
            [ "$n" = jq ] && continue
            [ -e "$1/$n" ] || ln -sf "$b" "$1/$n" 2>/dev/null || true
        done
    done
    printf '#!/bin/sh\nprintf "%%s\\n" %s\n' "'$2'" > "$1/jq"
    chmod +x "$1/jq"
}
mk_fakejq "$TMP/fj-garbage" 'garbage'
mk_fakejq "$TMP/fj-array"   '[1]'
mk_fakejq "$TMP/fj-object"  'object'
# Echoes the payload's own event name for every query: passes the
# "hook_event_name is present in the payload" cross-check, so only the
# arithmetic canary in hi_init can catch it.
mk_fakejq "$TMP/fj-event"   'PreToolUse'

new_sandbox() {
    local root="$TMP/$1"
    mkdir -p "$root/work/current/logs" "$root/home" "$root/proj" "$root/coord/proj/coord"
    printf '%s' "$root"
}

PAYLOAD='{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/x/a.go","content":"x"}}'
GATE_PAYLOAD='{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{"prompt":"x"}}'

# run <shell> <hookdir> <hook> <sandbox> <payload> [PATH=...] -> rc, out, err
run() {
    local sh="$1" hd="$2" hook="$3" sb="$4" payload="$5" path="${6:-$PATH}"
    rc=0
    printf '%s' "$payload" | env PATH="$path" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" \
        CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" \
        "$sh" "$hd/$hook" >"$sb/out" 2>"$sb/err" || rc=$?
    out="$(cat "$sb/out")"; err="$(cat "$sb/err")"
}

# ---- registry <-> bash agreement ---------------------------------------------
REG="$REPO_ROOT/cli-go/internal/hooks/registry/registry.go"
reg_closed="$(awk '/Name:[[:space:]]*"/ { gsub(/.*Name:[[:space:]]*"/,""); gsub(/".*/,""); n=$0 } /FailClosed:[[:space:]]*true/ { print n }' "$REG" | sort -u | tr '\n' ' ')"
bash_closed=""
for f in "$HOOKS"/*.sh; do
    if grep -q '^HOOK_FAIL_CLOSED=1' "$f" 2>/dev/null; then bash_closed="$bash_closed$(basename -- "$f" .sh)
"; fi
done
bash_closed="$(printf '%s' "$bash_closed" | sort -u | tr '\n' ' ')"
echo "== registry agreement =="
if [ -n "$reg_closed" ] && [ "$reg_closed" = "$bash_closed" ]; then
    ok "registry FailClosed set == bash HOOK_FAIL_CLOSED=1 set ($reg_closed)"
else
    bad "registry FailClosed [$reg_closed] != bash HOOK_FAIL_CLOSED=1 [$bash_closed]"
fi
BLOCKING=$reg_closed
NONBLOCKING="$(awk '/Name:[[:space:]]*"/ { gsub(/.*Name:[[:space:]]*"/,""); gsub(/".*/,""); n=$0 } /FailClosed:[[:space:]]*false/ { print n }' "$REG" | sort -u | tr '\n' ' ')"

run_suite() {
    local SH="$1" L="$2" fj h sb
    echo "== $L =="

    # ---- 1. lying jq -----------------------------------------------------------
    for fj in garbage array object event; do
        for h in $BLOCKING; do
            [ -f "$HOOKS/$h.sh" ] || continue
            sb="$(new_sandbox "j-$L-$h-$fj")"
            run "$SH" "$HOOKS" "$h.sh" "$sb" "$PAYLOAD" "$TMP/fj-$fj"
            if [ "$rc" = 2 ] && [ -z "$out" ] && printf '%s' "$err" | grep -q 'BLOCKED'; then
                ok "$L: blocking $h + jq($fj) -> exit 2 with reason"
            else
                bad "$L: blocking $h + jq($fj) — want rc=2 no stdout BLOCKED; got rc=$rc out=[$out] err=[$err]"
            fi
        done
        for h in $NONBLOCKING; do
            [ -f "$HOOKS/$h.sh" ] || continue
            sb="$(new_sandbox "j-$L-$h-$fj")"
            run "$SH" "$HOOKS" "$h.sh" "$sb" "$PAYLOAD" "$TMP/fj-$fj"
            if [ "$rc" = 0 ] && [ -z "$out" ] && printf '%s' "$err" | grep -q 'WARN'; then
                ok "$L: non-blocking $h + jq($fj) -> exit 0, WARN, no stdout"
            else
                bad "$L: non-blocking $h + jq($fj) — want rc=0 no stdout WARN; got rc=$rc out=[$out] err=[$err]"
            fi
        done
    done

    # A real-jq payload with no hook_event_name is not a hook payload.
    sb="$(new_sandbox "j-$L-noevent")"
    run "$SH" "$HOOKS" path-allowlist.sh "$sb" '{"tool_name":"Write","tool_input":{"file_path":"/x/a.go"}}'
    if [ "$rc" = 2 ]; then ok "$L: blocking hook + object without hook_event_name -> exit 2"; else bad "$L: no hook_event_name rc=$rc err=[$err]"; fi
    run "$SH" "$HOOKS" path-log.sh "$sb" '{"tool_name":"Write","tool_input":{"file_path":"/x/a.go"}}'
    if [ "$rc" = 0 ] && [ -z "$out" ]; then ok "$L: non-blocking hook + object without hook_event_name -> exit 0 no stdout"; else bad "$L: non-blocking no hook_event_name rc=$rc out=[$out]"; fi

    # ---- 2. plan-quality-gate exit-trap robustness ----------------------------
    sb="$(new_sandbox "t-$L-marker")"
    printf '{"plan_id":"plan-abc","reason":"low"}\n' > "$sb/work/current/.plan-blocked"
    run "$SH" "$HOOKS" plan-quality-gate.sh "$sb" "$GATE_PAYLOAD"
    if [ "$rc" = 2 ]; then ok "$L: gate marker present baseline -> 2"; else bad "$L: gate baseline rc=$rc err=[$err]"; fi

    rc=0
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&- || rc=$?
    if [ "$rc" = 2 ]; then ok "$L: gate marker + stderr closed -> 2"; else bad "$L: gate stderr closed rc=$rc"; fi

    rc=0
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$HOOKS/plan-quality-gate.sh" >&- 2>/dev/null || rc=$?
    if [ "$rc" = 2 ]; then ok "$L: gate marker + stdout closed -> 2"; else bad "$L: gate stdout closed rc=$rc"; fi

    rc=0
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$HOOKS/plan-quality-gate.sh" 2>&- >&- || rc=$?
    if [ "$rc" = 2 ]; then ok "$L: gate marker + stdout and stderr closed -> 2"; else bad "$L: gate both closed rc=$rc"; fi

    # Broken pipe: the reader exits at once, so the hook's stderr writes hit EPIPE.
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$HOOKS/plan-quality-gate.sh" 2>&1 >/dev/null | true
    rc="${PIPESTATUS[1]}"
    if [ "$rc" = 2 ]; then ok "$L: gate marker + broken stderr pipe -> 2"; else bad "$L: gate broken pipe rc=$rc (141 = SIGPIPE)"; fi

    # Crash path (rc 1 inside the script) with unwritable stderr: the trap must
    # still exit 2 even though its own message cannot be delivered.
    local cd="$TMP/c-$L"
    copy_hooks "$cd"; crash "$cd/lib/paths.sh"
    sb="$(new_sandbox "t-$L-crash")"
    run "$SH" "$cd" plan-quality-gate.sh "$sb" "$GATE_PAYLOAD"
    if [ "$rc" = 2 ] && printf '%s' "$err" | grep -q 'internal error'; then ok "$L: gate lib crash (rc 1) -> 2 with message"; else bad "$L: gate lib crash rc=$rc err=[$err]"; fi
    rc=0
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$cd/plan-quality-gate.sh" >/dev/null 2>&- || rc=$?
    if [ "$rc" = 2 ]; then ok "$L: gate lib crash + stderr closed -> 2"; else bad "$L: gate crash + stderr closed rc=$rc"; fi
    printf '%s' "$GATE_PAYLOAD" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$cd/plan-quality-gate.sh" 2>&1 >/dev/null | true
    rc="${PIPESTATUS[1]}"
    if [ "$rc" = 2 ]; then ok "$L: gate lib crash + broken stderr pipe -> 2"; else bad "$L: gate crash + broken pipe rc=$rc"; fi

    # Signals mid-run. A shim jq sleeps once so there is a window to signal
    # a live hook; no marker, so an un-fixed hook would otherwise pass (0).
    local sig
    for sig in TERM HUP; do
        sb="$(new_sandbox "t-$L-sig-$sig")"
        mkdir -p "$sb/slow"
        local flag="$sb/slow/once"
        printf '#!/bin/sh\nif [ ! -e "%s" ]; then : > "%s"; sleep 2; fi\nexec "%s" "$@"\n' "$flag" "$flag" "$REAL_JQ" > "$sb/slow/jq"
        chmod +x "$sb/slow/jq"
        printf '%s' "$GATE_PAYLOAD" > "$sb/payload"
        env PATH="$sb/slow:$PATH" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
            "$SH" "$HOOKS/plan-quality-gate.sh" < "$sb/payload" >"$sb/out" 2>"$sb/err" &
        local pid=$!
        sleep 0.7
        kill -"$sig" "$pid" 2>/dev/null || true
        rc=0; wait "$pid" || rc=$?
        if [ "$rc" = 2 ]; then ok "$L: gate SIG$sig mid-run -> 2"; else bad "$L: gate SIG$sig rc=$rc (143=TERM, 129=HUP)"; fi
    done

    # ---- 3. helper-lib syntax error -------------------------------------------
    local lib
    for lib in paths.sh hook-output.sh hook-input.sh; do
        local hd="$TMP/h-$L-$lib"
        copy_hooks "$hd"
        corrupt "$hd/lib/$lib"
        sb="$(new_sandbox "s-$L-$lib")"
        run "$SH" "$hd" plan-quality-gate.sh "$sb" "$GATE_PAYLOAD"
        if [ "$rc" = 2 ]; then ok "$L: gate + syntax error in lib/$lib -> 2"; else bad "$L: syntax error in $lib rc=$rc err=[$err]"; fi
    done
    local hd="$TMP/h-$L-clean"
    copy_hooks "$hd"
    sb="$(new_sandbox "s-$L-clean")"
    run "$SH" "$hd" plan-quality-gate.sh "$sb" "$GATE_PAYLOAD"
    if [ "$rc" = 0 ]; then ok "$L: gate + pristine libs, no marker -> 0 (control)"; else bad "$L: pristine control rc=$rc err=[$err]"; fi

    # Sentinels: set only when the whole file was consumed, and idempotent.
    local pair f v
    for pair in "hook-input.sh:HI_LOADED" "hook-output.sh:HO_LOADED" "paths.sh:YAKOS_PATHS_LOADED" "compat.sh:YAKOS_COMPAT_LOADED"; do
        f="${pair%%:*}"; v="${pair##*:}"
        if "$SH" -c ". '$hd/lib/$f' && . '$hd/lib/$f' && [ \"\${$v:-0}\" = 1 ]" 2>/dev/null; then
            ok "$L: $f sets $v after a clean, repeated source"
        else
            bad "$L: $f did not set $v"
        fi
        cp "$hd/lib/$f" "$TMP/broken-$L-$f"
        corrupt "$TMP/broken-$L-$f"
        if "$SH" -c ". '$TMP/broken-$L-$f'; [ \"\${$v:-0}\" != 1 ]" 2>/dev/null; then
            ok "$L: $f with a syntax error leaves $v unset"
        else
            bad "$L: $f with a syntax error still set $v"
        fi
    done
}

run_suite bash bash5
[ -x /bin/bash ] && [ "$(command -v bash)" != /bin/bash ] && run_suite /bin/bash bin-bash || true

echo
echo "hook-hardening: $pass passed, $fail failed"
if [ "$fail" -ne 0 ]; then printf '%b' "$fail_log"; exit 1; fi
