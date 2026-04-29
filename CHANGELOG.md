# Changelog

All notable changes to YakOS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.2.0] — 2026-04-29

### Added — runtime probe + audit polish + framework-side CI

Closing the "What else can improve yakOS?" survey. 10 items shipped;
8 explicitly deferred with reasons. v0.2.x now has CI, a runtime
feature probe, an inbox-snapshot audit path, a fixture-driven
classification test, and a substantively refreshed documentation
surface.

**Closed:**

- **`yakos doctor --probe-runtime`** — reports filesystem-side state
  of Claude Code Agent Teams, last-known in-session tool availability
  from `~/.yakos-state/runtime-probe.json`, and the exact prompt to
  refresh that state in a Claude Code session. Closes the "how would
  I know when TaskCreate becomes available?" question.
- **`~/.yakos` path collision fixed.** v0.2.0.0 placed the gate-log
  at `~/.yakos/gate-log.ndjson`, but `~/.yakos` is the YAKOS_ROOT
  pointer FILE — writes were silently failing. Relocated audit state
  to `~/.yakos-state/` (gate-log + runtime-probe). All references
  updated (gate hook, git-hooks driver, doctor, STYLE.md, README,
  hook README).
- **`mailbox-mirror.sh` upgrade** — `session-end-check.sh` now
  snapshots all team inbox files
  (`~/.claude/teams/<team>/inboxes/<recipient>.json`) into
  `work/current/team-inboxes/` at session end. Captures peer-to-peer
  DMs that don't transit lead context (uses Phase 0.5 finding).
- **Test-fixture runner** at
  [`tests/run-version-gate-fixtures.sh`](tests/run-version-gate-fixtures.sh).
  Sources `classify_file` and `is_public_surface` from the gate hook
  (via awk extraction) and asserts each fixture path matches its
  filename-encoded expected classification. **Caught a real bug**:
  `classify_file`'s `*.md` doc-paths case was firing BEFORE the
  `lib/agents/*` etc. case, so all framework markdown files were
  classifying as DOC_ONLY when they should've been PATCH_REFINEMENT.
  Reordered the case statement; 34/34 fixtures now pass.
- **`yakos update` and `yakos archive`** — confirmed not stubs
  (both implemented since earlier batches). Help text updated to
  remove stale "Stub commands" labels; commands now grouped by
  function (lifecycle / project / release).
- **`INCIDENT-CATALOG.md` refreshed** with three new v0.2.x entries:
  `incident:v0.2.0-project-agent-runtime-non-discovery`,
  `incident:v0.2.1-task-tools-not-exposed`,
  `incident:v0.2.1-shutdown-protocol-drift`.
- **README modernization** — Status section reflects v0.2.2.0
  shipped capabilities (12 agents, 16 skills, 8 hooks + git
  pre-push gate, posture clarification). New "Releasing —
  version-bump + pre-push gate" section. "Not in v0.1" → "Not in
  v0.2.x" with current deferral reasons.
- **`COOKBOOK.md` refresh** — four new patterns added (Pattern 6:
  dispatching with project-agent discipline, Pattern 7: hash-anchored
  edits, Pattern 8: iterate-until verifier, Pattern 9: releasing
  with version-bump + pre-push gate).
- **[`docs/adopting.md`](docs/adopting.md)** — new guide for
  adopting yakOS into an existing project (distinct from
  MIGRATING.md which targets migration from a tmux + dispatch-CLI
  setup). Five-command minimum-friction path; documents what lands
  in the project, what to add over time, common adoption questions.
- **CI pipeline** at `.github/workflows/ci.yml`. Five jobs:
  shellcheck (lints all .sh under cli/lib/hooks/lib/skills/tests),
  yakos validate (asserts 0 errors), gate-fixture suite (34/34
  pass), hook-fixtures (existing test runner), install-flow smoke
  (init/doctor/validate/uninstall against a throwaway repo).

**Stays deferred (with reasons):**

- 8 v0.2 cross-cutting agents (architect, incident-responder,
  release-manager, devops-infra, log-analyst, performance-engineer,
  privacy-reviewer, accessibility-reviewer/ux-reviewer) — need
  real-use signal per `docs/v0.2-notes.md`. Add as concrete demand
  surfaces.
- Real-use examples beyond `examples/tiny-go-api/` — need stack
  input (Python? Next.js+Postgres? CLI tool? Multiple?).
- Composable middleware-style hooks — substantial refactor; defer
  until next hook addition forces the issue.
- Per-session state store — design pass needed (file-based JSON,
  SQLite, per-process? affects scope of stateful hooks downstream).
