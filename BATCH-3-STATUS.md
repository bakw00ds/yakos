# Batch 3 — status report

**Status:** Complete. 7 generic agents + 11 skills + 4 cross-cutting rules
+ 3 README/INDEX files. All within line budgets. 0 validate errors.
20/20 fixture suite still green. 25 symlinks now appear in `~/.claude/`
when `yakos install` runs.

## What was built

### Agents (7 — `lib/agents/`)

All within the 80–140 line budget.

| File | Lines | Role | Model |
|---|---:|---|---|
| `lead-template.md` | 90 | orchestrator | opus |
| `planner.md` | 80 | specialist | opus |
| `test-runner.md` | 86 | specialist | sonnet |
| `code-reviewer.md` | 80 | reviewer | sonnet |
| `security-reviewer.md` | 89 | reviewer | opus |
| `troubleshooter.md` | 83 | specialist | sonnet |
| `doc-writer.md` | 82 | specialist | sonnet |
| `README.md` | 42 | inventory + standards (excluded from budget check) |

Each agent answers the **five specialist questions** in a "When to push
back / escalate" section per `docs/engineering-standards.md §9`:

1. When to push back on the lead
2. When to ask for human approval
3. What never to edit
4. What "done" means
5. What this specialist knows that a generic coder would miss

Question 5 is the substantive one — answered with concrete domain bullets
(test-runner: flaky-loop trap, race detector, flutter-tester hang;
security-reviewer: dependency-as-trust-surface, regex-as-security-boundary
warning; troubleshooter: bisect, time-box, "the proximate cause is rarely
the root").

### Skills (11 — `lib/skills/`)

All within the 80–180 line budget. Each `SKILL.md` has the validate-required
sections (Purpose, Scope, Automated pass, Manual pass, Known gotchas).

| Skill | Lines | Mode |
|---|---:|---|
| `pre-commit/SKILL.md` | 80 | review |
| `test-suite/SKILL.md` | 86 | review |
| `session-recovery/SKILL.md` | 91 | recover |
| `project-init/SKILL.md` | 87 | implement |
| `gather-feedback/SKILL.md` | 88 | gather |
| `deploy-check/SKILL.md` | 87 | review |
| `verify-agent-work/SKILL.md` | 84 | review |
| `split-mega-task/SKILL.md` | 84 | plan |
| `contract-handoff/SKILL.md` | 92 | implement |
| `phase-complete/SKILL.md` | 92 | review |
| `dependency-update/SKILL.md` | 87 | maintain |

### Rules (4 — `lib/rules/`)

All within the 60–150 line budget.

| Rule | Lines | Scope |
|---|---:|---|
| `git-hygiene.md` | 79 | always-loaded |
| `commit-format.md` | 84 | always-loaded |
| `secret-handling.md` | 83 | path-scoped (`.env*`, credential patterns) |
| `pr-conventions.md` | 80 | always-loaded |

`INDEX.md` (34 lines) lists the inventory and explains the load model.

## Self-validation

| # | Test | Result |
|---|---|---|
| 1 | Each agent's frontmatter parses cleanly | ✓ all 7 |
| 2 | Each agent line count within 80–140 | ✓ 80–90 actual |
| 3 | Each agent has Purpose / Execution / Special rules / Handling peer messages / Personality sections | ✓ all 7 |
| 4 | Each agent answers the five specialist questions | ✓ in "When to push back / escalate" section |
| 5 | Each `SKILL.md` line count within 80–180 | ✓ 80–92 actual |
| 6 | Each `SKILL.md` has Purpose / Scope / Automated pass / Manual pass / Known gotchas | ✓ all 11 |
| 7 | Each rule line count within 60–150 | ✓ 79–84 actual |
| 8 | `yakos validate lib/` reports 0 errors | ✓ |
| 9 | `yakos validate lib/` reports no NEW warnings on lib/agents, lib/skills, lib/rules | ✓ — all 26 existing warnings are on shell scripts written before Batch 2.75 |
| 10 | `tests/run-hook-fixtures.sh` still 20/20 green | ✓ |
| 11 | End-to-end: `yakos install` creates 25 symlinks in `~/.claude/{agents,skills,rules}/` | ✓ (8 + 12 + 5 = 25) |
| 12 | `yakos doctor` against the test project reports 0 errors, 0 warnings | ✓ |

## Standards conformance

Every new file in this batch has:

- Frontmatter matching the schema in Phase 1.5 §9 / §10 / §11.
- Required sections per `yakos validate`.
- Line count within budget.
- `references:` field listing rule/incident dependencies (no `playbook:`
  references — see "What's deferred" below).

The 26 existing validate WARNs (all "missing Purpose: header" on
shell-scripts written in Batches 1A/1B/2/2-retrofit) are unchanged.
Per the Batch 2.75 spec, these get cleaned up incrementally as files
are touched in future work, not in a single mega-PR.

## What's deferred / out of scope

### `lib/playbooks/` is not populated in v0.1

Phase 1.5 §4 lists 6 playbooks (01-security through 06-hipaa-phi) in
`yakos/lib/playbooks/`, but no batch in the v0.1 build prompt populates
them. The directory remains empty (`.gitkeep` only).

**Decision:** agents do NOT use `playbook:` references in their
`references:` field, since those references would fail to resolve.
Playbook-shaped content (procedural, multi-domain) is the natural
home for v0.2 expansion — populate then.

The build-prompt-v2 Batch 3 spec mentions playbooks as a *source* for
agent content ("the 6 PandaOS playbooks already in `lib/playbooks/`
inform what `security-reviewer` and `code-reviewer` look like"). Since
those playbooks don't exist, I synthesized the agent content from:

- The architecture doc's domain-specific guidance (Phase 1.5 §10 on
  per-domain validators; §15 on incident-shaped lessons).
- The Phase 0 / Phase 1.7 results (peer-message handling, the
  `agent_type` discriminator, mailbox auditability).
- The five-questions framework from `docs/engineering-standards.md §9`,
  which is the durable framing v0.2 playbooks will plug into.

### Inheritance not exercised

The `lead-template` is the base of an inheritance chain projects fill
in (PandaOS's `lead.md` would `extends: lead-template`). v0.1 ships
the template; the example of project-level extension lands in the
Batch 5 `tiny-go-api` example.

### Project-specific allowlist examples

The `path-allowlist.json` template's example block references roles
that don't ship in the framework (`go-api`, `flutter-ui`, etc.) —
these are project-specific specialists. The framework's generic agents
(`code-reviewer`, `test-runner`, etc.) intentionally don't get
allowlist entries because they're cross-cutting; their access is
constrained by what their `tools:` lists allow, not by path policy.

## Bugs caught and fixed

None during this batch. The standards checks added in Batch 2.75
caught zero issues on new files — the explicit standards reference
in the Checkpoint 6 prompt did its job.

## What's next

**Checkpoint 7 — Batch 4 (documentation).** Per the execution plan,
Batch 4 ships `README.md` (expanded), `CUSTOMIZING.md`, `MIGRATING.md`,
`PHILOSOPHY.md` (expanding the Batch-2.75 stub), `COOKBOOK.md` (with
the team-shapes addendum mid-batch), `INCIDENT-CATALOG.md` (PandaOS
incidents populated), `COMPATIBILITY.md`, and `RELEASE-NOTES-v0.1.0.md`.

Pushed to `origin/main`.
