# Git hooks shipped by YakOS

These hooks are project-side git hooks (live in `<project>/.git/hooks/`),
distinct from the Claude Code hooks under `lib/hooks/*.sh` (which live in
`<project>/scripts/hooks/` after `yakos init`).

The two are different concepts:

- **Claude Code hooks** fire on Claude Code's tool-use lifecycle
  (PreToolUse, PostToolUse, TaskCompleted, SessionEnd). They gate
  agent behavior.
- **Git hooks** fire on git's lifecycle (pre-commit, pre-push,
  post-merge). They gate human + agent behavior at the VCS boundary.

## Inventory

| Hook | When it fires | What it does | Install via |
|---|---|---|---|
| `pre-push-version-gate.sh` | git pre-push | Refuses pushes that contain substantive code changes since the last version tag without a corresponding VERSION change. Doc-only pushes pass through. | `yakos git-hooks install` |

## How install works

`yakos git-hooks install` copies `pre-push-version-gate.sh` to the
current repo's `.git/hooks/pre-push` and makes it executable. If a
`pre-push` hook already exists, install refuses without `--force`.

`yakos init <project> --with-gate` does the same during project
initialization.

The copy uses `.framework-hash` siblings (same pattern as
`scripts/hooks/`) so `yakos doctor` can detect drift between the
project's installed copy and the framework version.

## Override / bypass

Every project-local pre-push hook honors:

- `YAKOS_GATE_DISABLE=1 git push` — bypass the gate. Logged to
  `~/.yakos-state/gate-log.ndjson` with reason `YAKOS_GATE_DISABLE=1`.
- `git push --no-verify` — bypasses ALL git hooks (not just YakOS's).
  Native git mechanism. Use sparingly.

## Audit trail

Every gate decision (allow / refuse / override / error) appends one
NDJSON line to `~/.yakos-state/gate-log.ndjson` with timestamp, repo path,
HEAD SHA, last tag, decision, required tier, actual bump, and reason.

This is the canonical record. If a push happened, the gate either
allowed it or was overridden — and the log says which.
