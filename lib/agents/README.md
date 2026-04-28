# Generic agents

Reusable specialist roles available to every project that installs YakOS.
Project-specific specialists live in `<project>/.claude/agents/` and shadow
these (per Phase 1.5 §17 override semantics).

## Inventory

| Agent | Role | Model | Purpose |
|---|---|---|---|
| `lead-template` | orchestrator | opus | Base lead pattern; project leads `extends:` this. |
| `planner` | specialist | opus | Decomposes work; pushes back on under-specified tasks. |
| `test-runner` | specialist | sonnet | Runs the test suite; reports flakes; refuses to paper-over failures. |
| `code-reviewer` | reviewer | sonnet | Reviews changes for correctness, idiom, and surprise. |
| `security-reviewer` | reviewer | opus | Audits changes for security and data-handling issues. |
| `troubleshooter` | specialist | sonnet | Read-only diagnosis; never edits; dispatches fixes. |
| `doc-writer` | specialist | sonnet | Writes/updates docs, changelogs, release notes. |

Agents are loaded into a project at install time via the per-file symlink
mechanism (`yakos install`). Project-specific overrides are loaded from
`<project>/.claude/agents/<name>.md` and take precedence; an `extends:`
field in the project version walks up the precedence stack to inherit
this generic version's body.

## Standards

Every file here:

- Uses the schema in [STYLE.md §7](../../STYLE.md) and Phase 1.5 §9.
- Answers the **five specialist questions** documented in
  [docs/engineering-standards.md §9](../../docs/engineering-standards.md).
- Stays within the 80–140 line budget enforced by `yakos validate`.
- Never references playbooks not yet shipped (lib/playbooks/ is empty
  in v0.1; v0.2 populates it).

When adding a new generic agent:

1. Match the schema (frontmatter + sections).
2. Run `yakos validate` to confirm the line budget and required sections.
3. Add an entry to the inventory table above.
4. If the role belongs to a specific project, write it in
   `<project>/.claude/agents/` instead.
