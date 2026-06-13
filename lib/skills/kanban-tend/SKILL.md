---
name: kanban-tend
description: Maintain the project kanban — add tasks at dispatch, move on lifecycle events, archive completed work at release time. Use when dispatching work, on a blocker/completion, or when the board needs tidying.
allowed-tools: Read Edit Bash Grep
argument-hint: "[--archive | --add \"<title>\" | --move <id> <col>]"
mode: [orchestrate]
---

# Kanban tend

## Purpose

Encapsulates the lead's kanban-maintenance discipline as a skill.
The lead invokes it at three trigger points:

1. **Pre-dispatch**: before spawning a teammate, add the task to
   TODO so the lifecycle hooks have something to update.
2. **Periodic**: every retrospective cycle, sanity-check the
   board — collapse over-granular tasks, archive stale DONE.
3. **At archive**: before `yakos archive`, the lead invokes this
   skill with `--archive` to move all DONE into a dated section
   so the next session starts clean.

## Scope

Operates on `<work>/current/kanban.md` only. Does not touch
`<project>/`, `~/.claude/`, or any other state.

## Automated pass

Triggered by `team-lifecycle.sh` (in M4 phase):

```sh
# Pseudo-code from team-lifecycle.sh:
case "$tool_name" in
    TeamCreate)
        yakos kanban move "$(last TODO task)" IN-PROGRESS
        ;;
    TeamDelete)
        yakos kanban move "$(last IN-PROGRESS task)" DONE
        ;;
esac
```

The lead doesn't run the automated pass; the hook does.

## Manual pass

The lead invokes one of:

```sh
yakos kanban add "<title>"      # pre-dispatch
yakos kanban move <id> <col>    # manual transition
yakos kanban done <id>          # shortcut to DONE
yakos kanban --archive          # move DONE to dated section
```

Pre-dispatch is the most common: before a `TeamCreate`, the lead
adds the task. The hook fills in the assignee.

## Findings synthesis

Output of the skill is the updated `kanban.md`. The
`<work>/current/logs/team-lifecycle.ndjson` audit trail records
every state transition for retrospective inspection.

If the kanban is becoming noisy (>10 TODO or >15 DONE
without archive), the librarian flags this in the
retrospective's drift-report as a process smell.

## Known gotchas

- **Stale DONE.** A common failure mode is letting DONE
  accumulate without archive. Rule of thumb: archive at the end
  of each release cycle, or any time DONE > 15 entries.
- **Over-granular TODO.** The lead can be tempted to track
  every micro-task on the board. Heuristic: if the task wouldn't
  warrant a `git commit`, it doesn't warrant a kanban entry.
- **Tasks that bounce between columns.** If a task moves DONE →
  IN PROGRESS more than once, that's a signal the task was
  badly scoped. File a new task; preserve the history.
- **Multi-dev mode**: kanban is per-Unix-user in v1 (lives at
  `<work>/current/` which is private). Sharing across operators
  is deferred to a peer-coord extension.
