#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: model-routing.sh — CLI for the model-routing eval harness (Phase 2).
#
# Subcommands (Phase 2):
#   yakos model-routing eval <agent-id> [--cases <glob>] [--judge <agent>]
#                                        [--max-cost-usd <n>] [--project <path>]
#   yakos model-routing list
#   yakos model-routing show <agent-id>
#
# Phase 3 stubs (not yet implemented):
#   yakos model-routing promote <agent-id> [--tier <tier>]
#   yakos model-routing reject  <agent-id> [--reason "<text>"]
#   yakos model-routing history [<agent-id>]

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos model-routing'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos model-routing'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

YAKOS_STATE_DIR="${HOME}/.yakos-state"
MR_EVAL_LOG="${YAKOS_MR_EVAL_LOG:-${YAKOS_STATE_DIR}/model-routing-eval-log.ndjson}"
MR_CANDIDATES="${YAKOS_MR_CANDIDATES:-${YAKOS_STATE_DIR}/model-routing-candidates.ndjson}"

# ---- settings helpers -------------------------------------------------------

_mr_setting() {
    # _mr_setting <key> <default>
    local key="$1" default="$2"
    local settings="${YAKOS_STATE_DIR}/settings.json"
    if [ -f "$settings" ] && command -v jq >/dev/null 2>&1; then
        val="$(jq -r --arg k "$key" '.model_routing[$k] // empty' "$settings" 2>/dev/null || true)"
        [ -n "$val" ] && printf '%s' "$val" && return
    fi
    printf '%s' "$default"
}

_mr_epsilon()           { _mr_setting epsilon_pass_rate        0.05; }
_mr_min_cases()         { _mr_setting min_cases_for_eval       5;    }
_mr_min_cases_conf()    { _mr_setting min_cases_for_confidence 12;   }
_mr_max_cost()          { _mr_setting max_eval_run_cost_usd    5.00; }
_mr_weekly_max_cost()   { _mr_setting weekly_max_cost_usd      50.00;}

# ---- Wilson 95% CI lower bound (pure awk) -----------------------------------
#
# wilson_lower <k> <n>
#   k = passing cases, n = total cases
#   Returns the lower bound of the 95% Wilson score interval.
#   Formula:
#     phat   = k/n
#     z      = 1.96
#     denom  = 1 + z^2/n
#     center = (phat + z^2/(2n)) / denom
#     margin = z * sqrt(phat*(1-phat)/n + z^2/(4*n*n)) / denom
#     lower  = center - margin
wilson_lower() {
    local k="$1" n="$2"
    awk -v k="$k" -v n="$n" 'BEGIN {
        if (n == 0) { printf "0"; exit }
        phat   = k / n
        z      = 1.96
        denom  = 1 + z*z/n
        center = (phat + z*z/(2*n)) / denom
        var    = phat*(1-phat)/n + z*z/(4*n*n)
        if (var < 0) var = 0
        margin = z * sqrt(var) / denom
        lower  = center - margin
        if (lower < 0) lower = 0
        printf "%.6f", lower
    }'
}

# ---- run_id generator -------------------------------------------------------

_mr_gen_run_id() {
    local ts hex
    ts="$(date +%s)"
    # Portable random hex: od with /dev/urandom; fallback to $$+RANDOM.
    hex="$(od -An -tx1 -N4 /dev/urandom 2>/dev/null | tr -d ' \n' || printf '%08x' "$$")"
    printf 'yakos-mr-%s-%s' "$ts" "$hex"
}

# ---- find agent file + read model frontmatter --------------------------------

_mr_find_agent() {
    local id="$1" project="${2:-}"
    # Search project override first, then framework.
    local dirs=""
    [ -n "$project" ] && dirs="$project/.claude/agents"
    dirs="${dirs:+$dirs }$YAKOS_ROOT/lib/agents"
    for d in $dirs; do
        [ -d "$d" ] || continue
        local f="$d/${id}.md"
        [ -f "$f" ] && { printf '%s' "$f"; return 0; }
    done
    return 1
}

_mr_agent_model() {
    # Read `model:` frontmatter from an agent file.
    local f="$1"
    awk '
        BEGIN { in_fm=0 }
        NR==1 && /^---[[:space:]]*$/ { in_fm=1; next }
        in_fm && /^---[[:space:]]*$/  { exit }
        in_fm && /^model:[[:space:]]/  { sub(/^model:[[:space:]]*/,""); print; exit }
    ' "$f"
}

_mr_agent_model_policy() {
    local f="$1"
    awk '
        BEGIN { in_fm=0 }
        NR==1 && /^---[[:space:]]*$/ { in_fm=1; next }
        in_fm && /^---[[:space:]]*$/  { exit }
        in_fm && /^model-policy:[[:space:]]/ { sub(/^model-policy:[[:space:]]*/,""); print; exit }
    ' "$f"
}

_mr_agent_domain() {
    local f="$1"
    awk '
        BEGIN { in_fm=0 }
        NR==1 && /^---[[:space:]]*$/ { in_fm=1; next }
        in_fm && /^---[[:space:]]*$/  { exit }
        in_fm && /^domain:[[:space:]]/ { sub(/^domain:[[:space:]]*/,""); print; exit }
    ' "$f"
}

# ---- default judge selection ------------------------------------------------

_mr_default_judge() {
    local domain="$1"
    case "$domain" in
        backend|frontend|mobile|database|testing) printf 'code-reviewer' ;;
        cross-cutting|design)                     printf 'architect'      ;;
        *)                                        printf 'code-reviewer'  ;;
    esac
}

# ---- log helpers -------------------------------------------------------------

_mr_log_write() {
    mkdir -p "$YAKOS_STATE_DIR" 2>/dev/null || true
    ct_rotate_log "$MR_EVAL_LOG" 2>/dev/null || true
    printf '%s\n' "$1" >> "$MR_EVAL_LOG" 2>/dev/null || true
}

_mr_candidate_write() {
    mkdir -p "$YAKOS_STATE_DIR" 2>/dev/null || true
    ct_rotate_log "$MR_CANDIDATES" 2>/dev/null || true
    printf '%s\n' "$1" >> "$MR_CANDIDATES" 2>/dev/null || true
}

# ---- weekly cost guard -------------------------------------------------------

_mr_check_weekly_budget() {
    local weekly_max
    weekly_max="$(_mr_weekly_max_cost)"
    # Sum total_cost_usd from eval_case records in the past 7 days.
    [ -f "$MR_EVAL_LOG" ] || return 0
    local cutoff
    cutoff="$(date -u -v-7d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
              || date -u -d '7 days ago' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
              || echo "1970-01-01T00:00:00Z")"
    local spent
    spent="$(jq -r --arg cutoff "$cutoff" \
        'select(.type == "eval_case" and .ts >= $cutoff) | .total_cost_usd // 0' \
        "$MR_EVAL_LOG" 2>/dev/null \
        | awk '{s+=$1} END {printf "%.4f", s+0}')"
    if awk -v s="$spent" -v m="$weekly_max" 'BEGIN{exit !(s+0 >= m+0)}'; then
        ct_die "model-routing: weekly eval spend \$$spent >= cap \$$weekly_max; cannot start run"
    fi
}

