---
plan_id: p-test-cross-domain-plan-fixture
---

# Add notifications feature

Plan for adding push notifications — backend, frontend, and mobile all in one task.

## Assumptions

- Push notification service credentials are in the environment.
- All three platforms (web, mobile, backend) can be touched in a single task.
- Firebase FCM is already set up.
- No database migrations needed.

## Tasks

### T-1: Add notifications everywhere
agent_type: backend
estimate: 1.5 days
blockedBy: []
blockedBy_reason: ""
done_means: >
  `api/handlers/notifications.go` implements POST /v1/notifications;
  `web/src/components/NotificationBell.tsx` shows the badge count;
  `mobile/lib/notifications.dart` registers the FCM token on login;
  all tests pass.

### T-2: Write documentation
agent_type: doc-writer
estimate: 0.5 days
blockedBy: [T-1]
blockedBy_reason: docs written after implementation
done_means: >
  `docs/notifications.md` describes the notification API and
  the FCM registration flow.

## Risks

- No irreversible steps; all changes are additive.
