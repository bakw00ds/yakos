#!/usr/bin/env bash
# runtime-resolve.sh — resolve a runtime id to the adapter that implements it.
#
# Purpose: keep `yakos start`, `yakos dispatch`, `yakos auth`, and
# `yakos doctor` decoupled from individual runtime adapters. Each adapter
# at cli/lib/runtimes/<name>.sh exports yk_rt_<name>_<verb> functions;
# this file picks one and re-exports the namespaced functions under a
# stable yk_rt_<verb> alias for the caller.
#
# Usage (sourced):
#     . "$YAKOS_LIB/runtime-resolve.sh"
#     yk_rt_load <runtime-id>     # claude | codex | gemini
#     yk_rt_launch ...            # now bound to the loaded adapter

set -eu

: "${YAKOS_LIB:?runtime-resolve.sh: YAKOS_LIB must be set}"

# Known runtimes in order of capability fidelity (closest analog to
# yakos's original target — Claude Code — first).
# - claude:     Anthropic Claude Code CLI (interactive + headless)
# - claude-sdk: Anthropic Claude Agent SDK (Python, headless-only;
#               bundles the Claude Code CLI under the hood — launch
#               delegates to claude.sh)
# - codex:      OpenAI Codex CLI
# - agy:        Google Antigravity CLI (Gemini 3.x); replaces gemini
# - gemini:     deprecated shim for legacy Gemini CLI, sources agy.sh
YK_RT_KNOWN_BUILTIN="claude claude-sdk codex agy gemini"

# yk_rt_plugins
#   Stdout one plugin runtime id per line, derived from
#   ~/.yakos/plugins/<id>/runtime.sh.
yk_rt_plugins() {
    local d="$HOME/.yakos/plugins"
    [ -d "$d" ] || return 0
    local p
    for p in "$d"/*/runtime.sh; do
        [ -f "$p" ] || continue
        # parent dir name = plugin id
        basename -- "$(dirname -- "$p")"
    done
}

yk_rt_known() {
    {
        printf '%s\n' "$YK_RT_KNOWN_BUILTIN" | tr ' ' '\n'
        yk_rt_plugins
    } | grep -v '^$' | sort -u
}

yk_rt_is_known() {
    # Built-in fast path.
    case " $YK_RT_KNOWN_BUILTIN " in
        *" $1 "*) return 0 ;;
    esac
    # Plugin lookup.
    [ -f "$HOME/.yakos/plugins/$1/runtime.sh" ] && return 0
    return 1
}

# yk_rt_load <runtime-id>
#   Source the adapter at cli/lib/runtimes/<id>.sh and bind its
#   yk_rt_<id>_<verb> functions as yk_rt_<verb> for the rest of the
#   script's lifetime. Subsequent loads replace the binding.
yk_rt_load() {
    local id="$1"
    if ! yk_rt_is_known "$id"; then
        ct_die "runtime-resolve: unknown runtime '$id' (known: $YK_RT_KNOWN)"
    fi
    local adapter="$YAKOS_LIB/runtimes/${id}.sh"
    # v0.9+: plugin runtimes live under ~/.yakos/plugins/<id>/runtime.sh.
    # Built-in adapters take precedence; plugin path is the fallback.
    if [ ! -f "$adapter" ]; then
        local plugin_adapter="$HOME/.yakos/plugins/${id}/runtime.sh"
        if [ -f "$plugin_adapter" ]; then
            adapter="$plugin_adapter"
        else
            ct_die "runtime-resolve: adapter not found at $adapter (and no plugin at $plugin_adapter)"
        fi
    fi
    # shellcheck source=/dev/null
    . "$adapter"
    YK_RT_CURRENT="$id"
    export YK_RT_CURRENT

    # Bind namespaced functions to stable aliases. Each adapter must
    # implement every verb listed here; missing ones will surface at
    # call time as "command not found" rather than a silent fallback.
    # Sanitize the id for the bash function name (hyphens → underscores),
    # so e.g. claude-sdk → yk_rt_claude_sdk_<verb>. The runtime id stays
    # hyphenated everywhere it's user-facing (config, env, CLI).
    local id_safe verb
    id_safe="${id//-/_}"
    for verb in id check_cli check_auth capabilities \
                materialize_agents cleanup_agents \
                launch dispatch; do
        eval "yk_rt_${verb}() { yk_rt_${id_safe}_${verb} \"\$@\"; }"
    done
}

# yk_rt_default
#   Stdout the default runtime id. Reads YAKOS_RUNTIME env var first,
#   then $HOME/.yakos-state/default-runtime, falls back to "claude".
yk_rt_default() {
    if [ -n "${YAKOS_RUNTIME:-}" ]; then
        printf '%s\n' "$YAKOS_RUNTIME"
        return 0
    fi
    local pref="$HOME/.yakos-state/default-runtime"
    if [ -f "$pref" ]; then
        local id
        id="$(head -1 "$pref" 2>/dev/null | tr -d '[:space:]')"
        if [ -n "$id" ] && yk_rt_is_known "$id"; then
            printf '%s\n' "$id"
            return 0
        fi
    fi
    printf 'claude\n'
}

# yk_rt_set_default <id>
#   Persist the operator's default-runtime preference at
#   ~/.yakos-state/default-runtime. Used by `yakos auth login <id> --as-default`.
yk_rt_set_default() {
    local id="$1"
    yk_rt_is_known "$id" || ct_die "runtime-resolve: unknown runtime '$id'"
    mkdir -p "$HOME/.yakos-state" 2>/dev/null || true
    printf '%s\n' "$id" > "$HOME/.yakos-state/default-runtime"
}

# yk_rt_capability <runtime-id> <capability-name>
#   Return 0 if the runtime advertises the capability; else 1.
#   Loads the adapter as a side-effect (cheap; idempotent).
yk_rt_capability() {
    local id="$1"
    local cap="$2"
    yk_rt_load "$id"
    case ",$(yk_rt_capabilities)," in
        *",$cap,"*) return 0 ;;
        *) return 1 ;;
    esac
}
