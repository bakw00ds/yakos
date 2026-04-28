---
name: pr-conventions
description: Branch naming, PR description shape, review requirements.
references:
  - rule:git-hygiene
  - rule:commit-format
---

# PR Conventions

Always loaded. Applies to any branch destined for `main` / `master`.

## Branch naming

- `feat/<short-description>` — new functionality
- `fix/<incident-or-issue>` — bug fix referencing an existing tracker
- `chore/<topic>` — maintenance, deps, framework noise
- `refactor/<area>` — restructuring with no behavior change
- `docs/<topic>` — documentation-only

Short = ≤4 hyphenated words. Match the commit type prefix.

## PR description

Every PR has, at minimum:

```markdown
## Summary
<1-3 bullets describing what changed and why>

## Test plan
<bulleted checklist of how this was verified>

## Risks / known limitations
<anything a reviewer or operator should know>
```

Optional sections (use when relevant):

- **Migration notes** — for changes that need ordered rollout
- **Rollback plan** — for changes that aren't trivially reversible
- **Screenshots / before-and-after** — for UI changes
- **Feedback citations** — for backend-only changes shipped under a
  combined release

## Review requirements

- Every PR gets at least one human review.
- Security-sensitive PRs (auth, authz, data handling, deploy config,
  third-party integrations) get a `security-reviewer` pass before
  the human approves.
- Migrations get a security-reviewer + db-migrations specialist
  approval pair (per Phase 1.5 §10 team-shapes guidance).
- "LGTM, nothing too risky" is not a review. The review confirms the
  diff was read; if you can't summarize the change, you didn't read it.

## Merge etiquette

- Squash-and-merge by default. Preserves the PR title as the commit
  message; clean main history.
- Merge-commit for branches that legitimately want preserved history
  (long-lived feature integrations).
- Never rebase-merge to main from agent sessions — the history-rewrite
  intersects badly with concurrent worktrees.

## Anti-patterns

- "Cleanup" PRs that mix unrelated changes.
- PRs that change >500 lines without a decomposition explained in the
  description.
- PRs without a Test plan section. Even "no test needed because X" is
  better than blank.
- Review-thrashing — re-pushing to address every comment immediately.
  Batch fixups; one push per round of review feedback.

## Anti-pattern (from agent contexts)

- A teammate opening a PR before the relevant `task-complete-dispatch`
  validators have run. Open the PR after the local validators pass; CI
  is for confirmation, not first-pass.
