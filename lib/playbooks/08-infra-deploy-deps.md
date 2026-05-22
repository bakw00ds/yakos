# Domain 8: Infrastructure — DB + Migrations + CI/Deploy + Dependencies

**Goal:** Audit the layers BELOW the application — database health,
migration discipline, CI/deploy pipeline, dependency hygiene, and the
hidden-but-load-bearing config files (reverse proxy, edge tunnel,
systemd, cron).

This playbook is invoked by the framework's `infra-auditor` agent
(`references: playbook:08-infra-deploy-deps`) and by project-specific
release-audit skills.

The other domains touch infra in passing — Domain 5 (Performance)
covers DB query plans, Domain 2 (Code Quality) covers CI lint — but
no other domain owns reverse-proxy + tunnel config, migration
sequencing, deploy mechanics, or supply-chain hygiene end-to-end.

---

## When this domain is in scope

Always, if the project ships to production. The scope-down lever
isn't "skip" — it's "limit to what changed in the audit window" if
the deploy infra is mature + stable.

## Scope

- Database schema sanity (PKs, FKs without indexes, unbounded tables)
- Migration sequencing + reversibility
- Backup + restore drill cadence
- CI/CD pipeline (workflows, matrix, gates, secrets)
- Deploy mechanism (auto vs manual, rollback path, blue/green vs
  rolling)
- Reverse proxy + edge config (nginx, Caddy, Cloudflare tunnel,
  service mesh)
- Dependency freshness + CVE coverage per language ecosystem
- License compliance + SBOM generation
- Subprocessor / vendor inventory (data-flow lineage)

## Audit scope rules

- **Read state from the SERVER, not just the repo.** Configs in
  `deploy/` may not match what's actually on prod. Cron entries,
  systemd units, and reverse-proxy configs drift. Spot-check live
  state.
- **Network ingress paths are first-class.** Reverse proxy + edge +
  CDN are part of the production system. Misconfig in any of them
  takes the app down regardless of how clean the application code is.
- **Migrations are append-only by convention.** Spot-check the last
  10 for index coverage on new columns + working `.down.sql`. Any
  gap in the numbering deserves a 1-line explanation in the audit
  report.

## Read first

- All migration files (note any numeric gaps)
- Pre-push / pre-commit hooks; deploy scripts
- `deploy/`, `infra/`, `terraform/`, `pulumi/` (whichever applies)
- `cron/`, `systemd/`, `logrotate/`
- Reverse-proxy configs for every environment
- Backup scripts + retention policy
- `.github/workflows/` (or other CI config)
- Lockfiles for every language ecosystem in the repo
- License-attribution files (`NOTICES.md`, `THIRD-PARTY.md`)
- Runbooks: `db-restore.md`, `migration-ownership.md`,
  `incident-response.md`, `rollback.md` (or surface that they don't
  exist)

## Automated pass

Run these in order. Capture raw output to `raw/08-infra/`.

### 8.A.1 Schema sanity (Postgres example; adapt for other RDBMS)

```bash
# Tables without primary keys (anti-pattern)
psql -c "SELECT t.table_name FROM information_schema.tables t \
  LEFT JOIN information_schema.table_constraints c \
  ON c.table_name = t.table_name AND c.constraint_type = 'PRIMARY KEY' \
  WHERE t.table_schema = 'public' AND c.constraint_name IS NULL;"

# Largest tables (candidates for retention policy review)
psql -c "SELECT relname, n_live_tup FROM pg_stat_user_tables \
  ORDER BY n_live_tup DESC LIMIT 20;"

# Unused indexes
psql -c "SELECT relname, indexrelname, idx_scan FROM pg_stat_user_indexes \
  WHERE idx_scan = 0 AND indexrelname NOT LIKE 'pg_%';"
```

`audit_log`-style tables are common offenders: they grow
monotonically + nothing prunes them. If retention isn't documented,
P1.

### 8.A.2 Migration health

- Numbered sequentially? List numeric gaps + classify each
  (intentional renumber? mistakenly skipped? deleted?)
- Each `.up.sql` has a working `.down.sql`?
- Single-statement migrations that change semantics (drop NOT NULL,
  change column type) flagged for split per expand→migrate→contract
- Any migration files committed without going through the pre-push
  gate?

### 8.A.3 Backup + restore

- Backup script + cron entry installed on prod?
- Verified by a recent restore drill (per quarterly cadence)?
- Offsite copy configured?
- Encryption-at-rest for backups confirmed?
- Drill report exists for the last quarter?

Re-verify cron entries are still installed on every audit cycle —
configs drift.

### 8.B.1 Reverse-proxy + edge syntax

```bash
# nginx config syntax (run on a host that has the config)
nginx -t -c /etc/nginx/nginx.conf

# Caddy
caddy validate --config /etc/caddy/Caddyfile

# Cloudflare tunnel (cloudflared)
cloudflared tunnel info <tunnel-id>
```

