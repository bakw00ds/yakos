# Generic skills

Reusable, domain-agnostic skills available to every project. Project-specific
skills (e.g. `release-audit`) live in `<project>/.claude/skills/`.

## Inventory

| Skill | Mode | Purpose |
|---|---|---|
| `pre-commit` | review | Pre-commit checks: lint, secret scan, test sample. |
| `test-suite` | review | Run the full test suite with reporting and flake handling. |
| `session-recovery` | recover | Reconstruct context after a session interrupt or fresh shell. |
| `project-init` | implement | Bootstrap a new project's `.claude/` config. |
| `gather-feedback` | gather | Pull and triage user feedback across configured sources. |
| `deploy-check` | review | Pre-deploy verification: build, smoke, env-var sanity. |
| `verify-agent-work` | review | Spot-check what a teammate produced before accepting. |
| `split-mega-task` | plan | Break a too-large task into reviewable chunks. |
| `contract-handoff` | implement | Publish API/DB/UI contracts for downstream teammates. |
| `phase-complete` | review | Verify a phase's exit criteria before declaring done. |
| `dependency-update` | maintain | Survey and apply dependency updates safely. |

## Standards

Every `SKILL.md`:

- Has frontmatter with `name`, `description`, `allowed-tools`,
  `argument-hint`, `mode` (per Phase 1.5 §10).
- Has sections: `## Purpose`, `## Scope`, `## Automated pass`,
  `## Manual pass`, `## Findings synthesis` (where applicable),
  `## Known gotchas` — enforced by `yakos validate`.
- Stays within 80–180 lines.

## Phase 0 reminder: skills are session-global

Per Phase 0 Test 2 finding (and Phase 1.5 §10), the `skills:` frontmatter
field on a teammate's subagent definition is **ignored**. Skills loaded
into a session are available to all teammates. Two implications:

1. Pick names that won't be invoked accidentally.
2. Dangerous skills (deploy-check, force operations) belong with the
   lead — put guidance in the lead prompt that *only the lead* runs them.
