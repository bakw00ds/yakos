#!/usr/bin/env bash
# runtimes/claude.sh — Claude Code adapter.
#
# Purpose: implement the runtime contract (cli/lib/runtimes/README.md)
# for `claude`. This is the original yakOS target; the adapter wraps
# the launch path that previously lived directly in cli/lib/start.sh.
#
# Capabilities: inline-agents, path-allowlist-hard, hooks, mcp-flag,
# system-prompt-flag, fork-headless.

set -eu

: "${YAKOS_LIB:?claude.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

yk_rt_claude_id() { printf 'claude\n'; }

yk_rt_claude_capabilities() {
    printf 'inline-agents,path-allowlist-hard,hooks,mcp-flag,system-prompt-flag,fork-headless\n'
}

yk_rt_claude_check_cli() {
    if command -v claude >/dev/null 2>&1; then
        return 0
    fi
    ct_log "claude: 'claude' CLI not on PATH (https://docs.claude.com/en/docs/claude-code)"
    return 1
}

# yk_rt_claude_check_auth
#   Claude Code uses ~/.claude/auth.json (OAuth) or
#   ANTHROPIC_API_KEY env. Heuristic: presence of either is "configured".
yk_rt_claude_check_auth() {
    if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
        return 0
    fi
    if [ -f "$HOME/.claude/auth.json" ]; then
        return 0
    fi
    # Fallback: claude can stash auth in keychain (macOS); we can't
    # reliably probe that without launching claude. Treat the binary's
    # presence as "auth probably set up" if the binary exists.
    if command -v claude >/dev/null 2>&1; then
        return 0
    fi
    ct_log "claude: no auth configured (run 'yakos auth login claude')"
    return 1
}

# yk_rt_claude_materialize_agents <yakos-root> <project> <out-dir>
#   Claude uses --agents JSON injection at launch — no on-disk file.
#   This is a no-op; the JSON is composed at launch via agents-compose.
#   Returns nothing on stdout (the launch path queries agents-compose
#   directly).
yk_rt_claude_materialize_agents() {
    : # no-op
}

yk_rt_claude_cleanup_agents() {
    : # no-op (nothing was written)
}

# yk_rt_claude_launch <project> <perm-mode> [extra-flags...]
#   exec claude with --add-dir <project>, --permission-mode <translated>,
#   and --agents <json-from-agents-compose>. Extra flags pass through
#   verbatim (--continue, --resume, etc.).
yk_rt_claude_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift
    local cli_perm
    case "$perm_mode" in
        bypass) cli_perm="bypassPermissions" ;;
        safe)   cli_perm="default" ;;
        *) ct_die "claude_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    local agents_json
    agents_json="$(yk_agents_compose "$YAKOS_ROOT" "$project")"
    local agent_count
    agent_count="$(printf '%s' "$agents_json" | jq 'length')"

    local args=( --add-dir "$project" --permission-mode "$cli_perm" )
    if [ "$agent_count" -gt 0 ]; then
        args+=( --agents "$agents_json" )
    fi

    # Auto-detect <project>/.mcp.json
    if [ -f "$project/.mcp.json" ]; then
        args+=( --mcp-config "$project/.mcp.json" )
    fi

    # Append caller's extra flags.
    if [ "$#" -gt 0 ]; then
        args+=( "$@" )
    fi

    exec claude "${args[@]}"
}

# yk_rt_claude_dispatch <project> <agent-name> <task-prompt>
#   One-shot dispatch via `claude -p`. The agent body becomes the
#   --agents JSON payload, and the task is sent as the prompt.
#   Stdout is the captured response; exit code is claude's.
yk_rt_claude_dispatch() {
    local project="$1"
    local agent_name="$2"
    local task="$3"

    local agents_json
    agents_json="$(yk_agents_compose "$YAKOS_ROOT" "$project")"

    local single
    single="$(printf '%s' "$agents_json" | jq --arg n "$agent_name" \
        'if has($n) then {($n): .[$n]} else null end')"
    if [ "$single" = "null" ] || [ -z "$single" ]; then
        ct_die "claude_dispatch: agent '$agent_name' not found in composed set"
    fi

    local framed
    framed="Use the Agent tool to dispatch the following task to subagent_type=\"$agent_name\". Return only the subagent's final report.

Task:
$task"

    # If the caller (dispatch.sh) set YAKOS_USAGE_OUT, run claude with
    # --output-format stream-json, parse the final result event for
    # actual token usage, write to that path, and reconstruct the
    # text-only response for stdout.
    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        local raw_tmp
        raw_tmp="$(mktemp -t yakos-claude-raw.XXXXXX)"

        claude --agents "$single" \
               --permission-mode bypassPermissions \
               --add-dir "$project" \
               --output-format stream-json \
               --verbose \
               -p "$framed" > "$raw_tmp" 2>/dev/null
        local rc=$?

        # Extract assistant text and the final result event's usage.
        jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "text") | .text' \
            "$raw_tmp" 2>/dev/null
        jq -c 'select(.type == "result") | {input_tokens: .usage.input_tokens,
            output_tokens: .usage.output_tokens,
            cache_read: .usage.cache_read_input_tokens,
            cache_creation: .usage.cache_creation_input_tokens,
            duration_ms: .duration_ms,
            total_cost_usd: .total_cost_usd}' \
            "$raw_tmp" 2>/dev/null | tail -1 > "$YAKOS_USAGE_OUT"

        rm -f "$raw_tmp" 2>/dev/null
        return "$rc"
    fi

    # Default path — text-only, no telemetry capture.
    claude --agents "$single" \
           --permission-mode bypassPermissions \
           --add-dir "$project" \
           -p "$framed"
}
