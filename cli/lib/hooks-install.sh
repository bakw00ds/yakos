#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# hooks-install.sh — install yakOS reference hooks into a runtime's
# native hook configuration.
#
# Purpose: yakOS's lib/hooks/ scripts are written against Claude Code's
# hook stdin/stdout shape. Codex and gemini both have hook surfaces but
# different schemas. This script translates the subset of yakOS hooks
# that map cleanly (path-allowlist, secret-scan) into each runtime's
# native config file.

set -eu

: "${YAKOS_ROOT:?hooks-install.sh: YAKOS_ROOT must be set}"
: "${YAKOS_LIB:?hooks-install.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

usage() {
    cat <<'EOF'
yakos hooks <subcommand> [args...]

Subcommands:
  install <runtime> --project <path>   Translate yakOS hooks to the
                                       runtime's native config.
                                       Currently supports: codex, gemini.
  status [<project>]                   Report which hooks are installed
                                       per-runtime for the project.

Hooks translated for codex / gemini in v0.5:
  - path-allowlist (PreToolUse / BeforeTool)
  - secret-scan    (PreToolUse / BeforeTool)

NOT translated (yakOS-specific events or claude-only primitives):
  - mailbox-mirror, team-lifecycle, task-complete-dispatch,
    task-dependency-gate, session-end-check, path-log

The project's <repo>/scripts/hooks/ (copied at `yakos init` time)
remains the source-of-truth for hook content. This subcommand only
writes the runtime config that points at those scripts.

Examples:
  yakos hooks install codex --project ~/code/myapp
  yakos hooks status ~/code/myapp
EOF
}

# Translatable hooks: yakos hook script | claude event | codex event |
# gemini event | matcher tool names...
HOOK_PORTS=$(cat <<'EOF'
path-allowlist|PreToolUse|PreToolUse|BeforeTool|Edit|Write
secret-scan|PreToolUse|PreToolUse|BeforeTool|Edit|Write|apply_patch
EOF
)

install_codex_hooks() {
    local project="$1"
    local force="$2"
    local target="$project/.codex/hooks.json"
    local hooks_dir="$project/scripts/hooks"

    [ -d "$hooks_dir" ] || ct_die "hooks install codex: $hooks_dir missing — run 'yakos init' first"

    mkdir -p "$project/.codex"
    if [ -f "$target" ] && [ "$force" != "1" ]; then
        cp "$target" "$target.yakos-bak-$(ct_iso_utc)"
    fi

    # v0.8: also translate the project's path-allowlist.json data into
    # codex's permissions.<name> table where possible. The hook script
    # remains the runtime-time gate; permissions adds defense in depth.
    install_codex_permissions "$project"

    local entries='[]'
    while IFS='|' read -r hook_name _ codex_evt _ a b c; do
        [ -n "$hook_name" ] || continue
        local hook_path="$hooks_dir/${hook_name}.sh"
        if [ ! -x "$hook_path" ]; then
            ct_log "hooks install codex: $hook_path not found or not executable; skipping"
            continue
        fi
        local matcher_regex
        matcher_regex="$(printf '%s|%s|%s' "$a" "$b" "$c" | tr '|' '\n' | grep -v '^$' | tr '\n' '|' | sed 's/|$//')"
        entries="$(printf '%s' "$entries" | jq \
            --arg cmd "$hook_path" \
            --arg matcher "^($matcher_regex)$" \
            --arg evt "$codex_evt" \
            '. + [{event: $evt, matcher: $matcher, command: $cmd, timeout: 30}]')"
    done <<< "$HOOK_PORTS"

    local final
    final="$(printf '%s' "$entries" | jq '
        reduce .[] as $e ({}; .[$e.event] = (.[$e.event] // []) + [
            {matcher: $e.matcher, command: $e.command, timeout: $e.timeout}
        ]) | {hooks: .}
    ')"
    printf '%s\n' "$final" > "$target"
    ct_log "wrote codex hooks config: $target ($(printf '%s' "$entries" | jq 'length') entries)"
}

