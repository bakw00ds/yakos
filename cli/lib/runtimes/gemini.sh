#!/usr/bin/env bash
# runtimes/gemini.sh — Google Gemini CLI adapter.
#
# Purpose: implement the runtime contract (cli/lib/runtimes/README.md)
# for `gemini` (https://github.com/google-gemini/gemini-cli).
# gemini-cli's agent definition format is markdown-with-frontmatter
# (very close to yakOS's source format). This adapter materializes
# yakOS agents to <project>/.gemini/agents/yakos-*.md.
#
# Capabilities: path-allowlist-hard, hooks, fork-headless (unverified;
# may need slash-command for headless fork). NOT: inline-agents
# (no --agents flag); mcp-flag (MCP must be inline in settings.json);
# system-prompt-flag (system prompt only via GEMINI_SYSTEM_MD env var).
#
# Tested against gemini-cli 0.41.2 (May 2026).

set -eu

: "${YAKOS_LIB:?gemini.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

yk_rt_gemini_id() { printf 'gemini\n'; }

yk_rt_gemini_capabilities() {
    printf 'path-allowlist-hard,hooks\n'
}

yk_rt_gemini_check_cli() {
    if command -v gemini >/dev/null 2>&1; then
        return 0
    fi
    ct_log "gemini: 'gemini' CLI not on PATH (npm install -g @google/gemini-cli; see https://github.com/google-gemini/gemini-cli)"
    return 1
}

