---
name: interview-me
description: Close the gap between what was asked and what is actually needed via one-question-at-a-time interviewing, each question carrying your guessed answer, until intent is ~95% clear. Use when a request is underspecified on who/why/success-criteria/constraints, when you're tempted to fill in unstated assumptions, or when the user says "interview me" / "stress-test my thinking" before any building begins.
tier: sonnet
invocable_by: [lead, planner, architect]
domains: [planning, requirements]
version: 1
references:
  - rule:lead-dispatch-discipline
  - playbook:04-docs-architecture
---

# interview-me

## Purpose

Reach ~95% confidence about a request's underlying intent BEFORE work
starts, using one focused question at a time. The output is a
confirmed statement of intent that downstream planning and
implementation can build on.

The failure mode this prevents: building on assumptions. The cost to
switch direction after code exists is ~10x the cost of one more
clarifying question before it does.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `interview-me`. Complements the yakOS `planner` agent: this
skill produces the confirmed intent; the planner decomposes it.

## Scope

- **In:** underspecified asks — unclear on WHO it's for, WHY now, what
  SUCCESS looks like, or what CONSTRAINTS bind; asks phrased by
  convention ("make it scalable") rather than specifics.
- **Out:** unambiguous requests, pure information lookups, and cases
  where the user explicitly prioritizes speed over verification.

## Automated pass

The process, one question at a time:

1. **State a hypothesis with a confidence number.** Your best read in
   one sentence plus an honest 0–100%. Below ~70%, say what's missing.
2. **Ask ONE question with your guess attached.** Single focused
   question + your predicted answer + your reasoning. Then wait.
   Reacting to a wrong guess is faster for the user than generating an
   answer cold, and it surfaces your assumptions.
3. **Listen for "should want" signals.** Pattern-matched answers
   ("scalable", "best practice") are convention, not intent. Probe:
   "If you didn't have to justify this to anyone, what would you
   actually want?"
4. **Restate intent concisely.** Structure: Outcome | User | Why now |
   Success | Constraint | Out of scope. 5–8 lines, in their language.
5. **Confirm explicitly.** Accept only a clear "yes." Reject
   delegation ("whatever you think"), vague endorsement ("sounds
   good"), and silence — reframe as a binary choice.

## Stop condition

Stop when you can predict the user's reaction to the next three
questions. This is testable and has a floor: if several rounds don't
raise confidence, stop and flag that something foundational is
missing — don't loop forever.

## Manual pass

The lead checks the confirmed-intent restate before dispatching
downstream work: is success criteria concrete (not "make it good")?
are constraints named? is "out of scope" stated? An interview that
ends on "sounds good" rather than an explicit yes isn't done.

## Anti-rationalization

| Rationalization | Reality |
|---|---|
| "I'll batch the questions to save round-trips" | Users skim batches; one question gets a real reaction to your hypothesis. |
| "I'll skip my guess and just ask" | Reacting to a wrong guess is faster than generating an answer from scratch. |
| "'Whatever you think' is a green light" | That's delegation, not a decision; reframe as a binary choice. |
| "I'll start building and clarify as I go" | Post-code switching costs ~10x the pre-code clarification. |

## Known gotchas

- **Don't pass the interview to a specialist mid-flight.** This is a
  lead/planner-facing elicitation with the operator in the loop; it's
  not delegated work.
- **Floor exists.** If confidence won't climb, the gap is structural
  (the user doesn't know yet) — escalate that finding, don't keep
  asking.

## Findings synthesis

Output is a confirmed statement of intent (the step-4 restate with
explicit confirmation). Record it where the planner will read it —
typically `work/current/decisions.md` or the task brief — so the
decomposition starts from confirmed intent, not the raw ask.

## Tier rationale

Sonnet — hypothesis-forming and reading "should want" signals are
judgment work. Haiku can't calibrate confidence; Opus is overkill for
a structured interview.
