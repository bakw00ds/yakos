#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervisor-stream.sh — PostToolUse hook that streams mutation events
# to the live supervisor (v0.34+).
#
# Design (Option 2 supervisor redesign):
#   Matcher in settings.json limits invocation to Edit|Write|MultiEdit|Bash —
#   reads never reach this hook. Within those mutation calls, a local
#   shell-only pre-filter runs FIRST:
#
#     Sensitive path   → file matches a deny-glob in .claude/path-allowlist.json
#     Large diff        → edit produced > supervisor.pre_filter.min_diff_lines
#     Out-of-scope edit → touched file not referenced in decisions.md / plan.md
#     Risk regex        → content matches a known dangerous-command pattern
#
#   NONE trip  → append to buffer and return (no LLM dispatch, ~0 cost).
#   ANY trip   → log trigger reason + proceed to score-dispatch path.
#
#   Only ESCALATIONS count toward score_every_n_calls.
#   When dispatch fires, it runs nohup … & disown (async, does NOT block
#   PostToolUse return).
#
# Model tier: reads supervisor.model from .yakos.yml (default: haiku).
# TODO: supervisor.runtime: local-llm → Ollama integration (future PR).
#
# Never blocks. Always exits 0. This is telemetry, not policy.
# Policy enforcement happens in supervisor-gate.sh (PreToolUse).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# Skip if supervisor mode disabled via env
if [ "${YAKOS_SUPERVISOR_DISABLE:-0}" = "1" ]; then
    exit 0
fi

# Skip if .yakos.yml says supervisor.enabled: false. Use awk to extract
# only the direct children of supervisor: (2-space indent), not nested
# keys. grep -A would match nested 'enabled: false' (e.g. pre_filter.enabled).
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
_supervisor_enabled() {
    # Extracts only the direct-child keys of supervisor: (those with exactly
    # 2-space indent). Returns "false" if enabled: false, "true" otherwise.
    awk '
        /^supervisor:[[:space:]]*$/ { in_s=1; next }
        in_s && /^[^[:space:]#]/ { exit }
        in_s && /^  [a-z_][a-z_]*:/ { print; next }
        in_s { next }
    ' "$1" 2>/dev/null | grep -E '^  enabled:[[:space:]]*false[[:space:]]*$'
}
if [ -f "$yakos_yml" ]; then
    if _supervisor_enabled "$yakos_yml" | grep -q .; then
        exit 0
    fi
fi

command -v jq >/dev/null 2>&1 || exit 0
command -v yakos_current_dir >/dev/null 2>&1 || exit 0

current_dir="$(yakos_current_dir)"
[ -d "$current_dir" ] || exit 0

buffer="$current_dir/supervisor-buffer.ndjson"
counter="$current_dir/.supervisor-counter"
findings="$current_dir/supervisor-findings.ndjson"

# --- Read config from .yakos.yml -------------------------------------------

# supervisor.pre_filter.enabled (default: true)
pre_filter_enabled="true"
if [ -f "$yakos_yml" ]; then
    pf="$(grep -A 30 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -A 10 '^[[:space:]]*pre_filter:' \
        | grep -E '^[[:space:]]*enabled:[[:space:]]*(true|false)' \
        | head -1 | awk '{print $2}' | tr -d '[:space:]')"
    [ "$pf" = "false" ] && pre_filter_enabled="false"
fi

# supervisor.pre_filter.min_diff_lines (default: 20)
min_diff_lines=20
if [ -f "$yakos_yml" ]; then
    mdl="$(grep -A 30 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -A 10 '^[[:space:]]*pre_filter:' \
        | grep -E '^[[:space:]]*min_diff_lines:[[:space:]]*[0-9]+' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    case "$mdl" in
        ''|*[!0-9]*) : ;;
        *) min_diff_lines="$mdl" ;;
    esac
fi

# supervisor.model (default: haiku)
sup_model="haiku"
if [ -f "$yakos_yml" ]; then
    m="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*model:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$m" ] && sup_model="$m"
fi

# --- 1. Append to buffer ----------------------------------------------------

agent="$(hi_sender_role)"
tool="$(hi_tool)"
file_path="$(hi_file_path)"
session_id="$(hi_session_id)"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Truncate previews so the buffer doesn't bloat
new_preview="$(hi_new_string 2>/dev/null | head -c 300 || true)"
content_preview="$(hi_content 2>/dev/null | head -c 300 || true)"

event="$(jq -nc \
    --arg ts "$ts" \
    --arg agent "$agent" \
    --arg tool "$tool" \
    --arg file "$file_path" \
    --arg new "$new_preview" \
    --arg content "$content_preview" \
    --arg sid "$session_id" \
    '{ts: $ts, agent: $agent, tool: $tool,
      input: {file_path: $file,
              new_preview: (if $new == "" then null else $new end),
              content_preview: (if $content == "" then null else $content end)},
      session_id: $sid}' 2>/dev/null)"

[ -n "$event" ] || exit 0
printf '%s\n' "$event" >> "$buffer" 2>/dev/null || exit 0

