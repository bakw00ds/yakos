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

  correlate [--project <p>] [--since <iso>] [--min-n <n>]
      Join plan_scored + plan_outcome records by plan_id and emit a
      markdown report showing per-dimension Pearson r against first_try_pass,
      scope-creep by score quartile, and threshold → outcome rates.
      Requires at least --min-n (default 20) joined records; refuses with
      exit 1 if the sample is too small.

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

# ---- subcommand: correlate ---------------------------------------------------

cmd_correlate() {
    local filter_project=""
    local since_iso=""
    local min_n=20

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --project)   shift; filter_project="$1" ;;
            --project=*) filter_project="${1#--project=}" ;;
            --since)     shift; since_iso="$1" ;;
            --since=*)   since_iso="${1#--since=}" ;;
            --min-n)     shift; min_n="$1" ;;
            --min-n=*)   min_n="${1#--min-n=}" ;;
            -h|--help)   usage; exit 0 ;;
            *) ct_die "plan score correlate: unknown option '$1'" ;;
        esac
        shift
    done

    if ! command -v jq >/dev/null 2>&1; then
        ct_die "plan score correlate: jq is required"
    fi

    if [ ! -f "$PLAN_QUALITY_LOG" ] || [ ! -s "$PLAN_QUALITY_LOG" ]; then
        echo "No records found in $PLAN_QUALITY_LOG" >&2
        exit 1
    fi

    # Default since: 90 days ago
    if [ -z "$since_iso" ]; then
        # Portable: subtract 90 days in seconds
        local ninety_days_ago_s
        ninety_days_ago_s="$(( $(date +%s) - 7776000 ))"
        # Format as ISO-8601
        if date -u -d "@$ninety_days_ago_s" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
            since_iso="$(date -u -d "@$ninety_days_ago_s" +%Y-%m-%dT%H:%M:%SZ)"
        elif date -u -r "$ninety_days_ago_s" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
            since_iso="$(date -u -r "$ninety_days_ago_s" +%Y-%m-%dT%H:%M:%SZ)"
        else
            since_iso="1970-01-01T00:00:00Z"
        fi
    fi

    # Extract and join scored + outcome records using jq + awk
    # Build a temp file of joined records (TSV: plan_id, aggregate, dim×6, scope_creep, first_try_pass)
    local tmp_joined
    tmp_joined="$(mktemp -t yakos-pqs-corr.XXXXXX)"
    trap 'rm -f "$tmp_joined" 2>/dev/null || true' RETURN

    # Step 1: extract all plan_scored records into a JSON object keyed by plan_id
    # Step 2: for each plan_outcome, look up the scored record and emit a joined line
    # Note: use [., inputs] (not [inputs]) because the first object from the file
    # is bound to . and inputs reads the rest.
    jq -r --arg since "$since_iso" --arg proj "$filter_project" '
        # Collect all records
        [., inputs] |
        # Build lookup of scored records by plan_id
        . as $all |
        ($all | map(select(.type == "plan_scored" and .ts >= $since))
              | map(select(if $proj != "" then .project == $proj else true end))
              | group_by(.plan_id)
              | map({key: .[0].plan_id, value: .[-1]})
              | from_entries) as $scored |
        # For each outcome record, join
        $all | map(select(.type == "plan_outcome" and .plan_id != null)) |
        map(select(if $proj != "" then .project == $proj else true end)) |
        map(
            . as $o |
            ($scored[$o.plan_id] // null) as $s |
            if $s != null then
                [
                    $o.plan_id,
                    ($s.aggregate_score // 0),
                    ($s.per_dimension_median.acceptance_criteria_specificity // 0),
                    ($s.per_dimension_median.assumption_surfacing // 0),
                    ($s.per_dimension_median.decomposition_granularity // 0),
                    ($s.per_dimension_median.dependency_clarity // 0),
                    ($s.per_dimension_median.domain_boundaries_respected // 0),
                    ($s.per_dimension_median.risk_rollback_honesty // 0),
                    ($o.scope_creep_ratio // -1),
                    (if $o.first_try_pass == true then 1 elif $o.first_try_pass == false then 0 else -1 end)
                ] | @tsv
            else empty end
        ) | .[]
    ' "$PLAN_QUALITY_LOG" 2>/dev/null > "$tmp_joined" || true

    local n_joined
    n_joined="$(wc -l < "$tmp_joined" | tr -d ' ')"

    if [ "${n_joined:-0}" -lt "$min_n" ]; then
        echo "plan score correlate: only $n_joined joined records found (minimum is $min_n)." >&2
        echo "  Collect more plan outcomes via 'yakos work close' before running correlate." >&2
        echo "  To lower the minimum: --min-n <n>" >&2
        exit 1
    fi

    # Compute Pearson r and other stats in awk
    local report
    report="$(awk -v n_joined="$n_joined" -v since="$since_iso" '
    BEGIN {
        ndims = 6
        split("acceptance_criteria_specificity assumption_surfacing decomposition_granularity dependency_clarity domain_boundaries_respected risk_rollback_honesty", dimnames, " ")
        # Indices: col 2=aggregate, 3..8=dims, 9=scope_creep, 10=first_try_pass
        # We compute Pearson r between each dim score and first_try_pass
        # Only rows where first_try_pass != -1 contribute to r
        # Only rows where scope_creep != -1 contribute to quartile stats
    }
    {
        plan_id     = $1
        aggregate   = $2
        # Use flat string key "d_row" for POSIX awk compatibility (no 2D arrays)
        for (d=1; d<=6; d++) dim_scores[d "_" NR] = $(d+2)
        scope_creep[NR] = $9
        ftp[NR]     = $10
        agg[NR]     = $2
        total_rows  = NR
    }
    END {
        printf "Plan-quality vs outcomes (n=%d joined plans, since %s):\n\n", n_joined, since

        # --- Per-dimension Pearson r against first_try_pass ---
        printf "  Per-dimension Pearson r against first_try_pass:\n"

        max_r = -999; max_dim = ""
        for (d=1; d<=ndims; d++) {
            # Build arrays of paired (x=dim_score, y=ftp) excluding ftp==-1
            n = 0; sx=0; sy=0; sxx=0; syy=0; sxy=0
            for (i=1; i<=total_rows; i++) {
                if (ftp[i] == -1) continue
                x = dim_scores[d "_" i]; y = ftp[i]
                n++; sx+=x; sy+=y; sxx+=x*x; syy+=y*y; sxy+=x*y
            }
            if (n < 2) { r = 0 }
            else {
                num = n*sxy - sx*sy
                den = sqrt((n*sxx - sx*sx) * (n*syy - sy*sy))
                r = (den > 0) ? num/den : 0
            }
            pearson_r[d] = r
            if (r > max_r) { max_r = r; max_dim = d }
            tag = (d == max_dim) ? "    *** highest predictor" : ""
            printf "    %-44s r=%+.2f%s\n", dimnames[d], r, tag
        }

        printf "\n"

        # --- Scope-creep by aggregate-score quartile ---
        # Collect rows with valid scope_creep
        m = 0
        for (i=1; i<=total_rows; i++) {
            if (scope_creep[i] != -1) {
                sc_vals[++m] = scope_creep[i]
                sc_agg[m] = agg[i]
            }
        }

        if (m >= 4) {
            printf "  Scope-creep by aggregate-score quartile:\n"
            # Sort agg values to find quartile boundaries (bubble sort, small n)
            for (i=1; i<=m; i++) sorted_agg[i] = sc_agg[i]
            for (i=1; i<=m; i++)
                for (j=i+1; j<=m; j++)
                    if (sorted_agg[j] < sorted_agg[i]) {
                        t=sorted_agg[i]; sorted_agg[i]=sorted_agg[j]; sorted_agg[j]=t
                    }
            q1_max = sorted_agg[int(m/4)]
            q2_max = sorted_agg[int(m/2)]
            q3_max = sorted_agg[int(3*m/4)]

            for (q=1; q<=4; q++) { qsum[q]=0; qcnt[q]=0 }
            for (i=1; i<=m; i++) {
                a = sc_agg[i]
                if      (a <= q1_max) q=1
                else if (a <= q2_max) q=2
                else if (a <= q3_max) q=3
                else                  q=4
                qsum[q] += sc_vals[i]; qcnt[q]++
            }
            qnames[1]="Q1 (lowest scores) "; qnames[2]="Q2                 "
            qnames[3]="Q3                 "; qnames[4]="Q4 (highest scores)"
            for (q=1; q<=4; q++) {
                mean_sc = (qcnt[q]>0) ? qsum[q]/qcnt[q] : 0
                printf "    %s: mean creep %.1f\303\227  (n=%d)\n", qnames[q], mean_sc, qcnt[q]
            }
            printf "\n"
        }

        # --- Threshold → first-try-pass rate ---
        threshold = 0.75
        below_n=0; below_pass=0; above_n=0; above_pass=0
        for (i=1; i<=total_rows; i++) {
            if (ftp[i] == -1) continue
            if (agg[i] < threshold) {
                below_n++; if (ftp[i]==1) below_pass++
            } else {
                above_n++; if (ftp[i]==1) above_pass++
            }
        }
        printf "  Below threshold (<%.2f) \342\206\222 first-try-pass rate:  %s\n",
            threshold, (below_n>0 ? sprintf("%d%%", int(below_pass*100/below_n)) : "n/a")
        printf "  At or above threshold     \342\206\222 first-try-pass rate:  %s\n",
            (above_n>0 ? sprintf("%d%%", int(above_pass*100/above_n)) : "n/a")
    }
    ' "$tmp_joined")"

    printf '%s\n' "$report"
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
            show)       cmd_show "$@" ;;
            history)    cmd_history "$@" ;;
            override)   cmd_override "$@" ;;
            correlate)  cmd_correlate "$@" ;;
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
    correlate)
        cmd_correlate "$@"
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
