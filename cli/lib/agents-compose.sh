#!/usr/bin/env bash
# agents-compose.sh — compose --agents JSON for `claude --agents`.
#
# Purpose: produce a single JSON object suitable for `claude --agents`
# from the framework's lib/agents/*.md and a project's
# .claude/agents/*.md, resolving `extends:` and giving project agents
# precedence on id collisions. Sourced by start.sh and doctor.sh.
#
# Scans framework agents (lib/agents/*.md) and project agents
# (<project>/.claude/agents/*.md), parses frontmatter, resolves
# `extends:`, and emits a single JSON object suitable for
# `claude --agents '<json>'` on the launch command line.
#
# Empirically (probed 2026-05-08 against claude 2.1.136):
# the --agents JSON value-shape that registers an agent as
# `subagent_type` is:
#     { "<name>": {
#         "description": "<short string>",
#         "prompt":      "<system prompt body>",
#         "tools":       [ "Read", "Edit", ... ],   # optional
#         "model":       "haiku" | "sonnet" | "opus" # optional
#     }, ... }
#
# File-based agents at ~/.claude/agents/*.md and <project>/.claude/agents/*.md
# are NOT runtime-discoverable in claude 2.1.136 (incident:v0.2.0).
# This composer is the workaround until that's fixed upstream.
#
# Usage (sourced):
#     . "$YAKOS_LIB/agents-compose.sh"
#     yk_agents_compose <yakos-root> <project-root> [<extra-agents-dir>...]
#
# Output: a single JSON object on stdout. Logs go to stderr via ct_log.
# Exits non-zero on jq failure or missing required helpers.

set -eu

: "${YAKOS_COMPAT_LOADED:-0}" 1>/dev/null
if [ "${YAKOS_COMPAT_LOADED:-0}" != "1" ]; then
    : "${YAKOS_LIB:?agents-compose.sh: YAKOS_LIB must be set, and compat.sh sourced first}"
    # shellcheck source=./compat.sh
    . "$YAKOS_LIB/compat.sh"
fi

# yk_agents_extract_frontmatter <file>
#   Print the YAML frontmatter block (between leading --- markers) to stdout.
#   Empty output if the file has no frontmatter.
yk_agents_extract_frontmatter() {
    local file="$1"
    awk '
        BEGIN { in_fm = 0; lines = 0 }
        NR == 1 && /^---[[:space:]]*$/ { in_fm = 1; next }
        in_fm == 1 && /^---[[:space:]]*$/ { exit }
        in_fm == 1 { print; lines++ }
    ' "$file"
}

# yk_agents_extract_body <file>
#   Print everything AFTER the closing --- frontmatter marker.
#   If there's no frontmatter, prints the whole file.
yk_agents_extract_body() {
    local file="$1"
    awk '
        BEGIN { in_fm = 0; saw_open = 0 }
        NR == 1 && /^---[[:space:]]*$/ { in_fm = 1; saw_open = 1; next }
        in_fm == 1 && /^---[[:space:]]*$/ { in_fm = 2; next }
        saw_open == 0 { print; next }
        in_fm == 2 { print }
    ' "$file"
}

# yk_agents_fm_get <frontmatter-string> <key>
#   Read a scalar key from a YAML frontmatter block. Returns the raw
#   value (no quote stripping). Empty string if not found.
#   Limitation: handles only `key: value` on a single line; lists
#   like `tools: [a, b, c]` returned as the bracketed string.
yk_agents_fm_get() {
    local fm="$1"
    local key="$2"
    printf '%s\n' "$fm" | awk -v k="$key" '
        $0 ~ "^[[:space:]]*"k"[[:space:]]*:" {
            sub("^[[:space:]]*"k"[[:space:]]*:[[:space:]]*", "", $0)
            sub("[[:space:]]*$", "", $0)
            print
            exit
        }
    '
}

# yk_agents_fm_list <frontmatter-string> <key>
#   Parse a YAML inline list: `tools: [Read, Edit, Bash]` → newline-
#   separated names on stdout. Empty if not found / not a list.
yk_agents_fm_list() {
    local raw
    raw="$(yk_agents_fm_get "$1" "$2")"
    case "$raw" in
        \[*\])
            raw="${raw#[}"
            raw="${raw%]}"
            printf '%s' "$raw" | tr ',' '\n' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' | grep -v '^$' || true
            ;;
        *) : ;;
    esac
}

# yk_agents_derive_description <body>
#   Pull the first non-blank line under "## Purpose" (or first non-blank
#   prose line if no Purpose section), truncated to 200 chars. Used as
#   the agent's `description` field for --agents JSON.
yk_agents_derive_description() {
    local body="$1"
    printf '%s\n' "$body" | awk '
        BEGIN { in_purpose = 0; found = 0 }
        /^##[[:space:]]+Purpose/ { in_purpose = 1; next }
        in_purpose == 1 && /^##/ { exit }
        in_purpose == 1 && NF > 0 && !/^[[:space:]]*$/ {
            print; found = 1; exit
        }
    ' | cut -c1-200
}

