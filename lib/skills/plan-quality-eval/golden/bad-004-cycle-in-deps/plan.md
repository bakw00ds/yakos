# Plan: Add Real-time Order Status WebSocket

## Summary
Add a WebSocket endpoint for real-time order status updates. Estimated: 2 days.

## Assumptions
- Go 1.22; gorilla/websocket v1.5 already in go.mod.
- Redis 7.2 is used for pub/sub between order processors.
- YAKOS_WS_MAX_CONNECTIONS env var limits concurrent connections.
- Frontend team will consume the endpoint (separate task for them).

## Tasks

### T-1: Add WebSocket handler at /ws/v1/orders/{id}/status
agent_type: backend
estimate: 1d
blockedBy: [T-3]
blockedBy_reason: Handler needs the Redis pub/sub channel setup from T-3.
done_means: `TestWebSocketOrderStatus` connects and receives a status update
  within 100ms of a simulated order state change.

### T-2: Add Redis pub/sub publisher in order processor
agent_type: backend
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Publisher must match the channel format the handler subscribes to.
done_means: `TestOrderProcessorPublish` asserts the correct channel name and
  message format are used.

### T-3: Add Redis subscription helper and channel naming conventions
agent_type: backend
estimate: 0.5d
blockedBy: [T-2]
blockedBy_reason: Channel naming convention must be validated against the
  publisher before the handler consumes it.
done_means: `internal/redis/pubsub.go` exported with documented channel
  naming convention.

## Risks
- No irreversible steps. WebSocket connections are stateless. Rollback: remove
  route registration; no DB state affected.
