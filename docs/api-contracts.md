# yakOS REST API Contracts

**Source of truth:** `cli-go/internal/restapi/` (server) and `cli-go/internal/restapi/openapi.yaml` (full spec).
**Implemented in:** Phase 2 REST API dispatch (2026-06-03).
**Consumers:** IDE extensions, dashboard, CI integrations.

---

## Transport

- Protocol: HTTP/1.1 over TCP
- Default listen address: `127.0.0.1:7892` (loopback-only)
- Override via `yakos serve --rest-addr <addr>`
- Disable via `yakos serve --rest-addr -`

The REST server starts concurrently with the JSON-RPC socket (`127.0.0.1` Unix socket)
and WebSocket bus (`127.0.0.1:7891`) under `yakos serve`.

---

## Authentication

Two-token model (Phase 2 Q7 decision):

| Token | File | Scope |
|---|---|---|
| Read token | `~/.yakos-state/rest-read-token` | GET endpoints only |
| Write token | `~/.yakos-state/rest-write-token` | GET + POST + PATCH |

Header: `Authorization: Bearer <token>`
- Bearer prefix is case-insensitive
- Both tokens are 64 hex chars (32 random bytes)
- Mode 0600 on disk
- Auto-generated on first daemon start; persist across restarts
- Rotate via daemon flag: `yakos serve --rotate-rest-tokens`

---

## Endpoints

### GET /v1/version

**Auth:** None (public)

**Response 200:**
```json
{
  "version": "0.36.0.0 (go)",
  "runtime": "go1.23.12"
}
```

---

### GET /v1/kanban

**Auth:** Read token

**Response 200:**
```json
{
  "items": [
    {"id": "K-1", "title": "implement health endpoint", "column": "TODO"},
    {"id": "K-2", "title": "write tests", "column": "IN PROGRESS"},
    {"id": "K-3", "title": "design review", "column": "DONE"}
  ]
}
```

`items` is always a JSON array (never null; empty board returns `[]`).

---

### POST /v1/kanban/items

**Auth:** Write token
**Idempotency-Key header:** Optional; server does not deduplicate in Phase 2

**Request body:**
```json
{
  "title": "implement health endpoint",
  "column": "TODO",
  "category": "feat",
  "notes": "optional notes"
}
```

- `title`: required
- `column`: optional, default `"TODO"`; accepted: `"TODO"`, `"IN PROGRESS"`, `"DONE"`
- `category`: optional, default `"other"`
- `notes`: optional

**Response 201:**
```json
{"id": "K-5"}
```

**Response 400:** Missing title or unknown JSON field.

---

### PATCH /v1/kanban/items/{id}

**Auth:** Write token
**Idempotent:** Yes (move to current column is no-op)

**Path parameter:** `id` — kanban item ID (e.g. `"K-5"`)

**Request body** (at least one field required):
```json
{
  "column": "IN PROGRESS",
  "notes": "updated notes"
}
```

`column` accepted values: `"TODO"`, `"IN PROGRESS"`, `"DONE"` (case-insensitive normalization applied).

**Response 200:**
```json
{"ok": true}
```

**Response 404:** Item ID not found on the board.

---

### GET /v1/dispatches

**Auth:** Read token

**Query parameters:**
- `since` (optional): ISO-8601 timestamp lower bound; only events at or after this time are returned.

**Response 200:**
```json
{
  "dispatches": [
    {
      "ts": "2026-06-03T10:22:00Z",
      "agent": "backend",
      "runtime": "claude",
      "exit_code": 0,
      "duration_s": 142.3,
      "model_resolved": "sonnet"
    }
  ]
}
```

`dispatches` is always a JSON array (never null).

---

### POST /v1/dispatches

**Auth:** Write token
**Idempotency-Key header:** Optional; NOT enforced server-side in Phase 2
**NOT idempotent:** each call spawns an agent process

**Request body:**
```json
{
  "agent": "backend",
  "task": "add a /health endpoint",
  "runtime": "claude",
  "model": "sonnet",
  "timeout": 0
}
```

