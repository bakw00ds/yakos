# Audit scope — v0.39.0.0

**Version:** v0.39.0.0
**Commit:** 9aab4e8
**Date:** 2026-06-11
**Mode:** advisory (findings surfaced; no automated deploy gate)

## Profiles applied

- `go-backend` — Go CLI source (`cli-go/`)
- `shell` — bash scripts (`cli/`, `scripts/`, `lib/hooks/`)
- `infra-iac` — CI workflows (`.github/workflows/`)

## Domains audited

| Domain | Included | Notes |
|--------|----------|-------|
| 1 — Security | Yes | |
| 2 — Code quality | Yes | |
| 3 — Tests | No | Out of scope for this audit |
| 4 — Documentation/Architecture | Yes | Primary focus |
| 5 — Performance | Yes | |
| 6 — Dependencies | No | Out of scope for this audit |
| 7 — Accessibility | No | No UI surface in scope |
| 8 — Infrastructure/IaC | Yes | CI workflows |
| 9 — Mobile | N/A | No mobile surface |
| 10 — Web frontend | N/A | No web frontend surface |

## Operator context

Single-operator install (no multi-dev coordinator). The repository
is public on GitHub at `github.com/bakw00ds/yakos`. Binary releases
are distributed via `scripts/install.sh`.
