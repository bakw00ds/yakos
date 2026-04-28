---
name: verify-agent-work
description: Spot-check a teammate's claimed work before accepting it
allowed-tools: Read Grep Bash
argument-hint: "<teammate-name> [--task <id>]"
mode: [review]
---

# Verify Agent Work

## Purpose

Spot-check what a teammate produced before accepting their "done."
Phase 0 Test 4 surfaced that teammates can drift under peer pressure,
and Test 5 surfaced that tasks can be marked complete while still
blocked. This skill is the cheap, repeatable check that catches both
classes of issue early.

## Scope

Operates on a single teammate's recent work — the diff they introduced
since their last accepted handoff, or a specific task by ID. Read-only;
the skill does not modify the work.

NOT in scope: replacing `code-reviewer`. That's a structured review of
correctness/idiom. This skill is the lighter-weight "did the teammate
actually do what they said?" check.

## Automated pass

1. Identify the teammate's work surface — the file paths they have
   touched per `path-log.ndjson` since the last sync point.
2. For each touched file, read the diff. Compare against the task
   description.
3. Cross-check three claims:
   - **Files touched match task scope.** A teammate assigned to
     `api/internal/foo.go` shouldn't have edits in `web/`. Per
     `rule:git-hygiene` and the path-allowlist.
   - **The change matches the task description.** A task "add
     /v1/foo endpoint" should produce code that adds an endpoint;
     a 1-line config tweak isn't fulfillment.
   - **Tests exist for the new behavior.** A new endpoint with no
     test added is "tested by accident."
4. Read the teammate's `findings.md` section if present. Verify the
   claimed findings exist in the diff.

## Manual pass

The reviewing agent:

- Looks for things absent from the spec the teammate skipped: error
  paths, edge cases, idempotency.
- Asks whether the teammate's commits are atomic and well-named (per
  `rule:commit-format`).
- Checks for over-completion: did the teammate change things outside
  their scope while in the file?

## Findings synthesis

```
verify-agent-work: <teammate>
  Task scope:    <pass|fail|n/a> (touched <n> files; <m> outside scope)
  Description match: <pass|partial|fail>
  Tests for new behavior: <pass|missing|n/a>
  Findings.md claims: <verified|drifted|empty>
  Recommendation:  <accept|partial|reject>
```

`partial` means accept some, reject some — the reviewing agent breaks
out which parts.

## Known gotchas

- A teammate that produced great work but went outside scope is still
  worth flagging. Scope creep is how unrelated changes ship in
  someone else's PR — bad for review and rollback.
- Test absence is a flag, not a refusal. Some changes legitimately
  don't need new tests (refactor with no behavior change). The agent
  judges; the skill surfaces the absence.
- "Tested by accident" is the most-common silent failure: a change
  passes existing tests because no test exercises the new path. Watch
  for changes that pass with no new test additions.
- The skill is read-only by intent. If issues are found, dispatch a
  remediation task; don't fix in place.
