---
name: gather-feedback
description: Pull and triage user feedback from configured sources
allowed-tools: Read Bash Grep
argument-hint: "[--since <date>] [--source <name>]"
mode: [gather]
---

# Gather Feedback

## Purpose

Collect user-facing feedback (bug reports, feature requests, support
tickets, survey responses) from configured sources, deduplicate, and
present a triage-ready summary. Reading feedback is a planning input,
not an implementation task — this skill produces a list, not changes.

## Scope

Operates on whatever feedback sources the project configures. The
skill is generic; project-level configuration lists which sources
apply (issue tracker, support tickets, Slack channels, in-app feedback,
etc.). Without configuration, the skill surfaces "no sources configured"
and stops — silent assumption is worse than asking.

NOT in scope: writing replies, changing tickets' state, or doing
anything that mutates the source systems. Read-only by design.

## Automated pass

1. Read project feedback configuration from
   `<project>/.claude/feedback-sources.json` (or equivalent — projects
   define their own location). If absent, surface and stop.
2. For each configured source, fetch entries since the cutoff (default:
   last 7 days; `--since` overrides).
3. Deduplicate across sources — the same issue often gets reported
   through multiple channels. Match on title similarity + reporter
   identity where available.
4. Categorize by classification:
   - **bug** — something is broken
   - **feature** — new functionality requested
   - **question** — needs information, not change
   - **noise** — spam, off-topic, dupe
   - **unclear** — couldn't classify confidently
5. Score by signal: number of distinct reporters, recency, severity
   keywords (production, crash, data-loss, blocked).

## Manual pass

The invoking agent reviews the categorization for false categorizations
(LLM classification is fallible) and re-categorizes as needed. Then
prioritizes for action:

- Which bugs need an incident response now?
- Which features fit the upcoming planning cycle?
- Which questions need a doc update vs an individual reply?

## Findings synthesis

```
Feedback gathered: <n> entries from <m> sources, <date> to <date>
  bug:      <n> (<n> high-signal)
  feature:  <n> (<n> high-signal)
  question: <n>
  noise:    <n>
  unclear:  <n>

Top 5 by signal:
  <id> | <category> | <signal-score> | <one-line summary>
  ...
```

Each entry retains its original ID and source for traceability — no
fields are paraphrased into oblivion.

## Known gotchas

- Classification quality depends heavily on the source format.
  Free-form support tickets classify worse than structured bug reports.
  When in doubt, mark "unclear" — over-eager classification poisons
  triage.
- Customer feedback often contains sensitive data (PII, account info).
  This skill is read-only, but the artifact it produces lands in
  `work/current/artifacts/` — treat that artifact as sensitive too.
- Feedback "score" is a heuristic, not truth. A single high-quality
  report from a knowledgeable user can outweigh 50 vague tickets.
- The skill respects `--since`; without it, default of 7 days is fine
  for a weekly cadence but wrong for a quarterly review.
