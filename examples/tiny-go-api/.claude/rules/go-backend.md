---
name: go-backend
description: Conventions for the tiny-go-api Go server at api/
paths:
  - "cmd/**"
references:
  - rule:git-hygiene
  - rule:commit-format
---

# Go backend conventions (tiny-go-api)

Loaded when Claude reads any file under `cmd/`. These are
project-specific conventions; the framework's cross-project rules
(`git-hygiene`, `commit-format`, `secret-handling`,
`pr-conventions`) apply on top.

## What we use

- `net/http` for routing. Use `http.NewServeMux` rather than the
  default `http.DefaultServeMux` so testing doesn't pollute global
  state.
- `httptest` for handler tests. `httptest.NewRequest` +
  `httptest.NewRecorder` is the canonical pattern.
- `encoding/json` for payloads. JSON-encode directly into the
  response writer; no string formatting.
- Standard `log` package. No structured logging library — too
  heavy for an example.

## What we don't

- **Router frameworks** (chi, gin, echo, fiber). `net/http` is
  enough at this scale.
- **Dependency injection containers.** A handler taking a struct
  field is enough.
- **ORMs.** This example has no database; if it ever did, plain
  `database/sql` + `sqlx` would be the choice.
- **Generics-heavy abstractions.** Concrete types win at this size.
- **Custom error types** beyond what `errors.New` and `fmt.Errorf`
  provide.

## Test conventions

- One `_test.go` file per source file (`main_test.go` next to
  `main.go`).
- Table-driven tests when the variation matters; explicit per-case
  tests when each case has a meaningfully different setup.
- `t.Fatalf` for unrecoverable errors (the test can't continue);
  `t.Errorf` for assertions where the rest of the test still
  produces useful info.

## Anti-patterns

- `fmt.Println` in handlers. Use `log.Printf`.
- Mutable package-level state.
- Missing `Content-Type` headers on JSON responses.
- Returning unwrapped errors from handlers (they should produce a
  meaningful HTTP response, not just propagate).

## Tooling we run before "done"

- `go vet ./...`
- `go test ./... -count=1`
- `gofmt -l .` (output should be empty)

`-race` is overkill for a server with no goroutines yet; add it when
the code grows concurrent.

## Don't expand this rule

This rule is for the tiny example. Adding more Go conventions makes
the rule longer than the code it describes — anti-pattern. Real Go
projects have their own `rules/go-backend.md` with much more depth;
this example is intentionally light.
