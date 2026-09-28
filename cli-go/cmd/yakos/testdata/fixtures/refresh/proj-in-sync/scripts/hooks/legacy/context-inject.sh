#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: context-inject.sh — UserPromptSubmit hook that injects useful
# session context for long-running work (v0.35+).
#
# Before each user prompt is processed, this hook can surface:
#   1. Tail of decisions.md (last N entries) — what was decided recently
#   2. Budget remaining (from .budget-state.json) — heads-up on caps
#   3. Peer-status one-liner (if multi-dev) — who else is active
#   4. Recent supervisor finding (if supervisor enabled) — outstanding judgments
#
# All output goes to stderr (Claude Code surfaces hook stderr as
# system-message context). Configurable in .yakos.yml; defaults to
# OFF (operator opts in to avoid surprise context bloat).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

# Env disable
if [ "${YAKOS_CONTEXT_INJECT_DISABLE:-0}" = "1" ]; then
    exit 0
fi

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"

# Opt-in: must have context_inject.enabled: true in .yakos.yml
if [ ! -f "$yakos_yml" ]; then
    exit 0
fi
if ! grep -A 5 '^[[:space:]]*context_inject:' "$yakos_yml" 2>/dev/null \
    | grep -q '^[[:space:]]*enabled:[[:space:]]*true[[:space:]]*$'; then
    exit 0
fi

command -v yakos_current_dir >/dev/null 2>&1 || exit 0
current_dir="$(yakos_current_dir)"
[ -d "$current_dir" ] || exit 0

# Per-section toggles (default all on when context_inject.enabled: true)
_inject_section_enabled() {
    local key="$1"
    # If explicitly set to false, skip
    if grep -A 10 '^[[:space:]]*context_inject:' "$yakos_yml" 2>/dev/null \
        | grep -q "^[[:space:]]*$key:[[:space:]]*false[[:space:]]*$"; then
        return 1
    fi
    return 0
}

emitted_anything=0

# --- 1. Recent decisions ---
if _inject_section_enabled inject_decisions; then
    if [ -f "$current_dir/decisions.md" ]; then
        echo "[yakos context-inject] Recent decisions (decisions.md tail):" >&2
        tail -n 20 "$current_dir/decisions.md" | sed 's/^/  /' >&2
        echo "" >&2
        emitted_anything=1
    fi
fi

# --- 2. Budget remaining ---
if _inject_section_enabled inject_budget; then
    state="$current_dir/.budget-state.json"
    if [ -f "$state" ] && command -v jq >/dev/null 2>&1; then
        count="$(jq -r '.tool_call_count // 0' "$state" 2>/dev/null)"
        started="$(jq -r '.started_at // 0' "$state" 2>/dev/null)"
        elapsed=$(($(date +%s) - started))
        # Read caps
        max_calls="$(grep -A 10 '^[[:space:]]*budget:' "$yakos_yml" 2>/dev/null \
            | grep -E '^[[:space:]]*max_tool_calls:' | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
        max_wall="$(grep -A 10 '^[[:space:]]*budget:' "$yakos_yml" 2>/dev/null \
            | grep -E '^[[:space:]]*max_wall_seconds:' | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
        if [ -n "$max_calls" ] || [ -n "$max_wall" ]; then
            echo "[yakos context-inject] Budget:" >&2
            [ -n "$max_calls" ] && echo "  tool calls: $count / $max_calls" >&2
            [ -n "$max_wall" ] && echo "  wall-clock: ${elapsed}s / ${max_wall}s" >&2
            echo "" >&2
            emitted_anything=1
        fi
    fi
fi

# --- 3. Peer status (multi-dev) ---
if _inject_section_enabled inject_peers; then
    if command -v yakos_coord_enabled >/dev/null 2>&1 && yakos_coord_enabled; then
        sessions_dir="$(yakos_coord_sessions_dir 2>/dev/null)"
        if [ -d "$sessions_dir" ]; then
            n="$(find "$sessions_dir" -name '*.ndjson' 2>/dev/null | wc -l | tr -d ' ')"
            if [ "${n:-0}" -gt 1 ]; then
                echo "[yakos context-inject] Multi-dev: $n peer session(s) active." >&2
                echo "  Run 'yakos peer status' for details, 'yakos peer log' for recent activity." >&2
                echo "" >&2
                emitted_anything=1
            fi
        fi
    fi
fi

# --- 4. Recent supervisor finding ---
if _inject_section_enabled inject_supervisor; then
    findings="$current_dir/supervisor-findings.ndjson"
    if [ -f "$findings" ] && command -v jq >/dev/null 2>&1; then
        last_critical="$(jq -c 'select(.overall == "CRITICAL")' "$findings" 2>/dev/null | tail -n 1)"
        if [ -n "$last_critical" ]; then
            ts="$(printf '%s' "$last_critical" | jq -r '.ts')"
            rationale="$(printf '%s' "$last_critical" | jq -r '.rationale')"
            echo "[yakos context-inject] Most recent supervisor CRITICAL ($ts):" >&2
            echo "  $rationale" >&2
            echo "  (review: cat $findings | jq 'select(.ts == \"$ts\")')" >&2
            echo "" >&2
            emitted_anything=1
        fi
    fi
fi

# Audit log entry (REPORT-level; quiet)
if [ "$emitted_anything" = "1" ]; then
    ho_log "context-inject" "REPORT" "pass" "injected session context" \
        "$(jq -nc '{emitted: true}')"
else
    ho_log "context-inject" "REPORT" "pass" "no context to inject" \
        "$(jq -nc '{emitted: false}')"
fi

exit 0