# ---- dispatch with mock support ---------------------------------------------
# If YAKOS_MR_MOCK_DISPATCH is set, it points to a directory of mock response
# files: <dir>/<agent_id>/<tier>.json  containing
#   {"stdout":"...","cost":0.003,"duration_s":2,"input_tokens":100,"output_tokens":50}
# Used exclusively by the test suite; never set in production.

_mr_dispatch_case() {
    local subject="$1" task="$2" tier="$3" run_id="$4" project="$5"
    local response_file cost duration_s input_tokens output_tokens

    if [ -n "${YAKOS_MR_MOCK_DISPATCH:-}" ]; then
        # Test-mode: read from mock directory.
        local mock_f="${YAKOS_MR_MOCK_DISPATCH}/${subject}/${tier}.json"
        if [ ! -f "$mock_f" ]; then
            # Fall back to a generic default response.
            mock_f="${YAKOS_MR_MOCK_DISPATCH}/default.json"
        fi
        if [ -f "$mock_f" ]; then
            _MR_DISPATCH_STDOUT="$(jq -r '.stdout // "mock response"' "$mock_f")"
            _MR_DISPATCH_COST="$(jq -r '.cost // 0.003' "$mock_f")"
            _MR_DISPATCH_DURATION="$(jq -r '.duration_s // 1' "$mock_f")"
            _MR_DISPATCH_IN_TOK="$(jq -r '.input_tokens // 100' "$mock_f")"
            _MR_DISPATCH_OUT_TOK="$(jq -r '.output_tokens // 50' "$mock_f")"
        else
            _MR_DISPATCH_STDOUT="mock response (no fixture)"
            _MR_DISPATCH_COST="0.001"
            _MR_DISPATCH_DURATION="1"
            _MR_DISPATCH_IN_TOK="50"
            _MR_DISPATCH_OUT_TOK="25"
        fi
        return 0
    fi

    # Real dispatch path.
    local dispatch_log="${YAKOS_STATE_DIR}/dispatch-log.ndjson"
    local out_tmp
    out_tmp="$(mktemp -t yakos-mr-dispatch.XXXXXX)"
    local project_flag=""
    [ -n "$project" ] && project_flag="--project $project"
    # shellcheck disable=SC2086
    bash "$YAKOS_LIB/dispatch.sh" "$subject" "$task" \
        --model "$tier" \
        --eval-run-id "$run_id" \
        $project_flag \
        > "$out_tmp" 2>&1 || true
    _MR_DISPATCH_STDOUT="$(cat "$out_tmp")"
    rm -f "$out_tmp" 2>/dev/null || true

    # Read telemetry from the dispatch-log's last matching finished record.
    _MR_DISPATCH_COST="0"
    _MR_DISPATCH_DURATION="0"
    _MR_DISPATCH_IN_TOK="0"
    _MR_DISPATCH_OUT_TOK="0"
    if [ -f "$dispatch_log" ]; then
        local last_entry
        last_entry="$(grep '"dispatch_finished"' "$dispatch_log" 2>/dev/null \
                      | grep "\"$run_id\"" | tail -1 || true)"
        if [ -n "$last_entry" ]; then
            _MR_DISPATCH_COST="$(printf '%s' "$last_entry" \
                | jq -r '.usage.total_cost_usd // .usage.cost_usd // 0' 2>/dev/null || echo 0)"
            _MR_DISPATCH_DURATION="$(printf '%s' "$last_entry" \
                | jq -r '.duration_s // 0' 2>/dev/null || echo 0)"
            _MR_DISPATCH_IN_TOK="$(printf '%s' "$last_entry" \
                | jq -r '.usage.input_tokens // .est_input_tokens // 0' 2>/dev/null || echo 0)"
            _MR_DISPATCH_OUT_TOK="$(printf '%s' "$last_entry" \
                | jq -r '.usage.output_tokens // .est_output_tokens // 0' 2>/dev/null || echo 0)"
        fi
    fi
}

# Judge dispatch: same mock/real split.
_mr_dispatch_judge() {
    local judge="$1" judge_input_json="$2" project="$3"

    if [ -n "${YAKOS_MR_MOCK_JUDGE:-}" ]; then
        # Test-mode: mock judge response file.
        local mock_f="${YAKOS_MR_MOCK_JUDGE}"
        if [ -f "$mock_f" ]; then
            _MR_JUDGE_STDOUT="$(cat "$mock_f")"
        else
            _MR_JUDGE_STDOUT='{"case_id":"mock","tier":"sonnet","composite":0.9,"criteria_scores":[{"name":"mock","score":0.9}],"pass":true,"notes":"mock judge"}'
        fi
        return 0
    fi

    local out_tmp
    out_tmp="$(mktemp -t yakos-mr-judge.XXXXXX)"
    local project_flag=""
    [ -n "$project" ] && project_flag="--project $project"
    # shellcheck disable=SC2086
    bash "$YAKOS_LIB/dispatch.sh" "$judge" "$judge_input_json" \
        $project_flag \
        > "$out_tmp" 2>&1 || true
    _MR_JUDGE_STDOUT="$(cat "$out_tmp")"
    rm -f "$out_tmp" 2>/dev/null || true
}

# ---- sha256 of a file (for case_hash) ----------------------------------------

_mr_case_hash() {
    ct_sha256 "$1" 2>/dev/null || printf 'unknown'
}

# ---- subcommand: eval -------------------------------------------------------

