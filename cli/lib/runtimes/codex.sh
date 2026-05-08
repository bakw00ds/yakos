#!/usr/bin/env bash
# runtimes/codex.sh — OpenAI Codex CLI adapter.
#
# Purpose: implement the runtime contract (see runtimes/README.md) for
# `codex`. Codex agents are TOML; this adapter materializes yakOS
# markdown agents to <project>/.codex/agents/yakos-*.toml.

set -eu

: "${YAKOS_LIB:?codex.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./_emitter-shared.sh
. "$YAKOS_LIB/runtimes/_emitter-shared.sh"

yk_rt_codex_id() { printf 'codex\n'; }

yk_rt_codex_capabilities() {
    printf 'path-allowlist-hard,hooks,system-prompt-flag,fork-headless\n'
}

yk_rt_codex_check_cli() {
    if command -v codex >/dev/null 2>&1; then return 0; fi
    ct_log "codex: 'codex' CLI not on PATH (npm install -g @openai/codex; see https://github.com/openai/codex)"
    return 1
}

yk_rt_codex_check_auth() {
    if [ -n "${OPENAI_API_KEY:-}" ]; then return 0; fi
    local home="${CODEX_HOME:-$HOME/.codex}"
    if [ -f "$home/auth.json" ]; then return 0; fi
    ct_log "codex: no auth configured (run 'yakos auth login codex' or 'codex login')"
    return 1
}

# Codex TOML schema: name, description, developer_instructions (system prompt),
# optional model. python3 path is preferred; jq fallback for portability.
# The python script is bash-single-quoted so its string-literal backslashes
# pass through verbatim to python -c.
_yk_codex_emit_py='
import json, sys
agent_id, out_file, json_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(json_path) as f:
    data = json.load(f)
def esc(s):
    return s.replace("\\", "\\\\").replace("\"\"\"", "\\\"\\\"\\\"")
desc = (data.get("description") or "Agent: " + agent_id).replace("\"", "\\\"")
body = data.get("prompt") or ""
model = data.get("model")
lines = ["name = \"" + agent_id + "\"", "description = \"" + desc + "\""]
if model:
    lines.append("model = \"" + model + "\"")
lines.append("developer_instructions = \"\"\"")
lines.append(esc(body).rstrip("\n"))
lines.append("\"\"\"")
with open(out_file, "w") as f:
    f.write("\n".join(lines) + "\n")
'

yk_rt_codex_emit_toml() {
    local id="$1" agent_json="$2" out_dir="$3"
    local out_file="$out_dir/yakos-${id}.toml"
    mkdir -p "$out_dir"

    if yk_emit_check_python; then
        yk_emit_run_python "$id" "$out_file" "$agent_json" "$_yk_codex_emit_py"
    else
        local desc body model
        desc="$(printf '%s' "$agent_json" | jq -r '.description // ""')"
        body="$(printf '%s' "$agent_json" | jq -r '.prompt // ""')"
        model="$(printf '%s' "$agent_json" | jq -r '.model // ""')"
        {
            printf 'name = "%s"\n' "$id"
            printf 'description = "%s"\n' "${desc//\"/\\\"}"
            [ -n "$model" ] && [ "$model" != "null" ] && printf 'model = "%s"\n' "$model"
            printf 'developer_instructions = """\n%s\n"""\n' "$body"
        } > "$out_file"
        ct_log "codex: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '%s\n' "$out_file"
}

yk_rt_codex_materialize_agents() {
    local yakos_root="$1" project="$2"
    local out_dir="${3:-$project/.codex/agents}"
    mkdir -p "$out_dir"

    local composed
    composed="$(yk_agents_compose "$yakos_root" "$project")"
    [ -n "$composed" ] || return 0

    local id agent_json
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        agent_json="$(printf '%s' "$composed" | jq --arg n "$id" '.[$n]')"
        yk_rt_codex_emit_toml "$id" "$agent_json" "$out_dir"
    done < <(printf '%s' "$composed" | jq -r 'keys[]')
}

yk_rt_codex_cleanup_agents() {
    local project="$1"
    local dir="$project/.codex/agents"
    [ -d "$dir" ] || return 0
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.toml' -delete 2>/dev/null || true
}

yk_rt_codex_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift

    yk_rt_codex_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local args=( --add-dir "$project" )
    case "$perm_mode" in
        bypass) args+=( --dangerously-bypass-approvals-and-sandbox ) ;;
        safe) : ;;
        *) ct_die "codex_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    [ "$#" -gt 0 ] && args+=( "$@" )
    exec codex "${args[@]}"
}

yk_rt_codex_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    yk_rt_codex_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local framed="Delegate this task to subagent named '$agent_name'. Use the agent's discipline and report only the final result.

Task:
$task"

    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        # codex exec --json emits NDJSON events; the final 'response.completed'
        # event carries usage in the response.usage field. Pipe to a tee, parse
        # the final usage event into YAKOS_USAGE_OUT, and reconstruct the
        # text-only output for stdout.
        local raw_tmp
        raw_tmp="$(mktemp -t yakos-codex-raw.XXXXXX)"

        codex exec --add-dir "$project" \
                   --dangerously-bypass-approvals-and-sandbox \
                   --json \
                   "$framed" > "$raw_tmp" 2>/dev/null
        local rc=$?

        # codex output items: assistant deltas, tool calls, final result.
        # Extract assistant message texts.
        jq -r 'select(.type == "item.completed") | .item.text // empty' \
            "$raw_tmp" 2>/dev/null

        # Final usage typically appears in a 'turn.completed' event.
        jq -c 'select(.type == "turn.completed") | .usage |
            {input_tokens: (.input_tokens // .prompt_tokens // 0),
             output_tokens: (.output_tokens // .completion_tokens // 0),
             cache_read: (.cache_read_input_tokens // 0),
             total_tokens: (.total_tokens // 0)}' \
            "$raw_tmp" 2>/dev/null | tail -1 > "$YAKOS_USAGE_OUT"

        rm -f "$raw_tmp" 2>/dev/null
        return "$rc"
    fi

    codex exec --add-dir "$project" \
               --dangerously-bypass-approvals-and-sandbox \
               --output-last-message - \
               "$framed"
}
