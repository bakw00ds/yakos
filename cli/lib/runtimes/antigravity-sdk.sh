#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: runtimes/antigravity-sdk.sh — Google Antigravity SDK (Python) adapter.
#
# Headless-dispatch-only adapter alongside agy.sh. Uses the
# google-antigravity Python package
# (https://github.com/google-antigravity/antigravity-sdk-python). The
# SDK bundles a compiled runtime binary in its platform wheels and
# exposes a high-level Agent context manager + lower-level Conversation
# API. The value over agy.sh is programmatic agent loop control
# (in-process tools as Python callables, declarative policy system)
# rather than text-streaming from a forked CLI process.
#
# launch verb delegates to agy.sh — the SDK is headless; humans use
# the agy TUI for interactive sessions. dispatch verb uses Agent.chat()
# via a small async Python script.
#
# Verified against the upstream SDK (examples/getting_started/
# {subagents.py,observability.py,persona_config.py} at v0.26):
#   package           google-antigravity (pip install google-antigravity)
#   import            from google.antigravity import Agent, LocalAgentConfig
#                     from google.antigravity import types (CapabilitiesConfig,
#                                                            BuiltinTools, ...)
#   async only        uses asyncio
#   config            LocalAgentConfig(system_instructions=, api_key=,
#                                       capabilities=, tools=, mcp_servers=,
#                                       policies=, triggers=, hooks=)
#   default policy    READ-ONLY — adapter passes CapabilitiesConfig() to enable writes
#   sub-agents        CapabilitiesConfig(enable_subagents=True); agent
#                     autonomously spawns via BuiltinTools.START_SUBAGENT
#   usage/cost        agent.conversation.total_usage exposes prompt_token_count /
#                     response_token_count / total_token_count / thoughts_token_count
#                     (per examples/getting_started/observability.py)
#   auth              GEMINI_API_KEY env var or config api_key=
#   model param       NOT documented at v0.26 (SDK chooses Gemini model)
#   cwd param         NOT documented — adapter sets python process cwd before
#                     constructing Agent
#   tool name model   antigravity-native names (view_file, run_command, ...) not
#                     claude-style (Edit/Write/Bash); yakOS allowed_tools does
#                     not passthrough — adapter opts into CapabilitiesConfig()
#                     and relies on yakOS path-allowlist hooks (same posture as
#                     agy CLI)

set -eu

: "${YAKOS_LIB:?antigravity-sdk.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./agy.sh
. "$YAKOS_LIB/runtimes/agy.sh"

# ---------------------------------------------------------------------------
# id / capabilities
# ---------------------------------------------------------------------------

yk_rt_antigravity_sdk_id() { printf 'antigravity-sdk\n'; }

yk_rt_antigravity_sdk_capabilities() {
    # vs agy.sh: programmatic-agents (Agent context manager, not forked
    # CLI process); sub-agents (CapabilitiesConfig(enable_subagents=True)
    # — agent autonomously spawns subagents ad-hoc); native-telemetry
    # (agent.conversation.total_usage exposes token counts at session
    # end); mcp-in-process via McpStdioServer; declarative-policies for
    # the deny/allow/ask_user/enforce system.
    printf 'programmatic-agents,sub-agents,native-telemetry,path-allowlist-soft,mcp-in-process,declarative-policies,headless-only,async-asyncio\n'
}

# ---------------------------------------------------------------------------
# CLI / auth presence checks
# ---------------------------------------------------------------------------

yk_rt_antigravity_sdk_check_cli() {
    command -v python3 >/dev/null 2>&1 || {
        ct_log "antigravity-sdk: python3 not on PATH"
        return 1
    }
    if python3 -c 'from google.antigravity import Agent' 2>/dev/null; then
        return 0
    fi
    ct_log "antigravity-sdk: Google Antigravity SDK not installed"
    ct_log "antigravity-sdk: install: pip install google-antigravity"
    ct_log "antigravity-sdk: (compiled binary ships in the PyPI wheel;"
    ct_log "                 cloning the repo alone is insufficient)"
    return 1
}

yk_rt_antigravity_sdk_check_auth() {
    # SDK auth is GEMINI_API_KEY env var or LocalAgentConfig(api_key=...).
    # README does not document keychain sharing with the agy CLI; we don't
    # assume credential carry-over. If GEMINI_API_KEY is set we're good;
    # otherwise the operator must wire api_key= in their session.
    if [ -n "${GEMINI_API_KEY:-}" ]; then
        return 0
    fi
    # Fallback: if agy CLI is authenticated, the operator likely has a
    # working Google Cloud / Vertex setup. Log a NOTE — not a hard pass —
    # so dispatch surfaces the auth gap clearly at first use rather than
    # masking it as OK.
    if yk_rt_agy_check_auth 2>/dev/null; then
        ct_log "antigravity-sdk: GEMINI_API_KEY not set; agy CLI auth detected"
        ct_log "antigravity-sdk: SDK README does not document keychain reuse;"
        ct_log "                 export GEMINI_API_KEY to be safe"
        return 1
    fi
    return 1
}

# ---------------------------------------------------------------------------
# Agent materialization — no on-disk artifacts (SDK consumes the composed
# JSON directly via env at dispatch time)
# ---------------------------------------------------------------------------

yk_rt_antigravity_sdk_materialize_agents() {
    :
}

yk_rt_antigravity_sdk_cleanup_agents() {
    :
}

# ---------------------------------------------------------------------------
# Launch — delegate to agy.sh (SDK is headless-only)
# ---------------------------------------------------------------------------

yk_rt_antigravity_sdk_launch() {
    ct_log "antigravity-sdk: SDK adapter is headless-only; delegating launch to agy.sh"
    yk_rt_agy_launch "$@"
}

# ---------------------------------------------------------------------------
# Dispatch — invokes the Python async script
# ---------------------------------------------------------------------------

yk_rt_antigravity_sdk_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    local composed
    composed="$(yk_agents_compose "$YAKOS_ROOT" "$project")"
    [ -n "$composed" ] || ct_die "antigravity-sdk: no agents composed"

    local agent_exists
    agent_exists="$(printf '%s' "$composed" | jq --arg n "$agent_name" 'has($n)')"
    [ "$agent_exists" = "true" ] || ct_die "antigravity-sdk: agent '$agent_name' not in composition"

    local dispatch_script="$YAKOS_LIB/runtimes/antigravity-sdk-dispatch.py"
    [ -f "$dispatch_script" ] || ct_die "antigravity-sdk: dispatch script missing at $dispatch_script"

    YAKOS_AGENT_ID="$agent_name" \
    YAKOS_PROJECT_DIR="$project" \
    YAKOS_AGENTS_JSON="$composed" \
    python3 "$dispatch_script" <<<"$task"
}
