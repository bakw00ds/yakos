# Incident Catalog

Durable, IDed records of past incidents. Other artifacts (rules,
agents, hook scripts) reference these by stable ID. New incidents
get added here once their resolution is understood; entries are
immutable in spirit (corrections welcome; deletion is anti-pattern).

## Schema

Each entry follows a fixed shape:

```markdown
## incident:<stable-id>

**Date:** <ISO date>
**Project:** <name>
**Severity:** <P0/P1/P2/P3>
**Summary:** <one paragraph>
**Impact:** <observed user-facing or operational impact>
**Root cause:** <what actually broke>
**Prevented by:** <list of artifacts that defend against recurrence>
**Related rules / agents / playbooks:** <cross-refs>
```

Reference syntax in other artifacts: `incident:<stable-id>`.
`yakos validate` (post-Batch-3) checks every reference resolves.

---

## incident:v2.49.0-force-push

**Date:** 2025-Q4
**Project:** PandaOS
**Severity:** P1 — committed work disappeared from main

**Summary:** A force-push to `main` overwrote ~6 hours of merged
commits. The agent that did the push believed it was rebasing onto
a clean base; the base it rebased onto was stale, and the force-push
nuked the intervening commits.

**Impact:** Six hours of merged commits had to be reconstructed from
local checkouts and CI artifacts. Two team members lost in-flight
unmerged work that depended on the dropped commits.

**Root cause:** No hook-level prevention of commit-dropping force
pushes. The agent's body said "don't force-push to main"; the body
was insufficient under operational pressure.

**Prevented by:**
- `rule:git-hygiene` §"Force push" — the soft control.
- Git pre-push hook (`agent-pre-push.sh`) — the hard control. Refuses
  any force-push to main that drops commits.
- `path-allowlist.sh` for `.git/` operations on the lead role.

**Related rules / agents:** `rule:git-hygiene`, `rule:pr-conventions`.

---

## incident:v2.62.4-worktree-collision

**Date:** 2026-01
**Project:** PandaOS
**Severity:** P1 — concurrent agent edits to the same working tree
corrupted both teammates' work-in-progress.

**Summary:** Two specialists were spawned in the same git working
tree for what looked like independent tasks. They edited overlapping
files. The second teammate's edits silently overwrote the first's
in-flight changes; the corruption surfaced only when the lead tried
to commit and saw a confused diff.

**Impact:** ~2 hours of specialist work lost; both teammates had to
re-do their work from scratch in separate worktrees.

**Root cause:** No structural separation between concurrent
teammates. The lead spawned multiple agents without giving each its
own worktree.

**Prevented by:**
- `rule:git-hygiene` §"Worktree" — the soft control. Lead must
  create a worktree per agent for concurrent dispatch.
- Lead's body specifically mentions worktree setup before spawning
  agents that will edit.

**Related rules / agents:** `rule:git-hygiene`, `lead-template`.

---

## incident:v2.62.7.2-manifest-drift

**Date:** 2026-02
**Project:** PandaOS
**Severity:** P2 — a deploy went out with stale manifest metadata.

**Summary:** A code change updated the application but didn't
update the `version` field in the deployment manifest. The
auto-deploy script applied the new code under the old manifest's
version label, breaking version pinning for subsequent rollbacks.

**Impact:** Production traffic served the new code but observability
(logs, metrics, error tracking) tagged it with the previous version
— making rollback decisions harder for the next 6 hours until the
mismatch was noticed.

**Root cause:** The manifest file wasn't covered by any agent's path
allowlist, so changes there required no validation. The version-
update step was assumed to be done by the human; it was missed.

**Prevented by:**
- `path-allowlist.json` covers manifest files explicitly; an
  edit-before-bumping check fires.
- `deploy-check` skill verifies manifest-vs-source version match.

**Related rules / agents:** `deploy-check` skill.

---

## incident:v2.65.1.1-extract-week

**Date:** 2026-04
**Project:** PandaOS
**Severity:** P1 — a migration applied an `EXTRACT(WEEK FROM ...)`
expression that returned the wrong type, causing every dependent
materialized view to error.

**Summary:** A schema migration computed `EXTRACT(WEEK FROM date - date)`
expecting an interval but got an INTEGER count of days. Downstream
materialized views that joined on the EXTRACT result errored on
the type mismatch. The migration applied "successfully" because
EXTRACT itself didn't error; the error surfaced when the MVs were
queried by user-facing API.

**Impact:** API endpoints returning meal-plan data crashloop'd in
production for 4 minutes until rolled back.

**Root cause:** Insufficient migration testing. The migration was
verified in isolation; not against the dependent MVs.

