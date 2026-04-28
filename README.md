# YakOS

A portable, multi-project agent framework for [Claude Code](https://docs.claude.com/en/docs/claude-code/overview).

YakOS is a versioned framework you install once and use across multiple
projects. It bundles the agents, skills, rules, and hooks that turn
Claude Code's Agent Teams primitives into a reliable multi-agent
workflow — with the enforcement gaps filled in (path allowlists,
dependency gates, mailbox audit, session-end checks).

## Status

**v0.1.0 — Batch 1A complete.** This release ships only the CLI
skeleton: `install`, `uninstall`, `doctor`, plus stubs for the rest.
Subsequent batches add the remaining subcommands, hook scripts, generic
agents, skills, and documentation. See [CHANGELOG.md](CHANGELOG.md).

## Prerequisites

- macOS or Linux
- `bash` 3.2+ (macOS system bash works)
- `git`
- `jq` (`brew install jq` / `apt install jq`)
- `claude` CLI v2.1.32 or later
- `tmux` (used by the lifecycle commands in later batches)

Optional: `coreutils` for `gtimeout`/`gsed` on macOS, `python3` for
fuller schema validation, `shellcheck` for self-checks.

## Install

```sh
git clone https://github.com/<you>/yakos.git ~/code/yakos
cd ~/code/yakos
./cli/yakos install
```

`install` creates per-file symlinks under `~/.claude/{agents,skills,rules,playbooks}/`
that point into this repo's `lib/`, writes a pointer file at `~/.yakos`,
and safely merges the experimental-agent-teams env var into
`~/.claude/settings.json`. It NEVER touches `~/.claude/projects/`
(your auto-memory).

If `~/.claude/settings.json` already exists, install:
- Validates that it's valid JSON (aborts if not — won't clobber your config)
- Writes a timestamped backup at `~/.claude/settings.json.yakos-bak-<ISO8601>`
- Merges only the YakOS-owned `env` block; preserves all other keys

## Verify

```sh
./cli/yakos doctor
```

## Uninstall

```sh
./cli/yakos uninstall
```

Removes only the YakOS-owned symlinks and the `~/.yakos` pointer.
Files YakOS didn't create are left in place. Auto-memory at
`~/.claude/projects/` is **never** touched, even with any flag.

## What's not in v0.1

- The four critical hook scripts (`path-allowlist.sh`,
  `task-dependency-gate.sh`, `task-complete-dispatch.sh`,
  `session-end-check.sh`) — Batch 2.
- Generic specialist agents and skills — Batch 3.
- Full documentation (`COOKBOOK.md`, `INCIDENT-CATALOG.md`,
  `MIGRATING.md`, `PHILOSOPHY.md`) — Batch 4.
- The `tiny-go-api` example project — Batch 5.
- End-to-end smoke test — Batch 6.

## Documentation

- [docs/architecture/phase-1.5-architecture.md](docs/architecture/phase-1.5-architecture.md) — the spec
- [docs/architecture/phase-0-validation-results.md](docs/architecture/phase-0-validation-results.md) — Agent Teams primitives validated
- [docs/architecture/phase-1.7-results.md](docs/architecture/phase-1.7-results.md) — SendMessage hookability validated
- [CHANGELOG.md](CHANGELOG.md)

## License

TBD.
