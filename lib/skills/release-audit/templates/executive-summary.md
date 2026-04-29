# Release Audit — Executive Summary

**Release:** {{version}}
**Audit date:** {{YYYY-MM-DD}}
**Mode:** {{audit | audit+remediate}}
**Lead auditor:** {{agent-id}}
**Domains covered:** {{list}}
**Report folder:** `/docs/audits/{{YYYY-MM-DD}}-{{version}}/`

---

## TL;DR

{{2-4 sentences. What's the overall readiness? Headline risks? Is there anything that should block shipping in your view? Be direct.}}

## Findings matrix

| Domain | P0 | P1 | P2 | P3 | Info | Total |
|---|---|---|---|---|---|---|
| Security + API Security | | | | | | |
| Code Quality + Test Coverage | | | | | | |
| UI/UX + Accessibility | | | | | | |
| Documentation + Architecture | | | | | | |
| Performance + Load Testing | | | | | | |
| HIPAA / PHI Handling | | | | | | |
| **Total** | | | | | | |

## Top 10 findings across all domains

Sorted by criticality, then by blast radius.

| # | ID | Domain | Title | Criticality | Effort |
|---|---|---|---|---|---|
| 1 | | | | | |
| 2 | | | | | |
| ... | | | | | |

## Tooling gaps

Tools recommended by this skill's playbooks that are not currently installed. These were logged as Info findings in their respective domain reports. Consider adopting before next audit.

| Domain | Tool | Purpose | Install |
|---|---|---|---|
| | | | |

## Per-domain report links

- [Domain 1 — Security](./01-security.md)
- [Domain 2 — Code Quality](./02-code-quality.md)
- [Domain 3 — UI/UX + Accessibility](./03-ui-ux-a11y.md)
- [Domain 4 — Documentation + Architecture](./04-docs-architecture.md)
- [Domain 5 — Performance](./05-performance.md)
- [Domain 6 — Regulated-Data](./06-regulated-data.md)

## Scope and methodology

See [00-scope.md](./00-scope.md) for what was in scope, what was deferred, tool versions, and audit caveats.

---

## Lead-agent review prompt

Paste the block below into the lead orchestrator agent to collect dispositions for every finding. This is the hand-off from audit to decision.

```
ROLE: lead-auditor (review phase)

CONTEXT:
You are the lead auditor for release {{version}}. Six domain findings reports and an executive summary are committed at /docs/audits/{{YYYY-MM-DD}}-{{version}}/. Your job is to walk the user (the operator) through every finding and record a disposition for each.

PROCESS:
1. Load 00-executive-summary.md and each domain report.
2. Build a flat list of all findings, sorted: P0 → P1 → P2 → P3 → Info. Break ties by domain order (1–6).
3. For each finding, present to the operator exactly:
   - Finding ID and title
   - Criticality
   - Domain and sub-area
   - One-line summary of impact
   - Recommended fix (summary, not full detail)
   - Effort estimate

4. Ask the operator to choose a disposition:
   - fix-now       → address this release
   - defer-next    → target a future release (ask for target release tag)
   - accept-risk   → will not fix; require a justification string of ≥1 sentence
   - invalid       → false positive / out of scope

5. Record decisions in dispositions.md using templates/dispositions.md.

6. When all findings have dispositions, produce a summary:
   - Counts: X fix-now, Y defer-next, Z accept-risk, W invalid
   - List of fix-now items grouped by domain
   - List of accept-risk items with their justifications (for audit trail)
   - Any P0/P1 marked accept-risk must be flagged for second-look: the operator confirms again.

7. Confirm with the operator before handing off to the remediation phase. If mode is `audit` only, stop here and note that remediation is out of scope for this run.

RULES:
- Do not assign dispositions yourself. the operator decides.
- Do not combine findings. One finding = one disposition.
- If the operator wants to defer a P0, require an explicit confirmation ("Yes, defer this P0 to release <tag>") — P0s are critical and deferring them is unusual.
- If the operator wants to accept-risk a regulated-data finding, require that the justification mentions either (a) compensating control or (b) acknowledged residual risk that will be reviewed by [name].
- Keep a running tally and show progress ("24 of 87 findings reviewed") after each decision.
```

---

## Sign-off

Audit complete. Dispositions pending review. Next action: the operator runs the lead-agent review prompt above.