- `agent`: required
- `task`: required
- `runtime`: optional; empty = resolve from agent frontmatter
- `model`: optional; accepted: `"haiku"`, `"sonnet"`, `"opus"`
- `timeout`: optional; 0 = 600s default

**Response 202** (blocking — returns after runtime exits):
```json
{
  "exit_code": 0,
  "duration_s": 142.3,
  "model_resolved": "sonnet"
}
```

**Response 400:** Missing agent or task.
**Response 503:** `yakos_root` not configured on daemon.
**Response 502:** Runtime dispatch error.

---

### GET /v1/cost

**Auth:** Read token

**Query parameters:**
- `by` (optional): aggregation axis. Accepted: `"runtime"` (default), `"agent"`, `"day"`, `"project"`.

**Response 200:**
```json
{
  "by": "runtime",
  "rows": [
    {
      "Key": "claude",
      "Count": 42,
      "OK": 40,
      "Fail": 2,
      "TotalDurationS": 5820.1,
      "TotalInTokens": 128000,
      "TotalOutTokens": 45000
    }
  ]
}
```

`rows` is always a JSON array. `Count=0` → empty rows slice.

**Response 400:** Unknown `by` value.

---

### GET /v1/status

**Auth:** Read token

**Response 200:** Structured project status report (schema matches `internal/status.Report`).
The exact shape is documented in `internal/status/status.go`.

---

### GET /v1/supervise/pending

**Auth:** Read token

**Response 200:**
```json
{
  "pending_count": 2,
  "output": "yakos supervise pending — project: myproject\n\n  Unacknowledged escalation findings...\n"
}
```

---

## Error Envelope

All error responses use:
```json
{"error": "human-readable description"}
```

HTTP status codes:
- `400 Bad Request` — invalid request body or parameters
- `401 Unauthorized` — missing `Authorization: Bearer` header
- `403 Forbidden` — token present but insufficient permissions (read token on write endpoint)
- `404 Not Found` — resource (e.g. kanban item ID) not found
- `500 Internal Server Error` — server-side processing failure
- `502 Bad Gateway` — dispatch runtime error
- `503 Service Unavailable` — daemon not fully configured

---

## Idempotency Summary

| Endpoint | Idempotent | Notes |
|---|---|---|
| `GET *` | Yes | Read-only |
| `PATCH /v1/kanban/items/{id}` | Yes | Set operation |
| `POST /v1/kanban/items` | No | Creates new item per call |
| `POST /v1/dispatches` | No | Spawns agent per call |

---

## Content-Type

All responses are `application/json`. Requests with a body must set
`Content-Type: application/json`.

---

## Library Extracts (pkg/)

Three new public packages added alongside the REST API:

### pkg/refresh

Re-exports `internal/refresh`. Public surface:
- `Config` (type alias)
- `Report`, `ProjectReport`, `HookPhaseReport`, `SettingsPhaseReport`, `AgentPhaseReport` (type aliases)
- `Run(cfg Config) (*Report, error)`
- `CollectProjects(homeDir string) []string`

### pkg/supervise

Re-exports `internal/supervise`. Public surface:
- `Config`, `Result`, `Finding`, `AckRecord` (type aliases)
- `Run(cfg Config) (*Result, error)`
- `PrintHelp(w io.Writer)`

### pkg/agent

Re-exports `internal/agent`. Public surface:
- `Config`, `Result` (type aliases)
- `Run(cfg Config) (*Result, error)`
- `PrintHelp(w io.Writer)`

All three packages follow the same stability promise as existing `pkg/*`
packages: `experimental`, pre-1.0, breaking changes allowed at minor bumps.

---

## Implementation Files

