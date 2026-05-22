#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: team-lifecycle.sh — PreToolUse on TeamCreate|Agent|TeamDelete.
#
# This is a TELEMETRY HOOK. It NEVER blocks. Always exits 0.
# (See lib/hooks/README.md "No-block policy for telemetry hooks".)
#
# Responsibilities:
#
#   TeamCreate
#     - logs/team-lifecycle.ndjson: append "team_created" event
#     - .session-started:           overwrite with the current ISO 8601 ts
#                                   (yakos status reads this for "Last team")
#     - .session-started-history.ndjson: append a "team_created" line
#       (NDJSON; one event per line; append-only; never reformat to a
#       JSON array — fragile under concurrent writes)
#
#   Agent (subagent spawn)
#     - logs/team-lifecycle.ndjson: append "agent_spawned" event
#     - Does NOT touch .session-started or .session-started-history.
#       Agent spawns within an existing team don't reset session tracking.
#
#   TeamDelete
#     - logs/team-lifecycle.ndjson: append "team_deleted" event
#     - sessions.ndjson:           append a session summary record (with
#                                  idempotency check on session_id+exit_kind)
#     - .session-summarized:       create marker so session-end-check.sh
#                                  knows the summary is already written
#
# The hook NEVER blocks. If any write fails (disk full, permission denied,
# jq missing) we log degraded-mode and exit 0 anyway — preventing the
# user's actual work would be the wrong tradeoff for telemetry.
#
# Phase 0/1.7 confirmed wildcard PreToolUse fires for TeamCreate and Agent;
# TeamDelete is inferred-not-validated (Batch 2 surfaced this UNCLEAR).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=./lib/compat.sh
[ -f "$HOOK_DIR/lib/compat.sh" ] && . "$HOOK_DIR/lib/compat.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    TeamCreate|Agent|TeamDelete) ;;
    *) exit 0 ;;
esac

session_id="$(hi_session_id)"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
caller="$(hi_sender_role)"
raw="$(hi_raw)"
tool_input="$(printf '%s' "$raw" | jq -c '.tool_input // {}' 2>/dev/null || echo '{}')"

# Best-effort migration from Batch 1B's legacy .session-started-history
# (JSON array) to .session-started-history.ndjson. No-op once migrated.
if command -v yakos_migrate_session_history >/dev/null 2>&1; then
    yakos_migrate_session_history 2>/dev/null || true
fi

current_dir="$(yakos_current_dir)"
mkdir -p "$current_dir" 2>/dev/null || true

# Helper for logging team-lifecycle events to its own ndjson + the central
# log via ho_log. Never throws.
emit_event() {
    local event="$1"
    local extra
    extra="$(jq -nc --arg event "$event" --arg caller "$caller" --arg tool "$tool" --argjson input "$tool_input" \
        '{event: $event, caller: $caller, tool: $tool, tool_input: $input}' 2>/dev/null || echo '{}')"
    ho_log "team-lifecycle" "REPORT" "pass" "team-lifecycle: $event" "$extra"
}

# Kanban auto-update (Plan 3 M4). Moves the first matching task block from
# one column to another in <work>/current/kanban.md. No-op if file doesn't
# exist (project hasn't opted into kanban). NEVER throws — telemetry hook.
#
# Args: <src-column> <dst-column> <new-checkbox-char>
#   src-column: "TODO" | "IN PROGRESS" | "DONE"
#   dst-column: same
#   new-checkbox-char: " " | "-" | "x"
kanban_move_first() {
    local src="$1" dst="$2" cbox="$3"
    local kb="$current_dir/kanban.md"
    [ -f "$kb" ] || return 0

    local tmp
    tmp="$(mktemp -t yakos-kb.XXXXXX)" || return 0

    awk -v src="$src" -v dst="$dst" -v cbox="$cbox" '
        # Captured task block stored in `task_buf`; emitted when we hit dst section.
        BEGIN { state = "scan"; task_first = ""; task_rest = ""; moved = 0 }

        # Source section header — start scanning for first task line.
        $0 ~ "^## " src "[[:space:]]*$" {
            print; state = "in_src"; next
        }

        # Inside source section: first "- [<x>] " line is the task to capture.
        state == "in_src" && !moved && /^- \[/ {
            task_first = $0
            state = "capturing_cont"
            next
        }

        # Continuation lines of the captured task (indented 2+ spaces).
        state == "capturing_cont" && /^  / {
            task_rest = task_rest $0 "\n"
            next
        }

        # End of captured task block (non-indent, non-task line); emit nothing,
        # transition back to in_src to pass through the rest.
        state == "capturing_cont" {
            moved = 1
            state = "in_src"
            # fall through to normal print of current line
        }

        # Any new section ends the in_src state.
        /^## / && state == "in_src" {
            state = "scan"
        }

        # Destination section: print header, then re-insert task block with new checkbox.
        $0 ~ "^## " dst "[[:space:]]*$" {
            print
            if (moved) {
                # Replace first "- [<any>]" with "- [<cbox>]"
                line = task_first
                sub(/^- \[.\]/, "- [" cbox "]", line)
                print line
                if (length(task_rest) > 0) printf "%s", task_rest
                moved_emitted = 1
            }
            next
        }

        # Default: pass through.
        { print }

        END {
            # If we captured but didnt emit (src found, dst missing), best
            # not to drop the task — emit a warning marker.
            if (moved && !moved_emitted) {
                print "# WARN: yakos kanban auto-update found src but no dst section"
                print task_first
                printf "%s", task_rest
            }
        }
    ' "$kb" > "$tmp" 2>/dev/null && mv "$tmp" "$kb" 2>/dev/null || rm -f "$tmp" 2>/dev/null
}

