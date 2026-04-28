# Per-domain validators

These scripts are invoked by `task-complete-dispatch.sh` (or manually)
to run domain-specific checks when a teammate completes a task.

## Contract

Each validator:

- Takes an optional `<project-relative-path>` argument (the task's
  primary file). Without an argument, runs project-wide checks.
- Reads `$CLAUDE_PROJECT_DIR` for the project root. Falls back to
  the current working directory if unset.
- Emits one structured JSON record to stdout describing the outcome.
- Exits 0 on PASS, 2 on BLOCK, 0 with `decision: warn` for non-blocking
  warnings (e.g. toolchain missing).

## v0.1 status

The dispatcher (`../task-complete-dispatch.sh`) is REPORT-only in v0.1
because Phase 0 didn't dump the TaskCompleted JSON shape. These
validators are still fully functional for manual invocation:

```sh
CLAUDE_PROJECT_DIR=/path/to/project ./backend-validate.sh
```

When v0.2 lands a confirmed schema for TaskCompleted, the dispatcher
flips to BLOCKING and these validators gate completion.

## Adding a new validator

1. Add `<domain>-validate.sh` here.
2. Add a case in `../task-complete-dispatch.sh` mapping the relevant
   `agent_type` value to your domain.
3. Document the validator's checks in this README.
