# Team shapes for different project types

A *team shape* is a recommended composition of agents spawned together
for a particular kind of work. Different project types — and different
stages of the same project's lifecycle — need different teams. The
v0.1 framework ships a generic specialist roster; project-specific
specialists (and v0.2 additions) fill in the gaps.

This document is the practical companion to
[PHILOSOPHY.md "Orchestration shapes"](../PHILOSOPHY.md#orchestration-shapes).
The philosophy describes the principle (one team per logical unit of
work; pick the cheapest tier each role needs); this document is the
catalog of recommended compositions.

## How to read this doc

For each shape:

- **When to spawn** — the specific work that calls for this composition.
- **Roles** — which agents to spawn, and what each contributes.
- **Notes** — caveats, model-tier choices, anti-patterns.

Some shapes reference agents that **don't ship in v0.1**. Those entries
are marked `(NEW — needed)` and serve as both a usage guide and a v0.2
roadmap. If your project needs a shape that uses agents v0.1 doesn't
ship, write the missing roles project-locally in `<project>/.claude/agents/`
— YakOS shadows project agents over framework agents, so a project
file takes precedence over an eventual framework version.

## The principle

**One team per logical unit of work.** A feature team shouldn't also
handle incident response; an incident response team shouldn't also do
greenfield architecture. Spawn the right team for the work; clean up
when done; archive the scratchpad before starting the next unit.

This affects three things:

1. **Context hygiene.** Each team has its own `work/current/` —
   reusing a team across logical units conflates unrelated work in
   the audit trail.
2. **Model cost.** Per Phase 1.5 §22 decision 4: lead, planner,
   db-migrations, go-api, security-reviewer on Opus; troubleshooter,
   test-runner, doc-writer on Sonnet; dependency-update on Haiku.
   A team of 7 all-Opus runs significantly more expensive than a
   mixed-tier team of the same size. Pick the cheapest tier each
   role needs.
3. **Coordination cost.** Larger teams pay more in coordination
   overhead per piece of work. The lead's job is decomposition;
   adding teammates beyond what the work needs is overhead, not help.

---

## Lifecycle stages

Group team shapes by where in the project lifecycle the work sits:

### Stage 1 — Greenfield / project initiation

The project doesn't exist yet. Teams here decide architecture, scope,
initial repo structure. v0.1 doesn't ship a strong specialist for this
stage (the lead does the architectural work itself); v0.2 candidates:
**architect**, **planner** (already shipped, but operates at task
scope, not system scope).

### Stage 2 — Active development

The project exists. Teams here ship features, fix bugs, refactor,
migrate data. **This is the stage v0.1 covers best.** Most v0.1
shapes target this stage.

### Stage 3 — Release / cutover

A version is being shipped. Teams here verify, audit, deploy. v0.1
ships a `code-reviewer` and a `security-reviewer`; the
project-specific `release-auditor` (referenced in PandaOS examples)
runs the audit playbook.

### Stage 4 — Operational / steady-state

The project runs in production. Teams here respond to issues,
monitor, plan next iterations. v0.1's `troubleshooter` covers
diagnosis; **incident-responder**, **log-analyst**, **devops-infra**
are v0.2 candidates.

### Stage 5 — Maintenance / sunset

The project is winding down or in pure-maintenance mode. Teams here
do dependency updates, security backports, eventual archival. v0.1's
`dependency-update` skill is the workhorse.

---

## Stack-specialist templates (v0.1.4)

The framework ships five generic stack-specialist templates carrying
discipline only — no stack names, no specific file paths. They're the
shape of the work, not the implementation:

- `backend` — server-side application code
- `frontend` — web UI
- `mobile` — iOS/Android client
- `database` — schema, migrations, repository layer
- `maintainer` — routine hygiene (dep bumps, lint, dead-code)

A project deploys these templates by writing a thin `extends:` wrapper
in `<project>/.claude/agents/<role>.md` that names the stack
(Go/Echo, Next.js/React, Flutter, Postgres/pgx, etc.), the file paths
(`api/internal/...`, `web/src/...`), and the project's incident lore.
The wrapper carries only the project-specific delta — the framework
template carries the discipline.

This is the same `extends:` mechanism `examples/tiny-go-api/.claude/agents/`
uses for `lead-template` and `test-runner`; it's now available for the
full team shape.

These templates **complement** the v0.2 cross-cutting roster
(architect, incident-responder, release-manager, etc., listed below)
— they're not a substitute for it.

---

## Team shapes — buildable from v0.1

These compositions can be assembled from agents YakOS v0.1 ships,
optionally combined with project-specific specialists.

### Small web app team (Stage 2)

When to spawn: implementing a feature that touches both backend and
frontend.

Roles:

- `lead` — orchestrates
- `planner` (Opus) — decomposes into 3–5 tasks
- `backend` (Sonnet) — implements API; project's `extends:` agent
  fills in the stack (Go, Node, Python, etc.)
- `frontend` (Sonnet) — implements UI; project's `extends:` agent
  fills in the stack (React, Vue, Svelte, etc.)
- `test-runner` (Sonnet) — verifies
- `code-reviewer` (Sonnet) — reviews diffs at handoffs
- `doc-writer` (Sonnet) — updates README/CHANGELOG

Notes: The `contract-handoff` skill is essential here — backend
publishes the API contract to `contracts.md`, frontend reads it.
Don't have the lead invent the contract; the specialist who
implements is the one who knows.

### Mobile feature team (Stage 2)

When to spawn: feature touching mobile + backend.

Roles:

- `lead`
- `planner`
- `mobile` (Sonnet) — project's `extends:` agent picks the platform
  framework (Flutter, React Native, native iOS/Android, etc.)
- `backend`
- `test-runner`
- `code-reviewer`

Notes: Mobile-specific UX/accessibility review **isn't** a v0.1
generic agent. For mobile UX-heavy work, either invoke an audit
skill manually (release-audit covers this in PandaOS) or write a
project-specific `ux-reviewer`. Watch for the flutter-tester-hang
incident — wrap test runs in `timeout 120`.

### Bug investigation team (Stage 2)

When to spawn: a confirmed bug needs diagnosis and fix.

Roles:

- `lead`
- `troubleshooter` (Sonnet) — read-only diagnosis; NEVER edits
- one specialist matching the bug's domain — performs the fix
- `test-runner` — verifies the fix and adds regression coverage

Notes: Smaller team intentionally. The split between
`troubleshooter` (diagnoses) and the domain specialist (fixes) is
load-bearing — see the `troubleshooter` agent body for why
conflating diagnosis with fix produces false-positive root causes.
The troubleshooter dispatches the fix via `SendMessage` once the
diagnosis is verified.

### Database migration team (Stage 2)

When to spawn: any schema change.

Roles:

- `lead`
- `planner`
- `database` (Sonnet) — writes the migration; project's `extends:`
  agent picks the RDBMS and migration runner conventions
- `backend` (for any API contract updates)
- `test-runner`
- `security-reviewer` (Opus) — migrations touch production data

Why `security-reviewer`: migrations are one of the highest-risk
changes a project makes. Per `incident:v2.65.1.2-dual-runner-conflict`,
a single migration can crashloop production for 17 minutes. Worth a
review pass even when the migration looks routine.

### Release-prep team (Stage 3)

When to spawn: cutting a version.

Roles:

- `lead`
- project-specific `release-auditor` (runs the release-audit skill;
  PandaOS-style)
- `doc-writer` — changelog, release notes
- `security-reviewer` — final security pass before tag

Notes: The `release-auditor` invokes the 6-domain audit playbook
(see [`lib/playbooks/`](../lib/playbooks/) — populated in v0.1.1).
The lead approves findings and decides what blocks the release vs
ships as a known-issue.

### Maintenance / dependency team (Stage 5)

When to spawn: scheduled dependency-update sweep.

Roles:

- `lead`
- `maintainer` (Sonnet) — runs dep bumps, lint baseline drains,
  dead-code passes; project's `extends:` agent supplies stack-
  specific commands
- `dependency-update` skill (the mechanics; `maintainer` invokes it)
- `security-reviewer` — flags vulnerabilities being patched
- `test-runner` — verifies the suite still passes
- one specialist per language touched by the updates (only when a
  major-version bump needs domain judgement)

The `dependency-update` skill (v0.1 ships this) handles the
mechanics; specialists evaluate breaking changes; the security-
reviewer prioritizes advisories; the test-runner is the safety net.

---

## Team shapes that need new agents

These compositions reference agents that **don't ship in v0.1**.
Document them so users know what to write project-locally and what's
on the v0.2 roadmap.

### Greenfield architecture team (Stage 1)

Roles:

- `lead`
- **architect** — `(NEW — needed)`
- `planner`
- `security-reviewer`

Notes: An **architect** is a strategic role — data model, API
surface, deployment topology, technology choices. v0.1's `planner`
operates at task scope, not system scope. Until v0.2 ships an
architect agent, the lead does this work itself; that works for
small projects but bottlenecks for substantial greenfield.

### Security-sensitive SaaS team (Stage 2)

Roles:

- `lead`
- `architect`
- backend, frontend, db specialists
- `security-reviewer`
- **privacy-reviewer** — `(NEW — needed)`
- `test-runner`
- **release-manager** — `(NEW — needed)`

Notes: **privacy-reviewer** is distinct from security-reviewer — it
reasons about data classification, retention, regulatory compliance
(GDPR, HIPAA, CCPA), and the third-party service surface. v0.1 has
a HIPAA playbook reference but no privacy-reviewer agent.
**release-manager** handles cross-cutting release coordination
(versioning, rollout plan, rollback plan, customer comms). The
PandaOS `release-auditor` is closer to a verifier than a manager.

### Incident response team (Stage 4)

Roles:

- `lead`
- **incident-responder** — `(NEW — needed)`
- `troubleshooter` — supports the responder
- **log-analyst** — `(NEW — needed)`
- **devops-infra** — `(NEW — needed)`
- `doc-writer` — post-mortem

Notes: **incident-responder** is the on-call role — read the alert,
check the runbook (the incident catalog), propose mitigation,
escalate. Distinct from `troubleshooter` (which is development-
focused diagnosis, not on-call). **log-analyst** reads structured
logs at scale, finds patterns, surfaces correlations. Most incidents
start as "something looks wrong in the logs" — this is the agent
that reads them. **devops-infra** is the deploy/infra specialist
(terraform, kubernetes, deploy pipelines, monitoring), distinct
from a backend specialist who writes application code.

### Performance investigation team (Stage 2 or 4)

Roles:

- `lead`
- **performance-engineer** — `(NEW — needed)`
- backend or frontend specialist (matching where the perf issue lives)
- `test-runner` (for benchmarking)

Notes: **performance-engineer** knows profiling, benchmarking, load
testing, cost modeling. v0.1's release-audit playbook (Domain 5)
covers the methodology; the agent that uses it routinely is v0.2
work.

### Accessibility-focused work (Stage 2)

Roles:

- `lead`
- **accessibility-reviewer** — `(NEW — needed)`
- frontend or mobile specialist
- **ux-reviewer** — `(NEW — needed)`

Notes: **accessibility-reviewer** applies WCAG and platform-specific
accessibility standards. **ux-reviewer** is broader — interaction
patterns, information architecture, user journey flow. Both v0.2.

---

## Choosing a team shape

A decision flow you can apply when an ask lands:

1. **What stage of the project lifecycle are we in?** (1–5)
2. **What's the unit of work?** (feature, bug, release, incident,
   migration, perf-investigation, dep-sweep)
