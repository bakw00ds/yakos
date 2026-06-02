# yakOS Go Port — Phase 2 Architectural Spec

**Status:** draft for operator review; expands §4 of `docs/go-port-plan.md`.
**Owner:** rotating lead; per-section sign-off by operator.
**Source of truth:** this file is the design; `go-port-plan.md` remains the roadmap.
**Prereq:** Phase 1 exit criteria met (see plan §3) AND ≥3 weeks operator adoption with no rollback.

---

## 1. Overview + dependency graph

Phase 2 turns the one-shot Go binary into a long-running per-developer process that mediates state, exposes APIs, and coordinates across developers. Six deliverables; not all independent.

```
                 ┌────────────────────────────┐
                 │  yakos serve (daemon)      │   foundation
                 │  internal/daemon            │
                 └─────┬───────────┬──────────┘
                       │           │
        ┌──────────────┘           └─────────────┐
        ▼                                        ▼
  ┌──────────────┐                       ┌──────────────────┐
  │ pkg/* lib    │                       │ Transport layer  │
  │ (extract     │                       │ Unix sock / pipe │
  │  internal/*) │                       │ JSON-RPC framed  │
  └─────┬────────┘                       └──┬───────────────┘
        │                                    │
        │            ┌───────────────────────┼─────────────────┐
        ▼            ▼                       ▼                 ▼
  ┌──────────┐ ┌────────────┐         ┌────────────┐    ┌──────────────┐
  │ REST/    │ │ Native MCP │         │ WebSocket  │    │ Perf         │
  │ gRPC API │ │ server     │         │ multi-dev  │    │ dashboard    │
  └──────────┘ └────────────┘         └────────────┘    └──────────────┘
```

**Build order (sequential within Phase 2):**

| Wk | Deliverable | Depends on | Size |
|---|---|---|---|
| 1–2 | Library extract (`pkg/dispatch`, `pkg/kanban`, `pkg/workdir`) | Phase 1 `internal/*` stable | M ~30h |
| 2–4 | Daemon skeleton + transport (`internal/daemon`, `internal/transport`) | library | M ~40h |
| 4–5 | Native MCP server | daemon + library | M ~50h |
| 5–7 | WebSocket multi-dev | daemon | L ~80h |
| 7–8 | REST + gRPC API | daemon + library | M ~40h |
| 8–10 | Perf dashboard | daemon + WS | M ~30h |

Library lands first so the daemon and all surfaces import the same package — no parallel re-invention.

---

## 2. Daemon process model

### Lifecycle

One daemon per OS user per workspace root. Identity: `(uid, abs(workspace_root))`. If a second daemon for the same identity starts, it exits with code 75 (EX_TEMPFAIL) after detecting the existing PID file.

| Phase | Action |
|---|---|
| start | `yakos serve` writes PID to `$XDG_RUNTIME_DIR/yakos/<hash>.pid`, opens socket, drops READY token to `$XDG_RUNTIME_DIR/yakos/<hash>.ready` |
| run | accept clients; goroutine-per-connection; in-memory state mirror reconciled to disk on every mutation |
| stop | SIGTERM → 5s drain → SIGKILL; on clean stop, fsync state, close socket, remove PID + READY files |
| restart | `yakos serve restart` issues stop + start with a 2s overlap window for new clients |

### Auto-start (CLI ↔ daemon detection)

The CLI does **not** auto-spawn a daemon by default. Auto-start is opt-in via `YAKOS_DAEMON=auto`. Default behavior (`YAKOS_DAEMON=off`) keeps Phase 1 semantics intact.

CLI client connect sequence:

1. Read `YAKOS_DAEMON` (default `off`).
2. If `off` → execute the subcommand in-process (Phase 1 behavior). Done.
3. If `on` → require daemon. Open socket; on connect failure exit 69 (EX_UNAVAILABLE).
4. If `auto` → try socket; on `ECONNREFUSED` or missing file, fall back to in-process; emit `slog` warning at debug level.

