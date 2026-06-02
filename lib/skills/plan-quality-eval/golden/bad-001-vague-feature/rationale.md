# Rationale: bad-001-vague-feature

**acceptance_criteria_specificity (0.0):** "Dashboard works better" and "things
load faster" are completely unverifiable. No file, endpoint, test, or metric
is named. A judge cannot determine when these tasks are done.

**assumption_surfacing (0.0):** A single assumption ("The app is running") is
too vague to be actionable. No runtime version, DB, env vars, or external
service dependencies are mentioned. Scores 0 — the rubric requires at least
3 specific items for a 1.0; this has 1 useless item.

**decomposition_granularity (0.0):** T-1 has a 3-day estimate, exceeding the
2-day maximum. T-2 has no estimate at all. Both conditions independently score 0.

**dependency_clarity (0.0):** No blockedBy fields despite T-2 ("fix slow
queries") potentially depending on knowing which queries the dashboard
improvements introduce. The dependency structure is invisible.

**domain_boundaries_respected (0.5):** Both tasks are assigned to backend, which
is at least consistent — the plan isn't asking backend to write frontend code.
However the plan is so vague that it's impossible to confirm domain boundaries
are actually respected; partial credit only.

**risk_rollback_honesty (0.0):** "Could break something" is not a risk entry.
There is no rollback plan, no identification of irreversible steps, and no
actionable mitigation.