install_codex_permissions() {
    local project="$1"
    local src="$project/.claude/path-allowlist.json"
    local cfg="$project/.codex/config.toml"
    [ -f "$src" ] || return 0
    [ -f "$cfg" ] || printf '# yakOS managed: codex configuration\n' > "$cfg"

    # Extract the allow + deny lists from yakOS's claude-shaped JSON.
    # Schema (yakos): {"allow": ["pattern", ...], "deny": [...]}
    local allow deny
    allow="$(jq -r '.allow // [] | .[]' "$src" 2>/dev/null | tr '\n' ',' | sed 's/,$//')"
    deny="$(jq -r '.deny // [] | .[]' "$src" 2>/dev/null | tr '\n' ',' | sed 's/,$//')"

    # Strip any prior yakos-managed permissions block (idempotent re-run).
    awk '
        /^# yakos-permissions-start$/ { skip = 1; next }
        /^# yakos-permissions-end$/   { skip = 0; next }
        !skip { print }
    ' "$cfg" > "${cfg}.t" && mv "${cfg}.t" "$cfg"

    {
        printf '\n# yakos-permissions-start\n'
        printf '[permissions.yakos-paths]\n'
        if [ -n "$allow" ]; then
            printf 'filesystem.allow_glob = [%s]\n' \
                "$(printf '%s\n' "$allow" | tr ',' '\n' | awk 'NF { printf "\"%s\",", $0 }' | sed 's/,$//')"
        fi
        if [ -n "$deny" ]; then
            printf 'filesystem.deny_glob = [%s]\n' \
                "$(printf '%s\n' "$deny" | tr ',' '\n' | awk 'NF { printf "\"%s\",", $0 }' | sed 's/,$//')"
        fi
        printf '# yakos-permissions-end\n'
    } >> "$cfg"
    ct_log "translated path-allowlist.json into $cfg [permissions.yakos-paths]"
}

install_agy_hooks() {
    # Per Antigravity migration guide, agy hooks share JSON format with
    # the Gemini CLI's. Only the output path changes:
    #   gemini → <project>/.gemini/settings.json (.hooks block)
    #   agy    → <project>/.agents/hooks.json    (top-level object)
    # Same translation logic; new destination.
    local project="$1"
    local force="$2"
    local target="$project/.agents/hooks.json"
    local hooks_dir="$project/scripts/hooks"

    [ -d "$hooks_dir" ] || ct_die "hooks install agy: $hooks_dir missing — run 'yakos init' first"

    mkdir -p "$project/.agents"
    if [ -f "$target" ] && [ "$force" != "1" ]; then
        cp "$target" "$target.yakos-bak-$(ct_iso_utc)"
    else
        [ -f "$target" ] || printf '{}\n' > "$target"
    fi

    local hooks_block='{}'
    while IFS='|' read -r hook_name _ _ gemini_evt a b c; do
        [ -n "$hook_name" ] || continue
        local hook_path="$hooks_dir/${hook_name}.sh"
        if [ ! -x "$hook_path" ]; then
            ct_log "hooks install agy: $hook_path not found; skipping"
            continue
        fi
        local matcher_regex
        matcher_regex="$(printf '%s|%s|%s' "$a" "$b" "$c" | tr '|' '\n' | grep -v '^$' | tr '\n' '|' | sed 's/|$//')"
        hooks_block="$(printf '%s' "$hooks_block" | jq \
            --arg evt "$gemini_evt" \
            --arg name "yakos-${hook_name}" \
            --arg cmd "$hook_path" \
            --arg matcher "^($matcher_regex)$" \
            '.[$evt] = (.[$evt] // []) + [{
                matcher: $matcher,
                hooks: [{name: $name, type: "command", command: $cmd, timeout: 30000}]
            }]')"
    done <<< "$HOOK_PORTS"

    # agy hooks.json: top-level object with .hooks key (Gemini-shape but
    # extracted out of settings.json into its own file per migration guide).
    local final
    final="$(jq --argjson hooks "$hooks_block" \
        '{hooks: ((.hooks // {}) + $hooks)}' "$target")"
    printf '%s\n' "$final" > "$target"
    ct_log "wrote agy hooks into: $target"
}

install_gemini_hooks() {
    local project="$1"
    local force="$2"
    local target="$project/.gemini/settings.json"
    local hooks_dir="$project/scripts/hooks"

    [ -d "$hooks_dir" ] || ct_die "hooks install gemini: $hooks_dir missing — run 'yakos init' first"

    mkdir -p "$project/.gemini"
    if [ -f "$target" ] && [ "$force" != "1" ]; then
        cp "$target" "$target.yakos-bak-$(ct_iso_utc)"
    else
        [ -f "$target" ] || printf '{}\n' > "$target"
    fi

    local hooks_block='{}'
    while IFS='|' read -r hook_name _ _ gemini_evt a b c; do
        [ -n "$hook_name" ] || continue
        local hook_path="$hooks_dir/${hook_name}.sh"
        if [ ! -x "$hook_path" ]; then
            ct_log "hooks install gemini: $hook_path not found; skipping"
            continue
        fi
        local matcher_regex
        matcher_regex="$(printf '%s|%s|%s' "$a" "$b" "$c" | tr '|' '\n' | grep -v '^$' | tr '\n' '|' | sed 's/|$//')"
        hooks_block="$(printf '%s' "$hooks_block" | jq \
            --arg evt "$gemini_evt" \
            --arg name "yakos-${hook_name}" \
            --arg cmd "$hook_path" \
            --arg matcher "^($matcher_regex)$" \
            '.[$evt] = (.[$evt] // []) + [{
                matcher: $matcher,
                hooks: [{name: $name, type: "command", command: $cmd, timeout: 30000}]
            }]')"
    done <<< "$HOOK_PORTS"

    local final
    final="$(jq --argjson hooks "$hooks_block" \
        '. + {hooks: ((.hooks // {}) + $hooks)}' "$target")"
    printf '%s\n' "$final" > "$target"
    ct_log "wrote gemini hooks into: $target"
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    install) ;;
    status) ;;
    *) ct_die "hooks: unknown subcommand '$SUB' (try --help)" ;;
