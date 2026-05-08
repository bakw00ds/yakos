#!/usr/bin/env bash
# runtimes/codex.sh — OpenAI Codex CLI adapter.
#
# Purpose: implement the runtime contract (cli/lib/runtimes/README.md)
# for `codex` (https://github.com/openai/codex). Codex's agent-definition
# format is TOML; this adapter converts yakOS markdown agents to
# codex-native TOML and stages them at <project>/.codex/agents/yakos-*.toml.
#
# Capabilities: path-allowlist-hard, hooks, system-prompt-flag (via
# AGENTS.md / -c), fork-headless (codex fork). NOT: inline-agents
# (no --agents JSON flag — file-based only); mcp-flag (MCP via
# config.toml, not CLI flag).
#
# Tested against codex 0.129.0 (May 2026).

set -eu

: "${YAKOS_LIB:?codex.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

yk_rt_codex_id() { printf 'codex\n'; }

yk_rt_codex_capabilities() {
    printf 'path-allowlist-hard,hooks,system-prompt-flag,fork-headless\n'
}

yk_rt_codex_check_cli() {
    if command -v codex >/dev/null 2>&1; then
        return 0
    fi
    ct_log "codex: 'codex' CLI not on PATH (npm install -g @openai/codex; see https://github.com/openai/codex)"
    return 1
}

# yk_rt_codex_check_auth
#   codex stores auth at $CODEX_HOME/auth.json (default ~/.codex/auth.json)
#   or accepts OPENAI_API_KEY env. Heuristic: file or env present.
yk_rt_codex_check_auth() {
    if [ -n "${OPENAI_API_KEY:-}" ]; then
        return 0
    fi
    local home="${CODEX_HOME:-$HOME/.codex}"
    if [ -f "$home/auth.json" ]; then
        return 0
    fi
    ct_log "codex: no auth configured (run 'yakos auth login codex' or 'codex login')"
    return 1
}

# yk_rt_codex_emit_toml <agent-id> <agent-json> <out-dir>
#   Convert a single agent's composed JSON record (from agents-compose)
#   into codex's TOML agent format and write to
#   <out-dir>/yakos-<agent-id>.toml. Stdout: the written path.
#
# codex agent schema (per docs):
#   name = "<agent-id>"                 # required
#   description = "<short>"             # required
#   developer_instructions = "<body>"   # required (system prompt)
#   model = "..."                       # optional
#   sandbox_mode = "workspace-write"    # optional
yk_rt_codex_emit_toml() {
    local id="$1"
    local agent_json="$2"
    local out_dir="$3"

    local out_file="$out_dir/yakos-${id}.toml"
    mkdir -p "$out_dir"

    # Pull fields via jq; TOML-escape with python (toml dump) if available,
    # else use a conservative heredoc with backslash-escaped triple-double-
    # quotes for the body. python3 path is preferred for correctness.
    if command -v python3 >/dev/null 2>&1; then
        local json_tmp
        json_tmp="$(mktemp -t yakos-codex-agent.XXXXXX)"
        printf '%s' "$agent_json" > "$json_tmp"
        python3 - "$id" "$out_file" "$json_tmp" <<'PYEOF'
import json, sys
agent_id, out_file, json_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(json_path) as f:
    data = json.load(f)
def esc(s):
    # TOML triple-double-quoted basic strings: escape \ and """
    return s.replace('\\', '\\\\').replace('"""', '\\"\\"\\"')
desc = (data.get("description") or f"Agent: {agent_id}").replace('"', '\\"')
body = data.get("prompt") or ""
model = data.get("model")
lines = []
lines.append(f'name = "{agent_id}"')
lines.append(f'description = "{desc}"')
if model:
    lines.append(f'model = "{model}"')
lines.append('developer_instructions = """')
lines.append(esc(body).rstrip("\n"))
lines.append('"""')
with open(out_file, "w") as f:
    f.write("\n".join(lines) + "\n")
PYEOF
        rm -f "$json_tmp" 2>/dev/null || true
    else
        # Fallback (no python3): use jq to extract fields and emit a
        # best-effort TOML. body is written naively; users with rich
        # markdown in agent prompts should install python3.
        local desc body model
        desc="$(printf '%s' "$agent_json" | jq -r '.description // ""')"
        body="$(printf '%s' "$agent_json" | jq -r '.prompt // ""')"
        model="$(printf '%s' "$agent_json" | jq -r '.model // ""')"
        {
            printf 'name = "%s"\n' "$id"
            printf 'description = "%s"\n' "${desc//\"/\\\"}"
            [ -n "$model" ] && [ "$model" != "null" ] && printf 'model = "%s"\n' "$model"
            printf 'developer_instructions = """\n'
            printf '%s\n' "$body"
            printf '"""\n'
        } > "$out_file"
        ct_log "codex: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '%s\n' "$out_file"
}

# yk_rt_codex_materialize_agents <yakos-root> <project> [<out-dir>]
#   Compose yakOS agents and emit TOML to <project>/.codex/agents/.
#   Returns one written path per line on stdout.
yk_rt_codex_materialize_agents() {
    local yakos_root="$1"
    local project="$2"
    local out_dir="${3:-$project/.codex/agents}"

    mkdir -p "$out_dir"
    local composed
    composed="$(yk_agents_compose "$yakos_root" "$project")"
    [ -n "$composed" ] || return 0

    # Iterate agent ids; emit one TOML per id.
    local ids
    ids="$(printf '%s' "$composed" | jq -r 'keys[]')"
    local id agent_json
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        agent_json="$(printf '%s' "$composed" | jq --arg n "$id" '.[$n]')"
        yk_rt_codex_emit_toml "$id" "$agent_json" "$out_dir"
    done <<< "$ids"
}

# yk_rt_codex_cleanup_agents <project>
yk_rt_codex_cleanup_agents() {
    local project="$1"
    local dir="$project/.codex/agents"
    [ -d "$dir" ] || return 0
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.toml' -delete 2>/dev/null || true
}

# yk_rt_codex_launch <project> <perm-mode> [extra-flags...]
#   Materialize agents, then exec codex --add-dir <project> with the
#   right approval/sandbox modes.
yk_rt_codex_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift

    yk_rt_codex_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local args=( --add-dir "$project" )
    case "$perm_mode" in
        bypass)
            # Codex's full-bypass flag (sandbox + approvals).
            args+=( --dangerously-bypass-approvals-and-sandbox )
            ;;
        safe)
            # Default codex behavior: prompts + workspace-write sandbox.
            : # no extra flags
            ;;
        *) ct_die "codex_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    if [ "$#" -gt 0 ]; then
        args+=( "$@" )
    fi

    exec codex "${args[@]}"
}

# yk_rt_codex_dispatch <project> <agent-name> <task-prompt>
#   One-shot via `codex exec`. Agent must already be materialized
#   in <project>/.codex/agents/. Caller is responsible for ensuring
#   materialization happened (or pass --materialize via wrapper).
yk_rt_codex_dispatch() {
    local project="$1"
    local agent_name="$2"
    local task="$3"

    yk_rt_codex_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    # Frame the task so codex routes through the named agent.
    # codex's docs indicate "@agent-name" syntax in user prompts may
    # work; fallback is to inject the agent's developer_instructions
    # via -c override. The simpler path: just instruct in prose.
    local framed
    framed="Delegate this task to subagent named '$agent_name'. Use the agent's discipline and report only the final result.

Task:
$task"

    codex exec --add-dir "$project" \
               --dangerously-bypass-approvals-and-sandbox \
               --output-last-message - \
               "$framed"
}
