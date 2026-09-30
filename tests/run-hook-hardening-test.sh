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
#   4. K-107: every registry-fail-closed hook (not just plan-quality-gate) must
#      exit 2 -- never 0/1/141/143 -- with stderr closed, a broken stderr pipe,
#      SIGTERM/SIGHUP mid-run, a truncated lib, a lib that is a directory, and a
#      lib with a syntax error. (Also: a jq that hangs, see section 5.)
#
# Every case runs under `bash` (first on PATH) and /bin/bash (3.2 on macOS).
# Usage: bash tests/run-hook-hardening-test.sh
# K-110: with no bash on PATH (for example a Windows runner without Git-bash)
# there is nothing to drive. Skip cleanly instead of failing on the first case.
# POSIX sh only: this runs before anything bash-specific is parsed.
if ! command -v "${YAKOS_HOOK_BASH:-bash}" >/dev/null 2>&1; then
    echo "SKIP: ${0##*/}: no ${YAKOS_HOOK_BASH:-bash} on PATH; the hook fixtures need bash" >&2
    exit 0
fi

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
# Proxies real jq for `jq -n ...` (so the arithmetic canary PASSES) and lies
# for every query that reads the payload. Only the identity cross-check in
# hi_init can catch this one.
mk_fakejq "$TMP/fj-proxy"   'BogusEventName'
printf '#!/bin/sh\ncase "$1" in -n*|-rn*|-nr*) exec "%s" "$@" ;; esac\nprintf "%%s\\n" BogusEventName\n' "$REAL_JQ" > "$TMP/fj-proxy/jq"
chmod +x "$TMP/fj-proxy/jq"

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


# ---- K-107: per-hook blocking scenarios -----------------------------------------
# gate_scenario <hook> <sandbox>: prepares the sandbox and sets GS_PAYLOAD/GS_ENV
# so that <hook> BLOCKS (exit 2, message on stderr). GS_ENV is a space-separated
# list of KEY=VAL words (no value contains a space).
GS_PASS_PAYLOAD='{"session_id":"gs1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"true"}}'
gate_scenario() {
    local h="$1" sb="$2"
    GS_ENV="YAKOS_PROJECT_NAME=proj"
    case "$h" in
        budget-guard)
            printf 'budget:\n  enabled: true\n  max_tool_calls: 1\n' > "$sb/proj/.yakos.yml"
            printf '{"session_id":"gs1","started_at":%s,"tool_call_count":1,"last_tool":"Read","last_tool_run_count":1}\n' "$(date +%s)" > "$sb/work/current/.budget-state.json"
            GS_PAYLOAD='{"session_id":"gs1","hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/x"}}' ;;
        path-allowlist)
            mkdir -p "$sb/proj/.claude"
            printf '{"lead":{"allow":["**"],"deny":[]},"go-api":{"allow":["api/**"],"deny":[]}}\n' > "$sb/proj/.claude/path-allowlist.json"
            GS_PAYLOAD='{"session_id":"gs1","hook_event_name":"PreToolUse","agent_type":"go-api","tool_name":"Edit","tool_input":{"file_path":"web/index.js","old_string":"a","new_string":"b"}}' ;;
        peer-claim)
            printf '{"generated_at":"2026-09-20T00:00:00Z","claims":[{"path":"src/auth/login.ts","owners":[{"user":"alice","host":"dev01","pid":1001,"agent":"frontend-pro","status":"confirmed","expires_at":"2099-01-01T00:00:00Z"}]}]}\n' > "$sb/coord/proj/coord/active-claims.json"
            GS_ENV="YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
            GS_PAYLOAD='{"session_id":"gs1","hook_event_name":"PreToolUse","agent_type":"backend-pro","tool_name":"Edit","tool_input":{"file_path":"src/auth/login.ts","old_string":"a","new_string":"b"}}' ;;
        plan-quality-gate)
            printf '{"plan_id":"plan-abc","reason":"low"}\n' > "$sb/work/current/.plan-blocked"
            GS_PAYLOAD="$GATE_PAYLOAD" ;;
        secret-scan)
            GS_PAYLOAD="$(jq -nc --arg c "aws_access_key_id: $(printf '%s%s' AKIA 0123456789ABCDEF)" '{session_id:"gs1",hook_event_name:"PreToolUse",tool_name:"Write",tool_input:{file_path:"config.yaml",content:$c}}')" ;;
        supervisor-ack-gate)
            printf '{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture critical","recommended_action":"halt"}\n' > "$sb/work/current/supervisor-findings.ndjson"
            GS_PAYLOAD="$GATE_PAYLOAD" ;;
        supervisor-gate)
            printf '{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture critical","recommended_action":"halt"}\n' > "$sb/work/current/supervisor-findings.ndjson"
            GS_PAYLOAD='{"session_id":"gs1","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"api/x.go","old_string":"a","new_string":"b"}}' ;;
        *) GS_PAYLOAD="$GATE_PAYLOAD" ;;
    esac
}

