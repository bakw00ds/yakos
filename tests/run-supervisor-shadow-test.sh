#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-shadow-test.sh — K-111 P2b regression test for the shadow
# decision call in lib/hooks/supervisor-stream.sh and its Go twin.
#
# Contract under test (nothing here may block, alter or delay a tool call):
#   (n) provider none (default, or unknown, or jev without a key, or the kill
#       switch): the hook never starts a decision call.
#   (m) provider mock: both sides start `yakos decide supervisor-prefilter
#       --shadow --local <pass|escalate> ...` with the SAME argv and the SAME
#       state JSON on stdin (a fake CLI records both).
#   (e) end to end with the real Go binary and the mock provider: both sides
#       leave the same decision-log record (mode shadow, local verdict); a
#       provider error is a silent fail-open (hook exit 0, error class logged).
#   (c) `yakos decide compare` reads those records back.
#
# The Go half needs bin/yakos (make build). Run under `bash` and `/bin/bash`
# (3.2). No network and no key: the mock provider only.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/supervisor-stream.sh"
FIXT="$REPO_ROOT/tests/fixtures/hooks"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
HAVE_GO=1; [ -x "$GO_BINARY" ] || HAVE_GO=0

unset YAKOS_ROOT YAKOS_LIB YAKOS_CLI YAKOS_SUPERVISOR_DISABLE YAKOS_DECISION_PROVIDER \
      YAKOS_DECISION_DISABLE YAKOS_DECISION_MOCK TYPESAFE_API_KEY
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-shadow-test-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

sides="bash"; [ "$HAVE_GO" = 1 ] && sides="bash go"

mksb() { # mksb <name> <yml-body>
    local sb="$TMP/$1"
    mkdir -p "$sb/.claude" "$sb/work/current/logs" "$sb/home"
    printf '%s' "$2" > "$sb/.yakos.yml"
    printf '%s' "$sb"
}
# fake CLI: records argv (one ARG: line each) and stdin, then exits 0.
mkfake() { # mkfake <record-prefix>
    local f="$TMP/fake-yakos-$RANDOM"
    printf '#!/bin/sh\nfor a in "$@"; do printf "ARG:%%s\\n" "$a" >> "%s.argv"; done\ncat > "%s.stdin"\n' "$1" "$1" > "$f"
    chmod +x "$f"; printf '%s' "$f"
}
run_payload() { # run_payload <side> <sandbox> <json> [env assignments...]
    local side="$1" sb="$2" json="$3"; shift 3
    if [ "$side" = "bash" ]; then
        printf '%s' "$json" | env "$@" HOME="$sb/home" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "${BASH:-bash}" "$HOOK" >"$sb/stdout" 2>"$sb/stderr"
    else
        printf '%s' "$json" | env "$@" HOME="$sb/home" YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "$GO_BINARY" hook run supervisor-stream >"$sb/stdout" 2>"$sb/stderr"
    fi
    echo $? > "$sb/rc"
}
bash_payload() { jq -nc --arg c "$1" '{session_id:"sess-1",hook_event_name:"PostToolUse",tool_name:"Bash",tool_input:{command:$c}}'; }
edit_payload() { jq -nc --arg f "$1" --arg b "$2" '{session_id:"sess-1",hook_event_name:"PostToolUse",tool_name:"Edit",tool_input:{file_path:$f,new_string:$b}}'; }
wait_for() { # wait_for <file> [min-lines]
    local i=0 n="${2:-1}"
    while [ "$i" -lt 100 ]; do
        [ -f "$1" ] && [ "$(wc -l < "$1" | tr -d ' ')" -ge "$n" ] && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}
wait_for_file() { # wait_for_file <file>: exists and non-empty
    local i=0
    while [ "$i" -lt 100 ]; do
        [ -s "$1" ] && return 0
        sleep 0.1; i=$((i + 1))
    done
    return 1
}
norm() { sed "s|$1|<SB>|g"; }

YML_MOCK=$'supervisor:\n  score_every_n_calls: 1000\ndecisions:\n  provider: mock\n'

