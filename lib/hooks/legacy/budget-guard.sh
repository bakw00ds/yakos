#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: budget-guard.sh — PreToolUse hook that enforces per-session
# budget caps (v0.34+).
#
# Caps enforced (any → ho_block):
#   1. max_tool_calls          — total tool-call count this session
#   2. max_wall_seconds        — wall-clock since first tool call
#   3. max_repeat_same_tool    — same tool + similar-args repeated N
#                                 times in a row (loop detection)
#
# Configuration in .yakos.yml:
#   budget:
#     enabled: true
#     max_tool_calls: 500
#     max_wall_seconds: 7200
#     max_repeat_same_tool: 8
#
# All caps optional; missing keys are not enforced. Counters live at
# work/current/.budget-state.json (auto-created on first PreToolUse).
#
# Emergency bypass: YAKOS_BUDGET_DISABLE=1
# Per-cap bypass: add hook-bypass.md entry with
#   Scope: cap=max_tool_calls
# (or max_wall_seconds / max_repeat_same_tool)

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

if [ "${YAKOS_BUDGET_DISABLE:-0}" = "1" ]; then
    exit 0
fi

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
if [ ! -f "$yakos_yml" ]; then
    exit 0  # no config → no enforcement
fi

# Read budget config
if grep -A 10 '^[[:space:]]*budget:' "$yakos_yml" 2>/dev/null \
    | grep -q '^[[:space:]]*enabled:[[:space:]]*false[[:space:]]*$'; then
    exit 0
fi
# If no budget block at all, also no-op
grep -q '^[[:space:]]*budget:' "$yakos_yml" 2>/dev/null || exit 0

command -v jq >/dev/null 2>&1 || exit 0
command -v yakos_current_dir >/dev/null 2>&1 || exit 0

current_dir="$(yakos_current_dir)"
[ -d "$current_dir" ] || mkdir -p "$current_dir" 2>/dev/null || exit 0
state_file="$current_dir/.budget-state.json"

_get_cap() {
    local key="$1"
    grep -A 10 '^[[:space:]]*budget:' "$yakos_yml" 2>/dev/null \
        | grep -E "^[[:space:]]*$key:[[:space:]]*[0-9]+" \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]'
}

max_tool_calls="$(_get_cap max_tool_calls)"
max_wall_seconds="$(_get_cap max_wall_seconds)"
max_repeat_same_tool="$(_get_cap max_repeat_same_tool)"

# Read existing state (or init fresh)
now_epoch="$(date +%s)"
tool="$(hi_tool)"
session_id="$(hi_session_id)"

if [ -f "$state_file" ]; then
    state="$(cat "$state_file" 2>/dev/null)"
    jq empty <<<"$state" 2>/dev/null || state=""
fi
if [ -z "${state:-}" ]; then
    state="$(jq -nc --arg sid "$session_id" --argjson now "$now_epoch" \
        '{session_id: $sid, started_at: $now,
          tool_call_count: 0, last_tool: "",
          last_tool_run_count: 0}')"
fi

# Increment counters
new_count="$(jq -r '.tool_call_count + 1' <<<"$state")"
last_tool="$(jq -r '.last_tool' <<<"$state")"
last_run="$(jq -r '.last_tool_run_count' <<<"$state")"
started_at="$(jq -r '.started_at' <<<"$state")"
elapsed=$((now_epoch - started_at))

if [ "$tool" = "$last_tool" ]; then
    new_run="$((last_run + 1))"
else
    new_run=1
fi

# Write updated state. Atomic via tmp + mv.
new_state="$(jq -nc --arg sid "$session_id" \
    --argjson sa "$started_at" \
    --argjson c "$new_count" \
    --arg lt "$tool" \
    --argjson lr "$new_run" \
    '{session_id: $sid, started_at: $sa,
      tool_call_count: $c, last_tool: $lt,
      last_tool_run_count: $lr}')"
tmp="$state_file.tmp.$$"
printf '%s\n' "$new_state" > "$tmp" 2>/dev/null && mv "$tmp" "$state_file" 2>/dev/null