**Prevented by:**
- `db-migration-validate.sh` runs migrations against a clone of
  staging schema, exercises dependent MVs.
- `rule:postgres-migrations` (project-specific) requires verifying
  dependent objects.

**Related rules / agents:** `rule:postgres-migrations`,
`db-migrations` agent.

---

## incident:v2.65.1.2-dual-runner-conflict

**Date:** 2026-04-26
**Project:** PandaOS
**Severity:** P1 — production crashloop, ~17 minute outage.

**Summary:** Migration 147 contained two compounded bugs: an
`EXTRACT(WEEK FROM date - date)` that returned INTEGER days
instead of interval (see `incident:v2.65.1.1-extract-week`), and an
ownership-mismatch on the created MVs (postgres user via deploy.sh
vs pandaos user via API runner).

**Impact:** API crashloop on TEST + PROD + DEV environments. ~17
minutes recovery. Manual SQL backfill required across 3 envs.

**Root cause:** Two migration runners (deploy.sh as postgres user,
API internal runner as pandaos user) didn't coordinate. Plus
auto-deploy.sh had warn-and-continue on migration failure, masking
the partial application for hours.

**Prevented by:**
- `rule:postgres-migrations` §dual-runner — explicit "one runner per
  migration" rule.
- `db-migration-validate.sh` includes ownership-consistency check.
- `deploy/auto-deploy.sh` §atomic-stamp (v2.65.1.2 hardening) —
  refuses to continue on migration failure.

**Related rules / agents / playbooks:** `rule:postgres-migrations`,
`db-migrations`, playbook `02-code-quality` §migration-safety.

---

## incident:v2.62.43-51-auto-resolve-timing

**Date:** 2026-03 (range)
**Project:** PandaOS
**Severity:** P2 (cumulative; each individual incident P3)

**Summary:** Across 8 minor releases, a sequence of races between
the auto-resolve scheduler and user-initiated resolves caused
double-resolution of the same support ticket. Each individual
incident affected a single ticket; cumulative impact was confused
metrics and one customer escalation.

**Impact:** ~12 tickets affected over 6 weeks. Metrics showed
inflated resolve-rate. One customer reported their ticket "kept
getting marked resolved without being read."

**Root cause:** The auto-resolve job and the user-resolve flow used
different locking. The resolve operation wasn't idempotent at the
database level.

**Prevented by:**
- `rule:postgres-migrations` §locking — explicit guidance on
  idempotent resolve operations.
- `task-complete-dispatch` runs the backend validator on resolve-
  flow changes, which now includes a race-condition probe.

**Related rules / agents:** `rule:postgres-migrations`, `go-api`.

---

## incident:v2.62.57-cwd-bug

**Date:** 2026-04
**Project:** PandaOS
**Severity:** P3 — agent runs from the wrong working directory
produced misleading test results.

**Summary:** An agent invocation was running from a parent shell's
old `cwd` (the user had `cd`'d in another tab; the spawn inherited
the old cwd). The agent ran `go test ./...` against a stale
checkout and reported green; the actual code in the new cwd had
real failures.

**Impact:** A "passing" test run shipped to PR. CI caught the
failure; the round-trip cost 30 minutes.

**Root cause:** The launcher script didn't pin `cwd`; it inherited
from whatever the parent shell happened to have.

**Prevented by:**
- `rule:git-hygiene` (worktree) — agents in worktrees have
  unambiguous cwd.
- `yakos init` standardizes the agent-control directory layout so
  the launch cwd is always `~/agent-control/<project>`.
- The launcher (`yakos team restart`) explicitly cd's before spawn.

**Related rules / agents:** `rule:git-hygiene`.

---

## incident:flutter-tester-hang

**Date:** ongoing — recurring
**Project:** PandaOS
**Severity:** P3 — `flutter test` periodically hangs in
`flutter_tester`, blocking test runs.

**Summary:** `flutter test` invocations sometimes hang indefinitely
in the `flutter_tester` subprocess. The cause is upstream and not
fully characterized; observed correlation with system load.

**Impact:** Test runs that should complete in 2 minutes hang for
arbitrary duration; agents blocked waiting for return.

**Root cause:** Upstream flutter tooling. Tracked via
`https://github.com/flutter/flutter/issues/<some-id>`. Workaround
is the layer-of-our-control fix.

**Prevented by:**
- `test-runner` agent body specifies wrapping `flutter test` in
  `timeout 120` per call.
- `mobile-validate.sh` per-domain validator uses `ct_timeout`
  (via the compat helpers) for the same wrapper.
