---
name: deploy-check
description: Pre-deploy verification — build, smoke test, env-var sanity. Use immediately before deploying or promoting to prod, to catch a broken build or missing config before it ships.
allowed-tools: Read Bash Grep
argument-hint: "[--target <env>]"
mode: [review]
---

# Deploy Check

## Purpose

Run the pre-deploy gate: build the production artifact, smoke-test
critical paths, verify environment-variable sanity (presence, not
values), and produce a structured go/no-go report. Distinct from
`test-suite` (correctness) and `pre-commit` (cheap fast checks) —
deploy-check is deploy-shaped: it cares about what would actually
ship to a target environment.

## Scope

Operates on the current branch's HEAD; reads the project's deploy
configuration (`deploy/`, `Dockerfile`, `cloudbuild.yaml`, project-
specific scripts). With `--target <env>`, the smoke tests can be
environment-specific (e.g. staging vs prod has different smoke targets).

NOT in scope: actually deploying. The skill is verification only.
A separate operator action triggers the deploy.

## Automated pass

1. **Build.** Run the project's production build (`make build`,
   `npm run build`, `flutter build`, `docker build`, etc.). Warnings
   logged; failures abort.
2. **Artifact sanity.** Verify the artifact has expected size order-of-
   magnitude (bin sizes don't suddenly 10x without warning), expected
   files (no missing required output), expected provenance metadata.
3. **Env-var presence.** Read the deploy config to enumerate required
   env vars; verify each is set to a non-empty value in the target
   env. Do NOT print values — print presence/absence (per
   `rule:secret-handling`).
4. **Smoke tests.** Run the project-defined deploy-smoke target
   (`make smoke`, `npm run test:smoke`). These exercise critical paths
   end-to-end; they're slower than unit tests but should still complete
   in single-digit minutes.
5. **Migration check.** If the change includes DB migrations, verify
   they're idempotent and have a rollback. (The migration validator
   from `lib/hooks/per-domain/db-migration-validate.sh` runs here.)

## Manual pass

The operator reviews:

- The size-deltas vs last release. A 30% size jump in a "small fix"
  is suspicious.
- The list of env vars compared against the target environment's
  declared spec.
- Any warnings from the build step. Warnings aren't errors but they
  accumulate.

## Findings synthesis

```
deploy-check results: <go|no-go>
  build:       <pass|fail> (<duration>; warnings: <n>)
  artifact:    <pass|fail> (size <delta vs last>; required files <ok|missing>)
  env-vars:    <pass|fail> (<n>/<total> set; <list-of-missing>)
  smoke:       <pass|fail> (<n>/<total>)
  migrations:  <pass|fail|n/a>
```

A `no-go` blocks the deploy. A `go` is necessary but not sufficient —
the operator still runs the deploy command manually.

## Known gotchas

- `--target` defaults to the safest target. Don't auto-default to
  production — the operator names the target explicitly.
- Smoke tests that hit the actual target environment can have side
  effects (creating test users, leaving traces). Project policy
  decides whether smoke-against-prod is allowed. Default is no.
- Env-var "presence" doesn't validate "correctness" — a key that's
  set but wrong-valued passes presence. That's a correctness check
  in CI, not pre-deploy.
- A passing deploy-check doesn't mean the deploy itself is safe.
  Network partitions, target-env drift, and active-traffic effects
  aren't covered. Deploy-check is necessary, never sufficient.
