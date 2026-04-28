# Batch 1A — status report

**Status:** Complete. Self-validation green. Ready for review.

## What was built

| File | Lines | Role |
|---|---:|---|
| `cli/yakos` | ~80 | Subcommand dispatcher; sources compat.sh; supports `--version`, `--help` |
| `cli/lib/compat.sh` | ~140 | Cross-platform helpers: `ct_realpath`, `ct_timeout`, `ct_sed_inplace`, `ct_json_get`, `ct_json_merge`, `ct_json_valid`, `ct_iso_utc`, `ct_log`, `ct_die`. Targets bash 3.2. |
| `cli/lib/install.sh` | ~190 | Per-file symlinks; safe settings.json merge with timestamped backup; refuses on invalid JSON before touching any state |
| `cli/lib/uninstall.sh` | ~150 | Removes only YakOS-owned symlinks (resolved via `~/.yakos`); deletes settings.json only if YakOS created it; supports `--restore-settings`; never touches `~/.claude/projects/` |
| `cli/lib/doctor.sh` | ~150 | Required-commands check, optional-commands info, pointer resolution, symlink resolution, JSON validity, auto-memory state |
| `cli/lib/{update,init,validate,archive,status,team}.sh` | ~10 each | Stubs with clear "Batch 1B" deferral messages and `exit 0` |
| `lib/{agents,skills,rules,playbooks,hooks,settings}/.gitkeep` | 0 | Empty subdirs — populated in later batches |
| `VERSION` | 1 | `0.1.0` |
| `README.md` | ~80 | Quickstart only; full docs in Batch 4 |
| `CHANGELOG.md` | ~50 | Initial 0.1.0 entry |
| `.gitignore` | ~20 | macOS junk, Claude internals, editor backups, yakos backups |
| `docs/architecture/SUMMARY-FROM-CLAUDE.md` | ~60 | Read-back of architecture, written before code (in housekeeping commit) |

Total: about 12 working files + 6 stubs + scaffolding.

## What was tested

All seven self-validation steps from the build prompt, plus three extras
because the settings.json merge is the highest-risk path.

### Spec tests (build prompt §"Self-validation steps")

| # | Test | Result |
|---|---|---|
| 1 | `bash cli/yakos --version` → `yakos 0.1.0` | ✓ |
| 2 | `bash cli/yakos --help` → subcommand list with stubs marked | ✓ |
| 3 | `bash cli/yakos install --help` → usage | ✓ |
| 4 | `source compat.sh && declare -F | grep ct_` → all 9 helpers | ✓ |
| 5 | Round-trip against `HOME=$(mktemp -d)`: install / ls / cat settings / doctor / uninstall / verify cleanup | ✓ |
| 6 | `shellcheck cli/yakos cli/lib/*.sh` | SKIPPED — shellcheck not installed (per spec: "if shellcheck is installed") |
| 7 | Confirm via `git log` that no `$TEST_HOME` files were committed | ✓ — no `/var/folders/...` or `/tmp/...` paths in git history |

### Extra tests (highest-risk paths)

**Test A — Pre-existing settings.json with user keys.** Created a settings.json with `model`, `statusLine`, `env.MY_USER_VAR`, and `hooks.PreToolUse` populated, ran install. Result:

