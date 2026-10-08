#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-ledger-compat-test.sh — K-136 (P0d part 2): the BASH twin keeps working on
# the Go dispatcher's ledger rows.
#
# The dispatch log is shared: the bash dispatcher and the Go dispatcher append to
# one file. Since K-136 the Go writer adds omitempty keys to dispatch_finished
# rows (provider, model_id, billing, cost_source, api_equivalent_usd, route_rule,
# route_reason, fallback_from, route_class, policy_sha, surface,
# native_session_id) and records 0 as usage.total_cost_usd for a subscription run
# (the harness's figure moves to api_equivalent_usd). Bash writers never emit
# those keys, so every bash/jq reader now meets rows with unknown keys and a zero
# usage cost. This suite runs each reader that can run offline against a MIXED
# log (tests/fixtures/ledger-compat/mixed-log.ndjson: legacy bash rows, legacy Go
# rows, started rows and K-136 ledger rows) and asserts exit 0, no jq or parse
# error on stderr, and the numbers that follow from "unknown keys are ignored and
# a subscription row adds 0 dollars".
#
#   (1) cost.sh                  yakos cost: every axis, --json, --since, table
#   (2) model-routing.sh         _mr_dispatch_case's telemetry read-back
#   (3) work-close.sh            yakos work close: tokens and dollars, rework
#   (4) hooks/plan-outcome-capture.sh   the same sums from the SessionEnd fallback
#   (5) session.sh               yakos session export slices rows verbatim
#   (6) recipes                  the jq one-liners of the dispatch workflow and of
#                                the agent-audit and session-summary skills
#   (7) supervisor-stream.sh     the budget gate on a mixed spend log; needs the
#                                Go binary (the hook reads the budget through
#                                `yakos budget check`), so it is skipped without
#                                one unless YAKOS_REQUIRE_GO_BINARY=1
#
# The fixture, in file order: legacy bash rows (a started row, finished rows with
# and without usage, a budget_violation, a codex row in the bash convention with
# the cached tokens inside input_tokens, an agy estimate); legacy Go rows (started,
# claude and codex finished); K-136 ledger rows (claude subscription with
# api_equivalent_usd, claude api, codex subscription, agy subscription, one row
# carrying every new key, one with only some route_* keys, a local-billing row, a
# codex row that reported no usage); then a bash finished row and a bash started
# row. Rows with an eval_run_id are the ones (2) looks up.
#
# Nothing here touches the operator's ~/.yakos-state: HOME is a temp directory.
# Run under both `bash` and `/bin/bash` (3.2 on macOS); on macOS put the stock
# bash first on PATH as well, because the CLI starts its subcommands as `bash`.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
FIXTURE="$REPO_ROOT/tests/fixtures/ledger-compat/mixed-log.ndjson"
BASH_BIN="${BASH:-bash}"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"

# Every reader is pointed at a temp HOME and at this checkout explicitly. An
# ambient YAKOS_ROOT / YAKOS_LIB would alias another checkout, and
# YAKOS_DISPATCH_LOG means a state DIRECTORY to the Go twin and to the supervisor
# hook but a log FILE to work-close and the plan-outcome hook.
unset YAKOS_ROOT YAKOS_LIB YAKOS_CLI YAKOS_IMPL YAKOS_HOOKS YAKOS_DISPATCH_LOG \
      YAKOS_WORK_DIR YAKOS_PLAN_QUALITY_LOG YAKOS_SUPERVISOR_DISABLE \
      YAKOS_PROJECT_NAME YAKOS_INPLACE_WORK CLAUDE_PROJECT_DIR \
      YAKOS_MR_MOCK_DISPATCH YAKOS_MR_MOCK_JUDGE YAKOS_MR_EVAL_LOG YAKOS_MR_CANDIDATES

pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }
# eq <label> <want> <got>
eq() {
    if [ "$2" = "$3" ]; then ok "$1"; else
        bad "$1"; printf '    want: %s\n    got:  %s\n' "$2" "$3"
    fi
}

command -v jq >/dev/null 2>&1 || { echo "FAIL: jq is required"; exit 1; }
[ -f "$FIXTURE" ] || { echo "FAIL: missing fixture $FIXTURE"; exit 1; }

