# Plan: Migrate user_events Table to TimescaleDB Hypertable

## Summary
Convert the existing `user_events` PostgreSQL table to a TimescaleDB
hypertable partitioned by `created_at`. Estimated: 2 days.

## Assumptions
- TimescaleDB 2.14 extension is installed in the target PostgreSQL 16 instance.
- `user_events` currently has ~40M rows; migration will run during a maintenance window.
- Application write path uses parameterized INSERT; no raw SQL strings.
- DB superuser credentials are available in the deployment environment.
- Read replicas will lag during migration; acceptable per SLA.
- `YAKOS_DB_MIGRATION_LOCK_TIMEOUT` env var controls lock acquisition timeout.

## Tasks

### T-1: Write migration script to enable TimescaleDB and convert table
agent_type: db-migrations
estimate: 1d
done_means: `db/migrations/0055_timescale_user_events.sql` applies without
  error on a staging DB seeded with 1M rows; `SELECT * FROM timescaledb_information.hypertables`
  shows `user_events`.

### T-2: Add integration test for hypertable insert + continuous-aggregate query
agent_type: backend
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Integration test requires the hypertable schema to exist.
done_means: `TestUserEventsHypertableInsert` and `TestUserEventsContinuousAgg`
  pass in the CI postgres-timescale service container.

### T-3: Deploy migration to production with maintenance window
agent_type: ops
estimate: 0.5d
blockedBy: [T-2]
blockedBy_reason: T-2 must pass in staging before production deployment.
done_means: Production `timescaledb_information.hypertables` confirms conversion;
  p95 insert latency < 50ms confirmed in Grafana after 15 minutes.

## Risks
- **IRREVERSIBLE:** Converting a table to a hypertable cannot be undone without
  dropping and recreating the table. Rollback plan: restore from pre-migration
  RDS snapshot (YAKOS_DB_SNAPSHOT_ID must be recorded before T-3 begins).
  The snapshot must be verified restorable in staging before T-3 executes.
- Lock acquisition during conversion may briefly block writes. Mitigation:
  set YAKOS_DB_MIGRATION_LOCK_TIMEOUT=5s; abort and retry during lower-traffic
  window if exceeded.
