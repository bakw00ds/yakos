# Domain 4: Documentation + Architecture

**Goal:** Ensure the system is understandable by a new engineer in
< 1 day and that architecture, API, and operational knowledge are
captured, current, and discoverable.

This playbook is invoked by the framework's `doc-writer` agent
(`references: playbook:04-docs-architecture`).

---

## Scope

- Repo-level READMEs and setup guides
- API documentation (OpenAPI / Swagger / equivalent)
- Architecture diagrams (C4 levels 1–3 minimum)
- Architecture Decision Records (ADRs)
- Code-level documentation (godoc, dartdoc, JSDoc, etc.) for public
  APIs
- Runbooks / operational docs
- Onboarding docs
- Changelog / release notes process

## Automated pass

### 4.1 OpenAPI generation and linting

Generate OpenAPI spec from your backend:

```bash
# Go + Echo
swag init -g <entrypoint>.go -o docs/openapi --parseDependency --parseInternal

# Other ecosystems use their respective generators (swagger-jsdoc,
# drf-spectacular, NestJS @nestjs/swagger, etc.)
```

Lint:

```bash
spectral lint docs/openapi/swagger.json --format=json > raw/04-docs-architecture/spectral.json
```

Compare endpoints in generated spec against actual route
registrations:

- Route in code but missing from spec → **P1**
- Route in spec but not in code → P2
- Route documented but missing response schema → P2
- Route with no description / summary → P3

### 4.2 Doc coverage

Per-language tools:

```bash
# Go
golangci-lint run --disable-all --enable=revive --enable-all=godot > raw/04-docs-architecture/go-doc-gaps.txt

# Dart
dart doc --output raw/04-docs-architecture/dartdoc/ 2>&1 | tee raw/04-docs-architecture/dartdoc.txt

# TypeScript
npx typedoc --plugin typedoc-plugin-coverage > raw/04-docs-architecture/ts-doc-coverage.txt
```

Threshold: ≥ 80% of exported / public symbols documented. Below = P2.

### 4.3 Link checking

```bash
lychee --format json --output raw/04-docs-architecture/lychee.json './**/*.md'
```

Any 4xx/5xx → P3 (P2 if it's a primary setup doc).

### 4.4 Prose linting (optional)

```bash
vale --output=JSON ./docs > raw/04-docs-architecture/vale.json
```

Helpful for consistency; results are advisory / Info-level.

### 4.5 Diagram freshness

For each diagram in `/docs/architecture/`:

- Last modified date recorded
- If older than the oldest modified service directory → P2 ("diagram
  likely stale")
- If there's no diagram for a major service → **P1**

## Manual pass

### Manual §Repo root

- [ ] `README.md` covers: what it is, who it's for, how to run
  locally in < 10 min, how to run tests, where to find more docs
- [ ] `CONTRIBUTING.md` or equivalent exists
- [ ] `LICENSE` present and correct
- [ ] `.env.example` or equivalent lists required env vars with
  descriptions
- [ ] Setup guide tested by an outside human within the last 90
  days (or document it)

### Manual §Architecture docs

Required documents at minimum:

- [ ] **System Context (C4 L1)** — the system and its external
  actors / systems
- [ ] **Container Diagram (C4 L2)** — services, databases, caches,
  external dependencies
- [ ] **Component Diagram (C4 L3)** — major modules within each
  service
- [ ] **Data flow / sequence diagrams** for critical flows: auth,
  primary writes, primary reads, billing
- [ ] **Deployment diagram** — where things run, ingress, egress,
  network boundaries, where regulated data is stored
- [ ] **Data model overview** — entity relationships, which tables
  contain regulated data

Each diagram:

- [ ] Has a date and author
- [ ] Source is in-repo (Mermaid, Structurizr DSL, or SVG source) —
  not just a PNG
- [ ] Referenced from README or `/docs/architecture/README.md` index

### Manual §ADRs

- [ ] `/docs/adr/` exists with numbered markdown files
- [ ] ADR template used consistently
- [ ] At minimum, ADRs exist for: database choice, auth/session
  strategy, deployment platform, frontend framework, major
  third-party integrations, any regulatory-relevant decision
- [ ] Superseded ADRs marked as such (not deleted)

### Manual §API docs

- [ ] OpenAPI spec is published and browsable (Swagger UI or Redoc)
- [ ] Every endpoint has summary, description, request/response
  schemas, auth requirement, error responses
- [ ] Example requests provided for non-trivial endpoints
- [ ] Versioning strategy documented
- [ ] Rate limits documented
- [ ] Webhook / callback docs (if any)

### Manual §Code docs

- [ ] Package-level doc.go (Go) or library.dart (Dart) or module
  README explains purpose
- [ ] All exported / public functions / types / methods documented
- [ ] Non-obvious internal functions documented
- [ ] No stale comments contradicting current code (spot-check 10
  samples)

### Manual §Runbooks

Required runbooks at minimum:

- [ ] Incident response — who to contact, how to escalate
- [ ] Database restore from backup
- [ ] Rolling back a release
- [ ] Rotating a leaked secret
- [ ] Responding to a regulated-data access request (if applicable
  per playbook 06)
- [ ] Responding to a regulated-data deletion request
- [ ] Standard deploys
- [ ] On-call checklist

### Manual §Operational docs

- [ ] Monitoring / alerting overview — what's monitored, what
  thresholds, who gets paged
- [ ] Logging — format, retention, where to find logs
- [ ] Environments — dev, staging, prod — purposes and data policies
- [ ] Backup policy — what's backed up, frequency, retention,
  tested-restore cadence
- [ ] Disaster recovery — RTO / RPO targets

### Manual §Build & test

- [ ] CI pipeline documented (what runs when, where artifacts go)
- [ ] Build reproducibility — same commit → same artifact
  (checksummed)
- [ ] Test pyramid documented — unit, integration, e2e proportions
  and intent
- [ ] Regression test approach documented

### Manual §Release process

- [ ] Release checklist exists (this audit becomes part of it)
- [ ] Changelog maintained (Keep A Changelog format recommended)
- [ ] Versioning scheme documented (SemVer)
- [ ] Database migration process documented
- [ ] Rollback steps documented

### Manual §Onboarding

- [ ] New-engineer day-1 doc gets someone from clone to running
  tests
- [ ] Glossary of domain terms (project-specific)
- [ ] Links to team comms, on-call, access requests

## Findings synthesis

Group by:

1. Missing documents (with severity based on criticality of missing
   content)
2. Stale / out-of-date documents (with evidence of staleness)
3. API documentation gaps
4. Code documentation coverage (with percentages per package)
5. Link rot

## Known gotchas (cross-project)

- Documentation generators that require annotations on handlers
  break silently when handlers use method receivers or grouped
  routes; verify spec against real routes.
- `@internal` / `@hidden` / equivalent must be applied correctly,
  not over-applied.
- Diagrams version-control cleanly only if their source is in-repo.
  PNG-only diagrams rot instantly.