# Trim buffer to last 50 lines (rolling window)
if [ -f "$buffer" ]; then
    buf_lines="$(wc -l < "$buffer" 2>/dev/null | tr -d ' ')"
    if [ "${buf_lines:-0}" -gt 50 ]; then
        tmp="$buffer.tmp.$$"
        tail -n 50 "$buffer" > "$tmp" 2>/dev/null && mv "$tmp" "$buffer" 2>/dev/null
    fi
fi

# --- 2. Local pre-filter (shell-only, zero LLM cost) -----------------------

# When pre_filter is disabled, every mutation counts toward the score counter
# and behaves like pre-redesign (every Nth call dispatches regardless).
if [ "$pre_filter_enabled" = "false" ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "pre-filter disabled; counting toward score threshold" \
        "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
            '{pre_filter: "disabled", tool: $tool, file: $file}')"
    # Fall through to counter/dispatch logic below
else
    # Pre-filter is enabled — run deterministic triage.
    # ESCALATE accumulates the first trigger reason found.
    escalate_reason=""

    # 2a. Sensitive-path check: file matches a deny-glob in path-allowlist.json
    if [ -z "$escalate_reason" ] && [ -n "$file_path" ]; then
        allowlist_file="$project_dir/.claude/path-allowlist.json"
        if [ -f "$allowlist_file" ] && command -v jq >/dev/null 2>&1; then
            # Extract deny globs for the lead (the agent initiating the edit)
            deny_globs="$(jq -r '(.lead.deny // []) | .[]' "$allowlist_file" 2>/dev/null || true)"
            if [ -n "$deny_globs" ]; then
                while IFS= read -r glob; do
                    [ -n "$glob" ] || continue
                    # Shell glob match against the file path
                    # Use case for portability (no extglob needed for simple globs)
                    # shellcheck disable=SC2254  # intentional: glob patterns from policy config
                    case "$file_path" in
                        $glob) escalate_reason="sensitive-path:$glob"; break ;;
                    esac
                    # Also strip leading **/ for partial path matching
                    bare_glob="${glob#\*\*/}"
                    # shellcheck disable=SC2254  # intentional: glob patterns from policy config
                    case "$file_path" in
                        */$bare_glob|$bare_glob) escalate_reason="sensitive-path:$glob"; break ;;
                    esac
                done <<< "$deny_globs"
            fi
        fi
    fi

    # 2b. Diff-size check: new_string or content line count > min_diff_lines
    if [ -z "$escalate_reason" ]; then
        diff_text="${new_preview}${content_preview}"
        if [ -n "$diff_text" ]; then
            diff_lines="$(printf '%s' "$diff_text" | wc -l | tr -d ' ')"
            if [ "${diff_lines:-0}" -gt "$min_diff_lines" ]; then
                escalate_reason="large-diff:${diff_lines}-lines"
            fi
        fi
    fi

    # 2c. Out-of-scope edit: touched file not referenced in decisions.md or plan.md
    if [ -z "$escalate_reason" ] && [ -n "$file_path" ]; then
        decisions="$current_dir/decisions.md"
        plan="$current_dir/plan.md"
        # Only apply this check when the reference files actually exist
        if [ -f "$decisions" ] || [ -f "$plan" ]; then
            found_in_scope=0
            # Strip the project dir prefix for the lookup — the reference files
            # typically mention relative paths or basenames.
            file_basename="$(basename -- "$file_path")"
            file_rel="${file_path#$project_dir/}"
            for ref_file in "$decisions" "$plan"; do
                [ -f "$ref_file" ] || continue
                if grep -qF "$file_basename" "$ref_file" 2>/dev/null || \
                   grep -qF "$file_rel" "$ref_file" 2>/dev/null; then
                    found_in_scope=1
                    break
                fi
            done
            if [ "$found_in_scope" -eq 0 ]; then
                escalate_reason="out-of-scope:$file_basename"
            fi
        fi
    fi

    # 2d. Risk-regex check: content matches a dangerous-command pattern.
    # Default patterns; extendable via .yakos.yml supervisor.pre_filter.risk_regex list.
    if [ -z "$escalate_reason" ] && [ -n "${new_preview}${content_preview}" ]; then
        combined="${new_preview}${content_preview}"
        # Built-in default patterns (POSIX ERE for grep -E)
        default_patterns=(
            'drop[[:space:]]+table'
            'force.*push'
            'rm[[:space:]]+-rf'
            'chmod[[:space:]]+777'
            '(password|secret|api_key|token)[[:space:]]*=[[:space:]]*[^$({][^[:space:]]{8,}'
        )
        for pat in "${default_patterns[@]}"; do
            if printf '%s' "$combined" | grep -qiE "$pat" 2>/dev/null; then
                escalate_reason="risk-regex:$pat"
                break
            fi
        done

        # Additional patterns from .yakos.yml supervisor.pre_filter.risk_regex
        if [ -z "$escalate_reason" ] && [ -f "$yakos_yml" ]; then
            # Extract YAML list items under pre_filter.risk_regex
            extra_patterns="$(grep -A 50 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
                | grep -A 30 '^[[:space:]]*pre_filter:' \
                | grep -A 20 '^[[:space:]]*risk_regex:' \
                | grep '^[[:space:]]*-[[:space:]]*' \
                | sed 's/^[[:space:]]*-[[:space:]]*//' | tr -d '"' || true)"
            if [ -n "$extra_patterns" ]; then
                while IFS= read -r xpat; do
                    [ -n "$xpat" ] || continue
                    if printf '%s' "$combined" | grep -qiE "$xpat" 2>/dev/null; then
                        escalate_reason="risk-regex:$xpat"
                        break
                    fi
                done <<< "$extra_patterns"
            fi
        fi
    fi

    # If no trigger fired, buffer-only return — no dispatch, no counter tick.
    if [ -z "$escalate_reason" ]; then
        ho_log "supervisor-stream" "REPORT" "pass" \
            "pre-filter: no trigger; buffered without dispatch" \
            "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
                '{pre_filter: "pass", tool: $tool, file: $file}')"
        exit 0
    fi

    # A trigger fired — log it and fall through to the score-dispatch path.
    ho_log "supervisor-stream" "REPORT" "pass" \
        "pre-filter: ESCALATE ($escalate_reason); counting toward score threshold" \
        "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
            --arg reason "$escalate_reason" \
            '{pre_filter: "escalate", trigger: $reason, tool: $tool, file: $file}')"
