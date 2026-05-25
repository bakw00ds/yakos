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
from awesome-harness-engineering's debugging category.

## Scope

- Applies to any debugging task: failing test, production
  incident, customer-reported bug, "this used to work" regression
- Output is a structured diagnosis + cited fix proposal
- Does NOT apply to greenfield development (no runtime to cite)
- Does NOT skip writing tests (the fix must come with a test that
  fails before the fix and passes after)

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
