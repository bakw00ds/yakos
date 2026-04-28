# Migration notes for tiny-go-api

This is **a fresh project, not a tmux→YakOS migration.** It was
written from scratch as the YakOS canonical example. There is no
"before" — the directory tree above is the entire history.

## Why this matters

If you're reading [MIGRATING.md](../../MIGRATING.md) for guidance
on porting an existing tmux + dispatch-CLI setup to YakOS, the
tiny-go-api example **isn't an example of the migration process**.
It's an example of YakOS in steady state — what a project looks like
once it's already been written to use the framework.

A worked migration story is Phase 8's PandaOS migration (separate
session post-v0.1). Until that lands, MIGRATING.md describes the
mechanics but doesn't have a worked end-to-end before/after.

## What this example IS

- A demonstration of YakOS's project layout (the
  `<project>/.claude/` + `<project>/scripts/hooks/` shape).
- A demonstration that agents extend framework versions (per
  Phase 1.5 §17), with project-specific bodies that override.
- A demonstration that the framework's hooks (path-allowlist,
  secret-scan, mailbox-mirror, etc.) work against a real Go
  project's edit/test surface.
- A test bed for the framework — `yakos validate
  examples/tiny-go-api/` runs in CI to catch regressions in
  validation logic against a known-good project shape.

## What this example is NOT

- A starting template. Don't `cp -r examples/tiny-go-api/ my-project/`
  and customize from there. The example's smallness is a feature;
  real projects need more (cmd/, internal/, multiple endpoints,
  database layer). Use `yakos init` instead.
- An exhaustive demonstration. Many YakOS features (multi-specialist
  coordination, contract handoffs, adversarial review, the
  release-audit skill) aren't exercised here because they need a
  project bigger than this.
- An accumulated incident catalog. The example has no production
  history; it's never failed in a way that produced a postmortem.
  When you need incident references in a real project, write them
  in the project's INCIDENT-CATALOG.md.

## See also

- [README.md](README.md) — what the example shows + simulated workflow
- [CLAUDE.md](CLAUDE.md) — session source of truth
- [../../MIGRATING.md](../../MIGRATING.md) — actual migration guide
- [../../docs/team-shapes.md](../../docs/team-shapes.md) — team
  composition catalog (this example uses the "small Go service"
  shape but at half-size)
