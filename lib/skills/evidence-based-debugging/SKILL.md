---
name: evidence-based-debugging
description: Constrain debugging to cite runtime evidence before proposing fixes
tier: sonnet
invocable_by: lead
domains: [debugging, quality]
version: 1
references:
  - rule:lead-dispatch-discipline
  - playbook:01-security
---

# evidence-based-debugging

## Purpose

When an agent (or the lead) is debugging a failure, this skill
constrains the work to **cite specific runtime evidence** —
stack traces, log lines, variable snapshots, test output,
timestamps — before proposing a fix.

The failure mode this prevents: "patch and pray" — an agent
proposes a plausible-looking fix based on the file structure
without verifying that the proposed change addresses the actual
runtime cause. Often passes review (looks reasonable) and ships
to production where it doesn't fix anything because the real bug
is elsewhere.

Maps to [Syncause/debug-skill](https://github.com/Syncause/debug-skill)
from awesome-harness-engineering's debugging category, and extends it
with the root-cause triage from
[addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `debugging-and-error-recovery`.

## Scope

- Applies to any debugging task: failing test, production
  incident, customer-reported bug, "this used to work" regression
- Output is a structured diagnosis + cited fix proposal
- Does NOT apply to greenfield development (no runtime to cite)
- Does NOT skip writing tests (the fix must come with a test that
  fails before the fix and passes after)

## Root-cause triage

Run this triage to GATHER the cited evidence; it feeds the diagnosis
template below. Each step produces evidence, not a guess.

1. **Reproduce.** Make the failure happen reliably. If you can't
   reproduce it, you can't fix it with confidence — the first finding
   is "not yet reproducible."
2. **Localize.** Narrow WHERE it fails — which layer (UI, API, DB,
   build, external service, test) is responsible.
3. **Reduce.** Create the minimal failing case: strip unrelated code
   and simplify inputs until only the bug remains and the cause is
   obvious.
4. **Fix the root cause.** Ask "why does this happen?" repeatedly until
   you reach the actual cause, not the symptom's location.
5. **Guard against recurrence.** Write a test that catches this
   specific failure (it fails before the fix, passes after).
6. **Verify end-to-end.** Run the specific test, the full suite, the
   build, and any needed manual check before declaring done.

Steps 1–3 generate the cited evidence; steps 4–6 are the fix and its
proof. The diagnosis template below records the output.

## Automated pass

The agent doing the debugging MUST produce a written diagnosis
following this template before proposing changes:

```markdown
## Diagnosis

**Symptom observed:** <one line; cite where you saw it>

**Evidence:**
- <log line / stack trace / test output / etc.>
  Source: <file:line OR command:invocation OR ndjson:ts>
- <next evidence item>
  Source: ...
- <minimum 3 evidence items; cite each>

**Hypothesis:** <one paragraph explaining what's happening,
referencing the evidence above by number>

**Proposed fix:** <one paragraph>

**Test that will validate:** <name + location of the test that
will fail before the fix and pass after>

**Reverse-test (anti-regression):** <one or two paragraphs naming
what could plausibly break with this change, and how the test
suite covers it>
```

If the agent cannot produce 3+ pieces of cited evidence, that
itself is the finding — the symptom is real but the cause is not
yet localized. Recommend instrumentation (add logging, attach
debugger, run with --verbose) rather than guessing.

## Manual pass

Lead invokes this skill before dispatching to a specialist for
debugging work. The specialist is responsible for filling in the
template; the lead reviews the completed diagnosis before
approving the fix.

If a specialist returns a proposed fix WITHOUT the diagnosis
template, the lead refuses and re-dispatches with the template
as a required output.

## Known gotchas

- **Citation creep.** Cite the most specific thing you can:
  `path/file.go:142` is better than "the auth module". A log
  excerpt with a ts is better than "the logs show".
- **Don't synthesize evidence.** If you read the file at 142 and
  didn't see what you expected, cite that as evidence too:
  "expected X at file.go:142, found Y" — the absence of expected
  evidence is itself a finding.
- **Evidence has a freshness date.** A stack trace from 3 weeks
  ago may not reflect current behavior. Re-run if possible;
  otherwise cite the ts and say "stale; reproduced or not?"
- **Three-strikes rule.** If a fix attempt fails twice and the
  evidence still doesn't localize the cause, STOP. Escalate to a
  human or a different specialist with a different perspective.

## When to skip this skill

- Trivial typo fix (no runtime question, evidence is the diff
  itself)
- Greenfield: writing new code with no prior runtime to debug
- The user explicitly says "try the fix without diagnosing first"
  — they're accepting the patch-and-pray risk

## Tier rationale

Sonnet — judgment + synthesis on multi-source evidence (logs +
code + tests). Haiku is too narrow for cross-source correlation.
Opus is overkill unless the bug is genuinely novel.