Socket activation (systemd `LISTEN_FDS`) is **not** supported in Phase 2. Operators wanting always-on use `launchd` (macOS), `systemd --user` (Linux), or Task Scheduler (Windows) with documented unit files under `docs/integrations/`.

### Detection

- Liveness: connect + send `{"jsonrpc":"2.0","method":"ping","id":1}`; 200ms timeout.
- Version match: daemon's `serverInfo.version` must match CLI's own `version.Read()` major.minor; mismatch → CLI prints upgrade hint and falls back per `YAKOS_DAEMON`.

---

## 3. Transport protocol

**Choice: JSON-RPC 2.0 over Unix domain socket (Linux/macOS) or named pipe (Windows).** Not gRPC for the CLI↔daemon hop.

**Rationale:**
- JSON-RPC keeps wire debugging trivial (`socat - UNIX-CONNECT:...`).
- Zero codegen step; the CLI already uses `encoding/json`.
- gRPC's HTTP/2 framing buys us nothing on a local socket; we save the proto toolchain dependency entirely.
- Phase 2 §7 REST/gRPC layer is a separate surface for IDE extensions; it does NOT replace the CLI transport.

### Framing

Newline-delimited JSON (NDJSON). Each frame is one JSON object terminated by `\n`. No length-prefix; `bufio.Scanner` with a 4 MiB max-token suffices for the largest expected payload (full kanban board diff).

Backpressure: server may pause reads if a client's outbound queue exceeds 256 messages; client treats stale connection as fatal.

### Socket location

| OS | Path |
|---|---|
| Linux | `$XDG_RUNTIME_DIR/yakos/<workspace-hash>.sock` (fallback `/tmp/yakos-<uid>/<hash>.sock`) |
| macOS | `$TMPDIR/yakos/<workspace-hash>.sock` (XDG_RUNTIME_DIR unset on stock macOS) |
| Windows | `\\.\pipe\yakos-<uid>-<workspace-hash>` |

`<workspace-hash>` = first 16 hex chars of SHA-256 of the absolute workspace root path. Stable per workspace; no collisions in practice.

Permissions: 0600 on Unix; named-pipe ACL restricts to the creating SID on Windows.

### Method surface (CLI ↔ daemon)

All methods namespaced `yakos.<domain>.<verb>`. Initial set mirrors `pkg/` library functions 1:1 so the daemon is a thin RPC adapter, not a re-implementation.

```
yakos.kanban.list / .add / .move / .done / .blocker
yakos.dispatch.run / .status / .cancel
yakos.workdir.current / .archive
yakos.supervise.run / .stream
yakos.refresh.run
yakos.system.ping / .shutdown / .version
```

Errors use JSON-RPC 2.0 error objects. Reserved codes:

| Code | Meaning |
|---|---|
| -32000 | workspace mismatch |
| -32001 | concurrent write conflict |
| -32002 | dispatch runtime not available |
| -32003 | state-file corrupt; requires `yakos doctor` |

---

## 4. WebSocket schema (multi-dev coordination)

Separate from the CLI socket. Exposed on TCP because cross-machine is the point.

### Endpoint

`ws://127.0.0.1:7456/v1/events` (loopback default; bind override via `yakos serve --ws-bind 0.0.0.0:7456` — operator must opt in).

Port 7456 = 0x1D20; arbitrary but stable for documentation.

### Authentication

Two-tier:

| Use case | Mechanism |
|---|---|
| Same-machine dev | per-dev token in `$XDG_CONFIG_HOME/yakos/token`; generated at first `yakos serve` run; mode 0600; passed as `Authorization: Bearer <token>` header on WS upgrade |
| Cross-machine | mTLS. Operator generates CA via `yakos serve tls init`; per-dev client certs via `yakos serve tls issue <devname>`; server validates client cert CN against an allowlist in `$XDG_CONFIG_HOME/yakos/peers.yaml` |

No password auth. No OAuth. The mTLS path is the only blessed cross-machine option; "just expose it on the LAN with a token" is documented as supported-but-unrecommended.

### Connection lifecycle