cmd_eval() {
    local agent_id="" judge_override="" max_cost_override="" project=""
    local cases_glob="case-*.json"

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --judge)     shift; judge_override="$1" ;;
            --judge=*)   judge_override="${1#--judge=}" ;;
            --max-cost-usd) shift; max_cost_override="$1" ;;
            --max-cost-usd=*) max_cost_override="${1#--max-cost-usd=}" ;;
            --cases)     shift; cases_glob="$1" ;;
            --cases=*)   cases_glob="${1#--cases=}" ;;
            --project)   shift; project="$1" ;;
            --project=*) project="${1#--project=}" ;;
            --*) ct_die "model-routing eval: unknown flag '$1'" ;;
            *)
                [ -z "$agent_id" ] || ct_die "model-routing eval: unexpected argument '$1'"
                agent_id="$1"
                ;;
        esac
        shift
    done

    [ -n "$agent_id" ] || ct_die "model-routing eval: missing <agent-id>"

    # Resolve agent file and current model.
    local agent_file
    agent_file="$(_mr_find_agent "$agent_id" "$project")" \
        || ct_die "model-routing eval: agent '$agent_id' not found"

    local current_model
    current_model="$(_mr_agent_model_policy "$agent_file")"
    [ -z "$current_model" ] && current_model="$(_mr_agent_model "$agent_file")"
    [ -z "$current_model" ] && current_model="sonnet"

    local domain
    domain="$(_mr_agent_domain "$agent_file")"

    # Resolve judge.
    local judge
    judge="${judge_override:-$(_mr_default_judge "$domain")}"

    # === ANTI-SELF-CONGRATULATION HARD GUARD (line ~225) =====================
    # Refuse if judge == subject. No --force override exists.
    if [ "$judge" = "$agent_id" ]; then
        ct_die "model-routing eval: judge and subject are the same agent ('$agent_id'); self-evaluation is forbidden. Use --judge <different-agent>."
    fi
    # =========================================================================

    # Settings.
    local epsilon min_cases min_cases_conf max_cost
    epsilon="$(_mr_epsilon)"
    min_cases="$(_mr_min_cases)"
    min_cases_conf="$(_mr_min_cases_conf)"
    max_cost="${max_cost_override:-$(_mr_max_cost)}"

    # Weekly budget check.
    _mr_check_weekly_budget

    # Locate eval dir.
    local eval_dir=""
    local agent_file_dir
    agent_file_dir="$(dirname "$agent_file")"
    local agent_base
    agent_base="$(basename "$agent_file" .md)"
    if [ -d "${agent_file_dir}/${agent_base}/eval" ]; then
        eval_dir="${agent_file_dir}/${agent_base}/eval"
    elif [ -d "${agent_file_dir}/eval" ]; then
        eval_dir="${agent_file_dir}/eval"
    fi

    [ -n "$eval_dir" ] || ct_die "model-routing eval: no eval/ directory found for agent '$agent_id'"

    # Count valid cases.
    local case_files n_cases
    case_files="$(find "$eval_dir" -maxdepth 1 -name "$cases_glob" -type f 2>/dev/null | sort)"
    n_cases="$(printf '%s\n' "$case_files" | grep -c '.' 2>/dev/null || echo 0)"

    if [ "$n_cases" -lt "$min_cases" ]; then
        local refused_rec
        refused_rec="$(jq -cn \
            --arg ts "$(ct_iso_now_z)" \
            --arg agent "$agent_id" \
            --arg reason "min_cases" \
            --argjson n "$n_cases" \
            --argjson req "$min_cases" \
            '{type:"candidate_refused",ts:$ts,agent:$agent,reason:$reason,
              n_cases:$n,min_required:$req}')"
        _mr_log_write "$refused_rec"
        ct_die "model-routing eval: agent '$agent_id' has $n_cases case(s); minimum is $min_cases"
    fi

    local run_id
    run_id="$(_mr_gen_run_id)"
    local tiers="haiku sonnet opus"

    # Emit eval_run_started.
    local started_rec
    started_rec="$(jq -cn \
        --arg ts "$(ct_iso_now_z)" \
        --arg run_id "$run_id" \
        --arg agent "$agent_id" \
        --argjson n_cases "$n_cases" \
        --arg judge "$judge" \
        --argjson epsilon "$epsilon" \
        --argjson max_cost "$max_cost" \
        '{type:"eval_run_started",ts:$ts,run_id:$run_id,agent:$agent,
          n_cases:$n_cases,tiers:["haiku","sonnet","opus"],
          judge:$judge,epsilon:$epsilon,max_cost_usd:$max_cost}')"
    _mr_log_write "$started_rec"

    echo "model-routing eval: $agent_id  run=$run_id"
    echo "  cases=$n_cases  judge=$judge  budget=\$0/\$$max_cost"
    echo

    # Per-tier accumulators.
    local haiku_pass=0 haiku_total=0 haiku_cost=0
    local sonnet_pass=0 sonnet_total=0 sonnet_cost=0
    local opus_pass=0 opus_total=0 opus_cost=0
    local total_spent=0
    local budget_hit=0

    for case_file in $case_files; do
        [ -f "$case_file" ] || continue
        local case_id task
        case_id="$(jq -r '.case_id' "$case_file")"
        task="$(jq -r '.task' "$case_file")"
        local case_hash
        case_hash="$(_mr_case_hash "$case_file")"
        local expected_json rubric_json
        expected_json="$(jq -c '.expected_outcomes' "$case_file")"
        rubric_json="$(jq -c '.rubric' "$case_file")"

        for tier in haiku sonnet opus; do
            if [ "$budget_hit" -eq 1 ]; then break 2; fi

            # Dispatch subject.
            _mr_dispatch_case "$agent_id" "$task" "$tier" "$run_id" "$project"
            local response="$_MR_DISPATCH_STDOUT"
            local case_cost="$_MR_DISPATCH_COST"
            local case_dur="$_MR_DISPATCH_DURATION"
            local in_tok="$_MR_DISPATCH_IN_TOK"
            local out_tok="$_MR_DISPATCH_OUT_TOK"

            # Build judge input.
            local judge_input
            judge_input="$(jq -cn \
                --arg case_id "$case_id" \
                --arg task "$task" \
                --argjson expected "$expected_json" \
                --argjson rubric "$rubric_json" \
                --arg response "$response" \
                --argjson dur "$case_dur" \
                --argjson cost "$case_cost" \
                '{case_id:$case_id,task:$task,expected_outcomes:$expected,
                  rubric:$rubric,agent_response:$response,
                  duration_s:$dur,actual_cost_usd:$cost}')"

            # Dispatch judge.
            _mr_dispatch_judge "$judge" "$judge_input" "$project"
            local judge_out="$_MR_JUDGE_STDOUT"

            # Parse judge result.
            local passed rubric_scores judge_notes
            passed="false"
            rubric_scores="[]"
            judge_notes=""
            if printf '%s' "$judge_out" | jq -e '.pass' >/dev/null 2>&1; then
                passed="$(printf '%s' "$judge_out" | jq -r '.pass')"
                rubric_scores="$(printf '%s' "$judge_out" | jq -c '.criteria_scores // []')"
                judge_notes="$(printf '%s' "$judge_out" | jq -r '.notes // ""')"
            fi

            # Emit eval_case.
            local case_rec
            case_rec="$(jq -cn \
                --arg ts "$(ct_iso_now_z)" \
                --arg run_id "$run_id" \
                --arg agent "$agent_id" \
                --arg case_id "$case_id" \
                --arg case_hash "$case_hash" \
                --arg tier "$tier" \
                --argjson pass "$([ "$passed" = "true" ] && echo true || echo false)" \
                --argjson rubric_scores "$rubric_scores" \
                --argjson cost "$case_cost" \
                --argjson dur "$case_dur" \
                --argjson in_tok "$in_tok" \
                --argjson out_tok "$out_tok" \
                --arg judge "$judge" \
                --arg notes "$judge_notes" \
                '{type:"eval_case",ts:$ts,run_id:$run_id,agent:$agent,
                  case_id:$case_id,case_hash:$case_hash,tier:$tier,
                  pass:$pass,rubric_scores:$rubric_scores,
                  total_cost_usd:$cost,duration_s:$dur,
                  usage:{input_tokens:$in_tok,output_tokens:$out_tok},
                  judge:$judge,judge_notes:$notes}')"
            _mr_log_write "$case_rec"

            # Accumulate per-tier stats.
            case "$tier" in
                haiku)
                    haiku_total=$((haiku_total + 1))
                    [ "$passed" = "true" ] && haiku_pass=$((haiku_pass + 1)) || true
                    haiku_cost="$(awk -v a="$haiku_cost" -v b="$case_cost" 'BEGIN{printf "%.6f",a+b}')"
                    ;;
                sonnet)
                    sonnet_total=$((sonnet_total + 1))
                    [ "$passed" = "true" ] && sonnet_pass=$((sonnet_pass + 1)) || true
                    sonnet_cost="$(awk -v a="$sonnet_cost" -v b="$case_cost" 'BEGIN{printf "%.6f",a+b}')"
                    ;;
                opus)
                    opus_total=$((opus_total + 1))
                    [ "$passed" = "true" ] && opus_pass=$((opus_pass + 1)) || true
                    opus_cost="$(awk -v a="$opus_cost" -v b="$case_cost" 'BEGIN{printf "%.6f",a+b}')"
                    ;;
            esac

            total_spent="$(awk -v a="$total_spent" -v b="$case_cost" 'BEGIN{printf "%.6f",a+b}')"

            # Cost cap check.
            if awk -v s="$total_spent" -v m="$max_cost" 'BEGIN{exit !(s+0 > m+0)}'; then
                local budget_rec
                budget_rec="$(jq -cn \
                    --arg ts "$(ct_iso_now_z)" \
                    --arg run_id "$run_id" \
                    --arg agent "$agent_id" \
                    --argjson spent "$total_spent" \
                    --argjson cap "$max_cost" \
                    '{type:"budget_exceeded",ts:$ts,run_id:$run_id,
                      agent:$agent,spent_usd:$spent,cap_usd:$cap}')"
                _mr_log_write "$budget_rec"
                echo "  WARN: budget cap \$$max_cost exceeded after \$$total_spent spent; aborting run"
                budget_hit=1
                break
            fi
        done
    done

    # --- Compute pass-rates and Wilson CI lower bounds -----------------------

    local haiku_rate sonnet_rate opus_rate
    haiku_rate="$(awk -v p="$haiku_pass" -v t="$haiku_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",p/t}}')"
    sonnet_rate="$(awk -v p="$sonnet_pass" -v t="$sonnet_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",p/t}}')"
    opus_rate="$(awk -v p="$opus_pass" -v t="$opus_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",p/t}}')"

    local haiku_ci sonnet_ci opus_ci
    haiku_ci="$(wilson_lower "$haiku_pass" "$haiku_total")"
    sonnet_ci="$(wilson_lower "$sonnet_pass" "$sonnet_total")"
    opus_ci="$(wilson_lower "$opus_pass" "$opus_total")"

    local haiku_mean_cost sonnet_mean_cost opus_mean_cost
    haiku_mean_cost="$(awk -v c="$haiku_cost" -v t="$haiku_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",c/t}}')"
    sonnet_mean_cost="$(awk -v c="$sonnet_cost" -v t="$sonnet_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",c/t}}')"
    opus_mean_cost="$(awk -v c="$opus_cost" -v t="$opus_total" \
        'BEGIN{if(t==0){printf "0"}else{printf "%.6f",c/t}}')"

    # --- Promotion decision --------------------------------------------------
    # current_model is the tier we compare against. Check cheaper tiers.
    local candidate_tier="" candidate_reason="" candidate_emitted=false

    _tier_cheaper_than() {
        # Returns 0 if $1 is cheaper than $2 (haiku < sonnet < opus).
        local a="$1" b="$2"
        local va vb
        case "$a" in haiku) va=1 ;; sonnet) va=2 ;; opus) va=3 ;; *) return 1 ;; esac
        case "$b" in haiku) vb=1 ;; sonnet) vb=2 ;; opus) vb=3 ;; *) return 1 ;; esac
        [ "$va" -lt "$vb" ]
    }

    _current_rate_ci() {
        # Pass-rate and CI for the current model tier.
        case "$current_model" in
            haiku)  printf '%s %s' "$haiku_rate"  "$haiku_ci"  ;;
            sonnet) printf '%s %s' "$sonnet_rate" "$sonnet_ci" ;;
            opus)   printf '%s %s' "$opus_rate"   "$opus_ci"   ;;
        esac
    }

    _candidate_rate_ci_cost() {
        # Pass-rate, CI lower, mean cost for a candidate tier.
        case "$1" in
            haiku)  printf '%s %s %s' "$haiku_rate"  "$haiku_ci"  "$haiku_mean_cost"  ;;
            sonnet) printf '%s %s %s' "$sonnet_rate" "$sonnet_ci" "$sonnet_mean_cost" ;;
            opus)   printf '%s %s %s' "$opus_rate"   "$opus_ci"   "$opus_mean_cost"   ;;
        esac
    }

    # Evaluate sonnet then haiku (prefer cheapest that qualifies).
    local cur_rate
    cur_rate="$(printf '%s' "$(_current_rate_ci)" | awk '{print $1}')"
    local cur_mean_cost
    case "$current_model" in
        haiku)  cur_mean_cost="$haiku_mean_cost"  ;;
        sonnet) cur_mean_cost="$sonnet_mean_cost" ;;
        opus)   cur_mean_cost="$opus_mean_cost"   ;;
    esac

    for cand_tier in haiku sonnet; do
        _tier_cheaper_than "$cand_tier" "$current_model" || continue
        local cand_info cand_rate cand_ci cand_cost
        cand_info="$(_candidate_rate_ci_cost "$cand_tier")"
        cand_rate="$(printf '%s' "$cand_info" | awk '{print $1}')"
        cand_ci="$(printf '%s' "$cand_info"   | awk '{print $2}')"
        cand_cost="$(printf '%s' "$cand_info"  | awk '{print $3}')"

        local n_run
        case "$cand_tier" in
            haiku)  n_run="$haiku_total"  ;;
            sonnet) n_run="$sonnet_total" ;;
            opus)   n_run="$opus_total"   ;;
        esac

        if [ "$n_run" -ge "$min_cases_conf" ]; then
            # CI-only gate.
            local threshold
            threshold="$(awk -v r="$cur_rate" -v e="$epsilon" \
                'BEGIN{printf "%.6f", r - e}')"
            if awk -v ci="$cand_ci" -v thr="$threshold" \
               'BEGIN{exit !(ci+0 >= thr+0)}'; then
                candidate_tier="$cand_tier"
                candidate_reason="ci_lower[${cand_tier}]=${cand_ci} >= pass_rate[${current_model}]=${cur_rate} - epsilon=${epsilon}"
                break
            else
                local refused_rec
                refused_rec="$(jq -cn \
                    --arg ts "$(ct_iso_now_z)" \
                    --arg run_id "$run_id" \
                    --arg agent "$agent_id" \
                    --arg reason "low_confidence" \
                    --arg detail "ci_lower[${cand_tier}]=${cand_ci} < pass_rate[${current_model}]=${cur_rate} - epsilon=${epsilon}" \
                    '{type:"candidate_refused",ts:$ts,run_id:$run_id,agent:$agent,
                      reason:$reason,detail:$detail}')"
                _mr_log_write "$refused_rec"
            fi
        else
            # Strict floor: need >= 2x cost saving AND >= 0.10 pass-rate margin.
            local cost_ok margin_ok
            cost_ok="$(awk -v cand="$cand_cost" -v cur="$cur_mean_cost" \
                'BEGIN{if(cur+0 > 0 && cand+0 <= cur/2){print "yes"}else{print "no"}}')"
            margin_ok="$(awk -v cand="$cand_rate" -v cur="$cur_rate" \
                'BEGIN{if(cand+0 >= cur+0 + 0.10){print "yes"}else{print "no"}}')"

            if [ "$cost_ok" = "yes" ] && [ "$margin_ok" = "yes" ]; then
                candidate_tier="$cand_tier"
                candidate_reason="strict_floor: cost_saving=${cand_cost}<=${cur_mean_cost}/2 AND margin=${cand_rate}>=${cur_rate}+0.10"
                break
            else
                local refused_reason="cost_only_no_quality"
                [ "$cost_ok" = "yes" ] && refused_reason="cost_only_no_quality" || refused_reason="insufficient_cost_saving"
                local refused_rec
                refused_rec="$(jq -cn \
                    --arg ts "$(ct_iso_now_z)" \
                    --arg run_id "$run_id" \
                    --arg agent "$agent_id" \
                    --arg reason "$refused_reason" \
                    --arg detail "cost_ok=${cost_ok} margin_ok=${margin_ok} n_run=${n_run}" \
                    '{type:"candidate_refused",ts:$ts,run_id:$run_id,agent:$agent,
                      reason:$reason,detail:$detail}')"
                _mr_log_write "$refused_rec"
            fi
        fi
    done

    [ -n "$candidate_tier" ] && candidate_emitted=true || candidate_emitted=false

    # Emit eval_run_finished.
    local finished_rec
    finished_rec="$(jq -cn \
        --arg ts "$(ct_iso_now_z)" \
        --arg run_id "$run_id" \
        --arg agent "$agent_id" \
        --argjson haiku_r "$haiku_rate" \
        --argjson sonnet_r "$sonnet_rate" \
        --argjson opus_r "$opus_rate" \
        --argjson haiku_c "$haiku_mean_cost" \
        --argjson sonnet_c "$sonnet_mean_cost" \
        --argjson opus_c "$opus_mean_cost" \
        --argjson haiku_ci "$haiku_ci" \
        --argjson sonnet_ci "$sonnet_ci" \
        --argjson opus_ci "$opus_ci" \
        --argjson candidate_emitted "$candidate_emitted" \
        --arg candidate_tier "${candidate_tier:-null}" \
        --arg candidate_reason "${candidate_reason:-}" \
        '{type:"eval_run_finished",ts:$ts,run_id:$run_id,agent:$agent,
          tier_pass_rates:{haiku:$haiku_r,sonnet:$sonnet_r,opus:$opus_r},
          tier_mean_costs:{haiku:$haiku_c,sonnet:$sonnet_c,opus:$opus_c},
          tier_ci_lower:{haiku:$haiku_ci,sonnet:$sonnet_ci,opus:$opus_ci},
          candidate_emitted:$candidate_emitted,
          candidate_tier:(if $candidate_tier == "null" then null else $candidate_tier end),
          candidate_reason:$candidate_reason}')"
    _mr_log_write "$finished_rec"

    # Emit candidate record.
    if $candidate_emitted; then
        local savings
        savings="$(awk -v cand_cost="$opus_mean_cost" -v cur_cost="$cur_mean_cost" \
            'BEGIN{diff=cur_cost-cand_cost; if(diff<0)diff=0; printf "%.2f", diff*1000}')"
        # Use actual candidate cost for savings.
        case "$candidate_tier" in
            haiku)  local ct_cost="$haiku_mean_cost" ;;
            sonnet) local ct_cost="$sonnet_mean_cost" ;;
            *) local ct_cost="0" ;;
        esac
        savings="$(awk -v cand_cost="$ct_cost" -v cur_cost="$cur_mean_cost" \
            'BEGIN{diff=cur_cost-cand_cost; if(diff<0)diff=0; printf "%.2f", diff*1000}')"
        local cand_rec
        cand_rec="$(jq -cn \
            --arg agent "$agent_id" \
            --arg current_model "$current_model" \
            --arg suggested_model "$candidate_tier" \
            --argjson haiku_r "$haiku_rate" \
            --argjson sonnet_r "$sonnet_rate" \
            --argjson opus_r "$opus_rate" \
            --argjson haiku_ci "$haiku_ci" \
            --argjson sonnet_ci "$sonnet_ci" \
            --argjson opus_ci "$opus_ci" \
            --argjson haiku_c "$haiku_mean_cost" \
            --argjson sonnet_c "$sonnet_mean_cost" \
            --argjson opus_c "$opus_mean_cost" \
            --argjson n_cases "$n_cases" \
            --arg run_id "$run_id" \
            --argjson epsilon "$epsilon" \
            --arg judge "$judge" \
            --argjson savings "$savings" \
            --arg generated_at "$(ct_iso_now_z)" \
            '{agent:$agent,current_model:$current_model,suggested_model:$suggested_model,
              evidence:{
                pass_rates:{haiku:$haiku_r,sonnet:$sonnet_r,opus:$opus_r},
                ci_lower:{haiku:$haiku_ci,sonnet:$sonnet_ci,opus:$opus_ci},
                mean_costs:{haiku:$haiku_c,sonnet:$sonnet_c,opus:$opus_c},
                n_cases:$n_cases,eval_run_id:$run_id,epsilon_used:$epsilon,judge:$judge},
              estimated_monthly_savings_usd:$savings,
              generated_at:$generated_at}')"
        _mr_candidate_write "$cand_rec"
    fi

    # Human summary.
    echo "  tier     pass-rate  ci-lower  mean-cost/case"
    printf '  %-8s   %5.1f%%    %5.1f%%    $%.4f\n' \
        "haiku"  "$(awk -v r="$haiku_rate"  'BEGIN{printf "%.1f",r*100}')" \
        "$(awk -v c="$haiku_ci"  'BEGIN{printf "%.1f",c*100}')" "$haiku_mean_cost"
    printf '  %-8s   %5.1f%%    %5.1f%%    $%.4f\n' \
        "sonnet" "$(awk -v r="$sonnet_rate" 'BEGIN{printf "%.1f",r*100}')" \
        "$(awk -v c="$sonnet_ci" 'BEGIN{printf "%.1f",c*100}')" "$sonnet_mean_cost"
    printf '  %-8s   %5.1f%%    %5.1f%%    $%.4f\n' \
        "opus"   "$(awk -v r="$opus_rate"   'BEGIN{printf "%.1f",r*100}')" \
        "$(awk -v c="$opus_ci"   'BEGIN{printf "%.1f",c*100}')" "$opus_mean_cost"
    echo
    if $candidate_emitted; then
        echo "  candidate: $candidate_tier  (savings ~\$$savings/mo)"
        echo "    reason: $candidate_reason"
    else
        echo "  candidate: refused"
        echo "    (see eval-log for details)"
    fi
    echo "  total spent: \$$total_spent"
}

