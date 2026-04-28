---
name: version-bump
description: Bump the project's VERSION file and prepend a CHANGELOG entry for the new version
allowed-tools: Bash Read Write
argument-hint: "--component {major|minor|patch|hotfix} [--message <text>] [--no-commit]"
mode: [release]
---

# Version Bump

## Purpose

Bump VERSION, prepend a CHANGELOG entry, and commit the change with a
standard message format. Provides a manual but cheap way to bump
versions deliberately. Required by the pre-push gate for any non-doc
push that introduces functional changes.

## Scope

VERSION is a four-part `major.minor.patch.hotfix` string at the
project's root (override with `YAKOS_VERSION_PATH`). CHANGELOG.md is
at the project's root (override with `YAKOS_CHANGELOG_PATH`). The
skill operates against the current git working tree — it does NOT
push or tag.

## When to use

- Before pushing changes that warrant a version bump.
- As a step in a release task orchestrated by the lead.
- After the pre-push gate refuses a push and tells you which tier to
  bump.

## When NOT to use

- Doc-only commits (the pre-push gate skips them).
- Initial project setup before VERSION exists — author it by hand.
- Ad-hoc "I just want a number to go up" use; the bump tier is load
  bearing.

## Bump semantics

| Component | What it means | What it resets |
|---|---|---|
| `major` | Backwards-incompatible schema/CLI changes | minor, patch, hotfix → 0 |
| `minor` | Additive features (new agent, skill, playbook, CLI command) | patch, hotfix → 0 |
| `patch` | Bug fixes, doc improvements, non-breaking refactors | hotfix → 0 |
| `hotfix` | Emergency fix to a deployed version, outside normal release flow | — |

The `pre-push-version-gate.sh` hook classifies file-paths against
these tiers and refuses the push if VERSION wasn't bumped to the
required tier.

## Automated pass

1. Parse `--component`. Reject if not in `{major, minor, patch, hotfix}`.
2. Read current VERSION; parse as four-part. Reject if malformed.
3. Compute new VERSION per tier rules above.
4. Prepend a CHANGELOG entry under the `## [Unreleased]` line (or at
   the top if no `## [Unreleased]` is present), formatted:
   ```
   ## [X.Y.Z.W] — YYYY-MM-DD

   ### Changed
   - <message from --message, or auto-generated from `git log <last_tag>..HEAD`>
   ```
5. If `--no-commit` not set, run
   `git add VERSION CHANGELOG.md && git commit -m "chore(version): bump to X.Y.Z.W"`
   (commit message overridable via `--message`).
6. Print the new VERSION string to stdout (callers parse this).

## Manual pass

Author of the bump verifies before push:

- The CHANGELOG entry under the new version describes the actual
  changes since the last tag (the auto-generated form is best-effort).
- The bump tier matches the highest classification of files in the
  push (run `git diff --name-only <last_tag>..HEAD` against your
  mental classification, or rely on the pre-push gate to catch a
  mismatch).
- If a `[Unreleased]` section already had bullets, those should be
  moved under the new version's heading rather than orphaned.

## Output trust model

The skill writes to VERSION and CHANGELOG.md in the project repo —
unlike most YakOS skills (which write to `work/current/artifacts/`).
Releases are the explicit exception: version state IS repo state, so
the version-bump skill writes to the repo.

## Known gotchas

- Running `version-bump` on a clean tree with no prior commits beyond
  the last release will produce a "version bumped but nothing changed"
  situation. Either commit your work first, then bump, OR use
  `--no-commit` and amend the bump into your existing commit.
- The skill assumes VERSION is at repo root. If it's elsewhere, set
  `YAKOS_VERSION_PATH=<path>` before invocation.
- Same for CHANGELOG: override with `YAKOS_CHANGELOG_PATH=<path>`
  (default: `CHANGELOG.md` at repo root).
- The auto-generated message reads `git log <last_tag>..HEAD` via
  `--oneline`. If no version tag exists yet (first release), it falls
  back to "Initial release" as the message body.
- `hotfix` increments only the fourth tier; it does NOT bump
  patch/minor/major. Use it for emergency fixes that need to ship
  without resetting the lower tiers.

## Worked examples

Bump tier → segment increment (consistent with the resets table):

| Component | Segment incremented | Lower segments |
|---|---|---|
| `major` | 1 (`major`) | 2, 3, 4 → 0 |
| `minor` | 2 (`minor`) | 3, 4 → 0 |
| `patch` | 3 (`patch`) | 4 → 0 |
| `hotfix` | 4 (`hotfix`) | — |

**Patch bump after a bug fix:**
```
$ cat VERSION
0.2.0.0
$ yakos version-bump --component patch --message "fix(cli): doctor reports stale ~/.yakos pointer correctly"
0.2.1.0
$ cat VERSION
0.2.1.0
```

**Minor bump for a new agent template:**
```
$ cat VERSION
0.2.1.0
$ yakos version-bump --component minor
0.3.0.0
```
(CHANGELOG gets an `## [0.3.0.0] — 2026-04-28` entry; commit
`chore(version): bump to 0.3.0.0` lands.)

**Hotfix on a deployed version:**
```
$ cat VERSION
0.3.0.0
$ yakos version-bump --component hotfix --message "fix(deploy): restore health-check probe path"
0.3.0.1
```

## Related

- [`lib/hooks/git/pre-push-version-gate.sh`](../../hooks/git/pre-push-version-gate.sh) —
  enforces that bumps happen before pushes with substantive code change.
- [`cli/yakos`](../../../cli/yakos) — `yakos version-bump`
  subcommand wraps `scripts/bump.sh`.
- [`STYLE.md`](../../../STYLE.md) — versioning discipline section.
