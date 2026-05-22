#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# project-config.sh — read project-level yakOS config.
#
# Purpose: support a declarative <project>/.yakos.yml that overrides
# per-agent frontmatter for projects with many agents that share a
# runtime preference. Read by start.sh, dispatch.sh, and agents-compose
# during materialization.
#
# Schema (all fields optional):
#
#   yakos: 0.8                         # schema version — used by future migrations
#   default-runtime: claude            # or codex | gemini
#   default-fallback: [claude]         # falls back if default unavailable
#   default-permission: bypass         # or safe
#
#   per-domain:                        # match agent.domain → runtime
#     code-review: codex
#     ui: gemini
#
#   model-aliases:                     # project-specific alias overrides
#     cheap:
#       claude: haiku
#       codex: gpt-5-nano
#       gemini: gemini-2.5-flash
#
# yakOS doesn't ship a yaml parser; this reader handles only the
# specific subset above via grep/awk for portability under bash 3.2.

set -eu

: "${YAKOS_LIB:?project-config.sh: YAKOS_LIB must be set}"

# Schema versions yakOS knows how to read. Bumping this list is the
# public marker that a future yakOS release may need a migration.
YK_PCFG_SCHEMA_KNOWN="0.7 0.8"
YK_PCFG_SCHEMA_CURRENT="0.8"

# yk_pcfg_path <project>
#   Stdout the canonical path of the project's .yakos.yml (whether
#   it exists or not).
yk_pcfg_path() {
    printf '%s\n' "$1/.yakos.yml"
}

# yk_pcfg_schema_check <project>
#   Read the `yakos:` schema field and verify it's a known version.
#   Empty (file absent or no field) returns 0 — backward compatibility
#   with pre-v0.8 projects. Unknown version returns 1 + warning to stderr.
yk_pcfg_schema_check() {
    local project="$1"
    local v
    v="$(yk_pcfg_get "$project" "yakos")"
    [ -n "$v" ] || return 0
    case " $YK_PCFG_SCHEMA_KNOWN " in
        *" $v "*) return 0 ;;
        *)
            ct_log "WARN: $project/.yakos.yml declares yakos: $v but yakOS knows $YK_PCFG_SCHEMA_KNOWN"
            return 1
            ;;
    esac
}

# yk_pcfg_get <project> <key>
#   Read a top-level scalar field. Returns empty string if missing.
#   Supports nested one-level keys via 'parent.child' (e.g.
#   'per-domain.code-review').
yk_pcfg_get() {
    local project="$1"
    local key="$2"
    local cfg
    cfg="$(yk_pcfg_path "$project")"
    [ -f "$cfg" ] || return 0

    case "$key" in
        *.*)
            local parent="${key%%.*}"
            local child="${key#*.}"
            awk -v parent="$parent" -v child="$child" '
                $0 ~ "^[[:space:]]*"parent"[[:space:]]*:" { in_block = 1; next }
                in_block && /^[^[:space:]]/ { exit }
                in_block && $0 ~ "^[[:space:]]+"child"[[:space:]]*:" {
                    sub("^[[:space:]]+"child"[[:space:]]*:[[:space:]]*", "", $0)
                    sub("[[:space:]]*$", "", $0)
                    sub("^\"", "", $0); sub("\"$", "", $0)
                    sub("^'"'"'", "", $0); sub("'"'"'$", "", $0)
                    print
                    exit
                }
            ' "$cfg"
            ;;
        *)
            awk -v key="$key" '
                $0 ~ "^"key"[[:space:]]*:" {
                    sub("^"key"[[:space:]]*:[[:space:]]*", "", $0)
                    sub("[[:space:]]*$", "", $0)
                    sub("^\"", "", $0); sub("\"$", "", $0)
                    sub("^'"'"'", "", $0); sub("'"'"'$", "", $0)
                    print
                    exit
                }
            ' "$cfg"
            ;;
    esac
}

# yk_pcfg_get_list <project> <key>
#   Read an inline list value: `key: [a, b, c]` → newline-separated.
yk_pcfg_get_list() {
    local raw
    raw="$(yk_pcfg_get "$1" "$2")"
    case "$raw" in
        \[*\])
            raw="${raw#[}"; raw="${raw%]}"
            printf '%s' "$raw" | tr ',' '\n' \
                | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' \
                | grep -v '^$' || true
            ;;
        *) : ;;
    esac
}

# yk_pcfg_resolve_model <project> <alias> <runtime>
#   Resolve a model alias to a concrete model name for a runtime.
#   Project .yakos.yml overrides framework defaults (lib/settings/
#   model-aliases.json).
yk_pcfg_resolve_model() {
    local project="$1"
    local alias="$2"
    local runtime="$3"
    local cfg
    cfg="$(yk_pcfg_path "$project")"

    # Project override first (yaml; we parse with awk).
    if [ -f "$cfg" ]; then
        local resolved
        resolved="$(awk -v alias="$alias" -v runtime="$runtime" '
            $0 ~ "^[[:space:]]*model-aliases[[:space:]]*:" { in_top = 1; next }
            in_top && /^[^[:space:]]/ && !/^[[:space:]]*#/ { exit }
            in_top && $0 ~ "^[[:space:]]+"alias"[[:space:]]*:" { in_alias = 1; next }
            in_alias && $0 ~ "^[[:space:]]+[a-z]+[[:space:]]*:" && $0 !~ "^[[:space:]]+"runtime"[[:space:]]*:" {
                # Different alias starts; reset
                if ($0 !~ "^[[:space:]][[:space:]]+") { in_alias = 0 }
            }
            in_alias && $0 ~ "^[[:space:]]+"runtime"[[:space:]]*:" {
                sub("^[[:space:]]+"runtime"[[:space:]]*:[[:space:]]*", "", $0)
                sub("[[:space:]]*$", "", $0)
                sub("^\"", "", $0); sub("\"$", "", $0)
                print
                exit
            }
        ' "$cfg")"
        if [ -n "$resolved" ]; then
            printf '%s\n' "$resolved"
            return 0
        fi
    fi

    # Framework default.
    local defaults="$YAKOS_ROOT/lib/settings/model-aliases.json"
    if [ -f "$defaults" ] && command -v jq >/dev/null 2>&1; then
        jq -r --arg a "$alias" --arg r "$runtime" \
            '.aliases[$a][$r] // empty' "$defaults" 2>/dev/null
    fi
}

# yk_pcfg_resolve_runtime <project> <agent-id> <agent-domain> <agent-runtime-fm>
#   Resolve which runtime an agent should run on, applying:
#     1. agent's own frontmatter `runtime:` (highest priority)
#     2. <project>/.yakos.yml `per-domain.<domain>:`
#     3. <project>/.yakos.yml `default-runtime:`
#     4. (caller falls back to YAKOS_RUNTIME env / state file / claude)
#   Stdout the resolved runtime; empty string if none of the above
#   defines one (caller uses its own default chain).
yk_pcfg_resolve_runtime() {
    local project="$1"
    # $2 = agent_id (reserved for future per-agent rules in .yakos.yml)
    local agent_domain="$3"
    local agent_rt_fm="$4"

    if [ -n "$agent_rt_fm" ]; then
        printf '%s\n' "$agent_rt_fm"
        return 0
    fi
    if [ -n "$agent_domain" ]; then
        local from_domain
        from_domain="$(yk_pcfg_get "$project" "per-domain.$agent_domain")"
        if [ -n "$from_domain" ]; then
            printf '%s\n' "$from_domain"
            return 0
        fi
    fi
    yk_pcfg_get "$project" "default-runtime"
}
