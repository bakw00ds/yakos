# yakOS

Multi-agent operating discipline for Claude Code (and codex/agy): a CLI that
ships a roster of specialist agents, audit-first hooks, kanban +
retrospectives, and per-project audit trails across runtimes.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-0.40.0.0-orange.svg)](CHANGELOG.md)
[![Stability: alpha](https://img.shields.io/badge/stability-alpha-red.svg)](#status)

> Not affiliated with Anthropic, OpenAI, or Google.

## Quickstart

Prerequisite: the `claude` CLI installed and authenticated
([docs.claude.com](https://docs.claude.com/en/docs/claude-code/overview)).

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

The installer downloads the Go binary for your platform, verifies the SHA256
checksum, runs `yakos install` (materializes the embedded framework lib and
wires `~/.claude` symlinks), and persists `export YAKOS_IMPL=go` to your shell
profile. Open a new terminal (or `source` the profile it printed), then:

```sh
yakos doctor                            # verify the install
yakos init myapp --project ~/code/myapp # bootstrap a project
yakos start myapp                       # launch a session
```

No repo clone required. See [docs/getting-started.md](docs/getting-started.md)
for the full install guide, including the dev/from-source path.

## Common commands

| Command | What it does |
|---|---|
| `yakos quickstart` | Install + init + start (idempotent, one command) |
| `yakos start <name>` | Launch a session for a bootstrapped project |
| `yakos dispatch <agent> "<task>"` | One-shot dispatch to any specialist |
| `yakos serve` | Start the daemon + open the unified console at `http://127.0.0.1:7890` |
| `yakos workflow run <name>` | Run a Flows DAG workflow headlessly |
| `yakos kanban` | Render the WIP board; `serve` opens a web UI |
| `yakos supervise enable` | Turn on the live shadow-agent supervisor |
| `yakos doctor` | Environment + install health check |
| `yakos update` | Pull framework updates + refresh symlinks |
| `yakos refresh` | Detect and repair per-project deployment drift |
| `yakos uninstall` | Remove yakOS-owned symlinks (never touches your memory) |
| `yakos metrics collect\|report\|trend\|compare\|gate\|serve\|install-hook\|uninstall-hook` | Project-health metrics time series |
| `yakos skill plan-quality-eval <plan.md>` | Score a plan against the 6-dimension rubric |
| `yakos model-routing eval\|list\|show` | Evaluate an agent's golden-set across model tiers |

Full list: `yakos --help` (41 subcommands ported to Go).

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
- **Project-health metrics.** `yakos metrics` tracks efficiency, DORA, and
  per-language quality indicators across commits. CI gate via
  `budgets.yaml`; loopback dashboard via `yakos metrics serve`.
- **Unified web console.** `yakos serve` opens a single loopback console at
  `http://127.0.0.1:7890` behind one bearer token. Tabs: Overview (live
  activity feed + operator presence), Chat (per-model REPL panes across
  claude/codex/agy/gemini × model tiers; claude streams token-by-token),
  Kanban, Cost, and Performance — plus the Flows workflow builder. See
  [docs/unified-console.md](docs/unified-console.md).
- **Flows DAG orchestration.** Define multi-agent workflows as YAML files;
  the headless DAG engine runs them with Kahn topological scheduling,
  fan-out/fan-in, resume-from-failure, and per-run cost tracking. Author
  in the console Flows tab or run headlessly with `yakos workflow run <name>`.
- **Model-tier routing with fable.** Four tiers: `haiku < sonnet < opus < fable`.
  `fable` is the top tier above `opus`; shipped framework agents top out at
  `opus` and must opt in to `fable` explicitly via agent frontmatter. `frontier`
  is an accepted alias. Override per-dispatch with `--model fable`.
- **Self-contained binary.** The Go binary embeds the full framework `lib/`
  via `//go:embed`. A `curl|sh` install is fully self-sufficient with no repo
  clone.
- **Cross-runtime portability.** The same workflow runs on Claude Code, codex,
  and agy via runtime adapter shims. `yakos dispatch <agent> "<task>"
  --runtime codex` is the escape hatch.

## Project layout

```
yakos/
  cli-go/           Go binary source (41 ported subcommands)
  cli/              bash CLI fallback + adapter scripts
  lib/
    agents/         framework specialist agents (lead-template, planner, …)
    hooks/          hook scripts (path-allowlist, secret-scan, supervisor, …)
    rules/          always-loaded + path-scoped behavioral rules
    skills/         skills (plan-quality-eval, version-bump, …)
  docs/             operator guides (getting-started, metrics-ci, overview, …)
  tests/            e2e + fixture suites
```

## Where to look next

- [docs/getting-started.md](docs/getting-started.md) — install, bootstrap, first session
- [docs/unified-console.md](docs/unified-console.md) — unified console guide (Chat REPLs, Flows orchestration)
- [docs/overview.md](docs/overview.md) — guided architecture walkthrough
- [docs/metrics-ci.md](docs/metrics-ci.md) — running `yakos metrics` in CI
- [lib/rules/INDEX.md](lib/rules/INDEX.md) — all cross-cutting rules
- [lib/agents/README.md](lib/agents/README.md) — full agent roster and frontmatter schema
- [CHANGELOG.md](CHANGELOG.md) — complete history

## Status

**v0.40.0.0** — alpha, pre-1.0. CLI commands and `.yakos.yml` schema are
stable within minor versions. See [CHANGELOG.md](CHANGELOG.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
