#!/usr/bin/env bash
# cost.sh — aggregate dispatch-log telemetry into a per-runtime / per-agent
# cost report.
#
# Reads ~/.yakos-state/dispatch-log.ndjson (rotated archives optional) and
# rolls up est_input_tokens, est_output_tokens, duration_s, and exit codes
# by axis. The chars/4 token estimate is rough; v0.6.x will overlay
# actual tokens parsed from runtime --json output for higher fidelity.

set -eu

: "${YAKOS_LIB:?cost.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos cost [--since <ISO>] [--by agent|runtime|day|project]
            [--all-projects] [--json]

Aggregate dispatch-log.ndjson telemetry. Defaults to all-time, by-runtime.

By default, --by project / --all-projects rolls up across every project
that has appeared in the dispatch-log. Useful for multi-project burn-rate
review.

Flags:
  --since <ISO-date>   Filter events with ts >= <ISO-date>. Examples:
                       --since 2026-05-01 (start of day)
                       --since 2026-05-08T12:00:00Z
  --by <axis>          Aggregation axis: agent, runtime (default), day.
  --json               Emit machine-readable JSON instead of a table.

Sources rolled up:
  ~/.yakos-state/dispatch-log.ndjson      (current)
  ~/.yakos-state/dispatch-log.*.ndjson    (rotated archives)

Token columns:
  est_in / est_out — chars/4 estimate from prompt/output bytes.
  Real per-runtime token counts arrive in v0.6.x once stream-json
  parsing per-adapter lands.

Examples:
  yakos cost
  yakos cost --by agent --since 2026-05-01
  yakos cost --by day --json | jq
EOF
}

SINCE=""
BY="runtime"
EMIT_JSON=0
ALL_PROJECTS=0

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --since) shift; [ "$#" -gt 0 ] || ct_die "cost: --since requires a date"; SINCE="$1" ;;
        --since=*) SINCE="${1#--since=}" ;;
        --by) shift; [ "$#" -gt 0 ] || ct_die "cost: --by requires an axis"; BY="$1" ;;
        --by=*) BY="${1#--by=}" ;;
        --json) EMIT_JSON=1 ;;
        --all-projects) ALL_PROJECTS=1 ;;
        -*) ct_die "cost: unknown flag '$1'" ;;
        *) ct_die "cost: unexpected argument '$1'" ;;
    esac
    shift
done

case "$BY" in
    agent|runtime|day|project) ;;
    *) ct_die "cost: --by must be agent | runtime | day | project, got '$BY'" ;;
esac

if [ "$BY" = "project" ]; then
    ALL_PROJECTS=1
fi

LOG_DIR="$HOME/.yakos-state"
PATTERN="$LOG_DIR/dispatch-log*.ndjson"

# Collect all matching log files.
files=()
for f in $PATTERN; do
    [ -f "$f" ] || continue
    files+=("$f")
done

if [ "${#files[@]}" = "0" ]; then
    if [ "$EMIT_JSON" = "1" ]; then
        echo '{"events":0,"rows":[]}'
    else
        echo "no dispatch-log.ndjson found at $LOG_DIR (no dispatches yet)"
    fi
    exit 0
fi

# Concatenate, filter dispatch_finished, optional --since cut, group by axis.
concat="$(cat "${files[@]}")"

# Filter to dispatch_finished events only — those have token + duration.
finished="$(printf '%s' "$concat" | jq -c 'select(.type == "dispatch_finished")' 2>/dev/null)"

if [ -n "$SINCE" ]; then
    finished="$(printf '%s' "$finished" | jq -c --arg s "$SINCE" 'select(.ts >= $s)')"
fi

n_events="$(printf '%s\n' "$finished" | grep -c . 2>/dev/null || echo 0)"

if [ "$n_events" = "0" ]; then
    if [ "$EMIT_JSON" = "1" ]; then
        echo '{"events":0,"rows":[]}'
    else
        echo "no dispatch_finished events match the filter (since=$SINCE)"
    fi
    exit 0
fi

# Build the aggregation key per axis.
case "$BY" in
    agent)   key_expr='.agent' ;;
    runtime) key_expr='.runtime' ;;
    day)     key_expr='.ts | split("T")[0]' ;;
    project) key_expr='.project // "(unknown)"' ;;
esac

# jq-side rollup. Note: input is NDJSON (one event per line); use -s to
# slurp into an array, then group_by + map.
rolled="$(printf '%s\n' "$finished" | jq -s --arg axis "$BY" "
    group_by($key_expr)
    | map({
        key: (.[0] | $key_expr),
        count: length,
        ok: ([.[] | select(.exit_code == 0)] | length),
        fail: ([.[] | select(.exit_code != 0)] | length),
        total_duration_s: ([.[] | .duration_s // 0] | add),
        total_in_tokens: ([.[] | .est_input_tokens // 0] | add),
        total_out_tokens: ([.[] | .est_output_tokens // 0] | add)
      })
    | sort_by(.total_in_tokens + .total_out_tokens)
    | reverse
    ")"

if [ "$EMIT_JSON" = "1" ]; then
    printf '%s\n' "$rolled" | jq --argjson n "$n_events" '{events:$n, rows:.}'
    exit 0
fi

# Pretty table.
echo "yakos cost — $n_events dispatch event(s)$( [ -n "$SINCE" ] && printf ' since %s' "$SINCE" ) by $BY"
echo
printf '  %-24s %6s %5s %5s %8s %10s %10s\n' "key" "count" "ok" "fail" "dur(s)" "est_in_tok" "est_out_tok"
printf '  %-24s %6s %5s %5s %8s %10s %10s\n' "------------------------" "------" "-----" "-----" "--------" "----------" "----------"
printf '%s\n' "$rolled" | jq -r '.[] |
    "  \(.key | tostring | .[0:24]) \(.count) \(.ok) \(.fail) \(.total_duration_s) \(.total_in_tokens) \(.total_out_tokens)"' \
    | awk '{ printf "  %-24s %6s %5s %5s %8s %10s %10s\n", $1, $2, $3, $4, $5, $6, $7 }'
echo
total_in="$(printf '%s' "$rolled" | jq '[.[] | .total_in_tokens] | add')"
total_out="$(printf '%s' "$rolled" | jq '[.[] | .total_out_tokens] | add')"
total_dur="$(printf '%s' "$rolled" | jq '[.[] | .total_duration_s] | add')"
printf '  %-24s %6s %5s %5s %8s %10s %10s\n' "TOTAL" "$n_events" "" "" "$total_dur" "$total_in" "$total_out"
