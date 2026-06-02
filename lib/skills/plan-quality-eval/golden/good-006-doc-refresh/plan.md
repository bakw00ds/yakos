# Plan: Refresh API Documentation for v2 Endpoints

## Summary
Update the OpenAPI spec and developer guide to reflect the v2 endpoint
changes shipped in Q1. Estimated: 1.5 days.

## Assumptions
- OpenAPI spec lives at `docs/openapi.yaml` and is the source of truth.
- The developer guide is at `docs/developer-guide.md`.
- `redocly lint` is the validation tool (already in package.json devDeps).
- The v2 endpoints that changed: /v2/orders, /v2/users/{id}, /v2/auth/token.
- No code changes are needed; this is documentation only.

## Tasks

### T-1: Update openapi.yaml for /v2/orders, /v2/users/{id}, /v2/auth/token
agent_type: api-designer
estimate: 1d
done_means: `npx redocly lint docs/openapi.yaml` exits 0; all three endpoint
  schemas match the handlers' response structs (verified by api-designer review).

### T-2: Update developer-guide.md request/response examples
agent_type: docs
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Examples in the guide must match the spec; T-1 must finalize
  the spec first.
done_means: All code examples in `docs/developer-guide.md` match the
  openapi.yaml schemas for the three changed endpoints; no stale v1 paths remain.

## Risks
- No irreversible steps. Documentation changes are fully reversible via
  `git revert`. No DB or API behavior is affected.
