#compdef yakos
# SPDX-License-Identifier: Apache-2.0
# yakos.zsh — zsh completion for the yakos CLI.
#
# Install:
#   yakos completion install                # auto-detects shell
# Or manually:
#   mkdir -p ~/.zsh/completions
#   yakos completion zsh > ~/.zsh/completions/_yakos
#   # then in ~/.zshrc (before compinit):
#   fpath=(~/.zsh/completions $fpath)
#   autoload -U compinit && compinit

_yakos() {
    local context state state_descr line
    typeset -A opt_args

    local -a top_cmds runtimes
    top_cmds=(
        'quickstart:install (if needed) → init (if cwd is git repo) → start'
        'install:install yakOS into ~/.claude'
        'uninstall:remove yakOS-owned symlinks'
        'update:pull framework + refresh symlinks (--all for per-project)'
        'init:bootstrap a project'
        'doctor:verify install + environment health'
        'validate:standards check'
        'archive:roll work/current/ to work/archive/<tag>/'
        'status:per-project dashboard'
        'team:team lifecycle'
        'start:launch a session'
        'auth:per-runtime auth: status/login/logout/set-default'
        'dispatch:one-shot cross-runtime agent dispatch'
        'memory:portable yakOS memory'
        'agent:agent file lifecycle'
        'agents:agent file lifecycle (plural alias)'
        'cost:aggregate dispatch-log telemetry'
        'session:session lifecycle export + listing'
        'migrate:apply schema migrations to a project'
        'plugin:plugin management'
        'teach:teach the framework new patterns'
        'soul:operator-personal soul files'
        'retro:manage 10-cycle retrospectives'
        'skill:manage skill candidates'
        'compact:context compaction'
        'checkpoint:session-state snapshots'
        'kanban:3-column markdown WIP board'
        'env:dev/test/prod environment management'
        'standards:cross-project standards opt-ins'
        'peer:multi-dev coord (co-pilot mode)'
        'mcp:MCP server install/uninstall/status'
        'hooks:translate yakOS hooks to runtime-native config'
        'version-bump:bump VERSION + prepend CHANGELOG entry'
        'git-hooks:install/uninstall pre-push version gate'
        'completion:emit/install shell completion scripts'
    )

    runtimes=(claude claude-sdk codex agy antigravity-sdk gemini)

    _arguments -C \
        '1: :->cmd' \
        '*::arg:->args'

    case $state in
        cmd)
            _describe -t commands 'yakos command' top_cmds
            ;;
        args)
            local cmd=$line[1]
            local -a projects agents
            # Dynamic helpers
            _yakos_projects() {
                local ac="$HOME/agent-control" d
                [[ -d $ac ]] || return
                for d in $ac/*/; do
                    projects+=("${d:t}")
                done
            }
            _yakos_agent_names() {
                local root="${$(<"$HOME/.yakos" 2>/dev/null)}"
                if [[ -d "$root/lib/agents" ]]; then
                    for f in $root/lib/agents/*.md; do
                        [[ -f $f ]] || continue
                        local n="${f:t:r}"
                        [[ $n == README || $n == INDEX ]] && continue
                        agents+=($n)
                    done
                fi
                if [[ -d $HOME/.claude/agents ]]; then
                    for f in $HOME/.claude/agents/*.md; do
                        [[ -f $f ]] || continue
                        agents+=("${f:t:r}")
                    done
                fi
            }

            case $cmd in
                start|status|team|migrate|archive)
                    _yakos_projects
                    _describe -t projects 'project' projects
                    ;;
                dispatch)
                    _yakos_agent_names
                    _describe -t agents 'agent' agents
                    ;;
                auth)
                    if (( CURRENT == 2 )); then
                        _values 'auth subcommand' \
                            'status[show runtime auth state]' \
                            'login[log into a runtime]' \
                            'logout[clear runtime credentials]' \
                            'set-default[set default runtime]'
                    elif (( CURRENT == 3 )); then
                        _values 'runtime' $runtimes --all
                    fi
                    ;;
                peer)
                    if (( CURRENT == 2 )); then
                        _values 'peer subcommand' \
                            'status' 'log' 'claim' 'release' 'claims' \
                            'deadlock' 'propose-mode' 'respond-mode'
                    fi
                    ;;
                mcp)
                    if (( CURRENT == 2 )); then
                        _values 'mcp subcommand' \
                            'install' 'uninstall' 'status' 'probe'
                    fi
                    ;;
                memory)
                    if (( CURRENT == 2 )); then
                        _values 'memory subcommand' \
                            'list' 'show' 'put' 'migrate-from-claude' \
                            'sync' 'diff'
                    fi
                    ;;
                standards)
                    if (( CURRENT == 2 )); then
                        _values 'standards subcommand' \
                            'list' 'enable' 'disable' 'check' 'init'
                    fi
                    ;;
                skill)
                    if (( CURRENT == 2 )); then
                        _values 'skill subcommand' \
                            'candidates' 'promote' 'reject' 'defer' 'stats'
                    fi
                    ;;
                soul)
                    if (( CURRENT == 2 )); then
                        _values 'soul subcommand' \
                            'show' 'edit' 'history' 'revert' \
                            'pending' 'approve' 'reject'
                    fi
                    ;;
                retro)
                    if (( CURRENT == 2 )); then
                        _values 'retro subcommand' \
                            'now' 'last' 'history' 'status' \
                            'disable' 'enable'
                    fi
                    ;;
                completion)
                    if (( CURRENT == 2 )); then
                        _values 'completion subcommand' \
                            'bash' 'zsh' 'install'
                    fi
                    ;;
                hooks)
                    if (( CURRENT == 2 )); then
                        _values 'hooks subcommand' 'install' 'status'
                    elif (( CURRENT == 3 )) && [[ $line[2] == install ]]; then
                        _values 'runtime' $runtimes
                    fi
                    ;;
                quickstart)
                    _arguments \
                        '--runtime[runtime to start]:runtime:('"$runtimes"')' \
                        '--multi-dev[enable co-pilot mode]' \
                        '--safe[prompts on]' \
                        '--dry-run[preview, do not exec]'
                    ;;
                init)
                    _arguments \
                        '--project[path to project repo]:project:_files -/' \
                        '--force[overwrite existing hooks]' \
                        '--with-gate[install pre-push version gate]' \
                        '--multi-dev[enable co-pilot mode]'
                    ;;
                update)
                    _arguments \
                        '--all[walk every ~/agent-control/*/ project]' \
                        '--allow-non-ff[allow non-fast-forward pulls]'
                    ;;
                doctor)
                    _arguments \
                        '--probe-runtime[report runtime feature state]' \
                        '--fix[auto-remediate cheap fixes]'
                    ;;
                validate)
                    _arguments \
                        '--strict[promote warnings to errors]' \
                        '--all[also validate user-installed plugins]'
                    ;;
                version-bump)
                    _arguments \
                        '--component[bump tier]:tier:(major minor patch hotfix)' \
                        '--message[changelog text]:text' \
                        '--no-commit[do not git commit]'
                    ;;
            esac
            ;;
    esac
}

_yakos "$@"
