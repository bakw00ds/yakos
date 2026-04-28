#!/usr/bin/env bash
# team-lifecycle.sh — PreToolUse on TeamCreate|Agent.
#
# Audit-only in v0.1: records every team creation and teammate spawn to
# the structured log. Always exits 0. Phase 1.7 confirmed wildcard
# PreToolUse captures these tool calls; hookability is solid.
#
# Future versions could enforce team-shape policy (which roles are allowed
# to spawn which teammates) by exiting 2 here.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    TeamCreate|Agent) ;;
    *) exit 0 ;;
esac

agent="$(hi_sender_role)"
# tool_input shape varies between TeamCreate and Agent; capture verbatim
tool_input="$(printf '%s' "$(hi_raw)" | jq -c '.tool_input // {}' 2>/dev/null || echo '{}')"

extra="$(jq -nc --arg agent "$agent" --arg tool "$tool" --argjson input "$tool_input" \
    '{caller: $agent, tool: $tool, tool_input: $input}')"
ho_log "team-lifecycle" "REPORT" "pass" "team-lifecycle event" "$extra"

exit 0
