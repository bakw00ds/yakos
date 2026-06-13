---
name: agent-audit
description: Scan dispatch-log for misuse patterns — lead-did-specialist-work, wrong-runtime, budget-violations, repeated-failures. Use when reviewing how a session dispatched work, or auditing for dispatch-discipline drift.
allowed-tools: Read Bash
argument-hint: "[--since <ISO>] [--project <name>]"
mode: [audit]
---

# Agent Audit

## Purpose

Spot patterns of agent misuse from the dispatch-log so the operator
can either tighten frontmatter (e.g., add `runtime-fallback`) or
update the lead's discipline (e.g., the lead is dispatching the
wrong specialist for a task type). Read-only — produces a report,
makes no changes.

## Scope

Audit-only. The skill produces a markdown report listing:

1. **Lead-did-specialist-work** — sessions where the lead's
   `Bash` calls touched code (git commit, npm install, build
   commands). Indicates the lead bypassed dispatch.
2. **Wrong-runtime patterns** — agents whose `runtime:` doesn't
   match the bulk of what they were actually dispatched on.
   E.g. an agent declared `runtime: codex` but most calls used
   `--runtime claude` overrides — frontmatter is stale.
3. **Budget violations** — `budget_violation` events from
   dispatch-log.ndjson (added v0.8): real cost exceeded
   `max-cost-per-task`.
4. **Repeated failures** — agents with >2 consecutive `exit_code != 0`
   calls. Likely a misconfigured agent body or a runtime regression.
5. **Unused agents** — agents present in `<project>/.claude/agents/`
   that haven't been dispatched in the audit window. Either dead
   code or under-utilized.

## When to use

- Weekly / monthly retrospective on yakOS usage in a project.
- After a stretch of "feels like agents aren't pulling their weight"
  to get evidence.
- Before a refactor of project agents — see what's actually used.

## When NOT to use

- For a single session — too noisy; use `session-summary` instead.
- As a security audit — this is process hygiene, not threat
  detection. Use `security-reviewer` agent for the latter.

## Automated pass

1. Resolve the audit window:
   ```sh
   since="${SINCE:-$(date -u -v-30d +%Y-%m-%d 2>/dev/null \
       || date -u -d '30 days ago' +%Y-%m-%d)}"
   ```

2. Pull dispatch-log entries:
   ```sh
   tmp=$(mktemp -t yakos-audit.XXXXXX)
   for f in $HOME/.yakos-state/dispatch-log*.ndjson; do
       [ -f "$f" ] || continue
       jq -c --arg s "$since" 'select(.ts >= $s)' "$f" >> "$tmp"
   done
   ```

3. **Pattern 1: Lead-did-specialist-work.**
   yakOS hooks log lead-context Bash commands at
   `~/agent-control/<project>/work/current/logs/path-log.ndjson`.
   Filter for entries with command = git commit / npm install /
   build commands. Cross-reference with the session's
   dispatch-log: if the lead committed but no maintainer/release-
   manager dispatch fired in the same window, flag.

4. **Pattern 2: Wrong-runtime.**
   For each agent that was dispatched ≥3 times, count which
   runtime ran it. If the agent's frontmatter `runtime:` differs
   from the majority runtime, surface as drift.
   ```sh
   jq -s 'group_by(.agent)
       | map(select(length >= 3))
       | map({
           agent: .[0].agent,
           by_runtime: ([.[] | .runtime] | group_by(.) | map({(.[0]): length}) | add),
           majority: ([.[] | .runtime] | group_by(.) | sort_by(length) | last | .[0])
         })' "$tmp"
   ```

5. **Pattern 3: Budget violations.** Filter
   `select(.type == "budget_violation")` from the log.

6. **Pattern 4: Repeated failures.** Walk dispatch_finished events
   per agent in chronological order; count consecutive non-zero rcs.

7. **Pattern 5: Unused agents.** List
   `<project>/.claude/agents/*.md` ids; subtract those that appear
   in the dispatch-log for the window.

8. Compose markdown report. Prefix each pattern with severity
   (info / warn / err). Per-finding, suggest a remediation.

## Manual pass

For ad-hoc spot checks:
```sh
yakos cost --since 2026-04-01 --by agent | head -20    # heavy hitters
yakos cost --since 2026-04-01 --json --by agent | jq '.rows[] | select(.fail > 0)'
jq -c 'select(.type == "budget_violation")' ~/.yakos-state/dispatch-log*.ndjson
```

## Known gotchas

- **Empty audit window.** If the window predates yakOS v0.6 (when
  telemetry started), patterns 3+ have no data. Skill notes this.
- **Lead Bash detection is heuristic.** `git status` is fine;
  `git commit` is the signal. The skill uses a configurable
  command pattern — operator can extend `~/.yakos-state/audit-
  patterns.txt` to refine.
- **Multi-runtime confusion.** If an agent legitimately runs on
  multiple runtimes (different domains for different tasks), the
  "wrong-runtime" pattern produces false positives. Add the agent
  to an audit ignore list.

## References

- `cli/lib/dispatch.sh` — emits `budget_violation` events.
- `cli/lib/cost.sh` — primary cost surface.
- `lib/skills/session-summary/SKILL.md` — single-session view.
- `~/.yakos-state/dispatch-log.ndjson` — source data.
