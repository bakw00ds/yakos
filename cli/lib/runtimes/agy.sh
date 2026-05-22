#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# runtimes/agy.sh — Google Antigravity CLI (agy) adapter.
#
# Purpose: implement the runtime contract (runtimes/README.md) for `agy`.
# Replaces the deprecated gemini.sh — Gemini CLI sunsets 2026-06-18; agy
# is the successor (essentially Gemini CLI rebranded as the Antigravity
# 2.0 CLI; same Google Code Assist API + auth chain + conversation .pb
# format).
#
# M0 verification (2026-05-22, agy 1.0.1):
#   --add-dir         exists; workspace scope (claude-compatible flag name)
#   -p "<prompt>"     positional prompt arg; plain Markdown output
#   --dangerously-skip-permissions   bypass-mode flag
#   --sandbox                        extra safety primitive (yakos exposes
#                                    as the "safe" permission mode)
#   -c / --continue                  resume most recent conversation
#   --conversation <id>              resume by ID
#   --print-timeout <duration>       default 5m
#   no --output-format / no -m flag  plain text; model picked via config
#   auth                              ~/.gemini/<...>/ via keyring (OAuth)
#   MCP                              ~/.gemini/config/mcp_config.json
#
# Capability tag: path-allowlist-hard (--add-dir is real scope, not soft).

set -eu

: "${YAKOS_LIB:?agy.sh: YAKOS_LIB must be set}"
# shellcheck source=../compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./_emitter-shared.sh
. "$YAKOS_LIB/runtimes/_emitter-shared.sh"

# ---------------------------------------------------------------------------
# id / capabilities
# ---------------------------------------------------------------------------

yk_rt_agy_id() { printf 'agy\n'; }

yk_rt_agy_capabilities() {
    # --add-dir confirmed; sandbox flag confirmed; no JSON output mode.
    printf 'path-allowlist-hard,hooks,sandbox-flag,headless-print\n'
}

# ---------------------------------------------------------------------------
# CLI / auth presence checks
# ---------------------------------------------------------------------------

yk_rt_agy_check_cli() {
    if command -v agy >/dev/null 2>&1; then return 0; fi
    # macOS / Linux default install location (per agy 1.0.1).
    if [ -x "$HOME/.local/bin/agy" ]; then return 0; fi
    ct_log "agy: 'agy' CLI not on PATH"
    ct_log "agy: install: curl -fsSL https://antigravity.google/cli/install.sh | bash"
    return 1
}

yk_rt_agy_check_auth() {
    # agy uses Google OAuth via keyring; M0 confirmed via cli.log
    # ('ChainedAuth: authenticated via keyring'). No env var probe.
    # Best-effort: existence of conversations dir implies prior auth + use.
    if [ -d "$HOME/.gemini/antigravity-cli/conversations" ] && \
       find "$HOME/.gemini/antigravity-cli/conversations" -maxdepth 1 -name '*.pb' -print -quit 2>/dev/null | grep -q .; then
        return 0
    fi
    # Migration window: ~/.gemini OAuth artifacts from prior gemini CLI use
    # carry forward (M0 confirmed: same keychain account).
    if [ -d "$HOME/.gemini" ] && [ -d "$HOME/.gemini/antigravity-cli" ]; then
        return 0
    fi
    ct_log "agy: no auth detected (run 'agy' interactively to OAuth, then retry)"
    return 1
}

# ---------------------------------------------------------------------------
# Agent materialization — markdown-with-frontmatter (same shape as Gemini's
# replaced format; agy 'plugin import' migrates gemini plugins so the file
# format is preserved between the two).
# ---------------------------------------------------------------------------

_yk_agy_emit_py='
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

yk_rt_agy_emit_md() {
    local id="$1" agent_json="$2" out_dir="$3"
    local out_file="$out_dir/yakos-${id}.md"
    mkdir -p "$out_dir"

    if yk_emit_check_python; then
        yk_emit_run_python "$id" "$out_file" "$agent_json" "$_yk_agy_emit_py"
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
        ct_log "agy: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '%s\n' "$out_file"
}

