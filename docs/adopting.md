# Adopting YakOS into an existing project

This guide is for the case where you have a project under git, you
write code in it, and you want to add YakOS-managed agentic work to
your toolkit. It assumes you've already done `./cli/yakos install`
once at the user level (per the README's Install section). If
you're migrating from a hand-rolled tmux + dispatch-CLI setup,
[`MIGRATING.md`](../MIGRATING.md) is the doc you want instead.

## The minimum-friction path

Five commands. Less than ten minutes, assuming the framework is
already installed.

```sh
# 1. From inside the project repo:
cd /path/to/your/project

# 2. Bootstrap YakOS for this project (creates ~/agent-control/<name>/,
#    drops project hooks into scripts/hooks/, drops .claude/{settings,
#    path-allowlist}.json templates if absent):
yakos init <project-name> --project "$(pwd)" --with-gate

# 3. Verify install + drift:
yakos doctor "$(pwd)"

# 4. Validate any agent / skill / rule files you create:
yakos validate "$(pwd)"

# 5. Run your first session:
cd ~/agent-control/<project-name>
claude --add-dir /path/to/your/project
```

`--with-gate` installs the pre-push version gate at
`<project>/.git/hooks/pre-push`. Your project also picks up the
four-part semver convention: `major.minor.patch.hotfix`. If your
project doesn't use four-part semver yet, add a `VERSION` file
containing the four-part form (`1.2.3.0` is fine for a project at
1.2.3).

## What just landed in the project

After step 2, your project has:

- `<project>/.claude/settings.json` — Claude Code session config
  (hooks, env). Idempotent template; preserves any pre-existing keys.
- `<project>/.claude/path-allowlist.json` — `path-allowlist.sh`'s
  per-role write paths. Edit to match your project's structure.
- `<project>/scripts/hooks/*.sh` — the framework's reference Claude
  Code hooks, copied with `.framework-hash` siblings (`yakos doctor`
  detects drift).
- `<project>/.git/hooks/pre-push` — pre-push version gate (only
  with `--with-gate`).
- `~/agent-control/<project-name>/` — the per-project ephemeral
  scratchpad (`work/current/{logs,artifacts,reports}/`,
  `decisions.md`, `messages.ndjson`, `team-inboxes/` snapshot at
  session end). NOT in your project's git.

## What to add over time

The bootstrap step deliberately doesn't drop project-specific agents
or rules. Add those as concrete needs surface:

### Project-specific agents

In `<project>/.claude/agents/<role>.md`. Use the `extends:`
mechanism to inherit framework discipline:

```yaml
---
id: myproject-backend
role: specialist
domain: nodejs-api
extends: backend
mode: [feature, fix, refactor]
tools: [Read, Edit, Write, Bash, Grep, TaskList, TaskUpdate, SendMessage]
model: sonnet
---

# MyProject Backend

## Purpose

Extends `backend` (layered architecture, contracts-driven workflow,
audit-log-on-mutation). Adds MyProject-specific stack: Node.js +
Fastify + Prisma + Postgres, the `apps/api/` layout...
```

Note: project-level agents are NOT currently runtime-discoverable as
`subagent_type` values (Phase 0.5 finding). The on-disk discipline
documents intent; live dispatch goes through
`skill:dispatch-as-project-agent` (general-purpose Agent + injected
agent body). See [`docs/team-shapes.md`](team-shapes.md) "Runtime
dispatch in v0.1" for the full picture.

### Project-specific rules

In `<project>/.claude/rules/<domain>.md` with path-scoped
frontmatter:

```yaml
---
id: myproject-nodejs
paths:
  - "apps/api/**/*.ts"
  - "apps/api/**/*.tsx"
---

# Node.js / Fastify / Prisma rules
...
```

These auto-load when Claude reads matching files.

### Project-specific skills

In `<project>/.claude/skills/<name>/SKILL.md`. Skills shadow generic
ones (project version wins on name conflict).

## Versioning + release flow

The pre-push gate (installed by `--with-gate`) refuses pushes that
contain substantive code changes since the last `v*.*.*` tag without
a corresponding VERSION change. Doc-only and hotfix-only pushes pass
through.

To bump:

```sh
yakos version-bump --component {major|minor|patch|hotfix}
git tag -a v$(cat VERSION) -m "release"
git push origin main && git push origin v$(cat VERSION)
```

Bump rules in [STYLE.md §8](../STYLE.md). The skill auto-detects
non-empty `[Unreleased]` and PROMOTES it to a versioned header
(rename) rather than inserting beside it.

## Audit + observability

Three files yakOS will accumulate as you use it:

| File | What it is | When to read it |
|---|---|---|
| `~/agent-control/<project>/work/current/decisions.md` | The lead's running notes for the current session. | Before starting a follow-up session, to recover context. |
| `~/agent-control/<project>/work/current/messages.ndjson` | Live mirror of every SendMessage call. | When peer DMs led to a decision and you need to audit it. |
| `~/.yakos-state/gate-log.ndjson` | Every pre-push gate decision (allow/refuse/override) across all projects. | After a refused push, OR to audit override usage. |

Plus per-session-end snapshots of team inbox files at
`work/current/team-inboxes/` (captures peer-to-peer DMs that don't
transit lead context).

## Session-recovery

When a session is interrupted or you start a fresh shell:

```sh
cd ~/agent-control/<project>
claude --add-dir /path/to/your/project
# Then in-session:
# Run skill:session-recovery
```

The skill reads the scratchpad, the latest decisions, and the
project's `CLAUDE.md` if present, and produces a working summary so
the next turn has the right context.

## What yakOS does NOT do for you

- Auto-detect your project's stack and configure accordingly. The
  templates are minimal; you fill in stack-specifics in
  project-level rules and (optional) extending agents.
- Migrate existing agentic-framework setups. See
  [`MIGRATING.md`](../MIGRATING.md) for that path.
- Replace your CI. The pre-push gate is a local-side audit; CI is
  still your ground truth for tests, builds, deploy.
- Modify your auto-memory at `~/.claude/projects/<encoded>/`. yakOS
  never touches that.

## Common adoption questions

**Do I need to run `yakos init` from inside the project, or from
elsewhere?** Either works. The `--project <path>` argument is
absolute. Idiomatic: from inside the project.

**Can I install only a subset of the framework hooks?** Yes — after
`yakos init`, edit `<project>/scripts/hooks/` (delete what you don't
want) and edit `<project>/.claude/settings.json`'s `hooks` block to
match. `yakos doctor <project>` will report the divergence as drift.

**What if my project already has `.git/hooks/pre-push`?** `yakos
git-hooks install` (or `yakos init --with-gate`) refuses to overwrite
non-YakOS hooks. Either compose them manually or use `--force` (which
overwrites — destructive).

**Is the `~/agent-control/<project>/` directory required to be at
that path?** Yes for v0.2.x. The path is hard-coded into the
`yakos_*` resolver helpers. Future: configurable via env.

**Does YakOS work without Claude Code?** No — it's a Claude Code
framework. The hooks fire on Claude Code's tool surface; the agents
load via Claude Code's agent loader. See
[`PHILOSOPHY.md`](../PHILOSOPHY.md) "Local models are workers, not
the orchestrator" for why.
