#!/usr/bin/env bash
# runtimes/gemini.sh — Google Gemini CLI adapter.
#
# Purpose: implement the runtime contract (see runtimes/README.md) for
# `gemini`. Gemini agents are markdown-with-frontmatter; this adapter
# materializes yakOS agents to <project>/.gemini/agents/yakos-*.md and
# synthesizes <project>/.gemini/settings.json from .mcp.json when present.

set -eu

: "${YAKOS_LIB:?gemini.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./_emitter-shared.sh
. "$YAKOS_LIB/runtimes/_emitter-shared.sh"

yk_rt_gemini_id() { printf 'gemini\n'; }

yk_rt_gemini_capabilities() {
    printf 'path-allowlist-hard,hooks\n'
}

yk_rt_gemini_check_cli() {
    if command -v gemini >/dev/null 2>&1; then return 0; fi
    ct_log "gemini: 'gemini' CLI not on PATH (npm install -g @google/gemini-cli)"
    return 1
}

yk_rt_gemini_check_auth() {
    if [ -n "${GEMINI_API_KEY:-}" ]; then return 0; fi
    if [ "${GOOGLE_GENAI_USE_VERTEXAI:-}" = "true" ]; then return 0; fi
    # OAuth creds: any non-settings file under ~/.gemini/.
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

# Gemini agent schema: frontmatter (name, description, optional model/tools)
# + markdown body. python3 path is preferred; jq fallback below.
_yk_gemini_emit_py='
import json, sys
agent_id, out_file, json_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(json_path) as f:
    data = json.load(f)
def yq(s):
    return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"") + "\""
desc = data.get("description") or ("Agent: " + agent_id)
body = data.get("prompt") or ""
model = data.get("model")
tools = data.get("tools") or []
lines = ["---", "name: " + agent_id, "description: " + yq(desc)]
if model:
    lines.append("model: " + yq(model))
if tools:
    lines.append("tools: [" + ", ".join(yq(t) for t in tools) + "]")
lines.append("---")
lines.append("")
lines.append(body.rstrip("\n"))
with open(out_file, "w") as f:
    f.write("\n".join(lines) + "\n")
'

yk_rt_gemini_emit_md() {
    local id="$1" agent_json="$2" out_dir="$3"
    local out_file="$out_dir/yakos-${id}.md"
    mkdir -p "$out_dir"

    if yk_emit_check_python; then
        yk_emit_run_python "$id" "$out_file" "$agent_json" "$_yk_gemini_emit_py"
    else
        local desc body model
        desc="$(printf '%s' "$agent_json" | jq -r '.description // ""')"
        body="$(printf '%s' "$agent_json" | jq -r '.prompt // ""')"
        model="$(printf '%s' "$agent_json" | jq -r '.model // ""')"
        {
            printf -- '---\n'
            printf 'name: %s\n' "$id"
            printf 'description: "%s"\n' "${desc//\"/\\\"}"
            [ -n "$model" ] && [ "$model" != "null" ] && printf 'model: "%s"\n' "$model"
            printf -- '---\n\n%s\n' "$body"
        } > "$out_file"
        ct_log "gemini: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '%s\n' "$out_file"
}

yk_rt_gemini_materialize_agents() {
    local yakos_root="$1" project="$2"
    local out_dir="${3:-$project/.gemini/agents}"
    mkdir -p "$out_dir"

    local composed
    composed="$(yk_agents_compose "$yakos_root" "$project")"
    [ -n "$composed" ] || return 0

    local id agent_json
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        agent_json="$(printf '%s' "$composed" | jq --arg n "$id" '.[$n]')"
        yk_rt_gemini_emit_md "$id" "$agent_json" "$out_dir"
    done < <(printf '%s' "$composed" | jq -r 'keys[]')
}

yk_rt_gemini_cleanup_agents() {
    local project="$1"
    local dir="$project/.gemini/agents"
    [ -d "$dir" ] || return 0
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.md' -delete 2>/dev/null || true
}

# Gemini-cli has no --mcp-config flag; mcpServers must live in settings.json.
# Merge <project>/.mcp.json into <project>/.gemini/settings.json with a
# timestamped backup of any existing settings.
yk_rt_gemini_synth_settings() {
    local project="$1"
    local mcp_src="$project/.mcp.json"
    local settings="$project/.gemini/settings.json"

    [ -f "$mcp_src" ] || return 0
    mkdir -p "$project/.gemini"

    if [ -f "$settings" ]; then
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
    [ -n "$merged" ] && printf '%s\n' "$merged" > "$settings"
}

yk_rt_gemini_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift

    yk_rt_gemini_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null
    yk_rt_gemini_synth_settings "$project"

    # If yakos memory sync gemini has been run, the synthesized
    # system.md gets exported as GEMINI_SYSTEM_MD so memory flows
    # into the session context.
    local sysmd="$project/.gemini/yakos-system.md"
    if [ -f "$sysmd" ] && [ -z "${GEMINI_SYSTEM_MD:-}" ]; then
        export GEMINI_SYSTEM_MD="$sysmd"
        ct_log "gemini: exported GEMINI_SYSTEM_MD=$sysmd"
    fi

    local args=( --include-directories "$project" )
    case "$perm_mode" in
        bypass) args+=( --approval-mode=yolo ) ;;
        safe) : ;;
        *) ct_die "gemini_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    [ "$#" -gt 0 ] && args+=( "$@" )
    exec gemini "${args[@]}"
}

yk_rt_gemini_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    yk_rt_gemini_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local framed="@yakos-$agent_name $task"

    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        # gemini -p --output-format stream-json emits NDJSON events;
        # the 'usage' on the final response carries token counts.
        local raw_tmp
        raw_tmp="$(mktemp -t yakos-gemini-raw.XXXXXX)"

        gemini --include-directories "$project" \
               --approval-mode=yolo \
               --output-format stream-json \
               -p "$framed" > "$raw_tmp" 2>/dev/null
        local rc=$?

        # Extract response text from content events.
        jq -r 'select(.type == "content") | .content // empty' \
            "$raw_tmp" 2>/dev/null

        # gemini's final response event has a 'usage' or 'tokens' field.
        jq -c 'select(.usage != null) | .usage |
            {input_tokens: (.prompt_tokens // .input_tokens // 0),
             output_tokens: (.completion_tokens // .output_tokens // 0),
             cache_read: (.cached_content_tokens // 0),
             total_tokens: (.total_tokens // 0)}' \
            "$raw_tmp" 2>/dev/null | tail -1 > "$YAKOS_USAGE_OUT"

        rm -f "$raw_tmp" 2>/dev/null
        return "$rc"
    fi

    gemini --include-directories "$project" \
           --approval-mode=yolo \
           -p "$framed"
}