# ---- subcommand: list --------------------------------------------------------

cmd_list() {
    if [ ! -f "$MR_CANDIDATES" ]; then
        echo "no candidates yet (run: yakos model-routing eval <agent>)"
        return 0
    fi

    # Latest-per-agent: for each agent, pick the record with max generated_at.
    jq -r '
        .agent + " " + (.current_model // "?") + " " + (.suggested_model // "?") + " " +
        ((.estimated_monthly_savings_usd // 0) | tostring) + " " +
        ((.evidence.n_cases // 0) | tostring) + " " +
        ((.evidence.ci_lower // {}) | to_entries | map(.key + "=" + (.value|tostring)) | join(",")) + " " +
        (.generated_at // "")
    ' "$MR_CANDIDATES" 2>/dev/null \
    | sort -t' ' -k1,1 -k8,8 \
    | awk '!seen[$1]++' \
    | while IFS=' ' read -r agent cur sug savings n ci_str gen_at; do
        printf '%-24s current=%-8s -> suggested=%-8s savings=~$%s/mo  n=%s  ci=[%s]\n' \
            "$agent" "$cur" "$sug" "$savings" "$n" "$ci_str"
    done

    local count
    count="$(jq -rs '[.[].agent] | unique | length' "$MR_CANDIDATES" 2>/dev/null || echo 0)"
    [ "$count" -gt 0 ] || echo "(no records)"
}

# ---- subcommand: show -------------------------------------------------------

cmd_show() {
    local agent_id="${1:-}"
    [ -n "$agent_id" ] || ct_die "model-routing show: missing <agent-id>"

    if [ ! -f "$MR_CANDIDATES" ]; then
        echo "no candidates on file"
        return 0
    fi

    # Latest candidate for this agent.
    local cand
    cand="$(jq -s --arg agent "$agent_id" \
        '[.[] | select(.agent == $agent)] | sort_by(.generated_at) | last // empty' \
        "$MR_CANDIDATES" 2>/dev/null || true)"

    if [ -z "$cand" ] || [ "$cand" = "null" ]; then
        echo "no candidate for agent '$agent_id'"
        return 0
    fi

    echo "=== candidate: $agent_id ==="
    printf '%s\n' "$cand" | jq '.'
    echo

    # Last 3 eval run records.
    echo "=== last 3 eval runs for $agent_id ==="
    if [ -f "$MR_EVAL_LOG" ]; then
        jq -s --arg agent "$agent_id" \
            '[.[] | select(.type == "eval_run_finished" and .agent == $agent)] | sort_by(.ts) | .[-3:][]' \
            "$MR_EVAL_LOG" 2>/dev/null | jq -c '.' || echo "(no eval_run_finished records found)"
    else
        echo "(eval log not found)"
    fi
}

# ---- Phase 3 state paths ----------------------------------------------------

MR_HISTORY="${YAKOS_MR_HISTORY:-${YAKOS_STATE_DIR}/model-routing-history.ndjson}"
MR_GRAVEYARD="${YAKOS_MR_GRAVEYARD:-${YAKOS_STATE_DIR}/model-routing-graveyard.ndjson}"
MR_BACKUPS_DIR="${YAKOS_MR_BACKUPS_DIR:-${YAKOS_STATE_DIR}/model-routing-backups}"

# ---- Phase 3 helpers --------------------------------------------------------

# _mr_find_agent_for_promote <agent-id> [--global]
# Resolves the agent file for promotion.
#   - project override first (cwd or $YAKOS_PROJECT_DIR)
#   - then framework lib/agents/
# Sets _MR_AGENT_FILE and _MR_AGENT_IS_FRAMEWORK (0=project, 1=framework).
_mr_find_agent_for_promote() {
    local id="$1" want_global="${2:-0}"
    local project_dir="${YAKOS_PROJECT_DIR:-$(pwd -P)}"

    # Try project override first.
    local project_agent=""
    for d in \
        "${project_dir}/.claude/agents" \
        "${YAKOS_PROJECT_DIR:-}/.claude/agents"
    do
        [ -d "$d" ] || continue
        [ -f "$d/${id}.md" ] || continue
        project_agent="$d/${id}.md"
        break
    done

    local framework_agent="${YAKOS_ROOT}/lib/agents/${id}.md"

    if [ -n "$project_agent" ]; then
        _MR_AGENT_FILE="$project_agent"
        _MR_AGENT_IS_FRAMEWORK=0
        return 0
    elif [ -f "$framework_agent" ]; then
        _MR_AGENT_FILE="$framework_agent"
        _MR_AGENT_IS_FRAMEWORK=1
        return 0
    fi
    return 1
}

# _mr_latest_candidate <agent-id>
# Prints the most recent candidate JSON line for the agent, or empty string.
_mr_latest_candidate() {
    local id="$1"
    [ -f "$MR_CANDIDATES" ] || { printf ''; return 0; }
    jq -s --arg agent "$id" \
        '[.[] | select(.agent == $agent)] | sort_by(.generated_at) | last // empty' \
        "$MR_CANDIDATES" 2>/dev/null || printf ''
}

# _mr_strip_candidate <agent-id>
# Filters all rows for agent from MR_CANDIDATES (latest-per-agent semantics).
_mr_strip_candidate() {
    local id="$1"
    [ -f "$MR_CANDIDATES" ] || return 0
    local tmp
    tmp="$(mktemp -t yakos-mr-strip.XXXXXX)"
    jq -c --arg agent "$id" 'select(.agent != $agent)' "$MR_CANDIDATES" > "$tmp" 2>/dev/null || true
    mv "$tmp" "$MR_CANDIDATES"
}

# _mr_rewrite_model_frontmatter <file> <new_model>
# Atomically rewrites the model: line in a YAML frontmatter block.
# Strategy: awk identifies the line within the ---...--- block and
# replaces only that line, preserving all other bytes byte-for-byte.
# Writes to a tempfile then mv over original (atomic).
_mr_rewrite_model_frontmatter() {
    local file="$1" new_model="$2"
    local tmp
    tmp="$(mktemp -t yakos-mr-rewrite.XXXXXX)"
    awk -v new_model="$new_model" '
        BEGIN { in_fm=0; done=0 }
        NR==1 && /^---[[:space:]]*$/ { in_fm=1; print; next }
        in_fm && /^---[[:space:]]*$/ { in_fm=0; done=1; print; next }
        in_fm && !done && /^model:[[:space:]]/ {
            print "model: " new_model
            next
        }
        { print }
    ' "$file" > "$tmp"
    mv "$tmp" "$file"
}

# _mr_graveyard_count <agent-id> <suggested-model>
# Count how many times (agent, suggested_model) appears in the graveyard.
_mr_graveyard_count() {
    local id="$1" suggested="$2"
    [ -f "$MR_GRAVEYARD" ] || { echo 0; return; }
    jq -r --arg agent "$id" --arg sug "$suggested" \
        'select(.agent == $agent and .suggested_model == $sug) | .agent' \
        "$MR_GRAVEYARD" 2>/dev/null | wc -l | tr -d ' '
}

# ---- subcommand: promote ----------------------------------------------------

cmd_promote() {
    local agent_id="" want_global=0

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --global) want_global=1 ;;
            --*) ct_die "model-routing promote: unknown flag '$1'" ;;
            *)
                [ -z "$agent_id" ] || ct_die "model-routing promote: unexpected argument '$1'"
                agent_id="$1"
                ;;
        esac
        shift
    done

    [ -n "$agent_id" ] || ct_die "model-routing promote: missing <agent-id>"

    # 1. Locate agent file.
    _mr_find_agent_for_promote "$agent_id" "$want_global" \
        || ct_die "model-routing promote: agent '$agent_id' not found in project or framework"

    local agent_file="$_MR_AGENT_FILE"
    local is_framework="$_MR_AGENT_IS_FRAMEWORK"

    # 2. Framework guard — require --global.
    if [ "$is_framework" -eq 1 ] && [ "$want_global" -eq 0 ]; then
        ct_die "model-routing promote: '$agent_id' is a framework-shipped agent.
Pass --global to promote a framework-shipped agent (rewrites lib/agents/${agent_id}.md under YAKOS_ROOT)."
    fi

    # 3. Find the most recent candidate.
    local cand
    cand="$(_mr_latest_candidate "$agent_id")"
    if [ -z "$cand" ] || [ "$cand" = "null" ]; then
        ct_die "model-routing promote: no candidate on file for '$agent_id' (run: yakos model-routing eval $agent_id)"
    fi

    local current_model suggested_model eval_run_id
    current_model="$(printf '%s' "$cand" | jq -r '.current_model // "unknown"')"
    suggested_model="$(printf '%s' "$cand" | jq -r '.suggested_model // "unknown"')"
    eval_run_id="$(printf '%s' "$cand" | jq -r '.evidence.eval_run_id // "unknown"')"

    # 4. Backup before write.
    mkdir -p "$MR_BACKUPS_DIR" 2>/dev/null || true
    local ts_safe
    ts_safe="$(date -u +%Y%m%dT%H%M%SZ)"
    local backup_file="${MR_BACKUPS_DIR}/${agent_id}-${ts_safe}.md"
    cp "$agent_file" "$backup_file"

    # 5. Atomic frontmatter rewrite (tempfile + mv).
    _mr_rewrite_model_frontmatter "$agent_file" "$suggested_model"

    # 6. Validate. If validation fails, restore from backup and exit non-zero.
    local validate_target
    if [ "$is_framework" -eq 1 ]; then
        validate_target="$YAKOS_ROOT"
    else
        validate_target="${YAKOS_PROJECT_DIR:-$(pwd -P)}"
    fi
    if ! bash "$YAKOS_LIB/validate.sh" --strict "$validate_target" >/dev/null 2>&1; then
        ct_log "model-routing promote: validate --strict failed; restoring backup"
        cp "$backup_file" "$agent_file"
        ct_die "model-routing promote: validation failed after rewrite; original restored from $backup_file"
    fi

    # 7. Append history entry.
    mkdir -p "$YAKOS_STATE_DIR" 2>/dev/null || true
    local history_rec
    history_rec="$(jq -cn \
        --arg ts "$(ct_iso_now_z)" \
        --arg agent "$agent_id" \
        --arg from_model "$current_model" \
        --arg to_model "$suggested_model" \
        --arg eval_run_id "$eval_run_id" \
        --arg by_user "$(id -un 2>/dev/null || echo unknown)" \
        '{ts:$ts,action:"promoted",agent:$agent,from_model:$from_model,
          to_model:$to_model,eval_run_id:$eval_run_id,by_user:$by_user}')"
    printf '%s\n' "$history_rec" >> "$MR_HISTORY"

    # 8. Strip candidate from candidates file.
    _mr_strip_candidate "$agent_id"

    echo "model-routing promote: $agent_id"
    echo "  $current_model -> $suggested_model"
    echo "  agent file: $agent_file"
    echo "  backup:     $backup_file"
}

# ---- subcommand: reject -----------------------------------------------------

cmd_reject() {
    local agent_id="" note="" force=0

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --note)   shift; note="$1" ;;
            --note=*) note="${1#--note=}" ;;
            --force)  force=1 ;;
            --*) ct_die "model-routing reject: unknown flag '$1'" ;;
            *)
                [ -z "$agent_id" ] || ct_die "model-routing reject: unexpected argument '$1'"
                agent_id="$1"
                ;;
        esac
        shift
    done

    [ -n "$agent_id" ] || ct_die "model-routing reject: missing <agent-id>"

    # Find the most recent candidate.
    local cand
    cand="$(_mr_latest_candidate "$agent_id")"
    if [ -z "$cand" ] || [ "$cand" = "null" ]; then
        ct_die "model-routing reject: no candidate on file for '$agent_id'"
    fi

    local suggested_model eval_run_id
    suggested_model="$(printf '%s' "$cand" | jq -r '.suggested_model // "unknown"')"
    eval_run_id="$(printf '%s' "$cand" | jq -r '.evidence.eval_run_id // "unknown"')"

    # Repeat-rejection guard: if (agent, suggested_model) rejected >= 3 times, warn+exit unless --force.
    local reject_count
    reject_count="$(_mr_graveyard_count "$agent_id" "$suggested_model")"
    if [ "$reject_count" -ge 3 ] && [ "$force" -eq 0 ]; then
        ct_die "model-routing reject: ($agent_id, $suggested_model) has been rejected $reject_count times already.
