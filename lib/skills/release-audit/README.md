# release-audit

Generic, stack-aware pre-release audit skill. The orchestrator
(`SKILL.md`) is invocable from any project; it adapts to the
detected stack and dispatches the relevant subset of 8 domain
auditors.

Originated as scaffolding-only (orchestrator was per-project) and
generalized in May 2026 to apply across all projects, with stack
detection at Phase 0 driving which playbooks and tools load.

## What's here

```
release-audit/
├── README.md                     ← this file
├── SKILL.md                      ← stack-agnostic orchestrator (Phase 0 → 6)
├── references/
│   ├── tooling-matrix.md         ← per-stack-profile tool requirements
│   └── portable-prompt.md        ← self-contained version for non-Claude-Code runtimes
├── scripts/
│   └── check-tools.sh            ← Phase 1 readiness checker, takes <profile-id>
├── templates/
│   ├── scope.md                  ← Phase 0 scoping doc
│   ├── domain-report.md          ← per-domain finding report
│   ├── executive-summary.md      ← cross-domain summary + lead-review prompt
│   └── dispositions.md           ← finding → fix-now / defer-next / accept-risk / invalid
└── agents/
    ├── lead-auditor.md             ← orchestrator role; spawns + merges
    ├── security-auditor.md             → lib/playbooks/01-security.md
    ├── code-quality-auditor.md         → lib/playbooks/02-code-quality.md
    ├── uiux-auditor.md                 → lib/playbooks/03-ui-ux-a11y.md
    ├── docs-auditor.md                 → lib/playbooks/04-docs-architecture.md
    ├── performance-auditor.md          → lib/playbooks/05-performance.md
    ├── regulated-data-auditor.md       → lib/playbooks/06-regulated-data.md
    ├── mobile-auditor.md               → lib/playbooks/07-mobile.md
    └── infra-auditor.md                → lib/playbooks/08-infra-deploy-deps.md
```

The 8 domain playbooks live at `lib/playbooks/01..08.md` and are
consumed by the auditor agents directly (the `playbook:` field on
each agent file points at the absolute framework path).

## How stack detection works

Phase 0 inspects the repo against the heuristics in
`references/tooling-matrix.md` § Stack profiles. Each detected
profile (e.g. `go-backend`, `web-frontend-react`, `flutter-mobile`)
adds:

1. The corresponding tool list to the Phase 1 readiness check.
2. Stack-specific commands referenced from the domain playbooks
   (Domain 1 SAST, Domain 2 coverage tools, Domain 5 profilers).
3. Domain 7 (Mobile) is dispatched only if a mobile profile is
   detected; otherwise it's skipped silently.

A project may override the auto-detection at Phase 0 — useful for
unusual monorepo layouts or when scoping the audit to a subset.

## How a project customizes

Most projects need no customization — the generic skill works.

When a project needs project-specific framing, the supported
extension points are:

1. **Project-level rules** at `<project>/.claude/rules/` — tighten
   any playbook's bar (e.g., a Go project mandating `gocyclo < 10`
   instead of the playbook's `< 15`).
2. **Project-level skill override** at
   `<project>/.claude/skills/release-audit/SKILL.md` — wins over the
   framework version per Phase 1.5 §17 override semantics. Use this
   when the workflow itself differs (different environments map to
   staging, different role roster, additional domain).
3. **Project-level playbook override** at
   `<project>/.claude/playbooks/NN-domain.md` — replaces the
   framework playbook for that domain. Use sparingly.

`docs/audits/<YYYY-MM-DD>-<version>/` output structure is the same
across projects (it comes from the templates).

## Standards

`SKILL.md` follows the `lib/skills/` convention (frontmatter with
`name`, `description`, `allowed-tools`, `argument-hint`, `mode`;
sections Purpose / Scope / Automated pass / Manual pass / Findings
synthesis / Known gotchas; 80–180 lines).

The 9 agent definitions have frontmatter (`id`, `role`, `domain`,
`mode`, `invocable_by`, `playbook`) per the `lib/agents/` convention.
They are drop-in compatible with project-level lead orchestrators.

## See also

- `lib/playbooks/01..08.md` — the 8 domain playbooks these auditors execute
- `lib/skills/README.md` — generic-skills index
- `lib/agents/README.md` — generic-agents index (the auditor agents
  are audit-specialized cousins of the generic specialists)
