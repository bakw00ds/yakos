---
plan_id: p-test-good-plan-fixture
---

# Add /v1/orders GET Endpoint

A well-structured plan that scores well on all 6 rubric dimensions.

## Assumptions

- Runtime: Go 1.22+, Chi router, PostgreSQL 15.
- Environment: `DATABASE_URL` env var is set; `ORDERS_FEATURE_FLAG=true` must be set to enable.
- Input shape: JWT token in `Authorization: Bearer <token>` header; request has no body.
- DB schema: `orders` table exists with columns `id`, `user_id`, `status`, `created_at` per db-contracts.md v3.
- Frontend already consumes `/v1/orders` via the typed client generated from openapi.yaml.
- External dependency: none for Phase 1; payment webhook is Phase 2.

## Tasks

### T-1: Add orders repository method
agent_type: database
estimate: 0.5 days
blockedBy: []
blockedBy_reason: ""
done_means: >
  `internal/repository/orders.go` exports `ListOrdersByUserID(ctx, userID) ([]Order, error)`
  and the unit test `TestListOrdersByUserID_HappyPath` passes.

### T-2: Implement GET /v1/orders handler
agent_type: backend
estimate: 1 day
blockedBy: [T-1]
blockedBy_reason: handler depends on the orders repository method from T-1
done_means: >
  `internal/handlers/orders.go` exists; `GET /v1/orders` returns 200 with
  `{"orders": [...]}` for authenticated users; returns 401 for unauthenticated requests;
  handler unit tests pass; audit-log entry written on each call.

### T-3: Update OpenAPI spec
agent_type: api-designer
estimate: 0.5 days
blockedBy: [T-2]
blockedBy_reason: spec must reflect the finalized handler response shape
done_means: >
  `api/docs/openapi.yaml` has `/v1/orders` GET entry with response schema, auth
  requirement, and rate-limit class documented; `yakos skill api-diff` shows patch-level
  change only.

### T-4: Wire integration test
agent_type: backend
estimate: 0.5 days
blockedBy: [T-2]
blockedBy_reason: test requires the handler to be implemented first
done_means: >
  `internal/handlers/orders_integration_test.go` covers happy path (200),
  unauthenticated (401), and empty result set (200 with empty array);
  all 3 assertions pass in CI.

### T-5: Publish API contract
agent_type: backend
estimate: 0.25 days
blockedBy: [T-3]
blockedBy_reason: contract can only be published after spec is finalized
done_means: >
  `contracts/api-contracts.md` updated with `/v1/orders GET` entry including
  auth requirements, request/response shapes, and idempotency note;
  frontend teammate notified via SendMessage.

## Risks

- No irreversible steps in this plan; all changes are additive.
- Rate-limit: endpoint inherits the default class (100 req/min per user). If
  load tests show this is insufficient, revisit in Phase 2.
- If the `orders` table does not exist in the target environment, T-1 will
  fail fast (query error); rollback: deploy reverts cleanly since no
  migration is included.
