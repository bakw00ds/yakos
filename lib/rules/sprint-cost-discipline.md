---
name: sprint-cost-discipline
description: Bounds the cost of a multi-PR sprint — PR size cap, one non-forking reviewer per PR, a narrow fresh agent per round, narrow re-reviews, ceiling preflight, per-agent scratch, a token budget per PR, and replace-don't-wait liveness. Always-loaded.
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
  30 minutes is checked for liveness; if its last report is in the
  transcript, use it rather than re-running the round.

## References

- `rule:lead-dispatch-discipline` §Loop cadence — the narrow-agent-per-
  round rule this one enforces with numbers.
- `rule:verification-discipline` — the definition of done is unchanged;
  this rule bounds how much work sits under one review.
- `rule:git-hygiene` §Worktree — per-agent isolation.
- work/current/reports/multimodel-router-plan-2026-10-05.md §"Sprint 1
  retrospective" — the measurements behind the numbers above.
