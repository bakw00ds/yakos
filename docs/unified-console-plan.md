# yakOS Unified Console: Chat REPLs + Flows + Metrics + Kanban + Perf (multi-operator)

## Context

Today yakOS exposes its real-time state through **three separate loopback
dashboards** (kanban `serve`, metricsdash `:7896`, perfdash `:7895`) plus a
daemon (`yakos serve`) that already co-hosts gRPC, a WebSocket event bus, MCP,
and REST. The user asked whether we can (a) replicate the agent REPL as a chat
UI, (b) run a REPL per model, (c) build n8n-style visual orchestration on top,
(d) fold metrics + kanban into one tabbed UI, and (e) let **multiple operators
work out of the same interface**.

Yes to all five, but with one corrected assumption (below). ~85% of the backend
exists. Verified gaps: `Dispatch.Stream` is a proto-declared **stub**
(`grpcserver/server.go:311`); dispatch is **one-shot/buffered** with no token
surface; there is **no DAG engine** (`taskdependencygate` is advisory); the three
dashboards are **separate origins** with inconsistent auth (kanban has *no* token
and is mutational).

Five reviews (security, performance, UX, architecture, + a Fable cross-check)
converged on two load-bearing conclusions:

1. **The stack is identity-blind, and multi-operator is a cross-cutting
   identity/attribution concern, not a feature.** `dispatch.Run` reads
   conversation state from a *process-global env var* (`dispatch.go:147`), so two
   operators' panes would collide. `operator_id` is cheap to carry and expensive
   to retrofit through proto + NDJSON + every payload. **Identity lands first
   (Phase 2), before Chat.**
2. **"Streaming chat" is not just plumbing — the current dispatch path is
   structurally late-binding.** The claude adapter frames every dispatch as *"use
   the Agent tool to dispatch to subagent_type=… return only the final report"*
   (`runtime/claude.go:53-63`). The meaningful text arrives only *after* the
   subagent finishes; `--output-format stream-json` (no
   `--include-partial-messages`) emits whole-message events, not tokens. So Chat
   requires a **new unframed execution mode** (agent definition as system prompt,
   direct `-p`, `--include-partial-messages`) — a new adapter method, not just
   `RunStream`. The first-token latency target is governed by that mode, not by
   bus/flush tuning.

### Multi-user scope (decided)

Two deliberately separated axes:

- **The console/UI is same-host, multiple operators, one loopback daemon.**
  Several browsers / terminals / `ssh -L` tunnels on one machine. All such
  operators are already uid-equivalent on the box, so this adds no privilege —
  the loopback + token model stays intact. Honest limit: identity is
  **self-asserted** (one shared bearer token; `operatorId` is what the browser
  claims). NDJSON attribution is therefore *cooperative labeling for a
  uid-equivalent team*, not authenticated authz — stated plainly in the ADR so
  no future reader mistakes it for a security boundary.
- **Networked collaboration stays the existing multi-instance model.** Multiple
  yakOS *instances*, each its own loopback daemon, coordinating on a shared
  codebase through git + the `work/` artifacts — **not** by exposing this
  loopback console over the network. We do not bind the dispatch/flows console
  non-loopback; doing so would delete the only authentication the system has and
  expose browser-driven, unsandboxed `bypassPermissions` RCE. The `operator_id`
  plumbing built here is the foundation a future networked-identity layer (TLS +
  OIDC/SSO + RBAC + per-user audit) reuses — designed in, not bolted on.

Outcome: **one single-origin tabbed console** at `127.0.0.1:7890` behind one
token — tabs `[ Overview ] [ Chat ] [ Flows ] [ Kanban ] [ Cost ]
[ Performance ]` — shared by same-host operators with live presence/attribution.

## Existing infrastructure to reuse (do not reinvent)