TMP="$(mktemp -d -t yakos-ledger-compat-XXXXXX)"
cleanup() { find "$TMP" -delete 2>/dev/null || true; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

OUT="$TMP/out"; ERR="$TMP/err"; RC=0
# run_in <home> <command...>: run with HOME=<home>; stdout -> $OUT, stderr -> $ERR, status -> $RC.
run_in() {
    local h="$1"; shift
    env HOME="$h" "$@" >"$OUT" 2>"$ERR"; RC=$?
}
# mkhome <name> [log]: a fresh HOME whose state directory holds a copy of the log.
mkhome() {
    local h="$TMP/$1"
    mkdir -p "$h/.yakos-state"
    chmod 700 "$h/.yakos-state"
    cp "${2:-$FIXTURE}" "$h/.yakos-state/dispatch-log.ndjson"
    printf '%s' "$h"
}
# jq_err <file>: success when the file shows a jq or JSON parse error.
jq_err() { grep -Eiq 'jq: error|parse error|cannot (index|iterate)|syntax error|invalid (json|numeric)' "$1"; }
yakos_cli() { printf '%s' "$REPO_ROOT/cli/yakos"; }

# ---------------------------------------------------------------------------
echo "fixture"

NEWKEYS='"provider","model_id","billing","cost_source","api_equivalent_usd","route_rule","route_reason","fallback_from","route_class","policy_sha","surface","native_session_id"'
if jq -c . "$FIXTURE" >/dev/null 2>&1; then ok "the fixture is valid NDJSON"; else bad "the fixture is not valid NDJSON"; fi
for k in provider model_id billing cost_source api_equivalent_usd route_rule route_reason fallback_from route_class policy_sha surface native_session_id; do
    jq -s -e --arg k "$k" 'any(.[]; has($k))' "$FIXTURE" >/dev/null 2>&1 && ok "fixture has a row with $k" || bad "fixture has no row with $k"
done
jq -s -e "any(.[]; . as \$r | [$NEWKEYS] | all(. as \$k | \$r | has(\$k)))" "$FIXTURE" >/dev/null 2>&1 \
    && ok "fixture has a row carrying every new key" || bad "no fixture row carries every new key"
jq -s -e "[.[] | select(has(\"billing\") | not) | keys[] | select(IN(\"provider\",\"model_id\",\"cost_source\",\"api_equivalent_usd\",\"route_rule\",\"route_reason\",\"route_class\",\"policy_sha\",\"surface\",\"native_session_id\"))] | length == 0" "$FIXTURE" >/dev/null 2>&1 \
    && ok "legacy rows (no billing) carry none of the new keys" || bad "a legacy fixture row carries a new key"
jq -s -e '[.[] | .billing // empty] | (index("subscription") != null and index("api") != null and index("local") != null)' "$FIXTURE" >/dev/null 2>&1 \
    && ok "fixture has subscription, api and local billing" || bad "fixture misses a billing value"
jq -s -e 'any(.[]; .billing == "subscription" and .usage.total_cost_usd == 0 and .api_equivalent_usd > 0)' "$FIXTURE" >/dev/null 2>&1 \
    && ok "fixture has a subscription row: usage cost 0, api_equivalent_usd set" || bad "no subscription row with a zero usage cost and an api equivalent"
jq -s -e 'any(.[]; .billing == "api" and .usage.total_cost_usd > 0)' "$FIXTURE" >/dev/null 2>&1 \
    && ok "fixture has an api row: usage cost is the spend" || bad "no api row with a usage cost"
eq "fixture row mix: 16 finished, 3 started, 1 budget_violation" "16 3 1" \
    "$(jq -s -r '[([.[] | select(.type == "dispatch_finished")] | length), ([.[] | select(.type == "dispatch_started")] | length), ([.[] | select(.type == "budget_violation")] | length)] | map(tostring) | join(" ")' "$FIXTURE")"

# ---------------------------------------------------------------------------
echo "(1) cost.sh: yakos cost on the mixed log"

cost_digest() {
    jq -r '"events=\(.events)", (.rows[] | "\(.key) n=\(.count) ok=\(.ok) fail=\(.fail) dur=\(.total_duration_s) in=\(.total_in_tokens) out=\(.total_out_tokens)")'
}
# check_cost <label> <home> <want> <cost args...>: --json digest, exit 0 and a silent stderr.
check_cost() {
    local label="$1" h="$2" want="$3"; shift 3
    run_in "$h" "$BASH_BIN" "$(yakos_cli)" cost "$@" --json
    [ "$RC" = 0 ] && ok "$label: exit 0" || bad "$label: exit $RC"
    [ ! -s "$ERR" ] && ok "$label: nothing on stderr" || { bad "$label: stderr: $(cat "$ERR")"; }
    eq "$label: numbers" "$want" "$(cost_digest < "$OUT" 2>&1)"
}
H1="$(mkhome cost)"
WANT_RUNTIME="$(printf '%s\n' 'events=16' \
    'claude n=8 ok=8 fail=0 dur=217.5 in=775 out=2415' \
    'codex n=5 ok=4 fail=1 dur=200.5 in=270 out=950' \
    'agy n=2 ok=1 fail=1 dur=56 in=150 out=145' \
    'local n=1 ok=1 fail=0 dur=4 in=20 out=60')"
WANT_AGENT="$(printf '%s\n' 'events=16' \
    'backend n=3 ok=3 fail=0 dur=109.5 in=360 out=1500' \
    'frontend n=5 ok=4 fail=1 dur=200.5 in=270 out=950' \
    'architect n=2 ok=2 fail=0 dur=60.5 in=225 out=525' \
    'reviewer n=2 ok=2 fail=0 dur=35.5 in=180 out=350' \
    'researcher n=2 ok=1 fail=1 dur=56 in=150 out=145' \
    'local-fixture n=1 ok=1 fail=0 dur=4 in=20 out=60' \
    'doc-writer n=1 ok=1 fail=0 dur=12 in=10 out=40')"
WANT_DAY="$(printf '%s\n' 'events=16' \
    '2026-10-01 n=8 ok=7 fail=1 dur=160 in=655 out=1810' \
    '2026-09-20 n=2 ok=2 fail=0 dur=91 in=200 out=725' \
    '2026-09-02 n=2 ok=1 fail=1 dur=110 in=175 out=520' \
    '2026-09-01 n=2 ok=2 fail=0 dur=90 in=75 out=425' \
    '2026-09-03 n=1 ok=1 fail=0 dur=15 in=100 out=50' \
    '2026-10-02 n=1 ok=1 fail=0 dur=12 in=10 out=40')"
WANT_PROJECT="$(printf '%s\n' 'events=16' \
    '/fixture/proj-alpha n=8 ok=8 fail=0 dur=217.5 in=775 out=2415' \
    '/fixture/proj-beta n=7 ok=5 fail=2 dur=256.5 in=420 out=1095' \
    '/fixture/proj-gamma n=1 ok=1 fail=0 dur=4 in=20 out=60')"
WANT_SINCE="$(printf '%s\n' 'events=9' \
    'claude n=4 ok=4 fail=0 dur=71.5 in=440 out=1340' \
    'codex n=3 ok=2 fail=1 dur=60.5 in=155 out=325' \
    'agy n=1 ok=1 fail=0 dur=36 in=50 out=125' \
    'local n=1 ok=1 fail=0 dur=4 in=20 out=60')"
check_cost "cost --by runtime"            "$H1" "$WANT_RUNTIME" --by runtime
check_cost "cost --by agent"              "$H1" "$WANT_AGENT"   --by agent
check_cost "cost --by day"                "$H1" "$WANT_DAY"     --by day
check_cost "cost --by project"            "$H1" "$WANT_PROJECT" --by project
check_cost "cost --since 2026-10-01"      "$H1" "$WANT_SINCE"   --by runtime --since 2026-10-01

run_in "$H1" "$BASH_BIN" "$(yakos_cli)" cost --by runtime
flat="$(tr -s ' ' < "$OUT")"
[ "$RC" = 0 ] && [ ! -s "$ERR" ] && ok "cost table: exit 0, nothing on stderr" || bad "cost table: exit $RC, stderr: $(cat "$ERR")"
case "$flat" in *"16 dispatch event(s)"*) ok "cost table: header counts 16 events" ;; *) bad "cost table header: $flat" ;; esac
case "$flat" in *"claude 8 8 0 217.5 775 2415"*) ok "cost table: claude row" ;; *) bad "cost table has no claude row: $flat" ;; esac
case "$flat" in *"TOTAL 16 478 1215 3570"*) ok "cost table: TOTAL row" ;; *) bad "cost table has no TOTAL row: $flat" ;; esac

