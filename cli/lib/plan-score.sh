#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: plan-score.sh — operator CLI for plan quality eval records.
#
# Subcommands:
#   yakos plan score show [<plan_id>]
#       Pretty-print the most recent record from plan-quality-log.ndjson,
#       or the latest record matching <plan_id>.
#
#   yakos plan score history [--project <p>] [--limit <n>]
#       Tabular listing: ts | plan_id | verdict | aggregate | dissent | cost_usd
#       Default limit: 20.
#
#   yakos plan score override <plan_id> --reason "<text>"
#       Append an override record to the log; if work/current/.plan-blocked
#       exists for this plan_id, remove it. Refuses if plan_id has no
#       existing record.

set -eu

: "${YAKOS_LIB:?plan-score.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/paths.sh"

PLAN_QUALITY_LOG="${HOME}/.yakos-state/plan-quality-log.ndjson"
# Allow override for tests
PLAN_QUALITY_LOG="${YAKOS_PLAN_QUALITY_LOG:-$PLAN_QUALITY_LOG}"

# ---- skill root (for report script) -----------------------------------------
_plan_score_skill_dir() {
    local yakos_root="${YAKOS_ROOT:-$(cd "$YAKOS_LIB/../.." && pwd -P)}"
    printf '%s/lib/skills/plan-quality-eval' "$yakos_root"
}

usage() {
    cat <<'EOF'
yakos plan score <subcommand> [args...]

Subcommands:
  show [<plan_id>]
      Pretty-print the most recent scoring record (or the most recent
      record for a specific plan_id). Uses report-plan-score.sh to render.

  history [--project <name>] [--limit <n>]
      Tabular history: ts | plan_id | verdict | aggregate | dissent | cost_usd
      Sorted newest-first. Default limit: 20.

  override <plan_id> --reason "<text>"
      Mark a blocked plan as reviewed. Appends an override record to the
      log and removes work/current/.plan-blocked if it matches the plan_id.
      Requires --reason. Refuses if plan_id has no existing scored record.

Log file: ~/.yakos-state/plan-quality-log.ndjson
EOF
}

# ---- subcommand: show --------------------------------------------------------

cmd_show() {
    local plan_id="${1:-}"

    if [ ! -f "$PLAN_QUALITY_LOG" ] || [ ! -s "$PLAN_QUALITY_LOG" ]; then
        echo "No plan-quality-log.ndjson found at $PLAN_QUALITY_LOG" >&2
        echo "Run 'yakos skill plan-quality-eval <plan.md>' first." >&2
        exit 1
    fi

    if ! command -v jq >/dev/null 2>&1; then
        ct_die "plan score show: jq is required"
    fi

    # Find the record to display
    local record=""
    if [ -n "$plan_id" ]; then
        record="$(grep '"type"' "$PLAN_QUALITY_LOG" 2>/dev/null \
            | grep '"plan_scored"' \
            | grep "\"$plan_id\"" \
            | tail -1 || true)"
        if [ -z "$record" ]; then
            echo "No record found for plan_id: $plan_id" >&2
            exit 1
        fi
    fi

    # Use report script which reads the last record from the log
    local report_script
    report_script="$(_plan_score_skill_dir)/scripts/report-plan-score.sh"

    if [ ! -f "$report_script" ]; then
        ct_die "plan score show: report script not found at $report_script"
    fi

    if [ -n "$plan_id" ]; then
        # Write a temp log with just this record and render it
        local tmp_log
        tmp_log="$(mktemp -t yakos-pqs-show.XXXXXX.ndjson)"
        trap 'rm -f "$tmp_log" 2>/dev/null || true' RETURN
        printf '%s\n' "$record" > "$tmp_log"
        bash "$report_script" "$tmp_log"
    else
        bash "$report_script" "$PLAN_QUALITY_LOG"
    fi
}

# ---- subcommand: history -----------------------------------------------------

cmd_history() {
    local filter_project=""
    local limit=20

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --project) shift; filter_project="$1" ;;
            --project=*) filter_project="${1#--project=}" ;;
            --limit) shift; limit="$1" ;;
            --limit=*) limit="${1#--limit=}" ;;
            -h|--help) usage; exit 0 ;;
            *) ct_die "plan score history: unknown option '$1'" ;;
        esac
        shift
    done

    if [ ! -f "$PLAN_QUALITY_LOG" ] || [ ! -s "$PLAN_QUALITY_LOG" ]; then
        echo "No records found. Run 'yakos skill plan-quality-eval <plan.md>' first."
        exit 0
    fi

    if ! command -v jq >/dev/null 2>&1; then
        ct_die "plan score history: jq is required"
    fi

    # Print header
    printf '%-26s  %-28s  %-7s  %-9s  %-7s  %-10s\n' \
        "ts" "plan_id" "verdict" "aggregate" "dissent" "cost_usd"
    printf '%s\n' "$(printf '%-26s--%-28s--%-7s--%-9s--%-7s--%-10s' \
        "" "" "" "" "" "" | tr ' -' '-')"

    # Collect matching records into a temp file, then print last N newest-first.
    local tmp_recs
    tmp_recs="$(mktemp -t yakos-pqs-hist.XXXXXX)"
    # shellcheck disable=SC2064
    trap "rm -f '$tmp_recs' 2>/dev/null || true" RETURN

    {
        grep '"type"' "$PLAN_QUALITY_LOG" 2>/dev/null | grep '"plan_scored"' || true
    } | {
        if [ -n "$filter_project" ]; then
            grep "\"$filter_project\"" || true
        else
            cat
        fi
    } > "$tmp_recs" 2>/dev/null || true

    # Reverse the file (newest-first): tail -r on macOS, tac on Linux, awk fallback
    if tail -r "$tmp_recs" 2>/dev/null | head -c 1 >/dev/null 2>&1; then
        _pqs_reversed() { tail -r "$1"; }
    elif command -v tac >/dev/null 2>&1; then
        _pqs_reversed() { tac "$1"; }
    else
        _pqs_reversed() { awk '{ lines[NR]=$0 } END { for (i=NR; i>=1; i--) print lines[i] }' "$1"; }
    fi

    _pqs_reversed "$tmp_recs" | head -n "$limit" | while IFS= read -r rec; do
        [ -n "$rec" ] || continue
        if ! printf '%s' "$rec" | jq empty 2>/dev/null; then continue; fi
        ts="$(printf '%s' "$rec" | jq -r '.ts // ""')"
        pid="$(printf '%s' "$rec" | jq -r '.plan_id // ""')"
        verdict="$(printf '%s' "$rec" | jq -r '.verdict // ""')"
        agg="$(printf '%s' "$rec" | jq -r '.aggregate_score // 0')"
        dissent="$(printf '%s' "$rec" | jq -r '.dissent // false')"
        cost="$(printf '%s' "$rec" | jq -r '.cost_usd // 0')"
        printf '%-26s  %-28s  %-7s  %-9s  %-7s  %-10s\n' \
            "${ts:0:24}" "${pid:0:28}" "$verdict" "$agg" "$dissent" "$cost"
    done
}

