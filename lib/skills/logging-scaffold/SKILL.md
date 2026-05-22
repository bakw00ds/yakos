---
name: logging-scaffold
description: One-shot scaffold dropping in a structured logger for the project's primary backend language; wires JSON output, sensible defaults, example call sites
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--lang go|node|python|rust]"
mode: [scaffold]
---

# Logging scaffold

## Purpose

Drop in a working structured logger for the project's primary
backend language. Operator runs this once per project. After
that, `rule:logging-discipline` keeps the discipline going.

This skill is the FIRST half of the soft+hard pairing for
Standard 1 (logging). The rule is the second half.

## Scope

Operates on the operator's project repo, NOT on
`~/github/yakOS/`. Adds a logger module + example wiring;
doesn't touch existing log call sites (those evolve via normal
dev work).

NOT in scope:
- Log SHIPPING infrastructure (Loki / Datadog / Splunk / CloudWatch)
- Distributed tracing setup (OpenTelemetry)
- Log rotation / disk management
- Replacing existing custom loggers (operator decides)

## Automated pass

1. Detect primary backend language from project files:
   - `go.mod` → Go
   - `package.json` with `express`/`fastify`/`koa`/`nest` →
     Node/TS
   - `pyproject.toml` or `requirements.txt` with `django`/`flask`/
     `fastapi` → Python
   - `Cargo.toml` with `axum`/`actix` → Rust
   - Otherwise: ask operator via `--lang` flag

2. Read `.yakos.yml` `profile.logging` (format, levels) for
   config overrides.

3. Drop in logger module per language:

### Go variant
- Writes `internal/log/log.go` with `slog`-based logger
- JSON handler; reads `LOG_LEVEL` env (debug|info|warn|error)
- Example wiring in `cmd/server/main.go` if such file exists

### Node/TS variant
- Writes `src/lib/log/index.ts` with `pino` setup
- Adds `pino` to `package.json` dependencies (operator confirms)
- Example wiring in entry-point if detected

### Python variant
- Writes `src/log/__init__.py` with `python-json-logger` setup
- Adds `python-json-logger` to `requirements.txt` /
  `pyproject.toml`
- Example wiring in WSGI/ASGI entry-point

### Rust variant
- Writes `src/log.rs` with `tracing` + `tracing-subscriber`
- Adds dependencies to `Cargo.toml`
- Example wiring in `main.rs`

## Manual pass

After the scaffold runs:

1. Operator reviews the new logger module
2. Operator updates entry-point code to initialize the logger
   (the scaffold provides a template; operator may have
   project-specific bootstrapping)
3. Operator decides whether to migrate existing log call sites
   (scope NOT included)

## Findings synthesis

Output of the skill is the new logger module + a one-paragraph
README addendum:

```markdown
## Logging

This project uses structured logging via <library>. Logs are
JSON-formatted by default; override with `LOG_FORMAT=text` env
var. Log levels read from `LOG_LEVEL` (debug|info|warn|error;
default `info`).

See `<path-to-logger>` for the configuration and
`lib/rules/logging-discipline.md` for the team discipline.
```

Operator integrates the README addendum into the project's
existing README.md.

## Known gotchas

- **Existing custom logger**: scaffold detects and warns. Doesn't
  auto-replace. Operator decides whether to migrate.
- **CLI tools where stdout IS the output**: scaffold offers a
  `--cli-mode` variant where the logger goes to stderr and
  stdout is reserved for tool output.
- **Log rotation**: not handled. Operator wires logrotate /
  systemd journald / pm2 / etc. per their ops setup.
- **Per-request loggers**: scaffold's example shows
  context-propagated loggers (Go: ctx-based; Node: child logger
  via middleware; Python: contextvars). Project's request
  framework determines the exact wiring; scaffold's example
  may need adaptation.
- **Compatibility with existing structured-log shippers**: if
  the project already emits to Datadog via dd-trace, that
  agent wraps the logger; verify before swapping libraries.
