---
name: monitor-scaffold
description: Drop in supervisor config + /healthz endpoint + restart runbook per service, per supervisor (systemd / pm2 / k8s / docker-compose). Use when services need health checks and process supervision set up.
allowed-tools: Read Edit Write Bash Grep
argument-hint: "[--supervisor systemd|pm2|k8s|docker-compose] [--service <name>]"
mode: [scaffold]
---

# Monitor scaffold

## Purpose

Drop in a working supervisor configuration for each service in
the project's `profile.monitors.targets` list. Operator picks
the supervisor type once; the scaffold emits the right config
per service.

This skill is the first half of the soft+hard pairing for
Standard 3 (monitors). The rule is the second half.

## Scope

Operates on the operator's project repo. Adds:

- A supervisor config file per service in
  `profile.monitors.targets`
- A `/healthz` endpoint stub in each target service's source
  (if not already present)
- `docs/runbooks/<service>-runbook.md` for each target service
  (skeleton — operator fills in)

NOT in scope:
- Cloud-specific supervisor variants (ECS, Cloud Run, Fly.io) —
  these vary too much; scaffold writes for the most common 4
  (systemd, pm2, k8s, docker-compose)
- Production observability dashboards (Grafana / Datadog UIs)
- Auto-scaling configuration
- Alert routing

## Automated pass

1. Read `.yakos.yml` `profile.monitors.targets` and
   `profile.monitors.supervisor` (default: ask interactively
   or take `--supervisor` flag).

2. For each service in targets:

   ### systemd variant
   - Writes `deploy/systemd/<service>.service`:
     ```ini
     [Unit]
     Description=<service> (yakOS-managed)
     After=network.target

     [Service]
     Type=simple
     User=<service>
     WorkingDirectory=/opt/<service>
     ExecStart=/opt/<service>/bin/<service>
     Restart=on-failure
     RestartSec=5
     StandardOutput=journal
     StandardError=journal

     [Install]
     WantedBy=multi-user.target
     ```

   ### pm2 variant
   - Writes/extends `ecosystem.config.js`:
     ```js
     module.exports = {
       apps: [{
         name: '<service>',
         script: './dist/<service>.js',
         instances: 1,
         autorestart: true,
         watch: false,
         max_memory_restart: '512M',
         env: { NODE_ENV: 'production' },
       }]
     }
     ```

   ### k8s variant
   - Writes `deploy/k8s/<service>-deployment.yaml`:
     ```yaml
     apiVersion: apps/v1
     kind: Deployment
     metadata:
       name: <service>
     spec:
       replicas: 1
       selector:
         matchLabels: { app: <service> }
       template:
         metadata:
           labels: { app: <service> }
         spec:
           containers:
           - name: <service>
             image: <service>:latest
             livenessProbe:
               httpGet: { path: /healthz, port: 8080 }
               initialDelaySeconds: 30
               periodSeconds: 10
             readinessProbe:
               httpGet: { path: /healthz, port: 8080 }
               initialDelaySeconds: 5
               periodSeconds: 5
     ```
   - Plus accompanying `<service>-service.yaml`.

   ### docker-compose variant
   - Adds/updates `docker-compose.yml` service entry:
     ```yaml
     services:
       <service>:
         build: ./<service-dir>
         restart: unless-stopped
         healthcheck:
           test: ["CMD", "curl", "-f", "http://localhost:8080/healthz"]
           interval: 30s
           timeout: 5s
           retries: 3
     ```

3. Drop in `/healthz` stub if not already present per backend
   language (Go: `internal/handler/health.go`; Node/Express:
   middleware addition; Python/FastAPI: route addition; etc.).
   The stub MUST check at minimum the DB connection (if the
   service uses one); operator extends to check other downstream
   deps.

4. Drop in `docs/runbooks/<service>-runbook.md` skeleton with
   sections:
   - Quick reference (start/stop/restart commands)
   - Common failures + remediation
   - Drain procedure (graceful shutdown)
   - Rollback procedure (last-known-good)
   - On-call contact

## Manual pass

After scaffold:

1. Operator reviews supervisor configs against ops conventions
2. Operator extends `/healthz` to check ALL relevant deps (DB +
   queue + cache + downstream APIs as applicable)
3. Operator fills in runbook content
4. Operator wires supervisor into deploy pipeline (install
   systemd unit, deploy k8s manifest, etc.)

## Findings synthesis

Output is:
- `deploy/<supervisor>/<service>.<ext>` per target service
- `/healthz` endpoint code per service (if absent before)
- `docs/runbooks/<service>-runbook.md` skeleton per service
- README addendum:
  ```markdown
  ## Operations

  Services run under <supervisor>. See `deploy/<supervisor>/`
  for configs and `docs/runbooks/` for restart procedures.
  Healthchecks at `/healthz` on each service port.
  ```

## Known gotchas

- **Healthcheck depth.** A liveness probe that just confirms
  the process is alive (TCP connect to port 8080) is barely
  better than nothing. A REAL liveness check pings the DB,
  inspects pool status, verifies queue accessibility.
- **Initial delay too short.** k8s `initialDelaySeconds: 30`
  is a safe default; slow-starting services (long migrations
  on boot) need more. Operator tunes per service.
- **Restart-loop hiding root cause.** If `restart: always`
  hides a service crashing every 10s, the audit-trail shows
  a dead service that appears healthy in supervisor status.
  Couple with monitoring (out of scope here) that alerts on
  restart frequency.
- **Existing supervisor configs.** Scaffold detects and offers
  to skip rather than overwrite. Operator removes existing or
  uses `--force` per service.
- **No supervisor selection in .yakos.yml.** Scaffold prompts
  interactively or accepts `--supervisor` flag. Documented in
  `cross-project-standards-plan.md` §5.
