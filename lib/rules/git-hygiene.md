---
name: git-hygiene
description: Cross-project git conventions — worktree, force-push, never `git add -A`.
references:
  - rule:commit-format
  - rule:pr-conventions
---

# Git Hygiene

Cross-project conventions for using git from inside a YakOS team session.
Always loaded (no `paths:` field).

## Worktree

- Each agent gets its own git worktree for any concurrent multi-agent
  dispatch. The lead is responsible for setting up worktrees before
  spawning teammates that will edit files.
- Worktrees prevent the class of bug where two teammates step on each
  other's working tree mid-edit (incident: v2.62.4 worktree collision).
- Cleanup happens at archive time — `yakos archive` does NOT clean
  worktrees; that's manual still in v0.1.

## Force push

- **Never force-push to `main` or `master`.** Always. The lead refuses;
  hooks block it; CI rejects it. This rule supersedes any "but the
  history is messy" argument.
- Force-pushing to a feature branch is acceptable when the feature
  branch is exclusively the agent's. If shared, communicate before
  force-pushing — others have local checkouts.
- The git pre-push hook (project-level) refuses commit-dropping force
  pushes. If a force push is dropping commits, that's a bug, not a
  shortcut.

## `git add`

- **Never `git add -A` or `git add .` from an agent.** Stage explicit
  paths only. The wildcard variants accidentally stage:
  - secrets that landed in the working tree from another command
  - hook output / scratch files in `work/current/`
  - editor backups, cache files, OS metadata (.DS_Store)
- Use `git status --porcelain` to see what's outstanding, then stage
  by name.

## Branches

- Feature branches: `feat/<short-description>`. Short = ≤4 words.
- Fix branches: `fix/<incident-or-issue>`. Reference an existing tracker.
- Long-lived feature branches (>2 weeks) are tracked in the project's
  `decisions.md`. Don't accumulate untracked long-lived branches.

## Commit safety

- Never commit anything matching `*.env*`, `*credentials*`,
  `*secret*`, `*.pem`, `*.key`. The `secret-scan.sh` hook catches
  the obvious patterns; not committing the file in the first place
  is cheaper.
- If a secret slips into history, treat the secret as compromised —
  rotate it. The history rewrite is for clean-up, not for "fixing"
  the leak.

## Anti-patterns

- Committing scratch state from `work/current/` (init's `.gitignore`
  in agent-control prevents this; double-check from the project repo).
- Mixing concerns in a single commit ("fix bug + refactor"). Split.
- Vague commit messages. See `rule:commit-format`.

## What good looks like

```
feat(api): add /v1/meal-plans GET endpoint

Returns paginated meal plans for the authenticated user. Spec at
api/docs/openapi.yaml.

Feedback #a1b2c3d4
```