| Need | Reuse | Path |
|---|---|---|
| Node execution unit | `dispatch.Run(ctx, Request)` (non-zero exit → `Result.ExitCode`, **not** `err`) | `internal/dispatch/dispatch.go:29,31` |
| "dispatch → bus events" pattern | `handleDispatchRun` | `internal/serve/methods.go:161` |
| Live fan-out + replay | `wsbus.Bus.Publish`, `/v1/events`, `?since=<seq>` | `internal/wsbus/{bus,event}.go` |
| Loopback + token auth | `RequireLocalHost`, `RequireToken`, `TokenEqual` (constant-time) | `internal/dashauth/dashauth.go` |
| Mountable dashboard handlers | `(*Server).Handler()` | `perfdash/server.go:86`, `metricsdash/server.go:139` |
| mtime+size response cache (copy to perfdash) | `historyCache` | `metricsdash/server.go:66` |
| Embedded-SPA, no build step | `//go:embed dist/` | `perfdash/server.go`, `kanban/serve.go` |
| Atomic markdown/YAML/NDJSON writes | temp-file + rename / append | `kanban/write.go` |
| Daemon subsystem wiring | per-service goroutine + errCh block | `serve/serve.go:192,274` |
| Model tier validation | `runtime.ValidateTier`, `ResolveAlias`, `runtime.Known` | `internal/runtime/` |
| Presence payload (currently unused — wire it) | `PresencePayload` | `wsbus/event.go:86` |

## Collaboration model: **Shared Board + Private Panes**

- **SHARED live state:** Flows graph/YAML, Kanban, Cost, Performance, Overview/
  Activity. Edits propagate to all.
- **PRIVATE-by-default, promotable:** Chat panes. Owned by the operator who
  opened it; others see a presence chip ("P2 · claude/opus · streaming…") but not
  contents until the owner clicks **Share pane** (server enforces: 403 on another
  operator's SSE stream unless shared).
- **Optimistic concurrency, not locks.** Mutable artifacts (Flow YAML, kanban
  card) carry a version stamp on `GET`; `POST` echoes it; server returns **409
  Conflict** on stale write; UI reloads to truth. Advisory `flow.editing`
  presence gives a soft "P2 is editing" lock (consistent with yakOS's advisory-
  gate philosophy). No CRDT/OT.
- Every mutation + WS publish carries `{operatorId, displayName, color}`.

## Recommended approach (phased)

### Phase 1 — Unified console shell (Kanban + Cost + Performance + WS, single origin)

New `cli-go/internal/consoleui/`: `server.go` (mirrors `perfdash/server.go`) +
`dist/{index.html,app.js,styles.css}` (vanilla, no build step). Mounts under one
origin: `/` → SPA; `/kanban/*`, `/cost/*` (metricsdash), `/perf/*` (perfdash) via
`http.StripPrefix` → each `Handler()`; **and `/v1/events` → the wsbus handler
(proxied/mounted at the console origin)** so the Overview tab uses the bus under
the *console* token — not a second cross-port token. (Without this the SPA would
need the separate WS token + the `?token=` query the security section removes.)

**Auth once at the edge:** wrap the *entire* console mux in
`dashauth.RequireLocalHost(consoleAddr)` + a single `RequireToken(consoleToken)`
**before** any `StripPrefix`; **strip inner per-dashboard Host *and* token
middleware**. This closes kanban's current no-token mutational gap (integration
test: `POST /kanban/api/delete` → 401 no-token / 403 bad-Host).

