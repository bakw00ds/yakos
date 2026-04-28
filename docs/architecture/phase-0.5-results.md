# Phase 0.5 — TaskCompleted schema + ~/.claude/tasks/ format

**Status:** TEMPLATE / NOT YET RUN.

This doc is a stub. The probe has been built (see
[`tests/manual/phase-0.5-probe/`](../../tests/manual/phase-0.5-probe/))
but not yet executed in a live session. When you run the probe,
fill in the sections below from the captured data.

This template mirrors the shape of
[`phase-1.7-results.md`](phase-1.7-results.md) so the resulting docs
read consistently.

---

## Questions

1. What's the exact stdin shape of `TaskCompleted` hooks?
2. What's the format of `~/.claude/tasks/<team>/` files?

## Answer

(fill in after probe — single paragraph each, headline conclusion first)

## Tool name confirmation

Phase 0 confirmed `TaskCompleted` fires as a hook event (Test 5).
This probe additionally confirms:

- (fill in) The exact `hook_event_name` string in stdin.
- (fill in) Whether `TaskUpdate` is the underlying tool call, and
  whether wildcard `PreToolUse` captures it.

## Method

1. Built three probe scripts under
   [`tests/manual/phase-0.5-probe/`](../../tests/manual/phase-0.5-probe/):
   - `probe-taskcompleted.sh` — TaskCompleted matcher; full stdin + env capture.
   - `probe-taskcreated.sh` — TaskCreated matcher; same shape.
   - `probe-allpretool.sh` — wildcard PreToolUse for sanity.
2. (fill in) Wired into a throwaway project, ran the operator
   sequence per the playbook README. Captured N events.

## Observations

### TaskCompleted stdin — exact JSON shape (representative event)

```json
(paste a representative captured payload, with project paths
redacted but field structure preserved)
```

### Field-by-field

| Field | Present? | Type | Notes |
|---|---|---|---|
| `session_id` | (Y/N) | string | |
| `transcript_path` | (Y/N) | string | |
| `cwd` | (Y/N) | string | |
| `permission_mode` | (Y/N) | string | |
| `hook_event_name` | (Y/N) | string | exact value |
| `agent_type` | (Y/N) | string | **Critical**: present for teammate-driven updates? Absent for lead-driven? (Phase 1.7 pattern) |
| `task_id` or `task.id` | (Y/N) | ? | Which shape? |
| `blockedBy` | (Y/N) | array? | In stdin or only in tasks/? |
| Task status before/after | (Y/N) | ? | Visible? |

### Lead vs teammate discrimination

(fill in: does the absence of `agent_type` reliably distinguish
lead-driven vs teammate-driven TaskCompleted, mirroring Phase 0
Test 7 / Phase 1.7's findings on Stop and SendMessage?)

### Env vars visible to the hook script

```
(paste env capture)
```

Compare against Phase 0 Test 7's enumeration. Notable presence /
absence:

- `CLAUDE_CODE_AGENT` — (Phase 1.7 found this missing for in-team
  SendMessage; expect same here)
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS` — (expect set)
- (anything new)

### `blockedBy` enforcement at hook time

Phase 0 Test 4 found `blockedBy` is advisory at the runtime level
— TaskUpdate accepts state transitions regardless of blocker
state. The probe's t4 fixture (a task with a non-existent blocker
that gets completed anyway) confirms / clarifies whether the hook
sees this. Findings:

- (fill in) Does the TaskCompleted hook receive any indication that
  blockers are unmet?
- (fill in) Or does the hook need to read tasks/ to compute it
  itself?

This distinction is the load-bearing input for `task-dependency-gate.sh`'s
upgrade path.

## Observations — `~/.claude/tasks/<team>/` format

### Directory layout

```
(paste ls output of the snapshot)
```

### Per-file schema

For each file, paste a representative example:

```
(paste contents — file name, then content)
```

### How `blockedBy` is represented

(fill in)

### How state transitions are persisted

(fill in: is there a history? Or only current state?)

### Concurrency model

(fill in if observable: does the runtime hold a lock? Are writes
atomic? Is there a race between hook fire and tasks/ update?)

## Implications for YakOS

### `task-dependency-gate.sh` BLOCKING upgrade

(fill in: feasible? what's needed?)

- **If `blockedBy` is in stdin:** the hook can decide directly. Replace
  the REPORT-only marker with: read `blockedBy`; for each blocker, look
  up status in `~/.claude/tasks/<team>/<blocker>.json`; exit 2 if any
  blocker isn't `completed`. Done.
- **If `blockedBy` is only in tasks/:** the hook must read the task
  file for the just-completed task to learn its `blockedBy`. Adds one
  file read per fire; still feasible.
- **If `tasks/` doesn't have a stable schema:** UNCLEAR remains.
  Document as a v0.2 architectural blocker.

### `task-complete-dispatch.sh` BLOCKING upgrade

(fill in: feasible? `agent_type` present?)

- **If `agent_type` is in stdin:** flip the routing logic from
  REPORT-only to BLOCKING by replacing the `mode: "report-only"` field
  with actual per-domain validator invocation. The validators
  themselves already exist and exit 0/2 correctly.
- **If `agent_type` is missing:** routing needs a different
  discriminator. Worst case: read tasks/ for the just-completed task,
  pull the assignee from there.

### Hook-side concurrency considerations

Phase 0 Test 4's "the read isn't transactional" caveat applies. (fill
in any new findings.)

## Confidence

(fill in: percentage + residual uncertainty)

Same shape as Phase 1.7: state confidence in plain language ("~90%
on outcome A; outcome B has the residual uncertainty around X").

## What stays UNCLEAR after this probe

(fill in if anything; ideally empty, since the probe was designed
to close the two specific questions Phase 0 didn't.)

## Files left for inspection

```
$PROBE_PROJECT/work/probe/taskcompleted-*.json   — N files
$PROBE_PROJECT/work/probe/taskcreated-*.json     — N files
$PROBE_PROJECT/work/probe/allpretool.ndjson      — combined PreToolUse log
$PROBE_PROJECT/work/probe/tasks-snapshot/        — captured tasks dir
```

After filling in this doc, archive the probe data alongside the
results so future v0.2 work can re-verify if needed:

```sh
mv "$PROBE_PROJECT/work/probe" \
   ~/github/archive/yakos/phase-0.5-probe-$(date -u +%Y-%m-%d)/
```
