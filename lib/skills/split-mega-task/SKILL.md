---
name: split-mega-task
description: Decompose an over-large task into reviewable chunks
allowed-tools: Read Grep
argument-hint: "<task-id-or-description>"
mode: [plan]
---

# Split Mega Task

## Purpose

Take a too-large task and propose a decomposition into tasks small
enough for individual specialists to pick up. Companion to `planner`,
which produces the original decomposition; this skill is invoked when
a single task is later realized to be too big.

## Scope

Operates on a single task description or in-progress task that's been
flagged as too big. Produces a proposed sub-task list; doesn't apply
it (the lead approves and uses `TaskCreate` to instantiate).

NOT in scope: replacing `planner`. This is the targeted split, not the
top-of-feature decomposition.

## Automated pass

1. Read the task description and the relevant code/specs.
2. Identify the natural seams in the work:
   - **Layered seams.** Backend, then API contract, then frontend, then
     tests. Often the right axis for full-stack work.
   - **Domain seams.** Auth, then auth integration, then auth UI. When
     the work crosses domains.
   - **Risk seams.** Reversible parts first; irreversible parts behind
     explicit gates. (Migration first, then dependent code.)
   - **Review seams.** Refactor that touches many files first (mostly
     mechanical; reviewable as one), then the new behavior (small
     diff, focused review).
3. Propose 3–5 sub-tasks that cover the original. Each gets:
   - A specific assignee (`agent_type`)
   - A `blockedBy` chain that respects the seams
   - An acceptance criterion
4. Identify any work that doesn't fit cleanly — that's "leftover"
   that may need re-thinking.

## Manual pass

The lead reviews:

- Are the sub-tasks individually small enough? A common failure is
  splitting a big task into fewer-but-still-big tasks.
- Does the seam choice make sense for this codebase? Layered seams
  work in some codebases; risk seams work better in others.
- Is the leftover work suspiciously large? If yes, the original task
  was bigger than thought — re-decompose.

## Findings synthesis

```
split proposal: <original-task>
  axis: <layered|domain|risk|review>
  sub-tasks:
    1. <description> (assignee: <agent>; blockedBy: <list>)
    2. ...
  leftover: <list, or "none">
```

The lead approves the proposal and instantiates the sub-tasks via
`TaskCreate`. The original is updated to "split — see <ids>".

## Known gotchas

- Splitting a task and assigning all sub-tasks to the same teammate
  defeats the purpose. The split should typically distribute work,
  not just add bureaucracy.
- Some tasks resist splitting because they're genuinely atomic. A
  rename across 200 files is one task, not 200; the seam there is
  "review-friendly chunks" but they all assign to the same agent.
- A split that produces >5 sub-tasks is itself suspect. Either the
  original was a multi-feature collection (re-frame at planner level)
  or the splitter is over-decomposing.
- The skill produces a *proposal*. The lead must approve and
  instantiate — split-mega-task does not write to the task list.
