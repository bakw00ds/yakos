# Choosing the hook implementation (`--hooks-impl`)

`yakos refresh` decides which implementation `settings.json` wires up for
each hook. Two implementations exist: the bash scripts under
`scripts/hooks/<name>.sh`, and the Go hooks behind `yakos hook run <name>`.

```
yakos refresh --hooks-impl bash|go|hybrid
```

| Value | What `settings.json` registers |
|---|---|
| `bash` (default) | `${CLAUDE_PROJECT_DIR}/scripts/hooks/<name>.sh` for every hook. Byte-identical to refresh before this switch existed. |
| `go` | `<yakos> hook run <name>` for every hook, GoReady or not. Refresh prints a warning naming the non-GoReady hooks it switched. |
| `hybrid` | `<yakos> hook run <name>` only for hooks the registry marks `GoReady`; every other hook stays bash. |

`<yakos>` is the absolute path of the running binary (`os.Executable`,
symlinks evaluated), not a bare `yakos`. Claude Code launched from a GUI
or IDE may not have `yakos` on its `PATH`. A missing command exits 127,
which Claude Code treats as non-blocking, so gates such as `secret-scan`
and `budget-guard` would silently stop enforcing. A path containing shell
special characters is single-quoted.

The tradeoff: `settings.json` changes when the binary moves. Re-run
`yakos refresh` after reinstalling to a new location; each command is
rewritten in place. Refresh warns when the resolved path looks temporary
(the OS temp dir or a worktree directory), since such a path may vanish.

## Hybrid list

There is no separate list. `hybrid` uses every registry entry marked
`GoReady` in `cli-go/internal/hooks/registry`, the flag set by the A-1
parity work (`tests/run-hook-parity.sh`). Today that is `cycle-counter`,
`mailbox-mirror`, `path-log`, `session-end-check`, `task-dependency-gate`,
and `team-lifecycle`.

## Persistence

The choice is written to `<project>/.yakos.yml` as a top-level
`hooks_impl:` line. A later `yakos refresh` without the flag keeps it.
Passing `--hooks-impl` overrides the stored value and re-persists it.
Only the value is rewritten, so an inline comment and the file's line
endings are kept.
`--dry-run` never writes it. A default run with nothing stored writes no
`.yakos.yml`.

Each project's line in the refresh output states what was applied and
why, for example `hooks-impl: hybrid (persisted)`. The source is `flag`,
`persisted`, or `default`.

## Fail closed

For `go` and `hybrid`, every hook that would become `yakos hook run
<name>` must be registered in the Go registry (`yakos hook list`). If one
is not, refresh exits non-zero before writing anything, including under
`--dry-run`. It does not fall back to bash.

`go` is deliberately all-in: it also switches hooks that are registered
but not yet `GoReady`. Use `hybrid` to move only parity-verified hooks.

## Switching and ordering

Switching implementations replaces each hook's command in place, so hook
order inside every matcher block is unchanged and no hook is registered
twice. Going `bash` to `go` and back restores the same hook commands in
the same order; byte identity holds for a settings.json that refresh
itself wrote (refresh sorts keys and normalizes formatting). Output is
deterministic: the same inputs and binary path give the same bytes on
every run.

## Scope

The switch is Go-only. The bash refresh (`cli/lib/refresh.sh`, used with
`YAKOS_IMPL=bash`) always registers the bash scripts and does not read
`hooks_impl`.
