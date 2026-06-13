---
name: promote-branch
description: Promote code between environments (dev → test → prod) via PR with a structured changelog, gated by the promotion-aware pre-push hook. Use when moving a tested change up the environment ladder toward production.
allowed-tools: Read Bash Grep
argument-hint: "<from-env> <to-env> [--draft]"
mode: [release]
---

# Promote branch

## Purpose

Move code through the configured environment ladder
(dev → test → prod) without bypassing the pre-push promotion
gate. Wraps `yakos env promote` with the discipline of a guided
workflow: pre-checks, PR creation, post-merge confirmation.

## Scope

Operates against the project's `.yakos.yml` `environments:`
config and the configured git branches. Does NOT model deploy
infra (k8s, Vercel, etc.) — promotion stops at PR merge; deploy
is downstream.

## Automated pass

```sh
# Pre-checks (skill internals):
yakos env validate                   # config sanity
yakos env list                       # confirm from/to envs exist
git status --porcelain               # clean working tree required
git -C $project fetch --all          # local matches remote
```

If pre-checks pass, invokes `yakos env promote <from> <to>`,
which:

1. Confirms the from-branch is at HEAD-of-origin
2. Detects the PR tool (`gh`/`glab`/plain git)
3. Creates the PR with structured title + body
4. Returns the PR URL for operator review

## Manual pass

After the PR is open:

- Operator reviews the PR per the project's review discipline
  (`rule:pr-conventions`)
- For `requires_review: true` environments (typically `prod`),
  at least one human approval required
- Operator merges per the project's merge convention (squash,
  merge-commit, rebase)

The skill does NOT auto-merge. yakOS's audit-trail-first posture
applies: every promotion to prod requires a human signature on
the merge.

## Findings synthesis

After merge, the operator runs:

```sh
yakos env status     # confirms current branch matches expected target
```

If the post-merge state is inconsistent (e.g., main is at the
old HEAD), the skill flags this — likely a CI failure or a
merge-from-elsewhere happened concurrently.

## Known gotchas

- **Bypass via override**: `YAKOS_PROMOTION_OVERRIDE=1 git push`
  bypasses the pre-push gate but logs to gate-log.ndjson. Use
  only for documented incidents; the audit trail catches abuse.
- **Multi-dev mode**: both operators share the same promotion
  rules. If Alice promotes dev→test, Bob's next dev push has
  to merge through develop's new HEAD. No special handling
  needed — git's normal merge mechanics suffice.
- **Tag-based promotion**: this skill assumes branch-based
  promotion. Tag-based release flows (e.g., `v1.2.0` tag triggers
  prod deploy) are out of scope for v1; add a `promote-tag`
  skill later if needed.
- **Concurrent promotions**: if alice and bob both run
  `yakos env promote dev test` at the same time, the second
  PR will conflict. yakOS doesn't lock here; git's normal PR
  conflict mechanics apply. Operator resolves.
- **Empty promotion**: if from-branch == to-branch HEAD,
  `yakos env promote` warns and exits without creating an
  empty PR.