| File | Purpose |
|---|---|
| `cli-go/internal/restapi/server.go` | HTTP server, auth middleware, route registration |
| `cli-go/internal/restapi/handlers.go` | All endpoint handlers |
| `cli-go/internal/restapi/tokens.go` | Read/write token generation and persistence |
| `cli-go/internal/restapi/tokens_test.go` | Token tests (7 tests) |
| `cli-go/internal/restapi/server_test.go` | Handler tests via httptest.NewServer (38 tests) |
| `cli-go/internal/restapi/openapi.yaml` | OpenAPI 3.1 spec |
| `cli-go/internal/restapi/README.md` | Endpoint table + curl examples |
| `cli-go/internal/serve/serve.go` | Daemon integration (RESTAddr + RESTStateDir fields) |
| `cli-go/pkg/refresh/refresh.go` | Public refresh API |
| `cli-go/pkg/supervise/supervise.go` | Public supervise API |
| `cli-go/pkg/agent/agent.go` | Public agent API |

---

## Console Session Auth — ADR-0005 Phase 3b

The endpoints below are on the **networked console bind** (not the REST API port).
All console endpoints are at `https://<console-host>:<console-port>/`.

### Auth model for the networked console

| Regime | Clients | Mechanism |
|---|---|---|
| Networked humans | Browser operators | Password + session cookie (these endpoints) |
| Networked machines | CLI, CI, mTLS clients | Client certificate (existing) |
| Loopback | Localhost callers | Bearer token `#token=` (unchanged) |

### POST /login

**Auth:** None (unauthenticated endpoint; no bearer token required)
**Content-Type:** `application/json` (required; enforced by `requireJSONForMutations`)
**Idempotency-Key:** Not applicable (each call creates a new session; prior session for the same user is revoked — session fixation defense)

**Rate limit:** 20 requests per IP per 5 minutes (independent of per-user lockout in userstore)

**Request body:**
```json
{"username": "alice", "password": "correct-horse-battery"}
```
- Both fields required; any missing field → 400.
- Field names are the only accepted fields; no privilege fields (role, epoch) are bound.

**Response 200:**
```json
{"ok": true, "passwordResetRequired": false}
```
Sets two `Set-Cookie` headers:
- `yakos_session=<id>; HttpOnly; Secure; SameSite=Strict; Path=/` — session ID (never readable by JS)
- `yakos_csrf=<token>; Secure; SameSite=Strict; Path=/` — CSRF token (JS-readable for double-submit)

**Response 400:** Missing / invalid body.

**Response 401:**
```json
{"error": "invalid username or password"}
```
IDENTICAL body for all failure modes (unknown user, wrong password, disabled, locked). The specific reason is audit-logged server-side with `slog.Warn` and a `reason` field; it is NEVER sent to the client.

**Response 429:** Per-IP rate limit exceeded.
```json
{"error": "too many requests"}
```

---

### POST /logout

**Auth:** Session cookie (`yakos_session`). Idempotent: no session → 200 + clear cookies.
**CSRF:** `X-CSRF-Token` header required when authenticated (double-submit pattern).
**Content-Type:** `application/json` (required)

**Response 200:**
```json
{"ok": true}
```
Sets two `Set-Cookie` headers with `MaxAge=-1` to clear both cookies:
- `yakos_session=; HttpOnly; Secure; SameSite=Strict; Path=/; MaxAge=-1`
- `yakos_csrf=; Secure; SameSite=Strict; Path=/; MaxAge=-1`

---

### CSRF protocol for session-authenticated mutations

All state-mutating requests (POST/PUT/PATCH/DELETE) from session-authenticated browsers
MUST include:

```
X-CSRF-Token: <value-from-yakos_csrf-cookie>
```

The `yakos_csrf` cookie is non-HttpOnly so JS can read it. The Service Worker reads it
and injects `X-CSRF-Token` on all same-origin mutations in session mode (Phase 3c).

Violations:
- Missing header → `403 {"error":"invalid CSRF token"}`
- Wrong value → `403 {"error":"invalid CSRF token"}`

**Exempt from CSRF:**
- mTLS cert-authenticated requests (`AuthMethodCert`)
- Loopback bearer requests (`AuthMethodNone`)
- GET/HEAD/OPTIONS requests