Missing syntax checks in CI for any of these = P1.

### 8.C.1 Dependency hygiene per language

```bash
# Go
go mod tidy && git diff go.mod go.sum    # any drift?
go list -m -u all                        # check for upgrades
govulncheck ./...                        # CVE scan

# Node
npm outdated
npm audit --production

# Python
pip-audit
pip list --outdated

# Ruby
bundle outdated
bundle audit

# Rust
cargo outdated
cargo audit

# Flutter / Dart
flutter pub outdated
# (no first-party Dart CVE tool; cross-check pub.dev advisories)
```

### 8.C.2 License compliance

```bash
# Pick the right tool per ecosystem
license-checker --json > raw/08-infra/licenses-node.json
go-licenses csv ./... > raw/08-infra/licenses-go.csv
pip-licenses --format=json > raw/08-infra/licenses-py.json
```

Verify the project's license-attribution file matches. Any new dep in
the audit window introduced a license risk (GPL-family in a
proprietary product, etc.) = P1.

### 8.C.3 SBOM

```bash
# Syft is polyglot
syft . -o spdx-json > sbom.spdx.json
```

If no SBOM (SPDX or CycloneDX) is generated for releases, P2 — this
is becoming a compliance ask in supply-chain regulations (EO 14028,
EU CRA).

## Manual pass

### 8.A.M Database operational health

- Connection pooling configured for expected concurrency?
- Pool metrics exposed (active / idle / wait)?
- Long-running transactions monitored?
- Read-replica strategy documented if applicable?
- VACUUM / autovacuum tuning reviewed (Postgres)?

### 8.B.M CI / Deploy mechanics

- How does code reach prod? (cron pull, GitOps, manual, CD pipeline?)
- What's the rollback path? (`docs/runbooks/rollback.md` should
  exist)
- Verify by reading the auto-deploy log: how many deploys per day?
  Any 5xx during deploys?
- Pre-push / pre-merge gates: VERSION + changelog enforcement?
  Substantive-change detection?
- CI matrix covers all artifacts (api / web / mobile separately, or
  in one job)?
- Hook race conditions / pipe-failure modes (e.g., `set -euo
  pipefail` + `grep -q` on a large pipe causing SIGPIPE) addressed?
- Browser-driven login smoke as a release gate (catches CSP, cookie,
  CORS regressions that unit tests miss)?
- Reverse-proxy syntax check in CI (`nginx -t`, `caddy validate`,
  etc.)?

### 8.B.M Reverse-proxy + tunnel discipline

- Reverse proxy doesn't unconditionally send `Connection: upgrade` on
  paths that aren't websockets (a common copy-paste bug from dev/HMR
  templates that breaks chunk loads in prod)
- Upstream timeouts appropriate for SSE/long-poll endpoints (SSE
  wants `proxy_read_timeout 86400s` minimum, `proxy_buffering off`)
- Edge tunnel (cloudflared, ngrok, etc.) protocol setting
  intentional (default `quic` is fast; `http2` more forgiving on
  degraded paths)
- HA tunnel connection count + failover behavior documented
- Origin certificate rotation date documented; renewal automation
  verified
- CSP / HSTS / cookie-flag headers set at the right layer (origin
  vs edge — pick one and document)

### 8.C.M Subprocessor + vendor inventory

For health-adjacent or regulated-data products: every vendor that
touches data should be in a list, with:

- BAA / DPA status
- Data residency + transfer mechanism (SCCs, etc.)
- Retention policy on the vendor side
- Off-boarding procedure (data deletion request)

If any subprocessor is missing from the inventory, P1. If a vendor
is in the inventory but BAA/DPA status is "UNVERIFIED" or "TBD" for
PHI-touching workloads, P0.

### 8.C.M Secrets handling at deploy time

- Secrets injected at runtime from a secret store (vault, KMS,
  cloud secret manager) — not baked into images
- Rotation cadence documented; last rotation date recorded
- Break-glass access procedure documented
- Audit log of secret access exists + is reviewed

### 8.B.M Observability backstop

- Synthetic monitoring against the production login + 1 critical
  user journey
- Alert routing tested (page actually arrives at the right person)
- Status page (or equivalent communication channel) exists for
  user-facing incidents
- On-call rotation documented + accessible

## Findings synthesis

ID prefixes:

- `DB-` — DB / migration findings
- `CI-` — CI / deploy / release-pipeline findings
- `INFRA-` — reverse proxy, tunnel, network, secrets, observability
- `DEP-` — dependency / supply-chain findings

Severity heuristics:

- Anything that breaks live serving (proxy config, missing cron entry
  on prod) → **P0** if currently broken, **P1** if "would break
  under load"
- Missing rollback playbook, missing CVE scan, missing SBOM,
  unverified BAA on PHI-touching subprocessor → **P1**
