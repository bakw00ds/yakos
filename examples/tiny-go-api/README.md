# tiny-go-api

The YakOS canonical example. A minimal Go HTTP server demonstrating
the framework end-to-end: agents loaded, rules apply, hooks fire,
tests pass, lifecycle commands work — without project-specific
scaffolding beyond what the framework itself provides.

## Why this exists

Every framework benefits from a smallest-possible runnable example.
This is YakOS's: a single endpoint, two tests, no external deps,
two agents, one rule. Just enough to demonstrate the load-bearing
patterns and small enough that "what does YakOS do?" can be answered
in five minutes by reading the code.

For more substantial examples (a real project with full domain
specialists, project-specific skills, audit playbooks) see Phase 8's
PandaOS migration when it lands.

## What's inside

```
tiny-go-api/
├── README.md                  this file
├── CLAUDE.md                  project source of truth (read at session start)
├── MIGRATION-NOTES.md         note: fresh project, not a tmux migration
├── .gitignore                 ignores the build artifact
├── go.mod                     module github.com/yakos/examples/tiny-go-api
├── cmd/server/
│   ├── main.go                helloHandler + main()
│   └── main_test.go           OK + MethodNotAllowed cases
├── .claude/
│   ├── agents/
│   │   ├── tiny-lead.md       extends framework lead-template
│   │   └── tiny-api.md        extends framework test-runner
│   ├── rules/
│   │   └── go-backend.md      paths: ['cmd/**']
│   ├── settings.json          hook config wired
│   └── path-allowlist.json    per-(agent_type, path) policy
└── scripts/hooks/             copies of yakos/lib/hooks/ + .framework-hash siblings
```

## Running it directly (no Claude session)

```sh
cd examples/tiny-go-api
go build ./...           # produces ./server (gitignored)
go test ./...            # passes
go run ./cmd/server      # listens on :8080
curl http://localhost:8080/hello
# {"message":"hello, world"}
```

## Running it through YakOS

From the framework root, init this example as a project:

```sh
cd ~/code/yakos
./cli/yakos install
./cli/yakos init tiny-go-api --project /path/to/this/example
```

Init creates `~/agent-control/tiny-go-api/` and copies the framework
hooks into this example's `scripts/hooks/`. Since this example
already ships hook copies (for visibility), init reports them as
"skipped" — that's expected.

Then start a session:

```sh
cd ~/agent-control/tiny-go-api
claude --add-dir /path/to/examples/tiny-go-api
```

The lead loads with `tiny-lead` overriding the framework
`lead-template`, `tiny-api` overriding `test-runner` (per Phase 1.5
§17). The rule `go-backend.md` fires when Claude reads any file
under `cmd/`.

## Simulated workflow: "add a /goodbye endpoint"

The shape of a typical YakOS session against this example. Imagine
the user asks the lead to add a `/goodbye` endpoint that returns
`{"message":"goodbye, world"}`.

### Step 1 — lead decomposes

The lead reads the ask, then writes 2 tasks:

```
Task #1: implement /goodbye handler in cmd/server/main.go
         assignee: tiny-api
         done means: handler returns {"message":"goodbye, world"} on
         GET /goodbye; route registered in main()

Task #2: add a test for /goodbye
         assignee: tiny-api
         done means: TestGoodbyeHandler_OK passes; coverage exercises
         the new path
```

### Step 2 — lead spawns the team

```
TeamCreate("goodbye-feature")
Agent("tiny-api", "api")    # spawned the project specialist
```

The `team-lifecycle.sh` hook fires twice: once on `TeamCreate`
(writes `.session-started` + history line), once on `Agent` (logs
the spawn).

### Step 3 — `tiny-api` implements

`tiny-api` reads `cmd/server/main.go`. Reading that file triggers
the path-scoped rule `go-backend.md` — its conventions
(`net/http`, no router framework, JSON via `encoding/json`) are now
in `tiny-api`'s context.

