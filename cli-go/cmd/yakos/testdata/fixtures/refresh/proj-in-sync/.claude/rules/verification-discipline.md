---
name: verification-discipline
description: Definition of done before a PR is opened — mutation-tested regression tests, differential checks for behavior-neutral changes, adversarial cases beyond the fixture corpus, security findings covered call-site by call-site. Always-loaded.
references:
  - rule:lead-dispatch-discipline
  - rule:git-hygiene
---

# Verification Discipline

Always loaded (no `paths:` field). Round-2 review findings on the
2026-09-23/24 batch (S-6 B-2, S-6 A-2a, K-82, S-2) shared one root
cause: a test that passed without proving the fix, or a "parity" claim
checked only against the cases already in the fixture file. This rule
is the definition of done that closes that gap, before a PR is opened.

## Definition of done

- **Mutation-test every new or changed regression test.** Revert or
  invert the fix; the test must fail. Restore the fix; it must pass.
  Record the failing-output snippet in the report. A test that stays
  green with the fix reverted is not a regression test.
- **Behavior-neutral changes prove equivalence, not just "still
  compiles."** Refactors, ports, parser/flag conversions, mechanical
  mirrors: diff `--help`/output against the base binary or script,
  `go tool nm` symbol-table diff for pure moves, and a differential
  fuzz of argv/input permutations (≥200 per surface) with zero
  unexplained divergences.
- **Fixture-covered behavior gets adversarial cases beyond the
  corpus**, written and run by hand: empty/malformed input, unicode
  paths, missing state, `HOOK_FAIL_CLOSED=1`, concurrency. Only after
  that may a change be flagged "ready" or "parity."
- **Security-relevant changes enumerate every call site, transport,
  and handler the finding names**, as a table, and prove each is
  covered — not just the one the repro exercised.
- **Tests write only to `t.TempDir()`.** Never `repoRoot(t)` or a real
  checkout; never derive a path from `YAKOS_ROOT`/`YAKOS_LIB`
  (`rule:git-hygiene`).
- **The full suite runs once per push, by the implementer.** A reviewer
  skips it only when ALL hold: every CI job covering the touched area is
  green on the SAME sha (not pending, not just "CI green" overall), and
  CI actually exercises that area. Otherwise run it locally.
- **Known CI gaps a reviewer still runs locally:**
  - Hook changes: fixtures under macOS `/bin/bash` 3.2 (CI runs bash 5
    for most hook jobs); skip only if the bash32 fixtures job covers
    that specific suite.
  - Windows-only paths, unless the Windows job ran on the sha.
  - Anything CI skips by path filter.
  Even when skipping: targeted tests, mutation checks, adversarial probes.
- **Known pre-existing failures are classified, not re-litigated.**
  Reproduce on the base commit in a scratch worktree, cite it by name,
  and never re-investigate it once listed in the brief.

## Report shape

Per finding: change (`file:function`), test name, mutation proof,
deferrals with one line each. ≤10-line return summary.
- Name the exact sha reviewed/verified.

## Push, report, exit

Implementers push and report; they do not wait on CI. The lead owns
CI watching and dispatches follow-up (`rule:lead-dispatch-discipline`
§Loop cadence).

## Why

Each failure mode here was caught only on a second review round, after
work was reported done: a pinned-table test that missed an argv `--`
terminator a fuzz pass found in minutes; 4 of 5 hooks marked "ready"
that diverged on adversarial cases never in the fixture corpus;
regression tests that only exercised a seam, found by mutation
testing; a tautological path test. Catching these before the PR is
opened is cheaper than a second review round.

## References

- `rule:lead-dispatch-discipline` §Loop cadence — push/report/exit.
- `rule:git-hygiene` §Worktree — the `t.TempDir()` / no-`YAKOS_ROOT` rule.
- `rule:sprint-cost-discipline` — bounds PR size, review fan-out, and agent lifetime per round.
<!-- yakos:managed sha256=ec2676135979e68e3dc2685c274e588bf915eb4711695f0e9546be7daf91c28a -->
