---
name: retrospective-discipline
description: When the 10-cycle retro signal fires, the lead dispatches the librarian; humans approve any artifacts. Always-loaded.
references:
  - incident:librarian-self-congratulation-2026-05-22
---

# Retrospective discipline

Always loaded at session start (no `paths:` field; loaded via
CLAUDE.md import — same pattern as `lead-dispatch-discipline.md`).

## What this rule is for

Every 10 user prompts, the `cycle-counter.sh` hook drops a
`.retro-due` marker file into `<work>/current/`. This rule tells
the lead what to do with that marker.

## When to invoke the librarian

On every action where the lead is not mid-specialist-handoff:

1. Check for `<work>/current/.retro-due`.
2. If present: dispatch the `librarian` agent with the task:
   > Run a 10-cycle retrospective per your agent definition. Read
   > the transcript, decisions.md tail, mailbox tail, and hook logs.
   > Write findings to work/current/{lessons,mistakes,skill-candidates,
   > drift-report,soul-proposed-edits}.md. Return a one-line summary.
3. Wait for the librarian's summary.
4. Surface the summary one-line to the operator (e.g., "Retro
   complete: 2 lessons, 1 mistake, 3 skill candidates, no drift").
5. Remove the `.retro-due` marker file.

If a specialist is mid-task: defer the retrospective until the
specialist returns. Don't interrupt active dispatched work.

## What not to do

- **Do NOT skip the retrospective** even if the cycle seems
  "trivial." Drift accumulates across many trivial cycles —
  the cadence is the point.

- **Do NOT modify the librarian's output files yourself.** The
  librarian writes; you supervise. Editing `lessons.md` or
  `skill-candidates.md` directly defeats the audit trail and
  invites the very self-congratulatory pattern the librarian is
  designed against.

- **Do NOT promote skill candidates yourself.** Skill promotion is
  an operator action via `yakos skill promote <slug>`. The lead
  can SURFACE pending candidates to the operator
  ("3 candidates ready for review at cycle 30") but cannot
  promote them.

- **Do NOT approve soul edits yourself.** Soul edits affect future
  sessions' system prompt; only the operator approves via
  `yakos soul approve <slug>`.

- **Do NOT silently delete the `.retro-due` marker without
  dispatching.** If you're going to defer, leave the marker. If
  retrospective failed, surface the failure to the operator and
  leave the marker for the next attempt.

## Bypass

`yakos retro disable` flips a setting that pauses auto-dispatch
(the cycle counter still increments; the librarian is just not
invoked at cycle 10). Re-enable with `yakos retro enable`.

`yakos retro now` is the manual trigger — dispatches the librarian
on demand regardless of cycle count.

## Anti-pattern (from agent contexts)

The most common failure mode for this rule is the lead invoking
the librarian inside an active specialist handoff, interrupting
the work, and producing both:
- A confused specialist (mid-task interruption)
- A confused librarian (incomplete context to retrospect on)

The defer-until-specialist-returns discipline above prevents this.
If you find yourself wondering "should I retro mid-task?" the
answer is always no.

## References

- `incident:librarian-self-congratulation-2026-05-22` — the design
  constraint this rule exists under. Hermes Agent's Curator
  proposed too many skills; yakOS's librarian + this rule + the
  `yakos skill promote` manual gate together address that failure
  mode.
- `rule:lead-dispatch-discipline` — the four-line discipline this
  rule composes with. Retrospective is a lead action; specialists
  don't retro.