- Multi-model category routing — design-only; current
  `model: opus|sonnet|haiku` agent-frontmatter primitive is
  sufficient until a routing-driven workload appears.
- Skill-embedded MCPs — requires MCP infrastructure yakOS doesn't
  have yet.
- npm package distribution + i18n — strategic, gated on
  going-public decision.
- Hashed-edit runtime PreToolUse hook — design pass on stateless vs
  stateful staleness check (gating reason corrected from Phase 0.5
  in v0.2.1.0).
- `yakos iterate-until` CLI subcommand — wait for procedural shape
  to settle via real usage.

## [0.2.1.0] — 2026-04-29

### Polished — deferred items from v0.2.0.0 closed; Phase 0.5 probe run

Closing the v0.2.0.0 status report's "Known shortfalls / follow-ups
for v0.3+" list. Five of six items addressed; two stay deferred with
updated reasons.

**Closed:**

- **Test fixtures populated** under
  [`tests/fixtures/version/change-classification/`](tests/fixtures/version/change-classification/)
  — six fixture files (one per classification tier) plus a README
  explaining the convention. Documented expected classifications;
  ready for a v0.3+ runner script.
- **`version-bump` skill awkward on major-release consolidation —
  fixed.** The skill now detects whether `[Unreleased]` has
  substantive content. If yes: PROMOTES it to the new versioned
  header (rename) and adds a fresh empty `[Unreleased]` above. If
  no (empty section): keeps the original insert-under-[Unreleased]
  path. `--message` is ignored on promotion (the body already
  describes the work).
- **MAJOR_BREAKING classification rules tightened.** The gate now
  detects deletions (via `git diff --diff-filter=D`) and classifies
  deletion of public-surface files as MAJOR_BREAKING. Public surface
  expanded: framework agents/skills/rules/playbooks, top-level
  Claude-Code hooks, per-domain validators, hook contract libs
  (`lib/hooks/lib/hook-input.sh`, `hook-output.sh`), CLI subcommands
  + entrypoint, schema files, and settings templates. Modifications
  to hook contract libs or settings templates also classify as
  MAJOR_BREAKING (path-only can't tell if a specific change is
  breaking; conservative wins).
- **Phase 0.5 probe — partial in-session run.** A bigger finding
  than the original probe expected:
  [`docs/architecture/phase-0.5-results.md`](docs/architecture/phase-0.5-results.md)
  documents that **TaskCreate / TaskList / TaskUpdate aren't
  exposed as tools** in this Claude Code build (verified for both
  lead and a spawned `general-purpose` teammate). The
  `~/.claude/tasks/<team>/` directory only contains a sentinel
  `.lock` file. Bonus findings captured: full
  `~/.claude/teams/<team>/config.json` member schema (including
  `color`, `tmuxPaneId`, `backendType`, verbatim `prompt` capture);
  `~/.claude/teams/<team>/inboxes/<recipient>.json` mailbox file
  format; shutdown protocol field-name drift
  (`shutdown_approved` vs documented `shutdown_response`); force-
  cleanup via `rm -rf` works when `TeamDelete` blocks on stuck
  members. v0.2-notes.md updated to reflect that the BLOCKING
  upgrade of `task-dependency-gate.sh` and `task-complete-dispatch.sh`
  is now gated on a Claude Code runtime feature, not a schema
  confirmation.
