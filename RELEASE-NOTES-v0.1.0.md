# YakOS v0.1.0 — initial release

**Tag:** `v0.1.0`
**Date:** 2026-04-28

YakOS is a portable, multi-project agent framework for
[Claude Code](https://docs.claude.com/en/docs/claude-code/overview).
v0.1.0 is the initial release — a working framework with the
enforcement core in place and the documentation needed to adopt it.

## What v0.1 delivers

- **CLI** (`yakos install / uninstall / doctor / init / validate /
  archive / status / team restart / update`) — a single entry point
  for setting up the framework on a machine and bootstrapping
  projects on top of it.
- **8 reference hooks** under `lib/hooks/`:
  - `path-allowlist.sh` — BLOCKING. Per-(agent_type, path) allowlist
    on Edit / Write / MultiEdit. Phase 0 Test 6a validated.
  - `secret-scan.sh` — BLOCKING. Refuses writes containing common
    secret patterns (AWS keys, GitHub tokens, PEM private keys, etc.).
  - `mailbox-mirror.sh` — LOG. Mirrors every team-internal
    `SendMessage` to `messages.ndjson` for audit. Phase 1.7
    validated.
  - `path-log.sh`, `team-lifecycle.sh`, `session-end-check.sh` — LOG.
  - `task-dependency-gate.sh`, `task-complete-dispatch.sh` —
    **REPORT-only** in v0.1; see "Known-incomplete" below.
- **5 per-domain validators** (`backend / frontend / mobile /
  db-migration / changelog`) — functional standalone.
- **7 generic agents** + **11 framework skills** + **4 cross-cutting
  rules** — all under enforced line budgets (80–140 / 80–180 / 60–150).
- **6 framework playbooks** (security, code quality, UI/UX/a11y,
  docs, performance, regulated-data) — ported from production
  audit work. Playbook 06 generalized from HIPAA-only to multi-
  framework (HIPAA + GDPR + CCPA + SOC 2 + engagement-data).
- **`local-llm` skill** for safe local-model handoff (Ollama /
  LM Studio); output goes to `work/current/artifacts/` for
  Claude or human review.
- **Engineering standards** documented in
  [STYLE.md](STYLE.md) and
  [docs/engineering-standards.md](docs/engineering-standards.md);
  enforced lightly via `yakos validate` (WARN-only for most checks;
  ERROR-level for broken `playbook:` references).
- **Tiny-go-api example** at `examples/tiny-go-api/` demonstrating
  the framework end-to-end on a real Go HTTP server. Compiles,
  tests pass, validates clean.
- **Documentation:** [README.md](README.md), [PHILOSOPHY.md](PHILOSOPHY.md),
  [STYLE.md](STYLE.md), [CUSTOMIZING.md](CUSTOMIZING.md),
  [MIGRATING.md](MIGRATING.md), [COOKBOOK.md](COOKBOOK.md),
  [docs/team-shapes.md](docs/team-shapes.md),
  [INCIDENT-CATALOG.md](INCIDENT-CATALOG.md) (9 incidents),
  [COMPATIBILITY.md](COMPATIBILITY.md).

## Known-incomplete

These are honest gaps in v0.1. Each is documented; v0.2 closes them
in priority order.

### REPORT-only hooks

`task-dependency-gate.sh` and `task-complete-dispatch.sh` ship as
**REPORT-only** rather than BLOCKING. Both have the routing/decision
logic in place; both emit `mode: "report-only"` in their structured
log records.

**Why:** Phase 0 Test 5 confirmed `TaskCompleted` hooks CAN block,
but did not dump the `TaskCompleted` stdin schema. The team task
list at `~/.claude/tasks/<team>/` is documented as "auto-generated
and not safe to pre-author" — its file format is undocumented.
Without confirmed schemas for either input, these hooks cannot
make authoritative block/pass decisions in v0.1.

**v0.2 path:** a small Phase 0.5 probe that captures actual
TaskCompleted payloads in a live session, plus inspection of the
team task list format. Once both are confirmed, the hooks flip from
REPORT-only to BLOCKING with no API change.

### TeamDelete tool name inferred-not-validated

`team-lifecycle.sh` handles `TeamCreate`, `Agent`, and `TeamDelete`.
The first two are validated; `TeamDelete`'s exact tool_name string
is best-guess (Phase 1.7 didn't capture a TeamDelete event). If the
real name differs, only the TeamDelete branch silently no-ops;
TeamCreate and Agent paths remain validated.

### `INDEX.md` inflates inventory counts

`yakos validate`'s "agents: X | skills: Y | rules: Z" report counts
include `INDEX.md` files in the rules directory. Trivially cosmetic;
fix in v0.2.

### Stale CLI message

`yakos install`'s "Next steps" output still mentions
`yakos init (Batch 1B; not yet implemented)` — a Batch 1A stub
message that wasn't refreshed when 1B landed. Cosmetic; fix in v0.1.1.

### `lib/hooks/per-domain/` validators run only manually in v0.1

The five per-domain validators are functional but only invoked when
the dispatcher (`task-complete-dispatch.sh`) is BLOCKING — which it
isn't in v0.1. They can be invoked manually:

```sh
CLAUDE_PROJECT_DIR=/path/to/project bash lib/hooks/per-domain/backend-validate.sh
```

When the dispatcher flips to BLOCKING in v0.2, the validators will
gate task completion automatically.

### Pre-existing standards-check WARNs

`yakos validate` reports 26 WARN findings on shell scripts in `cli/`
and `lib/hooks/` — all of one type (`header lacks 'Purpose:' line`).
The standards landed in Batch 2.75 *after* the affected files were
written. Cleanup happens incrementally as files are edited; not a
v0.1.0 blocker.

### Local-model script's `ollama-required` tests are unverified

Steps 7, 9, and 10 of the Batch 5.5 self-validation suite require
an actual Ollama installation to run. They were skipped during the
v0.1.0 build (no Ollama on the build machine). When Ollama is
present, those tests can run as a v0.1.1 verification.

### `shellcheck` not run during the build

Optional per spec; was skipped because `shellcheck` wasn't installed
locally. v0.2 may make it required.

## What's NOT in v0.1

- **Multi-team coordination, cross-machine teams.** Single-machine,
  single-team (with N teammates) in v0.1.
- **Auto-migration tooling.** Manual port per
  [MIGRATING.md](MIGRATING.md).
- **PandaOS migration as a worked example.** That's Phase 8 — a
  separate session post-v0.1.
- **The `architect`, `incident-responder`, `log-analyst`,
  `devops-infra`, `performance-engineer`, `privacy-reviewer`,
  `accessibility-reviewer`, `ux-reviewer` agents.** Roadmap in
  [docs/team-shapes.md](docs/team-shapes.md).
- **Specialist refinement against real use.** Phase 7 — opens
  *after* 1–3 weeks of real use produce evidence on what to refine.

## Migration path for users with existing tmux setups

If you have a hand-rolled tmux + dispatch-CLI setup (per the Phase
1.5 §21 starting point), see
[MIGRATING.md](MIGRATING.md) for the 10-step port. Headlines:

- **Agent prompts** at `.panda-team/prompts/*.md` (or equivalent) →
  `<project>/.claude/agents/<role>.md`. Drop project prefixes; the
  directory disambiguates.
- **Dispatch CLIs** are gone. Native Agent Teams primitives
  (`SendMessage`, task list) replace them.
- **Hook configuration** lives in `<project>/.claude/settings.json`
  under the `hooks` field — NOT in a `hooks.json` file.
- **Ownership rules** embedded in agent prompts → path-scoped rules
  in `<project>/.claude/rules/`.
- **Active scratchpad** moves out of the project repo into
  `~/agent-control/<project>/work/current/`.

v0.1 expects you to commit fully — running the old setup *and*
YakOS side-by-side is not supported (they fight over hook config).

## Verifying v0.1.0 in your environment

```sh
git clone --branch v0.1.0 https://github.com/<you>/yakos.git ~/code/yakos
cd ~/code/yakos
./cli/yakos install
./cli/yakos doctor
```

Then bootstrap a project:

```sh
./cli/yakos init <project-name> --project /path/to/your/project
```

If `yakos doctor` reports clean, you're set. If it reports drift on
hook copies, check
[CUSTOMIZING.md](CUSTOMIZING.md) — drift is informational (projects
are expected to customize), not error.

## How v0.1.0 was built

11 batches over Phase 2, with a pause for human review at every batch
boundary:

| Batch | Commit | What landed |
|---|---|---|
| 1A | `cc1f48b` | CLI skeleton + safe install/uninstall |
| 1B | `aca2747` | Remaining CLI subcommands |
| 2 | `3262829` | Hooks + per-domain validators + 15 fixtures |
| 2-retrofit | `b34b973` | Tighten session tracking, unify work-dir resolution |
| 2.75 | `2ef7323` | Engineering standards + 8 validate checks |
| 3 | `430f59e` | Generic agents + skills + cross-cutting rules |
| 4 | `5c6290d` | Framework documentation |
| 5 | `24c4536` | Tiny-go-api example |
| 5.5 | `94de12c` | Local-model integration templates |
| 5.7 | `4186dd6` | Framework playbooks + ERROR-level reference check |
| 6 | (this) | Smoke test + tag |

Each batch is a single-commit, push-pause-review unit. The audit
trail is in the per-batch `BATCH-N-STATUS.md` files at the repo root.
Read them in order if you want to know how decisions were made.

## Thanks

To Phase 0 and Phase 1.7's validation work, which surfaced the
mailbox privacy gap, the `blockedBy` advisory-not-enforced finding,
the lead-vs-teammate `agent_type` discriminator, and the
SendMessage hookability that makes mailbox-mirror possible. Without
those validations, this release would have been wrong in
load-bearing ways.

To Ben, Thomas, and the friends who reviewed Phase 1 and Phase 1.5
and produced the architectural framing this v0.1 implements.

## What's next

- **1–3 weeks of real use.** Don't refine specialists yet. Don't add
  agents yet. Just use the framework on actual project work and
  capture observations.
- **Phase 7 — specialist refinement.** Opens after real-use
  evidence accumulates. Iterates the most-used specialists against
  observed failure modes.
- **v0.2** — playbook population for the architect / incident-
  responder / privacy-reviewer / etc. agents per the team-shapes
  roadmap, plus the Phase 0.5 probe to flip `task-dependency-gate.sh`
  and `task-complete-dispatch.sh` to BLOCKING.
- **Phase 8 — PandaOS migration.** The first real worked-example
  port. Separate Claude Code session, separate batch sequence.
