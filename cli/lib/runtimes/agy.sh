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
# agy 1.2.17 (2026-10) supersedes the 1.0.1 notes above: it has --model,
# --effort, --output-format text|json|stream-json and --conversation, and it
# discovers workspace skills at .agents/skills/<name>/SKILL.md (a directory per
# skill; flat .md files are not loaded). This bash adapter still runs plain
# text and does not pass --model; the Go adapter does (see docs/runtime-matrix.md).
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
# Agent materialization — one workspace skill per agent:
#   <project>/.agents/skills/yakos-<id>/SKILL.md   (agy discovers <dir>/<skill>/SKILL.md)
#   <project>/.agents/skills/yakos-<id>/.gitignore  ("*": keeps the generated
#       directory out of git status without editing the project's .gitignore)
#
# SKILL.md carries a yakos-generated marker. A SKILL.md without it is the
# operator's own and is never overwritten. The Go materializer
# (cli-go/internal/agentscompose/materialize_agy.go) emits identical bytes for
# the same agent JSON; tests/run-runtime-fixtures.sh and the Go parity tests
# keep the three implementations (python, jq fallback, Go) in step.
#
# The model line is written only for a model that is not a Claude tier: the
# composer resolves an alias such as balanced to a tier (sonnet) itself, and agy
# has no model by that name. Frontmatter values are one-line YAML double-quoted
# scalars, so a control character in one is written as \u00XX (see
# _emitter-shared.sh). An agent text holding a NUL byte is refused (status 3).
#
# Before K-134 this wrote a flat yakos-<id>.md with `name: <id>`; agy 1.2.x does
# not load that layout. The flat files are removed by yk_rt_agy_cleanup_agents.
# ---------------------------------------------------------------------------

_YK_AGY_MARKER='<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->'

_yk_agy_emit_py='
import json, re, sys
agent_id, out_file, json_path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(json_path, encoding="utf-8") as f:
    data = json.load(f)
desc = data.get("description") or ("Agent: " + agent_id)
body = data.get("prompt") or ""
model = data.get("model") or ""
tools = data.get("tools") or []
for text in [desc, body, model] + [t for t in tools if isinstance(t, str)]:
    if "\x00" in text:
        sys.stderr.write("agent text contains a NUL byte\n")
        sys.exit(3)
def ctl(m):
    return "\\u%04X" % ord(m.group())
def yq(s):
    s = re.sub(r"\r\n|\r|\n", " ", s).replace("\\", "\\\\").replace("\"", "\\\"")
    return "\"" + re.sub(r"[\x00-\x08\x0a-\x1f\x7f]", ctl, s) + "\""
lines = ["---", "name: yakos-" + agent_id, "description: " + yq(desc)]
if model and model not in ("haiku", "sonnet", "opus", "fable"):
    lines.append("model: " + yq(model))
if tools:
    lines.append("tools: [" + ", ".join(yq(t) for t in tools) + "]")
lines.append("---")
lines.append("<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->")
lines.append("")
lines.append(body.lstrip("\r\n").rstrip("\n"))
with open(out_file, "w", encoding="utf-8", newline="\n") as f:
    f.write("\n".join(lines) + "\n")
'

