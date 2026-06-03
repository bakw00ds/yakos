# yakOS REST API (`internal/restapi`)

Thin HTTP layer over `pkg/*`, served by the daemon at `127.0.0.1:7892`
(loopback-only) alongside the JSON-RPC socket and WebSocket bus.

## Overview

The REST API targets IDE extensions whose runtime can't speak
JSON-RPC over a Unix socket (e.g. VS Code WebView, browser-based extensions).
It exposes the same operations as the JSON-RPC method surface using plain
HTTP + JSON.

**Base URL:** `http://127.0.0.1:7892` (default; override via `yakos serve --rest-addr`)

## Authentication

Two-token model (Phase 2 design Q7 decision: separate read and write tokens
so the read-only dashboard URL can be shared without granting write access):

| Token | File | Grants |
|---|---|---|
| Read token | `~/.yakos-state/rest-read-token` | GET endpoints |
| Write token | `~/.yakos-state/rest-write-token` | GET + POST + PATCH |

Tokens are 64-char lowercase hex (32 random bytes), auto-generated on first
daemon start at mode 0600. To rotate: `yakos serve --rotate-rest-tokens`.

Pass as HTTP header: `Authorization: Bearer <token>`

## Endpoint Reference

| Method | Path | Auth | Description |
|---|---|---|---|
| `GET` | `/v1/version` | none | Daemon version + Go runtime |
| `GET` | `/v1/kanban` | read | List all kanban items |
| `POST` | `/v1/kanban/items` | write | Create kanban item |
| `PATCH` | `/v1/kanban/items/{id}` | write | Move/update kanban item |
| `GET` | `/v1/dispatches?since=...` | read | List dispatch log events |
| `POST` | `/v1/dispatches` | write | Invoke agent dispatch |
| `GET` | `/v1/cost?by=runtime` | read | Aggregated dispatch cost |
| `GET` | `/v1/status` | read | Project status report |
| `GET` | `/v1/supervise/pending` | read | Unacknowledged supervisor findings |

## curl Examples

Replace `$READ_TOKEN` / `$WRITE_TOKEN` with the values from `~/.yakos-state/`.

```bash
# Read the tokens
READ_TOKEN=$(cat ~/.yakos-state/rest-read-token)
WRITE_TOKEN=$(cat ~/.yakos-state/rest-write-token)

# GET /v1/version (no auth needed)
curl http://127.0.0.1:7892/v1/version

# GET /v1/kanban
curl -H "Authorization: Bearer $READ_TOKEN" \
     http://127.0.0.1:7892/v1/kanban

# POST /v1/kanban/items — create a new TODO item
curl -X POST \
     -H "Authorization: Bearer $WRITE_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"title":"add /health endpoint","category":"feat"}' \
     http://127.0.0.1:7892/v1/kanban/items

# PATCH /v1/kanban/items/{id} — move to IN PROGRESS
curl -X PATCH \
     -H "Authorization: Bearer $WRITE_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"column":"IN PROGRESS"}' \
     http://127.0.0.1:7892/v1/kanban/items/K-5

# PATCH /v1/kanban/items/{id} — move to DONE
curl -X PATCH \
     -H "Authorization: Bearer $WRITE_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"column":"DONE"}' \
     http://127.0.0.1:7892/v1/kanban/items/K-5

# GET /v1/dispatches — list recent dispatches
curl -H "Authorization: Bearer $READ_TOKEN" \
     "http://127.0.0.1:7892/v1/dispatches?since=2026-06-01T00:00:00Z"

# POST /v1/dispatches — invoke an agent (blocking)
curl -X POST \
     -H "Authorization: Bearer $WRITE_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"agent":"backend","task":"add a health check endpoint","runtime":"claude"}' \
     http://127.0.0.1:7892/v1/dispatches

# GET /v1/cost — aggregated cost by runtime (default)
curl -H "Authorization: Bearer $READ_TOKEN" \
     "http://127.0.0.1:7892/v1/cost?by=runtime"

# GET /v1/cost — aggregated cost by agent
curl -H "Authorization: Bearer $READ_TOKEN" \
     "http://127.0.0.1:7892/v1/cost?by=agent"

# GET /v1/status — project status report
curl -H "Authorization: Bearer $READ_TOKEN" \
     http://127.0.0.1:7892/v1/status

# GET /v1/supervise/pending — pending supervisor findings
curl -H "Authorization: Bearer $READ_TOKEN" \
     http://127.0.0.1:7892/v1/supervise/pending
```

## Idempotency

| Endpoint | Idempotent? | Notes |
|---|---|---|
| `GET *` | Yes | Read-only |
| `PATCH /v1/kanban/items/{id}` | Yes | Move is a set operation |
| `POST /v1/kanban/items` | No | Each call creates a new item |
| `POST /v1/dispatches` | No | Each call spawns an agent process |

For non-idempotent POSTs, supply an `Idempotency-Key` header to document
retry intent. The server does not enforce deduplication in Phase 2.

## OpenAPI Spec

Full spec at [`openapi.yaml`](openapi.yaml). IDE extensions can use this to
auto-generate typed clients.

## Rate Limiting

All endpoints inherit the loopback-only default (no external exposure). No
additional rate limiting is applied to the REST surface in Phase 2.

## Token Management

Tokens are auto-generated on first daemon start. They persist across daemon
restarts. To rotate (invalidates any clients using the old tokens):

```bash
yakos serve --rotate-rest-tokens
```

The daemon must be running to rotate tokens. After rotation, update any
IDE extension configuration that holds the old token values.

## Integration with the Daemon

The REST server starts concurrently with the JSON-RPC socket and WebSocket
bus under `yakos serve`. All three share the same daemon process. To disable
the REST server: `yakos serve --rest-addr -`.

Default port assignments:
- JSON-RPC: Unix socket (path derived from workspace hash)
- WebSocket: `127.0.0.1:7891`
- REST API: `127.0.0.1:7892`
