---
name: environment-discipline
description: Code flows dev → test → prod via git branches; the lead never pushes directly to test or prod from a dev session. Always loaded when .yakos.yml declares environments.
paths:
  - "**/.yakos.yml"
references:
  - rule:git-hygiene
  - rule:pr-conventions
  - rule:commit-format
---

# Environment discipline

Path-scoped: loaded when Claude reads `.yakos.yml`. (Always loaded
in practice for any project with `environments:` declared, since
the lead reads the config at session start.)

## What this rule is for

Many projects have a dev/test/prod (or dev/staging/main) split.
yakOS enforces the promotion order at the git boundary via the
pre-push gate; this rule tells the lead what the discipline LOOKS
like so the lead doesn't fight the gate.

The configuration lives in `.yakos.yml`:

```yaml
environments:
  dev:
    branch: develop
  test:
    branch: staging
    promotes_from: [dev]
  prod:
    branch: main
    promotes_from: [test]
    requires_review: true
```

## Promotion order

- **dev (`develop`)**: the inbox. Direct pushes allowed. The lead
  may commit and push to `develop` from any feature work as long
  as the change is on a `feat/*` or `fix/*` branch that merges to
  `develop`.
- **test (`staging`)**: receives merges from `develop` only.
  Direct pushes from feature branches → REFUSED by gate.
- **prod (`main`)**: receives merges from `staging` only. Direct
  pushes from any other branch → REFUSED by gate. Requires PR +
  review.

## What the lead does at each transition

**dev → test promotion** (`yakos env promote dev test`):
- Confirms develop is at HEAD with no uncommitted work
- Opens a PR (via detected tool: `gh`/`glab`/plain git)
- PR title: `chore(test): promote develop → staging at <iso>`
- PR body: changelog diff from last staging promotion

**test → prod promotion** (`yakos env promote test prod`):
- Confirms staging is at HEAD
- Confirms tests passed on staging (project-specific CI signal)
- Opens a PR; requires `requires_review: true` per config
- Higher scrutiny — operator MUST review before merge

## What the lead does NOT do

- **Don't `git push` directly to staging or main from a session.**
  The pre-push gate refuses; use `yakos env promote` instead.
- **Don't bypass the gate with `--no-verify`** (or
  `YAKOS_PROMOTION_OVERRIDE=1`) unless explicitly necessary for
  an incident; both are logged.
- **Don't add new environments** without updating `.yakos.yml`.
  yakOS doesn't model arbitrary environment graphs in v1; if
  you need dev → qa → uat → prod, this rule needs extension.

## Multi-dev mode

The pre-push gate applies equally to both operators. Bob can't
push to main from his checkout any more than Alice can. If a
push needs to happen out-of-band (incident response, emergency
patch), use `YAKOS_PROMOTION_OVERRIDE=1` + a documented bypass
entry per the multi-dev coord plan's hook-bypass mechanism.

## Anti-pattern

The single most common failure mode is "I'll just push this one
fix directly to main; it's urgent." Almost always it could
have gone through a feature branch + fast PR + staging promotion
in <5 minutes. The promotion gate exists because incident-mode
direct-to-prod commits accumulate as untestable changes. Resist.

## References

- `rule:git-hygiene` — no force pushes; explicit `git add`
- `rule:pr-conventions` — branch naming + PR description
- `rule:commit-format` — Conventional Commits
- See `cli/lib/environments.sh` for the `yakos env` CLI surface
- See `lib/hooks/git/pre-push-version-gate.sh` for the gate
