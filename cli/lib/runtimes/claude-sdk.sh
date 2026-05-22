#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: runtimes/claude-sdk.sh — Anthropic Claude Agent SDK (Python) adapter.
#
# Headless-dispatch-only adapter alongside claude.sh. Uses the
# claude-agent-sdk Python package
# (https://github.com/anthropics/claude-agent-sdk-python). The SDK
# itself bundles the Claude Code CLI under the hood; the value over
# claude.sh is programmatic agent loop control (async query() +
# ClaudeSDKClient bidirectional client) rather than text-streaming
# from a forked CLI process.
#
# launch verb delegates to claude.sh — the SDK is headless; humans use
# the CLI for interactive sessions. dispatch verb uses query() via a
# small async Python script.
#
# Verified against the upstream SDK (examples/agents.py + types.py;
# README is incomplete — types.py is the source of truth at v0.26):
#   package           claude-agent-sdk (pip install claude-agent-sdk)
#   import            from claude_agent_sdk import query, AgentDefinition,
#                                                 ClaudeAgentOptions, ResultMessage,
#                                                 AssistantMessage, TextBlock
#   async only        uses anyio
#   top-level options system_prompt, tools (base set), allowed_tools (auto-allow),
#                     model, cwd, add_dirs, mcp_servers, setting_sources,
#                     agents (sub-agent dict)
#   AgentDefinition   description, prompt, tools, model, disallowedTools,
#                     skills, memory, mcpServers, initialPrompt, maxTurns
#   ResultMessage     total_cost_usd, duration_ms, duration_api_ms, num_turns,
#                     session_id, stop_reason, usage, model_usage,
#                     permission_denials  (real telemetry — not the
#                     "probe and hope" of v0.24)
#   model param       PRESENT — top-level options.model AND per-agent
#                     AgentDefinition.model (alias: sonnet|opus|haiku|inherit
#                     OR full model id)
#   sub-agents        via options.agents={"name": AgentDefinition(...)} dict
#   auth              bundled CLI handles credentials; same chain as claude.sh

set -eu

: "${YAKOS_LIB:?claude-sdk.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./claude.sh
. "$YAKOS_LIB/runtimes/claude.sh"

# ---------------------------------------------------------------------------
# id / capabilities
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_id() { printf 'claude-sdk\n'; }

yk_rt_claude_sdk_capabilities() {
    # vs claude.sh: programmatic-agents (async query iterator);
    # sub-agents (options.agents={} dict; dispatched agent can delegate
    # via Task to siblings); native-telemetry (ResultMessage.total_cost_usd
    # + .duration_ms + .num_turns + .usage); model-passthrough (per-agent
    # model via AgentDefinition); mcp-in-process for SDK-defined tools.
    printf 'programmatic-agents,sub-agents,native-telemetry,model-passthrough,path-allowlist-soft,mcp-in-process,headless-only,async-anyio\n'
}

# ---------------------------------------------------------------------------
# CLI / auth presence checks
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_check_cli() {
    # The "CLI" for an SDK adapter is the python package + interpreter.
    command -v python3 >/dev/null 2>&1 || {
        ct_log "claude-sdk: python3 not on PATH"
        return 1
    }
    if python3 -c 'import claude_agent_sdk' 2>/dev/null; then
        return 0
    fi
    ct_log "claude-sdk: Anthropic Claude Agent SDK not installed"
    ct_log "claude-sdk: install: pip install claude-agent-sdk"
    ct_log "claude-sdk: requires python 3.10+"
    return 1
}

yk_rt_claude_sdk_check_auth() {
    # SDK bundles Claude Code CLI; same credentials apply. Delegate to
    # claude.sh's auth check (ANTHROPIC_API_KEY env or ~/.claude/auth.json
    # or keyring; same chain).
    yk_rt_claude_check_auth
}

# ---------------------------------------------------------------------------
# Agent materialization — no on-disk artifacts (SDK consumes
# system_prompt + allowed_tools + cwd directly via ClaudeAgentOptions)
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_materialize_agents() {
    # No-op: dispatch script receives the composed JSON via YAKOS_AGENTS_JSON
    # env var and extracts per-agent prompt+tools at invocation time. Nothing
    # written to disk.
    :
}

yk_rt_claude_sdk_cleanup_agents() {
    :
}

# ---------------------------------------------------------------------------
# Launch — delegate to claude.sh (SDK is headless-only)
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_launch() {
    ct_log "claude-sdk: SDK adapter is headless-only; delegating launch to claude.sh"
    yk_rt_claude_launch "$@"
}

# ---------------------------------------------------------------------------
# Dispatch — invokes the Python async script
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    local composed
    composed="$(yk_agents_compose "$YAKOS_ROOT" "$project")"
    [ -n "$composed" ] || ct_die "claude-sdk: no agents composed"

    local agent_exists
    agent_exists="$(printf '%s' "$composed" | jq --arg n "$agent_name" 'has($n)')"
    [ "$agent_exists" = "true" ] || ct_die "claude-sdk: agent '$agent_name' not in composition"

    local dispatch_script="$YAKOS_LIB/runtimes/claude-sdk-dispatch.py"
    [ -f "$dispatch_script" ] || ct_die "claude-sdk: dispatch script missing at $dispatch_script"

    YAKOS_AGENT_ID="$agent_name" \
    YAKOS_PROJECT_DIR="$project" \
    YAKOS_AGENTS_JSON="$composed" \
    python3 "$dispatch_script" <<<"$task"
}
