# Rationale: bad-006-no-rollback-migration

**acceptance_criteria_specificity (1.0):** T-1 names an S3 path, a specific
CLI command, and a checksum verification requirement. T-2 names the exact psql
command output and migration history confirmation. Both are fully verifiable.

**assumption_surfacing (1.0):** Five specific assumptions: DB version, which
table is active, the grep verification result, the data retention policy and
its env var, and the data age (90 days without writes). All are concrete.

**decomposition_granularity (1.0):** Two tasks with estimates 1d and 0.5d;
both under 2 days; count in range.

**dependency_clarity (1.0):** One blockedBy edge with a clear one-line reason
(archive must be verified before deletion). No cycles.

**domain_boundaries_respected (1.0):** T-1 is ops (archive to S3); T-2 is
db-migrations (DROP TABLE). Clean separation.

**risk_rollback_honesty (0.5):** The Risks section exists and correctly
identifies the irreversible step (table drop) and a partial risk (incomplete
export). However, it does not provide a rollback plan — it only names the
risks without saying what to do if T-2 needs to be reversed. For a 1.0, each
irreversible step must have a specific rollback note (e.g., "restore from S3
archive via `aws s3 cp ...` and re-import with COPY"). The risks are named
but rollback procedures are absent, which is the 0.5 condition in the rubric.
