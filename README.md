# yakOS

**Multi-runtime agent framework for [Claude Code](https://docs.claude.com/en/docs/claude-code/overview),
[OpenAI Codex](https://github.com/openai/codex), and
[Google Antigravity](https://antigravity.google) — with hard / soft controls,
audit-first hooks, and multi-developer coordination.**

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.32.0.0-orange.svg)](CHANGELOG.md)
[![Stability: alpha](https://img.shields.io/badge/stability-alpha-red.svg)](#status)

yakOS turns the agent primitives that ship in these CLIs (sub-agent
dispatch, hooks, settings, MCP) into a reliable multi-agent workflow
across many projects. It bundles 34 framework agents, 53 skills, 16
rules, 8 playbooks, and 12 reference hooks behind a single CLI; the
same workflow runs on every supported runtime.

The framework is built on a **hard / soft control taxonomy**
([PHILOSOPHY.md](PHILOSOPHY.md)): soft controls (agent prompts,
path-scoped rules, scratchpad conventions) shape behavior; hard
controls (PreToolUse hooks, git pre-push gates, CI checks) refuse
broken work. Anything safety-critical gets both.

> **Not affiliated with Anthropic, OpenAI, or Google.** yakOS uses
> these companies' agent CLIs as runtimes; the framework is independent.

## TL;DR — get running in one command

```sh
git clone https://github.com/bakw00ds/yakos.git ~/code/yakos
cd ~/code/your-project && ~/code/yakos/cli/yakos quickstart
```

`quickstart` (v0.30+) detects what's been done and runs only what's
needed: install yakOS if missing → init the project if cwd is a git
repo → start a session. Idempotent; safe to re-run.

After install, you can drop the path and just type `yakos`.

### Common scenarios

| Goal | One command |
|---|---|
| First-time install + run against a git repo | `yakos quickstart` |
| Bootstrap then start a project explicitly | `yakos init myapp --project ~/code/myapp && yakos start myapp` |
| Auto-detect project from cwd | `yakos` (bare) |
| Auth into every installed runtime in one pass | `yakos auth login --all` |
| Run with a non-default runtime | `yakos start myapp --runtime codex` |
| Two developers on the same dev box | `yakos init --multi-dev myapp --project /srv/code/myapp` |
| See what peer sessions are doing | `yakos peer status` / `yakos peer log` |
| Cross-runtime dispatch (shell) | `yakos dispatch <agent> "<task>" --runtime agy` |
| Cross-runtime dispatch (native, from Claude) | `yakos mcp install` once, then call `dispatch_codex(...)` etc. as tools |
| Multi-turn cross-runtime | `continue_codex(conversation_id=..., task=...)` via MCP |
| Update everything | `yakos update --all` (framework + every per-project state) |
| Tab completion (bash/zsh) | `yakos completion install` |
| Health check | `yakos doctor` |

Full multi-developer / shared-dev-box guide:
[docs/co-pilot-mode.md](docs/co-pilot-mode.md). Cross-runtime MCP
guide: [docs/mcp-integration.md](docs/mcp-integration.md).

## Architecture

yakOS is a **layered framework**. Each layer has a single responsibility
and a well-defined boundary with its neighbors.

```
┌────────────────────────────────────────────────────────────────────┐
│                  Layer 1 — Framework (this repo)                   │
│  Versioned, install-once, shared across all projects.              │
│  - 34 agents (lead-template, planner, code-reviewer, ...)          │
│  - 53 skills (release-audit, eval-gate, postmortem, ...)           │
│  - 16 rules (lead-dispatch, git-hygiene, ...)                      │
│  - 8 playbooks (security, ui-ux-a11y, regulated-data, ...)         │
│  - 12 hooks (path-allowlist, secret-scan, peer-claim, ...)         │
│  - 6 runtime adapters (claude / codex / agy / claude-sdk /         │
│    antigravity-sdk / gemini-deprecated-shim)                       │
└────────────────────────────────────────────────────────────────────┘
                              │ install
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│                Layer 2 — User state (~/.claude, ~/.yakos-state)    │
│  Symlinks into the framework + persistent operator-private state.  │
│  - ~/.claude/{agents,skills,rules,playbooks}/ → framework files    │
│  - ~/.yakos-state/memory/<project>/ → canonical memory             │
│  - ~/.yakos-state/{launch,dispatch,gate}-log.ndjson → audit trail  │
└────────────────────────────────────────────────────────────────────┘
                              │ init <name> --project <path>
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│              Layer 3 — Project config (<repo>/.claude, .yakos.yml) │
│  Per-project agents, hooks, allowlists. Committed to the repo.     │
│  - <repo>/.claude/{settings.json, path-allowlist.json}             │
│  - <repo>/.claude/agents/   ← project specialists                  │
│  - <repo>/scripts/hooks/    ← copies of framework hooks            │
│  - <repo>/.yakos.yml        ← per-project defaults                 │
└────────────────────────────────────────────────────────────────────┘
                              │ yakos start
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│        Layer 4 — Per-project ephemeral (~/agent-control/<name>/)   │
│  Scratchpad + audit logs for one project's sessions. Not in git.   │
│  - work/current/{plan,contracts,decisions,status,findings}.md      │
│  - work/current/logs/<hook>.ndjson                                 │
│  - work/current/messages.ndjson, hook-bypass.md                    │
│  - work/archive/<tag>/   ← yakos archive promotes here             │
└────────────────────────────────────────────────────────────────────┘
```

**Optional Layer 0** for multi-developer coordination:
`/var/lib/yakos/<project>/coord/` — see
[docs/co-pilot-mode.md](docs/co-pilot-mode.md).

## Key concepts

| Concept | Lives in | Loads when | Purpose |
|---|---|---|---|
| **Agent** | `lib/agents/*.md` | sub-agent dispatched, OR session start (lead) | a role + persona + tool restrictions |
| **Skill** | `lib/skills/*/SKILL.md` | invoked by name | a procedure with automated + manual passes |
| **Rule** | `lib/rules/*.md` | always-loaded OR when a matching file is read | a constraint the lead must respect |
| **Playbook** | `lib/playbooks/*.md` | referenced by an agent / skill / rule | a checklist for a domain (security, a11y, etc) |
| **Hook** | `lib/hooks/*.sh` | wired in `settings.json` | hard-control gate at PreToolUse / PostToolUse / etc |
| **Runtime adapter** | `cli/lib/runtimes/<id>.sh` | `yakos start --runtime <id>` | translates yakOS abstractions to a CLI's native format |

## Runtime support

| Runtime | Adapter | Status | Notes |
|---|---|---|---|
| Claude Code | `claude` | full | reference target; interactive + headless |
| OpenAI Codex | `codex` | full | hooks via `permissions.<name>` translation |
| Claude Agent SDK | `claude-sdk` | full | headless; sub-agents via `AgentDefinition`; native cost telemetry |
| Google Antigravity | `agy` | full | bundled OAuth; replaces Gemini CLI |
| Antigravity SDK | `antigravity-sdk` | full | headless; sub-agents via `enable_subagents`; `total_usage` telemetry |
| Gemini CLI | `gemini` | **deprecated** | 49-line shim → `agy`; removal date 2026-09-01 |

Cross-runtime dispatch via `yakos dispatch <agent> "<task>" --runtime <id>`.
See [docs/runtime-matrix.md](docs/runtime-matrix.md) for the capability
matrix.

## Multi-developer co-pilot mode

Two developers can share a dev box and have their yakOS sessions
coordinate via per-file claims + a synchronous mode-negotiation
protocol. Optional; vanilla yakOS works unchanged when not enabled.

```sh
# One-time admin setup (sudo)
sudo groupadd -f yakos-coord && sudo usermod -a -G yakos-coord alice bob
sudo mkdir -p /var/lib/yakos && sudo chgrp yakos-coord /var/lib/yakos
sudo chmod 2775 /var/lib/yakos       # setgid: new files inherit group

# Each developer
yakos init --multi-dev myapp --project /srv/code/myapp
yakos peer status                    # see active peer sessions
yakos peer log --since 2026-05-22T15:00:00Z
yakos peer claims                    # see active per-file claims
```

Full guide: [docs/co-pilot-mode.md](docs/co-pilot-mode.md).

## Cross-runtime dispatch from a Claude session (MCP)

A running Claude Code session can call codex / agy / SDK agents as
**native MCP tool calls** instead of shelling out via Bash. Multi-turn
conversations supported for codex / agy / claude / claude-sdk
(antigravity-sdk lacks cross-process resume).

```sh
pip install mcp                                # one-time, Anthropic's MCP Python SDK
cd ~/code/myapp
yakos mcp install                              # writes .mcp.json in cwd
# restart your Claude Code session in this project
```

Tools the lead sees in the session:

```
dispatch_codex(agent, task)                    # one-shot
continue_codex(conversation_id, task)          # multi-turn

dispatch_agy(...) / continue_agy(...)
dispatch_claude_sdk(...) / continue_claude_sdk(...)
dispatch_claude(...) / continue_claude(...)    # peer claude session
dispatch_antigravity_sdk(...)                  # one-shot only
```

Each `dispatch_*` returns response text + a yakOS `conversation_id` +
telemetry (duration, token usage). Pass that id to the matching
`continue_*` to keep the agent's context alive across turns. State at
`~/.yakos-state/mcp-conversations.json`.

Full guide: [docs/mcp-integration.md](docs/mcp-integration.md).

## Tab completion

Bash and zsh completion ship in the box. Install with:

```sh
yakos completion install      # auto-detects shell + writes to conventional path
```

Or emit the script to a custom location:

```sh
yakos completion bash > ~/.local/share/bash-completion/completions/yakos
yakos completion zsh  > ~/.zsh/completions/_yakos
```

Tab completion covers: top-level subcommands, nested subcommands
(`peer status`, `mcp install`, `auth login`, `memory show`, ...),
dynamic **project name completion** from `~/agent-control/*/`, and
**runtime name completion** for `--runtime` / `auth` args.

## Common commands

```sh
# Bootstrap + launch
yakos quickstart                     # install + init + start (idempotent)
yakos                                # bare — auto-detect from cwd → start
yakos start <name>                   # explicit launch
yakos init <name> --project <path>   # bootstrap (add --multi-dev for co-pilot mode)

# Health + update
yakos doctor                         # environment + install health
yakos validate --strict              # standards check (0 errors expected)
yakos update                         # framework: git pull + relink
yakos update --all                   # framework + every per-project state

# Auth
yakos auth login --all               # log into every installed runtime
yakos auth status                    # unified view across all runtimes

# Dispatch (cross-runtime)
yakos dispatch <agent> "<task>"      # one-shot, picks runtime from frontmatter
yakos dispatch <agent> "<task>" --runtime codex
yakos mcp install                    # then call dispatch_* / continue_* as MCP tools

# Multi-dev co-pilot
yakos peer status                    # active peer sessions
yakos peer log [--since <iso>]       # tail shared activity stream
yakos peer claims                    # active per-file claims
yakos peer propose-mode --mode serialize --targets 'src/auth/**' --reason '...'

# Observability + memory
yakos cost --by agent                # aggregate token/cost telemetry
yakos memory list <name>             # project memory (shared symlink → coord if multi-dev)
yakos archive <name> <tag>           # promote work/current/ → work/archive/<tag>/

# Standards + completion
yakos standards list                 # cross-project standards opt-ins
yakos completion install             # shell tab completion

# Release flow
yakos version-bump --component minor # bump VERSION + CHANGELOG entry
yakos git-hooks install              # pre-push version gate in current repo
```

Full subcommand list: `yakos --help`. There are 28 subcommands.

## Cross-project standards

yakOS ships opt-in standards that any project can adopt via
`yakos standards enable <name>`:

- **logging** — structured logging discipline
- **changelog-ui** — user-visible changelog rendering
- **feedback** — customer-feedback citation linking
- **about-page** — project metadata page
- **architecture-viz** — auto-rendered architecture diagrams
- **monitors** — uptime + cost monitor scaffolds
- **retrospectives** — 10-cycle retrospective cadence

`yakos standards check` verifies a project against its opted-in
standards. See [docs/cross-project-standards.md](docs/cross-project-standards.md).

## Prerequisites

- macOS or Linux
- `bash` 3.2+, `jq` 1.6+, `git` 2.20+
- At least one runtime CLI: `claude` 2.1.32+, `codex` 0.20+, or `agy` 1.0.1+
- Optional: `python3` 3.10+ (for `claude-sdk` / `antigravity-sdk`),
  `tmux` 3.0+, `gtimeout`, `shellcheck`

Full matrix: [COMPATIBILITY.md](COMPATIBILITY.md).

## Status

**v0.32.0.0** — alpha, pre-1.0. Active development. API stability:
CLI commands and `.yakos.yml` schema are stable within minor versions;
hook contract and agent frontmatter are stable within major versions.
SemVer is four-part: `MAJOR.MINOR.PATCH.HOTFIX`. See
[CHANGELOG.md](CHANGELOG.md) for what landed in each release.

`yakos validate --strict`: **0 errors / 0 warnings**. Multi-dev
end-to-end test: **10/10 passing** under real concurrent processes.

### Recent landings since v0.29 (when the repo went public)

- **v0.30** — `yakos quickstart`, `yakos update --all`, multi-dev e2e
  test (real concurrent processes, 10/10), live runtime smoke test
  (conditional SDK exec), SECURITY.md, gemini.sh hard cutoff on
  2026-09-01, 32 tags backfilled (v0.3.0.0 → v0.28.0.0)
- **v0.31** — MCP server with 9 cross-runtime tools (5 dispatch + 4
  continue), multi-turn resume substrate (YAKOS_CONVERSATION_ID
  env-var convention threaded through codex / agy / claude /
  claude-sdk adapters), `yakos auth login --all`, `yakos mcp`
  subcommand, docs/mcp-integration.md operator guide
- **v0.32** — bash + zsh tab completion (with dynamic project name
  discovery), `yakos completion {bash,zsh,install}` subcommand,
  README refresh

## Documentation map

**Start here:**

- [docs/overview.md](docs/overview.md) — guided architecture + agent + skill inventory
- [docs/adopting.md](docs/adopting.md) — adopting yakOS in an existing project
- [PHILOSOPHY.md](PHILOSOPHY.md) — hard/soft taxonomy + audit-first posture
- [COOKBOOK.md](COOKBOOK.md) — workflow recipes (add feature, investigate bug, ship release)

**Multi-runtime:**

- [docs/runtime-matrix.md](docs/runtime-matrix.md) — per-runtime capabilities
- [docs/mcp-integration.md](docs/mcp-integration.md) — cross-runtime tools inside a Claude Code session
- [docs/memory-portability.md](docs/memory-portability.md) — cross-runtime memory
- [docs/plugin-spec.md](docs/plugin-spec.md) — write your own runtime adapter

**Operations:**

- [docs/co-pilot-mode.md](docs/co-pilot-mode.md) — multi-dev coord on a shared box
- [docs/cross-project-standards.md](docs/cross-project-standards.md) — opt-in standards
- [docs/ci-integration.md](docs/ci-integration.md) — yakOS in GitHub Actions
- [docs/team-shapes.md](docs/team-shapes.md) — recommended team compositions
- [SECURITY.md](SECURITY.md) — responsible disclosure policy

**Reference:**

- [STYLE.md](STYLE.md) — engineering standards
- [CUSTOMIZING.md](CUSTOMIZING.md) — add project agents / hooks / rules / skills
- [UPGRADING.md](UPGRADING.md) — version-to-version migration
- [INCIDENT-CATALOG.md](INCIDENT-CATALOG.md) — durable incident records
- [docs/architecture/](docs/architecture/) — phase-by-phase design history

## Development

Contributions welcome. Follow the conventions in [STYLE.md](STYLE.md)
and [docs/engineering-standards.md](docs/engineering-standards.md); the
commit format is [Conventional Commits](https://www.conventionalcommits.org/)
per [lib/rules/commit-format.md](lib/rules/commit-format.md).

Before opening a PR:

```sh
./cli/yakos validate --strict        # must report 0 errors
./tests/run-hook-fixtures.sh         # hook regression tests
./tests/run-runtime-fixtures.sh      # runtime adapter tests
```

Security disclosure: email the maintainer privately rather than
filing a public issue. A formal `SECURITY.md` will land in a later
release.

## FAQ

**Why "yakOS"?** Yak-shaving as a feature: most agent workflows
require coordinating many small ceremonies (settings, hooks,
allowlists, audit logs). yakOS owns the yak-shaving so you don't
have to do it per-project.

**How does this differ from LangChain / AutoGen / CrewAI?** Those
are libraries you write Python against. yakOS is a framework around
the agent CLIs themselves — no library, no Python required for the
common case. Your code stays your code; yakOS is the operating
discipline around it.

**Do I need to use Claude Code?** No. Codex, agy, or either SDK works
too. The runtime adapter layer is the abstraction; pick whichever
you have access to.

**What does "Multi-dev co-pilot mode" actually require?** A single
shared machine (physical or VM) that both developers SSH into. Not
cross-machine; that's explicitly out of scope. See
[docs/co-pilot-mode.md](docs/co-pilot-mode.md) for the topology.

**Is this production-ready?** It's alpha (v0.x). The framework works
and is used daily by the author, but APIs may change before 1.0.
Pin a version in your project's `.yakos.yml` to avoid surprises.

## License

Apache License 2.0. See [LICENSE](LICENSE) for the full text.

Copyright 2026 bakw00ds.

Source files carry an `SPDX-License-Identifier: Apache-2.0` marker for
machine-readable license detection. The Apache 2.0 patent grant (§3)
and defensive-termination clause apply automatically to all
contributions submitted under §5.
