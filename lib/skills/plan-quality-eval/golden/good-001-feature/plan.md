# Plan: Add User Notification Preferences Endpoint

## Summary
Add a GET /v1/users/{id}/notification-prefs endpoint that returns
the user's notification preference settings. Estimated: 1.5 days.

## Assumptions
- Go 1.22 is the runtime; Gin framework is the router.
- PostgreSQL 15 is the DB; the users table already exists.
- YAKOS_SMTP_ENABLED env var is present in all environments.
- The frontend team already has the typed client interface defined.
- CI runs `go test ./...` on every PR; no manual test steps needed.
- Rate-limit middleware is applied at the router level globally.

## Tasks

### T-1: Add notif_prefs column migration
agent_type: db-migrations
estimate: 0.5d
done_means: migration file `db/migrations/0041_add_notif_prefs.sql` applies
  cleanly; `migrate status` shows applied.

### T-2: Add repository method GetNotifPrefs
agent_type: backend
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: T-1 must land before the column is readable in the query.
done_means: `TestGetNotifPrefs` in `internal/repo/notif_prefs_test.go` passes.

### T-3: Add handler GET /v1/users/{id}/notification-prefs
agent_type: backend
estimate: 0.5d
blockedBy: [T-2]
blockedBy_reason: handler calls repository method; needs T-2 to exist.
done_means: `TestHandlerGetNotifPrefs` in `internal/handler/notif_prefs_test.go`
  returns 200 with correct JSON shape.

## Risks
- No irreversible steps in this plan. The migration adds a nullable column
  and can be reversed with `DROP COLUMN IF EXISTS notif_prefs`.
  Rollback: `db/migrations/0041_add_notif_prefs_rollback.sql`.
