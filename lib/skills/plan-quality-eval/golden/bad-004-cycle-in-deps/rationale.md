# Rationale: bad-004-cycle-in-deps

**acceptance_criteria_specificity (1.0):** Each task names a test function
with a specific assertion (timing, channel name, message format) or a file
path with documented convention. All are verifiable.

**assumption_surfacing (0.5):** Four assumptions are listed, covering Go
version, library version, Redis version, and an env var. This is close to
the 1.0 threshold but misses the SMTP/email analogue here — there's no
assumption about the pub/sub message envelope format or the WebSocket frame
size limits. Scores 0.5 (assumptions present but incomplete for this scope).

**decomposition_granularity (1.0):** Three tasks with estimates 1d / 0.5d /
0.5d; all under 2 days; count in range.

**dependency_clarity (0.0):** The blockedBy edges form a cycle: T-1 blocks on
T-3, T-3 blocks on T-2, T-2 blocks on T-1. This is a circular dependency
(T-1 → T-3 → T-2 → T-1). The rubric scores 0 for any cycle in the
blockedBy chain, regardless of whether reasons are given.

**domain_boundaries_respected (1.0):** All three tasks are backend tasks.
No cross-domain work is present.

**risk_rollback_honesty (1.0):** Correctly identifies no irreversible steps.
Rollback is named precisely (remove route registration, no DB state affected).
