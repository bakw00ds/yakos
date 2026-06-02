# Plan: Refactor Auth Middleware into Separate Package

## Summary
Extract inline JWT validation from individual route handlers into a
shared `internal/middleware/auth` package. No behavior change.
Estimated: 2 days total.

## Assumptions
- Project uses Go 1.22 and Gin v1.9.
- All existing handlers in `internal/handler/` import JWT logic inline.
- The `golang-jwt/jwt/v5` library is already in go.mod.
- No external service or DB calls are made during JWT validation.
- All environment variables for JWT secret are already documented in .env.example.

## Tasks

### T-1: Create internal/middleware/auth package with ValidateJWT func
agent_type: backend
estimate: 1d
done_means: `TestValidateJWT` in `internal/middleware/auth/jwt_test.go` passes
  for valid, expired, and malformed tokens.

### T-2: Replace inline validation in all handlers with middleware.ValidateJWT
agent_type: backend
estimate: 1d
blockedBy: [T-1]
blockedBy_reason: Handlers cannot import the middleware package until T-1
  defines the function signature.
done_means: `grep -r "ParseWithClaims" internal/handler/` returns zero
  matches; all handler tests pass.

## Risks
- No irreversible steps. All changes are pure refactors reversible via git
  revert. Rollback: `git revert <commit>` on either T-1 or T-2 without
  affecting DB state.
