# Plan: Add CSV Export for Order History

## Summary
Allow authenticated users to download their order history as a CSV file
via GET /v1/orders/export. Estimated: 3 tasks, ~2.5 days.

## Assumptions
- Go 1.22, Gin router; no streaming library needed (encoding/csv in stdlib).
- Order table schema is stable; no pending migrations that alter it.
- YAKOS_ORDER_EXPORT_MAX_ROWS env var caps export at 10,000 rows (already configured).
- YAKOS_RATE_LIMIT_EXPORT env var sets per-user rate limit (already configured).
- Frontend team owns the download button; this plan covers API only.
- The api-contracts.md will be updated as a separate handoff task.

## Tasks
files_touched_estimate: 5

### T-1: Add ExportOrdersCSV repository method
agent_type: backend
estimate: 0.5d
done_means: `TestExportOrdersCSV` in `internal/repo/orders_test.go` returns
  correct CSV rows for a seeded test DB.

### T-2: Add GET /v1/orders/export handler with auth + rate-limit
agent_type: backend
estimate: 1d
blockedBy: [T-1]
blockedBy_reason: Handler calls the repository method created in T-1.
done_means: `TestHandlerExportOrdersCSV` in `internal/handler/orders_test.go`
  asserts Content-Type: text/csv; charset=utf-8 and correct row count.

### T-3: Update api-contracts.md with new endpoint shape
agent_type: api-designer
estimate: 0.5d
blockedBy: [T-2]
blockedBy_reason: Contract must reflect the final handler signature from T-2.
done_means: `contracts/api-contracts.md` updated with GET /v1/orders/export
  path, auth requirements, and response Content-Type.

## Risks
- No irreversible steps. Export is read-only. Rollback: remove the handler
  and route registration; no DB state is affected.
