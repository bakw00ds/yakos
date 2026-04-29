# release-audit (scaffolding)

Templates and auditor-agent definitions for project-level release-audit
skills. **The orchestrator (`SKILL.md`) lives per-project**, in
`<project>/.claude/skills/<project>-release-audit/SKILL.md`. This
directory ships the reusable scaffolding only.

## What's here

```
release-audit/
├── README.md                   ← this file
├── templates/
│   ├── scope.md                ← Phase 0 scoping doc
│   ├── domain-report.md        ← per-domain finding report
│   ├── executive-summary.md    ← cross-domain summary + lead-review prompt
│   └── dispositions.md         ← finding → fix-now / defer-next / accept-risk / invalid
└── agents/
    ├── lead-auditor.md         ← orchestrator role; spawns + merges
    ├── security-auditor.md         → lib/playbooks/01-security.md
    ├── code-quality-auditor.md     → lib/playbooks/02-code-quality.md
    ├── uiux-auditor.md             → lib/playbooks/03-ui-ux-a11y.md
    ├── docs-auditor.md             → lib/playbooks/04-docs-architecture.md
    ├── performance-auditor.md      → lib/playbooks/05-performance.md
    └── regulated-data-auditor.md   → lib/playbooks/06-regulated-data.md
```

The 6 domain playbooks live at `lib/playbooks/01..06.md` and are
consumed by the auditor agents directly (frontmatter `playbook:` field
on each agent file points at the absolute framework path).

## Why scaffolding-only

A release-audit is heavyweight enough that the orchestration is almost
always project-shaped: which environments map to staging, which roles
the audit must enumerate, which third-party processors need a
data-flow line item, which prior audits to diff against. Pinning the
orchestrator at the framework level forces every consumer to fight a
generic skeleton; pinning it per-project keeps the framework's job
small (reusable building blocks) and the project's job concrete
(workflow tuned to its actual surfaces).

## How a project consumes this

The project's `<project>/.claude/skills/<project>-release-audit/SKILL.md`
references this directory directly. A minimal pattern:

1. Project SKILL.md drives the workflow (Phase 0 → 6).
2. Project SKILL.md points at `lib/skills/release-audit/templates/` for
   the report shapes.
3. Project SKILL.md spawns auditor agents from
   `lib/skills/release-audit/agents/<name>.md` (or copies them if
   project-specific tweaks are needed — frontmatter override semantics
   per Phase 1.5 §17).
4. Project SKILL.md adds project-specific framing: env URLs, role
   roster, third-party inventory, domain ordering exceptions.

`docs/audits/<YYYY-MM-DD>-<version>/` output structure is the same
across projects (it comes from the templates).

## Standards

This scaffolding does NOT have its own `SKILL.md` — it is consumed by
project-level `SKILL.md` files. `yakos validate` should treat
`release-audit/` as an exception to the "every skill directory has
SKILL.md" rule (or be updated to recognize scaffolding-only directories
via this README's presence).

The 7 agent definitions DO have frontmatter (`id`, `role`, `domain`,
`mode`, `invocable_by`, `playbook`) per the `lib/agents/` convention.
They are drop-in compatible with project-level lead orchestrators.

## See also

- `lib/playbooks/01..06.md` — the 6 domain playbooks these auditors execute
- `lib/skills/README.md` — generic-skills index
- `lib/agents/README.md` — generic-agents index (the auditor agents are
  audit-specialized cousins of the generic specialists)
