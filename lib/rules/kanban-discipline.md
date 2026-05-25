---
name: kanban-discipline
description: The lead maintains <work>/current/kanban.md as a 3-column board reflecting team WIP. Updated automatically by team-lifecycle hooks and manually by the lead. Always loaded.
references:
  - rule:lead-dispatch-discipline
---

# Kanban discipline

Always loaded at session start (no `paths:` field; imported via
CLAUDE.md).

## What this rule is for

`<work>/current/kanban.md` is a 3-column markdown board (TODO /
IN PROGRESS / DONE) reflecting the current team's WIP. It serves
two audiences:

1. **The lead** — visible scratchpad for tracking what's
   dispatched, what's pending, what's complete
2. **The operator** — `yakos kanban` renders it readably; `yakos
   kanban --html` produces a shareable snapshot; `yakos kanban
   serve` opens a live web UI for viewing and managing the board
   (add / move / drag-drop) from a browser

The board is filesystem-shaped (markdown) for the same reasons
all yakOS scratchpad artifacts are: greppable, version-controlled,
human-editable, no new database.

## When the lead writes to it

The lead writes to the board:

- **At dispatch time**: before a `TeamCreate` or `Agent` spawn,
  add a task to TODO. The `team-lifecycle.sh` hook auto-moves
  it to IN PROGRESS when the spawn fires.
- **On significant scope discovery**: when the user adds a new
  requirement mid-session, append to TODO immediately. Don't
  let scope drift accumulate invisibly.
- **On blocker identification**: edit the task's `blockers:`
  field to record what's holding it.
- **Manual completion**: `yakos kanban done <id>` (or edit
  inline) when a task wraps without a TeamDelete trigger.

## When NOT to write to it

- **Don't update mid-tool-call.** The kanban is for between-task
  state. Updating mid-Edit thrashes the file.
- **Don't put internal sub-tasks on the kanban.** It's for the
  WIP visible to the operator, not for the lead's own
  decomposition tracking — that belongs in `plan.md`.
- **Don't move tasks back to TODO from DONE.** If a "done" task
  needs more work, file a new task. The board's history matters
  for the audit trail.

## Auto-updates from hooks

`team-lifecycle.sh` is extended to auto-update the kanban on:

- `TeamCreate` → most recent matching TODO task → IN PROGRESS
- `Agent` (subagent spawn) → if a task is in IN PROGRESS for this
  team, update its `assigned:` field
- `TeamDelete` → most recent IN PROGRESS task → DONE with timestamp

This means the lead's manual maintenance burden is mostly
"create TODO entries and edit blockers." The state transitions
ride along with the lifecycle hooks.

## Anti-pattern (from agent contexts)

The most common failure mode: lead creates 12 fine-grained TODO
entries for what should be one coherent task. The kanban becomes
noise. Heuristic: if a task would take a specialist less than 15
minutes to complete, it doesn't deserve its own kanban entry.
Roll it into the parent task or just dispatch directly.

The second-most-common failure: lead lets DONE accumulate
indefinitely. After ~20 DONE entries, the board is unreadable.
Acceptable patterns: archive DONE on each release; periodically
`yakos kanban move <id> DONE` to a dated section; let the
operator decide.

## References

- `rule:lead-dispatch-discipline` — kanban is a coordination
  artifact; the four-line dispatch discipline still governs WHO
  does WHAT