---

### Edge behavior for unauthenticated networked requests

| Request type | Response |
|---|---|
| API/XHR path (`/api/*`, `/flows/api/*`, `/v1/*`) or `Accept: application/json` | `401 {"error":"authentication required"}` |
| Top-level navigation (other paths, no `Accept: application/json`) | `302 Location: /login` |
| `/login`, `/login.js`, static SPA shell | Pass through (no auth required) |

---

### GET /login

**Auth:** None
Serves the minimal server-rendered login form (Phase 3b placeholder).
CSP: `script-src 'self'; form-action 'self'`. No inline scripts.

---

### Console auth implementation files

| File | Purpose |
|---|---|
| `cli-go/internal/consoleui/authhandler.go` | POST /login, POST /logout, CSRF middleware, edge redirect/401, per-IP rate limiter, zero-users redirect target |
| `cli-go/internal/consoleui/server.go` | Middleware chain wiring, `/login`, `/logout`, `/setup`, `/setup.js` route registration, `FullHandler()` |
| `cli-go/internal/consoleui/setuphandler.go` | GET /setup (page), POST /setup (create first admin), GET /setup.js |
| `cli-go/internal/consoleui/sessionauth.go` | `buildSessionLookupFn` glue (Phase 3a, updated to use `sessionCookieName`) |
| `cli-go/internal/consoleui/export_test.go` | Test exports: `LoginRateLimitRequests`, `SessionCookieName`, `RequireCSRFForSessionForTest` |
| `cli-go/internal/consoleui/auth_3b_test.go` | Phase 3b test cases (race-enabled) |
| `cli-go/internal/consoleui/setup_test.go` | Phase 3c test cases: /setup route, edge redirect, loopback invariant (race-enabled) |
| `cli-go/internal/consoleui/dist/login.html` | Minimal login page HTML (CSP-compliant, no inline scripts) |
| `cli-go/internal/consoleui/dist/login.js` | Login form JS (`script-src 'self'`) |
| `cli-go/internal/consoleui/dist/setup.html` | First-admin setup page HTML (CSP-compliant, no inline scripts) |
| `cli-go/internal/consoleui/dist/setup.js` | Setup form JS (`script-src 'self'`) |
| `cli-go/internal/setuptoken/setuptoken.go` | One-time setup token: generate, validate (constant-time), consume, file persistence |
| `cli-go/internal/consolecmd/consolecmd.go` | `yakos console bootstrap-token` CLI: regenerate setup token when Count()==0 |
| `cli-go/internal/authsession/export_test.go` | `CookieNameSession` export for authsession invariant tests |

---

## Console setup endpoints (ADR-0005 Phase 3c) — port 7890

These endpoints are on the networked console server (port 7890, not the REST API at 7892). They are active only when `ConsoleBind` is a non-loopback address (`NetworkedMode=true`).

### GET /setup

**Auth:** None (exempt — must be reachable before any user exists)

**Behavior:**
- When `Count()==0`: serves the first-admin setup page HTML.
- When `Count()>0`: 302 redirect to `/login` (setup is complete).

**Security notes:** CSP `form-action 'self'`, no inline scripts, `X-Content-Type-Options: nosniff`.

---

### POST /setup

**Auth:** None (setup token in request body)

**Content-Type:** `application/json` (enforced by `requireJSONForMutations`)

**Request body:**
```json
{
  "token": "<setup-token-from-daemon-stdout>",
  "username": "firstadmin",
  "password": "securepassword123"
}
```

- `token`: required; must match the in-memory setup token (constant-time), unexpired (30-min TTL), not yet consumed.
- `username`: required; must match `[A-Za-z0-9._@-]{1,64}`, not "." or "..".
- `password`: required; minimum `userstore.MinPasswordLen` (12) characters.

**Response 200:**
```json
{"ok": true, "message": "admin account created; please sign in"}
```