- All four pre-existing keys preserved verbatim
- Timestamped backup created at `settings.json.yakos-bak-20260428T040017Z`
- `env.MY_USER_VAR` preserved alongside the new `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`
- Marker file `.yakos-created-settings` NOT written (we didn't create the file)
- Subsequent uninstall left settings.json in place (as it should — we didn't create it), listed available backups for `--restore-settings`

**Test B — Malformed JSON refusal.** Created `{settings.json content: "this is not json {"}`, ran install:

- install exits 1 with FATAL error message naming the file
- Pointer `~/.yakos` NOT written (preflight check added after the first run of this test surfaced that it was)
- settings.json content unchanged

**Test C — Real `~/.claude` isolation.** Snapshotted `stat -f '%m'` of real `~/.claude` before and after the entire test sequence; mtime unchanged. Confirms the real user dotfile dir was not touched.

### Round-trip pass result

```
TEST_HOME=/var/folders/.../yakos-batch1a-XXXXXX.fnYDs3c2I0
install:    pointer + settings.json + 4 empty subdirs (lib is empty, no symlinks made)
doctor:     0 errors, 0 warnings
uninstall:  pointer removed, settings.json removed (we created it), marker removed
post:       only the four empty Claude subdirs remain (we leave them — user may have non-YakOS plans for them)
real ~/.claude mtime: UNCHANGED
```

## Deviations from the architecture doc / build prompt

None that change behavior. Two notes:

1. **Per-file symlinks, not directory symlinks.** The architecture doc shows `~/.claude/agents/` symlinks pointing per-file into `lib/agents/<file>.md`. The build prompt is silent on granularity. I went with per-file because (a) it preserves any existing user files in those directories, (b) it matches what the architecture doc visually depicts, and (c) the uninstall contract ("Remove every symlink … that resolves to a path inside the yakos repo. Leave non-YakOS files alone") implies per-file. With `lib/` currently empty, this is observably equivalent to the directory-link behavior.

2. **Empty `~/.claude/{agents,skills,rules,playbooks}/` dirs are left after uninstall.** Build prompt says post-uninstall state should be "only original-state files (or empty)". Empty dirs match. We don't `rmdir` because (a) we don't always know whether install created them (the first install creates them via `mkdir -p`; a second install would be a no-op), and (b) users may have placed non-symlink content there independently — a `rmdir` failing silently on non-empty dirs is fine, but conservative non-removal is even safer.

## Ambiguities / unclear that I want to flag

None block Batch 1B. One observation worth keeping in mind:

- The architecture doc's offhand "the hook script reads the agent name from the env" line (§12) is incomplete: per Phase 1.7, `CLAUDE_CODE_AGENT` is **not** set during in-team SendMessage hook fires. Hook scripts in Batch 2 will read `agent_type` from stdin JSON as canonical. SUMMARY-FROM-CLAUDE.md captures this; surfacing again here so it isn't lost.

## Environment findings (informational, not blockers)

| Tool | Version / Status |
|---|---|
| `claude` | 2.1.121 (≥ 2.1.32 required) ✓ |
| `bash` | 5.3.9 (compat targets 3.2 floor) ✓ |
| `git` | 2.50.1 ✓ |
| `jq` | 1.7.1-apple ✓ |
| `tmux` | 3.6a ✓ |
| `realpath` | present (`/bin/realpath`) ✓ |
| `gtimeout` | MISSING — compat handles fallback (warns once on use) |
| `gsed` | MISSING — compat handles via BSD `sed -i ''` |
| `shellcheck` | MISSING — optional self-check skipped (no fail) |
| `python3` | 3.9.6 — present, used optionally by future `validate` |

`brew install coreutils` would close the `gtimeout`/`gsed` gaps. `brew install shellcheck` would let Batch 2 run a stricter self-check.

## Surprises

- macOS filesystem is case-insensitive, so the original `~/.code/yakos` (per build prompt's instructions) and my actual working path `~/github/yakOS` (capital OS) both resolve to the same dir. Initial commit went into `/Users/tw/github/yakOS/.git/`. Not an issue, just worth knowing.
- The first malformed-JSON test (Test B) revealed the install was writing the pointer file before validating settings.json. Fixed in-place by adding a 4-line preflight check at the top of `install.sh`. Re-tested: install now refuses cleanly with no state mutation.

## What's next

Batch 1B per spec — remaining subcommands (`update`, `init`, `validate`,
`archive`, `status`, `team`). Will pause for "go" before starting.

Pushed to `origin/main`.
