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
| `go` | `yakos hook run <name>` for every hook. |
| `hybrid` | `yakos hook run <name>` only for hooks on the parity-verified allowlist; every other hook stays bash. |

The Go-form command uses the bare name `yakos`, resolved through `PATH`
when Claude Code runs the hook. No other generated settings command embeds
an absolute binary path, and a bare name keeps `settings.json` stable
across reinstalls.

## Hybrid allowlist

The list lives in one table, `goReadyAllowlist` in
`cli-go/internal/refresh/hooksimpl.go`. It starts with `cycle-counter`,
`mailbox-mirror`, `session-end-check`, `task-dependency-gate`, and
`team-lifecycle`, the hooks the S-6 parity matrix marks parity-verified
(`work/current/reports/s6-a1-hooks-translator-2026-09-23.md`). A test
asserts every entry is registered and marked `GoReady` in
`cli-go/internal/hooks/registry`.

## Persistence

The choice is written to `<project>/.yakos.yml` as a top-level
`hooks_impl:` line. A later `yakos refresh` without the flag keeps it.
Passing `--hooks-impl` overrides the stored value and re-persists it.
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

## Switching and ordering

Switching implementations replaces each hook's command in place, so hook
order inside every matcher block is unchanged and no hook is registered
twice. Going `bash` to `go` and back reproduces the original bytes.
Output is deterministic: the same inputs give the same bytes on every run.

## Scope

The switch is Go-only. The bash refresh (`cli/lib/refresh.sh`, used with
`YAKOS_IMPL=bash`) always registers the bash scripts and does not read
`hooks_impl`.
