# Batch 1B — status report

**Status:** Complete. All six remaining subcommands functional. Self-validation green. Ready for review.

## What was built

| File | Lines | Role |
|---|---:|---|
| `cli/lib/init.sh` | ~190 | Bootstraps `~/agent-control/<n>/` for a project; copies hooks with `.framework-hash` siblings; writes `.claude/settings.json`/`path-allowlist.json` from templates if missing; ensures `MEMORY.md` exists at the encoded auto-memory path |
| `cli/lib/archive.sh` | ~140 | Moves `work/current/` → `work/archive/<tag>/`; section-aware bypass-expiry check; tag validation; appends ledger entry to `sessions.ndjson` |
| `cli/lib/status.sh` | ~165 | Per-project dashboard: session age (>4h soft warning), scratchpad size (>100MB soft warning), file ages (decisions stale flag), hook log summaries, bypass count, mailbox count, MEMORY.md state |
| `cli/lib/team.sh` | ~95 | `yakos team restart <project>` — wraps archive with `--auto-tag` + prints relaunch instructions using `.project-path` written by init |
| `cli/lib/update.sh` | ~60 | `git pull --ff-only` + idempotent `install` re-run + commit/file-change report |
| `cli/lib/validate.sh` | ~155 | Three-mode (framework / project / `--all`); python3-when-available, grep fallback with "limited validation" warning; cleanly handles empty lib/ |
| `cli/lib/doctor.sh` (extended) | +~30 | Optional `<project-path>` arg adds SHA-256 drift check vs `.framework-hash` siblings (informational, not error) |
| `cli/lib/compat.sh` (extended) | +~50 | Added `ct_iso_now_z`, `ct_sha256` (shasum/sha256sum/openssl fallback chain), `ct_encode_project_path` (Claude Code's `/` `.` → `-` rule) |
| `lib/settings/settings.template.json` | ~60 | Project-level template wiring `path-log` / `path-allowlist` / `secret-scan` / `mailbox-mirror` / `team-lifecycle` PreToolUse, `task-dependency-gate` / `task-complete-dispatch` TaskCompleted, `session-end-check` SessionEnd. Hooks themselves arrive in Batch 2. |
| `lib/settings/path-allowlist.template.json` | ~15 | Per-`agent_type` allow/deny schema with a documented example block |
| `lib/settings/agent-control.gitignore.template` | 1 | Just `*` — control dir fully ignored |
| `lib/settings/settings.local.template.json` | ~3 | Per-user-per-project overrides; empty stub |
| `lib/settings/hook-bypass.template.md` | ~30 | Documents the bypass schema; `## Active entries` is the sentinel section parsers key off of |
| `cli/lib/init.sh` (`.project-path`) | +3 | init writes the project repo path here; `team restart` reads it for the relaunch prompt |

## What was tested

### Spec tests (build prompt §"Self-validation")

| # | Test | Result |
|---|---|---|
| 1 | Each subcommand `--help` parses (install, uninstall, update, init, validate, archive, status, team, doctor) | ✓ |
| 2 | `yakos validate` (framework, currently empty) — clean "no agents/skills/rules to validate" | ✓ — 0 errors, 0 warnings |
| 3 | `yakos init tinyproj --project <temp-git-repo>` — full agent-control tree created; project `.claude/{settings,path-allowlist}.json` written; MEMORY.md written under encoded path; `.project-path` written | ✓ |
| 4 | `yakos archive tinyproj v0.0.0-test` — current/ moved to archive/v0.0.0-test/, fresh current/ recreated with templates, sessions.ndjson appended | ✓ |
| 5 | `yakos status tinyproj` — output matches spec format (dashboard sections in order; `Bypasses: 0 active` after fix below) | ✓ |
| 6 | `yakos team restart tinyproj` — archives with `auto-<ISO>` tag, prints `cd ... && claude --add-dir <project-repo>` using the path captured at init | ✓ |

### Bonus tests (security / robustness)

- **Expired bypass refusal.** Injected an entry under `## Active entries` with a 2024 expiry. `yakos archive` exits 1 with the entry id and timestamp listed; `work/current/` is unchanged.
- **Tag validation.** `archive ... 'bad/tag'` and `archive ... 'has space'` both rejected with FATAL. Slash and whitespace are not in the allowed set (alphanumeric + `.`/`-`/`_`).
- **`yakos doctor <project>` drift check.** Runs against the `scripts/hooks/` of the test project; reports `0 clean, 0 drifted, 0 unhashed` since `lib/hooks/` is empty in v0.1 (so init copied nothing). Drift is surfaced as `[info]`, not `[err]`, per spec.
- **Real `~/.claude` isolation.** All tests run with `HOME=$(mktemp -d)`. The temp HOMEs and temp project repos are removed at end of each test pass.

### Bugs that surfaced during self-validation, and the fix

This is the "honesty over completeness" bullet from the build-prompt rules — flagging it here because it's a real example.

**Bug:** First archive run printed `bypass:<short-id> (unparseable Expires: <ISO-8601)` and refused to archive. The first status run showed `Bypasses: 1 active`. Root cause: the bypass-template's *format example* uses `## bypass:<short-id>` and `**Expires:** <ISO-8601 UTC>` literally — and my section-unaware parser was matching them as a real entry.

**Fix:** Made both parsers (in `archive.sh` and `status.sh`) section-aware. They only recognise `## bypass:` headings that appear *after* `## Active entries`. The template documents the format above that section header; entries go below. Strong, simple contract.

**Re-tested:** archive runs clean; status shows `Bypasses: 0 active`; the EXPIRED-bypass test injects a real entry under `## Active entries` and is correctly refused. Both code paths now match the spec contract.

## Deviations from the spec

None that change behavior. Two notes:

1. **Hook copy in v0.1 is observably a no-op.** The build prompt's `init` step 8 says "Copy each file from `yakos/lib/hooks/` into `<project>/scripts/hooks/`." Since `lib/hooks/` currently contains only `.gitkeep` (hooks arrive in Batch 2), init reports `hooks copied: 0 new, 0 overwritten, 0 skipped`. The `find ... ! -name '.gitkeep' ! -name 'README.md'` filter is correct; it'll begin copying real hooks once Batch 2 lands. Drift detection is similarly inert until then.

2. **`team restart` uses `.project-path` written by init.** The spec says "v0.1 doesn't auto-relaunch" and shows the relaunch print but doesn't specify how to know the project repo path. I added a tiny `.project-path` file written by init (one line, the absolute repo path) so the relaunch instructions are concrete instead of `<path-to-project-repo>`. Status also reads this if present. Falls back gracefully if the file is missing.

## Ambiguities / unclear that I want to flag

None block Batch 2.

- The spec's `archive` step 6 says "Append archive entry to `work/sessions.ndjson`." I write the file even on `init` (empty) so archive can always append. The existing `init` behavior creates it as zero-byte if missing. Acceptable.
- The spec for `status` expects a "Last team: started ... (active 2h 14m)" line. v0.1's session history is `[]` until the SessionStart hook lands in Batch 2, so the live-test output reads `Last team: not started`. The age-formatter and 4h soft warning are fully exercised once a real `started_at` is appended; logic is in place and correct.

## What's next

**Batch 2** per spec: hook scripts (`path-allowlist.sh`, `task-dependency-gate.sh`,
`task-complete-dispatch.sh`, `session-end-check.sh`, `mailbox-mirror.sh`,
`secret-scan.sh`, `team-lifecycle.sh`, `path-log.sh`) + per-domain validators
+ committed test fixtures under `tests/fixtures/hooks/`. The REPORT-only
fallback rule applies for any hook I can't confidently verify.

Pushed to `origin/main`.