# Unknown keys are ignored: the same log with the K-136 keys deleted gives the
# same answer on every axis.
STRIPPED="$TMP/stripped-log.ndjson"
jq -c "del($(printf '%s' "$NEWKEYS" | sed 's/"\([a-z_]*\)"/.\1/g'))" "$FIXTURE" > "$STRIPPED"
H1S="$(mkhome cost-stripped "$STRIPPED")"
for axis in agent runtime day project; do
    run_in "$H1"  "$BASH_BIN" "$(yakos_cli)" cost --by "$axis" --json; a="$(jq -S -c . < "$OUT" 2>&1)"
    run_in "$H1S" "$BASH_BIN" "$(yakos_cli)" cost --by "$axis" --json; b="$(jq -S -c . < "$OUT" 2>&1)"
    [ -n "$a" ] && [ "$a" = "$b" ] && ok "cost --by $axis: identical with and without the new keys" || bad "cost --by $axis differs without the new keys"
done

# ---------------------------------------------------------------------------
echo "(2) model-routing.sh: the telemetry _mr_dispatch_case reads back"

# mr_read <home> <run_id> -> cost|duration_s|in_tokens|out_tokens. The real
# dispatch path runs dispatch.sh first; with no project resolvable in a fresh
# HOME it dies before it logs anything, so the row read back is the fixture's
# (the log is checksummed to prove nothing was appended).
mr_read() {
    HOME="$1" YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" RID="$2" \
        "$BASH_BIN" -c '
            set -- help
            . "$YAKOS_LIB/model-routing.sh" >/dev/null
            _mr_dispatch_case no-such-agent task sonnet "$RID" ""
            printf "%s|%s|%s|%s\n" "$_MR_DISPATCH_COST" "$_MR_DISPATCH_DURATION" "$_MR_DISPATCH_IN_TOK" "$_MR_DISPATCH_OUT_TOK"
        ' 2>>"$TMP/mr-err"
}
H2="$(mkhome mr)"
H2S="$(mkhome mr-stripped "$STRIPPED")"
sum_before="$(cksum < "$H2/.yakos-state/dispatch-log.ndjson")"
: > "$TMP/mr-err"
mr_check() {  # mr_check <label> <run_id> <want cost|dur|in|out>
    local got gots
    got="$(mr_read "$H2" "$2")"; gots="$(mr_read "$H2S" "$2")"
    eq "$1" "$3" "$got"
    [ "$got" = "$gots" ] && ok "$1: same without the new keys" || bad "$1: differs without the new keys ($gots)"
}
mr_check "bash claude row with usage (eval run 0001)"      run-FIXTURE-0001 '0.5|15|210|110'
mr_check "claude subscription row: usage cost 0, not api_equivalent_usd" run-FIXTURE-0002 '0|8.5|4|120'
mr_check "claude api row: usage cost is spend"             run-FIXTURE-0003 '0.5|30.5|5|300'
mr_check "codex subscription row: tokens, no dollars"      run-FIXTURE-0004 '0|45|800|250'
mr_check "agy subscription row: tokens, no dollars"        run-FIXTURE-0005 '0|36|278|95'
mr_check "ledger row without usage: est_* fallback"        run-FIXTURE-0006 '0|5|30|0'
mr_check "bash row without usage: est_* fallback"          run-FIXTURE-0007 '0|12|10|40'
mr_check "no row for the run id: zeros"                    run-FIXTURE-9999 '0|0|0|0'
if jq_err "$TMP/mr-err"; then bad "model-routing: jq error on stderr: $(head -3 "$TMP/mr-err")"; else ok "model-routing: no jq or parse error on stderr"; fi
eq "model-routing: reading left the log untouched" "$sum_before" "$(cksum < "$H2/.yakos-state/dispatch-log.ndjson")"

