# wsbus — WebSocket event bus

Internal package providing the Phase-2 multi-dev coordination WebSocket surface.

## Endpoint

```
ws://127.0.0.1:7891/v1/events
```

Default port 7891 (override with `yakos serve --ws-addr <addr>`).

## Auth model

Two-tier per the Phase-2 design decisions (Q2):

| Use case | Mechanism |
|---|---|
| Same-machine | Bearer token in `~/.yakos-state/ws-token` (mode 0600, 256-bit random hex). Rotate with `yakos serve --rotate-ws-token`. |
| Cross-machine | mTLS only (deferred to Phase-2 follow-up per Q2 decision). |

Token is presented as:
- `Authorization: Bearer <token>` header on the WS upgrade request, OR
- `?token=<token>` query parameter (fallback for clients that cannot set headers).

Non-loopback connections are rejected with HTTP 403 before token validation.

## Topic schema

All event types are JSON objects with the envelope:

```json
{
  "seq":   <monotonic int64>,
  "topic": "<topic-string>",
  "ts":    "<RFC3339 UTC>",
  "payload": { ... }
}
```

`seq` is per-bus monotonically increasing. Clients use gaps to detect dropped
events. No replay in Phase 2 (Q8 decision; Phase 3 candidate).

### kanban.added

Emitted when a task is added to the board via the `yakos.kanban.add` RPC.

```json
{
  "seq": 1,
  "topic": "kanban.added",
  "ts": "2026-06-03T14:00:00Z",
  "payload": {
    "id":     "K-5",
    "title":  "implement feature X",
    "column": "TODO"
  }
}
```

### kanban.moved

Emitted when a task moves columns (`yakos.kanban.move` or `yakos.kanban.done`).

```json
{
  "seq": 2,
  "topic": "kanban.moved",
  "ts": "2026-06-03T14:01:00Z",
  "payload": {
    "id":   "K-5",
    "from": "TODO",
    "to":   "IN PROGRESS"
  }
}
```

### dispatch.started

Emitted when a dispatch starts via `yakos.dispatch.run`.

```json
{
  "seq": 3,
  "topic": "dispatch.started",
  "ts": "2026-06-03T14:02:00Z",
  "payload": {
    "agent":   "backend",
    "project": "/path/to/workspace",
    "ts":      "2026-06-03T14:02:00Z"
  }
}
```

### dispatch.finished

Emitted when a dispatch completes (success or failure).

```json
{
  "seq": 4,
  "topic": "dispatch.finished",
  "ts": "2026-06-03T14:05:00Z",
  "payload": {
    "agent":     "backend",
    "project":   "/path/to/workspace",
    "exit_code": 0,
    "ts":        "2026-06-03T14:05:00Z"
  }
}
```

### presence

Emitted by connected clients announcing or updating their presence.
(Phase-2 foundation: structure defined; full presence protocol in Phase-2 follow-up.)

```json
{
  "seq": 5,
  "topic": "presence",
  "ts": "2026-06-03T14:00:30Z",
  "payload": {
    "user":   "alice",
    "host":   "dev-macbook",
    "status": "active"
  }
}
```

Status values: `"active"` | `"idle"` | `"gone"`.

## Connection lifecycle

1. Client dials `ws://127.0.0.1:7891/v1/events` with auth header/param.
2. Server validates token + loopback check.
3. Server streams events to client as newline-delimited JSON objects.
4. Server sends a `{"topic":"ping"}` frame every 15 seconds as a keep-alive.
   Clients may ignore ping frames (they carry no payload data).
5. Client disconnects or server shutdown closes the stream.

There is no HELLO/WELCOME handshake in Phase 2.  Subscriptions are whole-bus
(all topics); per-topic filtering is available in the `yakos events` CLI client.

## Subscription model

Phase 2: clients receive all topics. Server-side topic filtering is deferred to
Phase 3.  The `yakos events --topic` flag performs client-side filtering.

## Overflow policy

Subscribers with full outbound buffers (256 events) are dropped.  The
subscription is removed and no further events are delivered.  This matches the
backpressure design in `docs/go-port-phase2-design.md §8`.

## CLI client

```sh
# Listen to all events:
yakos events

# Filter to kanban events only:
yakos events --topic kanban.*

# Connect to a non-default address:
yakos events --ws-addr 127.0.0.1:7456

# Replay (Phase 3 only):
yakos events --since 5m   # ERROR: not supported in Phase 2 (Q8)
```

## Token rotation

```sh
yakos serve --rotate-ws-token
# Output: ws token rotated: /Users/alice/.yakos-state/ws-token
```

After rotation, existing connected clients will continue until their connection
drops (token is validated at connection time only).  New clients must use the
new token.
