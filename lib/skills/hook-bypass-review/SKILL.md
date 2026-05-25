---
name: hook-bypass-review
description: Audit work/current/hook-bypass.md for stale entries, expired bypasses, and patterns suggesting a hook needs tuning
tier: haiku
invocable_by: lead
domains: [operations, quality]
version: 1
references:
  - rule:lead-dispatch-discipline
  - hook:secret-scan
  - hook:path-allowlist
  - hook:peer-claim
  - hook:supervisor-gate
  - hook:budget-guard
---

# hook-bypass-review

## Purpose

Periodically audit `work/current/hook-bypass.md` for entries that
should be removed (expired, completed, or never followed up) and
patterns that suggest a hook is too aggressive rather than the
bypass being legitimate.

Invoke before `yakos archive` (so stale bypasses don't roll into
the archive) and weekly during long projects.

## Scope

Reads only:
- `work/current/hook-bypass.md` (the active bypasses)
- `work/current/logs/<hook>.ndjson` (to confirm hook fire history)

Writes nothing automatically — outputs a structured report the
operator reviews.

## Automated pass

Walk every `## bypass:<id>` entry in the Active entries section.
For each:

```markdown
### bypass:<id>

- **Hook:** <hook name from the entry>
- **Created:** <ts>
- **Expires:** <ts>
- **Status:**
  - **EXPIRED** (Expires has passed) — should be removed
  - **STALE** (Created > 7 days ago, no Follow-up activity) — review
  - **ACTIVE** (within Expires) — fine
- **Pattern check:** has this Scope been bypassed > 2 times in the
  last 14 days?
  - If yes → suggests the underlying hook is too aggressive for
    this project; recommend tuning the hook rather than repeating
    the bypass. Cite the previous bypass entries.
- **Follow-up status:** does the Follow-up field describe a
  concrete action that's been done?
  - If no → the bypass is unaccounted for; surface to operator.
```

## Manual pass

Operator reads the report. For each EXPIRED entry: remove from
hook-bypass.md (or move to a `## Resolved entries` section for
audit history). For each pattern-flagged entry: consider editing
the underlying hook's threshold or scope rather than continuing
to bypass.

## Output format

```markdown
# Hook-bypass review — <ts>

Total active entries: <N>
- Expired: <count>
- Stale (>7d, no follow-up): <count>
- Pattern flagged: <count>
- Healthy: <count>

## Entries needing action

(list each with the four-field summary above)

## Hooks suggested for tuning

(if any pattern-flagged: name the hook + the recommended action)
```

## Known gotchas

- **Don't auto-remove.** Bypass entries are operator-approved; the
  skill outputs recommendations, not edits.
- **Time-zone honesty.** ISO-8601 ts comparisons need UTC. Don't
  compare local-time strings.
- **The "permanent bypass" anti-pattern.** Expires > 30 days is
  almost always a sign the bypass should be a config change instead
  (e.g., raising `budget.max_tool_calls` rather than bypassing
  forever).
- **A clean hook-bypass.md is a goal, not a guarantee of safety.**
  An empty bypass file just means nothing's been overridden; it
  doesn't mean the hooks themselves are correctly tuned.

## Invoke

- Before `yakos archive` (manual; lead runs the skill)
- Weekly during long projects
- After any new hook is added (verify the new hook isn't immediately
  being bypassed)

## Tier rationale

Haiku — pattern matching on YAML-shaped text + ts arithmetic. No
nuanced reasoning required.