# --- Enforcement ---
emit_audit() {
    local severity="$1" cap="$2" reason="$3" detail_extra="$4"
    ho_log "budget-guard" "$severity" "$([ "$severity" = "BLOCK" ] && echo block || echo pass)" \
        "$reason" \
        "$(jq -nc --arg cap "$cap" --argjson c "$new_count" \
            --argjson e "$elapsed" --argjson r "$new_run" \
            --arg t "$tool" --argjson extra "$detail_extra" \
            '{cap: $cap, tool_call_count: $c, elapsed_seconds: $e,
              repeat_count: $r, tool: $t} + $extra')"
}

# Check max_tool_calls
if [ -n "$max_tool_calls" ] && [ "$new_count" -gt "$max_tool_calls" ]; then
    if ho_check_bypass "budget" "cap=max_tool_calls"; then
        emit_audit WARN max_tool_calls "max_tool_calls exceeded but bypass active" '{"bypass":true}'
    else
        emit_audit BLOCK max_tool_calls "session tool-call count $new_count exceeds cap $max_tool_calls" '{"bypass":false}'
        ho_block "budget-guard" \
"session tool-call budget exceeded: $new_count > $max_tool_calls.
       This is a session-level cap configured in .yakos.yml budget.max_tool_calls.
       Options:
         1. Wait for the next session (counters reset per session)
         2. Add a bypass entry in work/current/hook-bypass.md with
            Scope: cap=max_tool_calls
         3. Raise the cap in .yakos.yml budget.max_tool_calls
         4. Emergency: export YAKOS_BUDGET_DISABLE=1"
    fi
fi

# Check max_wall_seconds
if [ -n "$max_wall_seconds" ] && [ "$elapsed" -gt "$max_wall_seconds" ]; then
    if ho_check_bypass "budget" "cap=max_wall_seconds"; then
        emit_audit WARN max_wall_seconds "max_wall_seconds exceeded but bypass active" '{"bypass":true}'
    else
        emit_audit BLOCK max_wall_seconds "session elapsed $elapsed seconds exceeds cap $max_wall_seconds" '{"bypass":false}'
        ho_block "budget-guard" \
"session wall-clock budget exceeded: ${elapsed}s > ${max_wall_seconds}s.
       This is a session-level cap configured in .yakos.yml budget.max_wall_seconds.
       Options:
         1. End the current session (counters reset)
         2. Add a bypass entry with Scope: cap=max_wall_seconds
         3. Raise the cap in .yakos.yml budget.max_wall_seconds
         4. Emergency: export YAKOS_BUDGET_DISABLE=1"
    fi
fi

# Check max_repeat_same_tool (loop detection)
if [ -n "$max_repeat_same_tool" ] && [ "$new_run" -gt "$max_repeat_same_tool" ]; then
    if ho_check_bypass "budget" "cap=max_repeat_same_tool"; then
        emit_audit WARN max_repeat_same_tool "$tool repeated $new_run times in a row but bypass active" '{"bypass":true}'
    else
        emit_audit BLOCK max_repeat_same_tool "$tool repeated $new_run times in a row, exceeds cap $max_repeat_same_tool (likely loop)" '{"bypass":false}'
        ho_block "budget-guard" \
"loop suspected: '$tool' invoked $new_run times in a row, exceeds cap $max_repeat_same_tool.
       This is loop-detection cap configured in .yakos.yml budget.max_repeat_same_tool.
       Common causes:
         - Agent stuck retrying a failing tool call
         - Agent reading many files sequentially when a glob would suffice
         - Genuine workflow that happens to use the same tool a lot
       Options:
         1. Reconsider whether the work needs $tool repeatedly
         2. Add bypass with Scope: cap=max_repeat_same_tool (if this is
            a genuine batch operation, not a loop)
         3. Raise the cap in .yakos.yml budget.max_repeat_same_tool
         4. Emergency: export YAKOS_BUDGET_DISABLE=1"
    fi
fi

# All caps respected — clean REPORT and exit
ho_log "budget-guard" "REPORT" "pass" \
    "within all budget caps" \
    "$(jq -nc --argjson c "$new_count" --argjson e "$elapsed" --argjson r "$new_run" \
        --arg t "$tool" \
        '{tool_call_count: $c, elapsed_seconds: $e, repeat_count: $r, tool: $t}')"

exit 0
