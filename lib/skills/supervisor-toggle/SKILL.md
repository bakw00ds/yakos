---
name: supervisor-toggle
description: Flip the supervisor's block_on_critical setting (active ↔ passive) for the current project without leaving the session. Use when the supervisor fires too aggressively (go passive) or the session enters higher-stakes work (go active).
tier: haiku
invocable_by: lead
domains: [coordination, operations]
version: 1
references:
  - rule:lead-dispatch-discipline
  - playbook:01-security
---

# supervisor-toggle

## Purpose

Flip the supervisor's `block_on_critical` setting in the project's
`.yakos.yml` from a running session, without the operator having to
edit the YAML by hand. Used when the supervisor is firing too
aggressively (switch to passive) or when the session enters a
higher-stakes phase (switch to active).

This skill does NOT toggle whether the supervisor is enabled —
that's a session-start decision via `yakos supervise enable/disable`.

## Scope

- Reads/writes `.yakos.yml` in the project root
- No other file changes; never edits source code, hooks, or agents
- Effect takes hold at the next PreToolUse hook fire (within seconds)

Does NOT:
- Disable the supervisor entirely (use `yakos supervise disable` for that)
- Change scoring frequency (`score_every_n_calls`) or runtime (`runtime`)
  — those are session-start decisions, not mid-session toggles
- Override an existing hook-bypass entry; that's a separate mechanism

## Automated pass

Lead invokes via `bash`:

```bash
# Switch to passive — supervisor still scores, never blocks
yakos supervise set block_on_critical false

# Switch to active — supervisor scores AND blocks on CRITICAL
yakos supervise set block_on_critical true

# Verify
yakos supervise status
```

Each invocation:
1. Reads the current value (PASS-through if already at target)
2. Edits `.yakos.yml` via `awk` (preserves other keys + comments)
3. Echoes the new state for the lead to confirm to the operator

## Manual pass

If `yakos supervise set` is unavailable (older yakOS, custom build),
the operator can edit `.yakos.yml` directly:

```yaml
supervisor:
  block_on_critical: false   # or true
```

Then any new tool call's PreToolUse hook will pick up the change.

## When to switch to PASSIVE (block_on_critical: false)

- Supervisor has fired 2+ false-positive CRITICALs this session
- Operator wants to see what the supervisor flags without being blocked
- Mid-exploration session where false drift signals waste time

## When to switch to ACTIVE (block_on_critical: true)

- Entering a high-stakes phase (production refactor, migration,
  release)
- After a session where you reviewed supervisor findings and felt
  they were well-calibrated
- When working on code the operator considers riskier than the
  session opener

## Known gotchas

- **`.yakos.yml` must exist**. If `yakos init` was never run on
  the project, this skill errors. The lead should run `yakos init`
  first, or the operator should.
- **The supervisor must be enabled for the toggle to matter.** If
  `supervisor.enabled: false`, flipping `block_on_critical` is a
  no-op — the supervisor doesn't run at all. Check
  `yakos supervise status` first.
- **The change only affects the LEAD's PreToolUse gate**. If the
  supervisor is in the middle of scoring a batch, the in-flight
  score still completes; the toggle affects the next tool call's
  consult of findings, not the next score.
- **Don't oscillate.** Flipping back and forth every few tool calls
  is a signal that the rubric or `score_every_n_calls` needs tuning,
  not that toggling is the right answer.

## Tier rationale

Haiku is plenty — this skill is "edit one boolean in YAML safely
preserving other keys". No reasoning required beyond invoking the
CLI and confirming the result.
