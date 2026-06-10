# AGENTS.md — Go project conventions

This file extends the base AGENTS.md with Go-specific conventions.
All AI coding tools (codex, cursor, openhands, aider) should read this.

## Go conventions (all agents)

- **Testing.** `go test ./...` must pass before any commit. Place
  tests in `_test.go` files alongside the code they test. Table-driven
  tests are preferred.
- **Lint.** `golangci-lint run ./...` must produce no errors. Configure
  enabled linters in `.golangci.yml` at the repo root.
- **SQL.** Parameterized queries only (`$1`, `?`, or named params).
  Never interpolate values into SQL strings.
- **Error handling.** Wrap errors with context using `fmt.Errorf("...: %w", err)`.
  Do not swallow errors silently.
- **Godoc.** Every exported function, type, and constant must have a
  godoc comment. `golint` enforces this.
- **Imports.** Group stdlib, external, internal with a blank line
  between groups (enforced by `goimports`).
- **Build tags.** Use `//go:build` directives, not the older `// +build`.

## yakOS framework conventions

- **Lead is tools-restricted.** Code edits go through dispatched
  specialists, not the lead directly.
- **Specialist agents.** See `.claude/agents/` for project-specific
  agent definitions (`backend.md`, `test-runner.md`, `release-manager.md`).
- **Audit trail.** Every tool call lands in NDJSON logs at
  `<work>/current/logs/`.
