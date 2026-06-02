# Plan: Drop Legacy audit_log_v1 Table

## Summary
Remove the legacy `audit_log_v1` table which has been superseded by
`audit_log_v2`. The table has not received writes in 90 days.

## Assumptions
- PostgreSQL 15 is the DB.
- `audit_log_v2` is the active table (confirmed by team).
- No application code references `audit_log_v1` (grep confirms no references).
- The table contains 8 years of compliance data; retention policy requires
  archiving before deletion per YAKOS_DATA_RETENTION_POLICY.

## Tasks

### T-1: Archive audit_log_v1 data to S3 cold storage
agent_type: ops
estimate: 1d
done_means: `aws s3 ls s3://yakos-archive/audit_log_v1/` lists Parquet files
  totaling the expected row count; checksum verified.

### T-2: Drop the audit_log_v1 table
agent_type: db-migrations
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Archive must be verified before the source data is deleted.
done_means: `\dt audit_log_v1` in psql returns "Did not find any relation"
  in production; migration logged in migration history.

## Risks
- Dropping the table is irreversible without restoring from archive.
- The S3 archive may be incomplete if the export job fails partway through.
