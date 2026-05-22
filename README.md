# YakOS

A portable, multi-project agent framework for
[Claude Code](https://docs.claude.com/en/docs/claude-code/overview).

YakOS is a versioned framework you install once and use across multiple
projects. It bundles the agents, skills, rules, and hooks that turn
Claude Code's Agent Teams primitives into a reliable multi-agent
workflow — with the enforcement gaps filled in (path allowlists,
secret scanning, mailbox audit, session-end checks).

The framework is built around a [hard/soft control taxonomy](PHILOSOPHY.md):
soft controls (agent prompts, path-scoped rules, scratchpad conventions)
shape behavior; hard controls (PreToolUse hooks, TaskCompleted hooks,
git pre-push, CI gates) refuse broken work. Anything safety-critical
gets both.

## Status

**v0.11.0.0** — multi-runtime, design-pipeline complete,
`yakos validate --strict`: 0 errors / 0 warnings.

For a guided overview of what yakOS is, how it works, the
architecture, and the full agent + skill inventory, read
[**docs/overview.md**](docs/overview.md). The bullet list below
is the at-a-glance summary; the overview is the depth.

- **CLI**: ~20 subcommands — `install`, `update`, `uninstall`,
  `doctor` (`--probe-runtime`, `--fix`), `init` (`--with-gate`),
  `migrate`, `start` (`--runtime`, `--safe`, `--dry-run`,
  `--continue`, `--resume`, `--bare`, `--ide`), `dispatch`
  (cross-runtime), `auth`, `memory`, `cost`, `agent`/`agents`,
  `plugin`, `hooks`, `session`, `validate` (`--strict`),
  `version-bump`, `git-hooks`, `archive`, `status`, `team`, `teach`.
  Bare `yakos` autodetects the current project from cwd.
- **`yakos start <name>`** — single-command session launcher.
  Materializes agents in the runtime's native format
  (claude: `--agents` JSON injection; codex: TOML files;
  gemini: markdown frontmatter), exec's the chosen runtime CLI
  with the right flags. Audit trail at
  `~/.yakos-state/launch-log.ndjson`. Banner reminds the operator
  of `rule:lead-dispatch-discipline` at every launch.
- **3 runtime adapters** — claude, codex, gemini — plus a plugin
  model for community runtimes
  (see [docs/plugin-spec.md](docs/plugin-spec.md)).
- **33 framework agents** spanning orchestration (lead-template,
  planner, architect), code quality (code-reviewer,
  security-reviewer, test-runner, troubleshooter, doc-writer,
  maintainer), stack specialists (backend, frontend, mobile,
  database), operations (release-manager, incident-responder,
  sre, devops-engineer, performance-engineer), API + data
  (api-designer, data-engineer), security (supply-chain-auditor),
  AI/LLM (prompt-engineer, eval-engineer, ai-safety-reviewer,
  red-team, rag-architect, ai-finops), and design + UX + i18n
  (app-designer, ux-researcher, design-system-curator,
  accessibility-reviewer, content-strategist, i18n-specialist).
- **44 skills** for cadence-driven work — release management, agent
  audit, eval gating, prompt injection testing, postmortem
  authoring, ADR scaffolding, license/CVE/SBOM auditing, a11y
  scanning, perf budget enforcement, and more. Full inventory
  in [docs/overview.md](docs/overview.md).
- **5 always-loaded rules** — `lead-dispatch-discipline`
  (lead orchestrates / specialists do; parallel by default),
  `git-hygiene`, `commit-format`, `pr-conventions`, plus the
  path-scoped `secret-handling`.
- **8 reference Claude Code hooks** under `lib/hooks/` plus 5
  per-domain validators. Hook conversion to codex/gemini config
  via `yakos hooks install`.
- **1 git hook** — pre-push version gate with classification-aware
  enforcement and NDJSON audit trail at
  `~/.yakos-state/gate-log.ndjson`.
- **Lead is restricted by tool, not just convention**:
  `lead-template.md` does not include `Edit` (v0.5+). Code edits
  go through dispatched specialists. The four-line dispatch
  rule loads in every session via `rule:lead-dispatch-discipline`.
- **Per-runtime real telemetry** — claude (via stream-json),
  codex (`exec --json`), gemini (stream-json) emit token usage
  + cost into `dispatch-log.ndjson`. `yakos cost --by agent`
  rolls up.
- **Portable cross-runtime memory** — single source of truth at
  `~/.yakos-state/memory/<project>/`; per-runtime materialization
  via `yakos memory sync <runtime>`.
- **Posture**: audit-trail-first, human-in-the-loop by design.
  See [PHILOSOPHY.md](PHILOSOPHY.md).

See [CHANGELOG.md](CHANGELOG.md) for what landed in each release.

## Prerequisites

- macOS or Linux (bash 3.2+; `jq` 1.6+; `git` 2.20+; `claude` CLI 2.1.32+;
  `tmux` 3.0+).
- See [COMPATIBILITY.md](COMPATIBILITY.md) for the full matrix and
  optional tools.

## Install

```sh
git clone https://github.com/<you>/yakos.git ~/code/yakos
cd ~/code/yakos
./cli/yakos install
./cli/yakos doctor    # verify
```

`install` creates per-file symlinks under
`~/.claude/{agents,skills,rules,playbooks}/`, writes a pointer at
`~/.yakos`, and safely merges the experimental-agent-teams env var
into `~/.claude/settings.json`. It NEVER touches
`~/.claude/projects/` (your auto-memory).

If `~/.claude/settings.json` already exists, install validates the
JSON, writes a timestamped backup at
`~/.claude/settings.json.yakos-bak-<ISO8601>`, and merges only the
YakOS-owned `env` block — preserving all other keys (`hooks`,
`statusLine`, `model`, etc.).

## Bootstrap a project

```sh
./cli/yakos init <project-name> --project /path/to/your/project
```

Sets up `~/agent-control/<project-name>/` (the per-project ephemeral
work area; not in git), copies reference hooks into
`<project>/scripts/hooks/`, drops `<project>/.claude/{settings,
path-allowlist}.json` from templates if absent, and ensures the
auto-memory `MEMORY.md` index exists.

## Run a session

```sh
yakos start <project-name>
```

Or run bare `yakos` from inside `~/agent-control/<project-name>/` or
the project repo — it auto-detects which project you mean.

By default `yakos start` launches Claude Code. v0.4+ also supports
[OpenAI Codex](https://github.com/openai/codex) and
[Gemini CLI](https://github.com/google-gemini/gemini-cli) as
alternative runtimes:

```sh
yakos start myapp --runtime codex
yakos auth status                    # per-runtime cli + auth state
yakos auth login codex --as-default  # set codex as the default
```

**Mixed-runtime dispatch** (v0.4.2+): a project can declare per-agent
runtime preferences in agent frontmatter and let the lead dispatch
each specialist to its preferred CLI:

```sh
# Inside a session (or any shell):
yakos dispatch frontend "implement the login form"     # routes to agent's runtime:
yakos dispatch backend "..." --runtime codex           # override per call
```

v0.5+ adds **runtime fallback chains** via the `runtime-fallback:`
frontmatter field, **token-usage telemetry** in the dispatch audit
log, and `yakos hooks install <runtime>` to translate the
path-allowlist + secret-scan hooks for codex/gemini.

**Project-level config** (v0.7+): drop a `.yakos.yml` in the project
root to set defaults across many agents:

```yaml
default-runtime: claude
default-fallback: [codex]
per-domain:
  code-review: codex
  ui: gemini
model-aliases:
  cheap:
    claude: haiku
    codex: gpt-5-nano
```

Then agent frontmatter can reference `model: cheap` and yakOS resolves
it per-runtime at dispatch time.

**CI integration** (v0.7+): the reusable workflow at
[`.github/workflows/yakos-dispatch.yml`](.github/workflows/yakos-dispatch.yml)
runs a yakos agent in GitHub Actions — for security-reviewer-on-PR
or architect-sign-off-on-migrations gates. See
[docs/ci-integration.md](docs/ci-integration.md).

**Portable memory** (v0.5+): the lead's auto-memory and operator
notes live at `~/.yakos-state/memory/<project>/` as a single source
of truth, materialized into each runtime's native location on
launch. See [docs/memory-portability.md](docs/memory-portability.md).

```sh
yakos memory list <project>
yakos memory migrate-from-claude <project>   # one-shot import
yakos memory sync claude <project>           # mirror to ~/.claude/projects/...
```

See [docs/runtime-matrix.md](docs/runtime-matrix.md) for the
capability matrix and trade-offs.

`yakos start` composes the framework + project agents into the
`claude --agents` JSON (so project-level specialists become
addressable as `subagent_type` — works around
`incident:v0.2.0-project-agent-runtime-non-discovery`), passes
`--add-dir <repo>`, defaults to `--permission-mode bypassPermissions`,
and auto-detects `<project>/.mcp.json` for `--mcp-config`. Use
`--safe` to keep permission prompts on, `--dry-run` to preview the
exec'd command, `--continue` / `--resume <id>` / `--fork-session` /
`--ide` / `--bare` / `--model <alias>` as pass-throughs to claude.

The lead agent loads the framework's generic agents, the project's
specialists, the path-scoped rules that match files Claude reads, and
any project-specific skills.

## Common workflows

- **Adding a feature touching DB/API/UI** → see
  [COOKBOOK.md Pattern 1](COOKBOOK.md).
- **Investigating a bug** → see [COOKBOOK.md Pattern 3](COOKBOOK.md).
- **Releasing a version** → see [COOKBOOK.md Pattern 4](COOKBOOK.md).
- **Picking the right team** → see
  [docs/team-shapes.md](docs/team-shapes.md).
- **Customizing for your project** → see
  [CUSTOMIZING.md](CUSTOMIZING.md).

## Verify and operate

```sh
yakos doctor                       # environment + install health
yakos doctor /path/to/proj         # also checks hook drift + gate status
yakos doctor --probe-runtime       # report Claude Code runtime feature state
yakos validate                     # standards check on framework lib/
yakos validate /path/to/proj       # standards check on project's .claude/
yakos status <project>             # per-project dashboard
yakos archive <project> <tag>      # roll work/current/ → work/archive/<tag>/
yakos team restart <project>       # archive + relaunch instructions
```

## Releasing — version-bump + pre-push gate

YakOS uses four-part semver (`major.minor.patch.hotfix`) and ships a
pre-push gate that refuses substantive code changes without a matching
VERSION bump.

```sh
# Inside the project repo (run once):
yakos git-hooks install     # installs the pre-push gate

# Each release:
yakos version-bump --component {major|minor|patch|hotfix}
git push                    # gate verifies VERSION bump matches change scope
```

If `[Unreleased]` has substantive content, `version-bump` PROMOTES it
to a versioned header (rename) instead of inserting beside it. Bump
semantics are documented in [STYLE.md §8](STYLE.md). Gate audit trail
at `~/.yakos-state/gate-log.ndjson`. Override:
`YAKOS_GATE_DISABLE=1 git push` (logged).

## Update / uninstall

See [UPGRADING.md](UPGRADING.md) for the authoritative upgrade path
from any prior version, schema migration table, full-uninstall
procedure, and rollback steps.

Quick path:

```sh
cd ~/code/yakos && git pull
./cli/yakos update                                 # relink symlinks
./cli/yakos doctor                                 # verify
for cd in ~/agent-control/*/; do
    p="$(head -1 "$cd/.project-path" 2>/dev/null)"
    [ -d "$p" ] && ./cli/yakos doctor "$p" --fix && \
        ./cli/yakos migrate "$(basename "$cd")"
done
```

To uninstall: `./cli/yakos uninstall` removes yakOS-owned symlinks
and the `~/.yakos` pointer. Auto-memory at `~/.claude/projects/`,
`~/.yakos-state/`, and `~/agent-control/` are **never** touched
without explicit operator intervention.

## Engineering standards

YakOS code follows a defined standard. See:

- [STYLE.md](STYLE.md) — quick reference
- [docs/engineering-standards.md](docs/engineering-standards.md) —
  explanatory guide with examples
- [tests/README.md](tests/README.md) — test layout and fixture naming

Standards are enforced lightly via `yakos validate` (WARN-only in v0.1;
v0.2 may promote some checks to errors).

## Documentation map

User-facing:

- [PHILOSOPHY.md](PHILOSOPHY.md) — hard/soft control taxonomy, trust-
  but-verify, orchestration shapes
- [STYLE.md](STYLE.md) — engineering standards (the law)
- [CUSTOMIZING.md](CUSTOMIZING.md) — adding project agents / hooks /
  rules / skills (one worked example each)
- [MIGRATING.md](MIGRATING.md) — moving from a tmux + dispatch-CLI
  setup
- [COOKBOOK.md](COOKBOOK.md) — common workflow recipes
- [docs/team-shapes.md](docs/team-shapes.md) — recommended team
  compositions per project type
- [INCIDENT-CATALOG.md](INCIDENT-CATALOG.md) — durable incident
  records other artifacts reference
- [COMPATIBILITY.md](COMPATIBILITY.md) — supported environments

Architecture and history:

- [docs/architecture/phase-1.5-architecture.md](docs/architecture/phase-1.5-architecture.md) — the spec
- [docs/architecture/phase-0-validation-results.md](docs/architecture/phase-0-validation-results.md) — Agent Teams primitives validated
- [docs/architecture/phase-1.7-results.md](docs/architecture/phase-1.7-results.md) — SendMessage hookability validated
- [docs/architecture/phase-2-execution-plan.md](docs/architecture/phase-2-execution-plan.md) — v0.1 build sequence
- [CHANGELOG.md](CHANGELOG.md)

Engineering:

- [docs/engineering-standards.md](docs/engineering-standards.md) —
  worked examples for STYLE.md
- [lib/hooks/README.md](lib/hooks/README.md) — hook authoring contract
- [tests/README.md](tests/README.md) — test layout

## Not in v0.3.x

Tracked roadmap items, gated for clear reasons:

- **The log-analyst, devops-infra, performance-engineer, privacy-
  reviewer, accessibility-reviewer, and ux-reviewer agents.** v0.3
  added architect, incident-responder, and release-manager from the
  cross-cutting roster (highest demand, lowest design risk). The
  remaining six wait for concrete demand from real use per
  [docs/team-shapes.md](docs/team-shapes.md).
- **`task-dependency-gate.sh` and `task-complete-dispatch.sh` BLOCKING
  upgrade.** Was scheduled for v0.2; gated on a Claude Code runtime
  feature (TaskCreate/TaskUpdate not currently exposed — see
  [`docs/architecture/phase-0.5-results.md`](docs/architecture/phase-0.5-results.md)
  and `incident:v0.2.1-task-tools-not-exposed`).
- **Hashed-edit runtime PreToolUse hook.** Helper scripts ship in
  `lib/skills/hashed-edit/`; the auto-enforcement hook needs design
  pass on stateless vs stateful staleness check.
- **`yakos iterate-until` CLI subcommand.** Procedural skill ships at
  `lib/skills/iterate-until/`; lift into a CLI once the procedural
  shape settles via real usage.
- **Multi-model category routing.** Design-only for v0.3+; current
  `model: opus|sonnet|haiku` agent-frontmatter primitive is the seed.
- **Composable middleware-style hooks.** Refactor of the existing 8
  hooks into a stackable middleware pipeline. Defer until next hook
  addition forces the issue.
- **Skill-embedded MCPs.** Requires MCP infrastructure yakOS doesn't
  have yet.
- **npm packaging + i18n + multi-language docs.** Strategic decisions
  gated on going-public. Currently solo-multiplier-shaped.
- **Per-session state store.** Would unlock the stateful hashed-edit
  hook and other features. Design pass needed (file-based JSON,
  SQLite, per-process?).

## License

Apache License 2.0. See [LICENSE](LICENSE) for the full text.

Copyright 2026 bakw00ds.

Source files carry an `SPDX-License-Identifier: Apache-2.0` marker for
machine-readable license detection. The Apache 2.0 patent grant (§3)
and defensive-termination clause apply automatically to all
contributions submitted under [§5](LICENSE).
