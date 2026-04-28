#!/usr/bin/env bash
# hook-output.sh — structured NDJSON logging + bypass + block helpers.
#
# Severity tiers (from Phase 1.5 §12):
#   BLOCK  — exit 2; tool call refused
#   WARN   — exit 0; non-empty warning field; agent sees it but action proceeds
#   PASS   — exit 0; clean
#   REPORT — exit 0; pure telemetry
#
# Usage:
#     . "$HOOK_DIR/lib/hook-output.sh"
#     ho_log <hook> <severity> <decision> <reason> [extra-jq-object]
#     ho_block <hook> <message>                # writes BLOCK record + exits 2
#     ho_check_bypass <hook> <scope> && pass   # returns 0 if bypass active

if [ "${HO_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
HO_LOADED=1

ho_now() {
    date -u +%Y-%m-%dT%H:%M:%SZ
}

ho_logdir() {
    printf '%s' "${CLAUDE_PROJECT_DIR:-.}/work/current/logs"
}

ho_log() {
    # ho_log <hook-name> <severity> <decision> <reason> [extra-jq-object]
    # extra-jq-object defaults to {}; merged into the record.
    local hook="$1" severity="$2" decision="$3" reason="$4" extra="${5:-}"
    [ -z "$extra" ] && extra='{}'

    local logdir logfile
    logdir="$(ho_logdir)"
    mkdir -p "$logdir" 2>/dev/null || return 0
    logfile="$logdir/${hook}.ndjson"

    # If hi_init wasn't called or jq isn't available, write a minimal record.
    if ! command -v jq >/dev/null 2>&1; then
        printf '{"ts":"%s","hook":"%s","severity":"%s","decision":"%s","reason":%s}\n' \
            "$(ho_now)" "$hook" "$severity" "$decision" \
            "$(printf '%s' "$reason" | sed 's/"/\\"/g; s/^/"/; s/$/"/')" \
            >> "$logfile"
        return 0
    fi

    jq -nc \
        --arg ts "$(ho_now)" \
        --arg hook "$hook" \
        --arg severity "$severity" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg agent "$(hi_sender_role 2>/dev/null || echo lead)" \
        --arg session "$(hi_session_id 2>/dev/null || echo '')" \
        --arg event "$(hi_event 2>/dev/null || echo '')" \
        --argjson extra "$extra" \
        '{ts: $ts, hook: $hook, severity: $severity, decision: $decision, reason: $reason, agent: $agent, session_id: $session, event: $event} + $extra' \
        >> "$logfile"
}

ho_block() {
    # Print message to stderr (surfaced to model per Phase 0 Test 6a) and
    # exit 2. The hook should already have written its log record before
    # calling this.
    local hook="$1" reason="$2"
    echo "${hook}: ${reason}" >&2
    exit 2
}

ho_check_bypass() {
    # Returns 0 (success) if the active-bypass file has an entry whose:
    #   **Hook:** value contains <hook-name>, AND
    #   **Scope:** value contains <scope-string> (or scope is empty)
    # Otherwise returns 1.
    #
    # Bypass entries must appear after the literal `## Active entries`
    # heading in work/current/hook-bypass.md. The format-example block
    # above that header is ignored.
    local hook="$1" scope="${2:-}"
    local bypass_file="${CLAUDE_PROJECT_DIR:-.}/work/current/hook-bypass.md"
    [ -f "$bypass_file" ] || return 1

    awk -v hook="$hook" -v scope="$scope" '
        BEGIN { active=0; in_entry=0; ok_hook=0; ok_scope=0; found=0 }
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active=1; next }
        active && /^##[[:space:]]+bypass:/ {
            if (in_entry && ok_hook && ok_scope) found=1
            in_entry=1; ok_hook=0; ok_scope=0; next
        }
        in_entry && /^\*\*Hook:\*\*/ {
            line=$0; sub(/^\*\*Hook:\*\*[[:space:]]*/, "", line)
            if (index(line, hook) > 0) ok_hook=1
        }
        in_entry && /^\*\*Scope:\*\*/ {
            line=$0; sub(/^\*\*Scope:\*\*[[:space:]]*/, "", line)
            if (scope == "" || index(line, scope) > 0) ok_scope=1
        }
        END {
            if (in_entry && ok_hook && ok_scope) found=1
            exit(found ? 0 : 1)
        }
    ' "$bypass_file"
}