# gate_libs <hook>: the libs the hook loads itself.
gate_libs() {
    case "$1" in
        path-allowlist) echo "hook-input.sh hook-output.sh paths.sh path-safety.sh" ;;
        *) echo "hook-input.sh hook-output.sh paths.sh" ;;
    esac
}

# grun <shell> <hookdir> <hook> <sandbox> <payload> <env-words> [redir-mode]
# redir-mode: "" (capture), noerr (2>&-), pipe (stderr into a reader that exits)
grun() {
    local sh="$1" hd="$2" hook="$3" sb="$4" payload="$5" words="$6" mode="${7:-}"
    rc=0
    # shellcheck disable=SC2086  # words are KEY=VAL without spaces
    case "$mode" in
        noerr) printf '%s' "$payload" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" $words \
                    "$sh" "$hd/$hook.sh" >/dev/null 2>&- || rc=$? ;;
        pipe)  printf '%s' "$payload" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" $words \
                    "$sh" "$hd/$hook.sh" 2>&1 >/dev/null | true
               rc="${PIPESTATUS[1]}" ;;
        *)     printf '%s' "$payload" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" $words \
                    "$sh" "$hd/$hook.sh" >"$sb/out" 2>"$sb/err" || rc=$?
               out="$(cat "$sb/out")"; err="$(cat "$sb/err")" ;;
    esac
}

