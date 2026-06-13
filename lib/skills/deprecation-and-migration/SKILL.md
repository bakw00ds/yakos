---
name: deprecation-and-migration
description: Manage the sunset of an old system, API, or feature — decide maintain-vs-sunset, build a proven replacement, migrate consumers incrementally, and remove only after verifying zero usage. Use when proposing to deprecate or remove anything with consumers, when an old code path has become a maintenance liability, or when planning a migration off a legacy interface.
allowed-tools: Read Edit Bash Grep SendMessage
argument-hint: "[<system-or-api>]"
mode: [plan, implement]
tier: sonnet
invocable_by: [lead, architect, api-designer, maintainer, backend]
domains: [architecture, maintenance, api]
version: 1
references:
  - skill:api-diff
  - rule:lead-dispatch-discipline
  - playbook:08-infra-deploy-deps
---

# deprecation-and-migration

## Purpose

Run the lifecycle of removing an old system/API/feature without
stranding the people who depend on it. Code is a liability, not an
asset — every line carries ongoing maintenance cost — but removing it
carelessly breaks consumers.

The failure mode this prevents: announce-and-abandon ("it's deprecated,
figure it out yourself"), and its mirror, the zombie path that's been
"soft deprecated" for years with no owner and no removal.

Adapted from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills)
(MIT) — `deprecation-and-migration`. Complements yakOS `api-diff`
(classifies the SemVer impact of the interface change) and the
`api-designer` agent (designs the replacement contract).

## Scope

- **In:** the maintain-vs-sunset decision, replacement design, consumer
  migration plan, deprecation signaling, and verified removal.
- **Out:** the routine version bump (`version-bump`), and the SemVer
  classification of a single diff (`api-diff`) — this skill orchestrates
  the larger sunset; those are tools it uses.

## The maintain-vs-sunset decision

Before deprecating, answer five questions:

1. Does the system provide unique value?
2. How many consumers depend on it?
3. Does a replacement exist (or what would it cost to build)?
4. What's the migration cost per consumer?
5. What's the maintenance cost of keeping it?

If the maintenance cost is low and consumers are many, "keep it" can be
the right answer. Deprecation is a decision, not a reflex.

## Deprecation type

- **Advisory** — stable system, migration optional. Signal via
  warnings + docs; no forced timeline.
- **Compulsory** — security risk, blocking issue, or unsustainable
  cost. Forced timeline, BUT it ships with migration tooling.

## Automated pass

Migration workflow:

1. **Build a production-proven replacement** covering all critical use
   cases — not a sketch.
2. **Announce** with clear docs and a migration guide.
3. **Migrate incrementally**, one consumer at a time.
4. **Remove only after verifying zero active usage** (telemetry, logs,
   grep across known consumers).

## Patterns

- **Strangler** — run old and new in parallel; route traffic to the new
  gradually until the old is dark.
- **Adapter** — wrap the old interface around the new implementation so
  consumers migrate at their own pace.
- **Feature flags** — per-consumer switching during the transition.

## Manual pass

The lead confirms the sunset has a real landing spot before signing
off: a proven replacement exists, the deprecation ships with tooling
and a date (not an open-ended "soft" deprecation), and removal is
gated on verified zero-usage, not assumption.

## Findings synthesis

The decision and plan land in `work/current/decisions.md` (the
maintain-vs-sunset rationale + the five answers) and an ADR if the
removal is architecturally significant (`skill:adr-write`). The
per-consumer migration status is tracked on the kanban.

## Anti-rationalization

| Rationalization | Reality |
|---|---|
| "Announce it deprecated; users will migrate" | Don't strand users — owning teams migrate their consumers or provide a compatible path. |
| "We'll remove it once everyone's off" (no plan) | Without tooling and a deadline, "soft deprecation" stalls for years. |
| "One more feature on the old system won't hurt" | Adding features to a deprecated system signals it isn't really deprecated. |
| "It's probably unused, just delete it" | Verify zero usage with telemetry/grep before removal — "probably" breaks consumers. |

## Known gotchas

- **No replacement, no deprecation.** Deprecating without a landing
  spot just creates pressure with no escape valve.
- **Zombie code.** A deprecated path with no owner and no removal date
  is the worst of both worlds — cost without progress. Assign an owner
  and a date or don't deprecate.
- **Removal needs proof, not assumption.** "I think nothing calls this"
  is not zero-usage verification.

## Tier rationale

Sonnet — the maintain-vs-sunset call and migration sequencing are
judgment across consumers, cost, and risk. Haiku can't weigh the
tradeoffs; Opus is reserved for genuinely novel architectural sunsets.
