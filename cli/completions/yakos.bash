# SPDX-License-Identifier: Apache-2.0
# yakos.bash — bash completion for the yakos CLI.
#
# Install:
#   yakos completion install                  # auto-detects shell
# Or manually:
#   yakos completion bash > ~/.local/share/bash-completion/completions/yakos
#   # then in ~/.bashrc:
#   [ -f ~/.local/share/bash-completion/completions/yakos ] && \
#       . ~/.local/share/bash-completion/completions/yakos
#
# Covers: top-level subcommands, nested subcommands for the multi-verb
# entry points (peer, mcp, auth, memory, standards, etc.), dynamic
# project name completion from ~/agent-control/*/, runtime names for
# --runtime and auth args.

_yakos() {
    local cur prev words cword
    _init_completion 2>/dev/null || {
        # Fallback if bash-completion isn't sourced
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
        words=("${COMP_WORDS[@]}")
        cword=$COMP_CWORD
    }

    # Top-level subcommands.
    local top_cmds="quickstart install uninstall update init doctor validate \
archive status team start auth dispatch memory agent agents cost session \
migrate plugin teach soul retro skill compact checkpoint kanban env standards \
peer mcp hooks version-bump git-hooks completion --help --version -h -v help"

    # Known runtimes — used for --runtime, auth, etc.
    local runtimes="claude claude-sdk codex agy antigravity-sdk gemini"

    # Top-level subcommand completion.
    if [ "$cword" -eq 1 ]; then
        # shellcheck disable=SC2207
        COMPREPLY=( $(compgen -W "$top_cmds" -- "$cur") )
        return 0
    fi

    # Subcommand-specific completion.
    local cmd="${words[1]}"

    # Helper: list project names from ~/agent-control/*/
    _yakos_projects() {
        local ac="$HOME/agent-control"
        [ -d "$ac" ] || return
        local d
        for d in "$ac"/*/; do
            [ -d "$d" ] || continue
            basename -- "$d"
        done
    }

    # Helper: list agent names from framework + user-installed
    _yakos_agents() {
        local d
        # Framework agents (resolved via ~/.yakos pointer)
        if [ -f "$HOME/.yakos" ]; then
            local root
            root="$(cat "$HOME/.yakos" 2>/dev/null)"
            [ -d "$root/lib/agents" ] && \
                find "$root/lib/agents" -maxdepth 1 -name '*.md' \
                    -not -name 'README.md' -not -name 'INDEX.md' \
                    -exec basename {} .md \;
        fi
        # User-symlinked agents
        if [ -d "$HOME/.claude/agents" ]; then
            find "$HOME/.claude/agents" -maxdepth 1 -name '*.md' \
                -exec basename {} .md \; 2>/dev/null
        fi
    }

    case "$cmd" in
        start|status|team|migrate|archive)
            # First positional is a project name
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "$(_yakos_projects)" -- "$cur") )
                return 0
            fi
            ;;
        dispatch)
            # First positional is an agent name
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "$(_yakos_agents)" -- "$cur") )
                return 0
            fi
            ;;
        auth)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "status login logout set-default" -- "$cur") )
                return 0
            fi
            if [ "$cword" -eq 3 ]; then
                local sub="${words[2]}"
                case "$sub" in
                    status|login|logout|set-default)
                        # shellcheck disable=SC2207
                        COMPREPLY=( $(compgen -W "$runtimes --all" -- "$cur") )
                        return 0
                        ;;
                esac
            fi
            ;;
        peer)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "status log claim release claims deadlock propose-mode respond-mode" -- "$cur") )
                return 0
            fi
            ;;
        mcp)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "install uninstall status probe" -- "$cur") )
                return 0
            fi
            ;;
        memory)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "list show put migrate-from-claude sync diff" -- "$cur") )
                return 0
            fi
            ;;
        standards)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "list enable disable check init" -- "$cur") )
                return 0
            fi
            ;;
        skill)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "candidates promote reject defer stats" -- "$cur") )
                return 0
            fi
            ;;
        soul)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "show edit history revert pending approve reject" -- "$cur") )
                return 0
            fi
            ;;
        retro)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "now last history status disable enable" -- "$cur") )
                return 0
            fi
            ;;
        compact)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "now threshold history" -- "$cur") )
                return 0
            fi
            ;;
        checkpoint)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "now list resume clean" -- "$cur") )
                return 0
            fi
            ;;
        kanban)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "add move done --html" -- "$cur") )
                return 0
            fi
            ;;
        env)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "status list validate promote" -- "$cur") )
                return 0
            fi
            ;;
        agent|agents)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "new lint" -- "$cur") )
                return 0
            fi
            ;;
        hooks)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "install status" -- "$cur") )
                return 0
            fi
            if [ "$cword" -eq 3 ] && [ "${words[2]}" = "install" ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "$runtimes" -- "$cur") )
                return 0
            fi
            ;;
        completion)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "bash zsh install" -- "$cur") )
                return 0
            fi
            ;;
        version-bump)
            if [ "$prev" = "--component" ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "major minor patch hotfix" -- "$cur") )
                return 0
            fi
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--component --message --no-commit" -- "$cur") )
            return 0
            ;;
        git-hooks)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "install uninstall status" -- "$cur") )
                return 0
            fi
            ;;
        plugin)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "list install uninstall validate" -- "$cur") )
                return 0
            fi
            ;;
        teach)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "snapshot diff approve reject list" -- "$cur") )
                return 0
            fi
            ;;
        session)
            if [ "$cword" -eq 2 ]; then
                # shellcheck disable=SC2207
                COMPREPLY=( $(compgen -W "export list" -- "$cur") )
                return 0
            fi
            ;;
        cost)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--since --by" -- "$cur") )
            return 0
            ;;
        validate)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--strict --all" -- "$cur") )
            return 0
            ;;
        doctor)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--probe-runtime --fix" -- "$cur") )
            return 0
            ;;
        update)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--all --allow-non-ff" -- "$cur") )
            return 0
            ;;
        init)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--project --force --with-gate --multi-dev" -- "$cur") )
            return 0
            ;;
        quickstart)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "--runtime --multi-dev --safe --dry-run" -- "$cur") )
            return 0
            ;;
    esac

    # Flag-aware completion for shared flags.
    case "$prev" in
        --runtime)
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -W "$runtimes" -- "$cur") )
            return 0
            ;;
        --project)
            # File-path completion
            # shellcheck disable=SC2207
            COMPREPLY=( $(compgen -d -- "$cur") )
            return 0
            ;;
    esac
}

complete -F _yakos yakos
