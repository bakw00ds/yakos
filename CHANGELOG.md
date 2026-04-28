# Changelog

All notable changes to YakOS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
