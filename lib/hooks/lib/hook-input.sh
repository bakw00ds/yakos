#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: hook-input.sh — stdin JSON helpers shared by every YakOS hook script.
#
# Usage in a hook:
#     . "$HOOK_DIR/lib/hook-input.sh"
#     hi_init                      # reads stdin once into $HI_INPUT
#     agent="$(hi_sender_role)"
#     file="$(hi_file_path)"
#
# The "sender" identity follows Phase 1.7's finding: read .agent_type from
# stdin JSON. Lead-originated events have .agent_type absent — we map that
# to the literal string "lead". This is the canonical lead/teammate
# discriminator across all hook events (Phase 0 Test 7 + Phase 1.7).

if [ "${HI_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
HI_LOADED=1

HI_INPUT=""

hi_init() {
    # Slurp stdin once. Subsequent hi_* calls query $HI_INPUT via jq.
    if [ -t 0 ]; then
        # No stdin provided — leave HI_INPUT empty so callers can decide
        # what to do. Most hooks should treat this as a no-op pass.
        HI_INPUT=""
    else
        HI_INPUT="$(cat)"
    fi
}

hi_field() {
    # Print the (string) value at jq path $1, or empty if missing/null.
    [ -n "$HI_INPUT" ] || { printf ''; return; }
    jq -r "$1 // empty" <<< "$HI_INPUT" 2>/dev/null || true
}

hi_field_or() {
    # Print field $1's value, or fallback $2 if missing/empty.
    local v
    v="$(hi_field "$1")"
    if [ -n "$v" ]; then
        printf '%s' "$v"
    else
        printf '%s' "$2"
    fi
}

hi_raw() {
    # Echo the whole input JSON (for hooks that pipe it onward).
    printf '%s' "$HI_INPUT"
}

# ---- common field shortcuts ------------------------------------------------

hi_event()        { hi_field '.hook_event_name'; }
hi_tool()         { hi_field '.tool_name'; }
hi_session_id()   { hi_field '.session_id'; }
hi_transcript()   { hi_field '.transcript_path'; }
hi_cwd()          { hi_field '.cwd'; }

# hi_strip_rt_prefix <value>
#   Strip the leading "yakos:" namespace prefix that claude attaches when
#   agents are registered via --plugin-dir with the "yakos" plugin name
#   (E2BIG fix, runtimes/claude.sh). Returns the bare agent id.
#   Example: "yakos:backend" → "backend", "lead" → "lead".
#   Only strips the exact "yakos:" prefix — does not touch other colons
#   (e.g. a project agent legitimately named "myns:helper" is left alone).
hi_strip_rt_prefix() {
    local v="$1"
    case "$v" in
        yakos:*) printf '%s' "${v#yakos:}" ;;
        *)        printf '%s' "$v" ;;
    esac
}

# Sender role: "lead" if .agent_type absent, otherwise the agent_type string
# with any "yakos:" namespace prefix stripped (see hi_strip_rt_prefix).
# Reading from stdin JSON, NOT $CLAUDE_CODE_AGENT (per Phase 1.7: env var
# is missing in team SendMessage hook fires).
hi_sender_role() {
    local raw
    raw="$(hi_field_or '.agent_type' 'lead')"
    hi_strip_rt_prefix "$raw"
}

# Tool-specific shortcuts
hi_file_path()    { hi_field '.tool_input.file_path'; }
hi_content()      { hi_field '.tool_input.content'; }      # Write
hi_new_string()   { hi_field '.tool_input.new_string'; }   # Edit

# SendMessage shortcuts (canonical names per Phase 1.7)
hi_msg_to()       { hi_field '.tool_input.to'; }
hi_msg_summary()  { hi_field '.tool_input.summary'; }
hi_msg_body()     { hi_field '.tool_input.message'; }
