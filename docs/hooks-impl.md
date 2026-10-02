# Choosing the hook implementation (`--hooks-impl`)

`yakos refresh` decides which implementation `settings.json` wires up for
each hook. Two implementations exist: the bash scripts under
`scripts/hooks/<name>.sh`, and the Go hooks behind `yakos hook run --impl go <name>`.

```
yakos refresh --hooks-impl bash|go|hybrid
```

| Value | What `settings.json` registers |
|---|---|
| `bash` | `${CLAUDE_PROJECT_DIR}/scripts/hooks/<name>.sh` for every hook. Byte-identical to refresh before this switch existed. The explicit escape hatch. |
| `go` | `<yakos> hook run --impl go <name>` for every hook, GoReady or not. Refresh prints a warning naming the non-GoReady hooks it switched. |
| `hybrid` (default) | `<yakos> hook run --impl go <name>` only for hooks the registry marks `GoReady`; every other hook stays bash. |

Since K-118 the default is `hybrid`. Nothing is persisted for a default run.
Two safety valves keep it from surprising anyone:

- A default run whose `yakos` binary looks temporary (OS temp dir or a
  worktree build) keeps bash and says so on the project's `hooks-impl:` line.
  Only an explicit `--hooks-impl` pins such a path.
- An unreadable settings template also keeps bash; the settings phase then
  reports the template problem itself.

A project with `hooks_impl: bash` persisted keeps bash.

`<yakos>` is the absolute path of the running binary (`os.Executable`,
symlinks evaluated), not a bare `yakos`. Claude Code launched from a GUI
or IDE may not have `yakos` on its `PATH`. A missing command exits 127,
which Claude Code treats as non-blocking, so gates such as `secret-scan`
and `budget-guard` would silently stop enforcing. A path containing shell
special characters is single-quoted.

## Enforcing hooks carry a guard

An absolute path does not help once the binary is gone, broken or replaced.
Claude Code treats every hook exit other than 2 as non-blocking, so a bare
Go command fails open when the binary is missing (127), is a directory
(126), is an empty file (1, or 0 under zsh), dies on a signal (137, 139, or
134 with GOTRACEBACK=crash), or is a `yakos` too old to know `--impl`.

Every Go command that can block or detects something is therefore wrapped:
the registry's `FailClosed` hooks (`path-allowlist` and `secret-scan` in the
default set; `budget-guard`, `supervisor-gate`, `peer-claim`,
`supervisor-ack-gate` and `plan-quality-gate` under `--hooks-impl go`) plus
the `Guard` hooks `task-dependency-gate` and `output-injection-scan`.
Telemetry-only hooks keep the plain command. The wrapper (internal/hookguard,
POSIX sh builtins only, no PATH lookup) does this:

- If the path is not a non-empty, executable, regular file, it runs the bash
  twin refresh deployed (`scripts/hooks/<name>.sh`), which keeps enforcing.
- Otherwise it runs `'<abs>/yakos' hook run --impl go <name>` and passes the
  exit code through only when it is 0 or 2. Any other code (a crash, a signal,
  an incompatible binary) becomes exit 2 with a reason on stderr.

stdin and the Go hook's own stdout and stderr are untouched. Tested under
sh, bash and zsh against every case above, including a real SIGKILL.

Refresh and `yakos doctor` recognize the wrapper exactly; any other shape is
not mistaken for a Go hook.

## A pinned binary must be new enough

`hook run --impl` first shipped in 0.60.0.0. An older binary reads `--impl`
as a hook name, prints "unknown hook" and exits 0, which no wrapper can tell
from a pass. So the version is checked where a binary is chosen or used:

- `yakos refresh` probes a pinned binary that is not the running one. The
  probe runs `hook run --impl go <unregistered name>`: a current binary answers
  exit 2 with "no Go implementation"; an older one answers 0. It asks the
  binary rather than trusting `--version`, which a development build or bare
  install may not be able to print. On a failed probe refresh warns and keeps
  bash for that project, even under an explicit `--hooks-impl go`.
- `yakos doctor <project>` reports an error for such a pinned binary.
- `yakos start` prints a warning for it before the session launches.

A binary that does not exist yet cannot be probed. Doctor and start warn
about it, and the wrapper falls back to bash for enforcing hooks.

## Where the default applies

The hybrid default is a property of the Go `yakos refresh`. With the bash CLI
tree present (a source checkout) and `YAKOS_IMPL` unset, `yakos refresh` is
proxied to the bash `refresh.sh`, which always registers the bash scripts and
rejects `--hooks-impl`. Set `YAKOS_IMPL=go` there to get the hybrid default. A
Go-only install gets it directly. `yakos doctor <project>` prints which path is
active. A refresh from a temporary or worktree binary also stays on bash.

`yakos doctor <project>` prints the mix, for example `10 go (...), 13 bash; 2
fail-closed go hook(s) fall back to their bash twin if the binary is missing`,
and still warns when a pinned binary is missing so you re-run `yakos refresh`.

## Why not every hook

