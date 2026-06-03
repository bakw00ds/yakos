# yakOS REST API Contracts

**Source of truth:** `cli-go/internal/restapi/` (server) and `cli-go/internal/restapi/openapi.yaml` (full spec).
**Implemented in:** Phase 2 REST API dispatch (2026-06-03).
**Consumers:** IDE extensions, dashboard, CI integrations.

---

## Transport

- Protocol: HTTP/1.1 over TCP
- Default listen address: `127.0.0.1:7892` (loopback-only)
- Override via `yakos serve --rest-addr <addr>`
- Disable via `yakos serve --rest-addr -`

The REST server starts concurrently with the JSON-RPC socket (`127.0.0.1` Unix socket)
and WebSocket bus (`127.0.0.1:7891`) under `yakos serve`.

---

## Authentication

Two-token model (Phase 2 Q7 decision):

| Token | File | Scope |
|---|---|---|
| Read token | `~/.yakos-state/rest-read-token` | GET endpoints only |
| Write token | `~/.yakos-state/rest-write-token` | GET + POST + PATCH |

Header: `Authorization: Bearer <token>`
- Bearer prefix is case-insensitive
- Both tokens are 64 hex chars (32 random bytes)
- Mode 0600 on disk
- Auto-generated on first daemon start; persist across restarts
- Rotate via daemon flag: `yakos serve --rotate-rest-tokens`

---

## Endpoints

### GET /v1/version

**Auth:** None (public)

**Response 200:**
```json
{
  "version": "0.36.0.0 (go)",
  "runtime": "go1.23.12"
}
```

---

### GET /v1/kanban

**Auth:** Read token

**Response 200:**
```json
{
  "items": [
    {"id": "K-1", "title": "implement health endpoint", "column": "TODO"},
    {"id": "K-2", "title": "write tests", "column": "IN PROGRESS"},
    {"id": "K-3", "title": "design review", "column": "DONE"}
  ]
}
```

`items` is always a JSON array (never null; empty board returns `[]`).

---

### POST /v1/kanban/items

**Auth:** Write token
**Idempotency-Key header:** Optional; server does not deduplicate in Phase 2

**Request body:**
```json
{
  "title": "implement health endpoint",
  "column": "TODO",
  "category": "feat",
  "notes": "optional notes"
}
```

- `title`: required
- `column`: optional, default `"TODO"`; accepted: `"TODO"`, `"IN PROGRESS"`, `"DONE"`
- `category`: optional, default `"other"`
- `notes`: optional

**Response 201:**
```json
{"id": "K-5"}
```

**Response 400:** Missing title or unknown JSON field.

---

### PATCH /v1/kanban/items/{id}

**Auth:** Write token
**Idempotent:** Yes (move to current column is no-op)

**Path parameter:** `id` — kanban item ID (e.g. `"K-5"`)

**Request body** (at least one field required):
```json
{
  "column": "IN PROGRESS",
  "notes": "updated notes"
}
```

`column` accepted values: `"TODO"`, `"IN PROGRESS"`, `"DONE"` (case-insensitive normalization applied).

**Response 200:**
```json
{"ok": true}
```

**Response 404:** Item ID not found on the board.

---

### GET /v1/dispatches

**Auth:** Read token

**Query parameters:**
- `since` (optional): ISO-8601 timestamp lower bound; only events at or after this time are returned.

**Response 200:**
```json
{
  "dispatches": [
    {
      "ts": "2026-06-03T10:22:00Z",
      "agent": "backend",
      "runtime": "claude",
      "exit_code": 0,
      "duration_s": 142.3,
      "model_resolved": "sonnet"
    }
  ]
}
```

`dispatches` is always a JSON array (never null).

---

### POST /v1/dispatches

**Auth:** Write token
**Idempotency-Key header:** Optional; NOT enforced server-side in Phase 2
**NOT idempotent:** each call spawns an agent process

**Request body:**
```json
{
  "agent": "backend",
  "task": "add a /health endpoint",
  "runtime": "claude",
  "model": "sonnet",
  "timeout": 0
}
```

- `agent`: required
- `task`: required
- `runtime`: optional; empty = resolve from agent frontmatter
- `model`: optional; accepted: `"haiku"`, `"sonnet"`, `"opus"`
- `timeout`: optional; 0 = 600s default