gate_prologue_suite() {
    local SH="$1" L="$2" h sb lib kind hd sig
    echo "== $L: K-107 gate prologue (all blocking hooks) =="

    for h in $BLOCKING; do
        [ -f "$HOOKS/$h.sh" ] || continue

        # baseline: the scenario really blocks, and a plain Bash call passes.
        sb="$(new_sandbox "g-$L-$h-base")"; gate_scenario "$h" "$sb"
        grun "$SH" "$HOOKS" "$h" "$sb" "$GS_PAYLOAD" "$GS_ENV"
        if [ "$rc" = 2 ] && [ -n "$err" ]; then ok "$L: $h blocking scenario -> 2 with stderr (control)"; else bad "$L: $h scenario did not block: rc=$rc err=[$err]"; continue; fi
        sb="$(new_sandbox "g-$L-$h-pass")"
        grun "$SH" "$HOOKS" "$h" "$sb" "$GS_PASS_PAYLOAD" "YAKOS_PROJECT_NAME=proj"
        if [ "$rc" = 0 ]; then ok "$L: $h pass scenario -> 0 (control)"; else bad "$L: $h pass control rc=$rc err=[$err]"; fi

        # closed stderr / broken stderr pipe on a real block
        sb="$(new_sandbox "g-$L-$h-noerr")"; gate_scenario "$h" "$sb"
        grun "$SH" "$HOOKS" "$h" "$sb" "$GS_PAYLOAD" "$GS_ENV" noerr
        if [ "$rc" = 2 ]; then ok "$L: $h block + stderr closed -> 2"; else bad "$L: $h block + stderr closed rc=$rc (1 = fail-open)"; fi
        sb="$(new_sandbox "g-$L-$h-pipe")"; gate_scenario "$h" "$sb"
        grun "$SH" "$HOOKS" "$h" "$sb" "$GS_PAYLOAD" "$GS_ENV" pipe
        if [ "$rc" = 2 ]; then ok "$L: $h block + broken stderr pipe -> 2"; else bad "$L: $h block + broken pipe rc=$rc (141 = SIGPIPE)"; fi

        # signals mid-run (a jq shim that sleeps once gives a window)
        for sig in TERM HUP; do
            if [ "$sig" = HUP ] && [ "$(bash -c 'kill -HUP $$; sleep 0.3; echo alive' 2>/dev/null)" = alive ]; then
                echo "  SKIP $L: $h SIGHUP (SIGHUP is ignored in this environment)"; continue
            fi
            sb="$(new_sandbox "g-$L-$h-sig-$sig")"; gate_scenario "$h" "$sb"
            mkdir -p "$sb/slow"
            printf '#!/bin/sh\nif [ ! -e "%s" ]; then : > "%s"; sleep 3; fi\nexec "%s" "$@"\n' "$sb/slow/once" "$sb/slow/once" "$REAL_JQ" > "$sb/slow/jq"
            chmod +x "$sb/slow/jq"
            printf '%s' "$GS_PASS_PAYLOAD" > "$sb/payload"
            # shellcheck disable=SC2086
            env PATH="$sb/slow:$PATH" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" $GS_ENV \
                "$SH" "$HOOKS/$h.sh" < "$sb/payload" >"$sb/out" 2>"$sb/err" &
            local pid=$!
            sleep 0.7
            kill -"$sig" "$pid" 2>/dev/null || true
            rc=0; wait "$pid" || rc=$?
            if [ "$rc" = 2 ]; then ok "$L: $h SIG$sig mid-run -> 2"; else bad "$L: $h SIG$sig mid-run rc=$rc (143=TERM, 129=HUP)"; fi
        done
    done

    # Library failures: one mutated hooks tree per (lib, kind), run by every
    # blocking hook that loads that lib, on the PASS payload (rc 2 can then only
    # come from the load failure, not from a legitimate block).
    for lib in hook-input.sh hook-output.sh paths.sh path-safety.sh; do
        for kind in truncated directory syntax; do
            hd="$TMP/gp-$L-$lib-$kind"
            copy_hooks "$hd"
            case "$kind" in
                truncated) grep -v '^[A-Z_]*LOADED=1$' "$hd/lib/$lib" > "$hd/lib/$lib.new" && mv "$hd/lib/$lib.new" "$hd/lib/$lib" ;;
                directory) rm -f "$hd/lib/$lib"; mkdir "$hd/lib/$lib" ;;
                syntax)    corrupt "$hd/lib/$lib" ;;
            esac
            for h in $BLOCKING; do
                [ -f "$HOOKS/$h.sh" ] || continue
                case " $(gate_libs "$h") " in *" $lib "*) ;; *) continue ;; esac
                sb="$(new_sandbox "gp-$L-$h-$lib-$kind")"
                grun "$SH" "$hd" "$h" "$sb" "$GS_PASS_PAYLOAD" "YAKOS_PROJECT_NAME=proj"
                if [ "$rc" = 2 ]; then ok "$L: $h + lib/$lib $kind -> 2"; else bad "$L: $h + lib/$lib $kind rc=$rc err=[$err] (0/1 = fail-open)"; fi
                # ...and with stderr closed, so the failure message cannot be delivered
                grun "$SH" "$hd" "$h" "$sb" "$GS_PASS_PAYLOAD" "YAKOS_PROJECT_NAME=proj" noerr
                if [ "$rc" = 2 ]; then ok "$L: $h + lib/$lib $kind + stderr closed -> 2"; else bad "$L: $h + lib/$lib $kind + stderr closed rc=$rc"; fi
            done
        done
    done
}

