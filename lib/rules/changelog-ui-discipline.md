---
name: changelog-ui-discipline
description: Every UI-bearing project surfaces version + latest changelog at the UI layer; release notes visible without leaving the app. Loads when profile.standards.changelog-ui == true.
paths:
  - "**/.yakos.yml"
  - "**/CHANGELOG.md"
  - "**/changelog.*"
  - "**/Changelog.*"
  - "**/components/Changelog*"
  - "**/components/changelog*"
references:
  - rule:profile-standards
  - rule:commit-format
---

# Changelog UI discipline

Loads when Claude reads `.yakos.yml` (to check
`profile.standards.changelog-ui`), any project `CHANGELOG.md`,
or any changelog UI component file.

## What this rule is for

If `profile.standards.changelog-ui == true`, the project exposes
its current version and a human-readable changelog AT THE UI
LAYER. yakOS already enforces VERSION + CHANGELOG.md at the git
boundary via `pre-push-version-gate.sh`. This standard adds the
UI integration so users see what changed since their last visit
without leaving the app.

## What's required

- **A changelog UI component or page** rendering the project's
  `CHANGELOG.md` content. Source MUST be the same `CHANGELOG.md`
  that the pre-push gate enforces — no parallel changelog
  artifacts.
- **Current version visible in the app header / footer / about
  area**. Operator's choice of placement; what matters is the
  user can see "I'm on v0.18.0.0" without guessing.
- **Latest entry surfaced on first load** OR a notification
  pattern (e.g., a "what's new" toast that links to the
  changelog page) so users notice updates organically.

## Version sources

The version string MUST be sourced from one of:

- `VERSION` file at repo root (yakOS convention; preferred)
- `package.json` `"version"` field (Node projects)
- `Cargo.toml` `version` (Rust)
- `pyproject.toml` `version` (Python)

The UI MUST use the same source the pre-push gate enforces.
Mismatched versions between git layer and UI = audit P1.

## Format conventions

`CHANGELOG.md` follows Keep a Changelog
(yakOS convention; enforced by `version-bump` skill):

- Headers: `## [<version>] — <date>`
- Sections: `### Added`, `### Changed`, `### Deprecated`,
  `### Removed`, `### Fixed`, `### Security`
- `[Unreleased]` at the top for in-progress changes

The UI component reads this structure. Don't reinvent.

## What's forbidden

- **Parallel changelog artifacts.** A
  `web/public/release-notes.md` separate from `CHANGELOG.md`
  diverges immediately. Use ONE source.
- **Version display that doesn't match VERSION.** Hard-coded
  `"1.0"` in the UI when VERSION says `0.18.0.0` is wrong.
  Read VERSION at build time.
- **Marketing copy in changelog entries.** Entries describe
  what changed; marketing belongs on the about page (Standard 6)
  or homepage.

## When the changelog UI updates

- **Every release.** The `version-bump` skill bumps VERSION +
  prepends a CHANGELOG entry. The UI's build pipeline picks up
  the new entry automatically (build-time import).
- **In-flight feature merges.** Entries land under
  `[Unreleased]` during dev; promoted on the next bump.

## Composition with about page (Standard 6)

If both standards are enabled, the about page (Standard 6)
includes a link to the changelog UI. Or both collapse into a
single architecture page (Standard 5). Operator's choice; the
scaffold offers a `--merge-with-about` flag.

## Anti-pattern

The single most common failure mode: the changelog stops being
updated. Either the operator forgets to write entries or the
pre-push gate is bypassed enough that entries are stale.

When the audit-time playbook (`lib/playbooks/04-docs-architecture.md`
§Changelog UI presence) sees CHANGELOG.md last-modified > 60
days while VERSION has bumped multiple times, that's the signal.

## References

- `skill:changelog-ui-scaffold` — one-shot scaffold to drop in
  the UI component per frontend stack
- `skill:version-bump` — already-shipped yakOS skill that
  prepends CHANGELOG entries
- `lib/hooks/git/pre-push-version-gate.sh` — already enforces
  VERSION bumps at the git layer
- `cross-project-standards-plan.md` §4 — full design
