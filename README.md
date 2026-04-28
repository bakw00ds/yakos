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

**v0.1.0** — initial release. Ships:

- CLI for install / uninstall / doctor / init / validate / archive /
  status / team-restart / update.
- 8 reference hooks under `lib/hooks/` (2 BLOCKING, 4 LOG/AUDIT, 2
  REPORT-only with documented UNCLEAR status).
- 5 per-domain validators (backend / frontend / mobile / db /
  changelog).
- 7 generic agents + 11 skills + 4 cross-cutting rules. All under
  the line budgets in [STYLE.md](STYLE.md).
- Engineering standards documentation: [STYLE.md](STYLE.md),
  [docs/engineering-standards.md](docs/engineering-standards.md),
  [tests/README.md](tests/README.md).
- Documentation: this README, [PHILOSOPHY.md](PHILOSOPHY.md),
  [CUSTOMIZING.md](CUSTOMIZING.md), [MIGRATING.md](MIGRATING.md),
  [COOKBOOK.md](COOKBOOK.md), [docs/team-shapes.md](docs/team-shapes.md),
  [INCIDENT-CATALOG.md](INCIDENT-CATALOG.md),
  [COMPATIBILITY.md](COMPATIBILITY.md).

See [CHANGELOG.md](CHANGELOG.md) for what landed in each batch.

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
cd ~/agent-control/<project-name>
claude --add-dir /path/to/your/project
```

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
yakos doctor                 # environment + install health
yakos doctor /path/to/proj   # also checks for hook drift
yakos validate               # standards check on framework lib/
yakos validate /path/to/proj # standards check on project's .claude/
yakos status <project>       # per-project dashboard
yakos archive <project> <tag>  # roll work/current/ → work/archive/<tag>/
yakos team restart <project>   # archive + relaunch instructions
```

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

## Not in v0.1

- **`lib/playbooks/` is empty.** Phase 1.5 §4 lists 6 playbooks (security,
  code-quality, UI/UX/a11y, docs, performance, HIPAA/PHI); v0.2
  populates them.
- **The architect, incident-responder, log-analyst, devops-infra,
  performance-engineer, privacy-reviewer, accessibility-reviewer, and
  ux-reviewer agents.** Roadmap in
  [docs/team-shapes.md](docs/team-shapes.md).
- **`task-dependency-gate.sh` and `task-complete-dispatch.sh` ship as
  REPORT-only.** Phase 0 didn't dump the TaskCompleted stdin schema.
  v0.2 needs a small probe to flip them to BLOCKING. Both have the
  routing logic in place; v0.1 logs the decision they would make.
- **Auto-migration tooling.** v0.1 expects manual migration per
  [MIGRATING.md](MIGRATING.md). A `yakos migrate` may show up in v0.2.
- **Multi-team coordination, cross-machine teams, auto-team-shape
  suggestion, and PandaOS migration.** See the per-doc "Not in v0.1"
  sections for specifics.
- **Specialist refinement against real use.** Phase 7 — opens *after*
  1–3 weeks of real use produce evidence on what to refine.

## License

TBD.