# ---- K-107: hung jq -----------------------------------------------------------
# mk_hungjq <dir> <timeout-mode>: a PATH whose jq sleeps for 30s. timeout-mode:
#   none  neither timeout nor gtimeout on PATH (macOS shape: background+poll path)
#   real  whatever the system has (GNU timeout on Linux CI)
#   stub  a python3 stand-in that follows GNU timeout's contract (rc 124)
mk_hungjq() {
    mkdir -p "$1"
    local d b n
    for d in /usr/bin /bin /usr/local/bin /opt/homebrew/bin; do
        [ -d "$d" ] || continue
        for b in "$d"/*; do
            [ -x "$b" ] || continue
            n="$(basename -- "$b")"
            case "$n" in jq) continue ;; timeout|gtimeout) [ "$2" = real ] || continue ;; esac
            [ -e "$1/$n" ] || ln -sf "$b" "$1/$n" 2>/dev/null || true
        done
    done
    printf '#!/bin/sh\nexec sleep 30\n' > "$1/jq"; chmod +x "$1/jq"
    if [ "$2" = stub ]; then
        printf '#!%s\nimport subprocess, sys\np = subprocess.Popen(sys.argv[2:])\ntry:\n    sys.exit(p.wait(timeout=float(sys.argv[1])))\nexcept subprocess.TimeoutExpired:\n    p.terminate(); p.wait(); sys.exit(124)\n' "$(command -v python3)" > "$1/timeout"
        chmod +x "$1/timeout"
    fi
}
mk_hungjq "$TMP/hj-none" none
mk_hungjq "$TMP/hj-real" real
if command -v python3 >/dev/null 2>&1; then mk_hungjq "$TMP/hj-stub" stub; fi

hung_jq_suite() {
    local SH="$1" L="$2" mode h sb t0 t1 dur path
    for mode in none real stub; do
        [ -d "$TMP/hj-$mode" ] || { echo "  SKIP $L: hung jq ($mode): python3 unavailable"; continue; }
        if [ "$mode" = real ] && ! command -v timeout >/dev/null 2>&1 && ! command -v gtimeout >/dev/null 2>&1; then
            echo "  SKIP $L: hung jq (real timeout): no timeout/gtimeout on this machine"; continue
        fi
        path="$TMP/hj-$mode"
        for h in $BLOCKING; do
            [ -f "$HOOKS/$h.sh" ] || continue
            sb="$(new_sandbox "hj-$L-$mode-$h")"
            t0="$(date +%s)"
            rc=0
            printf '%s' "$GS_PASS_PAYLOAD" | env PATH="$path" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
                YAKOS_COORD_ROOT="$sb/coord" YAKOS_HOOK_JQ_TIMEOUT=1 "$SH" "$HOOKS/$h.sh" >"$sb/out" 2>"$sb/err" || rc=$?
            t1="$(date +%s)"; dur=$(( t1 - t0 )); err="$(cat "$sb/err")"
            if [ "$rc" = 2 ] && [ "$dur" -lt 12 ] && printf '%s' "$err" | grep -q 'hung jq' && [ ! -s "$sb/out" ]; then
                ok "$L: blocking $h + hung jq ($mode) -> 2 in ${dur}s with reason"
            else
                bad "$L: blocking $h + hung jq ($mode) rc=$rc dur=${dur}s err=[$err] (want 2, <12s, 'hung jq')"
            fi
            if grep -q 'hung jq' "$sb/work/current/logs/$h.ndjson" 2>/dev/null; then ok "$L: $h hung-jq BLOCK record written ($mode)"; else bad "$L: $h hung-jq left no log record ($mode)"; fi
        done
        for h in path-log cycle-counter mailbox-mirror; do
            sb="$(new_sandbox "hj-$L-$mode-nb-$h")"
            t0="$(date +%s)"
            rc=0
            printf '%s' "$GS_PASS_PAYLOAD" | env PATH="$path" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
                YAKOS_HOOK_JQ_TIMEOUT=1 "$SH" "$HOOKS/$h.sh" >"$sb/out" 2>"$sb/err" || rc=$?
            t1="$(date +%s)"; dur=$(( t1 - t0 )); err="$(cat "$sb/err")"
            if [ "$rc" = 0 ] && [ "$dur" -lt 12 ] && printf '%s' "$err" | grep -q 'WARN' && [ ! -s "$sb/out" ]; then
                ok "$L: non-blocking $h + hung jq ($mode) -> 0 with WARN in ${dur}s"
            else
                bad "$L: non-blocking $h + hung jq ($mode) rc=$rc dur=${dur}s err=[$err]"
            fi
        done
    done
    # Windows shape: a non-GNU `timeout` (timeout.exe: `/t` syntax, exit 1, no
    # --version) sits on PATH ahead of anything else. It must be ignored, for a
    # healthy jq (hook passes) and a hung jq (still bounded).
    local bd="$TMP/bogus-timeout-$L"
    mkdir -p "$bd"
    printf '#!/bin/sh\necho "ERROR: Invalid syntax." >&2\nexit 1\n' > "$bd/timeout"; chmod +x "$bd/timeout"
    sb="$(new_sandbox "hj-$L-bogus-ok")"
    rc=0
    printf '%s' "$GS_PASS_PAYLOAD" | env PATH="$bd:$PATH" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" \
        "$SH" "$HOOKS/secret-scan.sh" >/dev/null 2>"$sb/err" || rc=$?
    if [ "$rc" = 0 ]; then ok "$L: non-GNU timeout on PATH + healthy jq -> hook passes (0)"; else bad "$L: non-GNU timeout broke a healthy hook rc=$rc err=[$(cat "$sb/err")]"; fi
    cp "$TMP/hj-none/jq" "$bd/jq"
    sb="$(new_sandbox "hj-$L-bogus-hung")"
    t0="$(date +%s)"; rc=0
    printf '%s' "$GS_PASS_PAYLOAD" | env PATH="$bd:$TMP/hj-none" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_HOOK_JQ_TIMEOUT=1 \
        "$SH" "$HOOKS/secret-scan.sh" >/dev/null 2>"$sb/err" || rc=$?
    dur=$(( $(date +%s) - t0 ))
    if [ "$rc" = 2 ] && [ "$dur" -lt 12 ]; then ok "$L: non-GNU timeout on PATH + hung jq -> 2 in ${dur}s"; else bad "$L: non-GNU timeout + hung jq rc=$rc dur=${dur}s"; fi

    # K-107 review round: YAKOS_HOOK_JQ_TIMEOUT parsing. 08/09 are decimal (not
    # octal), huge values do not overflow, garbage falls back to 5 with a WARN,
    # and none of them makes a healthy blocking hook block.
    local tv
    for tv in 08 09 0 1 30 99999999999999999999 abc 1.5 -3; do
        sb="$(new_sandbox "hj-$L-tv-$tv")"
        grun "$SH" "$HOOKS" secret-scan "$sb" "$GS_PASS_PAYLOAD" "YAKOS_HOOK_JQ_TIMEOUT=$tv"
        if [ "$rc" = 0 ]; then ok "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv + healthy jq -> 0"; else bad "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv rc=$rc err=[$err]"; fi
        case "$tv" in
            abc|1.5|-3)
                if printf '%s' "$err" | grep -q 'not a whole number'; then ok "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv warns"; else bad "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv gave no WARN: [$err]"; fi ;;
            *)
                if [ -n "$err" ]; then bad "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv must be accepted silently, stderr: [$err]"; else ok "$L: YAKOS_HOOK_JQ_TIMEOUT=$tv accepted silently"; fi ;;
        esac
    done
    # K-112 (g): the clamp ceiling is 25, strictly below the 30 s timeout that
    # `yakos refresh` writes for every hook, so the jq guard always fires first.
    local hook_to want_lim got_lim
    hook_to="$(sed -n 's/^[[:space:]]*Timeout:[[:space:]]*\([0-9][0-9]*\),$/\1/p' "$REPO_ROOT/cli-go/internal/hooksinstall/hooksinstall.go" | head -1)"
    for pair in "30:25" "26:25" "25:25" "24:24" "99999999999999999999:25" "1000:25" "0:1" "08:8" "5:5" ":5"; do
        tv="${pair%%:*}"; want_lim="${pair##*:}"
        got_lim="$("$SH" -c ". '$HOOKS/lib/hook-input.sh' >/dev/null 2>&1; YAKOS_HOOK_JQ_TIMEOUT='$tv' _hi_jq_limit_parse; printf '%s' \"\$_HI_JQ_LIMIT\"" 2>/dev/null)"
        if [ "$got_lim" = "$want_lim" ]; then ok "$L: YAKOS_HOOK_JQ_TIMEOUT='$tv' clamps to $want_lim"; else bad "$L: YAKOS_HOOK_JQ_TIMEOUT='$tv' gave limit '$got_lim' want $want_lim"; fi
    done
    if [ -n "$hook_to" ] && [ 25 -lt "$hook_to" ]; then ok "$L: jq ceiling 25 < generated hook timeout $hook_to"; else bad "$L: generated hook timeout '$hook_to' not above the jq ceiling"; fi
    # The no-timeout watchdog path is silent: no job-control notice on stderr.
    sb="$(new_sandbox "hj-$L-quiet")"
    rc=0
    printf '%s' "$GS_PASS_PAYLOAD" | env PATH="$TMP/hj-none" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_HOOK_JQ_TIMEOUT=1 \
        "$SH" "$HOOKS/secret-scan.sh" >/dev/null 2>"$sb/err" || rc=$?
    if ! grep -qiE 'terminated|killed' "$sb/err"; then ok "$L: hung-jq watchdog leaves no job-control noise on stderr"; else bad "$L: watchdog noise: [$(cat "$sb/err")]"; fi

    # ho_log's fallback record is valid JSON whatever the reason contains.
    mkdir -p "$TMP/jqfail"; printf '#!/bin/sh\nexit 5\n' > "$TMP/jqfail/jq"; chmod +x "$TMP/jqfail/jq"
    sb="$(new_sandbox "hj-$L-hologjson")"
    env PATH="$TMP/jqfail:$PATH" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$SH" -c \
        ". '$HOOKS/lib/hook-input.sh'; . '$HOOKS/lib/hook-output.sh'; ho_log t WARN pass \"line1
line2\ttab \\\\ back \\\"quote\" '{}'" >/dev/null 2>&1 || true
    if [ -s "$sb/work/current/logs/t.ndjson" ] && "$REAL_JQ" -e . "$sb/work/current/logs/t.ndjson" >/dev/null 2>&1; then
        ok "$L: ho_log fallback record is valid JSON for a reason with newline/tab/backslash/quote"
    else
        bad "$L: ho_log fallback record invalid or missing: [$(cat "$sb/work/current/logs/t.ndjson" 2>/dev/null)]"
    fi

    # A healthy jq is unaffected, and a nonsense timeout value falls back to the default.
    sb="$(new_sandbox "hj-$L-healthy")"
    grun "$SH" "$HOOKS" secret-scan "$sb" "$GS_PASS_PAYLOAD" "YAKOS_HOOK_JQ_TIMEOUT=abc"
    if [ "$rc" = 0 ]; then ok "$L: healthy jq + YAKOS_HOOK_JQ_TIMEOUT=abc -> 0 (default limit)"; else bad "$L: healthy jq with bad timeout value rc=$rc err=[$err]"; fi
}

run_suite() {
    local SH="$1" L="$2" fj h sb
    echo "== $L =="

    # ---- 1. lying jq -----------------------------------------------------------
    for fj in garbage array object event proxy; do
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

    # Blocking hooks with a lying jq AND stderr closed: the fail-closed branch's
    # own echos fail, and under `set -e` that used to exit 1 (non-blocking).
    for h in $BLOCKING; do
        [ -f "$HOOKS/$h.sh" ] || continue
        for fj in garbage proxy; do
            sb="$(new_sandbox "j-$L-$h-$fj-noerr")"
            rc=0
            printf '%s' "$PAYLOAD" | env PATH="$TMP/fj-$fj" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" \
                CLAUDE_PROJECT_DIR="$sb/proj" YAKOS_COORD_ROOT="$sb/coord" \
                "$SH" "$HOOKS/$h.sh" >/dev/null 2>&- || rc=$?
            if [ "$rc" = 2 ]; then ok "$L: blocking $h + jq($fj) + stderr closed -> exit 2"; else bad "$L: blocking $h + jq($fj) + stderr closed rc=$rc (1 = fail-open)"; fi
        done
    done
    # Non-blocking hooks must stay exit 0 (not 1) with stderr closed too.
    for h in path-log cycle-counter; do
        sb="$(new_sandbox "j-$L-$h-noerr")"
        rc=0
        printf '%s' "$PAYLOAD" | env PATH="$TMP/fj-garbage" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" \
            CLAUDE_PROJECT_DIR="$sb/proj" "$SH" "$HOOKS/$h.sh" >/dev/null 2>&- || rc=$?
        if [ "$rc" = 0 ]; then ok "$L: non-blocking $h + lying jq + stderr closed -> exit 0"; else bad "$L: non-blocking $h + stderr closed rc=$rc"; fi
    done

    # The Flows engine invokes output-injection-scan.sh with a synthetic payload
    # that has no hook_event_name (tool_name WorkflowNodeOutput) and
    # HOOK_FAIL_CLOSED=1. hi_init must accept it with a real jq, and must still
    # fail closed on it with a lying jq.
    local WF='{"tool_name":"WorkflowNodeOutput","tool_response":"perfectly benign upstream output","agent_type":"flows:a"}'
    sb="$(new_sandbox "j-$L-workflow")"
    HOOK_FAIL_CLOSED=1 run "$SH" "$HOOKS" output-injection-scan.sh "$sb" "$WF"
    if [ "$rc" = 0 ] && ! printf '%s' "$err" | grep -q 'BLOCKED'; then ok "$L: workflow payload without hook_event_name + real jq -> accepted (rc 0)"; else bad "$L: workflow payload rc=$rc err=[$err]"; fi
    for fj in garbage array object; do
        sb="$(new_sandbox "j-$L-workflow-$fj")"
        HOOK_FAIL_CLOSED=1 run "$SH" "$HOOKS" output-injection-scan.sh "$sb" "$WF" "$TMP/fj-$fj"
        if [ "$rc" = 2 ] && printf '%s' "$err" | grep -q 'BLOCKED'; then ok "$L: workflow payload + jq($fj) + HOOK_FAIL_CLOSED=1 -> exit 2"; else bad "$L: workflow payload + jq($fj) rc=$rc err=[$err]"; fi
    done

    # context-threshold must source compat.sh: with a transcript present its
    # probe has to produce a percentage, not probe_unavailable (K-101).
    local ct_sb ct_proj ct_enc ct_sid=ct-sess ct_log
    ct_sb="$(new_sandbox "ct-$L")"
    ct_proj="$ct_sb/proj"
    ct_enc="$(bash -c ". '$HOOKS/lib/compat.sh'; ct_encode_project_path '$ct_proj'")"
    mkdir -p "$ct_sb/home/.claude/projects/$ct_enc"
    # Real-shaped transcript named <session_id>.jsonl (K-112), exactly 700000 bytes.
    awk -v line="$(cat "$REPO_ROOT/tests/fixtures/hooks/claude-transcript-line.jsonl")" -v n=700000 \
        'BEGIN { while (t < n) { l = line "\n"; if (t + length(l) > n) l = substr(l, 1, n - t); printf "%s", l; t += length(l) } }' \
        > "$ct_sb/home/.claude/projects/$ct_enc/$ct_sid.jsonl"
    run "$SH" "$HOOKS" context-threshold.sh "$ct_sb" "{\"session_id\":\"$ct_sid\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"p\"}"
    ct_log="$ct_sb/work/current/logs/context-threshold.ndjson"
    if [ "$rc" = 0 ] && grep -q '"pct": *87' "$ct_log" 2>/dev/null && ! grep -q probe_unavailable "$ct_log" 2>/dev/null \
        && printf '%s' "$err" | grep -q 'NOTE: context at 87%'; then
        ok "$L: context-threshold probe runs (pct=87, NOTE emitted, no probe_unavailable)"
    else
        bad "$L: context-threshold probe rc=$rc err=[$err] log=[$(cat "$ct_log" 2>/dev/null)]"
    fi
    # K-110: the payload's transcript_path is used as-is, even when neither the
    # session id nor the project path would derive it.
    ct_sb="$(new_sandbox "ct-tp-$L")"
    mkdir -p "$ct_sb/elsewhere"
    awk -v line="$(cat "$REPO_ROOT/tests/fixtures/hooks/claude-transcript-line.jsonl")" -v n=700000 \
        'BEGIN { while (t < n) { l = line "\n"; if (t + length(l) > n) l = substr(l, 1, n - t); printf "%s", l; t += length(l) } }' \
        > "$ct_sb/elsewhere/renamed-transcript.jsonl"
    run "$SH" "$HOOKS" context-threshold.sh "$ct_sb" "{\"session_id\":\"no-such-session\",\"transcript_path\":\"$ct_sb/elsewhere/renamed-transcript.jsonl\",\"hook_event_name\":\"UserPromptSubmit\",\"prompt\":\"p\"}"
    ct_log="$ct_sb/work/current/logs/context-threshold.ndjson"
    if [ "$rc" = 0 ] && grep -q '"pct": *87' "$ct_log" 2>/dev/null && ! grep -q probe_unavailable "$ct_log" 2>/dev/null; then
        ok "$L: context-threshold uses payload transcript_path (pct=87)"
    else
        bad "$L: context-threshold transcript_path rc=$rc err=[$err] log=[$(cat "$ct_log" 2>/dev/null)]"
    fi
    # Broken compat.sh: warn, exit 0, no stdout.
    local ct_hd="$TMP/ct-hd-$L"
    copy_hooks "$ct_hd"; corrupt "$ct_hd/lib/compat.sh"
    ct_sb="$(new_sandbox "ct-bad-$L")"
    run "$SH" "$ct_hd" context-threshold.sh "$ct_sb" "$PAYLOAD"
    if [ "$rc" = 0 ] && [ -z "$out" ] && printf '%s' "$err" | grep -q 'cannot load lib/compat.sh'; then
        ok "$L: context-threshold + syntax error in compat.sh -> exit 0 with WARN"
    else
        bad "$L: context-threshold broken compat rc=$rc out=[$out] err=[$err]"
    fi

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
        # Under nohup (or any parent that sets SIGHUP to ignored) the signal
        # cannot be trapped by the hook either; skip rather than report a false
        # failure the hook cannot influence.
        if [ "$sig" = HUP ] && [ "$(bash -c 'kill -HUP $$; sleep 0.3; echo alive' 2>/dev/null)" = alive ]; then
            echo "  SKIP $L: gate SIGHUP (SIGHUP is ignored in this environment)"
            continue
        fi
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

    # ---- 4. K-107: the same guarantees for every blocking hook ----------------
    gate_prologue_suite "$SH" "$L"

    # ---- 5. K-107: a hung jq is bounded -----------------------------------------
    hung_jq_suite "$SH" "$L"

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
    # Clean truncation: drop the final sentinel statement so `.` returns 0 and
    # every function is defined. Only the sentinel check catches it.
    for lib in paths.sh hook-output.sh hook-input.sh; do
        local td="$TMP/h-$L-trunc-$lib"
        copy_hooks "$td"
        grep -v '^[A-Z_]*LOADED=1$' "$td/lib/$lib" > "$td/lib/$lib.new" && mv "$td/lib/$lib.new" "$td/lib/$lib"
        sb="$(new_sandbox "s-$L-trunc-$lib")"
        run "$SH" "$td" plan-quality-gate.sh "$sb" "$GATE_PAYLOAD"
        if [ "$rc" = 2 ] && printf '%s' "$err" | grep -q 'cannot load helper library'; then ok "$L: gate + lib/$lib truncated before its sentinel -> 2"; else bad "$L: truncated $lib rc=$rc err=[$err]"; fi
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

    # K-110: invalid UTF-8 in .yakos.yml must not abort the awk reader. macOS awk
    # exits 2 in a UTF-8 locale and used to drop every key after the bad line.
    local iu_fix="$REPO_ROOT/tests/fixtures/hooks/yakos-invalid-utf8.yml"
    printf 'enabled=false\nowner=caf\351\njunk=\377\376\nmode=block\nthreshold=0.9\n' > "$TMP/iu-want-$L"
    if LC_ALL=en_US.UTF-8 "$SH" -c ". '$HOOKS/lib/hook-input.sh' >/dev/null 2>&1; hi_yaml_block_children '$iu_fix' plan_quality" > "$TMP/iu-got-$L" 2>/dev/null \
        && cmp -s "$TMP/iu-want-$L" "$TMP/iu-got-$L"; then
        ok "$L: hi_yaml_block_children reads every key past invalid UTF-8 (rc 0)"
    else
        bad "$L: hi_yaml_block_children with invalid UTF-8: got [$(od -c "$TMP/iu-got-$L" | head -3)]"
    fi
}

run_suite bash bash5
[ -x /bin/bash ] && [ "$(command -v bash)" != /bin/bash ] && run_suite /bin/bash bin-bash || true

echo
echo "hook-hardening: $pass passed, $fail failed"
if [ "$fail" -ne 0 ]; then printf '%b' "$fail_log"; exit 1; fi
