---
name: code-simplification
description: Simplify code for clarity while preserving exact behavior, understanding why code exists (Chesterton's Fence) before changing it. Use when recently-changed code is hard to read — deep nesting, long functions, nested ternaries, generic names, duplicated logic — and you want a behavior-preserving cleanup, NOT a feature change or a dead-code sweep.
allowed-tools: Read Edit Bash Grep
argument-hint: "[<path-or-glob>]"
mode: [implement]
tier: sonnet
invocable_by: [lead, backend, frontend, mobile, maintainer]
domains: [quality, refactoring, readability]
version: 1
references:
  - rule:lead-dispatch-discipline
  - skill:test-driven-development
  - playbook:02-code-quality
---

# code-simplification

## Purpose

Make code easier for the next reader to understand while keeping its
behavior exactly the same. The test: would a new team member
understand this faster than the original?

The failure mode this prevents: "simplifications" that quietly change
behavior (dropped error handling, altered edge cases) or that are just
the author's stylistic preference imposed over the codebase's
conventions.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `code-simplification`. Distinct from the yakOS `maintainer`
agent's dead-code work: maintainer removes code that no longer runs;
this skill clarifies code that still runs. If a section is dead, that's
maintainer's job, not this one.

## Scope

- **In:** behavior-preserving clarity refactors on recently-changed
  code — flattening nesting, splitting long functions, naming,
  de-duplicating.
- **Out:** behavior changes, feature work, dead-code removal
  (maintainer), and unscoped whole-repo refactors. Scope to what
  changed unless the user explicitly asks for more.

## Five principles

1. **Preserve behavior exactly** — same outputs, side effects, and
   error handling across all inputs.
2. **Follow project conventions** — match the codebase's patterns;
   don't impose external preferences.
3. **Clarity over cleverness** — explicit beats compact when compact
   needs mental parsing.
4. **Keep balance** — don't over-inline helpers, merge unrelated
   logic, or optimize for line count alone.
5. **Scope to what changed** — recently modified code only, unless
   explicitly told otherwise.

## Automated pass

1. **Understand before touching (Chesterton's Fence).** "If you see a
   fence across a road and don't understand why it's there, don't tear
   it down." Before changing anything answer: what is this code's
   responsibility? what calls it / does it call? do tests define its
   behavior? why might it have been written this way?
2. **Identify opportunities.** Deep nesting (3+ levels), long functions
   (50+ lines), nested ternaries, generic names (`data`, `result`,
   `temp`), duplicated logic.
3. **Apply incrementally.** One change at a time; run the suite after
   each. Submit the refactor SEPARATELY from features or fixes.
4. **Verify.** Compare before/after. Would a teammate sign off on this
   as a genuine improvement?

## Manual pass

The reviewer confirms it's genuinely behavior-preserving: the test
suite passes WITHOUT being modified (a changed test means changed
behavior), the diff is scoped to what changed, and no error handling
was dropped in the name of tidiness.

## Anti-rationalization

| Rationalization | Reality |
|---|---|
| "This code looks pointless, delete it" | Chesterton's Fence: find out why it exists first; the edge case may be load-bearing. |
| "I'll rename it to my preferred style" | Match the codebase's conventions; consistency lowers everyone's load. |
| "Fewer lines is simpler" | Line count isn't clarity; over-inlined code is often harder to read. |
| "I'll batch all the cleanups into one commit" | One change at a time, tests after each — batches hide the change that broke behavior. |

## Known gotchas

- **If a simplification needs the tests changed, behavior changed.**
  That's the bright-line signal you've left the scope of this skill —
  stop and reassess.
- **Don't remove error handling for cleanliness.** "Cleaner" code that
  drops a catch is a behavior change.
- **Tests are the safety net.** If the code has no tests, write
  characterization tests first (see `skill:test-driven-development`)
  before refactoring.

## Tier rationale

Sonnet — judging "is this clearer?" and applying Chesterton's Fence
require reading intent across the surrounding code. Haiku misses
context; Opus is unnecessary for behavior-preserving cleanup.