- On timeout: `pkill -9 -f flutter_tester`, retry with `flutter test
  test/specific-dir/` rather than the full suite.

**Related rules / agents:** `test-runner`, `mobile-validate.sh`.

---

## incident:agent-pre-push-secret-leak

**Date:** 2025-Q4
**Project:** Cross-project (precedes YakOS).
**Severity:** P1 — committed AWS credentials reached GitHub.

**Summary:** An agent edited a config file and pasted an AWS
access key inline (rather than via env var). The change was
committed and pushed before the human noticed. The key was leaked
to public-readable history before the secret was rotated.

**Impact:** AWS detected the leak within 4 hours and rotated the
key automatically; no observed exploitation. Process improvement
followed.

**Root cause:** No PreToolUse-level secret-scan; only post-hoc
review.

**Prevented by:**
- `secret-scan.sh` PreToolUse hook (Batch 2) — refuses writes
  containing the AKIA pattern (and others).
- `rule:secret-handling` — the soft control listing what counts
  as a secret and how to handle them.

**Related rules / agents:** `rule:secret-handling`, `secret-scan.sh`.

---

## incident:v0.2.0-project-agent-runtime-non-discovery

**Date:** 2026-04-28
**Project:** YakOS itself (Phase 8 PandaOS migration probe + v0.2 re-test)
**Severity:** P2 — feature works on disk, doesn't work at runtime
**Status (2026-05-08):** RESOLVED for normal operation via
`yakos start`'s `--agents` JSON injection (v0.3.0). Underlying
runtime limitation in claude 2.1.136 unchanged; the workaround
fully restores `subagent_type` addressability for project agents.

**Summary:** Project-level agents at `<project>/.claude/agents/<role>.md`
are NOT discoverable as `subagent_type` values by the Claude Code
Agent tool, even within a `TeamCreate` context, even with the file
present at the session's `cwd/.claude/agents/` from session start
(retest 2026-04-28 confirmed all three discovery paths fail;
re-confirmed 2026-05-08 against claude 2.1.136). The Agent tool
resolves only the runtime built-ins (`general-purpose`, `Explore`,
`Plan`, `claude-code-guide`, `statusline-setup`).

**Impact:** YakOS's per-project agent design (project-specific
specialists override or extend framework templates) is documentary
only — the on-disk discipline doesn't bind at runtime. Multi-agent
dispatch with project-scoped discipline previously required the
`dispatch-as-project-agent` skill (general-purpose + injected agent
body) as a per-call workaround.

**Root cause:** Claude Code limitation, not a YakOS one. The
runtime's agent-type registry doesn't enumerate
`<cwd>/.claude/agents/` or any `--add-dir`'d directory.

**Resolution (v0.3.0, 2026-05-08):** `yakos start` composes the
`--agents` JSON at launch from `lib/agents/*.md` + project
`.claude/agents/*.md` and passes it via `claude --agents '<json>'`.
Claude Code 2.1.136's `--agents` flag DOES register agents as
addressable `subagent_type` values for the session's lifetime
(empirically verified). 21 agents (11 framework + 10 PandaOS)
registered cleanly in the verification probe. Project agents
override framework on id collision via jq merge precedence.

**Prevented by:**
- **`yakos start`** (v0.3.0) — composes and injects `--agents` JSON
  at session launch. The default path; closes the incident for
  normal use.
- **`skill:dispatch-as-project-agent`** (yakOS v0.2.0.0, still
  shipping) — useful when launching a project agent ad-hoc inside
  a session that wasn't started with `yakos start`, or when
  combining a project-agent body with `Explore`-type discipline.
  The agent-body injection technique remains valid.
- Documentation in `docs/team-shapes.md` "Runtime dispatch" section
  spells out which path applies when.

**Related rules / agents:** `cli/lib/start.sh`,
`cli/lib/agents-compose.sh`,
`lib/skills/dispatch-as-project-agent/SKILL.md`,
`docs/team-shapes.md`.

---

## incident:v0.2.1-task-tools-not-exposed

**Date:** 2026-04-29
**Project:** YakOS itself (Phase 0.5 probe)
**Severity:** P2 — planned BLOCKING upgrade now gated on a runtime
feature

**Summary:** During the in-session Phase 0.5 probe, neither the lead
nor a spawned `general-purpose` teammate has access to `TaskCreate`,
`TaskList`, or `TaskUpdate`. The team-task coordination primitive
documented in `TeamCreate`'s description isn't implemented as exposed
tools in this Claude Code build. The `~/.claude/tasks/<team>/`
directory is created at TeamCreate time but contains only a sentinel
empty `.lock` file because nothing has the tools to populate it.

