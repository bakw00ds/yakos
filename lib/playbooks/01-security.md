# Domain 1: Security + API Security

**Goal:** Identify vulnerabilities, misconfigurations, exposed secrets,
dependency CVEs, and insecure API patterns across the project's stack.

This playbook is invoked by the framework's `security-reviewer` agent
(`references: playbook:01-security`) and by project-specific
release-audit skills.

---

## Scope

- Backend (routes, middleware, auth, session, crypto, data access)
- Dependencies (language ecosystem packages and lockfiles)
- Container images (if any)
- Secrets hygiene (repo + runtime config)
- API surface (OWASP API Top 10)
- Authentication flows (OAuth, session, CSRF, token handling)
- Mobile/desktop clients (insecure storage, deep links, trust boundaries)

## Automated pass

Run these in order. Capture raw output to `raw/01-security/`.

### 1.1 Secret scanning (ALWAYS FIRST)

```bash
gitleaks detect --source . --report-path raw/01-security/gitleaks.json --report-format json --no-git=false
trufflehog filesystem . --json > raw/01-security/trufflehog.json
```

Any secret finding is **P0**. No exceptions. If a secret was ever
committed, it's burned even if rotated — flag for rotation and note
commit SHA.

### 1.2 SAST

Language-appropriate static analysis. Examples:

```bash
# Go
gosec -fmt json -out raw/01-security/gosec.json ./...
staticcheck -f json ./... > raw/01-security/staticcheck.json
golangci-lint run --out-format json > raw/01-security/golangci.json

# Dart / Flutter
dart analyze --format=json > raw/01-security/dart-analyze.json

# JS/TS
npx eslint --format json . > raw/01-security/eslint.json
semgrep --config=auto --json -o raw/01-security/semgrep.json
```

Triage by confidence + severity: High/High → P0; High/Medium or
Medium/High → P1; others P2.

### 1.3 Dependency vulnerabilities

```bash
# Go
govulncheck -json ./... > raw/01-security/govulncheck.json
osv-scanner --format json --recursive . > raw/01-security/osv.json

# Node
npm audit --json > raw/01-security/npm-audit.json

# Python
pip-audit --format json > raw/01-security/pip-audit.json

# Flutter
dart pub outdated --json > raw/01-security/pub-outdated.json
```

CVE → criticality:

- Critical CVE with known exploit → **P0**
- High CVE → **P1**
- Medium CVE in direct dep → **P2**
- Medium CVE in transitive dep with no call path → **P3**

Use `govulncheck` (Go) or equivalent reachability analysis to
downgrade unreachable CVEs to P3.

### 1.4 Container scanning (if applicable)

```bash
trivy image --format json -o raw/01-security/trivy-<image>.json <image>:<tag>
trivy config --format json -o raw/01-security/trivy-iac.json .
```

### 1.5 DAST — OWASP ZAP baseline

Stage a running instance against a seeded staging DB with synthetic,
non-production data.

```bash
docker run --rm -v "$(pwd)/raw/01-security:/zap/wrk" \
  ghcr.io/zaproxy/zaproxy:stable zap-baseline.py \
  -t https://<staging-url> \
  -r zap-baseline.html \
  -J zap-baseline.json
```

Authenticated scans use `zap-api-scan.py` with a login script.
Record the auth-flow script alongside the output.

### 1.6 OpenAPI fuzzing

```bash
schemathesis run <openapi-spec-url-or-path> \
  --checks all \
  --hypothesis-max-examples=200 \
  --report raw/01-security/schemathesis.tar.gz
```

### 1.7 Network surface (if infra is in scope)

```bash
nmap -sV -sC -oX raw/01-security/nmap.xml <target>
nuclei -u https://<target> -j -o raw/01-security/nuclei.json
```

Only run against hosts you own and for this release window. Document
authorization.

## Manual pass

Automated scanners catch ~60% of real issues. Manual is where
operational experience pays. Each item below becomes a finding if
broken.

### Manual §Auth

- [ ] OAuth state parameter present and validated on every callback
- [ ] PKCE used for public clients
- [ ] Session cookies: `HttpOnly`, `Secure`, `SameSite=Lax` or stricter
- [ ] Session fixation: session ID rotates on login
- [ ] Logout invalidates server-side session, not just client cookie
- [ ] Password reset tokens: single-use, time-bound (≤60 min), unpredictable
- [ ] Account enumeration: login / reset / signup return identical
  timing + response for valid-vs-invalid accounts
