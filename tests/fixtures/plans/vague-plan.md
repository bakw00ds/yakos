---
plan_id: p-test-vague-plan-fixture
---

# Improve the auth system

We need to make auth better. This plan covers improvements to the
authentication flow.

## Assumptions

- The system is running.

## Tasks

### T-1: Fix auth
agent_type: backend
estimate: 1 day
blockedBy: []
blockedBy_reason: ""
done_means: Auth is working better.

### T-2: Update frontend
agent_type: frontend
estimate: 1 day
blockedBy: [T-1]
blockedBy_reason: needs backend first
done_means: UI looks correct.

### T-3: Test everything
agent_type: backend
estimate: 0.5 days
blockedBy: [T-2]
blockedBy_reason: test after changes
done_means: Tests pass.

## Risks

- Things could break.