Pass --force to reject again despite the repeat-rejection guard."
    fi

    # Strip candidate from candidates file.
    _mr_strip_candidate "$agent_id"

    # Append to graveyard.
    mkdir -p "$YAKOS_STATE_DIR" 2>/dev/null || true
    local grave_rec
    grave_rec="$(jq -cn \
        --arg ts "$(ct_iso_now_z)" \
        --arg agent "$agent_id" \
        --arg suggested_model "$suggested_model" \
        --arg eval_run_id "$eval_run_id" \
        --arg reason "$note" \
        --arg by_user "$(id -un 2>/dev/null || echo unknown)" \
        '{ts:$ts,agent:$agent,suggested_model:$suggested_model,
          eval_run_id:$eval_run_id,reason:$reason,by_user:$by_user}')"
    printf '%s\n' "$grave_rec" >> "$MR_GRAVEYARD"

    echo "model-routing reject: $agent_id (suggested: $suggested_model)"
    [ -n "$note" ] && echo "  note: $note" || true
}

# ---- subcommand: history ----------------------------------------------------

cmd_history() {
    local filter_agent="${1:-}"

    # Read history (promoted) and graveyard (rejected) into one stream.
    local combined
    combined="$(
        {
            if [ -f "$MR_HISTORY" ]; then
                jq -c --arg f "$filter_agent" \
                    'if $f == "" then . else select(.agent == $f) end' \
                    "$MR_HISTORY" 2>/dev/null || true
            fi
            if [ -f "$MR_GRAVEYARD" ]; then
                # Normalize graveyard to match history shape for display.
                jq -c --arg f "$filter_agent" '
                    if $f == "" then . else select(.agent == $f) end
                    | {ts: .ts, action: "rejected", agent: .agent,
                       from_model: .suggested_model, to_model: "-",
                       reason: (.reason // ""), by_user: (.by_user // ""),
                       eval_run_id: (.eval_run_id // "")}
                ' "$MR_GRAVEYARD" 2>/dev/null || true
            fi
        } | sort -t'"' -k4,4 -r
    )"

    if [ -z "$combined" ]; then
        echo "(no history records found)"
        return 0
    fi

    printf '%-26s %-10s %-20s %-20s %-30s %s\n' \
        "ts" "action" "agent" "from_model -> to_model" "reason" "by_user"
    printf '%s\n' "$(printf '%.0s-' {1..110})" 2>/dev/null || \
        printf '%0.s-' 1 2 3 4 5 6 7 8 9 10 1 2 3 4 5 6 7 8 9 10 1 2 3 4 5 6 7 8 9 10 1 2 3 4 5 6 7 8 9 10 1 2 3 4 5 6 7 8 9 10 && printf '\n'

    printf '%s\n' "$combined" | while IFS= read -r line; do
        [ -n "$line" ] || continue
        local ts action agent from_model to_model reason by_user
        ts="$(printf '%s' "$line" | jq -r '.ts // ""')"
        action="$(printf '%s' "$line" | jq -r '.action // ""')"
        agent="$(printf '%s' "$line" | jq -r '.agent // ""')"
        from_model="$(printf '%s' "$line" | jq -r '.from_model // ""')"
        to_model="$(printf '%s' "$line" | jq -r '.to_model // ""')"
        reason="$(printf '%s' "$line" | jq -r '.reason // ""')"
        by_user="$(printf '%s' "$line" | jq -r '.by_user // ""')"
        printf '%-26s %-10s %-20s %-20s %-30s %s\n' \
            "$ts" "$action" "$agent" "${from_model} -> ${to_model}" \
            "${reason:0:29}" "$by_user"
    done
}

# ---- usage ------------------------------------------------------------------

usage() {
    cat <<'EOF'
yakos model-routing <subcommand> [args...]

Subcommands:
  eval <agent-id> [--judge <agent>] [--max-cost-usd <n>]
                  [--cases <glob>]  [--project <path>]
      Run a model-routing eval for <agent-id>.  Dispatches each eval/
      case at haiku/sonnet/opus, scores with a judge, computes Wilson
      95% CI bounds, and emits a candidate (or a refused-reason).
      Hard-refuses if judge == subject.

  list
      Show the latest candidate per agent (from
      ~/.yakos-state/model-routing-candidates.ndjson).

  show <agent-id>
      Full evidence for one agent's latest candidate + last 3 run records.

  promote <agent-id> [--global]
      Operator-gated: rewrite the agent's model: frontmatter to the
      suggested tier. Backs up the prior file; validates; records history.
      For framework-shipped agents (lib/agents/), --global is required.
      No --force option; promotion is always deliberate.

  reject <agent-id> [--note "<text>"] [--force]
      Discard the pending candidate.  Records reason in the graveyard.
      Repeat-rejection guard: exits 1 if (agent, model) rejected >= 3
      times already; --force bypasses the guard.

  history [<agent-id>]
      Show promotion + rejection history, sorted by ts descending.
      Optional <agent-id> filters to one agent.

Logs written to:
  ~/.yakos-state/model-routing-eval-log.ndjson
  ~/.yakos-state/model-routing-candidates.ndjson
  ~/.yakos-state/model-routing-history.ndjson
  ~/.yakos-state/model-routing-graveyard.ndjson
  ~/.yakos-state/model-routing-backups/<agent-id>-<ts>.md
EOF
}

# ---- dispatcher -------------------------------------------------------------

subcmd="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$subcmd" in
    eval)    cmd_eval    "$@" ;;
    list)    cmd_list    "$@" ;;
    show)    cmd_show    "$@" ;;
    promote) cmd_promote "$@" ;;
    reject)  cmd_reject  "$@" ;;
    history) cmd_history "$@" ;;
    -h|--help|help) usage ;;
    "")
        usage >&2
        ct_die "model-routing: missing subcommand"
        ;;
    *)
        usage >&2
        ct_die "model-routing: unknown subcommand '$subcmd'"
        ;;
esac
