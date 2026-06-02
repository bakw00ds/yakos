# Rationale: good-006-doc-refresh

**acceptance_criteria_specificity (1.0):** T-1 names the lint command, the
exit code, and a verification method. T-2 names the file and a negative
assertion (no stale v1 paths) plus a positive match requirement.

**assumption_surfacing (1.0):** Five assumptions: spec file location, guide
file location, lint tool and how it is invoked, the exact set of changed
endpoints, and the scope constraint (documentation only, no code changes).
Each is specific and would invalidate the plan if wrong.

**decomposition_granularity (1.0):** Two tasks, 1d and 0.5d, both under 2 days.

**dependency_clarity (1.0):** One blockedBy with a clear reason (guide
examples must match the finalized spec).

**domain_boundaries_respected (1.0):** T-1 is api-designer (spec ownership);
T-2 is docs (guide ownership). Clean separation.

**risk_rollback_honesty (1.0):** Explicitly states no irreversible steps with
the rationale (no DB or API behavior affected). Rollback via git revert is
named.