Go starts in about 40 ms. A cheap bash hook (`peer-claim` 16 ms,
`context-inject` 25 ms) is faster in bash. The default moves the hooks where
Go wins and parity holds. `supervisor-stream` is the biggest win (about 149
ms bash, 40 ms Go). Its Go twin now writes the same log record as bash
(`decision`, `reason`, `agent`, `session_id`, `event`), so it is `GoReady` and
runs on Go in the hybrid default. It gets a fallback-only wrapper, not the
fail-closed guard. It is a PostToolUse observer, so exit 2 would only inject
stderr into the model, and the wrapper never exits 2: any non-zero exit of the
Go hook (a panic exits 2) becomes exit 0 with a `WARN yakos exited <rc>` line on
stderr. An unusable binary (missing, directory, empty, not executable) execs the
bash twin instead, because a skipped supervisor-stream starves `supervisor-gate`,
which only reads the findings that stream-launched runs write. A crash after Go
consumed stdin cannot be replayed into bash, so that one call is unsupervised.
Known, accepted difference from bash: malformed envelopes Claude Code cannot
produce (a BOM, a duplicate or missing `tool_name`, an object-valued `command`)
escalate in bash but not in Go. The hook log is otherwise byte-identical to
bash: same fields, same extras order, same `trigger` spelling. A failed
wrapper spawn is reported on stderr only, because bash cannot observe one.
`yakos hook list` shows `go` for parity-verified hooks and
`go-unverified` for the rest.

## The tier is pinned in the command

Every generated command carries `--impl go`. The flag beats the
`YAKOS_HOOKS` environment variable, so enforcement never depends on
ambient environment. Without it the runner defaults to bash mode, which
runs only `lib/hooks-user/<name>.sh` and exits 0 when that file is
absent: a Go-form command run without `YAKOS_HOOKS=go` was a silent
no-op for every gate (fail-open). The first `--hooks-impl go|hybrid`
release (#288) generated that flag-less form.

Under `hybrid` the per-command flag is still `--impl go`: hybrid decides
which hooks are rewritten, and a rewritten hook always runs in Go.
`--impl` also accepts `bash` and `hybrid` for manual use; `YAKOS_HOOKS`
keeps working when the flag is absent. A hook with no Go implementation
run with `--impl` exits 2 with a reason on stderr, as does a repeated
`--impl`. `yakos hook` is always routed to the Go implementation, even
when `YAKOS_IMPL=bash` is exported, because the bash CLI has no `hook`
command and would answer exit 64, which Claude Code treats as
non-blocking.

Projects refreshed with the flag-less form are migrated in place by the
next `yakos refresh --hooks-impl go|hybrid` (or by a persisted
`hooks_impl`), and stay byte-stable after that.

The tradeoff: `settings.json` changes when the binary moves. Re-run
`yakos refresh` after reinstalling to a new location; each command is
rewritten in place. Refresh warns when the resolved path looks temporary
(the OS temp dir or a worktree directory), since such a path may vanish.

## Hybrid list

There is no separate list. `hybrid` uses every registry entry marked
`GoReady` in `cli-go/internal/hooks/registry`, the flag set by the A-1
parity work (`tests/run-hook-parity.sh`). Today that is `cycle-counter`,
`mailbox-mirror`, `output-injection-scan`, `path-allowlist`, `path-log`,
`secret-scan`, `session-end-check`, `task-complete-dispatch`,
`task-dependency-gate`, and `team-lifecycle`.

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
--impl go <name>` must be registered in the Go registry (`yakos hook list`). If one
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

## Plan-quality scoring debounce

`plan-quality-score` is a PostToolUse hook on `Edit|Write|MultiEdit`
that acts only on writes to `work/current/plan.md`. Both tiers (bash
`plan-quality-score.sh` and `yakos hook run --impl go plan-quality-score`)
debounce on the last scored version, not on the file's age (K-112):

- A `plan.md` that has not been scored yet is scored on the triggering
  fire, even though its mtime is only milliseconds old.
- A `plan.md` with the same mtime as the last score is skipped
  (`debounced: plan.md unchanged since the last score`).
- A new version saved under 5 s after the last scoring is scored once, as
  a trailing run (K-110). The first such fire claims the marker directory
  `work/current/.plan-quality-pending`, waits out the rest of the window
  (`debounce: re-save under 5s after the last score; waiting Ns, then
  scoring the latest version`), then scores whatever `plan.md` holds. Any
  other save that fires while the marker is held is skipped
  (`debounced: ... collapsed into the pending trailing score`). A burst
  therefore costs two judge panels, the first save and the last, and the
  last version is always the one scored last. A bad plan saved seconds
  after a good one is no longer missed. The waiting fire holds its tool
  call for at most 5 s. A marker left by a crashed fire is reaped after a
  minute, and one fire that finds the latest version already scored after
  the wait skips it.
- A scoring that fails for infrastructure reasons (scorer missing, exit
  2/3/4, no record) is forgotten, so the next fire retries the same version.

State lives in `work/current/.plan-quality-last-scored` as
`<mtime> <scored-at>` (epoch seconds), shared by both tiers. A missing or
malformed file means "nothing scored yet". The mtime has one-second
resolution, so two saves with different content inside the same second
look like one version; only the first is scored.

History: the earlier rule skipped any `plan.md` whose mtime was under 5 s
old at hook time. The hook runs right after the write that set the mtime,
so that rule skipped every fire and a plan was scored only if the hook was
delayed past 5 s. Measured with the real hook and a mock judge panel: fire
right after a write, no score; the same plan 7 s later, a score. The 5 s
window is not configurable. The fail-closed `plan-quality-gate` reads the
marker, not the mtime, so it is unaffected.

## Scope

The switch is Go-only. The bash refresh (`cli/lib/refresh.sh`, used with
`YAKOS_IMPL=bash`) always registers the bash scripts and does not read
`hooks_impl`.