case "$tool" in
    TeamCreate)
        team_name="$(printf '%s' "$tool_input" | jq -r '.name // empty' 2>/dev/null || true)"
        emit_event "team_created"

        # Plan 3 M4 — kanban auto-update: first TODO task → IN PROGRESS.
        kanban_move_first "TODO" "IN PROGRESS" "-" 2>/dev/null || true

        # .session-started — overwrite with current ts
        started_file="$(yakos_session_started_file)"
        printf '%s\n' "$ts" > "$started_file" 2>/dev/null || true

        # .session-started-history.ndjson — append
        history_file="$(yakos_session_history_file)"
        if command -v jq >/dev/null 2>&1; then
            jq -nc \
                --arg ts "$ts" \
                --arg event "team_created" \
                --arg team "$team_name" \
                --arg sid "$session_id" \
                '{ts: $ts, event: $event, team_name: $team, session_id: $sid}' \
                >> "$history_file" 2>/dev/null || true
        fi
        ;;

    Agent)
        emit_event "agent_spawned"
        # Intentionally do NOT touch .session-started or history.
        ;;

    TeamDelete)
        team_name="$(printf '%s' "$tool_input" | jq -r '.name // empty' 2>/dev/null || true)"
        emit_event "team_deleted"

        # Plan 3 M4 — kanban auto-update: first IN PROGRESS task → DONE.
        kanban_move_first "IN PROGRESS" "DONE" "x" 2>/dev/null || true

        sessions_log="$(yakos_sessions_log_file)"
        history_file="$(yakos_session_history_file)"
        marker_file="$(yakos_summarized_marker_file)"
        mkdir -p "$(dirname -- "$sessions_log")" 2>/dev/null || true
        touch "$sessions_log" 2>/dev/null || true

        exit_kind="team_deleted"

        # Idempotency check: skip if (session_id, exit_kind) already recorded.
        already_logged="false"
        if [ -s "$sessions_log" ] && command -v jq >/dev/null 2>&1; then
            if jq -e --arg sid "$session_id" --arg ek "$exit_kind" \
                   'select(.session_id == $sid and .exit_kind == $ek)' \
                   "$sessions_log" 2>/dev/null | grep -q .; then
                already_logged="true"
            fi
        fi

        if [ "$already_logged" = "true" ]; then
            emit_event "duplicate_summary_suppressed"
        else
            # Look up the most recent team_created ts for this session_id.
            ts_start=""
            if [ -f "$history_file" ] && command -v jq >/dev/null 2>&1; then
                ts_start="$(jq -rc --arg sid "$session_id" \
                    'select(.session_id == $sid and .event == "team_created") | .ts' \
                    "$history_file" 2>/dev/null | tail -n 1 || true)"
            fi
            # Fallback to .session-started file if history is empty
            if [ -z "$ts_start" ] && [ -f "$(yakos_session_started_file)" ]; then
                ts_start="$(head -n 1 "$(yakos_session_started_file)" 2>/dev/null || true)"
            fi

            duration="null"
            if [ -n "$ts_start" ] && command -v ct_iso_to_epoch >/dev/null 2>&1; then
                start_s="$(ct_iso_to_epoch "$ts_start")"
                end_s="$(ct_iso_to_epoch "$ts")"
                if [ "$start_s" -gt 0 ] && [ "$end_s" -gt 0 ]; then
                    duration="$((end_s - start_s))"
                fi
            fi

            scratchpad_bytes=0
            if command -v ct_dir_size_bytes >/dev/null 2>&1; then
                scratchpad_bytes="$(ct_dir_size_bytes "$current_dir")"
            fi

            if command -v jq >/dev/null 2>&1; then
                jq -nc \
                    --arg ts_start "${ts_start:-}" \
                    --arg ts_end "$ts" \
                    --argjson duration "$duration" \
                    --arg team "${team_name:-}" \
                    --arg sid "$session_id" \
                    --argjson size "$scratchpad_bytes" \
                    --arg ek "$exit_kind" \
                    '{ts_start: $ts_start, ts_end: $ts_end, duration_seconds: $duration, team_name: $team, session_id: $sid, scratchpad_size_bytes: $size, exit_kind: $ek}' \
                    >> "$sessions_log" 2>/dev/null || true
            fi

            # Marker so session-end-check.sh skips its own summary write
            touch "$marker_file" 2>/dev/null || true
        fi
        ;;
esac

exit 0
