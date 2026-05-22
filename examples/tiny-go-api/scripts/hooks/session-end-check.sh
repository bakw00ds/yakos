#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# session-end-check.sh — SessionEnd hook (final state audit).
#
# This is a TELEMETRY HOOK. It NEVER blocks. SessionEnd hooks cannot block
# exit anyway, but every write must be guarded — preventing the user's
# session from ending cleanly because of a telemetry hiccup is the wrong
# tradeoff. (See lib/hooks/README.md "No-block policy for telemetry hooks".)
#
# Behavior:
#
#   1. Run audit checks (decisions.md staleness, expired hook bypasses,
#      per-hook outcome counts) and emit a structured ho_log record. WARN
#      severity when something needs attention; otherwise REPORT.
#
#   2. Idempotent session summary in sessions.ndjson:
#        - If .session-summarized marker exists, team-lifecycle.sh's
#          TeamDelete branch already wrote the summary. Remove the marker
#          and skip.
#        - Otherwise, append a session-end summary with exit_kind set to
#          "session_end_without_team_delete", with the same idempotency
#          check on (session_id, exit_kind).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=./lib/compat.sh
[ -f "$HOOK_DIR/lib/compat.sh" ] && . "$HOOK_DIR/lib/compat.sh"

hi_init

session_id="$(hi_session_id)"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

current_dir="$(yakos_current_dir)"
logs_dir="$(yakos_logs_dir)"
mkdir -p "$current_dir" "$logs_dir" 2>/dev/null || true

# ---- audit: decisions.md staleness -----------------------------------------

decisions_stale="false"
decisions_age_s=0
if [ -f "$current_dir/decisions.md" ]; then
    mtime="$(stat -f '%m' "$current_dir/decisions.md" 2>/dev/null \
            || stat -c '%Y' "$current_dir/decisions.md" 2>/dev/null \
            || echo 0)"
    now_s="$(date +%s)"
    if [ "$mtime" -gt 0 ]; then
        decisions_age_s=$((now_s - mtime))
        [ "$decisions_age_s" -gt 7200 ] && decisions_stale="true"
    fi
fi

# ---- audit: expired bypass entries -----------------------------------------

expired_count=0
expired_ids=""
bypass_file="$(yakos_bypass_file)"
if [ -f "$bypass_file" ]; then
    while IFS=$'\t' read -r id exp; do
        [ -n "$id" ] || continue
        [ -n "$exp" ] || continue
        is_expired="$(printf '%s' "$exp" | jq -Rr 'try (fromdateiso8601 < now | tostring) catch "?"')"
        if [ "$is_expired" = "true" ]; then
            expired_count=$((expired_count + 1))
            expired_ids="${expired_ids}${id} "
        fi
    done < <(awk '
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active = 1; next }
        active && /^##[[:space:]]+bypass:/ {
            id = $0
            sub(/^##[[:space:]]+bypass:/, "", id)
            sub(/^[[:space:]]+/, "", id)
            next
        }
        active && /^\*\*Expires:\*\*/ {
            line = $0
            sub(/^\*\*Expires:\*\*[[:space:]]*/, "", line)
            split(line, parts, /[[:space:]]/)
            print id "\t" parts[1]
        }
    ' "$bypass_file")
fi

# ---- audit: per-hook outcome counts ----------------------------------------

block_count=0
warn_count=0
report_count=0
hook_summary='{}'
if [ -d "$logs_dir" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        name="$(basename "$f" .ndjson)"
        [ "$name" = "session-end-check" ] && continue
        b="$(jq -c 'select(.severity=="BLOCK")' "$f" 2>/dev/null | wc -l | tr -d ' ')"
        w="$(jq -c 'select(.severity=="WARN")'  "$f" 2>/dev/null | wc -l | tr -d ' ')"
        r="$(jq -c 'select(.severity=="REPORT" or .severity=="PASS")' "$f" 2>/dev/null | wc -l | tr -d ' ')"
        block_count=$((block_count + b))
        warn_count=$((warn_count + w))
        report_count=$((report_count + r))
        hook_summary="$(jq -nc --argjson prev "$hook_summary" --arg n "$name" --argjson b "$b" --argjson w "$w" --argjson r "$r" \
            '$prev + {($n): {block:$b, warn:$w, report:$r}}')"
    done < <(find "$logs_dir" -maxdepth 1 -type f -name '*.ndjson' 2>/dev/null)
fi

# ---- audit log entry --------------------------------------------------------

audit_extra="$(jq -nc \
    --arg decisions_stale "$decisions_stale" \
    --argjson decisions_age "$decisions_age_s" \
    --argjson expired_count "$expired_count" \
    --arg expired_ids "${expired_ids% }" \
    --argjson block_count "$block_count" \
    --argjson warn_count "$warn_count" \
    --argjson report_count "$report_count" \
    --argjson hook_summary "$hook_summary" \
    '{decisions_stale: $decisions_stale, decisions_age_s: $decisions_age, expired_bypass_count: $expired_count, expired_bypass_ids: $expired_ids, hook_blocks: $block_count, hook_warns: $warn_count, hook_reports: $report_count, hooks: $hook_summary}')"

severity="REPORT"
[ "$decisions_stale" = "true" ] && severity="WARN"
[ "$expired_count" -gt 0 ] && severity="WARN"

ho_log "session-end-check" "$severity" "pass" "session terminal state recorded" "$audit_extra"

# ---- session summary (idempotent) ------------------------------------------

marker_file="$(yakos_summarized_marker_file)"
if [ -f "$marker_file" ]; then
    # team-lifecycle TeamDelete already wrote the summary for this session.
    rm -f "$marker_file" 2>/dev/null || true
    exit 0
fi

sessions_log="$(yakos_sessions_log_file)"
mkdir -p "$(dirname -- "$sessions_log")" 2>/dev/null || true
touch "$sessions_log" 2>/dev/null || true

exit_kind="session_end_without_team_delete"

# Idempotency check
if [ -s "$sessions_log" ] && command -v jq >/dev/null 2>&1; then
    if jq -e --arg sid "$session_id" --arg ek "$exit_kind" \
           'select(.session_id == $sid and .exit_kind == $ek)' \
           "$sessions_log" 2>/dev/null | grep -q .; then
        exit 0
    fi
fi

# Best-effort start timestamp from .session-started, then history
ts_start=""
if [ -f "$(yakos_session_started_file)" ]; then
    ts_start="$(head -n 1 "$(yakos_session_started_file)" 2>/dev/null || true)"
fi
if [ -z "$ts_start" ] && [ -f "$(yakos_session_history_file)" ] && command -v jq >/dev/null 2>&1; then
    ts_start="$(jq -rc --arg sid "$session_id" \
        'select(.session_id == $sid and .event == "team_created") | .ts' \
        "$(yakos_session_history_file)" 2>/dev/null | tail -n 1 || true)"
fi

duration="null"
if [ -n "$ts_start" ] && command -v ct_iso_to_epoch >/dev/null 2>&1; then
    start_s="$(ct_iso_to_epoch "$ts_start")"
    end_s="$(ct_iso_to_epoch "$ts")"
    if [ "$start_s" -gt 0 ] && [ "$end_s" -gt 0 ]; then
        duration="$((end_s - start_s))"
    fi
fi

team_name=""
if [ -f "$(yakos_session_history_file)" ] && command -v jq >/dev/null 2>&1; then
    team_name="$(jq -rc --arg sid "$session_id" \
        'select(.session_id == $sid and .event == "team_created") | .team_name' \
        "$(yakos_session_history_file)" 2>/dev/null | tail -n 1 || true)"
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

exit 0
