# Rationale: bad-003-no-assumptions

**acceptance_criteria_specificity (1.0):** Every task has a concrete done signal:
T-1 names the file and an information_schema query; T-2 names the test function
and mock SMTP behavior; T-3 names the test function and specific status codes.
This is a well-specified plan on all other dimensions.

**assumption_surfacing (0.0):** The plan has no Assumptions section at all.
This is significant: there is no mention of the SMTP provider or env var,
the email templating library, token expiry duration, the Go version, or whether
a mock SMTP server is configured in CI. All of these could cause the plan to
diverge from reality. The rubric scores 0 for "no assumptions section and no
inline assumes tags."

**decomposition_granularity (1.0):** Three tasks with estimates 0.5d / 1d /
0.5d; all under 2 days; count in range.

**dependency_clarity (1.0):** Two blockedBy edges, each with a one-line reason.
Linear chain, no cycles.

**domain_boundaries_respected (1.0):** T-1 is db-migrations; T-2 and T-3 are
backend. Clean separation.

**risk_rollback_honesty (1.0):** Explicitly states no irreversible steps and
explains why (tokens expire; no permanent production data change).
