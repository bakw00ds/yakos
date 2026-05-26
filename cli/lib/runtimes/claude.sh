#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# runtimes/claude.sh — Claude Code adapter.
#
# Purpose: implement the runtime contract (cli/lib/runtimes/README.md)
# for `claude`. This is the original yakOS target; the adapter wraps
# the launch path that previously lived directly in cli/lib/start.sh.
#
# Capabilities: inline-agents, path-allowlist-hard, hooks, mcp-flag,
# system-prompt-flag, fork-headless.
#
# Agent-registration mechanism (LAUNCH path):
#   The composed agent roster is materialized as per-agent .md files in a
#   session-scoped plugin directory and registered via `claude --plugin-dir`.
#   This avoids passing the full roster (~167KB) as a single argv string,
#   which exceeds Linux's per-arg cap (MAX_ARG_STRLEN = 131072 bytes on
#   4K-page kernels) and causes E2BIG on exec.
#
#   Agents registered via --plugin-dir carry a namespace prefix:
#   the plugin name is "yakos", so subagent_type values become
#   "yakos:<id>" (e.g. "yakos:backend"). The lead discovers available
#   agent names from its Agent tool at runtime and dispatches accordingly.
#
#   DISPATCH path (yk_rt_claude_dispatch) still uses --agents with a single
#   agent's JSON (~5KB), which is well under the 128KB per-arg cap.
#   Do not apply the plugin-dir approach there.
#
# Plugin dir layout:
#   <work-current>/.yakos-agents-plugin/
#     .claude-plugin/plugin.json     — manifest (name, description)
#     agents/<id>.md                 — one file per composed agent

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

# yk_rt_claude_plugin_dir_path <project>
#   Print the deterministic session-scoped plugin directory path for
#   <project>. Derived from YAKOS_WORK_DIR (if set) or the canonical
#   $HOME/agent-control/<project-name>/work layout, matching paths.sh.
#   This is a pure path resolver — it does not create or remove anything.
yk_rt_claude_plugin_dir_path() {
    local project="$1"
    local work_current
    if [ -n "${YAKOS_WORK_DIR:-}" ]; then
        work_current="${YAKOS_WORK_DIR}/current"
    else
        local proj_name
        proj_name="$(basename -- "$project")"
        work_current="$HOME/agent-control/${proj_name}/work/current"
    fi
    printf '%s/.yakos-agents-plugin' "$work_current"
}

# yk_rt_claude_write_plugin_dir <plugin-dir> <agents-json>
#   Materialize <agents-json> (composed JSON object) as a --plugin-dir
#   layout under <plugin-dir>:
#     .claude-plugin/plugin.json   — manifest
#     agents/<id>.md               — one file per agent with native
#                                    frontmatter (name, description,
#                                    tools, model) and prompt body.
#
#   Rebuilds the directory from scratch on each call (rm -rf + mkdir).
#   Safe to call before exec because it completes before exec fires.
#   Returns the count of agent files written on stdout.
yk_rt_claude_write_plugin_dir() {
    local plugin_dir="$1"
    local agents_json="$2"

    # Wipe and recreate to prevent stale files from a prior session.
    rm -rf "$plugin_dir" 2>/dev/null || true
    mkdir -p "$plugin_dir/.claude-plugin" "$plugin_dir/agents"

    # Write the plugin manifest. Name "yakos" causes agents to register
    # as subagent_type "yakos:<id>" in the claude session.
    printf '{"name":"yakos","description":"yakOS agent roster (session-scoped; auto-generated)"}\n' \
        > "$plugin_dir/.claude-plugin/plugin.json"

    # Iterate over each agent id in the composed JSON and emit an .md file.
    local agent_count=0
    local ids
    ids="$(printf '%s' "$agents_json" | jq -r 'keys[]')"

    local id
    for id in $ids; do
        local agent_obj desc prompt tools_arr tools_str model
        agent_obj="$(printf '%s' "$agents_json" | jq --arg id "$id" '.[$id]')"

        desc="$(printf '%s' "$agent_obj" | jq -r '.description // ""')"
        prompt="$(printf '%s' "$agent_obj" | jq -r '.prompt // ""')"
        model="$(printf '%s' "$agent_obj" | jq -r '.model // ""')"

        # tools in composed JSON is a JSON array: ["Read", "Edit", ...].
        # Plugin agent frontmatter expects a comma-separated inline list.
        tools_arr="$(printf '%s' "$agent_obj" | jq -r '.tools // [] | length')"
        if [ "$tools_arr" -gt 0 ]; then
            tools_str="$(printf '%s' "$agent_obj" \
                | jq -r '.tools | join(", ")')"
        else
            tools_str=""
        fi

        {
            printf -- '---\n'
            printf 'name: %s\n' "$id"
            # description: must be a single line for frontmatter parsing.
            # Collapse newlines to spaces (descriptions are short by design).
            printf 'description: %s\n' "$(printf '%s' "$desc" | tr '\n' ' ')"
            [ -n "$tools_str" ] && printf 'tools: %s\n' "$tools_str"
            [ -n "$model" ] && [ "$model" != "null" ] && printf 'model: %s\n' "$model"
            printf -- '---\n'
            printf '\n'
            printf '%s\n' "$prompt"
        } > "$plugin_dir/agents/${id}.md"

        agent_count=$((agent_count + 1))
    done

    printf '%d\n' "$agent_count"
}

