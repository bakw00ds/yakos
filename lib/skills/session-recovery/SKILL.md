---
name: session-recovery
description: Reconstruct working context after a session interrupt or fresh shell
allowed-tools: Read Bash Grep
argument-hint: "[--full]"
mode: [recover]
---

# Session Recovery

## Purpose

Reconstruct enough context to continue work after a session interrupt:
a crashed terminal, a fresh `claude` invocation, a long-running task
that lost track of its own state. The skill is a guided walk through
the persistent state YakOS maintains, in priority order.

## Scope

Operates on the current project's `~/agent-control/<project>/` and
`~/.claude/projects/<encoded>/`. NOT in scope: reconstructing what was
in volatile memory only (e.g. an in-flight tool call's arguments).

## Automated pass

Walks state in priority order:

1. **Read `work/current/decisions.md`** if present — this is the
   highest-signal recap of what was decided.
2. **Read `work/current/plan.md`** for the current decomposition.
3. **Read `work/current/status.md`** for the task list mirror.
4. **Read `work/current/contracts.md`** for any inter-team contracts
   that aren't yet code.
5. **Tail `work/current/logs/*.ndjson`** — recent hook outcomes show
   what the team was doing in the last few minutes.
6. **Tail `work/current/messages.ndjson`** — recent peer DMs (per
   Phase 1.7's mailbox-mirror).
7. **Read `~/.claude/projects/<encoded>/MEMORY.md`** index — the
   cross-session memory the lead has accumulated.
8. **Read the `.session-started` timestamp** — how long has this
   session been running? Above 4 hours, suggest `yakos team restart`.

With `--full`, also surveys:

- Recent commits on the project's main branch.
- Open PRs (via `gh pr list`).
- Recent `sessions.ndjson` entries (the persistent ledger).

## Manual pass

The invoking agent (typically the lead, after recovery) reviews the
synthesized state and asks:

- Is the plan still valid given any project changes since?
- Are any of the in-flight tasks orphaned (assigned to a teammate that
  no longer exists)?
- Is `decisions.md` consistent with what the messages.ndjson shows
  was actually decided?

## Findings synthesis

A one-screen recap:

```
Session recovered.
  Project:    <name>
  Last seen:  <session-started> (<age> ago)
  Plan:       <one-line summary, or "no plan.md present">
  Tasks:      <n active, n completed, n blocked>
  Decisions:  <count, last updated <when>>
  Messages:   <n in current session>
  Open PRs:   <n> (with --full)
  Recent hooks: <one-line summary of last few outcomes>
```

After this, the agent has enough to either resume or escalate.

## Known gotchas

- The auto-memory directory uses a custom path encoding (slash and dot
  → hyphen). Use `ct_encode_project_path` from `compat.sh` rather than
  hand-encoding.
- `decisions.md` may be authoritative, may be stale; the timestamp
  matters. If it's >2h older than the most recent task activity, the
  agent should treat it as suspect.
- Recovery doesn't recover an in-flight task's mid-tool state. If the
  interrupt happened mid-Edit, the file may be partially modified;
  inspect `work/current/artifacts/` for any captured intermediate state.
- This skill READS state. It does not modify state to "fix" anything.
  Recovery is read-only by design — the agent uses the reconstructed
  context to act, not the skill itself.