# ---- (n) provider none never starts a call -----------------------------------
n=0
for spec in "default|supervisor:|" "none|decisions:|YAKOS_DECISION_PROVIDER=none" \
            "bogus|decisions:|YAKOS_DECISION_PROVIDER=bogus" \
            "jev-nokey|decisions:|YAKOS_DECISION_PROVIDER=jev" \
            "killswitch|decisions:|YAKOS_DECISION_DISABLE=1 YAKOS_DECISION_PROVIDER=mock"; do
    label="${spec%%|*}"; rest="${spec#*|}"; envs="${rest#*|}"
    for side in $sides; do
        n=$((n + 1))
        case "$label" in
            default) yml=$'supervisor:\n  score_every_n_calls: 1000\n' ;;
            *) yml=$'supervisor:\n  score_every_n_calls: 1000\ndecisions:\n  provider: none\n' ;;
        esac
        sb="$(mksb "n-$side-$label" "$yml")"
        fake="$(mkfake "$sb/rec")"
        # shellcheck disable=SC2086
        run_payload "$side" "$sb" "$(bash_payload 'rm -rf /')" "YAKOS_CLI=$fake" $envs
        sleep 0.3
        if [ ! -e "$sb/rec.argv" ] && [ "$(cat "$sb/rc")" = 0 ]; then
            ok "(n) $side provider-none case '$label' starts no decision call"
        else
            bad "(n) $side provider-none case '$label' started a call or failed (rc=$(cat "$sb/rc"))"
        fi
    done
done

# ---- (m) provider mock: same argv + same state on both sides -------------------
m=0
for spec in "rm|bash|rm -rf /" "ls|bash|ls -la" "edit-in-plan|edit|api.go" "edit-out-of-plan|edit|other.go"; do
    label="${spec%%|*}"; rest="${spec#*|}"; kind="${rest%%|*}"; arg="${rest#*|}"
    outs=""
    for side in $sides; do
        m=$((m + 1))
        sb="$(mksb "m-$side-$label" "$YML_MOCK")"
        printf 'fix the retry test in api.go\n' > "$sb/work/current/decisions.md"
        printf 'touch api.go only\n' > "$sb/work/current/plan.md"
        fake="$(mkfake "$sb/rec")"
        if [ "$kind" = bash ]; then json="$(bash_payload "$arg")"; else json="$(edit_payload "$sb/$arg" 'x := 1')"; fi
        run_payload "$side" "$sb" "$json" "YAKOS_CLI=$fake"
        [ "$(cat "$sb/rc")" = 0 ] && ok "(m) $side $label hook exit 0" || bad "(m) $side $label hook rc=$(cat "$sb/rc")"
        [ ! -s "$sb/stdout" ] && ok "(m) $side $label hook stdout untouched" || bad "(m) $side $label hook wrote stdout"
        if wait_for "$sb/rec.argv" 5 && wait_for_file "$sb/rec.stdin"; then
            ok "(m) $side $label started the decision call"
        else
            bad "(m) $side $label never started the decision call"
        fi
        sleep 0.1
        outs="$outs$( { norm "$sb" < "$sb/rec.argv"; norm "$sb" < "$sb/rec.stdin" | jq -cS .; } 2>&1)"$'\n=====\n'
    done
    if [ "$HAVE_GO" = 1 ]; then
        a="$(printf '%s' "$outs" | awk 'BEGIN{RS="=====\n"} NR==1')"; b="$(printf '%s' "$outs" | awk 'BEGIN{RS="=====\n"} NR==2')"
        if [ "$a" = "$b" ] && [ -n "$a" ]; then ok "(m) $label argv+state identical bash/go"; else bad "(m) $label differs: bash=[$a] go=[$b]"; fi
    fi
    if [ "$label" = rm ]; then
        printf '%s' "$outs" | grep -q -- '--local' && printf '%s' "$outs" | grep -q 'ARG:escalate' && ok "(m) rm escalates locally" || bad "(m) rm not recorded as escalate"
        printf '%s' "$outs" | grep -q 'ARG:risk-regex' && ok "(m) trigger kind passed without the pattern" || bad "(m) trigger kind missing"
    fi
    if [ "$label" = ls ]; then
        printf '%s' "$outs" | grep -q 'ARG:pass' && ok "(m) ls passes locally" || bad "(m) ls not recorded as pass"
    fi
    if [ "$label" = edit-in-plan ]; then
        printf '%s' "$outs" | grep -q '"plan_mentions_path":true' && ok "(m) plan_mentions_path true" || bad "(m) plan_mentions_path not true"
    fi
    if [ "$label" = edit-out-of-plan ]; then
        printf '%s' "$outs" | grep -q '"plan_mentions_path":false' && ok "(m) plan_mentions_path false" || bad "(m) plan_mentions_path not false"
    fi