# ---------------------------------------------------------------------------
echo "(3) work-close.sh: yakos work close sums tokens_total and cost_usd"

# No dispatch writer emits tokens_total or cost_usd at the top level of a row;
# work-close and the plan-outcome hook read exactly those keys (and nothing under
# usage). Three clearly synthetic rows give them something to sum: one before the
# session window, two inside it. The ledger rows must add nothing and break nothing.
H3="$(mkhome work-close)"
LOG3="$H3/.yakos-state/dispatch-log.ndjson"
printf '%s\n' \
    '{"ts":"2026-08-31T00:00:00Z","agent":"synthetic","tokens_total":500,"cost_usd":0.005}' \
    '{"ts":"2026-10-03T00:00:00Z","agent":"synthetic","tokens_total":1000,"cost_usd":0.01}' \
    '{"ts":"2026-10-03T00:30:00Z","agent":"synthetic","tokens_total":2000,"cost_usd":0.02}' >> "$LOG3"
PROJ="$TMP/proj"; mkdir -p "$PROJ"
printf '%s\n' '{"type":"plan_scored","ts":"2026-10-01T00:00:00Z","plan_id":"plan-FIXTURE-0001","project":"/fixture/proj-alpha","verdict":"PASS","aggregate_score":0.8}' \
    > "$H3/.yakos-state/plan-quality-log.ndjson"
mkdir -p "$TMP/wd-window/current" "$TMP/wd-all/current"
printf '%s\n' '2026-10-01T00:00:00Z' > "$TMP/wd-window/current/.session-started"

