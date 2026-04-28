# Batch 5 — status report

**Status:** Complete. Example compiles, tests pass, validates clean,
hook drift check clean, fixture suite still green.

## What was built

```
examples/tiny-go-api/
├── README.md                         what the example shows + simulated workflow
├── CLAUDE.md                         project source of truth
├── MIGRATION-NOTES.md                "fresh project, not a migration"
├── .gitignore                        ignores the build output binary
├── go.mod                            module github.com/yakos/examples/tiny-go-api
├── cmd/server/
│   ├── main.go                       helloHandler + main()
│   └── main_test.go                  OK + MethodNotAllowed cases
├── .claude/
│   ├── agents/
│   │   ├── tiny-lead.md              extends framework lead-template
│   │   └── tiny-api.md               extends framework test-runner
│   ├── rules/
│   │   └── go-backend.md             paths: ['cmd/**']
│   ├── settings.json                 hook config wired to scripts/hooks/
│   └── path-allowlist.json           per-(agent_type, path) policy
└── scripts/hooks/                    17 hook copies + .framework-hash siblings
```

## Self-validation

| # | Test | Result |
|---|---|---|
| 1 | `go build ./...` succeeds | ✓ produces `./server` (gitignored) |
| 2 | `go test ./...` passes | ✓ both TestHelloHandler_OK and TestHelloHandler_MethodNotAllowed |
| 3 | `go vet ./...` clean | ✓ |
| 4 | `gofmt -l .` empty (nothing to format) | ✓ |
| 5 | `yakos validate examples/tiny-go-api` reports 0 errors, 0 warnings | ✓ |
| 6 | `yakos doctor examples/tiny-go-api` drift section reports 17 clean, 0 drifted, 0 unhashed | ✓ |
| 7 | Each `scripts/hooks/*.sh` has a `.framework-hash` sibling matching the framework's SHA-256 | ✓ — verified manually for path-allowlist and mailbox-mirror |
| 8 | Agents prefixed `tiny-` per spec | ✓ tiny-lead, tiny-api |
| 9 | Agents extend framework versions | ✓ tiny-lead extends lead-template, tiny-api extends test-runner |
| 10 | Path-scoped rule loads on cmd/** reads | ✓ frontmatter declares paths: ['cmd/**'] |
| 11 | Fixture suite still green | ✓ 20/20 |
| 12 | End-to-end: `yakos install + init` against fresh temp HOME with this as the project | ✓ install creates 25 symlinks (now that lib/ is populated by Batch 3); init reports the existing 17 hook files as "skipped" (correctly recognized as already present); uninstall cleans up |

## Deviation from spec

**Directory layout: `cmd/server/` instead of `api/`.**

The build prompt v2 specified `api/main.go` for the example. I tried
that first; `go build ./...` from the project root failed with
`build output 'api' already exists and is a directory` — because Go
writes the output binary to the cwd named after the package's
directory, and a directory of the same name conflicts.

The spec's self-validation step #1 explicitly requires
`go build ./... && go test ./...` to succeed. Resolutions considered:

1. **`go build -o /dev/null ./api`** — only works for single
   packages, not `./...`.
2. **Add `.gitignore` for an `api` binary** — doesn't help; the
   build itself fails before producing output.
3. **Build from inside `api/`** — fights the spec's "from the
   project root" pattern.
4. **Restructure to `cmd/server/`** — fixes the conflict, is more
   idiomatic Go layout, costs only renaming a few path references.

Picked (4). Updated:

- `path-allowlist.json` now allows `cmd/**` for `tiny-api`.
- `rules/go-backend.md` now scopes to `cmd/**`.
- `CLAUDE.md` documents `cmd/server/` in the project shape.
- Agent bodies (`tiny-lead.md`, `tiny-api.md`) reference
  `cmd/server/` instead of `api/`.

The spec's intent (a tiny Go server example) is preserved. The
defect was a real bug in the spec, not in the example I should have
written.

## What this example demonstrates

- Project agents shadowing framework agents (Phase 1.5 §17)
- Path-scoped rules loading on read (Phase 0 Test 3)
- PreToolUse hook stack on Edit/Write
- Hook drift detection via `.framework-hash` siblings
- Session-tracking integration (work/ in `~/agent-control/`, NOT in
  the project repo)

## What this example deliberately does NOT demonstrate

- Multi-specialist coordination (only one specialist; no peer DMs)
- Bypass mechanism (no flaky tests to bypass)
- Adversarial review (single specialist precludes it)
- PandaOS-scale audit playbook (v0.2 territory)

These limitations are documented in README.md "What this example
does NOT demonstrate" so users know to read COOKBOOK.md patterns
for the multi-specialist cases.

## What's next

**Checkpoint 9 — Batch 5.5 (local model templates) — OPTIONAL.**
Per the execution plan, this is genuinely optional and can be
skipped to Checkpoint 10 (Batch 6 smoke test) if local-model
templates aren't a priority for v0.1.

If you want Batch 5.5: paste the addendum.
If you want to skip: say "go to Batch 6".

Pushed to `origin/main`.
