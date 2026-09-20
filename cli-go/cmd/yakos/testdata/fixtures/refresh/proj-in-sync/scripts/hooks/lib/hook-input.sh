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
#
# --- HOOK_FAIL_CLOSED (security review C5) ----------------------------------
#
# hi_init validates that jq is on PATH and that stdin (when present) parses
# as JSON. Historically, a missing jq or malformed stdin left $HI_INPUT
# effectively empty, every hi_* accessor returned "", and every hook's
# `case "$tool" in ... *) exit 0 ;; esac` fell through to a silent PASS —
# i.e. the enforcement hooks (path-allowlist, secret-scan, supervisor-gate,
# supervisor-ack-gate, budget-guard, peer-claim, plan-quality-gate) degraded
# to no-ops exactly when the operator believed they were still running.
#
# A hook that BLOCKS (calls ho_block / can exit 2) must set
# `HOOK_FAIL_CLOSED=1` before sourcing this file:
#
#     HOOK_FAIL_CLOSED=1
#     . "$HOOK_DIR/lib/hook-input.sh"
#
# With that set, hi_init exits 2 (with a stderr explanation) instead of
# silently continuing with empty input. Purely observational hooks
# (path-log, mailbox-mirror, team-lifecycle, session-end-check, telemetry
# branches of output-injection-scan/task-dependency-gate/
# task-complete-dispatch, which never call ho_block) must NOT set
# HOOK_FAIL_CLOSED — they keep today's fail-open-with-a-warning behavior,
# per the README's "no-block policy for telemetry hooks".
#
# See lib/hooks/README.md for the full writeup.

if [ "${HI_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
HI_LOADED=1

HI_INPUT=""

# hi_init's own degraded-input handler. Kept separate from hi_init so it can
# be unit-exercised and so the control flow in hi_init stays readable.
_hi_fail_or_warn() {
    local reason="$1"
    local name
    name="$(basename -- "${0:-hook}" 2>/dev/null || echo hook)"
    name="${name%.sh}"

    if [ "${HOOK_FAIL_CLOSED:-0}" = "1" ]; then
        # Best-effort log record before we exit — ho_log degrades gracefully
        # when jq itself is the thing that's missing (see hook-output.sh).
        if command -v ho_log >/dev/null 2>&1; then
            ho_log "$name" "BLOCK" "block" "degraded input, failing closed: $reason" "{}" 2>/dev/null || true
        fi
        echo "${name}: BLOCKED — cannot safely evaluate this tool call ($reason)." >&2
        echo "${name}: this hook enforces a security control and refuses to fail open." >&2
        echo "${name}: fix jq on PATH / the caller's JSON payload, then retry." >&2
        exit 2
    fi

    echo "${name}: WARN — $reason. This hook is degraded for this event (jq unavailable or stdin unparseable); treating input as empty." >&2
}

hi_init() {
    # Slurp stdin once. Subsequent hi_* calls query $HI_INPUT via jq.
    if [ -t 0 ]; then
        # No stdin provided — leave HI_INPUT empty so callers can decide
        # what to do. Most hooks should treat this as a no-op pass. This
        # is the deliberate "run me by hand with no input" case, distinct
        # from "stdin was provided but is broken" below, so it is not
        # subject to HOOK_FAIL_CLOSED.
        HI_INPUT=""
        return 0
    fi

    HI_INPUT="$(cat)"

    if ! command -v jq >/dev/null 2>&1; then
        HI_INPUT=""
        _hi_fail_or_warn "jq is not installed or not on PATH"
        return 0
    fi

    if [ -n "$HI_INPUT" ] && ! jq empty <<< "$HI_INPUT" >/dev/null 2>&1; then
        HI_INPUT=""
        _hi_fail_or_warn "stdin did not parse as valid JSON"
        return 0
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
#
# hi_file_path falls back to .tool_input.notebook_path (C4: NotebookEdit
# carries its target under a different key than Edit/Write/MultiEdit) so
# every path-based hook gets a usable path without special-casing the tool.
hi_file_path()    { hi_field '.tool_input.file_path // .tool_input.notebook_path'; }
hi_content()      { hi_field '.tool_input.content'; }        # Write
hi_new_string()   { hi_field '.tool_input.new_string'; }     # Edit
hi_new_source()   { hi_field '.tool_input.new_source'; }     # NotebookEdit
hi_notebook_path() { hi_field '.tool_input.notebook_path'; } # NotebookEdit

# SendMessage shortcuts (canonical names per Phase 1.7)
hi_msg_to()       { hi_field '.tool_input.to'; }
hi_msg_summary()  { hi_field '.tool_input.summary'; }
hi_msg_body()     { hi_field '.tool_input.message'; }