# work_close <label> <work dir> <want tokens> <want cost>
work_close() {
    run_in "$H3" env YAKOS_WORK_DIR="$2" "$BASH_BIN" "$(yakos_cli)" work close --no-prompt --plan-id plan-FIXTURE-0001 --project "$PROJ"
    [ "$RC" = 0 ] && ok "$1: exit 0" || bad "$1: exit $RC: $(tail -3 "$ERR")"
    if jq_err "$ERR"; then bad "$1: jq error on stderr: $(head -3 "$ERR")"; else ok "$1: no jq or parse error on stderr"; fi
    local rec
    rec="$(tail -n 1 "$H3/.yakos-state/plan-quality-log.ndjson")"
    eq "$1: tokens_spent_total" "$3" "$(printf '%s' "$rec" | jq -r '.tokens_spent_total + 0' 2>&1)"
    eq "$1: cost_usd_actual" "$4" "$(printf '%s' "$rec" | jq -r '.cost_usd_actual + 0' 2>&1)"
    eq "$1: rework_cycles counts every agent row" "15" "$(printf '%s' "$rec" | jq -r '.rework_cycles' 2>&1)"
}
work_close "work close, whole log"            "$TMP/wd-all"    3500 0.035
work_close "work close, session window opens 2026-10-01" "$TMP/wd-window" 3000 0.03

# ---------------------------------------------------------------------------
echo "(4) plan-outcome-capture.sh: the SessionEnd fallback reads the same sums"

HOOK_POC="$REPO_ROOT/lib/hooks/plan-outcome-capture.sh"
PQ4="$TMP/pq4.ndjson"
printf '%s\n' '{"type":"plan_scored","ts":"2026-10-01T00:00:00Z","plan_id":"plan-FIXTURE-0002","project":"/fixture/proj-alpha","verdict":"PASS","aggregate_score":0.8}' > "$PQ4"
printf '%s' '{"hook_event_name":"SessionEnd","session_id":"ses_FIXTURE_0009"}' > "$TMP/sessionend.json"
mkdir -p "$TMP/wd-hook"
env HOME="$H3" YAKOS_PLAN_QUALITY_LOG="$PQ4" YAKOS_DISPATCH_LOG="$LOG3" YAKOS_WORK_DIR="$TMP/wd-hook" CLAUDE_PROJECT_DIR="$PROJ" \
    "$BASH_BIN" "$HOOK_POC" < "$TMP/sessionend.json" >"$OUT" 2>"$ERR"; RC=$?
[ "$RC" = 0 ] && ok "plan-outcome hook: exit 0" || bad "plan-outcome hook: exit $RC"
if jq_err "$ERR"; then bad "plan-outcome hook: jq error on stderr: $(head -3 "$ERR")"; else ok "plan-outcome hook: no jq or parse error on stderr"; fi
rec="$(tail -n 1 "$PQ4")"
eq "plan-outcome hook: wrote a plan_outcome for the open plan" "plan_outcome plan-FIXTURE-0002" "$(printf '%s' "$rec" | jq -r '"\(.type) \(.plan_id)"' 2>&1)"
eq "plan-outcome hook: tokens_spent_total" "3500" "$(printf '%s' "$rec" | jq -r '.tokens_spent_total + 0' 2>&1)"
eq "plan-outcome hook: cost_usd_actual" "0.035" "$(printf '%s' "$rec" | jq -r '.cost_usd_actual + 0' 2>&1)"

# ---------------------------------------------------------------------------
echo "(5) session.sh: yakos session export copies the project's rows verbatim"

H5="$(mkhome session)"
mkdir -p "$H5/agent-control/ledger-proj"
printf '%s\n' '/fixture/proj-alpha' > "$H5/agent-control/ledger-proj/.project-path"
run_in "$H5" "$BASH_BIN" "$(yakos_cli)" session export ledger-proj tag1
[ "$RC" = 0 ] && ok "session export: exit 0" || bad "session export: exit $RC: $(tail -3 "$ERR")"
if jq_err "$ERR"; then bad "session export: jq error on stderr: $(head -3 "$ERR")"; else ok "session export: no jq or parse error on stderr"; fi
BUNDLE="$H5/agent-control/ledger-proj/work/exports/ledger-proj-tag1.tar.gz"
mkdir -p "$TMP/untar"
if [ -f "$BUNDLE" ] && tar xzf "$BUNDLE" -C "$TMP/untar" 2>/dev/null; then
    got="$(jq -S -c . "$TMP/untar/ledger-proj-tag1/dispatch-log.ndjson" 2>&1)"
    want="$(jq -S -c 'select(.project == "/fixture/proj-alpha")' "$FIXTURE")"
    eq "session export: the project slice equals the fixture's rows for it" "$want" "$got"
    eq "session export: 11 rows for the project" "11" "$(printf '%s\n' "$got" | grep -c .)"
    eq "session export: the ledger keys survive the slice" "subscription 0.0123 console-chat ses_FIXTURE_0001" \
        "$(printf '%s\n' "$got" | jq -r 'select(.native_session_id == "ses_FIXTURE_0001") | "\(.billing) \(.api_equivalent_usd) \(.surface) \(.native_session_id)"' 2>&1)"
