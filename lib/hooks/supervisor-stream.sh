#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervisor-stream.sh — PostToolUse hook that streams events to
# the live supervisor (v0.33+).
#
# For every tool call observed in PostToolUse:
#   1. Append a summary line to work/current/supervisor-buffer.ndjson
#      (rolling buffer; auto-trimmed to last 50 entries)
#   2. Increment a counter at work/current/.supervisor-counter
#   3. Every N calls (default 10, configurable via .yakos.yml), FORK a
#      supervisor dispatch in the background to score the recent batch.
#      The dispatch writes its finding to
#      work/current/supervisor-findings.ndjson.
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

# Skip if .yakos.yml says supervisor.enabled: false. Parse cheaply with
# grep — full YAML parsing is overkill for one boolean.
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
if [ -f "$yakos_yml" ]; then
    if grep -q '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null && \
       grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
         | grep -q '^[[:space:]]*enabled:[[:space:]]*false[[:space:]]*$'; then
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

# --- 2. Increment counter ---------------------------------------------------

cur=0
[ -f "$counter" ] && cur="$(cat "$counter" 2>/dev/null || echo 0)"
cur=$((cur + 1))
printf '%d\n' "$cur" > "$counter" 2>/dev/null || exit 0

# --- 3. Every N calls, fork supervisor dispatch -----------------------------

# Read score_every_n_calls from .yakos.yml; default 10
score_every=10
if [ -f "$yakos_yml" ]; then
    n="$(grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*score_every_n_calls:[[:space:]]*[0-9]+' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    case "$n" in
        ''|*[!0-9]*) : ;;
        *) score_every="$n" ;;
    esac
fi

if [ "$((cur % score_every))" -ne 0 ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "appended to buffer; not yet at score-every threshold" \
        "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
            '{counter: $cur, score_every: $every, will_score: false}')"
    exit 0
fi

# We're at the threshold — fork the supervisor.
ho_log "supervisor-stream" "REPORT" "pass" \
    "score threshold hit; forking supervisor dispatch" \
    "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
        '{counter: $cur, score_every: $every, will_score: true}')"

# Read supervisor runtime + agent from .yakos.yml (defaults: claude, supervisor)
sup_runtime="claude"
sup_agent="supervisor"
if [ -f "$yakos_yml" ]; then
    r="$(grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*runtime:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$r" ] && sup_runtime="$r"
    a="$(grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
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
# dispatch may take 5-30 seconds; we MUST NOT block this PostToolUse
# hook.
nohup "$yakos_cli" dispatch "$sup_agent" "$task" --runtime "$sup_runtime" \
    >> "$current_dir/.supervisor-stdout.log" 2>>"$current_dir/.supervisor-stderr.log" &
disown 2>/dev/null || true

exit 0
