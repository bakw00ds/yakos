# Executive summary — v0.39.0.0 release audit

**Version:** v0.39.0.0 @ 9aab4e8
**Audit date:** 2026-06-11
**Disposition:** fix-all approved; remediation PR in progress

## Finding matrix

| Severity | Count | Notes |
|----------|-------|-------|
| P0 — Critical | 0 | None |
| P1 — High | 9 | 1 Documentation/Architecture (UPGRADING.md stale) |
| P2 — Medium | 22 | 5 Documentation/Architecture (see 04-docs.md) |
| P3 — Low | 12 | 4 Documentation/Architecture (see 04-docs.md) |
| Info | several | 1 Info→do-it (ADR-0002 authored as part of remediation) |
| **Total** | **43+** | |

## Top findings (all domains)

See per-domain files for full detail. The lead will append detailed
per-finding text and final dispositions to each domain file.

### Documentation/Architecture (domain 4) — remediated in this PR

| ID | Severity | Finding | Status |
|----|----------|---------|--------|
| D4-P1-01 | P1 | `UPGRADING.md` stale — v0.9 last-updated, no binary-install upgrade path | Fixed |
| D4-P2-01 | P2 | `overview.md` false `fable` claim — says agents set `model: fable` | Fixed |
| D4-P2-02 | P2 | ADR-0001 says `metrics gate` is non-functional stub — fully implemented | Fixed |
| D4-P2-03 | P2 | CHANGELOG 0.38.0.0 wrong metrics snapshot path | Fixed |
| D4-P2-04 | P2 | CHANGELOG dead link `docs/metrics-ci-recipe.md` | Fixed |
| D4-P2-05 | P2 | overview.md skill count stale (said 44; actual 58); README said 57 | Fixed |
| D4-P3-01 | P3 | Agent count wrong (README/getting-started/overview said 35; actual 38) | Fixed |
| D4-P3-02 | P3 | README vs overview metrics-verb inconsistency (`compare` and hooks missing) | Fixed |
| D4-P3-03 | P3 | README/getting-started imply manual `export YAKOS_IMPL=go` | Fixed |
| D4-Info-01 | Info→do-it | Write ADR-0002 for embedded-lib materialization pattern | Done |
| D4-Info-02 | Info→do-it | Persist audit reports to `docs/audits/` | Done |

### Other domains

See `01-security.md`, `02-code-quality.md`, `05-performance.md`, `08-infra.md`
for finding counts and remediation status per domain. Detailed per-finding
text will be appended by the lead after remediation PRs land.