else
    bad "session export: no bundle at $BUNDLE"
fi

# ---------------------------------------------------------------------------
echo "(6) recipes: the jq one-liners other tooling runs on the log"

# The dispatch workflow keeps the usage object of the log's LAST row. The
# expression is copied from .github/workflows/yakos-dispatch.yml; the grep keeps
# the copy honest.
if grep -qF "jq -c '.usage // {}'" "$REPO_ROOT/.github/workflows/yakos-dispatch.yml"; then ok "workflow still reads the last row with jq -c '.usage // {}'"; else bad "yakos-dispatch.yml no longer holds the usage recipe; update this suite"; fi
jq -c 'select(.native_session_id == "ses_FIXTURE_0001")' "$FIXTURE" > "$TMP/one-row.ndjson"
eq "workflow recipe on a subscription ledger row: usage, cost 0" \
    '{"input_tokens":4,"output_tokens":120,"cache_read":21000,"cache_creation":3500,"duration_ms":8200,"total_cost_usd":0}' \
    "$(tail -1 "$TMP/one-row.ndjson" | jq -c '.usage // {}' 2>&1)"
jq -c 'select(.type == "dispatch_started")' "$FIXTURE" | tail -1 > "$TMP/started-row.ndjson"
eq "workflow recipe on a row with no usage: {}" '{}' "$(tail -1 "$TMP/started-row.ndjson" | jq -c '.usage // {}' 2>&1)"

# agent-audit, pattern 2 (wrong runtime): per agent with 3+ rows, runtimes and majority.
if grep -qF 'majority: ([.[] | .runtime] | group_by(.) | sort_by(length) | last | .[0])' "$REPO_ROOT/lib/skills/agent-audit/SKILL.md"; then ok "agent-audit still holds the pattern-2 recipe"; else bad "agent-audit SKILL.md changed its pattern-2 recipe; update this suite"; fi
got="$(jq -c --arg s "2026-09-01" 'select(.ts >= $s)' "$FIXTURE" | jq -s -c 'group_by(.agent)
       | map(select(length >= 3))
       | map({
           agent: .[0].agent,
           by_runtime: ([.[] | .runtime] | group_by(.) | map({(.[0]): length}) | add),
           majority: ([.[] | .runtime] | group_by(.) | sort_by(length) | last | .[0])
         })' 2>&1)"
eq "agent-audit pattern 2 on the mixed log" \
    '[{"agent":"architect","by_runtime":{"claude":3},"majority":"claude"},{"agent":"backend","by_runtime":{"claude":5},"majority":"claude"},{"agent":"frontend","by_runtime":{"codex":5},"majority":"codex"}]' "$got"
eq "agent-audit budget_violation recipe finds the one violation" "backend 0.62" \
    "$(jq -r 'select(.type == "budget_violation") | "\(.agent) \(.actual_cost_usd)"' "$FIXTURE" 2>&1)"

# session-summary: this session's finished dispatches for the project.
got="$(jq -c --arg s "2026-10-01T00:00:00Z" --arg p "/fixture/proj-alpha" \
    'select(.type == "dispatch_finished" and .ts >= $s and .project == $p)' "$FIXTURE" 2>&1 | jq -r '.agent' | tr '\n' ' ')"
eq "session-summary recipe: finished dispatches of the project since the session start" "backend architect reviewer doc-writer " "$got"

# ---------------------------------------------------------------------------
echo "(7) supervisor-stream.sh: the budget gate on a mixed spend log"

HOOK_SS="$REPO_ROOT/lib/hooks/supervisor-stream.sh"
# go_budget_ok: the Go binary exists, runs here and answers `budget check --json`. One that is
# present but cannot run (built for another system) or predates `budget` counts as missing.
go_budget_ok() {
    [ -x "$GO_BINARY" ] || return 1
    mkdir -p "$TMP/preflight"; chmod 700 "$TMP/preflight"
    YAKOS_DISPATCH_LOG="$TMP/preflight" "$GO_BINARY" budget check supervisor --json 2>/dev/null | jq -e '.state' >/dev/null 2>&1
}
if ! go_budget_ok; then
    if [ "${YAKOS_REQUIRE_GO_BINARY:-}" = 1 ]; then
        bad "(7) no runnable Go binary at $GO_BINARY and YAKOS_REQUIRE_GO_BINARY=1 (make build, or set YAKOS_GO_BINARY)"
    else
        echo "  SKIP (7) no runnable Go binary at $GO_BINARY; the hook reads the budget through 'yakos budget check' (make build, or set YAKOS_GO_BINARY)"
    fi
