# YakOS Cookbook

Worked recipes for common YakOS workflows. Each pattern is a short
walkthrough; see linked docs for depth.

## Choosing a team

Before reading the recipes below, see
[docs/team-shapes.md](docs/team-shapes.md) for the catalog of
recommended team compositions. Different project types and lifecycle
stages need different teams; the cookbook recipes assume you've
picked an appropriate shape.

The TL;DR from team-shapes.md: **one team per logical unit of work**.
Don't reuse a team across unrelated work.

---

## Pattern 1: Adding a feature that touches DB / API / UI

The most common feature shape. Use the **small web app team** from
team-shapes.md.

### Setup

```sh
cd ~/agent-control/<project>
claude --add-dir /path/to/project-repo
```

In the lead's first turn:

> Create a team for the workout-block feature.
> Spawn db-migrations as 'db', go-api as 'api', flutter-ui as 'ui',
> test-runner as 'tests'.
> API and UI publish a contract before either implements.
> Require plan approval for db.
> Wait for teammates — don't do work yourself.

### Flow

1. Lead writes `plan.md` with 4–6 tasks (`TaskCreate` for each).
2. `db` proposes the schema migration, surfaces for plan approval
   before implementing. Lead approves; db creates the migration.
3. `api` proposes the API contract, publishes via `contract-handoff`
   skill to `contracts.md`.
4. `ui` reads the contract, generates client types, implements.
5. `tests` runs the suite via `test-suite` skill; reports.
6. Lead synthesizes to `decisions.md`, closes out via `phase-complete`
   skill.

### What the framework does for you

- `path-allowlist.sh` enforces (agent, path) — db touches migrations,
  api touches `api/`, ui touches `mobile/lib/` or `web/`.
- `task-complete-dispatch.sh` (REPORT-only in v0.1) would route
  per-domain validators on completion. v0.1 logs the routing
  decision; specialists run validators manually via `pre-commit`.
- `mailbox-mirror.sh` records every peer DM in `messages.ndjson` —
  if api and ui negotiate the contract by message, the conversation
  is in the audit trail.
- `session-end-check.sh` audits stale `decisions.md` and any
  unresolved bypasses on lead exit.

---

## Pattern 2: Running a parallel review team

When you have a substantial change (release, large refactor, security-
sensitive feature), parallelize review.

### Setup

In the lead's first turn:

> Create a review team for v2.69.0.
> Spawn code-reviewer as 'cr', security-reviewer as 'sec',
> test-runner as 'tests', doc-writer as 'docs' (review mode).
> Each reviews independently; surface findings to findings.md;
> no peer DMs about reviews — write to findings.md.

### Why no peer DMs

Per Phase 0 Test 8, peer messages are private content. Cross-reviewer
discussion ("did you see X?", "is Y a problem?") needs to be in the
shared `findings.md` so the lead and audit trail see it. DMs would
fragment the discussion across private contexts.

### Flow

1. Each reviewer reads the change set. They work in parallel.
2. Each writes to their section of `findings.md`:
   `## code-reviewer findings`, `## security-reviewer findings`,
   etc.
3. Findings are categorized: blocking / suggested / nit (per
   `code-reviewer.md` execution).
4. Lead reads `findings.md`, decides which blocking findings to
   fix-first vs ship-with-known-issue.
5. `phase-complete` skill verifies exit criteria.

### What the framework does

- The `mailbox-mirror.sh` still captures any DMs that do happen
  (don't worry about teammates breaking the rule — the audit
  catches it).
- The `session-end-check.sh` audits whether `decisions.md` is
  fresh — a release without final decisions logged is a flag.
- The `code-reviewer` and `security-reviewer` agents have specific
  domain knowledge baked in (per their five-questions sections).

---

## Pattern 3: Investigating a bug with adversarial agents

Use the **bug investigation team** from team-shapes.md, with one twist:
add a second specialist as an *adversarial* second-opinion check.

### Setup

> Create a debugging team for incident-2649.
> Spawn troubleshooter as 'tro' (read-only), go-api as 'api'
> (fix-implementer).
> Tro diagnoses; api implements the fix on tro's diagnosis.
> When tro proposes a root cause, ask api to challenge it before
> implementing. We want adversarial review, not rubber-stamping.