Token is consumed on 200. User is created with `RoleAdmin`.

**Response 400:** Invalid username or password too short.

**Response 403:** Token missing, wrong, expired, or already consumed.

**Response 409:** `Count()>0` at time of request (setup already complete).

**Idempotency:** NOT idempotent — POST /setup is intentionally single-use. A second request after success returns 409.

**Audit log entry (server-side):** `"first admin created via setup token"` — fields: `username`, `remote_ip`. Token value is NEVER logged.

**Post-setup behavior:** The /setup endpoint refuses all further requests (409). The only path to additional users is the admin Users panel or `yakos console user add` (future phases).

---

### GET /setup.js

**Auth:** None (token-exempt static asset)

The setup form JS, served same-origin under `script-src 'self'`.

---

## CLI: yakos console bootstrap-token

**Auth:** Local host trust (must run on the daemon host; file-system trust via stateDir 0700)

**Command:** `yakos console bootstrap-token`

**Behavior:**
- When `Count()==0`: generates a fresh 30-minute setup token, writes marker file at `<stateDir>/setup-token` (0600), prints token to stdout. Previous token (if any) is replaced.
- When `Count()>0`: error; directs operator to Users panel or `yakos console user add`.

**Output (stdout):** Raw base64url token only (no surrounding text).
**Output (stderr):** Human-readable status message and security reminder.

**Security note:** The token is the recovery path if the original daemon-startup token is lost or expired. Transmit only over a secure channel (e.g., SSH session to the daemon host).

---

## Console Users Management API (ADR-0005 §D6, Phase 5)

**Implemented in:** `cli-go/internal/consoleui/users_handler.go`
**Available on:** networked console bind (`NetworkedMode=true`) and loopback
**ADR reference:** ADR-0005 §Admin Users panel and CLI mirror

### Auth requirements

All `/api/users/*` endpoints require `RoleAdmin`.
`/api/account` and `/api/account/password` require any authenticated user (`RoleRead` minimum).
Session-authenticated mutations require `X-CSRF-Token` (double-submit pattern, ADR-0005 §CSRF).
mTLS-authenticated mutations are CSRF-exempt (client certs are not browser-auto-attached).

### Self-protection invariant

Any operation that would leave **zero non-disabled admins** is rejected with `409 Conflict`.
Guard checked atomically before any mutation via `userstore.AdminCount()`.

---

### GET /api/users

List all users. **No password hashes are ever returned.**

**Auth:** RoleAdmin
**CSRF:** N/A (GET)

**Response 200:**
```json
{
  "users": [
    {
      "username": "alice",
      "role": "admin",
      "disabled": false,
      "passwordResetReq": false,
      "createdAt": "2026-06-15T10:00:00Z",
      "updatedAt": "2026-06-15T10:00:00Z",
      "failedAttempts": 0,
      "lockedUntil": "0001-01-01T00:00:00Z",
      "sessionEpoch": 0
    }
  ]
}
```

---

### POST /api/users

Create a new user.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Not idempotent. Second create with same username → 409. Username is the natural idempotency key; GET first if needed.

**Request body:**
```json
{
  "username": "charlie",
  "password": "securelongpassword1",
  "role": "read"
}
```
Valid roles: `read`, `dispatch`, `flows-run`, `admin`
Password minimum: `userstore.MinPasswordLen` (12) characters.
Username rules: `[A-Za-z0-9._@-]{1,64}`; `.` and `..` rejected.

**Response 201:**
```json
{ "ok": true, "message": "user created" }
```

**Error responses:**
- `400` — invalid username, password too short, invalid role
- `409` — username already exists

---

### POST /api/users/role

Change a user's role. Bumps `sessionEpoch`, immediately invalidating all live sessions for the target user.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Idempotent (setting the same role twice is a no-op).
**Self-protection:** Demoting the last admin → 409.

**Request body:**
```json
{ "username": "bob", "role": "dispatch" }
```