- **`hashed-edit` SKILL.md "Future enforcement" reasoning corrected.**
  The auto-enforcement hook is gated on design (what counts as
  stale, given Edit's exact-match semantics?), not on Phase 0.5.
  Edit tool stdin shape was already known via the existing
  `hi_old_string` / `hi_new_string` / `hi_file_path` primitives.

**Stays deferred (reasons updated):**

- **Hashed-edit runtime PreToolUse hook** — needs design pass on
  whether the staleness check is stateless (compute hash from
  `old_string` only, redundant with Edit's exact-match) or stateful
  (track per-session "agent last read this file at hash X", refuse
  Edit if file changed since). Stateful version requires a
  per-session state store yakOS doesn't have yet.
- **`yakos iterate-until` CLI subcommand** — keep deferred until
  the procedural skill shape settles via real usage.

**Not done (operator action):**

- **shellcheck on the gate hook** — `shellcheck` not installed
  system-wide; would require `brew install shellcheck` (system-level
  change deferred to operator decision).
- **Operator-driven Phase 0.5 probe** still useful for the
  remaining open question: does `TaskCompleted` ever fire on this
  Claude Code build? Run `tests/manual/phase-0.5-probe/` from a
  fresh session if/when a build with `TaskCreate`/`TaskUpdate`
  becomes available.

## [0.2.0.0] — 2026-04-29

### Added — pre-push version gate (Part B of the v0.2.0 build)

Closes the version-bump loop started in v0.1.4.0 (Part A — the
`version-bump` skill itself). Substantive code changes can no longer
be pushed without a corresponding VERSION change, unless the operator
explicitly overrides via `YAKOS_GATE_DISABLE=1` (logged) or
`git push --no-verify` (native git bypass).

- [`lib/hooks/git/pre-push-version-gate.sh`](lib/hooks/git/pre-push-version-gate.sh)
  — the gate. Classifies changed files since the last `v*.*.*` tag
  (DOC_ONLY / PATCH_REFINEMENT / PATCH_REFACTOR / MINOR_ADDITIVE /
  MAJOR_BREAKING / DEFAULT_PATCH), determines required bump tier from
  the highest classification, and refuses the push if VERSION wasn't
  bumped accordingly. Honors hotfix-only bumps as an emergency
  bypass. Every decision (allow/refuse/override/error) appends one
  NDJSON line to `~/.yakos/gate-log.ndjson` for audit.
- [`cli/lib/git-hooks.sh`](cli/lib/git-hooks.sh) — `yakos git-hooks
  {install|uninstall|status}`. Install copies the gate to
  `<repo>/.git/hooks/pre-push` with a `.framework-hash` sibling for
  drift detection. Uninstall refuses to remove non-YakOS hooks.
  Status reports installation + drift state.
- [`cli/lib/init.sh`](cli/lib/init.sh) — new `--with-gate` flag
  installs the gate as part of `yakos init <name> --project <path>`.
  Skips `lib/hooks/git/` from the framework hook-copy loop (those
  are project-side git hooks, not Claude Code hooks; they belong in
  `<repo>/.git/hooks/`, not `<project>/scripts/hooks/`).
- [`cli/lib/doctor.sh`](cli/lib/doctor.sh) — when run with a
  `<project-path>`, additionally reports pre-push gate status:
  installed/not-installed, YakOS-owned/foreign, current/drifted.
- [`lib/hooks/git/README.md`](lib/hooks/git/README.md) — documents
  the contract, install path, override mechanism, and audit-log
  location.
- [`STYLE.md`](STYLE.md) — new "§8. Versioning discipline" section
  documents bump semantics, the gate, the override mechanism, and
  the hotfix-tier reservation.

Smoke-tested end-to-end: doc-only allows, code-without-bump refuses
with classification + remediation steps, hotfix-only bump allows,
override env var works and is logged, NDJSON audit trail intact.

### Added — capability patterns absorbed from oh-my-openagent

After surveying [oh-my-openagent](https://github.com/code-yeongyu/oh-my-openagent)
(an autonomous-first multi-model orchestration harness for OpenCode),
three capability gaps were identified and closed. The borrowing is
deliberate: yakOS's human-in-loop posture stays; the new capabilities
preserve the audit trail and approval gates.

- [`lib/skills/hashed-edit/`](lib/skills/hashed-edit/SKILL.md) —
  hash-anchored line edits. Adapted from OMA's `hashline_edit` pattern
  (which reports reducing stale-line edit failures from ~93% → 32% on
  Grok Code). Two helper scripts:
  - `scripts/read-with-hashes.sh` — outputs `<lineno>#<hash>|<content>`
    per line (4-char hex digest from `cksum % 65536`).
  - `scripts/edit-by-hash.sh` — applies a single-line edit IFF the
    current line's hash matches the anchor; refuses with a diff and
    exit code 5 on mismatch.
  The runtime enforcement (PreToolUse hook intercepting all `Edit`
  calls) is deferred to v0.3 pending the Phase 0.5 probe's `Edit`
  tool stdin shape confirmation.

- [`lib/skills/iterate-until/`](lib/skills/iterate-until/SKILL.md) —
  formal "loop work-then-verify until done" pattern. yakOS-flavored
  Ralph Loop: the verifier is **never** the agent's own judgement —
  it's a test command, hook exit code, `yakos validate` result, or
  human-readable check. Hard iteration cap (default 3); each
  iteration's diff + verifier output logged to
  `work/current/iterations/<task-id>/<i>.md`; on cap reached,
  escalation to the human is mandatory.

- [`PHILOSOPHY.md`](PHILOSOPHY.md) — new "Human-in-the-loop by design"
  section makes the posture explicit. yakOS is built for
  production-touching work in audit-sensitive domains; it is **not**
  trying to be autonomous-first. Surfaced as the single most important
  thing to understand about yakOS relative to other agentic frameworks.
  Architectural consequences (plan-approval gates, audit-trail
  richness, soft+hard control pairing, lead-supervises-not-executes)
  spelled out.

What did NOT land in this batch (deferred with stated reasons):

- **Multi-model category routing** — design-only for v0.3. yakOS's
  current `model: opus|sonnet|haiku` agent-frontmatter primitive is
  the seed; OMA's category-based routing (`ultrabrain`, `quick`,
  `deep`, etc.) is the mature form. Not implementing without a clear
  driver.
- **Composable middleware-style hooks** — defer until next hook addition.
- **Skill-embedded MCPs** — requires MCP infrastructure yakOS doesn't
  have yet.
- **npm package distribution** — only relevant if yakOS goes public.
- **Auto-update CLI command** — separate concern; the existing
  `update` stub becomes its own ticket.

[`lib/skills/README.md`](lib/skills/README.md) inventory backfilled
to include `local-llm`, `dispatch-as-project-agent`, `version-bump`,
plus the two new skills.

### Added — release-audit scaffolding (`lib/skills/release-audit/`)

Copied the reusable building blocks of the PandaOS release-audit skill
into the framework. Scope per the design constraint already documented
in `lib/skills/README.md`: the **orchestrator (`SKILL.md`) stays
per-project**; the framework hosts only the templates and the auditor
agent definitions.

What landed:

- `lib/skills/release-audit/templates/` — 4 report templates: `scope.md`,
  `domain-report.md`, `executive-summary.md`, `dispositions.md`. Generic
  `{{version}}` / `{{operator}}` placeholders only; no project specifics.
- `lib/skills/release-audit/agents/` — 7 auditor agent definitions:
  `lead-auditor`, `security-auditor`, `code-quality-auditor`,
  `uiux-auditor`, `docs-auditor`, `performance-auditor`,
  `regulated-data-auditor` (the source PandaOS `hipaa-auditor` was
  renamed to match the framework's `lib/playbooks/06-regulated-data.md`
  rename and rewritten to reference HIPAA / GDPR / CCPA / SOC 2 /
  contract-bound data rather than HIPAA-only).
- Each auditor agent's `playbook:` frontmatter field points at
  `lib/playbooks/<NN>-<domain>.md` directly — no per-project copying of
  the playbooks needed.
- `lib/skills/release-audit/README.md` documents the consumer pattern
  and the deliberate omission of a `SKILL.md` (this directory is
  scaffolding; `yakos validate` should treat it as an exception or use
  the README presence as the marker).
- `lib/skills/README.md` inventory updated with a `release-audit/`
  scaffolding row + preamble clarifying the framework/project split.

What did NOT land:

- The orchestrator `SKILL.md` itself stays in PandaOS at
  `<project>/.claude/skills/pandaos-release-audit/SKILL.md`. It still
  references the project-local `references/domains/*` and
  `agents/*` paths; migrating PandaOS to consume the framework
  scaffolding is a separate change.
- The 6 domain playbooks under `references/domains/` in the source —
  these have drifted from `lib/playbooks/` in unknown direction.
  Reconciliation is a separate Batch.

### Changed — VERSION file format migrated to four-part semver

`VERSION` migrated from `0.1.4` (three-part `major.minor.patch`) to
`0.1.4.0` (four-part `major.minor.patch.hotfix`). The fourth tier
(`hotfix`) is reserved for emergency fixes to deployed versions
outside normal release flow. The `version-bump` skill (this same
release) encodes the bump semantics; the pre-push gate enforces them.

This is a format change only — `0.1.4.0` is the same release as
`0.1.4`. Existing `v0.1.4` tag preserved as-is; future tags use
the four-part form (`v0.2.0.0` next).

### Added — runtime-dispatch skill + clarified team-shapes

Confirmed via re-probe (within `TeamCreate` context) that project-level
`.claude/agents/<role>.md` files remain non-discoverable as
`subagent_type` values in the current Claude Code runtime — the team
config accepts arbitrary `agentType` strings, but
`Agent({subagent_type: "<project-role>"})` returns "not found"
regardless of team membership.

- [`lib/skills/dispatch-as-project-agent/SKILL.md`](lib/skills/dispatch-as-project-agent/SKILL.md)
  — workable dispatch pattern: spawn a `general-purpose` Agent with
  the project agent body (and any `extends:` parent) injected into
  the prompt. Documents what the spawned agent loses (hook coverage,
  TaskList integration, mailbox routing) and the lead's manual-pass
  responsibilities (verify the diff, run per-domain validators
  manually, mirror peer decisions to `decisions.md`).
- [`docs/team-shapes.md`](docs/team-shapes.md) — new
  "Runtime dispatch in v0.1" section explaining what works
  (`TeamCreate`, `TaskList`, path-scoped rules, the dispatch skill)
  and what doesn't (project `subagent_type` resolution, hook firing
  on injected dispatch). Both team shape catalogs in this doc point
  at the dispatch skill.

When Claude Code adds project-agent discovery, the skill becomes
unnecessary; the on-disk discipline already binds at runtime.

## [0.1.4] — 2026-04-28

### Added — stack-specialist agent templates

Five generic `extends:`-able agent templates derived by generalizing
PandaOS's project agents during the Phase 8 migration. Each carries
the discipline of the role with no stack names or specific file paths
— projects deploy a thin `extends:` wrapper carrying only the
project-specific delta (stack, paths, incident lore).

- [`lib/agents/backend.md`](lib/agents/backend.md) — server-side
  application code; reads db-contracts, writes api-contracts,
  enforces DTO-at-the-boundary and audit-log-on-mutation.
- [`lib/agents/frontend.md`](lib/agents/frontend.md) — web UI;
  consumes api-contracts, types-from-source-of-truth, doesn't add to
  tracked lint baselines.
- [`lib/agents/mobile.md`](lib/agents/mobile.md) — iOS/Android
  client; generated API client, native-platform usage-description
  defense, tap-target floors.
- [`lib/agents/database.md`](lib/agents/database.md) — schema,
  sequential migrations, repository layer; writes db-contracts;
  parameterized queries only; cascade-delete on user-data FKs.
- [`lib/agents/maintainer.md`](lib/agents/maintainer.md) — routine
  hygiene (dep bumps, lint baseline drains, dead-code, version +
  changelog parity); never touches business logic.

These complement the v0.2 cross-cutting roster (`architect`,
`incident-responder`, `release-manager`, etc.) — they fill in
stack-shaped specialists where the v0.2 roster covers cross-cutting
roles. See [`docs/v0.2-notes.md`](docs/v0.2-notes.md) for the
distinction.

[`docs/team-shapes.md`](docs/team-shapes.md) updated: the existing
"buildable from v0.1" team shapes now reference the framework
templates directly instead of "(project-specific, e.g. `go-api`)".
A new "Stack-specialist templates" subsection introduces the
`extends:` deployment pattern.

[`lib/agents/README.md`](lib/agents/README.md) inventory now
distinguishes "Cross-cutting roles" from "Stack-specialist templates".

## [0.1.3] — 2026-04-28

### Added — Phase 0.5 probe deliverables

Test infrastructure for the Phase 0.5 probe (operator-driven; needed
to flip the two REPORT-only hooks to BLOCKING in v0.2). Doesn't
change runtime behavior — adds artifacts under `tests/manual/`.

- `tests/manual/phase-0.5-probe/probe-taskcompleted.sh` —
  TaskCompleted matcher; captures full stdin + env per fire.
- `tests/manual/phase-0.5-probe/probe-taskcreated.sh` —
  TaskCreated matcher; same shape.
- `tests/manual/phase-0.5-probe/probe-allpretool.sh` — wildcard
  PreToolUse capture; sanity check for task-related tool calls.
- `tests/manual/phase-0.5-probe/settings-fragment.json` —
  `hooks` block to merge into a probe project's `.claude/settings.json`.
- `tests/manual/phase-0.5-probe/README.md` — operator playbook with
  a step-by-step prompt sequence for the live session, plus the
  inspection checklist for `~/.claude/tasks/<team>/`.
- `docs/architecture/phase-0.5-results.md` — results-doc template
  mirroring Phase 1.7's shape; filled in after probe runs.

`docs/v0.2-notes.md` updated to reference the probe location and
mark it "deliverables ready, not yet run."

The probe answers:

1. The exact stdin shape of `TaskCompleted` hooks (is `agent_type`
   present? how is the task identified? is `blockedBy` in stdin?).
2. The format of `~/.claude/tasks/<team>/` files (per-task or
   single-file? schema? state-transition representation?).

Both unlock the BLOCKING upgrade in v0.2.

## [0.1.2] — 2026-04-28

### Fixed (documentation drift)

Surfaced by a v0.1.1 cold-read familiarization session, where a
fresh lead reading the project end-to-end caught four documents
still claiming `lib/playbooks/` was empty after Batch 5.7 had
populated it.

- `README.md` "Not in v0.1": removed the "lib/playbooks/ is empty"
  bullet (now wrong); replaced with a PandaOS-migration roadmap
  bullet that's actually still deferred.
- `PHILOSOPHY.md` "Not in v0.1": rewrote the playbooks bullet to
  acknowledge v0.1.1 ships the 6 framework playbooks; the deferred
  work is playbooks for the v0.2 agent roster, not the framework
  baseline.
- `lib/agents/README.md`: rewrote the "Standards" bullet about
  playbook references — now describes the validate.sh ERROR-level
  check on broken `playbook:` refs (added in Batch 5.7) rather than
  saying playbooks aren't shipped.
- `docs/team-shapes.md`: release-prep team's `release-auditor` note
  no longer says "once Phase 1.5 §4's playbooks are populated in
  v0.2"; now points at `lib/playbooks/` directly.
- `docs/architecture/phase-1.5-architecture.md`: inline note added
  next to the `06-hipaa-phi.md` directory listing pointing at the
  Batch 5.7 rename to `06-regulated-data.md`. The spec line itself
  preserved as a frozen historical record (the rename is documented
  in BATCH-5.7-STATUS.md and the changelog).

### Added

- `docs/v0.2-notes.md` — holding place for v0.2 planning
  observations. Initial entries:
    - **G1: Lead supervision has no hard counterpart.** The
      "don't do specialist work" rule is purely soft; no hook
      detects when the lead drifts into doing specialist work
      itself. Possible v0.2: SessionEnd-time check comparing
      lead-vs-teammate edit counts.
    - **G2: `yakos validate` doesn't detect documentation drift.**
      Demonstrated by the four bullets above slipping past every
      validate run since Batch 5.7. Possible v0.2: a
      `yakos validate --docs` mode with a maintained list of
      stale-phrase patterns.
    - **G3: Inventory counts include INDEX.md / README.md.**
      Trivial cosmetic; one-line fix in `count_dir_files()`.
    - Phase 0.5 probe shape (needed to flip REPORT-only hooks to
      BLOCKING in v0.2).
    - The v0.2 agent roster from `docs/team-shapes.md` with shipping
      requirements per agent.

## [0.1.1] — 2026-04-28

### Fixed

- `yakos install` "Next steps" output no longer references
  `Batch 1B; not yet implemented` — a stale Batch 1A stub message
  that wasn't refreshed when `init` shipped. The output now shows
  the real `yakos init <name> --project <path>` invocation.

### Added — Batch 5.7 (framework playbooks)

- 6 framework playbooks under `lib/playbooks/` (1,445 lines total),
  closing the Phase 1.5 §4 gap Batch 3 flagged. Ported from
  PandaOS audit work with light cleanup on 01–05 and full
  generalization on 06:
    - `01-security.md` (248 lines) — secret scanning, SAST,
      dependency vulns, DAST, OpenAPI fuzzing, OWASP API Top 10
      walkthrough.
    - `02-code-quality.md` (172 lines) — coverage thresholds,
      complexity, flake detection, mutation testing, dead-code
      checks. Multi-language tool examples.
    - `03-ui-ux-a11y.md` (211 lines) — Lighthouse / axe / pa11y /
      Playwright; WCAG 2.2 AA target; keyboard nav, screen reader,
      forms, responsive sweep.
    - `04-docs-architecture.md` (226 lines) — OpenAPI generation,
      C4 levels 1-3, ADRs, runbooks, link checking.
    - `05-performance.md` (257 lines) — k6 load testing, pgbadger,
      pprof / clinic, microbenchmarks, SLO baseline table.
    - `06-regulated-data.md` (331 lines) — generalized from
      HIPAA-specific to multi-framework: HIPAA, GDPR, CCPA/CPRA,
      SOC 2, engagement-data. Three-control-family structure
      preserved.
- 4 agent reference fields wired:
  `security-reviewer` → `playbook:01-security`,
  `code-reviewer` and `test-runner` → `playbook:02-code-quality`,
  `doc-writer` → `playbook:04-docs-architecture`.
- `cli/lib/validate.sh` `check_playbook_references()` —
  **broken `playbook:` references are ERROR-level**, not WARN.
  Exit 1. The framework's first ERROR-tier standards check.

### Added — Batch 5.5 (local-model integration templates)

- `lib/skills/local-llm/SKILL.md` (108 lines, in 80–180 budget) —
  the safe-handoff pattern for local model use. Documents the
  output-trust-model warning, when-to-use vs when-NOT-to-use, the
  artifact-then-review pattern.
- `lib/skills/local-llm/scripts/ollama-prompt.sh` — reference
  implementation. Required `--template` / `--input` / `--output`;
  optional `--model` / `--max-bytes` / `--force`. Streams via
  mktemp + trap. Generates sidecar metadata via `jq --arg`. Exits
  0 / 2 / 3 / 4 per STYLE.md exit-code conventions. Validates
  user inputs before checking ollama presence so bad-args errors
  surface independently of "install ollama."
- 4 prompt templates: `summarize`, `classify`, `extract`,
  `sanity-check`. Generic; project-specific overrides go in
  `<project>/.claude/skills/local-llm/templates/`.
- `docs/examples/local-model-routing.md` — worked release-summary
  example end-to-end.
- `cli/lib/doctor.sh` extended with optional-tooling detection:
  ollama / lms / llama-server (presence + version);
  OPENAI_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY (presence
  ONLY — values never printed; verified via sentinel test).
- `COOKBOOK.md` "Pattern 5: Using local models safely" — output-
  trust model, four sub-patterns, data-boundary policy.
- `COMPATIBILITY.md` "Optional integrations" section.
- `PHILOSOPHY.md` "Local models are workers, not the orchestrator"
  + "Data boundary" sections.

### Added — Batch 5 (tiny-go-api example)

- `examples/tiny-go-api/` — minimal Go HTTP server demonstrating
  YakOS end-to-end. Single endpoint (GET /hello), two test cases,
  no external deps. Agents prefixed `tiny-` per spec; rules
  path-scoped to `cmd/**`; 17 hook copies with `.framework-hash`
  siblings under `scripts/hooks/`.
- Spec deviation: `cmd/server/` instead of `api/` (Go build conflict
  with directory of the same name). Documented in BATCH-5-STATUS.md.

### Added — Batch 4 (documentation)

- `README.md` (expanded) — quickstart, install, bootstrap, common
  workflows, full doc map.
- `PHILOSOPHY.md` (expanded; Batch 2.75 stub preserved verbatim) —
  full hard/soft taxonomy, trust-but-verify, flat-not-hierarchical,
  specialists-narrow, prefer-writing-over-reading, orchestration
  shapes (new framing).
- `CUSTOMIZING.md` — one worked example each for adding project
  specialists, hooks, rules, skills.
- `MIGRATING.md` — porting from tmux + dispatch-CLI setups; references
  Phase 1.5 §21 migration map.
- `COOKBOOK.md` — four common-workflow recipes (feature touching DB/
  API/UI, parallel review team, bug investigation with adversarial
  agents, releasing a version).
- `INCIDENT-CATALOG.md` — durable IDed incident records: v2.49.0
  force-push, v2.62.4 worktree-collision, v2.62.7.2 manifest-drift,
  v2.65.1.1 EXTRACT-week, v2.65.1.2 dual-runner-conflict,
  v2.62.43-51 auto-resolve-timing, v2.62.57 cwd-bug, flutter-tester-
  hang (recurring), agent-pre-push-secret-leak.
- `docs/team-shapes.md` — recommended team compositions per project
  type and lifecycle stage. Names six v0.2 candidate agents
  (architect, incident-responder, log-analyst, devops-infra,
  performance-engineer, privacy-reviewer, accessibility-reviewer,
  ux-reviewer). Referenced from COOKBOOK.md and PHILOSOPHY.md
  Orchestration shapes section.
- `COMPATIBILITY.md` — supported environments, required and optional
  tools, known caveats.

### Added — Batch 3 (generic agents + skills + cross-cutting rules)

- 7 generic agents in `lib/agents/`: `lead-template`, `planner`,
  `test-runner`, `code-reviewer`, `security-reviewer`, `troubleshooter`,
  `doc-writer`. All within the 80–140 line budget. Each answers the
  five specialist questions per
  `docs/engineering-standards.md §9`.
- 11 skills in `lib/skills/`: `pre-commit`, `test-suite`,
  `session-recovery`, `project-init`, `gather-feedback`,
  `deploy-check`, `verify-agent-work`, `split-mega-task`,
  `contract-handoff`, `phase-complete`, `dependency-update`. All
  within the 80–180 line budget.
- 4 cross-cutting rules in `lib/rules/`: `git-hygiene`,
  `commit-format`, `secret-handling` (path-scoped on `.env*`,
  credentials/, *.pem), `pr-conventions`. All within the 60–150
  line budget.
- README/INDEX files for each `lib/{agents,skills,rules}/` directory.
- `lib/playbooks/` remains empty in v0.1; populated in v0.2.

### Added — Batch 2.75 (engineering standards)

- `STYLE.md` — quick-reference engineering standards (shell, comments,
  logging, testing, no dark code, defensive input, agent quality)
- `docs/engineering-standards.md` — explanatory guide with worked examples
  for each STYLE.md section
- `tests/README.md` — test layout and fixture naming convention
- `PHILOSOPHY.md` — stub with the "Standards as control" section
  (Batch 4 will expand)
- `cli/lib/validate.sh` standards checks: shebang/strict-mode, header
  Purpose comment, executable bits on hooks, TODO-only files, dark-code
  detection (unreferenced scripts), SKILL.md required sections, agent
  required sections, line budgets (agents 80-140, skills 80-180,
  rules 60-150). All WARN-only in v0.1.
- README references to STYLE.md and PHILOSOPHY.md.

### Fixed — Batch 2 retrofit (post-Batch-2 defect fix)

- Work-directory resolution unified between CLI and hooks via
  `cli/lib/paths.sh`. Previously hooks wrote to `${CLAUDE_PROJECT_DIR}/work/`
  while CLI read from `~/agent-control/<project>/work/` — `yakos status`
  saw nothing and hooks polluted the project repo.
- `.session-started-history` migrated from JSON array to NDJSON
  (`.session-started-history.ndjson`). One event per line, append-only.
- Idempotent session summaries keyed on `(session_id, exit_kind)` —
  re-firing a hook doesn't duplicate ledger entries.
- `team-lifecycle.sh` and `session-end-check.sh` rewritten with no-block
  policy (telemetry hooks always exit 0).
- `ct_dir_size_bytes` and `ct_iso_to_epoch` added to compat.sh.
- Symlink approach for shared helpers: `lib/hooks/lib/{paths,compat}.sh`
  symlink to `cli/lib/{paths,compat}.sh`. `init -L` dereferences when
  copying to projects.
- `cli/lib/init.sh` migrates legacy `.session-started-history` if found.

### Future batches

Batches 3–6 will add: generic agents/skills/rules under the new standards,
full documentation, the `tiny-go-api` example, and a temporary-HOME
end-to-end smoke test.

## [0.1.0] — Batch 1A

Initial release. Ships only the CLI skeleton; later batches populate
agents, skills, hooks, docs, and examples. The build is gated by per-batch
status reports and pause points; this is the first.

### Added

- `cli/yakos` — entry point with subcommand dispatch and `--help`/`--version`
- `cli/lib/compat.sh` — cross-platform helpers (`ct_realpath`, `ct_timeout`,
  `ct_sed_inplace`, `ct_json_get`, `ct_json_merge`, `ct_json_valid`,
  `ct_iso_utc`, `ct_log`, `ct_die`). Targets bash 3.2.
- `cli/lib/install.sh` — first-time install:
  - Per-file symlinks under `~/.claude/{agents,skills,rules,playbooks}/`
    (preserves user files; refreshes YakOS-owned symlinks; never overwrites
    non-symlinks).
  - Writes `~/.yakos` pointer.
  - Safely merges `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` into
    `~/.claude/settings.json`. Validates JSON before merge; writes a
    timestamped backup if the file already exists; preserves unknown keys.
  - Marks `~/.claude/.yakos-created-settings` if it created the file.
- `cli/lib/uninstall.sh` — removes only YakOS-owned symlinks (resolved via
  the `~/.yakos` pointer); deletes `settings.json` only if YakOS created
  it; supports `--restore-settings` to restore from the most recent backup;
  removes the pointer file. **Never touches `~/.claude/projects/`** (auto-
  memory protection — no flag can override this in v0.1).
- `cli/lib/doctor.sh` — verifies required commands, the install pointer,
  symlink resolution under `~/.claude/`, and `settings.json` validity.
  Reports auto-memory state informationally.
- Stubs for `update`, `init`, `validate`, `archive`, `status`, `team`
  with clear "Batch 1B" deferral messages and `exit 0`.
- `lib/{agents,skills,rules,playbooks,hooks,settings}/` empty subdirs
  with `.gitkeep` markers — populated in later batches.
- `docs/architecture/SUMMARY-FROM-CLAUDE.md` — read-back of the
  architecture written before Batch 1A began.

### Safety properties

- Real `~/.claude/` is not touched by any automated test in this batch;
  the round-trip self-validation runs against `HOME=$(mktemp -d)`.
- `settings.json` merge is non-clobbering: existing keys (including
  `hooks`, `permissions`, `statusLine`, `model`) are preserved; only
  the YakOS-owned `env` entry is added.
- `uninstall` cannot delete auto-memory at `~/.claude/projects/`.

[Unreleased]: https://github.com/bakw00ds/yakos/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/bakw00ds/yakos/releases/tag/v0.1.0
