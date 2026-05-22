---
name: profile-standards
description: Reads .yakos.yml `profile:` section and tells the lead which cross-project standards the project has committed to. Path-scoped to .yakos.yml.
paths:
  - "**/.yakos.yml"
references:
  - rule:lead-dispatch-discipline
---

# Profile-driven standards

Loaded when Claude reads `.yakos.yml`. In practice this means at
session start (the lead reads the config) and any time the
operator opens or edits `.yakos.yml`.

## What this rule is for

`.yakos.yml` declares the project's TYPE (`web-app` / `service`
/ `library` / `cli-tool` / `data-pipeline`) and the set of
cross-project STANDARDS the project has committed to. This rule
tells the lead:

1. Read the profile at session start (or when .yakos.yml is
   accessed).
2. Internally track which standards are `true` in
   `profile.standards.*`.
3. For every dispatched task, consider whether the standard's
   discipline applies; if so, surface to the relevant specialist.
4. Never silently bypass a committed-to standard. If a standard
   needs to be skipped for a specific change, document the
   exception in `decisions.md` with a justification.

## Profile schema (what to expect in .yakos.yml)

```yaml
yakos: 0.9

profile:
  type: web-app             # or: service | library | cli-tool | data-pipeline
  standards:
    logging: true
    changelog-ui: true
    monitors: true
    feedback: false         # not all projects need user feedback
    architecture-viz: true
    about-page: true
```

Each `standards.*` key maps to a corresponding rule:
- `logging` → `rule:logging-discipline`
- `changelog-ui` → `rule:changelog-ui-discipline`
- `monitors` → `rule:monitor-discipline`
- `feedback` → `rule:feedback-discipline`
- `architecture-viz` → `rule:architecture-viz-discipline`
- `about-page` → `rule:about-page-discipline`

Only the rules for `true` standards apply.

## What the lead does at session start

1. Read `.yakos.yml`; extract `profile.type` and
   `profile.standards.*`.
2. Mentally enumerate the active standards.
3. For each task dispatched during the session, ask: "does this
   change touch the surface of an active standard?" If yes,
   include the relevant specialist (e.g., `sre.md` for
   `monitors`, `architect.md` for `architecture-viz`).
4. Update `<work>/current/decisions.md` with the active-standards
   list once per session, so the audit trail records what was
   in scope.

## When standards are missing

If `.yakos.yml` lacks a `profile:` section, this rule fires no
guidance — the project pre-dates standards adoption. Recommend
running `yakos standards init` (M4 in
`cross-project-standards-plan.md`) at the operator's discretion.

If `.yakos.yml` has `profile:` but all `standards.*` are
`false`, the project has opted out — no guidance fires. Respect
the opt-out; don't second-guess.

## Anti-pattern

The most common failure mode for this rule: a teammate edits a
service handler that should have new logging per
`rule:logging-discipline`, but neither the rule nor this one
fires because the teammate never read `.yakos.yml`. **The lead
must surface the active-standards list to dispatched specialists
at handoff time** — don't assume specialists read .yakos.yml.

## References

- `cross-project-standards-plan.md` — the full plan this rule
  enables
- Per-standard rules: `logging-discipline`,
  `changelog-ui-discipline`, `monitor-discipline`,
  `feedback-discipline`, `architecture-viz-discipline`,
  `about-page-discipline`
- `rule:lead-dispatch-discipline` — the four-line discipline
  this composes with