**Response 200:**
```json
{ "ok": true, "message": "role updated" }
```

**Error responses:**
- `400` — invalid role
- `404` — user not found
- `409` — would demote the last admin

---

### POST /api/users/reset-password

Admin-initiated password reset. Sets a temporary password and marks `passwordResetReq=true`. Bumps `sessionEpoch`, invalidating live sessions.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Idempotent (second reset just re-hashes; `passwordResetReq` stays true).

**Request body:**
```json
{ "username": "bob", "newPassword": "temporarypassword1" }
```

**Response 200:**
```json
{ "ok": true, "message": "password reset; user must change on next login" }
```

**Error responses:**
- `400` — password too short, `newPassword` missing
- `404` — user not found

---

### POST /api/users/disable

Disable a user account. Bumps `sessionEpoch` and revokes all live sessions via `RevokeAllForUser`.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Idempotent (disabling an already-disabled user is a no-op store-side).
**Self-protection:** Disabling the last active admin → 409.

**Request body:**
```json
{ "username": "bob" }
```

**Response 200:**
```json
{ "ok": true, "message": "user disabled" }
```

**Error responses:**
- `404` — user not found
- `409` — would disable the last admin

---

### POST /api/users/enable

Re-enable a disabled user account.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Idempotent (enabling an already-enabled user is a no-op store-side).

**Request body:**
```json
{ "username": "bob" }
```

**Response 200:**
```json
{ "ok": true, "message": "user enabled" }
```

**Error responses:**
- `404` — user not found

---

### POST /api/users/delete

Permanently delete a user. Revokes all live sessions **before** deletion. Irreversible.

**Auth:** RoleAdmin
**CSRF:** Required for session-auth callers
**Idempotency:** Second delete → 404 (user already gone).
**Self-protection:** Deleting the last active admin → 409.

**Request body:**
```json
{ "username": "bob" }
```

**Response 200:**
```json
{ "ok": true, "message": "user deleted" }
```

**Error responses:**
- `404` — user not found
- `409` — would delete the last admin

---

### POST /api/account/password

Self-service password change. The requesting user changes their **own** password only. Verifies the old password before setting the new one. Bumps `sessionEpoch` (all sessions for this user are invalidated; re-login required).

**Auth:** Any authenticated user (RoleRead minimum)
**CSRF:** Required for session-auth callers
**Idempotency:** Not idempotent (each call bumps epoch and produces a new hash).
**Scope:** Operates on the **authenticated user's own account** (identity from resolved `netid.Identity`). No target-username field; cannot be used to change another user's password.

**Request body:**
```json
{ "oldPassword": "correcthorsebattery1", "newPassword": "mynewpassword456" }
```

**Response 200:**
```json
{ "ok": true, "message": "password changed" }
```

**Error responses:**
- `400` — `oldPassword` or `newPassword` missing; `newPassword` too short
- `401` — old password incorrect (generic; no distinguishing detail)
- `401` — user not authenticated

---

### GET /api/account

Whoami: returns the authenticated user's operatorId, role, and authMethod.

**Auth:** Any authenticated user (RoleRead minimum)
**CSRF:** N/A (GET)

**Response 200:**
```json
{
  "operatorId": "alice",
  "role": "admin",
  "authMethod": "session"
}
```
`authMethod` values: `session` (password+cookie), `cert` (mTLS), `loopback` (loopback bearer).

**Error responses:**
- `401` — not authenticated

---

### CLI mirror: `yakos console user`

All admin operations above are available as CLI subcommands. The CLI operates directly on the user store file (no HTTP); it is trusted (daemon-host only).

```
yakos console user add <name> [--role <role>]   Create user (prompts for password, no echo)
yakos console user list                          List all users (tabular; no password hashes)
yakos console user set-role <name> <role>        Change role
yakos console user reset-password <name>         Set temporary password
yakos console user disable <name>                Disable account
yakos console user enable <name>                 Re-enable account
yakos console user delete <name>                 Delete user
```