# yk_rt_gemini_check_auth
#   gemini-cli has three auth paths:
#     1. OAuth (~/.gemini/ creds files)
#     2. GEMINI_API_KEY env
#     3. Vertex AI (GOOGLE_GENAI_USE_VERTEXAI=true + gcloud)
#   Heuristic: any one configured = OK.
yk_rt_gemini_check_auth() {
    if [ -n "${GEMINI_API_KEY:-}" ]; then
        return 0
    fi
    if [ "${GOOGLE_GENAI_USE_VERTEXAI:-}" = "true" ]; then
        return 0
    fi
    # gemini stashes OAuth creds under ~/.gemini/ — file names vary;
    # presence of any file other than settings.json is the heuristic.
    if [ -d "$HOME/.gemini" ]; then
        local f
        for f in "$HOME/.gemini"/*; do
            [ -e "$f" ] || continue
            case "$(basename -- "$f")" in
                settings.json|.|..) continue ;;
                *) return 0 ;;
            esac
        done
    fi
    ct_log "gemini: no auth configured (run 'yakos auth login gemini')"
    return 1
}

# yk_rt_gemini_emit_md <agent-id> <agent-json> <out-dir>
#   Convert a single agent's composed JSON into gemini-cli's
#   markdown-with-frontmatter format. gemini's schema is similar
#   to yakOS source; the main translation is moving the agent body
#   into the markdown body and writing only the fields gemini reads.
#
# gemini agent schema (per docs):
#   ---
#   name: <id>            # required
#   description: <short>  # required (visible to main agent for delegation)
#   tools: ["..."]        # optional — wildcards allowed
#   model: <model>        # optional
#   ---
#   <markdown body — the system prompt>
yk_rt_gemini_emit_md() {
    local id="$1"
    local agent_json="$2"
    local out_dir="$3"

    local out_file="$out_dir/yakos-${id}.md"
    mkdir -p "$out_dir"

    # Use python3 to safely quote frontmatter values; fall back to jq.
    if command -v python3 >/dev/null 2>&1; then
        local json_tmp
        json_tmp="$(mktemp -t yakos-gemini-agent.XXXXXX)"
        printf '%s' "$agent_json" > "$json_tmp"
        python3 - "$id" "$out_file" "$json_tmp" <<'PYEOF'
import json, sys
agent_id, out_file, json_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(json_path) as f:
    data = json.load(f)

def yaml_quote(s):
    # Quote with double quotes; escape backslash and double-quote.
    return '"' + s.replace('\\', '\\\\').replace('"', '\\"') + '"'

desc = data.get("description") or f"Agent: {agent_id}"
body = data.get("prompt") or ""
model = data.get("model")
tools = data.get("tools") or []

lines = ["---"]
lines.append(f'name: {agent_id}')
lines.append(f'description: {yaml_quote(desc)}')
if model:
    lines.append(f'model: {yaml_quote(model)}')
if tools:
    inline_tools = ", ".join(yaml_quote(t) for t in tools)
    lines.append(f'tools: [{inline_tools}]')
lines.append("---")
lines.append("")
lines.append(body.rstrip("\n"))
with open(out_file, "w") as f:
    f.write("\n".join(lines) + "\n")
PYEOF
        rm -f "$json_tmp" 2>/dev/null || true
    else
        # jq fallback — best-effort.
        local desc body model
        desc="$(printf '%s' "$agent_json" | jq -r '.description // ""')"
        body="$(printf '%s' "$agent_json" | jq -r '.prompt // ""')"
        model="$(printf '%s' "$agent_json" | jq -r '.model // ""')"
        {
            printf -- '---\n'
            printf 'name: %s\n' "$id"
            printf 'description: "%s"\n' "${desc//\"/\\\"}"
            [ -n "$model" ] && [ "$model" != "null" ] && printf 'model: "%s"\n' "$model"
            printf -- '---\n\n'
            printf '%s\n' "$body"
        } > "$out_file"
        ct_log "gemini: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '%s\n' "$out_file"
}

# yk_rt_gemini_materialize_agents <yakos-root> <project> [<out-dir>]
yk_rt_gemini_materialize_agents() {
    local yakos_root="$1"
    local project="$2"
    local out_dir="${3:-$project/.gemini/agents}"

    mkdir -p "$out_dir"
    local composed
    composed="$(yk_agents_compose "$yakos_root" "$project")"
    [ -n "$composed" ] || return 0

    local ids
    ids="$(printf '%s' "$composed" | jq -r 'keys[]')"
    local id agent_json
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        agent_json="$(printf '%s' "$composed" | jq --arg n "$id" '.[$n]')"
        yk_rt_gemini_emit_md "$id" "$agent_json" "$out_dir"
    done <<< "$ids"
}

# yk_rt_gemini_cleanup_agents <project>
yk_rt_gemini_cleanup_agents() {
    local project="$1"
    local dir="$project/.gemini/agents"
    [ -d "$dir" ] || return 0
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.md' -delete 2>/dev/null || true
}

# yk_rt_gemini_synth_settings <project>
#   gemini-cli has no --mcp-config flag — MCP servers must be configured
#   inline in settings.json. If <project>/.mcp.json exists (Claude-style),
#   merge its mcpServers into <project>/.gemini/settings.json so the
#   agents can use them.
yk_rt_gemini_synth_settings() {
    local project="$1"
    local mcp_src="$project/.mcp.json"
    local settings="$project/.gemini/settings.json"

    [ -f "$mcp_src" ] || return 0
    mkdir -p "$project/.gemini"

    if [ -f "$settings" ]; then
        # Backup once per session to avoid clobbering user edits.
        local bak_ts bak
        bak_ts="$(ct_iso_utc)"
        bak="${settings}.yakos-bak-${bak_ts}"
        cp "$settings" "$bak" 2>/dev/null || true
    else
        printf '{}\n' > "$settings"
    fi

    local merged
    merged="$(jq -s '.[0] as $s | .[1] as $m
        | $s + {mcpServers: ($s.mcpServers // {}) + ($m.mcpServers // {})}' \
        "$settings" "$mcp_src" 2>/dev/null)"
    if [ -n "$merged" ]; then
        printf '%s\n' "$merged" > "$settings"
    fi
}

# yk_rt_gemini_launch <project> <perm-mode> [extra-flags...]
yk_rt_gemini_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift

    yk_rt_gemini_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null
    yk_rt_gemini_synth_settings "$project"

    local args=( --include-directories "$project" )
    case "$perm_mode" in
        bypass) args+=( --approval-mode=yolo ) ;;
        safe) : ;;  # default approval mode
        *) ct_die "gemini_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    if [ "$#" -gt 0 ]; then
        args+=( "$@" )
    fi

    exec gemini "${args[@]}"
}

# yk_rt_gemini_dispatch <project> <agent-name> <task-prompt>
#   One-shot via `gemini -p`. The "@<agent-name>" syntax in the prompt
#   triggers gemini's subagent delegation; the materialized agent file
#   provides the discipline.
yk_rt_gemini_dispatch() {
    local project="$1"
    local agent_name="$2"
    local task="$3"

    yk_rt_gemini_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local framed
    framed="@yakos-$agent_name $task"

    gemini --include-directories "$project" \
           --approval-mode=yolo \
           -p "$framed"
}