1. Client sends `HELLO` frame with `{devName, workspaceRoot, capabilities[]}`.
2. Server responds `WELCOME` with `{sessionId, serverTime, peers[]}`.
3. Server begins streaming subscribed event types.
4. Client may send `SUBSCRIBE` / `UNSUBSCRIBE` per event type at any time.
5. Heartbeat: server sends `PING` every 15s; client must respond within 5s or be dropped.

### Event types

```
{"type":"kanban.update",       "payload":{"op":"move","id":"T-12","from":"TODO","to":"IN PROGRESS","by":"alice"}}
{"type":"presence.heartbeat",  "payload":{"dev":"alice","status":"active","focus":"feat/billing","kanbanItem":"T-12"}}
{"type":"presence.gone",       "payload":{"dev":"alice","reason":"timeout|disconnect|explicit"}}
{"type":"dispatch.progress",   "payload":{"dispatchId":"d-9f3a","phase":"running|complete|failed","agent":"backend","elapsedMs":12450}}
{"type":"supervisor.finding",  "payload":{"severity":"info|warn|error","subject":"...","evidence":[...]}}
{"type":"workdir.changed",     "payload":{"path":"work/current/decisions.md","by":"alice","rev":"sha256:..."}}
```

### Subscription model

Per-event-type filters with optional predicates (`dev=alice`, `kanbanColumn=IN PROGRESS`). Server-side filter to reduce client decode cost. No persistent subscriptions across reconnects in Phase 2; client must resubscribe.

### Ordering + delivery

Best-effort, at-most-once. Each event carries a monotonic `serverSeq`. Clients may detect gaps but cannot request replay in Phase 2. (Replay is a Phase 3 candidate.)

---

## 5. MCP tool surface

Daemon exposes MCP over **stdio** (for `claude mcp add yakos` clients) AND **streamable HTTP** (for browser-based MCP clients) per plan §4 decision.

`yakos serve mcp stdio` runs a single MCP session bound to the calling process's stdio; it proxies to the running daemon via the JSON-RPC socket. `yakos serve mcp http` is multiplexed by the daemon at `http://127.0.0.1:7457/mcp` (port adjacent to WS for memorability).

### Tools

| Tool | Args (JSON schema, abbreviated) | Returns | Idempotent? |
|---|---|---|---|
| `yakos.dispatch` | `{agent:string, task:string, briefPath?:string, runtime?:"claude"\|"codex"\|"gemini"\|"agy", workspace?:string}` | `{dispatchId, status, logPath}` | No (each call spawns) |
| `yakos.kanban.list` | `{column?:"TODO"\|"IN PROGRESS"\|"DONE", limit?:int}` | `{items:[{id,title,column,assigned,blockers,since}]}` | Yes |
| `yakos.kanban.add` | `{title:string, column?:"TODO", id?:string}` | `{id}` | Conditionally (idempotent if `id` supplied) |
| `yakos.kanban.move` | `{id:string, to:"TODO"\|"IN PROGRESS"\|"DONE"}` | `{ok:bool}` | Yes |
| `yakos.kanban.done` | `{id:string}` | `{ok:bool}` | Yes (move to DONE is idempotent) |
| `yakos.refresh` | `{dryRun?:bool}` | `{changedFiles:[string], settingsDiff?:string}` | Yes when `dryRun=true`; no otherwise (touches symlinks) |
| `yakos.supervise.run` | `{since?:RFC3339, scope?:"recent"\|"full"}` | `{findings:[{severity,subject,evidence}]}` | Yes |

### Error semantics

MCP errors use the JSON-RPC error envelope translated from §3's reserved codes. Tool failures (e.g. dispatch runtime missing) surface as `tool_use_error` with `isError: true` on the tool result — MCP convention; do not collapse to protocol errors.

### Idempotency contract

Documented per-tool above. Where supplied, callers may include an `Idempotency-Key` header / arg on stdio sessions; daemon deduplicates within a 300-second sliding window keyed by `(method, idempotencyKey)`.

---

## 6. Library API shape (`pkg/`)

### Layout

