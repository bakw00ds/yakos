---
name: monitor-discipline
description: Every long-running service ships with a supervisor config, /healthz endpoint, and documented restart runbook. Loads when profile.standards.monitors == true.
paths:
  - "**/.yakos.yml"
  - "**/deploy/systemd/**"
  - "**/deploy/k8s/**"
  - "**/ecosystem.config.*"
  - "**/docker-compose.yml"
  - "**/Dockerfile"
  - "**/healthz*"
  - "**/healthcheck*"
references:
  - rule:profile-standards
---

# Monitor discipline

Loads when Claude reads `.yakos.yml` (to check
`profile.standards.monitors`) or any supervisor / healthcheck
file.

## What this rule is for

If `profile.standards.monitors == true`, every long-running
service the project ships includes:

1. **A process supervisor configuration** — systemd unit, pm2
   ecosystem entry, k8s Deployment, or docker-compose service
   with restart policy.
2. **A `/healthz` (or equivalent) HTTP endpoint** that returns
   200 when the service is live + ready. Not a no-op; checks
   actual liveness (DB ping, downstream connectivity, queue
   accessible, etc.).
3. **A documented restart runbook** in `docs/runbooks/` or
   `<repo>/RUNBOOK.md` describing how to restart, drain, and
   roll back.

yakOS doesn't pick the supervisor — that's the project's infra
choice. yakOS makes sure the artifacts EXIST.

## What's required (per service in `profile.monitors.targets`)

- One of: systemd unit | pm2 entry | k8s Deployment yaml |
  docker-compose service entry
- `/healthz` (or `/health`, `/status`, `/ready`) endpoint
  returning 200 on healthy
- Restart documented in a runbook (or in the supervisor config
  comments)

## What's forbidden

- **`/healthz` that always returns 200.** A no-op healthcheck
  hides real liveness issues. Health must check something.
- **Services in `profile.monitors.targets` without supervisor
  config.** Audit-time P1.
- **Supervisor configs that lack `restart` policy.** Default
  policy for a long-running service should be `always` or
  `on-failure` with a sane backoff. Manual-only restarts are
  fine for short-lived jobs but not services.

## When supervisor configs update

- **New service shipped.** Add supervisor entry + healthcheck +
  runbook section IN THE SAME PR.
- **Service port / binary path / env changes.** Update supervisor
  config accordingly. PR description references the change.

## Composition with related yakOS primitives

- `lib/agents/sre.md` (already shipped) — owns the
  operational layer; this rule references their discipline.
- `lib/agents/devops-engineer.md` (already shipped) — owns CI/CD
  + deploy infra. Monitors are part of that surface.
- `lib/playbooks/08-infra-deploy-deps.md` — domain 8 of
  release-audit; new §Monitor presence section.

## Anti-pattern

Most common failure mode: developer adds `/healthz` returning
`{ "status": "ok" }` and considers the standard met. Two months
later, the database dies; healthcheck still returns 200; load
balancer never drains; service silently fails for users.

Audit-time check looks for telltale signs: healthcheck handler
that does no real check (single literal return). Severity: **P2**
if the handler is one line returning OK; **P3** if it does some
check but skips obvious dependencies (DB / queue / downstream).

## References

- `skill:monitor-scaffold` — one-shot scaffold per supervisor
- `lib/agents/sre.md`, `lib/agents/devops-engineer.md`
- `cross-project-standards-plan.md` §5 — full design