esac

if [ "$SUB" = "install" ]; then
    RUNTIME=""
    PROJECT=""
    FORCE=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -h|--help) usage; exit 0 ;;
            --project) shift; [ "$#" -gt 0 ] || ct_die "hooks install: --project requires a path"; PROJECT="$1" ;;
            --project=*) PROJECT="${1#--project=}" ;;
            --force) FORCE=1 ;;
            -*) ct_die "hooks install: unknown flag '$1'" ;;
            *)
                if [ -z "$RUNTIME" ]; then RUNTIME="$1"
                else ct_die "hooks install: too many positional args"
                fi
                ;;
        esac
        shift
    done

    [ -n "$RUNTIME" ] || { usage >&2; ct_die "hooks install: <runtime> required"; }
    [ -n "$PROJECT" ] || { usage >&2; ct_die "hooks install: --project required"; }
    yk_rt_is_known "$RUNTIME" || ct_die "hooks install: unknown runtime '$RUNTIME'"
    [ -d "$PROJECT" ] || ct_die "hooks install: project not found: $PROJECT"

    case "$RUNTIME" in
        claude)
            cat >&2 <<EOF
hooks install claude: claude is yakOS's source-of-truth runtime.
Hooks live at <project>/scripts/hooks/ and are wired via
<project>/.claude/settings.json on init. Nothing to translate.
EOF
            exit 0
            ;;
        codex)  install_codex_hooks  "$PROJECT" "$FORCE" ;;
        gemini) install_gemini_hooks "$PROJECT" "$FORCE" ;;
        agy)    install_agy_hooks    "$PROJECT" "$FORCE" ;;
    esac
    exit 0
fi

if [ "$SUB" = "status" ]; then
    PROJECT="${1:-}"
    if [ -z "$PROJECT" ]; then
        cwd_real="$(ct_realpath "$PWD")"
        if [ -d "$HOME/agent-control" ]; then
            for cd_path in "$HOME/agent-control"/*/.project-path; do
                [ -f "$cd_path" ] || continue
                p="$(head -1 "$cd_path")"; pr="$(ct_realpath "$p" 2>/dev/null || true)"
                case "$cwd_real" in "$pr"|"$pr"/*) PROJECT="$pr"; break ;; esac
            done
        fi
    fi
    [ -n "$PROJECT" ] || ct_die "hooks status: pass <project> or run from inside it"

    echo "yakos hooks status — $PROJECT"
    echo
    echo "  claude:"
    if [ -f "$PROJECT/.claude/settings.json" ]; then
        n="$(jq '[(.hooks // {}) | to_entries[] | .value | length] | add // 0' "$PROJECT/.claude/settings.json" 2>/dev/null || echo 0)"
        echo "    settings.json present: $n hook entries"
    else
        echo "    settings.json absent (run 'yakos init')"
    fi
    echo "  codex:"
    if [ -f "$PROJECT/.codex/hooks.json" ]; then
        n="$(jq '[.hooks // {} | to_entries[] | .value | length] | add // 0' "$PROJECT/.codex/hooks.json" 2>/dev/null || echo 0)"
        echo "    hooks.json present: $n hook entries"
    else
        echo "    hooks.json absent (run 'yakos hooks install codex --project $PROJECT')"
    fi
    echo "  gemini:"
    if [ -f "$PROJECT/.gemini/settings.json" ]; then
        n="$(jq '[.hooks // {} | to_entries[] | .value | length] | add // 0' "$PROJECT/.gemini/settings.json" 2>/dev/null || echo 0)"
        echo "    settings.json present: $n hook entries"
    else
        echo "    settings.json absent (run 'yakos hooks install gemini --project $PROJECT')"
    fi
    exit 0
fi
