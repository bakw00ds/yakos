#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
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

# ---- which agent files are read (sec-324) -----------------------------------
# A cloned repository controls a project's .claude/agents, so a file there is
# read only when that is safe: `extends:` is a bare agent id and nothing else (a
# path let a project extend any .md file the daemon can reach), and a symlink is
# followed only to a regular file inside the framework's lib/agents or the
# project's .claude/agents (a link to ~/.aws/credentials, or to the project's own
# .env, became an agent's prompt). A file that fails a rule is skipped with one
# warning, and the other agents still compose.
# Go twin: cli-go/internal/agentscompose/agentfile.go (BareAgentID, DisplayValue,
# InspectAgentFile). The text of every message here is byte-identical to the Go
# twin's; compose_bash_parity_test.go and tests/run-agent-enums-test.sh keep it so.
# `yakos validate` sources this file for the same functions.

YK_AGENTS_BARE_ID_RULE='1 to 128 of A-Z a-z 0-9 . _ -, starting with a letter or digit, no ".."'

# yk_agents_bare_id <value>
#   Exit 0 when the value is a bare agent id: 1 to 128 of A-Z a-z 0-9 . _ -,
#   starting with a letter or digit, with no "..". Bytes, whatever the locale.
yk_agents_bare_id() {
    local LC_ALL=C v="$1"
    [ -n "$v" ] || return 1
    [ "${#v}" -le 128 ] || return 1
    case "$v" in *..*) return 1 ;; esac
    case "$v" in [A-Za-z0-9]*) ;; *) return 1 ;; esac
    case "$v" in *[!A-Za-z0-9._-]*) return 1 ;; esac
    return 0
}

# yk_agents_display_value <value>
#   The value as a message shows it: every byte outside printable ASCII becomes
#   "?", at most 64 bytes are shown and "..." says there were more, in double
#   quotes. The value is untrusted, so no escape sequence reaches a log line.
yk_agents_display_value() {
    local LC_ALL=C v="$1" more=""
    if [ "${#v}" -gt 64 ]; then
        v="${v:0:64}"
        more="..."
    fi
    v="$(printf '%s' "$v" | LC_ALL=C tr -c '\040-\176' '?')"
    printf '"%s%s"' "$v" "$more"
}

# yk_agents_warn_skip <file> <reason>
yk_agents_warn_skip() {
    printf 'yakos: WARN: ignoring agent file %s: %s\n' "$1" "$2" >&2
}