done

# ---- (e) end to end: real binary, mock provider ---------------------------------
if [ "$HAVE_GO" = 1 ]; then
    FX_OK="$TMP/mock-ok.json"
    FX_ERR="$TMP/mock-err.json"
    cp "$REPO_ROOT/lib/decisions/examples/supervisor-prefilter.mock.json" "$FX_OK"
    printf '{"error":"timeout"}' > "$FX_ERR"
    for fx in ok err; do
        recs=""
        for side in $sides; do
            sb="$(mksb "e-$side-$fx" "$YML_MOCK")"
            mock="$FX_OK"; [ "$fx" = err ] && mock="$FX_ERR"
            run_payload "$side" "$sb" "$(bash_payload 'git push --force origin main')" \
                "YAKOS_CLI=$GO_BINARY" "YAKOS_ROOT=$REPO_ROOT" "YAKOS_DECISION_MOCK=$mock"
            [ "$(cat "$sb/rc")" = 0 ] && ok "(e) $side $fx hook exit 0" || bad "(e) $side $fx hook rc=$(cat "$sb/rc")"
            log="$sb/home/.yakos-state/decision-log.ndjson"
            if wait_for "$log" 1; then
                ok "(e) $side $fx decision log written"
            else
                bad "(e) $side $fx no decision log record"
                continue
            fi
            recs="$recs$(tail -n 1 "$log" | jq -cS 'del(.ts, .id, .latency_ms)')"$'\n'
            rec="$(tail -n 1 "$log")"
            [ "$(printf '%s' "$rec" | jq -r '.mode')" = shadow ] && ok "(e) $side $fx mode shadow" || bad "(e) $side $fx mode not shadow"
            [ "$(printf '%s' "$rec" | jq -r '.local_verdict')" = escalate ] && ok "(e) $side $fx local verdict recorded" || bad "(e) $side $fx local verdict missing"
            if [ "$fx" = err ]; then
                [ "$(printf '%s' "$rec" | jq -r '.status')" = timeout ] && ok "(e) $side error class logged (fail-open)" || bad "(e) $side error class not logged"
            else
                [ "$(printf '%s' "$rec" | jq -r '.answers.risk_class.choice')" = dangerous ] && ok "(e) $side shadow verdict logged" || bad "(e) $side shadow verdict missing"
            fi
            # The hook's own log and buffer are exactly what a no-provider run leaves.
            grep -q 'pre-filter: ESCALATE' "$sb/work/current/logs/supervisor-stream.ndjson" && ok "(e) $side $fx escalation path unchanged" || bad "(e) $side $fx escalation log missing"
        done
        if [ "$(printf '%s' "$recs" | grep -c .)" = 2 ]; then
            a="$(printf '%s' "$recs" | sed -n 1p)"; b="$(printf '%s' "$recs" | sed -n 2p)"
            [ "$a" = "$b" ] && ok "(e) $fx decision-log record identical bash/go" || bad "(e) $fx record differs: bash=$a go=$b"
        fi
    done

    # ---- (c) compare reads the records back --------------------------------------
    sb="$TMP/e-go-ok"
    out="$(env HOME="$sb/home" YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" decide compare supervisor-prefilter --json 2>&1)"
    if printf '%s' "$out" | jq -e '.records == 1 and .answered == 1 and .both_escalate == 1 and .agreement == 1' >/dev/null 2>&1; then
        ok "(c) decide compare: 1 shadow record, escalate on both sides, agreement 1"
    else
        bad "(c) decide compare output unexpected: $out"
    fi
    sb="$TMP/e-go-err"
    out="$(env HOME="$sb/home" YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" decide compare supervisor-prefilter --json 2>&1)"
    if printf '%s' "$out" | jq -e '.records == 1 and .answered == 0 and .errors.timeout == 1' >/dev/null 2>&1; then
        ok "(c) decide compare counts the fail-open record"
    else
        bad "(c) decide compare error output unexpected: $out"
    fi
else
    echo "  SKIP (e)/(c): bin/yakos not built"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
