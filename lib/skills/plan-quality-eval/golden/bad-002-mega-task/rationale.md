# Rationale: bad-002-mega-task

**acceptance_criteria_specificity (0.5):** The single done_means mentions
specific features (Stripe integration, webhook handling, refund flows,
reconciliation) but none are linked to a named test, file path, or
observable behavior that can be checked programmatically. Partial credit
because it names components; no credit for verifiability.

**assumption_surfacing (0.5):** Three assumptions are present but two are
too vague: "Stripe API key is in environment" doesn't name the env var;
"PostgreSQL is available" doesn't specify version or connection parameters.
The third (frontend ownership) is clear. Below the 3-specific-items threshold
for a 1.0, but not entirely absent.

**decomposition_granularity (0.0):** A 3-week estimate on a single task is a
direct violation. The rubric scores 0 for any task exceeding 2 days. The plan
also has only 1 task, below the minimum of 2.

**dependency_clarity (0.0):** With only one task there are no dependencies to
document. But the absence of decomposition makes this a structural failure —
no dependency information is possible.

**domain_boundaries_respected (0.5):** The task is assigned to backend, but
"admin reconciliation dashboard" implies frontend work that is silently included.
The plan partially acknowledges that frontend is separate ("after backend is done")
but does not create a separate task for it. Partial credit.

**risk_rollback_honesty (0.0):** "Payments are irreversible once settled" is the
risk statement, but there is no rollback plan for any step. For a 0.5 it would
need at least vague rollback notes; for a 1.0 it needs specific rollback steps
per irreversible item.
