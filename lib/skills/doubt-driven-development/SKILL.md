---
name: doubt-driven-development
description: Subject a high-stakes or irreversible decision to a fresh-context adversarial review BEFORE it stands, by dispatching an independent reviewer who only sees the artifact and contract — not your reasoning. Use when a decision crosses module boundaries, asserts an unverifiable property (thread-safety, ordering, idempotence), or has irreversible blast radius (prod deploy, data migration, public API change).
allowed-tools: Read SendMessage
argument-hint: "[<decision-or-artifact>]"
mode: [review, plan]
tier: opus
invocable_by: [lead, architect]
domains: [quality, architecture, risk]
version: 1
references:
  - rule:lead-dispatch-discipline
  - rule:git-hygiene
  - skill:test-driven-development
  - playbook:02-code-quality
---

# doubt-driven-development

## Purpose

Catch wrong directions *early*, while course-correction is still cheap,
by materializing a biased-to-disprove reviewer during development — not
after the PR is built. Accumulated session context quietly converts
assumptions into "facts"; a fresh-context reviewer who never saw that
context is the antidote.

The failure mode this prevents: the lead (or a specialist) reasons
itself into a confident decision, the reasoning is never independently
challenged, and the cost surfaces only after the irreversible thing has
shipped.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `doubt-driven-development`. In yakOS this is the codified form
of the dispatch/review gate: the "fresh context" is a separately
dispatched reviewer agent (`code-reviewer`, `security-reviewer`, or
`architect`), per `rule:lead-dispatch-discipline` — a reviewer in its
own context window, not the lead second-guessing itself.

## Scope

A decision qualifies as non-trivial (and worth doubting) if it:

- introduces or modifies branching logic,
- crosses module/service boundaries,
- asserts a property the type system can't verify (thread-safety,
  idempotence, ordering, an invariant),
- depends on context future readers won't have, or
- has irreversible blast radius (prod deploy, data migration, public
  API change).

**Skip** for: mechanical operations, unambiguous user instructions,
reading existing code, obviously-correct one-liners, pure tooling, or
when the user prioritizes speed.

## Automated pass

The five-step workflow:

| Step | Task |
|---|---|
| 1. CLAIM | Name the decision in 2–3 lines and why it matters. |
| 2. EXTRACT | Isolate the smallest reviewable artifact + its contract; strip your reasoning. |
| 3. DOUBT | Dispatch a fresh-context reviewer with an adversarial brief ("find what's wrong with this"); optionally a second reviewer on a different model. |
| 4. RECONCILE | Classify each finding: contract-misread → actionable → trade-off → noise. Re-read the artifact text before accepting any finding. |
| 5. STOP | Halt when findings go trivial, after 3 cycles, or on user override. |

The reviewer sees the ARTIFACT + CONTRACT, never the CLAIM/reasoning —
passing your reasoning biases the reviewer toward agreeing with you.

## Manual pass

The lead owns RECONCILE: read the artifact text against each reviewer
finding before acting on it, classify (contract-misread → actionable →
trade-off → noise), and record the disposition. The reviewer advises;
the lead decides and integrates.

## Why dispatch, not self-review

The independence is the whole point. A lead re-reading its own decision
shares the context that produced it and will rationalize the same way.
yakOS's model already separates these roles: the lead decomposes and
integrates; a *separate* reviewer agent in its own context window
provides the fresh eyes (`rule:lead-dispatch-discipline`). If
specialists edit concurrently, give the reviewer the artifact via a
worktree/branch hand-off (`rule:git-hygiene`), not the live tree.

## Anti-rationalization

| Rationalization | Reality |
|---|---|
| "I'll doubt this one-liner too, to be safe" | Misapplies scope; non-trivial/irreversible decisions only. |
| "The reviewer flagged it, so I'll just change it" | You remain the orchestrator — re-read the artifact against each finding before acting. |
| "I'll pass my reasoning so the reviewer has context" | That biases toward agreement; pass artifact + contract only. |
| "I'll keep looping until it's perfect" | >3 cycles signals the artifact isn't ready — escalate to the user. |

## Findings synthesis

The CLAIM, the reviewer's findings, and the reconciliation land in
`work/current/decisions.md` so the audit trail shows the decision was
adversarially tested before it stood. Pairs with TDD: a failing test
from the RED step *is* the doubt step for a behavioral claim.

## Known gotchas

- **This runs in the lead/main session.** A dispatched specialist
  doesn't itself spin up sub-reviewers — it returns its artifact and
  the lead runs the gate.
- **Three-cycle cap.** Looping past three rounds means the artifact is
  premature; escalate rather than grind.
- **Don't skip silently.** If you choose not to run a second-model
  review, that choice stays visible to the operator.

## Tier rationale

Opus — this gate fires on the highest-stakes, irreversible decisions
where the cost of a missed flaw is largest; the reasoning depth and
adversarial framing justify the top tier. (The dispatched reviewers run
at their own agents' tiers.)
