#!/usr/bin/env bash
# session-end-check.sh — SessionEnd hook (final state audit).
#
# SessionEnd cannot block exit. This hook produces a durable record of the
# session's terminal state: stuck teammates, stale decisions.md, expired
# bypass entries, and a hook-by-hook summary. Always exits 0.
#
# Replaces the "lead shuts down before work is done" failure mode by
# making the audit visible after the fact.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

CURRENT_DIR="${CLAUDE_PROJECT_DIR:-.}/work/current"
LOGDIR="$CURRENT_DIR/logs"
mkdir -p "$LOGDIR" 2>/dev/null || true

# ---- decisions.md staleness -------------------------------------------------

decisions_stale="false"
decisions_age_s=0
if [ -f "$CURRENT_DIR/decisions.md" ]; then
    mtime="$(stat -f '%m' "$CURRENT_DIR/decisions.md" 2>/dev/null \
            || stat -c '%Y' "$CURRENT_DIR/decisions.md" 2>/dev/null \
            || echo 0)"
    now_s="$(date +%s)"
    if [ "$mtime" -gt 0 ]; then
        decisions_age_s=$((now_s - mtime))
        if [ "$decisions_age_s" -gt 7200 ]; then
            decisions_stale="true"
        fi
    fi
fi

# ---- expired bypass entries -------------------------------------------------

expired_count=0
expired_ids=""
BYPASS_FILE="$CURRENT_DIR/hook-bypass.md"
if [ -f "$BYPASS_FILE" ]; then
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
    ' "$BYPASS_FILE")
fi

# ---- per-hook outcome counts ------------------------------------------------

block_count=0
warn_count=0
report_count=0
hook_summary='{}'
if [ -d "$LOGDIR" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        # Count records by severity for this hook
        name="$(basename "$f" .ndjson)"
        # Skip our own log file to avoid recursion in subsequent runs
        [ "$name" = "session-end-check" ] && continue
        b="$(jq -c 'select(.severity=="BLOCK")' "$f" 2>/dev/null | wc -l | tr -d ' ')"
        w="$(jq -c 'select(.severity=="WARN")'  "$f" 2>/dev/null | wc -l | tr -d ' ')"
        r="$(jq -c 'select(.severity=="REPORT" or .severity=="PASS")' "$f" 2>/dev/null | wc -l | tr -d ' ')"
        block_count=$((block_count + b))
        warn_count=$((warn_count + w))
        report_count=$((report_count + r))
        hook_summary="$(jq -nc --argjson prev "$hook_summary" --arg n "$name" --argjson b "$b" --argjson w "$w" --argjson r "$r" \
            '$prev + {($n): {block:$b, warn:$w, report:$r}}')"
    done < <(find "$LOGDIR" -maxdepth 1 -type f -name '*.ndjson' 2>/dev/null)
fi

# ---- terminal state record --------------------------------------------------

extra="$(jq -nc \
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

ho_log "session-end-check" "$severity" "pass" "session terminal state recorded" "$extra"

exit 0
