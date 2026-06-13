---
name: phase-complete
description: Verify a phase's exit criteria are met before declaring it done. Use at the boundary of a project phase, before moving on or claiming completion, to catch unmet criteria.
allowed-tools: Read Bash Grep TaskList
argument-hint: "<phase-name>"
mode: [review]
---

# Phase Complete

## Purpose

A "phase" is a labeled chunk of multi-step work — typically the unit
that ends with a release tag, a milestone, or an explicit hand-off
to a different team. This skill verifies the exit criteria of the
phase before the lead declares it done. Distinct from per-task
verification (`verify-agent-work`) — phase-complete looks at the
phase as a whole.

## Scope

Operates on a phase definition (typically captured in `decisions.md`
or `plan.md`). Reads the current state of tasks, hooks, and
artifacts. Read-only; produces a go/no-go signal.

NOT in scope: actually closing the phase. The lead does that based on
this skill's output and any judgment calls.

## Automated pass

1. Read the phase's exit criteria from `plan.md` or `decisions.md`.
   If criteria aren't documented, surface "phase has no documented
   exit criteria" and stop — phase completion isn't testable without
   them.
2. For each criterion, verify:
   - **Tasks.** Every task assigned to the phase is `completed`.
     Any "in_progress" or "blocked" tasks block phase-complete.
   - **Hooks.** No outstanding `BLOCK`-severity hook entries since
     the phase started.
   - **Bypasses.** No active hook bypasses (any open bypass is a
     debt to clear before phase close).
   - **Decisions.** `decisions.md` has been updated since the most
     recent task activity. A stale `decisions.md` at phase end means
     the audit trail is incomplete.
   - **Tests.** The full test suite passes (delegate to `test-suite`).
   - **Documentation.** If the phase shipped user-visible changes,
     CHANGELOG and relevant docs have entries.
3. Run any phase-specific custom checks declared in the project
   (e.g. PandaOS's release-audit playbook).

## Manual pass

The lead reviews:

- Whether any pre-existing failures are being grandfathered in (and
  whether that's intentional vs accidental).
- Whether the documentation changes match the user-visible changes
  in spirit, not just in form.
- Whether the phase produced any "leftovers" — partial work that
  needs to be re-scoped into the next phase.

## Findings synthesis

```
phase-complete: <phase-name>
  status: <go|no-go>
  tasks:        <n>/<n> completed (<n> blocked, <n> in-progress)
  hooks:        <ok|n outstanding blocks>
  bypasses:     <n active>
  decisions.md: <fresh|stale-by-Xh>
  tests:        <pass|fail>
  docs:         <ok|missing>
  custom:       <list, or "n/a">

Recommendation: <close|address-then-close|defer>
  Items: <list of remaining work>
```

## Known gotchas

- A phase declared "complete" with active bypasses is a phase declared
  complete on technicality, not substance. Close the bypasses (or
  document them as deliberately ongoing) before the phase closes.
- Phase boundaries should be obvious in retrospect. If determining
  whether a piece of work belongs to "this phase" or "the next" is
  ambiguous, that's a sign the phase boundary is too fuzzy.
- This skill reads task list state. If the team has been sloppy about
  marking tasks complete, the skill's output reflects sloppiness, not
  truth. Test 4's "blockedBy is advisory" caveat applies.
- The skill is appropriate for releases, milestones, and major
  feature ships. Don't run it for micro-phases — the overhead exceeds
  the value.
