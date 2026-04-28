# tiny-go-api — Claude session source of truth

A minimal Go HTTP server used as the YakOS canonical example. One
endpoint (`GET /hello`), one test file, no external dependencies.

## Project shape

```
cmd/
└── server/
    ├── main.go        — server entrypoint + helloHandler
    └── main_test.go   — TestHelloHandler_OK + TestHelloHandler_MethodNotAllowed

.claude/
├── agents/
│   ├── tiny-lead.md   — project lead (extends framework lead-template)
│   └── tiny-api.md    — Go specialist (extends framework test-runner)
├── rules/
│   └── go-backend.md  — path-scoped: cmd/**
├── settings.json      — hook config
└── path-allowlist.json — per-(agent, path) policy

scripts/hooks/         — copies of yakos/lib/hooks/ with .framework-hash
                         siblings (yakos doctor checks for drift)
```

## Conventions for this project

- **One package, one main.** This example is intentionally tiny; not a
  template for "all Go projects." Real projects use cmd/ and internal/.
- **`go test ./...` is the test command.** No race detector by default
  in the example — the surface is too small for it to matter.
- **No external deps.** Standard library only. Adding any dep requires
  a clear justification (this constraint exists to keep the example
  fast to clone-and-run for new YakOS users).
- **Endpoints return JSON.** No HTML, no templates.

## Don't do

- Don't add a `Makefile`. The point is to demonstrate that YakOS works
  without project-specific scaffolding; `go build` and `go test` are
  the surface.
- Don't add a `Dockerfile`. Same reason.
- Don't reach for a router framework. `net/http` is sufficient.
- Don't expand this example beyond what's needed to demonstrate
  YakOS. If a feature requires demonstration of something the
  framework can't yet handle, surface that as a gap; don't paper over
  it with example complexity.

## Running

```sh
cd examples/tiny-go-api
go build ./...           # produces ./server (gitignored)
go test ./...
go run ./cmd/server      # listens on :8080
curl http://localhost:8080/hello
# {"message":"hello, world"}
```

## YakOS framework specifics for this project

- **Agents prefixed `tiny-`.** Per the YakOS v0.1 decision (Phase 2
  Batch 5 spec), example agents use a `tiny-` prefix to avoid
  collision with framework agents. Once override behavior is fully
  tested in v0.2, the prefix can drop.
- **Hooks copied, not symlinked.** `scripts/hooks/` are copies of the
  framework's `lib/hooks/`. Each copy has a `.framework-hash` sibling
  so `yakos doctor <this-project>` can detect drift. Customizing a
  hook is fine; drift is informational, not error.
- **No project-specific specialists beyond `tiny-api`.** A
  microservice this size doesn't need a planner or doc-writer; the
  framework's generic versions suffice.

## See also

- [README.md](README.md) — what this example shows + simulated workflow
- [MIGRATION-NOTES.md](MIGRATION-NOTES.md) — note that this is a
  fresh project, not a tmux→YakOS migration
- Framework: ../../STYLE.md, ../../docs/team-shapes.md