# yk_agents_real_dir <path>
#   The physical directory that holds the file <path> finally names, following a
#   chain of symlinks (a relative link is read from the physical directory of the
#   link, as the kernel does). Fails on a loop or a directory that is missing.
#   Every cd in this file empties CDPATH: with it set, cd given a relative path
#   prints the directory it entered, and the extra line corrupted the answer, so
#   a link out of the project was taken for one inside it.
yk_agents_real_dir() {
    local p="$1" n=0 d l
    while [ -L "$p" ]; do
        n=$((n + 1))
        if [ "$n" -gt 40 ]; then return 1; fi
        d="$(CDPATH='' cd -P -- "$(dirname -- "$p")" 2>/dev/null && pwd -P)" || return 1
        l="$(readlink -- "$p")" || return 1
        case "$l" in
            /*) p="$l" ;;
            *) p="$d/$l" ;;
        esac
    done
    (CDPATH='' cd -P -- "$(dirname -- "$p")" 2>/dev/null && pwd -P)
}

# yk_agents_symlink_problem <file> <framework-agents-dir> <project-agents-dir or empty>
#   For a symlink that may not be followed, print why; print nothing for a file
#   that is not a symlink and for a link to a regular file inside one of the two
#   directories (the framework's lib/agents and the project's .claude/agents).
#   Inside means an ancestor directory of the target IS the root (same device and
#   inode), so a differently spelled path still matches.
yk_agents_symlink_problem() {
    local f="$1" fw_dir="$2" project_dir="${3:-}" dir root d inside=0
    [ -L "$f" ] || return 0
    if [ ! -f "$f" ] || ! dir="$(yk_agents_real_dir "$f")"; then
        echo "symlink does not resolve to a regular file"
        return 0
    fi
    for root in "$fw_dir" "$project_dir"; do
        if [ -z "$root" ] || [ ! -d "$root" ]; then continue; fi
        d="$dir"
        while :; do
            if [ "$d" -ef "$root" ]; then inside=1; break; fi
            if [ "$d" = "/" ] || [ "$d" = "." ]; then break; fi
            d="$(dirname -- "$d")"
        done
        if [ "$inside" = 1 ]; then break; fi
    done
    if [ "$inside" != 1 ]; then
        echo "symlink resolves outside the framework lib/agents and the project's .claude/agents"
    fi
    return 0
}

# yk_agents_warn_dir <kind> <dir> <reason>     kind is "agent" (or "skill")
yk_agents_warn_dir() {
    printf 'yakos: WARN: ignoring %s directory %s: %s\n' "$1" "$2" "$3" >&2
}

# yk_agents_dir_problem <project-root> <dir>
#   For a project's agent (or skill) directory reached through a symlink, the
#   directory itself or the .claude above it, print why it may not be read; print
#   nothing for a plain directory and for a link that resolves to a directory
#   inside the project. Files seen through a linked directory are regular files and
#   never reach yk_agents_symlink_problem, so the directory is checked itself. Inside
#   means an ancestor directory of the target IS the project (same device and
#   inode). Go twin: agentscompose.InspectProjectDir.
yk_agents_dir_problem() {
    local project="$1" dir="$2" real d
    [ -n "$project" ] || return 0
    if [ ! -L "$project/.claude" ] && [ ! -L "$dir" ]; then return 0; fi
    # Nothing there, so nothing is read through the link.
    if [ ! -e "$dir" ] && [ ! -L "$dir" ]; then return 0; fi
    if [ ! -d "$dir" ] || ! real="$(CDPATH='' cd -P -- "$dir" 2>/dev/null && pwd -P)"; then
        echo "symlink does not resolve to a directory"
        return 0
    fi
    d="$(dirname -- "$real")"
    while :; do
        if [ "$d" -ef "$project" ]; then return 0; fi
        if [ "$d" = "/" ] || [ "$d" = "." ]; then break; fi
        d="$(dirname -- "$d")"
    done
    echo "symlink resolves outside the project directory"
    return 0
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

    # Build model field — only include if non-empty AND resolvable.
    # Empirical: claude accepts haiku|sonnet|opus aliases on --agents.
    # The framework also uses cross-runtime semantic aliases
    # (cheap/balanced/best/reasoning); resolve those to claude's
    # concrete aliases here so agent frontmatter can stay
    # runtime-agnostic (see docs/supervisor-mode.md, runtime-pick).
    local model_filter='.'
    if [ -n "$model" ]; then
        local resolved_model="$model"
        case "$model" in
            cheap)            resolved_model='haiku' ;;
            balanced)         resolved_model='sonnet' ;;
            best|reasoning)   resolved_model='opus' ;;
            frontier)         resolved_model='fable' ;;
        esac
        case "$resolved_model" in
            haiku|sonnet|opus|fable) model="$resolved_model"; model_filter='.[$id].model = $model' ;;
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
    # third positional: the project root. A symlinked agent file or template may
    # resolve into <yakos-root>/lib/agents or <project-root>/.claude/agents.

    [ -d "$agents_dir" ] || return 0

    local file
    for file in "$agents_dir"/*.md; do
        local base reason
        base="$(basename -- "$file")"
        case "$base" in
            README.md|lead-template.md) continue ;;
        esac
        # A symlink is followed only to a regular file inside the framework's
        # lib/agents or the project's .claude/agents; anything else is skipped with
        # a warning, unread.
        reason="$(yk_agents_symlink_problem "$file" "$yakos_root/lib/agents" "${3:+$3/.claude/agents}")"
        if [ -n "$reason" ]; then
            yk_agents_warn_skip "$file" "$reason"
            continue
        fi
        [ -f "$file" ] || continue

        local fm body id model extends_name tools_list desc
        fm="$(yk_agents_extract_frontmatter "$file")"
        body="$(yk_agents_extract_body "$file")"

        # Use the filename stem as the canonical agent id.
        # The frontmatter `id` field is validated by `yakos validate` but
        # does NOT override the filename-derived key in the composed JSON.
        # This ensures that `yk_agents_compose` and `find_agent_file` agree
        # on the same key (both use the filename stem as the lookup path).
        # If frontmatter `id` were used here it would silently diverge from
        # any caller that resolves agents by filename (e.g. dispatch, tests).
        id="${base%.md}"

        model="$(yk_agents_fm_get "$fm" "model")"
        extends_name="$(yk_agents_fm_get "$fm" "extends")"
        tools_list="$(yk_agents_fm_list "$fm" "tools" || true)"

        # Resolve extends: the value is a bare agent id and the template is the
        # framework's lib/agents/<id>.md, read under the symlink rule above. A bad
        # value or an unsafe template skips this agent; a missing template means
        # the agent's own body alone.
        if [ -n "$extends_name" ]; then
            if ! yk_agents_bare_id "$extends_name"; then
                yk_agents_warn_skip "$file" "extends value $(yk_agents_display_value "$extends_name") is not a bare agent id ($YK_AGENTS_BARE_ID_RULE)"
                continue
            fi
            reason="$(yk_agents_symlink_problem "$yakos_root/lib/agents/${extends_name}.md" "$yakos_root/lib/agents" "${3:+$3/.claude/agents}")"
            if [ -n "$reason" ]; then
                yk_agents_warn_skip "$file" "extends $(yk_agents_display_value "$extends_name"): $reason"
                continue
            fi
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
    local proj_dir="" dir_reason=""
    if [ -n "$project_root" ]; then
        # The project's agent directory is checked itself: a link to a directory
        # outside the project is skipped whole, with one warning.
        dir_reason="$(yk_agents_dir_problem "$project_root" "$project_root/.claude/agents")"
        if [ -n "$dir_reason" ]; then
            yk_agents_warn_dir agent "$project_root/.claude/agents" "$dir_reason"
        elif [ -d "$project_root/.claude/agents" ]; then
            proj_dir="$project_root/.claude/agents"
        fi
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
