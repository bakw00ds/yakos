# Rationale: bad-005-cross-domain-task

**acceptance_criteria_specificity (0.5):** T-1 names a backend test function
but says "renders results from the new endpoint" for the React component —
that's not a named test or file path for the frontend work. T-2 names two
specific verification commands. Partial credit: some tasks are well-specified,
but T-1's frontend half is not.

**assumption_surfacing (1.0):** Five specific assumptions: PostgreSQL version
with extension, Go version, React version with TypeScript, two named env vars
with their semantics, and the frontend data-fetching library. All are specific
and actionable.

**decomposition_granularity (0.5):** Two tasks (in the 2-7 range). However
T-1 has a 2-day estimate, which is at the boundary. More importantly T-1
contains both backend AND frontend work, so it is implicitly a larger task
than its estimate suggests. Scores 0.5 because the estimate is at the edge
and the bundling makes it questionable.

**dependency_clarity (0.5):** One blockedBy with a reason. However the reason
for T-2 blocking on T-1 is inverted in logic — you would typically want the
DB index to exist before the query that uses it, not after. The reasoning is
present but the direction is wrong, which creates confusion. Partial credit
for having reasons; deducted for logical inversion.

**domain_boundaries_respected (0.0):** T-1 is assigned to backend but
explicitly includes updating the React SearchBar component — frontend code.
This is the canonical anti-example from the rubric. The agent_type is backend
but the work spans backend and frontend with no handoff task.

**risk_rollback_honesty (1.0):** Correctly identifies index creation as
reversible (CONCURRENTLY), names the rollback DROP INDEX command precisely.
Explicit and actionable.
