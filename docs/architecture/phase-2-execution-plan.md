# Phase 2 Execution Plan

**This document is the master sequence for building YakOS v0.1.** It describes
which prompt to paste at which batch boundary, in what order, with explicit
pause points where Claude Code must STOP and wait for human review.

This document does NOT replace the individual batch prompts. It coordinates
them. The actual prompts to paste live in separate files; this doc tells you
when each one applies.

**For Claude Code:** read this document in full before starting. Treat it as
the canonical roadmap. When you hit a pause point, stop and wait — do not
auto-proceed. The prompts at each boundary will be pasted by the human at
the right time.

**For the human (Thomas):** keep this document open in a tab while Phase 2
runs overnight. When you wake up and check status, find the most recent
checkpoint Claude Code paused at, then paste the next prompt.

---

## Source of truth files

These define the architecture and validation results that constrain the build.
They live in `~/code/yakos/docs/architecture/` and are READ by Claude Code,
not modified.

| File | Purpose |
|---|---|
| `phase-1.5-architecture.md` | The architecture spec. The "what" and "why" of YakOS. |
| `phase-0-validation-results.md` | What Agent Teams primitives actually do. |
| `phase-1.7-results.md` | SendMessage hookability validation. |
| `phase-2-execution-plan.md` | This document. The "when" and "in what order." |

## Prompt files (the "what to paste at each boundary")

These are paste-when-needed prompts, not committed-to-repo source. They
should be reachable from the Claude Code session, OR pasted by the human at
the right time. They are NOT read by Claude Code preemptively.

| File | When to paste |
|---|---|
| `phase-2-build-prompt-v2.md` | Initial paste — kicks off the whole build |
| `phase-2-session-tracking-retrofit-v2.md` | After Batch 2 review |
| `phase-2-batch-2.75-engineering-standards.md` | After retrofit review |
| `phase-2-batch-4-team-shapes-addendum.md` | DURING Batch 4 (mid-batch insert) |
| `phase-2-batch-5.5-addendum-v2.md` | After Batch 5 review (optional) |
| `phase-7-specialist-refinement-plan.md` | NOT during Phase 2 — weeks post-v0.1 |

---

## The full sequence — 11 checkpoints

### Checkpoint 0 — Pre-build setup

What happened:
- `~/code/yakos/` created as a git repo with GitHub origin
- Source-of-truth docs placed in `docs/architecture/`
- `phase-2-build-prompt-v2.md` pasted into a fresh Claude Code session
- Initial environment checks ran (claude version, jq, tmux, etc.)
- Source-of-truth docs were read and summarized back

### Checkpoint 1 — Batch 1A complete

Verify:
- `BATCH-1A-STATUS.md` exists in repo root
- `cli/yakos`, `cli/lib/compat.sh`, `cli/lib/install.sh`, `cli/lib/uninstall.sh`,
  `cli/lib/doctor.sh` exist and are functional
- Other CLI subcommands are STUBS (intentional — Batch 1B fills them)
- The temp-HOME install/uninstall round-trip test passed
- Commit message starts with `feat(batch-1a):`

### Checkpoint 2 — Batch 1B complete

Verify:
- `BATCH-1B-STATUS.md` exists
- `cli/lib/init.sh`, `update.sh`, `validate.sh`, `archive.sh`, `status.sh`,
  `team.sh` are functional (no longer stubs)
- `yakos init` produced an `.session-started-history` file (or its NDJSON
  variant) and copied hooks with `.framework-hash` siblings
- Commit message starts with `feat(batch-1b):`

### Checkpoint 3 — Batch 2 complete

Verify:
- `BATCH-2-STATUS.md` exists
- All hook scripts under `lib/hooks/` exist and are executable
- `lib/hooks/lib/hook-input.sh` and `hook-output.sh` exist
- `lib/hooks/per-domain/*.sh` exist (5 validators)
- `tests/fixtures/hooks/*.json` committed (the fixture suite)
- Each hook ran against its corresponding fixture during self-validation
- Any UNCLEAR or REPORT-only hooks are noted in `BATCH-2-STATUS.md`
- Commit message starts with `feat(batch-2):`

Read `BATCH-2-STATUS.md` carefully before pasting Checkpoint 4. Look for:

- Whether `task-dependency-gate.sh` and `task-complete-dispatch.sh` were
  implemented as full enforcement or fell back to REPORT-only mode
- Any UNCLEAR findings around tool names (TeamCreate, TeamDelete, Agent —
  these were inferred not validated)
- Whether `team-lifecycle.sh` and `session-end-check.sh` already write to
  `.session-started`, `.session-started-history`, or `sessions.ndjson` (the
  retrofit's behavior depends on what's already there)

### Checkpoint 4 — Session tracking retrofit

**Action:** Paste the prompt section from
`phase-2-session-tracking-retrofit-v2.md`.

This is a defect fix to Batch 2, not a new batch. It addresses:

- Work-directory resolution disagreement between Batch 1B's CLI commands and
  Batch 2's hooks (the most consequential change)
- NDJSON vs JSON-array format for `.session-started-history`
- Idempotency on session summaries (session_id + exit_kind)
- Portable directory size (`du -sb` doesn't work on macOS)
- Explicit no-block telemetry policy
- Tool name verification before assuming TeamCreate/TeamDelete/Agent

**Expected duration:** 30-45 minutes.

**Pause point:** When `BATCH-2-RETROFIT-STATUS.md` is committed and pushed.

Verify before proceeding:
- The retrofit's 12 self-validation steps all passed
- Tool names were verified against actual fixtures (`BATCH-2-RETROFIT-STATUS.md`
  documents what they are if different)
- Work-directory resolver is unified between CLI and hooks
- `yakos status` reads from the same path that hooks write to (this is the
  end-to-end test)
- Commit message starts with `fix(batch-2):`

### Checkpoint 5 — Batch 2.75 engineering standards

**Action:** Paste the prompt section from
`phase-2-batch-2.75-engineering-standards.md`.

This is the standards patch that Batch 3 onwards will follow. It:

- Creates `STYLE.md`, `docs/engineering-standards.md`, `tests/README.md`
- Updates `cli/lib/validate.sh` with lightweight standards checks
- Updates `README.md` and `PHILOSOPHY.md` with standards references

**Expected duration:** 60-90 minutes.

**Pause point:** When `BATCH-2.75-STATUS.md` is committed.

Verify before proceeding:
- `STYLE.md` is within 200-350 lines
- `docs/engineering-standards.md` is within 400-700 lines
- `yakos validate` runs without crashing and reports WARN messages
- The five specialist questions are documented in
  `docs/engineering-standards.md` (these are needed for Batch 3 quality)
- Commit message starts with `chore(batch-2.75):`

### Checkpoint 6 — Batch 3 (generic agents and skills)

**Action:** Tell Claude Code "go to Batch 3" with this addition:

> All files written in Batch 3 follow the standards in STYLE.md. Every
> agent file answers the five specialist questions documented in
> `docs/engineering-standards.md`. Run `yakos validate` after each subset
> of files to catch standards violations early. Stay within line budgets:
> agents 80-140, skills 80-180, rules 60-150.

This is the original Batch 3 from `phase-2-build-prompt-v2.md` plus the
explicit standards reference.

**Expected duration:** 90-120 minutes.

**Pause point:** When `BATCH-3-STATUS.md` is committed.

Verify before proceeding:
- 7 generic agents in `lib/agents/`
- 11 skills in `lib/skills/`
- 4 cross-cutting rules in `lib/rules/`
- Each agent's frontmatter parses, line count is within budget
- Each skill's `SKILL.md` has the required sections
- `yakos validate` reports few WARN messages
- Each agent answers the five specialist questions concretely
- Commit message starts with `feat(batch-3):`

### Checkpoint 7 — Batch 4 (documentation)

**Action:** Tell Claude Code "go to Batch 4."

**Mid-batch insert:** When Batch 4 has produced `README.md`, `CUSTOMIZING.md`,
`MIGRATING.md`, `PHILOSOPHY.md`, and is starting on `COOKBOOK.md`, paste the
prompt section from `phase-2-batch-4-team-shapes-addendum.md`.

Don't pause separately for the team-shapes addendum. It folds into Batch 4's
single commit.

**Expected duration:** 90-120 minutes total (including team-shapes).

**Pause point:** When `BATCH-4-STATUS.md` is committed.

Verify before proceeding:
- README, CUSTOMIZING, MIGRATING, PHILOSOPHY, COOKBOOK, INCIDENT-CATALOG,
  COMPATIBILITY, CHANGELOG, and `team-shapes.md` all exist
- Each doc has a "Not in v0.1" section if applicable
- Cross-references between docs resolve
- `team-shapes.md` is referenced from `COOKBOOK.md`
- Commit message starts with `docs(batch-4):`

### Checkpoint 8 — Batch 5 (tiny example)

**Action:** Tell Claude Code "go to Batch 5."

**Expected duration:** 60-90 minutes.

**Pause point:** When `BATCH-5-STATUS.md` is committed.

Verify before proceeding:
- `examples/tiny-go-api/` exists with all expected files
- The Go code compiles and tests pass
- `yakos validate examples/tiny-go-api/` is clean
- Hook files in `examples/tiny-go-api/scripts/hooks/` have `.framework-hash`
  siblings
- Agents are prefixed `tiny-` (e.g., `tiny-lead.md`, `tiny-api.md`)
- Commit message starts with `feat(batch-5):`

### Checkpoint 9 — Batch 5.5 (local model templates) — OPTIONAL

**Action:** Either:
- (A) Paste the prompt section from `phase-2-batch-5.5-addendum-v2.md` to
  add local-model integration templates, OR
- (B) Skip directly to Checkpoint 10 by telling Claude Code "go to Batch 6;
  Batch 5.5 will be added post-v0.1"

This is genuinely optional. The smoke test in Batch 6 doesn't depend on
Batch 5.5.

If you do Batch 5.5:
- **Expected duration:** 45-60 minutes.
- **Pause point:** When `BATCH-5.5-STATUS.md` is committed with message
  `feat(batch-5.5):`

### Checkpoint 10 — Batch 6 (smoke test) — VALIDATION ONLY

**Action:** Tell Claude Code "go to Batch 6."

**Critical:** Batch 6 uses a TEMPORARY HOME for all install testing. Real
`~/.claude/` is never touched. The script asserts `TEST_HOME != HOME` before
doing anything destructive.

This batch produces no new framework files. It runs the framework against
itself end-to-end: install → init → fake session → archive → uninstall.

**Expected duration:** 30-45 minutes.

**Pause point:** When `BATCH-6-STATUS.md` is committed AND the v0.1.0 tag
is pushed.

Verify before declaring v0.1 done:
- The full smoke test passed
- Real `~/.claude/` was never modified
- `git tag` shows `v0.1.0`
- `RELEASE-NOTES-v0.1.0.md` exists at repo root
- The release notes correctly identify which hooks (if any) shipped as
  REPORT-only and what's not in v0.1

### Checkpoint 11 — Phase 2 retrospective (15 min)

**Action:** Spend 15 minutes writing a brief retrospective in
`docs/retrospectives/phase-2-build.md`.

Capture:
- What went well
- What surprised you
- What would you change about the build prompts if redoing
- Which Batch's status report was most useful
- Any deviations Claude Code made from the architecture (already in batch
  status reports; consolidate here)

---

## What happens AFTER Phase 2

### Real-use phase (1-3 weeks)

Use YakOS on real PandaOS work. Don't refine specialists. Don't add agents.
Just use it. Capture observations. Track in a notes file. Don't try to fix
anything yet.

### Phase 7 — specialist refinement (post-real-use)

After 1-3 weeks of real use, you have evidence for what to refine. THEN
open `phase-7-specialist-refinement-plan.md`.

Do NOT start Phase 7 immediately after Phase 2 ends. Refining prompts
without real-use evidence produces theoretically-good prompts based on
speculation.

### v0.2 work (post-Phase-7, as needed)

After Phase 7 has produced refined versions of the most-used specialists,
consider v0.2 additions:

- Architect agent (for greenfield work)
- Incident-responder (for production support)
- Other specialist roles per `docs/team-shapes.md` roadmap

Add them when concrete demand surfaces.

### Possible Phase 8 — PandaOS migration (separate session)

The Phase 1.5 architecture supports migrating PandaOS off the tmux setup
onto YakOS. This is a separate Claude Code session.

---

## Pause-point discipline

The most important rule for Phase 2: **pause at every checkpoint**.

- Don't auto-merge between batches.
- Don't auto-fix issues you're unsure about.
- Don't try to be ahead.
- If you're paused at any checkpoint when the human checks in, that's
  correct.

When Claude Code finishes a batch and writes the status report, the next
correct action is to STOP and write a one-sentence message: "Paused at
Checkpoint N. Awaiting review."

The human responds with either:
- "go" or "go to Checkpoint N+1" → proceed
- "fix X first" → address feedback, then proceed
- "stop" → human takes over

---

## Quick-reference checkpoint summary

| # | Name | Source prompt | Duration |
|---|---|---|---|
| 0 | Pre-build setup | (manual setup) | — |
| 1 | Batch 1A — CLI core | `phase-2-build-prompt-v2.md` | — |
| 2 | Batch 1B — CLI extension | `phase-2-build-prompt-v2.md` | — |
| 3 | Batch 2 — Hooks | `phase-2-build-prompt-v2.md` | — |
| 4 | Session-tracking retrofit | `phase-2-session-tracking-retrofit-v2.md` | 30-45 min |
| 5 | Batch 2.75 — Standards | `phase-2-batch-2.75-engineering-standards.md` | 60-90 min |
| 6 | Batch 3 — Generic agents/skills | `phase-2-build-prompt-v2.md` (Batch 3 + standards reminder) | 90-120 min |
| 7 | Batch 4 — Documentation | `phase-2-build-prompt-v2.md` (Batch 4) + mid-batch addendum | 90-120 min |
| 8 | Batch 5 — Tiny example | `phase-2-build-prompt-v2.md` (Batch 5) | 60-90 min |
| 9 | Batch 5.5 — Local model (OPTIONAL) | `phase-2-batch-5.5-addendum-v2.md` | 45-60 min |
| 10 | Batch 6 — Smoke test | `phase-2-build-prompt-v2.md` (Batch 6) | 30-45 min |
| 11 | Retrospective | (manual; 15 min) | 15 min |