**Response 202** (blocking — returns after runtime exits):
```json
{
  "exit_code": 0,
  "duration_s": 142.3,
  "model_resolved": "sonnet"
}
```

**Response 400:** Missing agent or task.
**Response 503:** `yakos_root` not configured on daemon.
**Response 502:** Runtime dispatch error.

---

### GET /v1/cost

**Auth:** Read token

**Query parameters:**
- `by` (optional): aggregation axis. Accepted: `"runtime"` (default), `"agent"`, `"day"`, `"project"`.

**Response 200:**
```json
{
  "by": "runtime",
  "rows": [
    {
      "Key": "claude",
      "Count": 42,
      "OK": 40,
      "Fail": 2,
      "TotalDurationS": 5820.1,
      "TotalInTokens": 128000,
      "TotalOutTokens": 45000
    }
  ]
}
```

`rows` is always a JSON array. `Count=0` → empty rows slice.

**Response 400:** Unknown `by` value.

---

### GET /v1/status

**Auth:** Read token

**Response 200:** Structured project status report (schema matches `internal/status.Report`).
The exact shape is documented in `internal/status/status.go`.

---

### GET /v1/supervise/pending

**Auth:** Read token

**Response 200:**
```json
{
  "pending_count": 2,
  "output": "yakos supervise pending — project: myproject\n\n  Unacknowledged escalation findings...\n"
}
```

---

## Error Envelope

All error responses use:
```json
{"error": "human-readable description"}
```

HTTP status codes:
- `400 Bad Request` — invalid request body or parameters
- `401 Unauthorized` — missing `Authorization: Bearer` header
- `403 Forbidden` — token present but insufficient permissions (read token on write endpoint)
- `404 Not Found` — resource (e.g. kanban item ID) not found
- `500 Internal Server Error` — server-side processing failure
- `502 Bad Gateway` — dispatch runtime error
- `503 Service Unavailable` — daemon not fully configured

---

## Idempotency Summary

| Endpoint | Idempotent | Notes |
|---|---|---|
| `GET *` | Yes | Read-only |
| `PATCH /v1/kanban/items/{id}` | Yes | Set operation |
| `POST /v1/kanban/items` | No | Creates new item per call |
| `POST /v1/dispatches` | No | Spawns agent per call |

---

## Content-Type

All responses are `application/json`. Requests with a body must set
`Content-Type: application/json`.

---

## Library Extracts (pkg/)

Three new public packages added alongside the REST API:

### pkg/refresh

Re-exports `internal/refresh`. Public surface:
- `Config` (type alias)
- `Report`, `ProjectReport`, `HookPhaseReport`, `SettingsPhaseReport`, `AgentPhaseReport` (type aliases)
- `Run(cfg Config) (*Report, error)`
- `CollectProjects(homeDir string) []string`

### pkg/supervise

Re-exports `internal/supervise`. Public surface:
- `Config`, `Result`, `Finding`, `AckRecord` (type aliases)
- `Run(cfg Config) (*Result, error)`
- `PrintHelp(w io.Writer)`

### pkg/agent

Re-exports `internal/agent`. Public surface:
- `Config`, `Result` (type aliases)
- `Run(cfg Config) (*Result, error)`
- `PrintHelp(w io.Writer)`

All three packages follow the same stability promise as existing `pkg/*`
packages: `experimental`, pre-1.0, breaking changes allowed at minor bumps.

---

## Implementation Files

| File | Purpose |
|---|---|
| `cli-go/internal/restapi/server.go` | HTTP server, auth middleware, route registration |
| `cli-go/internal/restapi/handlers.go` | All endpoint handlers |
| `cli-go/internal/restapi/tokens.go` | Read/write token generation and persistence |
| `cli-go/internal/restapi/tokens_test.go` | Token tests (7 tests) |
| `cli-go/internal/restapi/server_test.go` | Handler tests via httptest.NewServer (38 tests) |
| `cli-go/internal/restapi/openapi.yaml` | OpenAPI 3.1 spec |
| `cli-go/internal/restapi/README.md` | Endpoint table + curl examples |
| `cli-go/internal/serve/serve.go` | Daemon integration (RESTAddr + RESTStateDir fields) |
| `cli-go/pkg/refresh/refresh.go` | Public refresh API |
| `cli-go/pkg/supervise/supervise.go` | Public supervise API |
| `cli-go/pkg/agent/agent.go` | Public agent API |
