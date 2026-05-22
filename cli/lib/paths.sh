#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: paths.sh — canonical path resolver for YakOS state files.
#
# Source-of-truth for where work/current/ lives. CLI subcommands AND hook
# scripts source this file so they agree on:
#
#   - the project's work directory
#   - the .session-started timestamp file (single line, ISO 8601)
#   - the .session-started-history.ndjson file (NDJSON, append-only)
#   - the sessions.ndjson ledger file
#   - the .session-summarized marker
#
# Resolution order for the work directory:
#
#   1. $YAKOS_WORK_DIR — absolute-path override (used by tests + the
#      retrofit's self-validation; bypasses other logic completely).
#   2. $YAKOS_INPLACE_WORK=1 — keep work/ inside the project repo
#      (i.e. ${CLAUDE_PROJECT_DIR}/work). This was the Batch-2 default;
#      it remains available for tests, examples, and any caller that
#      wants in-repo scratchpad.
#   3. ${HOME}/agent-control/${YAKOS_PROJECT_NAME}/work — the canonical
#      Phase-1.5 §7 location. YAKOS_PROJECT_NAME defaults to the basename
#      of $CLAUDE_PROJECT_DIR (or pwd as last resort).
#
# The resolver always echoes a path; it never blocks or exits.

if [ "${YAKOS_PATHS_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
YAKOS_PATHS_LOADED=1

yakos_project_name() {
    # Caller-controlled: set $YAKOS_PROJECT_NAME explicitly when the basename
    # of CLAUDE_PROJECT_DIR doesn't match the agent-control subdirectory
    # name. Otherwise infer from CLAUDE_PROJECT_DIR (or pwd).
    if [ -n "${YAKOS_PROJECT_NAME:-}" ]; then
        printf '%s' "$YAKOS_PROJECT_NAME"
        return
    fi
    local base
    if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
        base="$(basename -- "$CLAUDE_PROJECT_DIR")"
    else
        base="$(basename -- "$(pwd -P)")"
    fi
    printf '%s' "$base"
}

yakos_work_dir() {
    if [ -n "${YAKOS_WORK_DIR:-}" ]; then
        printf '%s' "$YAKOS_WORK_DIR"
        return
    fi
    if [ "${YAKOS_INPLACE_WORK:-0}" = "1" ] && [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
        printf '%s/work' "$CLAUDE_PROJECT_DIR"
        return
    fi
    printf '%s/agent-control/%s/work' "${HOME:-/tmp}" "$(yakos_project_name)"
}

yakos_current_dir() {
    printf '%s/current' "$(yakos_work_dir)"
}

yakos_logs_dir() {
    printf '%s/logs' "$(yakos_current_dir)"
}

yakos_session_started_file() {
    # Single-line ISO-8601 timestamp; overwritten on each TeamCreate.
    printf '%s/.session-started' "$(yakos_current_dir)"
}

yakos_session_history_file() {
    # NDJSON, append-only. One event per line.
    printf '%s/.session-started-history.ndjson' "$(yakos_current_dir)"
}

yakos_sessions_log_file() {
    # Persistent NDJSON ledger across sessions. Lives at the work/ level
    # (peer to current/ and archive/) so it survives 'yakos archive'.
    printf '%s/sessions.ndjson' "$(yakos_work_dir)"
}

yakos_summarized_marker_file() {
    # team-lifecycle.sh's TeamDelete handler creates this; session-end-check.sh
    # reads it to know "summary already written" and skip duplication.
    printf '%s/.session-summarized' "$(yakos_current_dir)"
}

yakos_bypass_file() {
    printf '%s/hook-bypass.md' "$(yakos_current_dir)"
}

yakos_messages_log_file() {
    printf '%s/messages.ndjson' "$(yakos_current_dir)"
}

# ----------------------------------------------------------------------------
# Plan 1 (multi-dev coordination) — coord helpers
#
# Multi-dev shared-dev-box coordination lives at
# /var/lib/yakos/<project>/coord/ owned by group yakos-coord. The
# coord directory is OPTIONAL — vanilla yakOS works without it; all
# hooks no-op via yakos_coord_enabled when it's absent.
#
# Root overridable via YAKOS_COORD_ROOT for tests + dev boxes that
# prefer a different location.
# ----------------------------------------------------------------------------

yakos_coord_root() {
    printf '%s' "${YAKOS_COORD_ROOT:-/var/lib/yakos}"
}

yakos_coord_dir() {
    printf '%s/%s/coord' "$(yakos_coord_root)" "$(yakos_project_name)"
}

yakos_coord_activity_log() {
    printf '%s/activity.ndjson' "$(yakos_coord_dir)"
}

yakos_coord_sessions_dir() {
    printf '%s/sessions' "$(yakos_coord_dir)"
}

yakos_coord_session_file() {
    # Per-session NDJSON ledger: <user>@<host>-<pid>.ndjson. Stable for
    # the lifetime of this process (pid is fixed); two concurrent sessions
    # from the same user on the same box get different files because their
    # pids differ.
    local user host pid
    user="${USER:-$(id -un 2>/dev/null || echo unknown)}"
    host="${HOSTNAME:-$(hostname -s 2>/dev/null || echo unknown)}"
    pid="${YAKOS_SESSION_PID:-$$}"
    printf '%s/%s@%s-%s.ndjson' "$(yakos_coord_sessions_dir)" "$user" "$host" "$pid"
}

yakos_coord_claims_file() {
    printf '%s/active-claims.json' "$(yakos_coord_dir)"
}

yakos_coord_memory_dir() {
    # Sibling of coord/ — shared canonical memory store. memory.sh
    # symlinks ~/.yakos-state/memory/<project>/ here when --multi-dev.
    printf '%s/%s/memory' "$(yakos_coord_root)" "$(yakos_project_name)"
}

yakos_coord_enabled() {
    # Return 0 iff coord is configured AND writable by this process.
    # Hooks call this; if it returns non-zero they no-op silently.
    local d
    d="$(yakos_coord_dir)"
    [ -d "$d" ] && [ -w "$d" ]
}

yakos_coord_emit() {
    # Append a single event to BOTH the shared activity.ndjson AND the
    # per-session ledger. Atomic-per-line via stdio append. No-op when
    # coord is disabled. Never blocks.
    #
    # Args: <kind> <json-detail>
    #   <kind>          string event kind (claim_intent, mode_proposal, etc)
    #   <json-detail>   pre-built JSON object string (must parse as JSON)
    yakos_coord_enabled || return 0
    command -v jq >/dev/null 2>&1 || return 0

    local kind="$1" detail="${2:-{\}}"
    local user host pid sid ts agent
    user="${USER:-$(id -un 2>/dev/null || echo unknown)}"
    host="${HOSTNAME:-$(hostname -s 2>/dev/null || echo unknown)}"
    pid="${YAKOS_SESSION_PID:-$$}"
    sid="${CLAUDE_SESSION_ID:-${YAKOS_SESSION_ID:-unknown}}"
    agent="${YAKOS_AGENT_ID:-}"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    local event
    event="$(jq -nc \
        --arg ts "$ts" \
        --arg kind "$kind" \
        --arg user "$user" \
        --arg host "$host" \
        --argjson pid "$pid" \
        --arg sid "$sid" \
        --arg agent "$agent" \
        --argjson detail "$detail" \
        '{ts: $ts, kind: $kind,
          actor: {user: $user, host: $host, pid: $pid,
                  session_id: $sid, agent: ($agent | select(length>0))},
          detail: $detail}' 2>/dev/null)"
    [ -n "$event" ] || return 0

    # Append to the per-session ledger and the shared activity log.
    # mkdir -p first — coord-readme.template doesn't create sessions/.
    mkdir -p "$(yakos_coord_sessions_dir)" 2>/dev/null || return 0
    printf '%s\n' "$event" >> "$(yakos_coord_session_file)" 2>/dev/null || true
    printf '%s\n' "$event" >> "$(yakos_coord_activity_log)" 2>/dev/null || true
}

yakos_migrate_session_history() {
    # One-shot migration: if the legacy .session-started-history file exists
    # as a JSON array (Batch 1B format), convert to NDJSON at the new path.
    # Safe to call repeatedly; no-op once migration has happened.
    local current legacy ndjson
    current="$(yakos_current_dir)"
    legacy="$current/.session-started-history"
    ndjson="$(yakos_session_history_file)"

    [ -f "$legacy" ] || return 0
    [ -f "$ndjson" ] && return 0   # already migrated

    # Only convert if the legacy file is a JSON array. Anything else,
    # leave alone and surface — we don't want to silently corrupt user state.
    if command -v jq >/dev/null 2>&1 && \
       jq -e 'type == "array"' "$legacy" >/dev/null 2>&1; then
        mkdir -p "$current" 2>/dev/null || return 0
        if jq -c '.[]' "$legacy" > "$ndjson" 2>/dev/null; then
            rm -f "$legacy"
        fi
    fi
}
