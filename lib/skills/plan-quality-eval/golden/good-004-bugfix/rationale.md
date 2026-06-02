# Rationale: good-004-bugfix

**acceptance_criteria_specificity (1.0):** T-1's done means names the test
function, its location, and a concrete observable behavior (exactly one token
issued, others return 409). T-2 names both the test function and the file path
plus the CI flag (`-race`).

**assumption_surfacing (1.0):** Five assumptions covering Redis version and
primitives used, exact file and function of the current bug location, CI
infrastructure, the env var contract, and client retry behavior. All are
specific and would change the fix if wrong.

**decomposition_granularity (1.0):** Two tasks with estimates 1d and 0.5d;
both under 2 days; count in range.

**dependency_clarity (1.0):** One blockedBy with a clear reason — the
regression test exercises the lock path that T-1 introduces.

**domain_boundaries_respected (1.0):** Both tasks are backend; no cross-domain
work.

**risk_rollback_honesty (1.0):** States no irreversible steps. Provides a
named feature flag with its exact behavior as the rollback mechanism, without
requiring a deployment. Clean.