yk_rt_agy_materialize_agents() {
    local yakos_root="$1" project="$2"
    # Migration-guide convention: workspace skills at .agents/skills/
    local out_dir="${3:-$project/.agents/skills}"
    mkdir -p "$out_dir"

    local composed
    composed="$(yk_agents_compose "$yakos_root" "$project")"
    [ -n "$composed" ] || return 0

    local id agent_json
    while IFS= read -r id; do
        [ -n "$id" ] || continue
        agent_json="$(printf '%s' "$composed" | jq --arg n "$id" '.[$n]')"
        yk_rt_agy_emit_md "$id" "$agent_json" "$out_dir"
    done < <(printf '%s' "$composed" | jq -r 'keys[]')

    # Translate MCP config (migration guide: mcpServers outer key dropped;
    # url -> serverUrl). Writes to .agents/mcp_config.json. No-op if
    # operator-edited file already present (marker check).
    yk_rt_agy_translate_mcp_config "$project"
}

yk_rt_agy_cleanup_agents() {
    local project="$1"
    local dir="$project/.agents/skills"
    [ -d "$dir" ] || return 0
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.md' -delete 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# MCP config translation (breaking change from Gemini CLI's format).
# Migration guide warning: remote MCP servers fail silently if 'url' is
# copied without renaming to 'serverUrl'.
# ---------------------------------------------------------------------------

yk_rt_agy_translate_mcp_config() {
    local project="$1"
    local src="$project/.mcp.json"
    local dst="$project/.agents/mcp_config.json"

    [ -f "$src" ] || return 0
    mkdir -p "$project/.agents"

    # Refuse to overwrite an operator-edited file (lacks our marker).
    if [ -f "$dst" ] && ! head -1 "$dst" | grep -q '"_yakos_generated"'; then
        ct_log "agy: refusing to overwrite operator-edited $dst (remove or use 'yakos hooks install agy --force')"
        return 0
    fi

    local tmp="${dst}.tmp"
    if jq '
        {"_yakos_generated": true} as $marker
        | (.mcpServers // {})
        | with_entries(
            .value |= (
                if has("url") then
                    (.serverUrl = .url) | del(.url)
                else .
                end
            )
        )
        | $marker + .
    ' "$src" > "$tmp" 2>/dev/null; then
        mv "$tmp" "$dst"
    else
        rm -f "$tmp" 2>/dev/null
        ct_log "agy: failed to translate $src -> $dst (malformed JSON?)"
        return 1
    fi
}

# ---------------------------------------------------------------------------
# Launch (interactive TUI)
# ---------------------------------------------------------------------------

yk_rt_agy_launch() {
    local project="$1"; shift
    local perm_mode="$1"; shift

    yk_rt_agy_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    local args=( --add-dir "$project" )
    case "$perm_mode" in
        bypass) args+=( --dangerously-skip-permissions ) ;;
        safe)   args+=( --sandbox ) ;;
        *) ct_die "agy_launch: unknown perm-mode '$perm_mode' (bypass|safe)" ;;
    esac

    [ "$#" -gt 0 ] && args+=( "$@" )
    exec agy "${args[@]}"
}

# ---------------------------------------------------------------------------
# Dispatch (one-shot headless)
# ---------------------------------------------------------------------------

yk_rt_agy_dispatch() {
    local project="$1" agent_name="$2" task="$3"

    yk_rt_agy_materialize_agents "$YAKOS_ROOT" "$project" >/dev/null

    # @yakos-<name> mention — agy preserved Gemini's @-mention syntax for
    # workspace-skill invocation. M0 didn't end-to-end verify (no
    # materialized test agent active during M0); operator confirms in
    # first real dispatch.
    local framed="@yakos-$agent_name $task"

    # Plain text output (no JSON mode in agy 1.0.1). For YAKOS_USAGE_OUT
    # we estimate via bytes/4 — agy doesn't expose token counts in the
    # headless surface (cli.log has glog-format telemetry but parsing
    # that is fragile).

    local out_tmp
    out_tmp="$(mktemp -t yakos-agy-out.XXXXXX)"

    agy --add-dir "$project" \
        --dangerously-skip-permissions \
        -p "$framed" > "$out_tmp" 2>/dev/null
    local rc=$?

    cat "$out_tmp"

    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        local task_bytes out_bytes
        task_bytes="$(printf '%s' "$task" | wc -c | tr -d ' ')"
        out_bytes="$(wc -c < "$out_tmp" | tr -d ' ')"
        # Estimate-only; agy has no headless token telemetry.
        jq -nc \
            --argjson in "$((task_bytes / 4))" \
            --argjson out "$((out_bytes / 4))" \
            '{input_tokens: $in, output_tokens: $out, cache_read: 0, total_tokens: ($in + $out), source: "estimate-only-agy-no-headless-telemetry"}' \
            > "$YAKOS_USAGE_OUT"
    fi

    rm -f "$out_tmp" 2>/dev/null
    return "$rc"
}
