---
name: sprint-cost-discipline
description: Bounds the cost of a multi-PR sprint — PR size cap, one non-forking reviewer per PR, a narrow fresh agent per round, narrow re-reviews, ceiling preflight, per-agent scratch, a token budget per PR, replace-don't-wait liveness, and merge gating (post-merge steps on merge success, mergeable re-read, stacked-PR base deletion). Always-loaded.
references:
  - rule:lead-dispatch-discipline
  - rule:verification-discipline
  - rule:git-hygiene
---

# Sprint Cost Discipline

Always loaded (no `paths:` field). Written after the 2026-10-05/06 router
sprint (P0 + K-138, PRs #322–#331, v0.62.0.0): two days, 123 subagents,
38.0M output tokens, 15.1B cache-read tokens, about 247 agent-hours.
Code review cost as much as implementation (13.9M vs 14.7M output
tokens). Model choice was not a driver.

## What drove the cost, in order

1. **Tranche size.** Each P0 tranche shipped as one 5k–18k line PR
   (61–73% tests and fixtures); #324 carried 57 commits, #330 carried 64.
   Review time and verdict rounds scale super-linearly with diff size.
2. **Reviewer self-forking.** Fourteen review agents on one PR (#330),
   each re-reading the same diff: 9.1M review output tokens on one PR.
3. **Agents held open across rounds.** Six agents lived 17–23 hours.
   Every wake re-read the accumulated context (one implementer made
   1,277 Bash calls in a single context).
4. **Stalls and duplicate work.** The supervisor launch ceiling (90)
   tripped overnight and blocked all edits; two agents looked silent for
   seven hours and were replaced, so their last round ran twice; six
   agents overflowed context and lost completion notices; a reviewer's
   TMPDIR cleanup killed the implementer's running race test; a macOS CI
   timeout and a Windows flake each forced a rerun.
5. **Scope growth under review.** Every round found real defects, each
   adding tests, a push and a full re-verification on two bash versions
   and three operating systems.

## The rules

- **PR cap: ≤3k added lines including tests.** Slice briefs to fit. A
  slice that cannot fit becomes sequential PRs with a contract handoff
  (`skill:contract-handoff`, `skill:split-mega-task`).
- **One reviewer per PR; reviewers do not fork.** A reviewer spawns no
  sub-agents unless the lead's brief names a second, non-overlapping
  scope. Security review is the one standing exception and runs as its
  own dispatch.
- **Narrow agent per round.** After each verdict the lead ends the
  implementer and the reviewer. The next round is a fresh agent briefed
  with the finding list and the pushed sha. No agent waits on CI or on
  another agent; the lead owns CI watching.
- **Re-reviews are narrow.** Verify only the prior findings against the
  new sha. A docs-only or test-only delta gets no full-suite rerun.
- **Preflight the ceilings.** Before dispatching: supervisor launch
  ceiling raised for the session's expected volume, Go build cache and
  scratch cleared, free disk checked. Do not discover a ceiling at 2am.
- **Per-agent TMPDIR and scratch subdir** named in every brief; an agent
  cleans up only what it created.
- **Budget per PR.** The brief states the expected output tokens for the
  PR; the lead stops the round at 2× and surfaces it to the operator
  rather than letting a round run open-ended.
- **Replace, don't wait.** An agent with no message or file activity for
  30 minutes is checked for liveness (process list, worktree mtime, the
  transcript); if its last report is already in the transcript, use it
  rather than re-running the round. If it is alive but silent, or
  nothing shows, stop it and dispatch a fresh narrow agent from the
  pushed sha: do not wait on it past one more check. Message delivery
  can lag by hours, so first look for a report that has arrived
  (2026-10-06: two agents were replaced and their late reports then
  showed they had finished).
- **Gate every post-merge step on the merge succeeding.** `gh pr merge`
  can refuse (for example CONFLICTING after a sibling PR merged). Chain
  the worktree and branch removal, the kanban move and the decisions
  entry with `&&` after it, and confirm the PR state is `MERGED` and
  `main` moved before recording anything (2026-10-07: an unconditional
  `;` chain removed a live worktree and logged a merge that never
  happened, #338).
- **Re-read `mergeable` right before merging.** A green check and a SHIP
  verdict were true of an earlier base. Read `mergeable` and `headRefOid`
  again immediately before `gh pr merge`; CONFLICTING means a merge
  round, not a retry. A conflicting PR also gets no pull-request CI.
- **Stacked PRs: open the follow-on against `main` after the parent
  merges.** With squash merges, deleting the parent's branch closes a
  PR whose base it was, and a closed PR cannot be re-targeted (2026-10-08:
  #357 closed when `--delete-branch` removed its base after #356, and
  #358 replaced it). Either open the follow-on against `main` once the
  parent has merged, or keep the parent's branch until the follow-on has
  been rebased onto `main` and re-targeted.

## References

- `rule:lead-dispatch-discipline` §Loop cadence — the narrow-agent-per-
  round rule this one enforces with numbers.
- `rule:verification-discipline` — the definition of done is unchanged;
  this rule bounds how much work sits under one review.
- `rule:git-hygiene` §Worktree — per-agent isolation.
- work/current/reports/multimodel-router-plan-2026-10-05.md §"Sprint 1
  retrospective" — the measurements behind the numbers above.