# The same file built by jq alone (python3 absent). _YK_EMIT_JQ_DEFS carries the
# escaping rules; see _emitter-shared.sh.
_yk_agy_emit_jq='
(.description // "") as $d | (.prompt // "") as $p | (.model // "") as $m | (.tools // []) as $t
| [ "---",
    "name: yakos-" + $id,
    "description: \"" + (($d | if . == "" then "Agent: " + $id else . end) | quoteline) + "\"" ]
  + (if $m != "" and ($m | tier | not) then ["model: \"" + ($m | quoteline) + "\""] else [] end)
  + (if ($t | length) > 0 then ["tools: [" + ($t | map("\"" + quoteline + "\"") | join(", ")) + "]"] else [] end)
  + [ "---", $marker, "", ($p | promptbody) ]
| join("\n") + "\n"
'

# _yk_agy_is_generated <skill-md>
#   0 when the marker is in the first 12 lines (the file is yakOS-generated).
_yk_agy_is_generated() {
    head -n 12 "$1" 2>/dev/null | grep -q 'yakos-generated:'
}

yk_rt_agy_emit_md() {
    local id="$1" agent_json="$2" out_dir="$3"
    local skill_dir="$out_dir/yakos-${id}"
    local out_file="$skill_dir/SKILL.md"
    mkdir -p "$skill_dir"

    if [ -f "$out_file" ] && ! _yk_agy_is_generated "$out_file"; then
        ct_log "agy: not overwriting $out_file (no yakos-generated marker; delete it, or add the marker line to let yakOS manage it)"
        printf '%s\n' "$out_file"
        return 0
    fi

    if yk_emit_check_python; then
        local rc=0
        yk_emit_run_python "$id" "$out_file" "$agent_json" "$_yk_agy_emit_py" || rc=$?
        if [ "$rc" -eq 3 ]; then
            ct_log "agy: not writing $out_file (the agent's text contains a NUL byte)"
            rmdir "$skill_dir" 2>/dev/null || true
            return 0
        fi
        [ "$rc" -eq 0 ] || return "$rc"
    else
        if yk_emit_nul_in_agent "$agent_json" tools; then
            ct_log "agy: not writing $out_file (the agent's text contains a NUL byte)"
            rmdir "$skill_dir" 2>/dev/null || true
            return 0
        fi
        local tmp="$out_file.tmp.$$"
        if ! printf '%s' "$agent_json" \
            | jq -j --arg id "$id" --arg marker "$_YK_AGY_MARKER" "$_YK_EMIT_JQ_DEFS $_yk_agy_emit_jq" > "$tmp"; then
            rm -f "$tmp" 2>/dev/null || true
            ct_log "agy: jq failed to emit $out_file"
            return 1
        fi
        mv -f "$tmp" "$out_file"
        ct_log "agy: emitted $out_file via jq fallback (install python3 for fidelity)"
    fi
    printf '*\n' > "$skill_dir/.gitignore"
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
    # Legacy flat files from before K-134.
    find "$dir" -maxdepth 1 -type f -name 'yakos-*.md' -delete 2>/dev/null || true
    # Generated skill directories: only those whose SKILL.md carries the
    # marker, and only the two files this adapter writes.
    local d
    for d in "$dir"/yakos-*/; do
        [ -d "$d" ] || continue
        if _yk_agy_is_generated "${d}SKILL.md"; then
            rm -f "${d}SKILL.md" "${d}.gitignore" 2>/dev/null || true
            rmdir "$d" 2>/dev/null || true
        fi
    done
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

    # Multi-turn (v0.31+): if YAKOS_CONVERSATION_ID is set, use
    # --conversation <uuid> to continue. agy stores conversations at
    # ~/.gemini/antigravity-cli/conversations/<uuid>.pb.
    local resume_args=()
    if [ -n "${YAKOS_CONVERSATION_ID:-}" ]; then
        resume_args=(--conversation "$YAKOS_CONVERSATION_ID")
    fi

    # Plain text output (no JSON mode in agy 1.0.1). For YAKOS_USAGE_OUT
    # we estimate via bytes/4 — agy doesn't expose token counts in the
    # headless surface (cli.log has glog-format telemetry but parsing
    # that is fragile).

    local out_tmp
    out_tmp="$(mktemp -t yakos-agy-out.XXXXXX)"

    agy --add-dir "$project" \
        --dangerously-skip-permissions \
        "${resume_args[@]}" \
        -p "$framed" > "$out_tmp" 2>/dev/null
    local rc=$?

    cat "$out_tmp"

    # Capture the conversation UUID for YAKOS_SESSION_OUT. agy writes
    # the new (or updated) conversation file to the conversations dir
    # immediately on completion. Pick the most-recently-modified .pb
    # file as our session identity.
    if [ -n "${YAKOS_SESSION_OUT:-}" ]; then
        local conv_root="$HOME/.gemini/antigravity-cli/conversations"
        if [ -d "$conv_root" ]; then
            local latest
            local _mtime_list=""
            while IFS= read -r -d '' _f; do
                _mt="$(stat -f '%m' "$_f" 2>/dev/null || stat -c '%Y' "$_f" 2>/dev/null || true)"
                [ -n "$_mt" ] && _mtime_list="${_mtime_list}${_mt} ${_f}"$'\n'
            done < <(find "$conv_root" -maxdepth 1 -type f -name '*.pb' -print0 2>/dev/null)
            latest="$(printf '%s' "$_mtime_list" | sort -n -r | head -1 | awk '{print $2}')"
            if [ -n "$latest" ]; then
                basename -- "$latest" .pb > "$YAKOS_SESSION_OUT"
            fi
        fi
    fi

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