**Impact:** YakOS's planned v0.2 BLOCKING upgrade for
`task-dependency-gate.sh` and `task-complete-dispatch.sh` (both
REPORT-only since v0.1) is now blocked on a Claude Code runtime
feature, not just a schema confirmation. If `TaskCreate`/`TaskUpdate`
aren't tools, `TaskCompleted` may never fire regardless of stdin
shape.

**Root cause:** Claude Code build state — the docs reference these
tools but they aren't wired in this version. Either build-flag
gated, removed but not documented, or available only in a different
Claude Code variant (Cowork, Anthropic-hosted teams, OpenCode).

**Prevented by:**
- `task-dependency-gate.sh` and `task-complete-dispatch.sh` stay
  REPORT-only until either (a) a Claude Code build with TaskCreate
  arrives, or (b) an alternate trigger mechanism is identified.
- `yakos doctor --probe-runtime` (v0.2.2.0) detects the absence
  programmatically and reports it.
- `docs/architecture/phase-0.5-results.md` documents the finding +
  what was captured + what remains unclear.

**Related rules / agents:** `lib/hooks/task-dependency-gate.sh`,
`lib/hooks/task-complete-dispatch.sh`,
`docs/architecture/phase-0.5-results.md`.

---

## incident:v0.2.1-shutdown-protocol-drift

**Date:** 2026-04-29
**Project:** YakOS itself (Phase 0.5 probe; opportunistic finding)
**Severity:** P3 — operational nuisance; manual workaround exists

**Summary:** The lead-side shutdown protocol documented in
`SendMessage`'s description (`shutdown_request` →
`shutdown_response` with `request_id`) doesn't match what the
runtime actually emits. A spawned teammate replied with
`{"type":"shutdown_approved","requestId":"...","paneId":"in-process","backendType":"in-process"}`
— field-name drift (`shutdown_approved` vs `shutdown_response`,
`requestId` vs `request_id`). The teammate then continued to be
considered "active" by the team-state tracker; three subsequent
`TeamDelete` calls (8s, 20s, 30s waits) all returned "Cannot
cleanup team with 1 active member(s)". Force-cleanup via
`rm -rf ~/.claude/teams/<team> ~/.claude/tasks/<team>` works.

**Impact:** Teams created in a session can't always be cleaned up
via `TeamDelete`. Each stuck team leaks a small amount of
filesystem state into `~/.claude/teams/` until force-cleaned.

**Root cause:** Schema drift between docs and runtime. The
`tmuxPaneId: "in-process"` backend may not honor process termination
at all — there's no separate process to kill; the "teammate" is a
record that the harness considers alive until something it doesn't
surface changes its state.

**Prevented by:**
- `team-lifecycle.sh` (REPORT-only in v0.1; v0.3 candidate to
  surface stuck-team detection).
- Operator runbook: when `TeamDelete` blocks, force-cleanup via
  `rm -rf ~/.claude/teams/<team> ~/.claude/tasks/<team>`.

**Related rules / agents:** `lib/hooks/team-lifecycle.sh`,
`docs/architecture/phase-0.5-results.md`.

---

## incident:librarian-self-congratulation-2026-05-22

**Date:** 2026-05-22 (design-time constraint, not a post-incident
record — pre-emptive entry recording a known failure mode from
peer framework before yakOS hits it)
**Project:** YakOS framework (Plan 3 framework-internal capabilities)
**Severity:** P2 — would degrade skill library quality over weeks
to months; cosmetic in the short term

**Summary:** Hermes Agent (Nous Research) ships a self-authored
`SKILL.md` Curator process. The community has documented that
the Curator is self-congratulatory: it proposes too many skills,
generalizes too eagerly, and praises its own observations. The
result over time is a skill library polluted with shallow,
paraphrastic skills — `clean-up-files`, `cleanup-files`,
`file-cleaner`, all of which describe the agent's normal
behavior, not reusable disciplines.

**Impact:** A self-promoting librarian-equivalent in yakOS would
degrade the framework's skill library (`lib/skills/`) and project
skill libraries (`<project>/.claude/skills/`) with low-value
entries that nonetheless load into session context. The
cumulative drag on session context is the load-bearing harm.

**Root cause (in peer framework):** The Curator agent's prompt
treats every observed pattern as worthy of capture. No
anti-promotion bias. No requirement to cite ≥N specific evidence
points. No human-approval gate before promotion.

**Prevented by:**
- `lib/agents/librarian.md` — yakOS's analogous agent, with
  explicit anti-self-congratulatory discipline in the
  Personality section
