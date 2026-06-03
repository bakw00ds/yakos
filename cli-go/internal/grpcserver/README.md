# internal/grpcserver — gRPC API server (Q5 override)

`grpcserver` implements the yakOS daemon gRPC API (Phase 2, Q5 override).
Five services are exposed over a single TCP listener at `127.0.0.1:7893` by
default.

## Services

| Service | Methods | Auth |
|---------|---------|------|
| `Dispatch` | `Run`, `Stream` | write |
| `Kanban` | `List`, `Add`, `Move`, `Done`, `Watch` | List/Watch: read; Add/Move/Done: write |
| `Cost` | `Aggregate` | read |
| `Status` | `Read` | read |
| `Refresh` | `Run` | write |

## Wire encoding

gRPC + JSON codec (registered via `encoding.RegisterCodec`). This avoids
the `protoc` toolchain dependency while preserving standard gRPC framing,
streaming semantics, and future mTLS compatibility.

Clients must force the JSON codec:

```go
conn, err := grpc.NewClient(addr,
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithDefaultCallOptions(grpc.ForceCodec(grpcserver.JSONCodec{})),
)
```

## Authentication

Two-token model inherited from the REST API:

- **Read token** — grants access to read-only methods (`List`, `Watch`, `Aggregate`, `Read`).
- **Write token** — grants access to all methods; also satisfies read-only checks.

Tokens are passed in gRPC metadata key `"authorization"` as `"Bearer <token>"`.
An absent or wrong token returns `codes.Unauthenticated`.

In production the tokens are loaded from `~/.yakos-state/rest-{read,write}-token`
(same files as the REST API). Rotate via `yakos serve --rotate-rest-tokens`.

## Daemon wiring

`yakos serve` starts the gRPC server alongside JSON-RPC, WS, REST, and
the perf-dashboard:

```
yakos serve --grpc-addr 127.0.0.1:7893   # default; set "-" to disable
```

## Proto definition

Source proto is at `cli-go/proto/yakos/v1/yakos.proto` (documentation and
regeneration guide). Go message types are hand-written in
`proto/yakos/v1/yakos.pb.go`; service descriptors in
`proto/yakos/v1/yakos_grpc.pb.go`. This approach was chosen because
`protoc` is not available in the build environment; if it becomes available,
the hand-written files should be replaced with generated output.

## Rate limiting

Inherits the daemon's connection-level rate limiting. No per-method rate
limiting in Phase 2; add via a unary/stream interceptor if needed.

## Idempotency

| Method | Idempotency |
|--------|-------------|
| `Kanban.List`, `Kanban.Watch`, `Cost.Aggregate`, `Status.Read` | Yes (read-only) |
| `Kanban.Add` | No — each call creates a new task |
| `Kanban.Move`, `Kanban.Done` | Yes — moving to the same column is a no-op |
| `Dispatch.Run`, `Dispatch.Stream` | No — each call invokes a new agent run |
| `Refresh.Run` | Yes when `dry_run=true`; no otherwise |

## Audit log

Every write method publishes an event to the in-process wsbus (`Bus.Publish`)
with the corresponding topic (`dispatch.started`, `kanban.added`,
`kanban.moved`, `dispatch.finished`). The WS event bus records these in its
replay buffer.

## Testing

Tests use a real `net.Listen("tcp", "127.0.0.1:0")` listener and
`grpc.NewClient` with `grpc.ForceCodec(grpcserver.JSONCodec{})`. No bufconn
is used; the real TCP stack is exercised in all 35 tests.

```bash
go test ./internal/grpcserver/
```
