#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: retro-dispatch.sh — auto-dispatch librarian when .retro-due marker present.
#
# Hook context: UserPromptSubmit. Telemetry hook (always exit 0).
# Reads:  <work>/current/.retro-due      (marker written by cycle-counter.sh)
#         ~/.yakos-state/settings.json   (retro.auto_dispatch flag)
#         ~/.yakos-state/retro-dispatch.pid (in-flight guard)
# Writes: <work>/current/logs/retro-dispatch.ndjson
#         ~/.yakos-state/retro-dispatch.pid (while dispatch in flight)
#
# Behavior:
#   - No marker → no-op, exit 0.
#   - Marker present + auto_dispatch=false (yakos retro disable) → no-op, exit 0.
#   - Marker present + prior dispatch in flight (PID alive) → skip, log.
#   - Marker present + no in-flight → spawn 'yakos dispatch librarian ...' in
#     background (nohup + disown); remove marker on successful spawn;
#     record dispatch in log.
#   - Any infrastructure error → log + exit 0 (never block UserPromptSubmit).
#
# The dispatch runs async. UserPromptSubmit proceeds immediately.
# The librarian's output goes to its own log/output channel, not here.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
# shellcheck source=lib/hook-input.sh
. "$HOOK_DIR/lib/hook-input.sh"
# shellcheck source=lib/hook-output.sh
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# ---------------------------------------------------------------------------
# Resolve paths
# ---------------------------------------------------------------------------

current_dir="$(yakos_current_dir)"

# No active project session — no-op.
[ -n "$current_dir" ] || exit 0
[ -d "$current_dir" ] || exit 0

marker_file="$current_dir/.retro-due"
settings_file="$HOME/.yakos-state/settings.json"
pid_file="$HOME/.yakos-state/retro-dispatch.pid"

# ---------------------------------------------------------------------------
# Guard: marker must be present
# ---------------------------------------------------------------------------

if [ ! -f "$marker_file" ]; then
    exit 0
fi

# ---------------------------------------------------------------------------
# Guard: operator opt-out (yakos retro disable sets .retro.auto_dispatch=false)
# ---------------------------------------------------------------------------

auto_dispatch=true
if [ -f "$settings_file" ] && command -v jq >/dev/null 2>&1; then
    # NOTE: jq's `//` operator treats `false` as an alternative trigger, so
    # `.retro.auto_dispatch // true` would return `true` when the value is
    # the boolean `false`. Use explicit null check instead.
    val="$(jq -r 'if .retro.auto_dispatch == false then "false" else "true" end' \
        "$settings_file" 2>/dev/null || true)"
    [ "$val" = "false" ] && auto_dispatch=false
fi

if [ "$auto_dispatch" = "false" ]; then
    ho_log "retro-dispatch" REPORT "skipped" \
        "auto_dispatch disabled; marker left in place" \
        '{"reason": "auto_dispatch_disabled"}'
    exit 0
fi

# ---------------------------------------------------------------------------
# Guard: in-flight check (PID file under ~/.yakos-state/)
# ---------------------------------------------------------------------------

if [ -f "$pid_file" ]; then
    prior_pid="$(cat "$pid_file" 2>/dev/null || true)"
    case "$prior_pid" in
        ''|*[!0-9]*) ;;    # malformed PID file — treat as stale
        *)
            if kill -0 "$prior_pid" 2>/dev/null; then
                # Prior dispatch still running — skip to avoid double retro.
                ho_log "retro-dispatch" REPORT "skipped" \
                    "prior dispatch in flight (pid=$prior_pid)" \
                    "$(jq -nc --argjson pid "$prior_pid" \
                        '{"reason": "in_flight", "prior_pid": $pid}' 2>/dev/null || \
                        printf '{"reason":"in_flight","prior_pid":%s}' "$prior_pid")"
                exit 0
            fi
            # Stale PID — remove and proceed.
            rm -f "$pid_file" 2>/dev/null || true
            ;;
    esac
fi

# ---------------------------------------------------------------------------
# Find yakos binary (must be in PATH for dispatch to work)
# ---------------------------------------------------------------------------

YAKOS_BIN=""
if command -v yakos >/dev/null 2>&1; then
    YAKOS_BIN="yakos"
elif [ -n "${YAKOS_ROOT:-}" ] && [ -x "$YAKOS_ROOT/cli/yakos" ]; then
    YAKOS_BIN="$YAKOS_ROOT/cli/yakos"
fi