- Manual `yakos skill promote <slug>` (Plan 3 M2) — required
  operator action before any candidate becomes a real skill
- `lib/rules/retrospective-discipline.md` — explicit ban on
  the lead promoting skills directly
- `~/.yakos-state/skill-graveyard.ndjson` — repeat-rejection
  tracking; re-proposed candidates produce a warning
- `yakos skill stats` — promotion-to-proposal ratio surfaces
  pathological librarian behavior

**Related rules / agents / playbooks:**
- `lib/agents/librarian.md` (new in Plan 3 M1)
- `lib/rules/retrospective-discipline.md` (new in Plan 3 M1)
- `lib/hooks/cycle-counter.sh` (new in Plan 3 M1)
- `framework-internal-plan.md` §4 (Capability B — self-learning
  skill generation)

**References (external):**
- [Hermes Agent skill-generation post](https://aiskill.market/blog/self-improving-agents-hermes-writes-skills) —
  documents the failure mode this entry exists to prevent
- [Voyager paper §4](https://arxiv.org/abs/2305.16291) —
  contrasts: Voyager's self-verifier as a discipline that yakOS
  borrows for the librarian's anti-spam stance

---

## incident:feedback-citation-orphans-2026-04-28

**Date:** 2026-04-28 (panda-os3.0 backfill discovery; folded into
yakOS as a cross-project design constraint 2026-05-22)
**Project:** panda-os3.0 (source); yakOS framework (lessons)
**Severity:** P2 — audit-trail completeness; not user-visible

**Summary:** At ~280 feedback records, two distinct
audit-trail failure modes were discovered in panda-os3.0's
feedback citation pattern:

1. **Cite-without-data**: CHANGELOG entries citing
   `Feedback #<8hex>` where no feedback row existed with that
   prefix. Causes observed: typo'd hex; feedback record deleted
   after citation written; citation written before the feedback
   row was committed.
2. **Resolved-without-citation**: feedback rows with
   `status = 'resolved'` but no CHANGELOG entry citing the row's
   8-hex prefix. Cause: developer marked the row resolved but
   skipped the changelog update.

Both modes silently degrade the user-facing audit trail:
users who reported issues didn't see their reports in the
changelog when fixed; the framework's "feedback drives
changelog" loop was broken.

**Impact:** Loss of the closure-of-loop social contract with
users who took the time to submit feedback. No technical
downstream consequence, but a meaningful trust signal eroded
over months of accumulating orphans.

**Root cause:** Citation pattern was a human-discipline-only
convention. No git-layer enforcement existed at the time;
`lib/hooks/per-domain/changelog-validate.sh` was added v2.91.1
(panda numbering) after this incident as a partial fix
(enforces citation presence at the git boundary; doesn't
catch cite-without-data orphans).

**Prevented by:**
- `lib/hooks/per-domain/changelog-validate.sh` (already shipped
  in yakOS) — enforces "every BE-only change cites a feedback
  ID or `[no-feedback]`" at the git layer
- `lib/rules/feedback-discipline.md` (this milestone) — codifies
  the closure-of-loop discipline for projects that opt into
  Standard 4
- `lib/playbooks/02-code-quality.md` §Feedback wiring (this
  milestone) — release-audit catches both orphan modes via DB
  reconciliation
- panda-os3.0 `deploy/sql/backfill-orphan-feedback-resolutions.sql`
  — the SQL backfill that initially discovered the at-scale
  failure mode

**Related rules / agents / playbooks:**
- `lib/rules/feedback-discipline.md`
- `lib/rules/changelog-ui-discipline.md` (composes — citation
  link in UI)
- `lib/hooks/per-domain/changelog-validate.sh`
- `lib/playbooks/02-code-quality.md` §Feedback wiring
- `cross-project-standards-plan.md` §6

---

## Adding new incidents

When an incident concludes:

1. Pick a stable ID. Format: `<release-or-date>-<short-slug>`. The
   slug is kebab-case; the prefix is the release version (when the
   incident was detected) or a date for non-release-aligned ones.
2. Write the entry under the schema above. Keep it under ~30 lines
   for readability.
3. Reference the incident from any rules, agents, or hooks that
   defend against its recurrence.
4. Add the incident to the `references:` field of any newly-touched
   agents.

## Not in v0.1

- **Auto-extraction from postmortems.** v0.1 is hand-written entries.
- **Searchable index.** `git grep "incident:"` is the v0.1 search.
  v0.2 may add a CLI surface.
- **Linkage with external trackers.** No bidirectional sync with
  Jira/Linear/etc. in v0.1.
