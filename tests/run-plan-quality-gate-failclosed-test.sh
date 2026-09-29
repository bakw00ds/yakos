#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-plan-quality-gate-failclosed-test.sh — K-81 regression tests for the
# plan-quality-gate / plan-quality-score split.
#
#   plan-quality-gate.sh   PreToolUse gate. Must FAIL CLOSED: any degraded
#                          input, unreadable marker dir, odd marker, or
#                          internal crash => exit 2 (never 0/1 on error).
#   plan-quality-score.sh  PostToolUse scorer. Must stay conservative:
#                          infra errors => WARN + exit 0.
#
# Every case runs under both `bash` (whatever is first on PATH) and
# /bin/bash (3.2 on macOS): a crash under 3.2 would be a silent fail-open.
#
# Usage: bash tests/run-plan-quality-gate-failclosed-test.sh
set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
unset YAKOS_ROOT YAKOS_LIB || true

TMP="$(mktemp -d -t yakos-pqgfc-XXXXXX)"
cleanup() { chmod -R u+rwx "$TMP" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

pass=0; fail=0; fail_log=""
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# PATH with every binary except jq.
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
HAVE_NOJQ=1
if PATH="$NOJQ" command -v jq >/dev/null 2>&1; then HAVE_NOJQ=0; fi

IS_ROOT=0; [ "$(id -u)" = "0" ] && IS_ROOT=1

pl_agent()   { jq -nc '{session_id:"s",hook_event_name:"PreToolUse",tool_name:"Agent",tool_input:{prompt:"x"}}'; }
pl_team()    { jq -nc '{session_id:"s",hook_event_name:"PreToolUse",tool_name:"TeamCreate",tool_input:{name:"t"}}'; }
pl_tool()    { jq -nc --arg t "$1" '{session_id:"s",hook_event_name:"PreToolUse",tool_name:$t,tool_input:{}}'; }

# new_sandbox <dirname> -> echoes sandbox root; layout <root>/work/current
new_sandbox() {
    local root="$TMP/$1"
    mkdir -p "$root/work/current/logs" "$root/home" "$root/proj"
    printf '%s' "$root"
}

# run_hook <shell> <hook> <sandbox> <payload> [KEY=VAL ...] -> sets rc, err
run_hook() {
    local sh="$1" hook="$2" sb="$3" payload="$4"; shift 4
    rc=0
    printf '%s' "$payload" \
        | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$@" \
            "$sh" "$HOOKS/$hook" >"$sb/out" 2>"$sb/err" || rc=$?
    err="$(cat "$sb/err")"
}

expect() { # <label> <want-rc>
    if [ "$rc" = "$2" ]; then ok "$SHLABEL: $1 (rc=$rc)"; else bad "$SHLABEL: $1 — want rc=$2 got rc=$rc; stderr: $err"; fi
}
expect_err() { # <label> <substring>
    if printf '%s' "$err" | grep -q -- "$2"; then ok "$SHLABEL: $1 (stderr mentions '$2')"; else bad "$SHLABEL: $1 — stderr lacks '$2': $err"; fi
}

marker_json() { printf '{"plan_id":"plan-abc123","reason":"aggregate 0.4 < threshold 0.75"}\n'; }

run_suite() {
    local SH="$1"; SHLABEL="$2"
    local sb

    echo "== gate: $SHLABEL =="

    # -- marker present / absent -------------------------------------------------
    sb="$(new_sandbox g1-$SHLABEL-marker)"; marker_json > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "marker present, Agent -> block" 2
    expect_err "block message names the plan id" "plan-abc123"
    expect_err "block message gives override hint" "yakos plan score override"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_team)"
    expect "marker present, TeamCreate -> block" 2

    sb="$(new_sandbox g2-$SHLABEL-nomarker)"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "marker absent -> pass" 0

    # -- emergency disable -------------------------------------------------------
    sb="$(new_sandbox g3-$SHLABEL-disable)"; marker_json > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)" YAKOS_PLAN_QUALITY_DISABLE=1
    expect "YAKOS_PLAN_QUALITY_DISABLE=1 with marker -> pass" 0

    # -- prefix must not gate ----------------------------------------------------
    sb="$(new_sandbox g4-$SHLABEL-prefix)"; marker_json > "$sb/work/current/.plan-blocked"
    for t in AgentX TeamCreateFoo agent Bash XAgent; do
        run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_tool "$t")"
        expect "tool '$t' with marker -> not gated" 0
    done
    # Stale old-layout PostToolUse registration (Edit) reaching the gate is a no-op.
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(jq -nc '{hook_event_name:"PostToolUse",tool_name:"Edit",tool_input:{file_path:"/x/work/current/plan.md"}}')"
    expect "stale PostToolUse Edit registration with marker -> no-op" 0

    # -- malformed input ---------------------------------------------------------
    sb="$(new_sandbox g5-$SHLABEL-malformed)"
    run_hook "$SH" plan-quality-gate.sh "$sb" '{not json'
    expect "malformed JSON -> fail closed" 2
    expect_err "malformed JSON stderr says BLOCKED" "BLOCKED"
    expect_err "malformed JSON handled by hi_init's fail-closed path (HOOK_FAIL_CLOSED=1)" "refuses to fail open"
    run_hook "$SH" plan-quality-gate.sh "$sb" ''
    expect "empty stdin -> fail closed" 2
    run_hook "$SH" plan-quality-gate.sh "$sb" '[1,2,3]'
    expect "JSON array -> fail closed" 2
    run_hook "$SH" plan-quality-gate.sh "$sb" '"Agent"'
    expect "bare JSON string -> fail closed" 2
    run_hook "$SH" plan-quality-gate.sh "$sb" '{"session_id":"s"}'
    expect "object without tool_name -> fail closed" 2
    run_hook "$SH" plan-quality-gate.sh "$sb" '{"tool_name":null}'
    expect "null tool_name -> fail closed" 2

    # -- jq missing --------------------------------------------------------------
    if [ "$HAVE_NOJQ" = "1" ]; then
        sb="$(new_sandbox g6-$SHLABEL-nojq)"
        local p; p="$(pl_agent)"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$p" PATH="$NOJQ"
        expect "jq missing, marker absent -> fail closed" 2
        expect_err "jq-missing stderr says BLOCKED" "BLOCKED"
        expect_err "jq-missing handled by hi_init's fail-closed path (HOOK_FAIL_CLOSED=1)" "refuses to fail open"
        marker_json > "$sb/work/current/.plan-blocked"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$p" PATH="$NOJQ"
        expect "jq missing, marker present -> block" 2
        run_hook "$SH" plan-quality-gate.sh "$sb" "$p" PATH="$NOJQ" YAKOS_PLAN_QUALITY_DISABLE=1
        expect "jq missing + YAKOS_PLAN_QUALITY_DISABLE=1 -> pass (emergency switch)" 0
    else
        echo "  SKIP jq-missing cases (cannot build jq-free PATH)"
    fi

    # -- odd markers -------------------------------------------------------------
    sb="$(new_sandbox g7-$SHLABEL-dirmarker)"; mkdir "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect ".plan-blocked is a directory -> block (not silently absent)" 2
    expect_err "directory marker message says remove by hand" "not a regular file"

    sb="$(new_sandbox g8-$SHLABEL-dangling)"; ln -s "$sb/nonexistent-target" "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect ".plan-blocked is a dangling symlink -> block" 2

    sb="$(new_sandbox g9-$SHLABEL-plaintext)"; printf 'plain text reason\nline2\n' > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "plain-text marker -> block" 2
    expect_err "plain-text reason surfaced" "plain text reason"

    sb="$(new_sandbox g10-$SHLABEL-emptymarker)"; : > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "empty marker file -> block" 2

    # -- paths with spaces -------------------------------------------------------
    sb="$(new_sandbox "g11 $SHLABEL with spaces")"; marker_json > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "marker path contains spaces, present -> block" 2
    rm -f "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "marker path contains spaces, absent -> pass" 0

    # -- unreadable / unsearchable dirs -----------------------------------------
    if [ "$IS_ROOT" = "0" ]; then
        sb="$(new_sandbox g12-$SHLABEL-unreadable)"; marker_json > "$sb/work/current/.plan-blocked"
        chmod 000 "$sb/work/current"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
        expect "work/current mode 000 (marker inside) -> fail closed" 2
        chmod 755 "$sb/work/current"

        sb="$(new_sandbox g13-$SHLABEL-noexec-parent)"
        chmod 000 "$sb/work"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
        expect "work/ mode 000 (cannot tell if marker exists) -> fail closed" 2
        chmod 755 "$sb/work"

        sb="$(new_sandbox g14-$SHLABEL-readonly-logs)"
        chmod 555 "$sb/work/current/logs"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
        expect "logs dir unwritable, marker absent -> still pass" 0
        marker_json > "$sb/work/current/.plan-blocked"
        run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
        expect "logs dir unwritable, marker present -> block (not exit 1)" 2
        chmod 755 "$sb/work/current/logs"
    else
        echo "  SKIP permission cases (running as root)"
    fi

    # work/current is a regular file (ENOTDIR) -> cannot look
    sb="$(new_sandbox g15b-$SHLABEL-currentisfile)"; rm -rf "$sb/work/current"; : > "$sb/work/current"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "work/current is a regular file -> fail closed" 2

    # work/current absent entirely but parent searchable -> no marker can exist
    sb="$(new_sandbox g15-$SHLABEL-nocurrent)"; rm -rf "$sb/work/current"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "work/current does not exist (parent searchable) -> pass" 0

    # -- per-project opt-out ------------------------------------------------------
    sb="$(new_sandbox g16-$SHLABEL-optout)"; marker_json > "$sb/work/current/.plan-blocked"
    printf 'yakos: 0.9\nplan_quality:\n  enabled: false\n' > "$sb/proj/.yakos.yml"
    run_hook "$SH" plan-quality-gate.sh "$sb" "$(pl_agent)"
    expect "plan_quality.enabled=false -> pass" 0
    if [ ! -e "$sb/work/current/.plan-blocked" ]; then ok "$SHLABEL: opt-out clears the marker"; else bad "$SHLABEL: opt-out left the marker"; fi

    # -- internal crash => 2 ------------------------------------------------------
    # Copy the hook + libs and break one dependency; a crash must never be exit 1.
    local cp="$TMP/crash-$SHLABEL"; rm -rf "$cp"; mkdir -p "$cp/lib"
    cp "$REPO_ROOT/lib/hooks/legacy/plan-quality-gate.sh" "$cp/plan-quality-gate.sh"
    cp "$HOOKS/lib/hook-input.sh" "$HOOKS/lib/hook-output.sh" "$HOOKS/lib/paths.sh" "$cp/lib/"
    sb="$(new_sandbox g17-$SHLABEL-crash)"
    rc=0; printf '%s' "$(pl_agent)" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$SH" "$cp/plan-quality-gate.sh" >/dev/null 2>"$sb/err" || rc=$?
    err="$(cat "$sb/err")"; expect "sanity: copied hook passes with no marker" 0
    rm -f "$cp/lib/hook-output.sh"
    rc=0; printf '%s' "$(pl_agent)" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$SH" "$cp/plan-quality-gate.sh" >/dev/null 2>"$sb/err" || rc=$?
    err="$(cat "$sb/err")"; expect "missing sourced lib (crash) -> exit 2, not 1" 2
    expect_err "crash message says failing closed" "failing closed"
    cp "$HOOKS/lib/hook-output.sh" "$cp/lib/"
    printf 'yakos_current_dir() { return 7; }\n' > "$cp/lib/paths.sh"
    rc=0; printf '%s' "$(pl_agent)" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$SH" "$cp/plan-quality-gate.sh" >/dev/null 2>"$sb/err" || rc=$?
    err="$(cat "$sb/err")"; expect "internal helper failure (exit 7) -> exit 2" 2

    # -- concurrent override + gate ----------------------------------------------
    sb="$(new_sandbox g18-$SHLABEL-concurrent)"
    marker_json > "$sb/proto"
    (
        i=0
        while [ "$i" -lt 150 ]; do
            cp "$sb/proto" "$sb/work/current/.plan-blocked.tmp" && mv "$sb/work/current/.plan-blocked.tmp" "$sb/work/current/.plan-blocked"
            rm -f "$sb/work/current/.plan-blocked"      # what `yakos plan score override` does
            i=$((i + 1))
        done
    ) &
    local bg=$! n=0 bad_rc=""
    local payload; payload="$(pl_agent)"
    while [ "$n" -lt 60 ]; do
        rc=0
        printf '%s' "$payload" | env HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb/proj" "$SH" "$HOOKS/plan-quality-gate.sh" >/dev/null 2>&1 || rc=$?
        case "$rc" in 0|2) ;; *) bad_rc="$bad_rc $rc" ;; esac
        n=$((n + 1))
    done
    wait "$bg" 2>/dev/null || true
    if [ -z "$bad_rc" ]; then ok "$SHLABEL: 60 gate runs racing marker create/remove only ever exit 0 or 2"; else bad "$SHLABEL: gate exited unexpected codes racing override:$bad_rc"; fi

    echo "== scorer: $SHLABEL =="

    # Scorer never gates: same marker + Agent payload -> exit 0.
    sb="$(new_sandbox s1-$SHLABEL-nogate)"; marker_json > "$sb/work/current/.plan-blocked"
    run_hook "$SH" plan-quality-score.sh "$sb" "$(pl_agent)"
    expect "scorer ignores PreToolUse Agent even with marker" 0

    # Scorer stays fail-open on degraded input (conservative posture unchanged).
    sb="$(new_sandbox s2-$SHLABEL-malformed)"
    run_hook "$SH" plan-quality-score.sh "$sb" '{not json'
    expect "scorer: malformed JSON -> WARN + pass" 0
    run_hook "$SH" plan-quality-score.sh "$sb" ''
    expect "scorer: empty stdin -> WARN + pass" 0
    if [ "$HAVE_NOJQ" = "1" ]; then
        local sp; sp="$(jq -nc --arg f "$sb/work/current/plan.md" '{hook_event_name:"PostToolUse",tool_name:"Write",tool_input:{file_path:$f,content:"x"}}')"
        run_hook "$SH" plan-quality-score.sh "$sb" "$sp" PATH="$NOJQ"
        expect "scorer: jq missing -> WARN + pass (never blocks a save)" 0
    fi

    # Scorer infra error (score script missing) -> WARN + exit 0, logged under its own name.
    sb="$(new_sandbox s3-$SHLABEL-infra)"
    printf 'plan body\n' > "$sb/work/current/plan.md"; touch -t 202001010000 "$sb/work/current/plan.md"
    local sp2; sp2="$(jq -nc --arg f "$sb/work/current/plan.md" '{hook_event_name:"PostToolUse",tool_name:"Write",tool_input:{file_path:$f,content:"x"}}')"
    run_hook "$SH" plan-quality-score.sh "$sb" "$sp2" YAKOS_ROOT="$sb/no-such-root"
    expect "scorer: score-plan.sh missing (infra error) -> pass" 0
    if grep -q '"WARN"' "$sb/work/current/logs/plan-quality-score.ndjson" 2>/dev/null; then ok "$SHLABEL: infra error logged WARN in plan-quality-score.ndjson"; else bad "$SHLABEL: no WARN record in plan-quality-score.ndjson"; fi
}

run_suite bash "bash"
if [ -x /bin/bash ] && [ "$(command -v bash)" != "/bin/bash" ]; then
    run_suite /bin/bash "bin-bash"
elif [ -x /bin/bash ]; then
    echo "(bash on PATH is /bin/bash; single pass)"
fi

echo
echo "Results: $pass passed, $fail failed"
if [ "$fail" -gt 0 ]; then printf '%b' "$fail_log"; exit 1; fi