if [ -z "$YAKOS_BIN" ]; then
    ho_log "retro-dispatch" "WARN" "skipped" \
        "yakos binary not found in PATH; marker left in place" \
        '{"reason": "yakos_not_found"}'
    exit 0
fi

# ---------------------------------------------------------------------------
# Resolve current project path for dispatch (best-effort)
# ---------------------------------------------------------------------------

project_path=""
if [ -n "${CLAUDE_PROJECT_DIR:-}" ] && [ -d "$CLAUDE_PROJECT_DIR" ]; then
    project_path="$CLAUDE_PROJECT_DIR"
else
    # Walk ~/.yakos-state settings or look up .project-path
    proj_name=""
    if [ -n "${YAKOS_PROJECT_NAME:-}" ]; then
        proj_name="$YAKOS_PROJECT_NAME"
    fi
    if [ -n "$proj_name" ]; then
        pp_file="$HOME/agent-control/$proj_name/.project-path"
        if [ -f "$pp_file" ]; then
            project_path="$(head -1 "$pp_file" 2>/dev/null || true)"
        fi
    fi
fi

# ---------------------------------------------------------------------------
# Spawn dispatch in background
# ---------------------------------------------------------------------------

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
dispatch_id="rd-${ts//[:T]/-}"
log_dir="$(yakos_logs_dir)"
mkdir -p "$log_dir" 2>/dev/null || true
dispatch_log="$log_dir/retro-dispatch.ndjson"

LIBRARIAN_TASK="Run 10-cycle retrospective per your agent definition. Read transcript, decisions.md tail, mailbox tail, hook logs. Write findings to work/current/{lessons,mistakes,skill-candidates,drift-report,soul-proposed-edits}.md. Return one-line summary."

# Build dispatch command — add --project flag when we have a project path.
if [ -n "$project_path" ]; then
    dispatch_cmd="$YAKOS_BIN dispatch librarian \"$LIBRARIAN_TASK\" --project \"$project_path\""
else
    dispatch_cmd="$YAKOS_BIN dispatch librarian \"$LIBRARIAN_TASK\""
fi

# Write PID placeholder; spawned process will write its own PID.
# Use a temp file + atomic rename to avoid a race between the PID write and
# the in-flight check above.
pid_tmp="$pid_file.tmp.$$"

# Spawn background process: writes its PID, runs dispatch, cleans up.
(
    printf '%s\n' "$$" > "$pid_tmp" 2>/dev/null && mv "$pid_tmp" "$pid_file" 2>/dev/null || true
    dispatch_exit=0
    eval "$dispatch_cmd" > /dev/null 2>&1 || dispatch_exit=$?
    rm -f "$pid_file" 2>/dev/null || true
    # Log the completion record (best-effort).
    jq -nc \
        --arg ts "$ts" \
        --arg agent "librarian" \
        --argjson exit_code "$dispatch_exit" \
        --argjson marker_consumed "true" \
        --arg dispatch_id "$dispatch_id" \
        '{"ts": $ts, "agent": $agent, "exit_code": $exit_code, "marker_consumed": $marker_consumed, "dispatch_id": $dispatch_id}' \
        >> "$dispatch_log" 2>/dev/null || true
) &
bg_pid=$!
disown "$bg_pid" 2>/dev/null || true

# ---------------------------------------------------------------------------
# Remove marker on successful spawn (marker consumed = dispatch fired once)
# ---------------------------------------------------------------------------

rm_exit=0
rm -f "$marker_file" 2>/dev/null || rm_exit=$?

if [ "$rm_exit" -ne 0 ]; then
    # Marker removal failed — dispatch fired but we can't prevent a re-fire.
    # Log as WARN so the operator knows.
    ho_log "retro-dispatch" WARN "dispatched" \
        "dispatched librarian but marker removal failed; may re-dispatch" \
        "$(jq -nc --arg did "$dispatch_id" --argjson pid "$bg_pid" \
            '{"dispatch_id": $did, "bg_pid": $pid, "marker_removal_failed": true}' \
            2>/dev/null || printf '{"dispatch_id":"%s"}' "$dispatch_id")"
    exit 0
fi

# ---------------------------------------------------------------------------
# Log successful dispatch
# ---------------------------------------------------------------------------

ho_log "retro-dispatch" REPORT "dispatched" \
    "librarian dispatched in background" \
    "$(jq -nc --arg did "$dispatch_id" --argjson pid "$bg_pid" \
        '{"dispatch_id": $did, "bg_pid": $pid}' \
        2>/dev/null || printf '{"dispatch_id":"%s"}' "$dispatch_id")"

exit 0
