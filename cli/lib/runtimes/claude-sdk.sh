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
#   auth              dispatch REQUIRES ANTHROPIC_API_KEY (K-137, see the gate below);
#                     launch is claude.sh, so check_auth stays claude's chain

set -eu

: "${YAKOS_LIB:?claude-sdk.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./claude.sh
. "$YAKOS_LIB/runtimes/claude.sh"

# Interpreter override: YAKOS_PYTHON lets the operator point at a venv
# python that has claude-agent-sdk installed (e.g. a pipx venv).
# Defaults to bare python3 for backward compatibility.
YK_PY="${YAKOS_PYTHON:-python3}"

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
    # Accept both a bare name (resolved via PATH) and an absolute path.
    case "$YK_PY" in
        /*) [ -x "$YK_PY" ] ;;
        *)  command -v "$YK_PY" >/dev/null 2>&1 ;;
    esac || {
        ct_log "claude-sdk: python interpreter not found: $YK_PY"
        return 1
    }
    if "$YK_PY" -c 'import claude_agent_sdk' 2>/dev/null; then
        return 0
    fi
    ct_log "claude-sdk: Anthropic Claude Agent SDK not installed"
    ct_log "claude-sdk: install: pip install claude-agent-sdk"
    ct_log "claude-sdk: requires python 3.10+"
    return 1
}

yk_rt_claude_sdk_check_auth() {
    # Deliberately claude.sh's chain (ANTHROPIC_API_KEY env or ~/.claude/auth.json
    # or keyring), not the stricter API-key gate below: launch delegates to
    # claude.sh, which is Claude Code itself and runs fine on a subscription
    # login, and `yakos start --runtime claude-sdk` reports its auth through
    # this function. The SDK itself is gated where it runs, in
    # yk_rt_claude_sdk_dispatch.
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
# API-key gate (K-137) — the bash twin of the Go SDK sidecar gate
# ---------------------------------------------------------------------------
#
# Anthropic's terms (2026-02-19) allow a Pro or Max subscription's OAuth only in
# Claude Code and claude.ai, not in "any other product, including the Agent SDK".
# This runtime IS the Agent SDK (the Python package), and its auth used to be
# "the same chain as claude.sh", which includes the operator's claude.ai login.
# dispatch therefore refuses to run unless ANTHROPIC_API_KEY holds an API key,
# and the python it starts never inherits subscription OAuth variables. The
# claude runtime (Claude Code itself) stays the path for subscription users.
#
# Twins: cli-go/internal/runtime/sdk_env.go (SDKSidecarEnv) for the Go sidecar,
# and claude-sdk-dispatch.py, which repeats the check as a second anchor.
# Markers match both OAuth access tokens (sk-ant-oat...) and refresh tokens
# (sk-ant-ort...), in any case, anywhere in a value.

# _yk_rt_claude_sdk_is_oauth_value <value>
#   Exit 0 when the value contains a subscription OAuth token marker.
_yk_rt_claude_sdk_is_oauth_value() {
    case "$1" in
        *[Ss][Kk]-[Aa][Nn][Tt]-[Oo][Aa][Tt]*|*[Ss][Kk]-[Aa][Nn][Tt]-[Oo][Rr][Tt]*) return 0 ;;
    esac
    return 1
}

# yk_rt_claude_sdk_key_state
#   Print ok, unset or oauth for ANTHROPIC_API_KEY: an API key, nothing usable
#   (missing or blank), or a subscription OAuth token in the key slot. Never
#   prints any part of the value. `yakos auth status claude-sdk` and the dispatch
#   gate below both read it.
yk_rt_claude_sdk_key_state() {
    local key="${ANTHROPIC_API_KEY:-}"
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    if [ -z "$key" ]; then
        printf 'unset\n'
    elif _yk_rt_claude_sdk_is_oauth_value "$key"; then
        printf 'oauth\n'
    else
        printf 'ok\n'
    fi
}

# yk_rt_claude_sdk_key_refusal
#   Exit 0, printing nothing, when ANTHROPIC_API_KEY holds an API key. Otherwise
#   print the one-line reason on stdout and exit 1. Never prints any part of the
#   value.
yk_rt_claude_sdk_key_refusal() {
    case "$(yk_rt_claude_sdk_key_state)" in
        ok)
            return 0
            ;;
        oauth)
            printf '%s\n' "claude-sdk: refusing to run: ANTHROPIC_API_KEY holds a subscription OAuth token, not an API key; the Agent SDK does not accept those (set an API key, or use the claude runtime, which is Claude Code itself)"
            ;;
        *)
            printf '%s\n' "claude-sdk: refusing to run: ANTHROPIC_API_KEY is not set; the Agent SDK does not run on a claude.ai subscription login (set an API key, or use the claude runtime, which is Claude Code itself)"
            ;;
    esac
    return 1
}

# _yk_rt_claude_sdk_is_oauth_name <name>
#   Exit 0 for CLAUDE_CODE_OAUTH* in any case (CLAUDE_CODE_OAUTH_TOKEN and its
#   siblings: refresh token, scopes, client id).
_yk_rt_claude_sdk_is_oauth_name() {
    case "$1" in
        [Cc][Ll][Aa][Uu][Dd][Ee]_[Cc][Oo][Dd][Ee]_[Oo][Aa][Uu][Tt][Hh]*) return 0 ;;
    esac
    return 1
}

# yk_rt_claude_sdk_oauth_env_names
#   Print, one per line, the NAME of every exported variable that carries
#   subscription OAuth material: any CLAUDE_CODE_OAUTH* name in any case, and any
#   variable whose value contains an OAuth token marker. Names only, never values.
#
#   The environment is read as NUL-delimited `env -0` entries, not from `compgen
#   -e`, because compgen lists only names a shell can hold as variables: bash 5
#   still hands a variable named A.B to its children, so a token in one would
#   pass. Where `env -0` is unavailable it falls back to compgen -e, which covers
#   identifier names only. A name that holds a newline cannot be listed one per
#   line and is skipped.
yk_rt_claude_sdk_oauth_env_names() {
    local entry name
    if env -0 >/dev/null 2>&1; then
        while IFS= read -r -d '' entry; do
            name="${entry%%=*}"
            [ -n "$name" ] || continue # a Windows "=C:=C:\..." entry has no name to unset
            case "$name" in *$'\n'*) continue ;; esac
            if _yk_rt_claude_sdk_is_oauth_name "$name" || _yk_rt_claude_sdk_is_oauth_value "${entry#*=}"; then
                printf '%s\n' "$name"
            fi
        done < <(env -0)
    else
        for name in $(compgen -e); do
            if _yk_rt_claude_sdk_is_oauth_name "$name" || _yk_rt_claude_sdk_is_oauth_value "${!name-}"; then
                printf '%s\n' "$name"
            fi
        done
    fi
}

# ---------------------------------------------------------------------------
# Dispatch — invokes the Python async script
# ---------------------------------------------------------------------------

yk_rt_claude_sdk_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    # K-137 hard gate: before anything is composed or started.
    local refusal
    if ! refusal="$(yk_rt_claude_sdk_key_refusal)"; then
        ct_die "$refusal"
    fi

    local composed
    composed="$(yk_agents_compose "$YAKOS_ROOT" "$project")"
    [ -n "$composed" ] || ct_die "claude-sdk: no agents composed"

    local agent_exists
    agent_exists="$(printf '%s' "$composed" | jq --arg n "$agent_name" 'has($n)')"
    [ "$agent_exists" = "true" ] || ct_die "claude-sdk: agent '$agent_name' not in composition"

    local dispatch_script="$YAKOS_LIB/runtimes/claude-sdk-dispatch.py"
    [ -f "$dispatch_script" ] || ct_die "claude-sdk: dispatch script missing at $dispatch_script"

    # The python (and the Claude Code CLI the SDK bundles) must not inherit
    # subscription OAuth material. `env -u` removes it from the child only.
    local -a scrub=()
    local oauth_name
    while IFS= read -r oauth_name; do
        scrub+=( -u "$oauth_name" )
    done < <(yk_rt_claude_sdk_oauth_env_names)

    YAKOS_AGENT_ID="$agent_name" \
    YAKOS_PROJECT_DIR="$project" \
    YAKOS_AGENTS_JSON="$composed" \
    env ${scrub[@]+"${scrub[@]}"} "$YK_PY" "$dispatch_script" <<<"$task"
}
