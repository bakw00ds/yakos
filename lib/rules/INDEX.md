# Cross-cutting rules

Rules shared across projects. Path-scoped or always-loaded; per Phase 1.5
§11, rules with no `paths:` field load at session start.

## Inventory

| Rule | Path scope | Purpose |
|---|---|---|
| `lead-dispatch-discipline` | always-loaded | Lead decomposes / integrates / synthesizes; specialists do specialist work; parallel by default. |
| `git-hygiene` | always-loaded | Worktree, commit, force-push behavior. |
| `commit-format` | always-loaded | Conventional Commits convention with exceptions. |
| `secret-handling` | `**/.env*`, credential patterns | Cross-project: never commit secrets. |
| `pr-conventions` | always-loaded | Branch naming, PR template, review requirements. |
| `retrospective-discipline` | always-loaded | 10-cycle retro cadence: hook auto-dispatches librarian on `.retro-due` marker; operator gates promotion via `yakos skill promote` / `yakos soul approve`. |
| `cache-stability` | always-loaded | Keep the cached request prefix byte-stable across turns so the Anthropic prompt cache keeps hitting; append new content at the tail, never mutate/reorder the prefix or inject volatile data. |

## How rules load

Rules with `paths:` fire when Claude reads a matching file (Phase 0
Test 3 confirmed this is path-on-read, not path-on-edit; the rule
remains in context for the rest of the session). Rules without
`paths:` always load at session start.

User-level rules at `~/.claude/rules/` (these) load before project rules
at `<project>/.claude/rules/`, giving project-level entries higher
precedence and the ability to override.

## Adding rules

Generic, cross-project rules go here. Project-specific rules
(`go-backend.md`, `flutter.md`, `postgres-migrations.md`, etc.) live in
the project's `.claude/rules/` and reference these as needed.

A rule's body should be tight — bullets, not essays. The `references:`
field is the right place to point at supporting documents (incident
catalog entries, playbooks).