3. **What domains does the work touch?** (backend, frontend,
   mobile, db, infra, security, etc.)
4. **What's the risk profile?** (data-model change, security-
   sensitive, deploy-touching, production-data-touching)
5. **Pick the smallest team that covers the work.**

### Anti-patterns

- **Spawning too many agents for a small task.** A typo fix doesn't
  need the security-reviewer.
- **Spawning too few agents for a high-risk task.** Migrations,
  deploys, and security-sensitive features need the larger teams.
- **Reusing a team across logical units of work.** The architecture
  doc and PHILOSOPHY both make this point: clean up between units,
  archive the scratchpad, start fresh.
- **Defaulting every role to Opus.** Per the model-tier matrix in
  Phase 1.5 §22 decision 4, only some roles need Opus. Cost-tier
  every role; don't tier-up "just in case."
- **Promoting a v0.1 agent to handle a v0.2 role.** Asking the
  generic `troubleshooter` to be an `incident-responder` works
  badly — the role boundary matters; pretending it doesn't
  produces shallow on-call work.

---

## Future agents (v0.2+)

The agents marked `(NEW — needed)` above are the v0.2 roadmap. Order
of priority based on how often each is needed:

1. **architect** — every greenfield project needs one
2. **incident-responder** — every project in production needs one
3. **release-manager** — projects past their first release benefit
4. **devops-infra** — projects with non-trivial deploy infrastructure
5. **log-analyst** — projects with significant production logs
6. **performance-engineer** — projects under perf scrutiny
7. **privacy-reviewer** — projects with regulatory exposure
8. **accessibility-reviewer / ux-reviewer** — user-facing projects