- [ ] 2FA offered to users; enforced for admin/elevated roles
- [ ] JWT (if used): strong alg pinned (no `alg: none`, no alg
  confusion), short TTL, refresh rotation
- [ ] Rate limiting on auth endpoints — per IP AND per account
- [ ] Lockout policy documented and tested

### Manual §Authorization

- [ ] IDOR: every endpoint that takes an ID validates the caller
  owns/can-access that resource
- [ ] Horizontal privilege escalation: tenant A cannot access tenant
  B's data
- [ ] Vertical escalation: lower-privilege role cannot hit higher-
  privilege endpoints
- [ ] Mass assignment: JSON binding explicitly allowlists fields
- [ ] GraphQL (if any): query depth + complexity limits set

### Manual §Input handling

- [ ] All DB queries parameterized (zero string concatenation)
- [ ] File upload: type checked (magic bytes, not extension), size
  capped, stored outside web root, served via signed URLs or
  streaming handler
- [ ] Image processing: ImageMagick/libvips policies restrict
  exploitable formats
- [ ] SSRF: no user-controlled URLs fetched server-side without
  allowlist
- [ ] XXE: XML parsers have external-entity processing disabled
- [ ] Deserialization: no unsafe deserializers on untrusted input

### Manual §Output handling

- [ ] CSP header set; no `unsafe-inline` or `unsafe-eval` unless
  justified and documented
- [ ] HSTS set with long max-age on production domains
- [ ] CORS: explicit origin allowlist; no `*` with credentials
- [ ] Error responses don't leak stack traces or internal paths in
  production

### Manual §Crypto

- [ ] TLS 1.2 minimum, prefer 1.3; weak ciphers disabled
- [ ] Certs valid, not expiring within 30 days
- [ ] Passwords: argon2id or bcrypt with appropriate cost; no
  MD5/SHA1/PBKDF2-low-iter
- [ ] Keys in KMS or env-provisioned secret store, never in repo
  or image
- [ ] Randomness: cryptographically-strong source for security-
  sensitive values (tokens, nonces, salts), never PRNGs

### Manual §Logging / info disclosure

- [ ] No regulated data in logs (connects to playbook 06)
- [ ] No auth tokens, session IDs, or passwords in logs
- [ ] Verbose errors gated by env flag; prod returns generic messages
- [ ] Debug endpoints disabled or bound to localhost in production

### Manual §Mobile-specific (if applicable)

- [ ] Secure storage used for tokens (not generic shared preferences)
- [ ] Cert pinning for API calls (or documented reason not to)
- [ ] Deep / universal link handlers validate origin
- [ ] Root/jailbreak detection considered (risk-based, document
  decision)
- [ ] No sensitive data in app screenshots (iOS background obscuring)

### Manual §API-specific (OWASP API Top 10)

Walk each item:

1. **Broken Object Level Authorization** — covered above
2. **Broken Authentication** — covered above
3. **Broken Object Property Level Authorization** — field-level: can
   a low-privilege user PATCH and elevate themselves?
4. **Unrestricted Resource Consumption** — rate limits, pagination
   caps, query depth
5. **Broken Function Level Authorization** — admin routes reachable
   via role manipulation?
6. **Unrestricted Access to Sensitive Business Flows** — bulk
   exports, mass operations — abusable?
7. **Server Side Request Forgery** — covered in Input handling
8. **Security Misconfiguration** — defaults, debug endpoints,
   directory listing
9. **Improper Inventory Management** — all API versions documented?
   Deprecated endpoints still reachable?
10. **Unsafe Consumption of APIs** — third-party calls — error
    handling, response validation, trust assumptions

## Findings synthesis

One finding per issue, using the project's report template. Group by
OWASP category in the report body but keep criticality as the
primary sort key.

## Known gotchas (cross-project)

- HTTP clients with no timeout default to wait-forever; audit for
  bare `http.Client{}` declarations.
- Default form/JSON binders accept multiple content types for the
  same struct; review every handler for mass-assignment.
- Session stores: confirm production uses a backed store (Redis,
  database), not cookie-store, for sensitive flows.
- SMTP relays: confirm SPF/DKIM/DMARC on the sending domain.

Project-specific gotchas belong in `<project>/.claude/rules/` and the
project's `INCIDENT-CATALOG.md`.
