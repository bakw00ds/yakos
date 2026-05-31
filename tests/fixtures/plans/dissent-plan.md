---
plan_id: p-test-dissent-plan-fixture
---

# Ambiguous API refactor

A plan where judges will disagree — used for dissent fixture testing.
When YAKOS_PLAN_JUDGE_MOCK is set to tests/fixtures/plan-judge-mock/dissent/,
the mock judges return spread scores that trigger the dissent flag.

## Assumptions

- Service is deployed on Kubernetes.
- API v1 clients exist and must not be broken.
- The refactor only affects internal routing, not the wire shape.

## Tasks

### T-1: Refactor route handlers
agent_type: backend
estimate: 1 day
blockedBy: []
blockedBy_reason: ""
done_means: >
  `internal/handlers/` directory reorganized; existing unit tests pass;
  no changes to `api/docs/openapi.yaml`.

### T-2: Update integration tests
agent_type: backend
estimate: 0.5 days
blockedBy: [T-1]
blockedBy_reason: tests updated after refactor to match new structure
done_means: >
  `internal/handlers/integration_test.go` updated; all assertions pass;
  CI green.

## Risks

- No irreversible steps.
