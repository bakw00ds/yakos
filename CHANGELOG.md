# Changelog

All notable changes to YakOS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