# ---- subcommand: override ----------------------------------------------------

cmd_override() {
    local plan_id="${1:-}"
    [ -n "$plan_id" ] || { usage; exit 1; }
    shift

    local reason=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --reason) shift; reason="$1" ;;
            --reason=*) reason="${1#--reason=}" ;;
            -h|--help) usage; exit 0 ;;
            *) ct_die "plan score override: unknown option '$1'" ;;
        esac
        shift
    done

    [ -n "$reason" ] || ct_die "plan score override: --reason is required"

    if ! command -v jq >/dev/null 2>&1; then
        ct_die "plan score override: jq is required"
    fi

    # Verify the plan_id has at least one existing scored record
    local existing=""
    if [ -f "$PLAN_QUALITY_LOG" ] && [ -s "$PLAN_QUALITY_LOG" ]; then
        existing="$(grep '"plan_scored"' "$PLAN_QUALITY_LOG" 2>/dev/null \
            | grep "\"$plan_id\"" | tail -1 || true)"
    fi

    if [ -z "$existing" ]; then
        echo "plan score override: no existing record for plan_id '$plan_id'" >&2
        echo "Use 'yakos plan score history' to see recorded plan IDs." >&2
        exit 1
    fi

    # Append override record
    local ts
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    local user="${USER:-$(id -un 2>/dev/null || echo unknown)}"

    mkdir -p "$(dirname "$PLAN_QUALITY_LOG")" 2>/dev/null || true
    jq -nc \
        --arg type "plan_score_override" \
        --arg ts "$ts" \
        --arg plan_id "$plan_id" \
        --arg reason "$reason" \
        --arg user "$user" \
        '{type: $type, ts: $ts, plan_id: $plan_id, reason: $reason, user: $user}' \
        >> "$PLAN_QUALITY_LOG"

    echo "Override recorded for plan_id: $plan_id"

    # Remove .plan-blocked marker if it matches this plan_id
    local current_dir
    current_dir="$(yakos_current_dir)"
    local blocked_marker="$current_dir/.plan-blocked"

    if [ -f "$blocked_marker" ]; then
        local marker_plan_id=""
        if jq -e . "$blocked_marker" >/dev/null 2>&1; then
            marker_plan_id="$(jq -r '.plan_id // empty' "$blocked_marker" 2>/dev/null || true)"
        fi
        if [ "$marker_plan_id" = "$plan_id" ] || [ -z "$marker_plan_id" ]; then
            rm -f "$blocked_marker"
            echo ".plan-blocked marker removed — dispatch gate lifted."
        else
            echo "Note: .plan-blocked marker exists for plan_id '$marker_plan_id' (not '$plan_id'); not removed."
        fi
    fi
}

# ---- dispatcher --------------------------------------------------------------

if [ "$#" -eq 0 ]; then
    usage
    exit 0
fi

subcmd="${1:-}"
shift

case "$subcmd" in
    score)
        # 'yakos plan score <sub>' — strip the 'score' word and re-dispatch
        # so that 'yakos plan score show' routes the same as 'yakos plan show'.
        subcmd="${1:-}"
        [ "$#" -gt 0 ] && shift
        case "$subcmd" in
            show)     cmd_show "$@" ;;
            history)  cmd_history "$@" ;;
            override) cmd_override "$@" ;;
            -h|--help|help|"") usage; exit 0 ;;
            *)
                echo "plan score: unknown subcommand '$subcmd'" >&2
                echo >&2; usage >&2; exit 64 ;;
        esac
        ;;
    show)
        cmd_show "$@"
        ;;
    history)
        cmd_history "$@"
        ;;
    override)
        cmd_override "$@"
        ;;
    -h|--help|help)
        usage
        exit 0
        ;;
    *)
        echo "plan score: unknown subcommand '$subcmd'" >&2
        echo >&2
        usage >&2
        exit 64  # EX_USAGE
        ;;
esac