### Why this works

Phase 0 Test 4 confirmed that teammates can drift under peer pressure.
*That* drift is what we want here: api pushing back on a plausible-
but-unverified diagnosis catches the class of bug where the proximate
cause isn't the root cause.

### Flow

1. `tro` reproduces the bug, bisects, forms a hypothesis.
2. `tro` writes to `findings.md` with the hypothesis + verification
   evidence.
3. Lead asks `api`: "Does this diagnosis explain the symptom? What's
   the most likely alternative cause?" SendMessage to `api`.
4. `api` either confirms or proposes alternatives. Either response
   is signal — confirmation strengthens the diagnosis; alternatives
   reveal blind spots.
5. Lead decides: proceed with `tro`'s fix-direction or have `tro`
   investigate the alternative.
6. `api` implements once the diagnosis is settled; `tests` (spawn
   a third teammate for this) verifies.

### What the framework does

- The `troubleshooter`'s body forbids editing — even when the cause
  feels obvious. The split is structural, not advisory.
- `mailbox-mirror.sh` captures the diagnostic peer-DM exchange so
  the audit trail records the adversarial review.

---

## Pattern 4: Releasing a version

Use the **release-prep team** from team-shapes.md.

### Setup

> Create a release-prep team for v2.69.0.
> Spawn release-auditor as 'audit' (project-specific, runs the
> release-audit skill), doc-writer as 'docs', security-reviewer
> as 'sec'.
> Audit first; docs and sec run in parallel after audit's clean
> pass.

### Flow

1. `audit` invokes the project's `release-audit` skill (the 6-domain
   playbook from PandaOS-style projects, or the framework's
   `phase-complete` for simpler releases). Writes to `reports/audit.md`.
2. After audit's clean pass, `docs` and `sec` run in parallel:
   - `docs` updates `CHANGELOG.md` and writes `RELEASE-NOTES-vX.Y.Z.md`.
   - `sec` does final pass on auth/data-handling/dep-updates since
     last release.
3. Lead synthesizes to `decisions.md`: what shipped, what didn't,
   what's known-issue.
4. Lead runs `deploy-check` skill before tagging (env-vars, build
   sanity, smoke).
5. Tag and push: `git tag -a v2.69.0 -m "..." && git push --tags`.
6. Lead invokes `yakos archive <project> v2.69.0` to roll the
   scratchpad into `work/archive/v2.69.0/`.

### What the framework does

- The `phase-complete` skill verifies exit criteria automatically
  (tasks completed, no outstanding blocks, no expired bypasses,
  decisions fresh, tests passing, docs updated).
- The `deploy-check` skill catches the most common pre-deploy
  blunders (missing env vars, build artifacts wrong size).
- `yakos archive` refuses if expired bypasses are present —
  forces clean state before ship.

---

## Anti-patterns to avoid

- **Reusing the same team across unrelated work.** Spawn fresh teams
  per logical unit; archive between.
- **Lead doing specialist work.** When the lead pulls a task from
  a specialist's backlog, the team's coherence degrades. Trust the
  specialists.
- **Fixing flakes by re-running.** A flake is the bug. Report; don't
  paper over.
- **Relying on `blockedBy` for safety.** It's advisory. The
  `task-dependency-gate.sh` hook is what enforces (REPORT-only in
  v0.1; v0.2 makes it BLOCKING).
- **Force-pushing to main.** Always blocked by hooks; don't try to
  bypass.
- **Letting the scratchpad grow unbounded.** `yakos status` warns at
  100MB; archive before then.

---

## Not in v0.1

- **Multi-team coordination.** A "team-of-teams" pattern (a release
  manager coordinating across feature teams) isn't a primitive in
  v0.1. v0.2+ if real demand surfaces.
- **Resumable interrupted teams.** If a team session crashes mid-
  flight, recovery is via the `session-recovery` skill, but the
  task list state may not be perfectly aligned with reality —
  reconciliation is manual.
- **Cross-machine teams.** Single-machine in v0.1.
