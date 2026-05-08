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

**v0.3.0** — session-launch UX, runtime-bound project agents. Ships:

- **CLI**: install / uninstall / doctor (with `--probe-runtime`) / init
  (with `--with-gate`) / **start** (NEW) / validate / archive / status /
  team / update / version-bump / git-hooks. Bare `yakos` autodetects
  the current project from `cwd`.
- **`yakos start <name>`** — single-command session launcher. Composes
  `--agents` JSON from framework + project agents, exec's `claude
  --add-dir <repo> --permission-mode bypassPermissions --agents <json>`.
  Auto-detects `<project>/.mcp.json` for `--mcp-config`. Pass-throughs
  for `--continue` / `--resume` / `--fork-session` / `--ide` / `--bare` /
  `--strict-mcp` / `--model`. Audit trail at
  `~/.yakos-state/launch-log.ndjson`.
- **8 reference Claude Code hooks** under `lib/hooks/` (path-allowlist,
  secret-scan, mailbox-mirror, path-log, team-lifecycle, session-end-check
  with team-inbox snapshot, task-dependency-gate, task-complete-dispatch).
  5 per-domain validators (backend / frontend / mobile / db / changelog).
- **1 git hook**: pre-push version gate (`lib/hooks/git/`) with
  classification-aware enforcement and NDJSON audit trail at
  `~/.yakos-state/gate-log.ndjson`.
- **15 generic agents** under the 80–140 line budget. Cross-cutting
  roles (lead-template, planner, code-reviewer, security-reviewer,
  test-runner, troubleshooter, doc-writer, maintainer, **architect**,
  **incident-responder**, **release-manager**) + stack-specialist
  templates with `extends:` deployment (backend, frontend, mobile,
  database).
- **16 skills** under `lib/skills/` including: contract-handoff,
  dispatch-as-project-agent (still useful for ad-hoc dispatch even
  with `--agents` injection in place), hashed-edit (OMA-inspired
  stale-line catch), iterate-until (formal "loop until human-checkable
  verifier passes"), version-bump (4-part semver with `[Unreleased]`
  promote-on-bump), release-audit/ scaffolding.
- **Posture made explicit**: PHILOSOPHY.md "Human-in-the-loop by
  design" section. yakOS optimizes for production-touching work in
  audit-sensitive domains, not autonomous-first prototyping.
- **Runtime findings**: project-level `.claude/agents/*.md` files are
  STILL not natively runtime-discoverable as `subagent_type` in claude
  2.1.136 (`incident:v0.2.0`). v0.3 closes this for normal operation
  via `yakos start`'s `--agents` JSON injection. TaskCreate / TaskList /
  TaskUpdate also not exposed in current Claude Code build
  (`incident:v0.2.1-task-tools-not-exposed`).

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

## Update

```sh
cd ~/code/yakos
git pull
./cli/yakos update    # git pull + relink + change report
```

## Uninstall

```sh
./cli/yakos uninstall
```

Removes only the YakOS-owned symlinks and the `~/.yakos` pointer.
Files YakOS didn't create are left in place. Auto-memory at
`~/.claude/projects/` is **never** touched, no matter what flags you
pass — this rule supersedes every other consideration in v0.1.

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

TBD.