Adding an agent to v0.2 is mechanical:

- Write the agent prompt following [STYLE.md](../STYLE.md) and
  [docs/engineering-standards.md](engineering-standards.md) — answer
  the five specialist questions.
- Add the file to `lib/agents/`.
- Reference the agent from this team-shapes doc.
- If the agent has a domain playbook, populate that under
  `lib/playbooks/`.
- Note the addition in `CHANGELOG.md`; tag a release.

If a project needs an agent that v0.1 (or v0.2) doesn't ship, write
it project-locally in `<project>/.claude/agents/`. YakOS shadows
project agents over framework agents (per Phase 1.5 §17), so a
project's `architect.md` takes precedence over an eventually-shipped
framework `architect.md`.

---

## Not in v0.1

- **Multi-team coordination.** A "team-of-teams" pattern (a release
  manager coordinating across feature teams) isn't a primitive in
  v0.1. The lead is the top of the hierarchy. v0.2+ if real demand
  surfaces.
- **External-lead / cross-machine teams.** Phase 1.5 §22 decision 6
  resolved this as "lost; documented as known tradeoff." YakOS
  teams are single-machine in v0.1.
- **Auto-team-spawning per-task type.** v0.1 doesn't auto-pick a
  shape — the lead does. v0.2 might add `yakos team suggest <ask>`
  that proposes a shape from the catalog above.