```
pkg/
├── dispatch/      # Dispatch + dispatch-log emission
├── kanban/        # Board parse, mutate, serialize (byte-identical round-trip)
├── workdir/       # work/current resolution, archive
├── supervise/     # Supervisor pass evaluation
├── runtime/       # Runtime resolver (claude/codex/gemini/agy adapters)
├── events/        # WS event types (shared with client SDKs)
└── client/        # Go client for the daemon (JSON-RPC over socket)
```

`internal/*` retains implementation details (parsers, writers, hook contracts). `pkg/*` is the public surface and re-exports only what's stable.

### Stability promise

`pkg/*` follows Go semantic versioning post-Phase-2-GA. Phase 2 ships as `v0.<n>.0` — pre-1.0; breaking changes allowed at minor bumps but each documented in `pkg/CHANGELOG.md`. Operator goal: reach `pkg/* v1.0.0` two minor releases after Phase 2 GA.

`internal/*` carries no compatibility promise; importers outside this module are explicitly unsupported.

### Public surface (illustrative)

```go
package kanban

type Board struct { /* ... */ }
type Item struct { ID, Title, Column, Assigned string; Blockers []string }

func Load(path string) (*Board, error)
func (b *Board) Add(item Item) error
func (b *Board) Move(id, toColumn string) error
func (b *Board) Save(path string) error  // atomic; preserves whitespace

package dispatch

type Request struct { Agent, Task string; Runtime Runtime; WorkspaceRoot string }
type Result   struct { ID string; ExitCode int; LogPath string; StartedAt, EndedAt time.Time }

func Run(ctx context.Context, req Request) (*Result, error)
func Stream(ctx context.Context, req Request, events chan<- Event) (*Result, error)

package client

func Dial(workspaceRoot string) (*Client, error)
func (c *Client) Kanban() KanbanAPI
func (c *Client) Dispatch() DispatchAPI
```

### Godoc template

Every exported symbol gets:
- One-line summary (verb-first for functions, noun phrase for types).
- "Errors:" block listing returned sentinels / wrapped errors.
- "Example:" block with at least one runnable example for top-level functions.
- Stability tag in package doc: `// Stability: experimental | stable | frozen`.

### Example use cases (illustrative — non-binding)

1. **IDE extension (VS Code).** Imports `pkg/client`, subscribes to `events.KanbanUpdate`, renders a tree view in the activity bar. `pkg/client` handles socket location + auth token discovery.
2. **Custom front-end (web).** A team builds a Slack-style alternative dashboard; imports `pkg/kanban` + `pkg/events`; talks to daemon via WS.
3. **CI integration.** Pipeline runs `import "github.com/bakw00ds/yakos/pkg/dispatch"` to fire an audit-agent against a PR; consumes `Result.LogPath` artifact.
4. **Alternative CLI.** Out-of-tree experimenter builds `yk` with a different UX over the same library — no shelling out.
5. **Embedded multiplexer.** A meta-tool runs multiple workspaces in one process; one `pkg/client` per workspace root.

---

## 7. REST API

Thin layer over `pkg/*`, served by the daemon. Aimed at IDE extensions whose runtime can't easily speak JSON-RPC-over-socket (mostly browser-based / WebView extensions).

### Endpoint set (minimal)

| Method | Path | Body | Returns |
|---|---|---|---|
| GET | `/v1/kanban` | — | `{items:[...]}` |
| POST | `/v1/kanban/items` | `{title, column?, id?}` | `{id}` |
| PATCH | `/v1/kanban/items/{id}` | `{column?, blockers?}` | `{ok}` |
| POST | `/v1/dispatch` | `{agent, task, runtime?}` | `{dispatchId, logPath}` |
| GET | `/v1/dispatch/{id}` | — | `{status, exitCode, elapsedMs}` |
| GET | `/v1/workdir` | — | `{currentPath, sessionId}` |
| POST | `/v1/refresh` | `{dryRun?}` | `{changedFiles}` |
| GET | `/v1/healthz` | — | `{ok:true, version}` |

### Authentication

Same per-dev token as WS (Bearer); mTLS optional. CORS allowlist configurable in `peers.yaml` for browser clients (default: deny all origins; operator must opt-in localhost variants).