# yk_agents_resolve_extends <yakos-root> <project-body> <extends-name>
#   When a project agent declares `extends: <framework-name>`, prepend
#   the framework template's body to the project body. The combined
#   text is the spawned agent's system prompt.
yk_agents_resolve_extends() {
    local yakos_root="$1"
    local project_body="$2"
    local extends_name="$3"
    local fw_file="$yakos_root/lib/agents/${extends_name}.md"
    if [ ! -f "$fw_file" ]; then
        ct_log "agents-compose: WARN extends:$extends_name not found at $fw_file; using project body alone"
        printf '%s\n' "$project_body"
        return 0
    fi
    local fw_body
    fw_body="$(yk_agents_extract_body "$fw_file")"
    printf '%s\n\n---\n\n%s\n' "$fw_body" "$project_body"
}

# yk_agents_compose_one <agent-id> <description> <prompt-body> <tools-newline> <model>
#   Emit a JSON fragment {"id": {...}} via jq with safe encoding.
#   Caller wraps multiple of these into a single object via jq -s add.
yk_agents_compose_one() {
    local id="$1"
    local desc="$2"
    local body="$3"
    local tools_nl="$4"
    local model="$5"

    # Skip empty descriptions — claude's --agents validator may reject.
    if [ -z "$desc" ]; then
        desc="Agent: $id"
    fi

    # Build tools JSON array via jq.
    local tools_json='[]'
    if [ -n "$tools_nl" ]; then
        tools_json="$(printf '%s\n' "$tools_nl" \
            | jq -R . \
            | jq -s '.')"
    fi

    # Build model field — only include if non-empty AND a known alias.
    # Empirical: claude accepts haiku|sonnet|opus aliases on --agents.
    local model_filter='.'
    if [ -n "$model" ]; then
        case "$model" in
            haiku|sonnet|opus) model_filter='.[$id].model = $model' ;;
            *) ct_log "agents-compose: WARN unknown model '$model' for $id; omitting from JSON" ;;
        esac
    fi

    jq -n \
        --arg id "$id" \
        --arg description "$desc" \
        --arg prompt "$body" \
        --arg model "$model" \
        --argjson tools "$tools_json" \
        '{
            ($id): ({
                description: $description,
                prompt:      $prompt
            } + (if ($tools | length) > 0 then {tools: $tools} else {} end))
         } | '"$model_filter"
}

