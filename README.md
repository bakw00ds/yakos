# yakOS

Multi-agent operating discipline for Claude Code (and codex/agy): a CLI that
ships a roster of 35 specialist agents, audit-first hooks, kanban +
retrospectives, and per-project audit trails across runtimes.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.35.0.0-orange.svg)](CHANGELOG.md)
[![Stability: alpha](https://img.shields.io/badge/stability-alpha-red.svg)](#status)

> Not affiliated with Anthropic, OpenAI, or Google.

## Quickstart

Prerequisite: the `claude` CLI installed and authenticated
([docs.claude.com](https://docs.claude.com/en/docs/claude-code/overview)).

```sh
git clone https://github.com/bakw00ds/yakos.git ~/code/yakos
cd ~/code/your-project
~/code/yakos/cli/yakos quickstart
```

`quickstart` detects state and runs only what's needed: install if missing →
init if the cwd is a git repo → start a session. After install, `yakos` is on
your PATH at `~/.local/bin/yakos`.

Or step by step:

```sh
~/code/yakos/cli/yakos install          # once; adds ~/.local/bin/yakos
yakos init myapp --project ~/code/myapp # bootstrap a project
yakos start myapp                       # launch a session
```

## Common commands

| Command | What it does |
|---|---|
| `yakos quickstart` | Install + init + start (idempotent, one command) |
| `yakos start <name>` | Launch a session for a bootstrapped project |
| `yakos dispatch <agent> "<task>"` | One-shot dispatch to any specialist |
| `yakos kanban` | Render the WIP board; `serve` opens a web UI |
| `yakos supervise enable` | Turn on the live shadow-agent supervisor |
| `yakos doctor` | Environment + install health check |
| `yakos update` | `git pull` framework + refresh symlinks |
| `yakos refresh` | Detect and repair per-project deployment drift (hooks + settings.json + agent symlinks) |
| `yakos uninstall` | Remove yakOS-owned symlinks (never touches your memory) |
| `yakos skill plan-quality-eval <plan.md>` | Score a plan against the 6-dimension rubric |
| `yakos plan score show\|history\|override` | Surface plan-quality-eval log records |
| `yakos model-routing eval\|list\|show` | Evaluate an agent's golden-set across haiku/sonnet/opus; surface routing candidates |

Full list: `yakos --help` (32 subcommands).

## What it does

- **Parallel-by-default dispatch with worktree isolation.** The lead
  decomposes tasks and dispatches specialists in parallel; each gets its own
  git worktree so concurrent edits never collide.
- **Lead-as-orchestrator discipline.** The lead reads, plans, dispatches, and
  integrates — it does not write code. Enforced via tool-list restrictions in
  the lead-template agent.
- **Hooks as quality gates.** `path-allowlist`, `secret-scan`, and
  `supervisor-stream/gate` run at `PreToolUse`; the budget-guard and
  output-injection-scan hooks run post-tool. Hard controls; they refuse, not
  warn.
- **10-cycle librarian retrospectives.** Every 10 prompts the librarian agent
  reads the transcript, surfaces lessons and drift, and proposes soul edits.
  Operator-gated; nothing promotes automatically.
- **Cross-runtime portability.** The same workflow runs on Claude Code, codex,
  and agy via runtime adapter shims. `yakos dispatch <agent> "<task>"
  --runtime codex` is the escape hatch.

## Project layout

```
yakos/
  cli/              yakos CLI entry point + subcommand scripts
  lib/
    agents/         35 framework specialist agents (lead-template, planner, …)
    hooks/          17 hook scripts (path-allowlist, secret-scan, supervisor, …)
    rules/          16 always-loaded + path-scoped behavioral rules
    skills/         57 skills (plan-quality-eval, version-bump, …)
  tests/            e2e + fixture suites
```

## Where to look next

- [CLAUDE.md](CLAUDE.md) — project-level agent instructions and conventions
- [lib/rules/INDEX.md](lib/rules/INDEX.md) — all cross-cutting rules
- [lib/agents/README.md](lib/agents/README.md) — full agent roster and frontmatter schema
- [docs/overview.md](docs/overview.md) — guided architecture walkthrough

## Status

**v0.35.0.0** — alpha, pre-1.0. CLI commands and `.yakos.yml` schema are
stable within minor versions. See [CHANGELOG.md](CHANGELOG.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