else
    SID="ledger-sid"
    NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    jq -nc --arg s "$SID" --arg b "$(awk 'BEGIN { for (i = 0; i < 25; i++) print "line" }')" \
        '{session_id:$s,hook_event_name:"PostToolUse",tool_name:"Edit",tool_input:{file_path:"big.go",new_string:$b}}' > "$TMP/benign.json"
    # mksb <name> <log row>...: a hook sandbox whose state dir holds the rows. The
    # supervisor's built-in limit ($100 a month) applies; no policy is written.
    mksb() {
        local sb="$TMP/$1" row; shift
        mkdir -p "$sb/.claude" "$sb/work/current/logs" "$sb/bin" "$sb/state"
        chmod 700 "$sb/state"
        printf 'supervisor:\n  score_every_n_calls: 1\n' > "$sb/.yakos.yml"
        printf 'min_launch_interval_s: 0\n' > "$sb/state/supervisor-policy.yml"; chmod 600 "$sb/state/supervisor-policy.yml"
        : > "$sb/state/dispatch-log.ndjson"
        for row in "$@"; do printf '%s\n' "$row" >> "$sb/state/dispatch-log.ndjson"; done
        # Fake CLI: budget goes to the real engine, dispatch is recorded.
        printf '#!/bin/sh\nif [ "$1" = budget ]; then exec "%s" "$@"; fi\nprintf "run\\n" >> "%s/runs"\n' "$GO_BINARY" "$sb" > "$sb/bin/fakeyakos"
        chmod +x "$sb/bin/fakeyakos"
        printf '%s' "$sb"
    }
    ss_runs() { [ -f "$1/runs" ] && wc -l < "$1/runs" | tr -d ' ' || echo 0; }
    # ss_fire <sandbox> <launches expected>: one routine event through the bash hook.
    # A launch is forked and recorded a moment later, so wait for it; when none is
    # expected the refusal is already on stderr, and a short pause shows none follows.
    ss_fire() {
        local n=0
        env YAKOS_DISPATCH_LOG="$1/state" YAKOS_CLI="$1/bin/fakeyakos" YAKOS_WORK_DIR="$1/work" CLAUDE_PROJECT_DIR="$1" \
            "$BASH_BIN" "$HOOK_SS" < "$TMP/benign.json" >/dev/null 2>"$1/hook.stderr"
        if [ "$2" -gt 0 ]; then
            while [ "$(ss_runs "$1")" -lt "$2" ] && [ "$n" -lt 50 ]; do sleep 0.1; n=$((n + 1)); done
        else
            sleep 0.4
        fi
    }
    row() {  # row <billing or ""> <usage cost> [api equivalent]
        jq -nc --arg t "$NOW" --arg b "$1" --argjson c "$2" --argjson a "${3:-0}" \
            '{type:"dispatch_finished",ts:$t,agent:"supervisor",runtime:"claude",project:"/fixture/proj-alpha",exit_code:0,duration_s:4,est_input_tokens:10,est_output_tokens:10,
              usage:{input_tokens:5,output_tokens:90,cache_read:9000,cache_creation:0,duration_ms:4000,total_cost_usd:$c}}
             + (if $b != "" then {provider:"anthropic",model_id:"claude-sonnet-5-5",billing:$b,cost_source:"harness",surface:"cli"} else {} end)
             + (if $a > 0 then {api_equivalent_usd:$a} else {} end)'
    }
    sb="$(mksb ss-sub "$(row subscription 0 250)")"; ss_fire "$sb" 1
    [ "$(ss_runs "$sb")" = 1 ] && ! grep -qi budget "$sb/hook.stderr" && ok "(7) subscription row (api equivalent \$250, usage cost 0): the launch proceeds, nothing budget-related said" || bad "(7) subscription row: runs=$(ss_runs "$sb") stderr: $(cat "$sb/hook.stderr")"
    sb="$(mksb ss-api "$(row api 150)")"; ss_fire "$sb" 0
    [ "$(ss_runs "$sb")" = 0 ] && grep -q 'supervisor budget exhausted' "$sb/hook.stderr" && ok "(7) api row costing \$150: the routine launch is refused" || bad "(7) api row: runs=$(ss_runs "$sb") stderr: $(cat "$sb/hook.stderr")"
    sb="$(mksb ss-legacy "$(row "" 150)")"; ss_fire "$sb" 0
    [ "$(ss_runs "$sb")" = 0 ] && grep -q 'supervisor budget exhausted' "$sb/hook.stderr" && ok "(7) row without billing (bash or pre-K-136) costing \$150: still counts, the launch is refused" || bad "(7) legacy row: runs=$(ss_runs "$sb") stderr: $(cat "$sb/hook.stderr")"