### gRPC

`pkg/grpc/yakos.proto` generated alongside; same endpoint set; one service `Yakos`. gRPC and REST share the same handler implementation via a generated gateway (recommend `grpc-gateway` only if footprint stays small; otherwise hand-roll the REST layer — gateway is the only third-party heavy hitter we're considering).

### OpenAPI sketch (excerpt)

```yaml
openapi: 3.0.3
info: {title: yakOS Daemon API, version: 0.1.0}
servers: [{url: http://127.0.0.1:7457}]
paths:
  /v1/kanban:
    get:
      operationId: listKanban
      security: [{bearer: []}]
      responses: {'200': {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Board'}}}}}
  /v1/dispatch:
    post:
      operationId: createDispatch
      requestBody:
        required: true
        content: {application/json: {schema: {$ref: '#/components/schemas/DispatchRequest'}}}
      responses: {'202': {description: accepted}}
components:
  securitySchemes:
    bearer: {type: http, scheme: bearer}
```

Full spec ships at `cli-go/api/openapi.yaml`. CI lints with `redocly lint`.

---

## 8. Concurrency model

### Daemon-internal

One goroutine per accepted connection (CLI socket, WS, REST). State mutations funnel through a single `stateLoop` goroutine via channels — the daemon does not lock shared state with mutexes from arbitrary handler goroutines. Pattern:

```
clients ──▶ requestCh ──▶ stateLoop ──▶ broadcastCh ──▶ subscribers
                            │
                            ▼
                        diskWriter (serialized)
```

`stateLoop` owns the canonical `*Board`, in-flight dispatch table, and presence map. Every mutation is processed sequentially; reads either hit the in-memory mirror via a `requestCh` query message (preferred) or a `RLock` on a copy-on-write snapshot (`atomic.Pointer[State]`).

### Concurrent dispatches

Each `yakos.dispatch.run` spawns a supervised child goroutine that owns the runtime CLI subprocess. The dispatch table caps concurrent dispatches at `runtime.NumCPU()` by default; overrideable via `YAKOS_DISPATCH_PARALLEL`. Excess requests queue with FIFO ordering.

Cancellation: dispatches respect `context.Context`. `yakos.dispatch.cancel` sends SIGTERM to the runtime subprocess; SIGKILL after 10s.

### Backpressure

WS subscribers with full outbound buffers (256 messages) are dropped with a `presence.gone(reason=overflow)` broadcast to peers. Slow CLI clients block on socket write; daemon kills clients exceeding 30s write timeout.

---

## 9. State management

### Authoritative store

The on-disk files remain authoritative:

| File | Owner | Write pattern |
|---|---|---|
| `work/current/kanban.md` | daemon when running; CLI when daemon off | atomic temp+rename |
| `work/current/dispatch-log.ndjson` | daemon when running; CLI when daemon off | O_APPEND with file lock; one fsync per batch (max 50ms) |
| `work/current/decisions.md` | operator + lead | daemon never writes; observes via fsnotify and re-broadcasts `workdir.changed` |
| `work/current/notes/*.md` | operator + agents | same as decisions.md |
| `~/.claude/settings.json` | refresh / install commands | atomic temp+rename; daemon mediates if running to serialize writes |

### Atomic-write contract

Every write the daemon performs follows:

1. Write payload to `<path>.tmp.<rand>` in the same directory.
2. `fsync` the temp file.
3. `rename` over the destination (atomic on POSIX; `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING` on Windows).
4. `fsync` the containing directory (POSIX only).
5. Emit `workdir.changed` after the rename completes.

### Cross-process safety

When daemon is running, CLI clients route writes through the daemon (no direct file writes). When daemon is off, CLI acquires an advisory file lock (`flock` POSIX; `LockFileEx` Windows) on `<file>.lock` for the duration of read-modify-write. Daemon also takes the lock for compatibility with CLI-only clients.

### Crash recovery

On startup the daemon:

1. Reads each managed file.
2. Validates kanban round-trip (`Save(Load(x)) == x` byte-for-byte). On mismatch, refuses to start; surfaces via `yakos doctor`.
3. Scans for orphan `<path>.tmp.*` files older than 5 minutes; deletes them.
4. Replays the dispatch-log tail to rebuild in-flight dispatch state; dispatches whose subprocess no longer exists are marked `aborted (crash)`.

No write-ahead log in Phase 2. The dispatch-log NDJSON serves as the audit trail; if a higher-durability story is needed, that's a Phase 3 discussion.

---

## 10. Cross-platform notes

| Concern | POSIX (macOS, Linux) | Windows |
|---|---|---|
| IPC primitive | Unix domain socket | Named pipe |
| Socket address | `$XDG_RUNTIME_DIR/yakos/<hash>.sock` (or `$TMPDIR`) | `\\.\pipe\yakos-<uid>-<hash>` |
| Permissions | 0600 file mode | Pipe ACL: creator SID only |
| Signal for shutdown | SIGTERM → drain → SIGKILL | `CTRL_BREAK_EVENT` via `os/signal`; no SIGKILL equivalent — process termination via `TerminateProcess` |
| File locking | `syscall.Flock` advisory | `LockFileEx` mandatory |
| Atomic rename | `os.Rename` | `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)` |
| Directory fsync | yes | no-op (NTFS doesn't expose; the rename is sufficient) |
| Service management docs | systemd --user (Linux), launchd (macOS) | Task Scheduler; nssm as community alternative |
| WS bind | loopback default; SO_REUSEADDR | loopback default; no SO_REUSEPORT — single-process only |

Filesystem case-sensitivity: workspace-hash uses `filepath.Clean` + lowercased absolute path on macOS/Windows; raw path on Linux. Documented; avoids two daemons fighting over the same directory referenced by differently-cased paths.

---

## 11. Testing approach

### Integration tests (daemon)

- `internal/daemon/integration_test.go` spawns the daemon as a subprocess against a temp `XDG_RUNTIME_DIR`, runs the CLI binary against it, asserts JSON-RPC responses. Tagged `//go:build integration`; runs in CI nightly + on daemon-touching PRs.
- Fixture workspaces under `cli-go/testdata/workspaces/` cover: empty, mid-dispatch, corrupt kanban, locked file.
- Race detector on; `t.Parallel()` for socket tests with per-test temp dirs.

### Contract tests (MCP)

- `cli-go/internal/mcp/contract_test.go` consumes the official MCP test fixtures. Every tool listed in §5 has at least one "happy path" + one "error envelope" assertion.
- Compatibility matrix CI: MCP protocol version 2025-06-18 baseline; track newer revisions when published.

### Parity tests (REST)

- For every CLI subcommand with a REST equivalent (kanban, dispatch, refresh, workdir): run both, diff outputs after canonical normalization (sorted keys, stripped timestamps). Goal: REST surface and CLI surface produce semantically identical state changes.

### WS tests

- `internal/ws/broadcast_test.go` — N synthetic clients connect; one mutates kanban; assert N-1 receive the event within 100ms.
- Auth tests: bad token rejected with 4401-class close; expired mTLS cert rejected with 1015.

### Performance budgets (enforced in CI)

| Operation | Budget |
|---|---|
| daemon cold start to READY | ≤300 ms |
| `yakos.kanban.list` round-trip (daemon hot) | ≤5 ms p95 |
| WS broadcast fan-out, 10 clients | ≤20 ms p95 |
| MCP `tools/list` | ≤10 ms |

---

## 12. Migration path from Phase 1

Phase 1 ships a one-shot binary. Phase 2 adds the daemon without removing the one-shot path.

### Steps

1. **Library extract.** `internal/dispatch` → `pkg/dispatch` etc. CLI subcommands switch their imports. No user-visible change. Tag `v0.<n>.0` mid-Phase-2.
2. **Daemon optional.** `yakos serve` ships; `YAKOS_DAEMON=off` remains the default. Operators opt in by setting `YAKOS_DAEMON=auto` in their shell rc.
3. **Hook compatibility.** Bash hooks continue to invoke `yakos <subcommand>` as one-shot. When daemon is running, `yakos kanban add` from a hook becomes a thin RPC; semantics identical.
4. **Two release tracks.** Same binary, behavior gated by env. No separate "daemon edition."
5. **Bash CLI deprecation.** Unchanged from Phase 1: bash `yakos` remains the reference implementation until Phase 1 exit. Phase 2 does not accelerate or alter deprecation.

### Operator upgrade checklist

```
1. Update binary: install.sh re-run.
2. Verify: yakos --version reports >= 0.<n+1>.0
3. Opt-in to daemon (optional): export YAKOS_DAEMON=auto; yakos serve start
4. Verify daemon: yakos doctor → daemon section reports "running" + uptime
5. (Cross-machine WS): yakos serve tls init && yakos serve tls issue <peer>
6. Roll back: unset YAKOS_DAEMON; yakos serve stop. Phase 1 behavior fully restored.
```

### Coexistence with bash

Bash `yakos` never speaks to the daemon. If both are installed and the daemon is running, bash continues writing directly to kanban.md / dispatch-log.ndjson — the daemon's advisory file locks (§9) serialize the writes. Documented as supported during transition; recommended to pick one impl per workspace once Phase 2 lands.

---

## 13. Open questions for the operator

Each: recommendation + decide-before tag.

1. **Auto-start policy default.** Should `YAKOS_DAEMON` default to `off` (Phase 1 behavior) or `auto` (graceful upgrade)? *Recommendation:* `off`. Operators opt in. Surprise daemons in `ps` output is a bad first impression. *Decide before:* daemon skeleton (week 2).
2. **Cross-machine WS — ship mTLS or bearer-token-only for v0?** *Recommendation:* mTLS only for cross-machine. Bearer is loopback-only. Reduces footgun blast radius. *Decide before:* WS milestone (week 5).
3. **MCP transport set: stdio + streamable HTTP, or also SSE?** *Recommendation:* stdio + streamable HTTP only. SSE is deprecated by the streamable HTTP spec; supporting it preserves a legacy attack surface. *Decide before:* MCP milestone (week 4).
4. **Library extract: same module or new module under `pkg.go.dev`?** *Recommendation:* same module (`github.com/bakw00ds/yakos`). Sub-packages under `pkg/`. Avoids the cross-module version-pinning headache. *Decide before:* library extract (week 1).
5. **gRPC: ship or defer to Phase 2.5?** *Recommendation:* defer. REST + WS covers known IDE-extension use cases. gRPC adds a proto toolchain dep with no concrete consumer. *Decide before:* REST/gRPC milestone (week 7).
6. **Dispatch concurrency cap.** `runtime.NumCPU()` default — too aggressive for laptops also running a dev server? *Recommendation:* default to `max(1, NumCPU()/2)`. Override via `YAKOS_DISPATCH_PARALLEL`. *Decide before:* dispatch table impl (week 3).
7. **Perf dashboard auth.** Same per-dev token, or separate read-only token? *Recommendation:* separate read-only token. Dashboard is the most-likely-to-be-shared URL; reuse risks token leak. *Decide before:* dashboard milestone (week 8).
8. **WS event replay.** Skip in Phase 2, or add a 1000-event ring buffer for reconnect catch-up? *Recommendation:* skip; document as Phase 3 candidate. Best-effort matches operator expectation for presence. *Decide before:* WS milestone (week 5).
9. **Daemon-managed `decisions.md` writes.** Daemon currently observes only — should agents write through daemon for atomicity? *Recommendation:* observe-only in Phase 2. `decisions.md` is operator-owned; routing writes through daemon adds a coordination hop without a current bug to justify it. *Decide before:* state-management impl (week 3).
10. **Windows service install command.** Bundle `yakos serve install --service` that registers a Task Scheduler entry, or document the recipe? *Recommendation:* document the recipe in Phase 2; bundle the helper in Phase 2.5 if operator demand surfaces. *Decide before:* cross-platform polish (week 9).

---

## Change log

- 2026-06-02: Phase 2 design drafted; awaiting operator review post-Phase-1-exit.
