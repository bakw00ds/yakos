# Plan: Fix Race Condition in Session Token Refresh

## Summary
Fix a race condition in the token refresh path where two concurrent requests
with the same expired token can both succeed, each generating a different new
token. Incident: INC-2026-04-17. Estimated: 1.5 days.

## Assumptions
- The session store is Redis 7.2 with SETNX and EXPIRE primitives available.
- The current token refresh handler is in `internal/handler/auth.go` func `RefreshToken`.
- Integration tests run against a real Redis instance (docker-compose in CI).
- YAKOS_REDIS_LOCK_TTL env var is set in all environments (in .env.example).
- No client-side retry logic exists; clients receive the first 200 response.

## Tasks

### T-1: Add Redis-based distributed lock around token refresh critical section
agent_type: backend
estimate: 1d
done_means: `TestRefreshTokenConcurrent` in `internal/handler/auth_test.go`
  runs 50 concurrent refresh requests with the same expired token and asserts
  exactly one new token is issued (all others return 409 Conflict).

### T-2: Add regression test to CI that replicates INC-2026-04-17
agent_type: backend
estimate: 0.5d
blockedBy: [T-1]
blockedBy_reason: Regression test must exercise the lock path introduced in T-1.
done_means: `TestRefreshTokenRaceRegression` file committed at
  `internal/handler/auth_race_test.go`; CI passes with `-race` flag.

## Risks
- No irreversible steps. The Redis lock uses a TTL and cannot corrupt data.
  Rollback: feature-flag `YAKOS_TOKEN_REFRESH_LOCK=0` disables the lock
  (returns to previous behavior) without a deployment.
