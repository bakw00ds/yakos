---
name: dispatch-as-project-agent
description: Spawn a generic Agent wearing a project-specific agent's discipline (workaround for v0.1 runtime not discovering project agents)
allowed-tools: Read Agent SendMessage
argument-hint: "<project-agent-name> <task-description>"
mode: [orchestrate]
---

# Dispatch As Project Agent

## Purpose

In Claude Code as of v0.1.4 of YakOS, project-level agents at
`<project>/.claude/agents/<role>.md` are NOT discoverable as
`subagent_type` values by the Agent tool. The Agent tool only resolves
the runtime built-ins (`general-purpose`, `Explore`, `Plan`,
`claude-code-guide`, `statusline-setup`). This is a Claude Code
limitation, not a YakOS one — confirmed by the Phase 8 probe and
re-confirmed within a `TeamCreate` context (the team config accepts
arbitrary `agentType` strings, but `Agent({subagent_type: "..."})`
still fails for project-defined types).

This skill is the workable dispatch pattern in the meantime: spawn a
`general-purpose` agent and inject the project agent's body into the
prompt. The spawned agent operates with the discipline declared in
the file even though the runtime doesn't natively bind it.

When Claude Code adds project agent discovery, this skill becomes
unnecessary — the on-disk layout is already ready.

## Scope

Dispatch with project-agent discipline applied. Three orthogonal
choices the lead makes per call:

- **Spawn type**: `general-purpose` for write work; `Explore` for
  read-only diagnosis (cheaper, no Edit/Write/Bash).
- **Discipline source**: project agent body alone, or project agent
  body + `extends:` framework template (preferred when `extends:` is
  declared).
- **Task scope**: a single domain (backend / frontend / mobile / db /
  etc.). Cross-domain work stays with the lead, who dispatches each
  domain separately and synthesizes.

## When to use

- The lead needs to dispatch a multi-file write task to a domain
  specialist (backend / frontend / mobile / database / etc.) AND
  wants the specialist's discipline (boundaries, anti-patterns,
  deferred-phase awareness) applied to the work.
- The lead is operating in a session that has the project's
  `.claude/agents/` available as files (either rooted in the project
  or via `--add-dir`).

## When NOT to use

- For read-only research tasks — `Explore` is cheaper and good
  enough; the inject-pattern's overhead isn't justified.
- For work that fits inside the lead's own context — the lead has
  read all the agent bodies during familiarization; wearing the hat
  directly is fine for one-off work.
- For tasks requiring `TeamCreate`-tied hooks
  (`team-lifecycle.sh`, `task-complete-dispatch.sh`). Those hooks
  fire on TeamCreate / TaskCompleted events; spawning via
  `general-purpose` skips the team primitives.

## Automated pass

1. **Read the project agent body.** Path is
   `<project>/.claude/agents/<role>.md`.
2. **Resolve `extends:` if present.** If frontmatter declares
   `extends: <framework-template>`, also read the framework template
   at `<yakos>/lib/agents/<framework-template>.md`. The combined
   content is the discipline the spawned agent should apply.
3. **Compose the prompt.** Concatenate, in order:
   - A one-line preamble: "You are operating as the project's `<role>`
     specialist. Apply the discipline in the agent body below."
   - The framework template body (if `extends:` was set).
   - The project agent body.
   - The actual task description (file:line, target outcome,
     boundaries).
4. **Spawn via `Agent`.** Use `subagent_type: "general-purpose"` so
   the spawned agent has full tool access (Edit, Write, Bash, etc.)
   needed for write work. For read-only diagnosis, `Explore` is the
   cheaper alternative.

## Manual pass

The lead does what the runtime would otherwise do for a native dispatch:

1. **Verify the diff.** The spawned agent's summary describes intent;
   verify the actual diff before reporting work as done. The
   path-allowlist hook doesn't fire on `general-purpose` spawns —
   the lead is the path safety net.
2. **Run per-domain validators manually.** `task-complete-dispatch.sh`
   validators (`backend-validate.sh`, `frontend-validate.sh`,
   `db-migration-validate.sh`, etc.) don't fire on injected dispatch.
   Run them yourself against the touched files before approving.
3. **Surface peer messages to `decisions.md`.** Without team-routed
   mailboxes, any decision the spawned agent reaches must be mirrored
   into the project's running decisions doc by the lead.

## What the spawned agent loses (vs native dispatch)

- **Hook coverage.** `team-lifecycle.sh`, `task-complete-dispatch.sh`,
  per-domain validators don't fire because the spawn isn't a TeamCreate
  member. The lead must run validators manually after the spawn returns.
- **TaskList integration.** The spawned agent doesn't share the
  `~/.claude/tasks/<team>/` task list. The lead tracks tasks
  externally (TodoWrite is the in-session substitute).
- **Mailbox / SendMessage routing.** Spawned agents can SendMessage
  to the lead, but peer-to-peer mailbox routing only works inside a
  team. Cross-spawn coordination goes through the lead.
- **`agent_type` discriminator in stdin.** Phase 1.7's
  lead-vs-teammate discriminator (`agent_type` absent for lead-driven)
  doesn't apply — every spawned `general-purpose` agent looks the
  same to hooks.

## Example dispatch (PandaOS)

The lead has a task: implement `POST /api/v1/clients/:id/meal-plans`.
The owning specialist per CLAUDE.md is the backend (Go/Echo).

1. Read `/Users/tw/github/panda-os3.0/.claude/agents/backend.md`.
2. `extends: backend` → also read
   `/Users/tw/github/yakos/lib/agents/backend.md`.
3. Compose prompt: preamble + framework backend body + project
   backend body + task spec (the OpenAPI annotation, the audit-log
   requirement, the contract handoff to frontend).
4. `Agent({subagent_type: "general-purpose", description: "Backend:
   implement /meal-plans POST", prompt: <composed>})`.
5. On return, the lead runs `go build ./... && go vet ./... && go test
   ./...` and `grep -c writeAuditLog api/internal/service/meal_plan_service.go`
   before approving.

## Known gotchas

- **Project agent not found.** The path was wrong (project not
  `--add-dir`'d, or filename mismatch). Surface the error; don't
  dispatch with partial discipline.
- **`extends:` parent not found.** The framework template was renamed
  or removed. Read what's available and proceed with a comment that
  inheritance broke; update the project agent's `extends:` field
  separately.
- **Composed prompt > context budget.** A particularly thick agent
  body + task description can exceed budget. Trim the task
  description; keep the agent body intact (the discipline is the
  point).

## Related

- [`docs/team-shapes.md`](../../docs/team-shapes.md) — when to use
  which team composition; references this skill for the runtime
  dispatch path in v0.1.
- [`lib/agents/lead-template.md`](../../lib/agents/lead-template.md) —
  the lead's role; this skill is one of the lead's tools.
- Phase 8 finding (in panda-os3.0 `memory/session_checkpoint.md`) —
  the discovery that motivated this skill.
