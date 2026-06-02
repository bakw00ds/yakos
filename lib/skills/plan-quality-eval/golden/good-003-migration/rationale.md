# Rationale: good-003-migration

**acceptance_criteria_specificity (1.0):** Each task has a concrete done signal.
T-1 names the exact migration file and a SQL query to verify conversion. T-2
names two specific test function names. T-3 names a specific Grafana metric with
a latency threshold.

**assumption_surfacing (1.0):** Six specific assumptions covering TimescaleDB
version, PG version, row count (which affects maintenance window sizing), the
write path contract (parameterized queries), credential availability, replica
lag tolerance, and the env var for lock timeout.

**decomposition_granularity (1.0):** Three tasks, estimates of 1d / 0.5d / 0.5d,
all under 2 days. Task count in range.

**dependency_clarity (1.0):** Two blockedBy edges, each with a one-line reason.
T-1 has no dependency; T-2 blocks on T-1 for schema; T-3 blocks on T-2 for
staging validation before production.

**domain_boundaries_respected (1.0):** db-migrations writes the migration; backend
writes the integration tests; ops runs the production deployment. No specialist
crosses into another's domain.

**risk_rollback_honesty (1.0):** Explicitly flags the irreversible step (hypertable
conversion), names the rollback strategy (RDS snapshot restore), and requires that
the snapshot be verified restorable before the irreversible step. Also documents
the write-lock risk with a mitigation.