`tiny-api` opens an `Edit` against `cmd/server/main.go` to add
`goodbyeHandler` and the route registration. The PreToolUse hooks
fire:

- `path-log.sh` — logs the attempt (always passes).
- `path-allowlist.sh` — checks `tiny-api`'s allow-list. `cmd/**`
  is in `tiny-api`'s allow → passes.
- `secret-scan.sh` — scans the new content for secrets → passes.

Edit applied.

### Step 4 — `tiny-api` adds the test

Same flow, this time editing `cmd/server/main_test.go`. Same hooks
fire, same outcome.

### Step 5 — `tiny-api` runs the suite

`tiny-api` runs `go vet ./... && go test ./... -count=1`.
`PreToolUse` fires for the `Bash` calls but the matcher is
`Edit|Write|MultiEdit` — Bash doesn't trigger `path-allowlist`.

Tests pass. `tiny-api` calls `TaskUpdate` to mark Task #1 and #2
complete. The `task-complete-dispatch.sh` hook fires (REPORT-only
in v0.1: logs the routing decision it would make — backend → would
run `backend-validate.sh` — but exits 0 without enforcing).

### Step 6 — lead synthesizes

Lead reads the diff, approves. Writes to `decisions.md`:

> Added /goodbye endpoint per request. Standard helloHandler shape.
> Tests pass. No deps added.

### Step 7 — session end

Lead exits. `session-end-check.sh` fires: confirms decisions.md is
fresh (just written), no expired bypasses, and writes a
session-summary entry to `sessions.ndjson` (since `team-lifecycle`
already wrote one on TeamDelete, `session-end-check` sees the
marker and skips its own write — idempotency).

## What this example demonstrates

- **Project agents shadow framework agents** (`tiny-lead` over
  `lead-template`, `tiny-api` over `test-runner` — except this is
  the v0.1 prefixed-naming variant; v0.2 will drop the prefix once
  override behavior is fully exercised).
- **Path-scoped rules load on read** (Phase 0 Test 3).
- **PreToolUse hook stack** (path-log + path-allowlist + secret-scan
  in sequence on Edit/Write).
- **Mailbox would mirror DMs** if the team had multiple peers (only
  one specialist here, so no peer DMs in this workflow).
- **Hook drift detection** via `.framework-hash` siblings — `yakos
  doctor /path/to/this/example` reports clean if no edits were made
  to the copies.
- **Session-tracking integration** — `.session-started` /
  `.session-started-history.ndjson` / `sessions.ndjson` get written
  to `~/agent-control/tiny-go-api/work/` (NOT into this repo).

## What this example does NOT demonstrate

- **Multi-specialist coordination.** Only one specialist; no peer
  DMs, no contract handoffs, no `task-dependency-gate` exercise.
  See COOKBOOK Pattern 1 for the multi-specialist case.
- **Bypass mechanism.** No flaky tests or known-tracked issues to
  bypass.
- **Adversarial review.** Single specialist precludes the diagnose-
  vs-fix split that COOKBOOK Pattern 3 demonstrates.
- **PandaOS-scale audit playbook.** v0.2 territory.

## Validating this example

```sh
cd ~/code/yakos
./cli/yakos validate examples/tiny-go-api/
./cli/yakos doctor examples/tiny-go-api/
```

The first checks frontmatter and JSON. The second checks hook
drift against `.framework-hash` siblings. Both should report clean.

```sh
cd examples/tiny-go-api
go build ./...
go test ./...
```

Should compile and pass.

## Not in v0.1

- No `Makefile`, `Dockerfile`, or CI integration. The example is
  intentionally minimal so contributors can clone-and-run.
- No skills directory. The framework's generic skills (`pre-commit`,
  `test-suite`, etc.) suffice; the example doesn't need project-
  specific ones.
- No incident-catalog entries specific to this example. It's never
  had a P-anything incident; it has too few moving parts.
