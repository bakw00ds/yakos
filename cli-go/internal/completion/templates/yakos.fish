# SPDX-License-Identifier: Apache-2.0
# yakos.fish — fish completion for the yakos CLI.
#
# Install:
#   yakos completion install                # auto-detects shell (fish)
# Or manually:
#   mkdir -p ~/.config/fish/completions
#   yakos completion fish > ~/.config/fish/completions/yakos.fish

# Disable file completions for yakos (we provide explicit ones).
complete -c yakos -f

# Top-level subcommands.
complete -c yakos -n '__fish_use_subcommand' -a 'quickstart'   -d 'install (if needed) → init (if cwd is git repo) → start'
complete -c yakos -n '__fish_use_subcommand' -a 'install'      -d 'install yakOS into ~/.claude'
complete -c yakos -n '__fish_use_subcommand' -a 'uninstall'    -d 'remove yakOS-owned symlinks'
complete -c yakos -n '__fish_use_subcommand' -a 'update'       -d 'pull framework + refresh symlinks'
complete -c yakos -n '__fish_use_subcommand' -a 'init'         -d 'bootstrap a project'
complete -c yakos -n '__fish_use_subcommand' -a 'doctor'       -d 'verify install + environment health'
complete -c yakos -n '__fish_use_subcommand' -a 'validate'     -d 'standards check'
complete -c yakos -n '__fish_use_subcommand' -a 'archive'      -d 'roll work/current/ to work/archive/<tag>/'
complete -c yakos -n '__fish_use_subcommand' -a 'status'       -d 'per-project dashboard'
complete -c yakos -n '__fish_use_subcommand' -a 'team'         -d 'team lifecycle'
complete -c yakos -n '__fish_use_subcommand' -a 'start'        -d 'launch a session'
complete -c yakos -n '__fish_use_subcommand' -a 'auth'         -d 'per-runtime auth: status/login/logout/set-default'
complete -c yakos -n '__fish_use_subcommand' -a 'dispatch'     -d 'one-shot cross-runtime agent dispatch'
complete -c yakos -n '__fish_use_subcommand' -a 'memory'       -d 'portable yakOS memory'
complete -c yakos -n '__fish_use_subcommand' -a 'agent'        -d 'agent file lifecycle'
complete -c yakos -n '__fish_use_subcommand' -a 'agents'       -d 'agent file lifecycle (plural alias)'
complete -c yakos -n '__fish_use_subcommand' -a 'cost'         -d 'aggregate dispatch-log telemetry'
complete -c yakos -n '__fish_use_subcommand' -a 'session'      -d 'session lifecycle export + listing'
complete -c yakos -n '__fish_use_subcommand' -a 'migrate'      -d 'apply schema migrations to a project'
complete -c yakos -n '__fish_use_subcommand' -a 'plugin'       -d 'plugin management'
complete -c yakos -n '__fish_use_subcommand' -a 'teach'        -d 'teach the framework new patterns'
complete -c yakos -n '__fish_use_subcommand' -a 'soul'         -d 'operator-personal soul files'
complete -c yakos -n '__fish_use_subcommand' -a 'retro'        -d 'manage 10-cycle retrospectives'
complete -c yakos -n '__fish_use_subcommand' -a 'skill'        -d 'manage skill candidates'
complete -c yakos -n '__fish_use_subcommand' -a 'compact'      -d 'context compaction'
complete -c yakos -n '__fish_use_subcommand' -a 'checkpoint'   -d 'session-state snapshots'
complete -c yakos -n '__fish_use_subcommand' -a 'kanban'       -d '3-column markdown WIP board'
complete -c yakos -n '__fish_use_subcommand' -a 'env'          -d 'dev/test/prod environment management'
complete -c yakos -n '__fish_use_subcommand' -a 'standards'    -d 'cross-project standards opt-ins'
complete -c yakos -n '__fish_use_subcommand' -a 'peer'         -d 'multi-dev coord (co-pilot mode)'
complete -c yakos -n '__fish_use_subcommand' -a 'mcp'          -d 'MCP server install/uninstall/status'
complete -c yakos -n '__fish_use_subcommand' -a 'git-hooks'    -d 'manage git hooks (pre-push version gate)'
complete -c yakos -n '__fish_use_subcommand' -a 'completion'   -d 'emit/install shell completion scripts'
complete -c yakos -n '__fish_use_subcommand' -a 'help'         -d 'print help'

# mcp subcommands.
complete -c yakos -n '__fish_seen_subcommand_from mcp' -a 'install'   -d 'add yakos-dispatch to .mcp.json'
complete -c yakos -n '__fish_seen_subcommand_from mcp' -a 'uninstall' -d 'remove yakos-dispatch from .mcp.json'
complete -c yakos -n '__fish_seen_subcommand_from mcp' -a 'status'    -d 'show entry presence'
complete -c yakos -n '__fish_seen_subcommand_from mcp' -a 'probe'     -d 'verify mcp python package'

# completion subcommands.
complete -c yakos -n '__fish_seen_subcommand_from completion' -a 'bash'    -d 'print bash completion script'
complete -c yakos -n '__fish_seen_subcommand_from completion' -a 'zsh'     -d 'print zsh completion script'
complete -c yakos -n '__fish_seen_subcommand_from completion' -a 'fish'    -d 'print fish completion script'
complete -c yakos -n '__fish_seen_subcommand_from completion' -a 'install' -d 'install for detected shell'

# git-hooks subcommands.
complete -c yakos -n '__fish_seen_subcommand_from git-hooks' -a 'install'   -d 'install pre-push version gate'
complete -c yakos -n '__fish_seen_subcommand_from git-hooks' -a 'uninstall' -d 'remove pre-push hook'
complete -c yakos -n '__fish_seen_subcommand_from git-hooks' -a 'status'    -d 'report gate installation state'

# Common flags for mcp install/uninstall/status.
complete -c yakos -n '__fish_seen_subcommand_from mcp' -l project -d 'project directory' -r