- Migration numbering gaps, dep-version drift, perf budgets without
  measurement source → **P2**
- Stale doc references in runbooks, missing one-of-many CI matrix →
  **P3**

## §Monitor presence (gated on profile.standards.monitors)

Audit checks fire only when the project has opted into Standard 3
in its `.yakos.yml` (`profile.standards.monitors: true`). See
`rule:monitor-discipline` and `skill:monitor-scaffold`.

### Automated checks

- **Supervisor config per target service.** For each service
  in `profile.monitors.targets`, look for at least one of:
  `deploy/systemd/<service>.service`, an entry in
  `ecosystem.config.js`/`.json` (pm2), a Deployment yaml in
  `deploy/k8s/` referencing the service name, or a
  `docker-compose.yml` service entry. Severity: **P1** if
  absent.

- **`/healthz` (or `/health`, `/ready`) endpoint present.** Grep
  handler / route files for the literal path. Severity: **P1**
  if absent in a target service.

- **Healthcheck is not a one-line return.** Read the
  healthcheck handler body; if it's a single literal
  `return 200` / `c.JSON(200, "ok")` / `res.send("ok")`
  with no actual liveness check (no DB ping, no downstream
  test), flag. Severity: **P2**.

- **Supervisor restart policy declared.** systemd unit has
  `Restart=...` directive; pm2 has `autorestart: true`; k8s
  has restart policy; docker-compose has `restart:`. Severity:
  **P2** if absent (Manual-only restart is fine for short-lived
  jobs but suspect for services in `profile.monitors.targets`).

- **Runbook exists.** Look for
  `docs/runbooks/<service>-runbook.md` or `RUNBOOK.md` at
  repo root with a section per target service. Severity: **P3**
  per service without a runbook.

### Manual checks

- **Healthcheck checks actual dependencies.** The handler
  pings the DB, verifies queue accessibility, confirms
  downstream services reachable. Severity: **P2** per
  service whose healthcheck skips obvious deps.

- **Initial-delay tuning.** k8s `initialDelaySeconds` is at
  least 30 for slow-starting services (boot migrations,
  warm cache, etc.). Severity: **P3** if tuned too tight.

- **Drain procedure documented.** Runbook covers graceful
  shutdown (SIGTERM handling, in-flight request drain,
  load-balancer deregistration). Severity: **P3** if not
  documented.

### Skill scaffold

If audit finds gaps but `profile.standards.monitors == true`,
surface the recommendation to run `skill:monitor-scaffold`.

## Known gotchas (cross-project)

- **Edge tunnel restarts cause transient instability.** Each restart
  cycles connections; the edge takes minutes to fully re-stabilize.
  Don't restart the tunnel more than once per audit run — repeated
  restarts can push the tunnel into a bad state for 30+ minutes.
- **Reverse-proxy `Connection: upgrade` on non-websocket paths**:
  HMR-pattern config copy-pasted from a dev template into prod.
  Silent break of static-asset chunk loads. ALWAYS audit
  reverse-proxy configs on PROD, not just in repo.
- **CSP wildcard syntax**: `*.subdomain.example.com` is valid;
  `subdomain.example.*` is NOT. Only enforced when CSP is enforcing
  (not Report-Only). Spot-check a real login round-trip post any CSP
  change.
- **Pre-push hook SIGPIPE**: `set -euo pipefail` + `grep -q` on a
  large piped diff = SIGPIPE = false-fail. Capture the diff once
  into a variable.
- **Migration numbering collisions**: parallel agent dispatches
  independently claim the same migration number. Always renumber at
  integration time + commit a note in the release commit body about
  the renumber decision.
- **Append-only audit tables**: writes accumulate; nothing prunes.
  If retention isn't enforced, the table grows unboundedly. Confirm
  retention policy + reader-side redaction for any sensitive fields.
- **CI runner secrets exposure**: secrets exposed to PR builds from
  forks = supply-chain attack vector. Workflows should gate secret
  access on merge / approval, not on PR open.
- **Container image base-OS drift**: pinning `node:18` (a tag that
  moves) vs `node:18.20.4-alpine3.19@sha256:...` (immutable). Tag
  drift makes "same commit → same artifact" false.

Project-specific gotchas belong in `<project>/.claude/rules/` and the
project's `INCIDENT-CATALOG.md`.

## Cross-references

- Domain 1 (Security) — TLS, auth-related infra (WAF, rate-limit
  middleware), secrets in CI
- Domain 2 (Code Quality + Observability) — alert / synthetic
  monitoring posture
- Domain 5 (Performance) — DB query plans, index coverage, connection
  pool sizing
- Domain 6 (Regulated-Data) — backup encryption + retention policy on
  sensitive tables, subprocessor BAA status

This domain owns the "between" layers; cross-references go OUT to
other domains for the user-facing impact, not IN from them.