# yk_agents_compose_dir <yakos-root> <agents-dir> <project-dir>
#   Process every .md file in <agents-dir>, emit one JSON fragment per
#   agent on stdout (one object per line). Skips README.md / lead-template.md
#   (template; not directly addressable as a subagent_type).
yk_agents_compose_dir() {
    local yakos_root="$1"
    local agents_dir="$2"
    # third positional (project_dir) reserved for future per-project filtering;
    # currently unused — kept to keep the signature stable.

    [ -d "$agents_dir" ] || return 0

    local file
    for file in "$agents_dir"/*.md; do
        [ -f "$file" ] || continue
        local base
        base="$(basename -- "$file")"
        case "$base" in
            README.md|lead-template.md) continue ;;
        esac

        local fm body id model extends_name tools_list desc raw_id
        fm="$(yk_agents_extract_frontmatter "$file")"
        body="$(yk_agents_extract_body "$file")"

        # Prefer frontmatter `id`; fall back to filename without .md.
        raw_id="$(yk_agents_fm_get "$fm" "id")"
        if [ -n "$raw_id" ]; then
            id="$raw_id"
        else
            id="${base%.md}"
        fi

        model="$(yk_agents_fm_get "$fm" "model")"
        extends_name="$(yk_agents_fm_get "$fm" "extends")"
        tools_list="$(yk_agents_fm_list "$fm" "tools" || true)"

        # Resolve extends:
        if [ -n "$extends_name" ]; then
            body="$(yk_agents_resolve_extends "$yakos_root" "$body" "$extends_name")"
        fi

        desc="$(yk_agents_derive_description "$body")"

        yk_agents_compose_one "$id" "$desc" "$body" "$tools_list" "$model"
    done
}

# yk_agents_compose <yakos-root> <project-root>
#   Compose the full --agents JSON for a yakos-launched session.
#   Project agents override framework agents with the same id.
#
# Memoizes per (yakos_root, project_root) pair within a single shell
# process — start.sh and dispatch.sh call this multiple times per
# invocation (count → print → materialize); without the cache each
# pass re-walks lib/agents/.
yk_agents_compose() {
    local yakos_root="$1"
    local project_root="${2:-}"

    if ! command -v jq >/dev/null 2>&1; then
        ct_die "agents-compose: jq is required (brew install jq)"
    fi

    local cache_key
    cache_key="$(printf '%s|%s' "$yakos_root" "$project_root" | tr -c 'A-Za-z0-9' '_')"
    local cache_var="YK_AGENTS_CACHE_${cache_key}"

    # Bash 3.2-compatible cache lookup via eval.
    local cached
    eval "cached=\${$cache_var:-}"
    if [ -n "$cached" ]; then
        # Cached values are base64-encoded to survive newlines in env vars.
        printf '%s\n' "$cached" | base64 -d 2>/dev/null
        return 0
    fi

    local fw_dir="$yakos_root/lib/agents"
    local proj_dir=""
    if [ -n "$project_root" ] && [ -d "$project_root/.claude/agents" ]; then
        proj_dir="$project_root/.claude/agents"
    fi

    local merged
    merged="$(
        {
            yk_agents_compose_dir "$yakos_root" "$fw_dir" "$project_root"
            [ -n "$proj_dir" ] && yk_agents_compose_dir "$yakos_root" "$proj_dir" "$project_root"
        } | jq -s 'reduce .[] as $a ({}; . + $a)'
    )"

    if [ -z "$merged" ] || [ "$merged" = "{}" ]; then
        ct_log "agents-compose: WARN no agents composed — empty output"
        printf '{}\n'
        return 0
    fi

    # Splice souls into lead's prompt (Plan 3 M1 / Capability A).
    # Reads ~/.yakos-state/soul/global.md and per-project layer; prepends
    # them to the LEAD agent's prompt only. Specialists do NOT see souls.
    # No-op if no soul files exist (current behavior unchanged for users
    # without souls). See lib/settings/soul.template.md and cli/lib/soul.sh.
    merged="$(yk_agents_apply_soul "$merged" "$project_root")"

    # Stash in cache for subsequent calls in the same process.
    # shellcheck disable=SC2163
    eval "$cache_var=\"\$(printf '%s' \"\$merged\" | base64)\""
    eval "export $cache_var"

    printf '%s\n' "$merged"
}

# yk_agents_apply_soul <composed-json> <project-root>
#   Read ~/.yakos-state/soul/{global,<project-slug>}.md (if present);
#   prepend them to the LEAD agent's `prompt` field. Project-layer
#   wins on conflict (Phase 1.5 §17 precedence).
#
#   Identifies the lead by: agent whose `id` starts with "lead-" OR
#   equals "lead" OR whose role frontmatter equals "orchestrator".
#   (The composed JSON only carries id/desc/prompt/tools/model — role
#   isn't in the value-shape — so we lean on the id-prefix heuristic.)
#
#   No-op (returns input unchanged) if neither soul file exists. Keeps
#   yakOS behavior identical for users who haven't created souls.
yk_agents_apply_soul() {
    local composed="$1" project_root="${2:-}"
    local soul_dir="$HOME/.yakos-state/soul"
    local global_soul="$soul_dir/global.md"
    local project_soul=""

    if [ -n "$project_root" ]; then
        # Slug = basename of project root. Matches soul.sh convention.
        project_soul="$soul_dir/$(basename -- "$project_root").md"
    fi

    # Fast no-op: if neither soul file exists, return composed unchanged.
    if [ ! -f "$global_soul" ] && { [ -z "$project_soul" ] || [ ! -f "$project_soul" ]; }; then
        printf '%s' "$composed"
        return 0
    fi

    # Read and concatenate souls in precedence order (global first; project
    # appended afterward — agent reads top-to-bottom so later overrides
    # earlier, matching Phase 1.5 §17 project-wins semantics).
    local soul_text=""
    if [ -f "$global_soul" ]; then
        soul_text="## Operator soul (global)

$(cat "$global_soul")

"
    fi
    if [ -n "$project_soul" ] && [ -f "$project_soul" ]; then
        soul_text="${soul_text}## Operator soul (this project)

$(cat "$project_soul")

"
    fi

    # Find the lead agent's key. Try ids in this order:
    #   lead-template, lead, project-lead, plus any key starting with "lead-".
    local lead_id
    lead_id="$(printf '%s' "$composed" | jq -r '
        ([keys[] | select(. == "lead-template" or . == "lead" or . == "project-lead")] +
         [keys[] | select(startswith("lead-"))])
        | unique[0] // empty')"

    if [ -z "$lead_id" ] || [ "$lead_id" = "null" ]; then
        # No lead identified — return composed unchanged. (Some sessions
        # may legitimately have no lead, e.g. headless dispatch tests.)
        ct_log "agents-compose: soul splice skipped — no lead-* agent detected"
        printf '%s' "$composed"
        return 0
    fi

    # Splice soul_text in FRONT of the lead's prompt. Use jq's --arg to
    # safely pass the soul text through (handles newlines, quotes, etc.).
    printf '%s' "$composed" | jq \
        --arg lead "$lead_id" \
        --arg soul "$soul_text" \
        '.[$lead].prompt = ($soul + .[$lead].prompt)'
}

# yk_agents_count <yakos-root> <project-root>
#   Stdout: integer count of agents that would be registered.
yk_agents_count() {
    yk_agents_compose "$1" "${2:-}" | jq 'length'
}