Refactors: extract `Handler()` from `kanban/serve.go`. `serve.go`: add
`ConsoleAddr`(=127.0.0.1:7890)/`NoConsole`/`ConsoleTokenPath`; `consoleErrCh`
goroutine (copy perfdash block); console constructs kanban/metrics/perf `Server`s
and mounts them (start metricsdash here — `serve.Run` doesn't today). `runServe`
(`cmd/yakos/main.go:~5187`): console flags + banner.

### Phase 2 — Identity + dispatch facade  ⟵ the retrofit-expensive slice, first

- **Thread identity through dispatch.** Add `OperatorID/ConversationID/SessionID`
  to `dispatch.Request` (`request.go`), the proto `DispatchRunRequest`, and the
  dispatch-log NDJSON. Replace `os.Getenv("YAKOS_CONVERSATION_ID")`
  (`dispatch.go:147`) with the per-request field. **NDJSON compat is explicit:**
  new fields are additive-optional; readers tolerate absence; the bash
  `dispatch.sh` still writes legacy lines — add a parity test that legacy lines
  parse, and **regenerate the duplicated hook fixtures** (`tests/fixtures/` +
  `cli-go/.../testdata/fixtures/` with their `.framework-hash` sidecars — the
  path-filtered CI hides this drift otherwise).
- **`dispatch.Service` facade.** The `params → Request` mapping is duplicated
  across `serve/methods.go:201`, `grpcserver/server.go:277`, REST. Consolidate so
  identity is stamped — **and the global concurrency governor is enforced** — in
  *one* chokepoint. Putting the governor in the facade (not the console layer)
  means gRPC/REST/MCP dispatches can't bypass it.
- How `operatorId` is minted: console issues it at connect (display name prompt /
  `?as=` / OS user), persisted in localStorage, stamped server-side onto every
  request and event. Spec this in the ADR.

### Phase 2.5 — Overview + live Activity feed (now that identity exists)

`dispatch.*`/`kanban.*` already publish. Overview home tab = a "Now" state panel
(in-flight dispatches who/model/elapsed; running workflows; **operators online
via `PresencePayload`** — wire its publisher, currently none) + an operator-
color-coded, filterable Activity feed. Depends on Phase 2 (presence/attribution
need identity — that's why this follows it). De-risks the WS client Chat/Flows
reuse.

### Phase 3 — Streaming Chat REPLs (per-model panes)

Two prerequisites the review surfaced, both new work beyond `RunStream`:

- **(a) Unframed chat execution mode.** New adapter method (alongside `ExecCmd`)
  that runs the runtime *without* the Agent-tool framing: agent definition as
  system prompt, user text via `-p`, claude with `--include-partial-messages` so
  partial deltas actually stream. This is what makes "chat" feel like a REPL
  rather than "wait minutes, get a report." Codex/agy lack partial streaming →
  degrade to one chunk + `summary`. **Spike each CLI's real streaming behavior
  before building UI affordances.**
- **(b) `internal/dispatch/stream.go`:** `RunStream(ctx, Request, onChunk)`
  mirrors `Run` but step 8 uses `execWithStreaming` (the unframed cmd) attaching
  `cmd.StdoutPipe()`, read with a **bounded `bufio.Reader.ReadBytes('\n')`** (cap
  line length; skip/log over-long) — not a 4MB `Scanner` per stream. stderr
  captured in parallel (preserve `dispatch_finished` `stderr_tail`). On exit emit
  `summary` + call the same `writeStarted/writeFinished` (parity test).

**Transport: a single per-operator SSE stream on the console — not the bus, not
per-pane.**
- *Not the bus:* chat is high-volume point-to-point; routing it through the
  shared bus floods the 1000-event replay ring (evicting lifecycle events) and
  weaponizes the slow-subscriber-drop (`bus.go:100`). Keep the bus for
  *lifecycle/broadcast* only. **Invariant + test: no token/content payloads are
  ever published to the bus.** (This *replaces* the earlier idea of per-subscriber
  bus topic-authz, which becomes dead work once tokens leave the bus.)
- *One stream per operator, multiplexed by `sessionID`* — browsers cap HTTP/1.1
  at ~6 connections/origin on a plain-HTTP loopback server, and per-pane SSE would
  starve the tab at 5-6 panes. Ownership/authz lives on this endpoint (403 on
  another operator's unshared session).
- gRPC `Stream` stays a thin adapter for CLI/programmatic consumers, fed from the
  same `onChunk` channel. `grpcserver/server.go:311`: rewrite → `RunStream`.
  Proto needs no change.
- Coalesce deltas (~50ms / boundary) into batches; on overflow coalesce-drop, never
  drop the session.

**Chat transcript persistence (load-bearing, was missing):**
`work/current/chats/<conversationID>.ndjson`, append-only, written server-side as
chunks emit. Required by: refresh (restore history), **Share-pane** (late joiner
sees prior turns), **export-chat-as-Flow**, and daemon-restart replay (SSE has no
`?since=`). Doubles as the record/replay fixture source.

Console: `POST /api/chat/dispatch` {runtime, model, agent, task, sessionID,
operatorId} → validate `runtime ∈ runtime.Known`, `ResolveAlias`+`ValidateTier`
on model, keep the existing agent-roster check (`dispatch.go:82`) but return a
*generic* 400 (no roster/path leak). **Pin `Project` server-side** to
`WorkspaceRoot`. Track `sessionID → cancelFunc`; pane-close cancels ctx.

UI: **column layout** (≤3 panes), grid+rail beyond; per-pane header with
**dependent runtime→model pickers**, owner badge, live cost-so-far, stop. Monitor-
not-read: typing shimmer + unread counts; `aria-live` for state changes only.

### Phase 4 — Flows engine (headless DAG executor) — gets an ADR

`cli-go/internal/workflow/`: `handleDispatchRun` looped with topo-ordering +
output threading, reusing `dispatch.Run` verbatim (buffered one-shot is *correct*
here — node-level lifecycle events are the right granularity; token streaming
inside a DAG is noise). Workflow dispatches inherit cost/perf/finops + `operatorId`
for free.

Schema — `<work>/current/workflows/<name>.yaml`:
```yaml
version: 1
name: release-audit            # validated ^[a-z0-9][a-z0-9-]{0,63}$ (path-traversal guard)
inputs: { target_branch: main }
nodes:
  - id: security
    agent: security-reviewer
    runtime: claude            # optional; resolves from frontmatter
    model: sonnet              # optional; ResolveAlias+ValidateTier
    timeout: 900               # per-node; surface in schema (600s default bites opus synthesis)
    prompt: "Audit ${inputs.target_branch} for CVEs."
    output_limit: 8000         # MANDATORY total tail-truncate budget fed downstream
  - id: synthesize
    agent: release-manager
    needs: [security, quality] # needs[] IS the edge list; fan-in
    prompt: "Sec: ${nodes.security.output}\nQual: ${nodes.quality.output}"
```
`needs: []` = root; `${nodes.<id>.output}`/`${inputs.<k>}` substituted **only for
declared ids**; `output_limit` enforced as a *total* budget; truncation emits
`workflow.node.truncated`.

Files: `schema.go`, `parse.go`, `validate.go` (unique ids, refs resolve, model
valid, **acyclic** via Kahn + strict `name`/`runID`/node-id path validation +
`filepath.Clean` prefix check), `engine.go` (Kahn → ready-set → `max_parallel`
semaphore default 4 *under the facade governor*; per node: substitute, build the
`methods.go:201`-identical `Request` with `Project` pinned, publish
`node.started`, call `dispatch.Run`, **check `Result.ExitCode != 0` separately
from `err`** — non-zero exit is not a Go error; store truncated output, publish
`node.finished`; failure → node `failed`, dependents `skipped`, run `failed`),
`runstate.go` (`RunState` behind a mutex; persistence **debounced** ~100–250ms;
full output → `runs/<runID>/nodes/<id>.stdout`). New `wsbus` topics
`workflow.{run,node}.{started,finished}` + `.truncated`. `serve/methods.go`:
`yakos.workflow.run` JSON-RPC + `yakos workflow run <name>` CLI (headless entry).
Cache the per-`(yakosRoot,project)` composed roster for a run (`dispatch.go:75`).

**Daemon crash/restart semantics (was absent — required):** a hard kill orphans
in-flight subprocesses (children outlive the daemon; `CommandContext` only fires
on graceful cancel) and loses the last debounced transitions + `writeFinished`.
Add a **startup reconciliation pass**: mark in-flight runs/nodes `unknown`
(resumable); detect orphaned `dispatch_started` without `dispatch_finished` in
the NDJSON. Otherwise resume-from-failure resumes from corrupt state on exactly
the failure mode (daemon death) that interrupts expensive runs.

**ADR decisions:** run ownership (`operatorId`, self-asserted — say so);
resume-from-failure (skip `completed`, re-run `failed`/`skipped`); **output
pinning on resume** (thread the *same* upstream output, not a re-roll); resume
**forks a new `runID` with `parent_run_id`** (preserves audit trail); **resume
pins the workflow YAML hash** — resuming against an edited graph (renamed/re-wired
nodes) is undefined → require identical hash or fail loudly; truncation strategy
node-declarable. Note the self-asserted-identity caveat explicitly.

### Phase 5 — Flows UI (in the console under `/flows/*`)

**Scope = read-only live canvas + YAML-first authoring. The Drawflow drag-edit
editor is an explicit stretch goal, not phase scope** — the DAG *engine* + live-
status view is where the value is; the n8n editor is the lowest value-per-effort
item. Routes fold into the console (single-origin auth): list/get/save (`POST`
validates + atomic-saves with the 409 optimistic-version check),
`POST /flows/api/run` → `Engine.Run` goroutine returns `runID`, poll-fallback,
node stdout. Live: Flows tab subscribes to `workflow.*` on the console WS.

**One canvas, View/Edit modes — not two engines** (avoid the Mermaid-watch vs
Drawflow-edit layout mismatch). Read-only first (Mermaid as *explicitly
temporary*, or locked Drawflow layout). Vendor Mermaid/Drawflow as pinned
`<script>` blobs via `//go:embed` (**record SHA-256, SRI-equivalent checksum
test, one-time license/source review; `govulncheck` won't see JS — dispatch
`supply-chain-auditor`**). Status **icon+label, not color alone**. **YAML is a
first-class accessible alternate view.** **Keyboard kanban moves**, not drag-only.
`prefers-reduced-motion`. **Surface non-determinism honestly:** live per-node +
per-run cost meter, pre-run estimate for opus-heavy graphs, runs as labeled
immutable snapshots, "re-run failed/downstream (reuse outputs)" vs "re-run all".

## Cross-cutting security remediations (human sign-off required pre-merge)

- **Header-only `Authorization: Bearer`**, never a cookie (makes
  `/api/chat/dispatch` structurally CSRF-resistant). On load read `location.hash`,
  `history.replaceState` to strip it, hold in memory. **Stop accepting `?token=`
  in the WS query** (`wsbus/server.go:145` — leaks to logs); first-message /
  `Sec-WebSocket-Protocol` auth.
- **WS `Origin` allow-list** (`wsbus/server.go` checks loopback `RemoteAddr` +
  token but **not `Origin`** → DNS-rebinding hole). Mounting the WS at the console
  origin (Phase 1) makes this fall out.
- Mutational routes require `Content-Type: application/json` (forces a preflight a
  cross-origin attacker can't satisfy); strict CSP + CORP same-origin.
- Route **all** token compares through `dashauth.TokenEqual` (constant-time) —
  `wsbus/server.go:128`, `restapi/server.go:~172` use plain `==`.
- Generic client-facing errors; bound the `?since=` replay window. Any future
  non-loopback bind mirrors kanban's loud off-by-default `--allow-all-interfaces`
  **and hard-disables `/api/chat/dispatch` + `/flows/run`**.

## Performance budgets (confirm against a real trace, don't pre-optimize)

bus/WS-added inter-batch p99 < 60ms; global cap ~24 concurrent panes; dashboard
GET p99 < 150ms cache-hit. (First-token latency is governed by the unframed-mode
spike, Phase 3a — not a fixed budget until measured.) **Copy metricsdash's mtime
cache to perfdash** (`perfdash/server.go:181` re-parses the full log per request).
Keep tokens off the bus. Global governor in the facade. Debounced run.json.

## Testing strategy (was thin)

- **Fake streaming adapter** — a scripted `stream-json` fixture binary that emits
  pre-recorded deltas with delays. Build it in Phase 3 (it's the deferred
  "record/replay" idea, which is really test infra). Without it, the streaming
  parity test + "two panes stream concurrently" are untestable without live LLM
  calls.
- DAG scheduler: `-race` table tests for diamond / multi-root fan-out / fan-in /
  failure-propagation / resume-from-failure / crash-reconciliation.
- Console auth: integration tests for the 401/403 matrix across every mounted
  prefix; the "no content on the bus" invariant test.

## Critical files

| File | Change |
|---|---|
| `internal/consoleui/server.go` (new) | single-origin shell; mounts kanban/cost/perf `Handler()`s + `/v1/events` + `/api/chat/*` + per-operator SSE + `/flows/*`; edge `RequireLocalHost`+`RequireToken` |
| `internal/dispatch/{request.go,dispatch.go}` | identity fields; drop env-var global (`dispatch.go:147`) |
| `internal/dispatch/service.go` (new) | `dispatch.Service` facade — one mapping, identity stamp, global governor |
| `internal/dispatch/stream.go` (new) | `RunStream`/`execWithStreaming` (bounded reader, coalesced) |
| `internal/runtime/claude.go` | new **unframed** chat exec method (`--include-partial-messages`, agent-def-as-system); factor `ParseStreamLine` |
| `internal/wsbus/{server.go,bus.go,event.go}` | Origin check; constant-time compare; owner fields; presence publisher; no-content invariant |
| `internal/workflow/{schema,parse,validate,engine,runstate}.go` (new) | DAG engine; path-safe validation; `ExitCode` check; crash reconciliation; debounced state |
| `internal/grpcserver/server.go:311` | wire `Stream` → `RunStream` |
| `internal/perfdash/server.go:181` | mtime cache (copy `metricsdash/server.go:66`) |
| `internal/serve/{serve.go,methods.go}` | console+metricsdash goroutines; `yakos.workflow.run`; startup reconciliation; identity in NDJSON |
| `internal/kanban/serve.go` | extract `Handler()`; optimistic-409 moves; actor in events |
| `proto/yakos/v1/yakos.proto` | identity fields on `DispatchRunRequest` |
| `cmd/yakos/main.go` (`runServe`) | console flags + banner; `yakos workflow run` |
| `work/current/chats/`, `work/current/workflows/` | new artifact trees |

## Ideas to add (disciplined)

- **Audit/attribution in `dispatch-log.ndjson`** — near-free with Phase 2; the
  multi-user (cooperative) audit trail.
- **Export a chat session as a replayable Flow** — a pane is already a sequence of
  `(agent,model,prompt)` dispatches; serialize the persisted transcript to a
  linear DAG. Unifies Chat+Flows (post-P4).
- **Live per-pane cost + per-operator budget ceiling** tripping the
  `sessionID → cancelFunc` (`cost.Aggregate` exists).
- **MCP exposure of console actions** — *deferred*; authority amplification, gate
  behind the write-token, attribute as `operator_id=mcp:<agent>`.

## Riskiest unknowns + de-risking

1. **Unframed streaming behavior per runtime (highest).** Only claude supports
   partial deltas; the framed default doesn't stream meaningfully. *Spike the
   unframed mode (3a) on each CLI before UI work.*
2. **Identity retrofit cost.** Land Phase 2 first; the field is cheap to carry,
   expensive to insert.
3. **Crash/orphan + resume-vs-edited-YAML.** Startup reconciliation + YAML-hash
   pin; resume forks a new `runID`.
4. **Flows non-determinism / partial expensive state.** Mandatory `output_limit`
   + truncation events + resume + cost meters + labeled-snapshot runs.
5. **Scheduler concurrency.** Mutex `RunState`; `-race` tests; `dispatch.Run`'s
   ctx so cancel kills in-flight; governor caps thrash.
6. **Mounted-handler Host/token checks.** Strip inner middleware; one edge gate.
7. **Canvas effort sink.** Read-only + YAML first; Drawflow editor is a stretch.

## Verification

- **Phase 1:** open `http://127.0.0.1:7890/`; three tabs render live over one
  token; Overview WS works at the console origin (no second token); kanban mutate
  rejects without the token; `POST /kanban/api/delete` → 401/403 matrix passes.
- **Phase 2:** a dispatch from browser A is attributed to A in the feed and
  NDJSON; legacy bash-written NDJSON lines still parse (parity test);
  regenerated fixtures pass CI.
- **Phase 2.5:** two browsers show each other in presence; feed is operator-color-
  coded.
- **Phase 3:** the unframed mode streams partial deltas in a claude pane ending in
  `summary` (validated via the fake streaming adapter); `dispatch_finished`
  identical to non-streaming (parity); refresh restores transcript from
  `chats/<id>.ndjson`; operator B gets 403 on A's unshared session; 6+ panes don't
  starve the tab (single multiplexed SSE).
- **Phase 4:** `yakos workflow run release-audit` runs security+quality parallel,
  synthesize gating both; `-race` tests incl. crash-reconciliation; node failure
  detected via `ExitCode`; resume forks a new `runID`+`parent_run_id`, reuses
  pinned outputs, rejects an edited-YAML resume; over-limit emits
  `workflow.node.truncated`.
- **Phase 5:** Flows tab renders + live-colors the DAG (icon+label), 409 on
  concurrent edit, YAML alt-view accessible, keyboard kanban moves, vendored-blob
  checksum test passes.
- Throughout: `go test ./... -race` + `go vet` in `cli-go/`; `govulncheck` green;
  every server loopback-only, crypto/rand 0600 token, header-only Bearer, WS
  Origin-checked; "no content on the bus" invariant test green.