fi

# ---------------------------------------------------------------------------
echo "(8) dispatch.sh: the bash writer appends next to ledger rows"

# The other half of the shared log. A bash dispatch (a mock runtime plugin, as in
# run-dispatch-log-perms-test.sh) appends to a log that already holds ledger rows:
# the file must stay valid NDJSON, the new rows must be bash-shaped (none of the
# K-136 keys) and `yakos cost` must count the new dispatch with the old ones.
H8="$(mkhome writer)"
PROJ8="$TMP/writer-project"
mkdir -p "$H8/.yakos/plugins/mock-ledger" "$PROJ8/.claude/agents"
cat > "$PROJ8/.claude/agents/test-agent.md" <<'AGENTEOF'
---
id: test-agent
role: specialist
domain: test
mode: [feature]
tools: [Read]
model: sonnet
references: []
version: "1.0"
---

# Test Agent

## Purpose

Minimal agent for the ledger-compat writer test.
AGENTEOF
cat > "$H8/.yakos/plugins/mock-ledger/runtime.sh" <<'RUNTIMEEOF'
#!/usr/bin/env bash
set -eu
yk_rt_mock_ledger_id()                  { printf 'mock-ledger\n'; }
yk_rt_mock_ledger_capabilities()        { printf 'headless-print\n'; }
yk_rt_mock_ledger_check_cli()           { return 0; }
yk_rt_mock_ledger_check_auth()          { return 0; }
yk_rt_mock_ledger_materialize_agents()  { return 0; }
yk_rt_mock_ledger_cleanup_agents()      { return 0; }
yk_rt_mock_ledger_launch()              { return 0; }
yk_rt_mock_ledger_dispatch() {
    printf 'ok\n'
    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        printf '%s\n' '{"input_tokens":7,"output_tokens":9,"cache_read":0,"cache_creation":0,"duration_ms":10,"total_cost_usd":0.25}' > "$YAKOS_USAGE_OUT"
    fi
}
RUNTIMEEOF
chmod +x "$H8/.yakos/plugins/mock-ledger/runtime.sh"
LOG8="$H8/.yakos-state/dispatch-log.ndjson"
# dispatch.sh runs its adapter function under a shell-native deadline, so a
# timeout(1) on PATH (Linux, or macOS with coreutils) no longer matters (K-169).
run_in "$H8" env YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    "$BASH_BIN" "$REPO_ROOT/cli/lib/dispatch.sh" test-agent "dummy task" --runtime mock-ledger --project "$PROJ8" --timeout 30
[ "$RC" = 0 ] && ok "bash dispatch next to ledger rows: exit 0" || bad "bash dispatch: exit $RC: $(tail -3 "$ERR")"
if jq -e . "$LOG8" >/dev/null 2>&1; then ok "the shared log is still valid NDJSON"; else bad "the shared log no longer parses"; fi
eq "the bash dispatch appended a started and a finished row" "22 dispatch_started dispatch_finished" \
    "$(wc -l < "$LOG8" | tr -d ' ') $(tail -n 2 "$LOG8" | jq -r '.type' | tr '\n' ' ' | sed 's/ $//')"
eq "the appended finished row is bash-shaped: runtime, usage cost, no ledger keys" "mock-ledger 0.25 0" \
    "$(tail -n 1 "$LOG8" | jq -r --argjson nk "[$NEWKEYS]" '"\(.runtime) \(.usage.total_cost_usd) \([keys[] | select(. as $k | any($nk[]; . == $k))] | length)"' 2>&1)"
run_in "$H8" "$BASH_BIN" "$(yakos_cli)" cost --by runtime --json
[ "$RC" = 0 ] && [ ! -s "$ERR" ] && ok "cost over the grown log: exit 0, nothing on stderr" || bad "cost over the grown log: exit $RC: $(cat "$ERR")"
want="$(printf '%s\n' "$WANT_RUNTIME" | sed 's/^events=16$/events=17/'; printf '%s\n' 'mock-ledger n=1 ok=1 fail=0 in=2 out=0')"
got="$(cost_digest < "$OUT" 2>&1 | sed 's/^\(mock-ledger n=1 ok=1 fail=0\) dur=[0-9]* /\1 /')"
eq "cost counts the new bash dispatch with the old rows" "$want" "$got"

echo "ledger-compat: $pass passed, $fail failed"
[ "$fail" = 0 ]
