---
name: logging-discipline
description: Every service emits structured logs with timestamp, severity, component, message; no print/console.log in production; secrets never logged. Loads when profile.standards.logging == true.
paths:
  - "**/.yakos.yml"
  - "**/internal/log/**"
  - "**/src/lib/log/**"
  - "**/src/util/logger.*"
  - "**/logger.go"
  - "**/logger.ts"
  - "**/logger.py"
references:
  - rule:profile-standards
  - rule:secret-handling
---

# Logging discipline

Loads when Claude reads `.yakos.yml` (to check
`profile.standards.logging`) or any project file matching the
logger paths above. Composes with `rule:secret-handling`.

## What this rule is for

If `profile.standards.logging == true`, every service in the
project emits structured logs. yakOS doesn't pick the logging
library; it picks the SHAPE. Required at every entry/exit of a
user-facing operation.

## Required log fields

Every log line includes (at minimum):

- `ts` — ISO-8601 UTC timestamp
- `level` — `debug` | `info` | `warn` | `error` (per
  `profile.logging.levels` in .yakos.yml; default: all four)
- `component` — logical service / module name
- `message` — human-readable summary
- `trace_id` (if request-scoped) — correlation id
- Additional structured fields per call site

## Format

Default: `json` (one JSON object per line). Operator can override
to `text` or `logfmt` via `profile.logging.format` in
`.yakos.yml`. JSON is recommended for any service shipping logs
to a structured store (ELK, Loki, Datadog).

## What's forbidden

- **`print` / `println` / `console.log` / `eprintln!` in
  production code.** Use the configured logger. Test code is
  fine; CLI tools where stdout IS the output are fine; service
  code is not.
- **Secrets in log messages.** Composes with
  `rule:secret-handling`. Tokens, passwords, PII, API keys
  never appear in logs even at debug level.
- **Stack traces without context.** A stack trace dumped without
  request/user/operation context is unactionable; wrap with
  structured fields.
- **Inconsistent levels.** `error` for what's actually `warn`,
  `info` for what should be `debug`, etc. Levels should follow
  the project's defined criteria.

## What's required

- **Every service entry/exit** logs at `info` level.
- **Every recoverable error** logs at `warn` with the failed
  operation + retry decision.
- **Every unrecoverable error** logs at `error` with the
  operation + propagated cause.
- **Long-running operations** log periodic progress at `info`
  (every N items processed, every N seconds, etc.).

## Per-language conventions

- **Go**: stdlib `slog` with JSON handler is the default.
  Loggers passed via context, not module-level globals (except
  for the root logger).
- **Node/TypeScript**: `pino` or `winston` with JSON output.
  Per-module loggers via `parent.child({ component: 'xyz' })`.
- **Python**: stdlib `logging` with `python-json-logger`'s
  `JSONFormatter`. One logger per module via `logging.getLogger(__name__)`.
- **Rust**: `tracing` + `tracing-subscriber` with `json`
  formatter feature.

## Anti-pattern

Most common failure mode: a specialist adds `fmt.Println("got
here")` for quick debugging, ships it, then the line stays in
production for months emitting unstructured noise. Heuristic:
if you typed any of `print` / `println` / `console.log` /
`printf` / `eprintln!` while debugging, search-and-replace
before commit.

## Audit-time enforcement

`lib/playbooks/02-code-quality.md` extension (M1 of
`cross-project-standards-plan.md`) audits for:

- `print` / `console.log` / `println!` in non-test code → P2
- Missing structured fields on long-running entry points → P3
- Secrets in log messages → P0 (cross-references
  `lib/hooks/secret-scan.sh`)
