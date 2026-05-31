---
plan_id: p-test-self-eval-drop-fixture
---

# Simple feature addition

A plan used to test the family-drop logic. When YAKOS_PLANNER_MODEL=haiku
(or any claude-family model), the claude judge should be dropped.

## Assumptions

- Go 1.22+ runtime.
- PostgreSQL 15 database.
- `FEATURE_FLAG_NEW_WIDGET=true` env var required.
- No external service dependencies.

## Tasks

### T-1: Add widget endpoint
agent_type: backend
estimate: 1 day
blockedBy: []
blockedBy_reason: ""
done_means: >
  `internal/handlers/widget.go` implements GET /v1/widget returning 200
  with `{"widget": {...}}`; unit test `TestWidgetHandler` passes.

### T-2: Add widget to OpenAPI spec
agent_type: api-designer
estimate: 0.5 days
blockedBy: [T-1]
blockedBy_reason: spec updated after handler shape is finalized
done_means: >
  `api/docs/openapi.yaml` has /v1/widget GET documented with response
  schema; `yakos skill api-diff` shows patch-level change.

### T-3: Update contract
agent_type: backend
estimate: 0.25 days
blockedBy: [T-2]
blockedBy_reason: contract published after spec finalized
done_means: >
  `contracts/api-contracts.md` updated; frontend teammate notified.

## Risks

- No irreversible steps.
