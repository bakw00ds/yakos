# Plan: Add Email Verification Flow

## Summary
Add email verification when users sign up. Users will receive a verification
email and must click a link before their account is activated.

## Tasks

### T-1: Add verification token column to users table
agent_type: db-migrations
estimate: 0.5d
done_means: `db/migrations/0060_add_verification_token.sql` applies; column
  exists in `information_schema.columns`.

### T-2: Send verification email on registration
agent_type: backend
estimate: 1d
blockedBy: [T-1]
blockedBy_reason: Token must exist in DB before it can be stored on registration.
done_means: `TestSendVerificationEmail` in `internal/handler/auth_test.go` passes;
  mock SMTP server receives email with correct verification link format.

### T-3: Add GET /v1/verify-email?token=<tok> handler
agent_type: backend
estimate: 0.5d
blockedBy: [T-2]
blockedBy_reason: Verification handler reads token written during T-2 registration.
done_means: `TestVerifyEmailHandler` passes; account `verified_at` set on valid
  token; 400 returned for invalid/expired tokens.

## Risks
- No irreversible steps. Verification tokens expire; no production data is
  permanently altered by this feature.