fi

# --- 3. Increment escalation counter ----------------------------------------
# Only escalations (pre-filter triggers OR pre-filter disabled) reach here.

cur=0
[ -f "$counter" ] && cur="$(cat "$counter" 2>/dev/null || echo 0)"
cur=$((cur + 1))
printf '%d\n' "$cur" > "$counter" 2>/dev/null || exit 0

# --- 4. Every N escalations, fork supervisor dispatch ----------------------

# Read score_every_n_calls from .yakos.yml; default 10
score_every=10
if [ -f "$yakos_yml" ]; then
    n="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*score_every_n_calls:[[:space:]]*[0-9]+' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    case "$n" in
        ''|*[!0-9]*) : ;;
        *) score_every="$n" ;;
    esac
fi

if [ "$((cur % score_every))" -ne 0 ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "escalation buffered; not yet at score-every threshold" \
        "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
            '{counter: $cur, score_every: $every, will_score: false}')"
    exit 0
fi

# We're at the threshold — fork the supervisor.
ho_log "supervisor-stream" "REPORT" "pass" \
    "escalation score threshold hit; forking supervisor dispatch (async)" \
    "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
        '{counter: $cur, score_every: $every, will_score: true}')"

# Read supervisor runtime + agent from .yakos.yml (defaults: claude, supervisor)
# TODO: supervisor.runtime: local-llm → Ollama integration (future PR)
sup_runtime="claude"
sup_agent="supervisor"
if [ -f "$yakos_yml" ]; then
    r="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*runtime:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$r" ] && sup_runtime="$r"
    a="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*agent:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$a" ] && sup_agent="$a"
fi

# Find the yakos CLI
yakos_cli=""
if [ -n "${YAKOS_CLI:-}" ]; then
    yakos_cli="$YAKOS_CLI"
elif [ -n "${YAKOS_ROOT:-}" ] && [ -f "$YAKOS_ROOT/cli/yakos" ]; then
    yakos_cli="$YAKOS_ROOT/cli/yakos"
elif command -v yakos >/dev/null 2>&1; then
    yakos_cli="$(command -v yakos)"
else
    # No CLI — log + abort the fork
    ho_log "supervisor-stream" "WARN" "pass" \
        "could not locate yakos CLI to fork supervisor" "{}"
    exit 0
fi

# Build the supervisor task. The agent itself knows the rubric (from
# its persona); we just point it at the buffer + tell it where to
# write findings.
task="Read $buffer (the last 50 tool calls; focus on the most recent $score_every).
Apply the rubric in your persona. Write your finding as a single
JSON line appended to $findings.

Stated intent of the active session: $(head -c 1500 \
    "$current_dir/decisions.md" 2>/dev/null \
    || echo '(decisions.md not found; use the most recent user prompt as intent)')"

# Fork in background; daemonize via nohup + redirect. The supervisor's
# dispatch may take 5-30 seconds; we MUST NOT block this PostToolUse hook.
# Pattern mirrors retro-dispatch.sh (PR #20): nohup + & + disown.
# Model flag (--model haiku by default) keeps routine scoring on the cheap
# tier; override via supervisor.model in .yakos.yml (haiku|sonnet|opus).
nohup "$yakos_cli" dispatch "$sup_agent" "$task" \
    --runtime "$sup_runtime" \
    --model "$sup_model" \
    >> "$current_dir/.supervisor-stdout.log" 2>>"$current_dir/.supervisor-stderr.log" &
disown 2>/dev/null || true

ho_log "supervisor-stream" "REPORT" "pass" \
    "supervisor dispatch forked async (model=$sup_model runtime=$sup_runtime)" \
    "$(jq -nc --arg model "$sup_model" --arg runtime "$sup_runtime" \
        '{dispatch: "async", model: $model, runtime: $runtime}')"

exit 0