All CLI commands enforce the same self-protection guard (cannot remove last admin).
Audit entries written to slog with `actor=cli`.

**Source:** `cli-go/internal/consolecmd/usercmd.go`

---

## Console Bash Pass-Through (feat/console-bash-backend)

**Implemented in:** `cli-go/internal/consoleui/bash_handler.go`
**Available on:** console bind (both loopback and networked)
**Flag:** `yakos serve --console-allow-bash` (required to enable over the network)

### Security model

| Connection | AllowNetworkedBash | Behaviour |
|---|---|---|
| Loopback (`127.x`, `::1`, `localhost`) | any | Always permitted |
| Non-loopback (networked) | `false` (default) | 403 |
| Non-loopback (networked) | `true` | Permitted |

Role gate: **RoleAdmin only**. RoleRead / RoleDispatch / RoleFlowsRun → 403.
CSRF + JSON-content-type gates: applied by the global middleware stack (same as all other console mutations). mTLS cert clients are CSRF-exempt.

Audit: every call writes `slog.Warn "consoleui: audit: bash exec"` with `operator_id`, `command`, and `exit_code`.

### POST /api/console/bash

**Auth:** RoleAdmin (session cookie + CSRF, or mTLS cert with admin role, or loopback bearer)
**Content-Type:** `application/json` (required; enforced globally)
**CSRF:** `X-CSRF-Token` header required for session-auth callers; exempt for mTLS and loopback
**Idempotency-Key:** Not declared — shell commands are side-effecting by nature. Do NOT retry without human review.
**Rate limit:** Inherits project default rate-limit class (no override).

**Request body:**
```json
{ "command": "ls -la" }
```
- `command`: required; empty or whitespace-only → 400.
- No other fields accepted (DisallowUnknownFields).

**Response 200:**
```json
{
  "stdout": "total 48\ndrwxr-xr-x  ...",
  "stderr": "",
  "exit_code": 0,
  "truncated": false
}
```
- `stdout`: captured standard output. Hard-truncated at 16384 bytes; truncation marker appended.
- `stderr`: captured standard error. Hard-truncated at 16384 bytes; truncation marker appended.
- `exit_code`: shell process exit code. `-1` means timeout or process-start failure.
- `truncated`: `true` if either stdout or stderr was truncated.

Execution: `sh -c <command>` with `Dir=WorkspaceRoot`. Timeout: 30 seconds.

**Response 400:**
```json
{ "error": "command is required" }
```

**Response 403 (non-loopback, flag off):**
```json
{ "error": "bash is loopback-only; start with --console-allow-bash to enable over the network" }
```

**Response 403 (wrong role):**
```json
{ "error": "forbidden" }
```

### Daemon flag

```
yakos serve --console-allow-bash
```

When set alongside `--console-bind <non-loopback-addr>`, a WARNING banner is printed to stderr at startup:

```
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
  WARNING: --console-allow-bash is active on a networked console.
  POST /api/console/bash executes arbitrary shell commands on this host.
  Any operator with RoleAdmin can run arbitrary code via this endpoint.
  Only enable this flag if you understand and accept the risk.
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
```

### Implementation files

| File | Purpose |
|---|---|
| `cli-go/internal/consoleui/bash_handler.go` | Handler, loopback check, exec, truncation, audit |
| `cli-go/internal/consoleui/server.go` | `AllowNetworkedBash` field in `Config`; route registration |
| `cli-go/internal/serve/serve.go` | `ConsoleAllowBash` field in `Config`; WARNING banner |
| `cli-go/cmd/yakos/main.go` | `--console-allow-bash` CLI flag in `runServe` |
| `cli-go/internal/serve/export_test.go` | `BuildConsoleCfgForTest` wiring of `AllowNetworkedBash` |
| `cli-go/internal/consoleui/bash_handler_test.go` | 10 tests covering all gate conditions |
| `cli-go/internal/serve/bash_wiring_test.go` | 2 tests: wiring guard + default-false guard |
