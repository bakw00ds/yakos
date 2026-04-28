# Changelog

All notable changes to YakOS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Future batches (1B → 6) will add: remaining CLI subcommands (update, init,
validate, archive, status, team), reference hooks + per-domain validators
+ fixtures, generic agents/skills/rules, full documentation, the
`tiny-go-api` example, and a temporary-HOME end-to-end smoke test.

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