# yk_rt_claude_materialize_agents <yakos-root> <project> <out-dir>
#   Writes the session-scoped plugin dir so yakos archive / lifecycle
#   hooks can discover and clean it. The launch path calls
#   yk_rt_claude_write_plugin_dir directly at launch time (before exec).
yk_rt_claude_materialize_agents() {
    local project="${2:-}"
    if [ -z "$project" ]; then return 0; fi
    local agents_json
    agents_json="$(yk_agents_compose "$YAKOS_ROOT" "$project" 2>/dev/null)" || return 0
    local plugin_dir
    plugin_dir="$(yk_rt_claude_plugin_dir_path "$project")"
    yk_rt_claude_write_plugin_dir "$plugin_dir" "$agents_json" >/dev/null
}

# yk_rt_claude_cleanup_agents <project>
#   Remove the session-scoped plugin dir. Called by yakos lifecycle /
#   archive. Safe to call multiple times (rm -rf is idempotent).
#   Note: exec in yk_rt_claude_launch replaces the shell process, so
#   this stub is intentionally wired to lifecycle hooks rather than a
#   post-launch trap. The dir is rebuilt at every launch start so a
#   leftover dir from a previous session is harmless.
yk_rt_claude_cleanup_agents() {
    local project="${1:-}"
    if [ -z "$project" ]; then return 0; fi
    local plugin_dir
    plugin_dir="$(yk_rt_claude_plugin_dir_path "$project")"
    rm -rf "$plugin_dir" 2>/dev/null || true
}

# yk_rt_claude_launch <project> <perm-mode> [extra-flags...]
#   exec claude with --add-dir <project>, --permission-mode <translated>,
#   and --plugin-dir <session-plugin-dir> (instead of --agents <json>).
#   Extra flags pass through verbatim (--continue, --resume, etc.).
#
#   The roster is materialized as per-agent .md files inside a session-
#   scoped plugin dir BEFORE exec. Each agent registers as
#   subagent_type "yakos:<id>" (e.g. "yakos:backend"). The lead
#   discovers these names from its Agent tool at runtime.
#
#   Using --plugin-dir avoids passing the entire roster (~167KB) as a
#   single argv string. On Linux, execve caps each argv element at
#   MAX_ARG_STRLEN (131,072 bytes on 4K-page kernels); exceeding it
#   returns E2BIG ("Argument list too long"). macOS has no equivalent
#   per-arg cap, so the crash only manifests in Linux containers.
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
        # Materialize roster as plugin dir; register via --plugin-dir.
        # Rebuild from scratch at every launch so stale files from a
        # prior session never pollute the new one. This must complete
        # before exec because exec replaces the process and no cleanup
        # can run afterwards.
        local plugin_dir
        plugin_dir="$(yk_rt_claude_plugin_dir_path "$project")"
        local written
        written="$(yk_rt_claude_write_plugin_dir "$plugin_dir" "$agents_json")"
        ct_log "claude_launch: wrote $written agent files to plugin dir $plugin_dir"
        args+=( --plugin-dir "$plugin_dir" )
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

    # Multi-turn (v0.31+): if YAKOS_CONVERSATION_ID is set, resume that
    # claude session via --resume <session_id>.
    local resume_args=()
    if [ -n "${YAKOS_CONVERSATION_ID:-}" ]; then
        resume_args=(--resume "$YAKOS_CONVERSATION_ID")
    fi

    # If the caller (dispatch.sh) set YAKOS_USAGE_OUT or
    # YAKOS_SESSION_OUT, run claude with --output-format stream-json so
    # we can parse the final result event for both usage and session_id.
    if [ -n "${YAKOS_USAGE_OUT:-}" ] || [ -n "${YAKOS_SESSION_OUT:-}" ]; then
        local raw_tmp
        raw_tmp="$(mktemp -t yakos-claude-raw.XXXXXX)"

        claude --agents "$single" \
               --permission-mode bypassPermissions \
               --add-dir "$project" \
               --output-format stream-json \
               --verbose \
               "${resume_args[@]}" \
               -p "$framed" > "$raw_tmp" 2>/dev/null
        local rc=$?

        jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "text") | .text' \
            "$raw_tmp" 2>/dev/null

        if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
            jq -c 'select(.type == "result") | {input_tokens: .usage.input_tokens,
                output_tokens: .usage.output_tokens,
                cache_read: .usage.cache_read_input_tokens,
                cache_creation: .usage.cache_creation_input_tokens,
                duration_ms: .duration_ms,
                total_cost_usd: .total_cost_usd}' \
                "$raw_tmp" 2>/dev/null | tail -1 > "$YAKOS_USAGE_OUT"
        fi

        if [ -n "${YAKOS_SESSION_OUT:-}" ]; then
            # Claude's stream-json emits session_id in the init or result event.
            jq -r 'select(.session_id != null) | .session_id' \
                "$raw_tmp" 2>/dev/null | head -1 > "$YAKOS_SESSION_OUT"
        fi

        rm -f "$raw_tmp" 2>/dev/null
        return "$rc"
    fi

    # Default path — text-only, no telemetry capture.
    claude --agents "$single" \
           --permission-mode bypassPermissions \
           --add-dir "$project" \
           "${resume_args[@]}" \
           -p "$framed"
}
