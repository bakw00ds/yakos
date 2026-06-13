---
name: test-driven-development
description: Drive implementation and bug fixes test-first (RED→GREEN→REFACTOR), proving behavior with a failing test before the code that satisfies it. Use when implementing new logic, changing existing behavior, fixing a reported bug, or adding edge-case handling — write the failing test first; a passing test is the evidence, "looks right" is not.
allowed-tools: Read Edit Bash Grep
argument-hint: "[<path-or-test-target>]"
mode: [implement, review]
tier: sonnet
invocable_by: [lead, backend, frontend, mobile, maintainer]
domains: [testing, quality, implementation]
version: 1
references:
  - rule:lead-dispatch-discipline
  - skill:evidence-based-debugging
  - playbook:02-code-quality
---

# test-driven-development

## Purpose

Constrain implementation and bug-fix work to a **test-first** loop:
write a failing test, watch it fail, write the minimal code to pass,
then refactor under a green suite. A passing test is the evidence the
behavior is correct; "seems right" is not done.

The failure mode this prevents: code that compiles and demos cleanly
but has no executable proof of its contract. Post-hoc tests measure the
implementation that was already written, not the behavior that was
wanted — so they ossify bugs instead of catching them.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `test-driven-development`. Pairs with yakOS
`evidence-based-debugging` (the failing reproduction test IS the cited
evidence for a bug).

## Scope

- **In:** new logic, behavior changes, bug fixes (prove-the-bug first),
  edge-case handling, any change that could regress existing behavior.
- **Out:** pure config/docs/static-content changes with no behavioral
  impact; mechanical renames with no logic change.

This is an implementer skill — `invocable_by` includes the
backend/frontend/mobile specialists and the maintainer, not lead-only.
The lead dispatches it; the specialist executes the loop.

## Automated pass

The specialist executes the loop below. The loop IS the work; there is
no "write the code then maybe test it" path.

### The RED→GREEN→REFACTOR loop

1. **RED.** Write a test for the desired behavior and run it. It MUST
   fail first — a test that passes before you write the code is testing
   the wrong thing (or nothing).
2. **GREEN.** Write the minimal code to make the test pass. No
   speculative generality, no gold-plating.
3. **REFACTOR.** With the suite green, improve the implementation
   without changing behavior. Re-run the suite after each step.

### Prove-It pattern (bug fixes)

Resist jumping to the fix. Instead:

1. Write a test that reproduces the bug — it should fail.
2. Confirm the failure (this validates the bug actually exists where
   you think it does).
3. Implement the fix.
4. Confirm the test now passes.
5. Run the full suite for regressions.

The reproduction test is the anti-regression guard: the same bug
cannot silently return.

## Manual pass

The lead (or reviewer) confirms the loop was actually followed, not
reconstructed after the fact:

- The new/repro test **failed before** the change and **passes after**.
  Ask for the red run, not just the green one.
- The diff makes the test pass by satisfying the behavior, not by
  weakening the assertion.
- Test sizing is sane (mostly small/fast) and names read as specs.

A specialist returning a fix with no failing-first test gets
re-dispatched with the test as a required deliverable.

## Test sizing

Bias the suite toward small, fast, deterministic tests.

| Size | Constraints | Speed | Example |
|---|---|---|---|
| Small | single process, no I/O, no network, no DB | ms | pure-function tests |
| Medium | multi-process, localhost only, no external services | sec | API test w/ test DB |
| Large | multi-machine, external services allowed | min | E2E / staging |

Aim ~80% small / ~15% medium / ~5% large. Small tests are the ones
that stay green and pinpoint the break.

## Writing good tests

- **State, not interactions.** Assert on outcomes, not on which methods
  were called — interaction tests break on refactors that preserve
  behavior.
- **DAMP over DRY.** Each test reads as a standalone spec; some
  duplication is fine if it keeps the test independently legible.
- **Prefer real > fake > stub > mock.** Over-mocking yields tests that
  pass while production breaks. Mock only slow/non-deterministic/
  side-effecting boundaries.
- **Arrange-Act-Assert**, one concept per test, names that read like a
  specification ("rejects empty titles").

## Anti-rationalization

Behavioral skill — the loop is lost to in-the-moment excuses. The
rebuttals:

| Rationalization | Reality |
|---|---|
| "I'll add tests later" | You won't. Post-hoc tests measure the code you already wrote, not the behavior you wanted. |
| "Too simple to test" | Simple code accretes complexity; the test is the spec that survives it. |
| "Tests slow me down" | Slow now, faster on every future change — and they make the change safe to dispatch. |
| "I tested it manually" | Manual testing doesn't persist; the next change breaks it silently. |
| "It's just a prototype" | Prototypes ship. Test debt compounds fast. |
| "All tests pass" (none ran) | Confirm tests actually executed and the new one failed first. |

## Known gotchas

- **A test that passes on the first run** may be testing nothing — make
  it fail before you make it pass.
- **Verifier-gaming.** A fix that passes by weakening the assertion
  (commenting it out, loosening the matcher) is a regression, not a
  fix. The reviewer reads the diff, not just the green checkmark.
- **Re-running an unchanged suite for reassurance** adds no confidence;
  it only adds clock. Re-run after a code change, not as a comfort
  ritual.

## Tier rationale

Sonnet — judgment on test design (sizing, state-vs-interaction, mock
boundaries) plus synthesis across the code-under-test and its suite.
Haiku is too narrow for test-design tradeoffs; Opus is overkill for
the routine loop.
