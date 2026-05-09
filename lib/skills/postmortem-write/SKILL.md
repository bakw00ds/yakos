---
name: postmortem-write
description: Produce a blameless postmortem — UTC timeline, impact, root cause, what worked / didn't, action items with owners and due dates
allowed-tools: Read Bash
argument-hint: "[--incident <id>] [--out <path>]"
mode: [author]
---

# Postmortem Write

## Purpose

Generate a blameless postmortem document for an incident. Used by
the `sre` and `incident-responder` agents. The output is a draft
the incident commander edits — it enforces the section order,
forces UTC timestamps, and requires every action item to have an
owner and due date before the doc passes lint.

## Scope

- Reads incident artifacts the operator points it at:
  - chat transcript (Slack export, Discord export, generic
    `messages.json`)
  - alert firings within the incident window
  - deploy log around the incident window
  - any operator-written notes
- Produces a postmortem markdown at
  `postmortems/YYYY-MM-DD-<slug>.md`.
- Enforces the blameless-postmortem template: timeline in UTC,
  impact quantified, root cause stated, what-worked / what-didn't,
  action items with owners + due dates.
- Does NOT publish to a wiki or send to stakeholders; the operator
  commits and notifies separately.

## When to use

- Within 5 business days of an incident. Memory decays fast; the
  fresher the writeup, the more useful.
- For sev-1 and sev-2 incidents always. Sev-3: optional, but
  recommended if the incident revealed a process gap.
- After a near-miss that didn't page but should have (latent
  failure mode caught by chance) — call it a "near-miss
  postmortem" with the same template.

## When NOT to use

- For routine deploy rollbacks that worked. A rollback is not an
  incident; it's the system working as designed.
- As performance review material. "Blameless" means individuals
  are not named as causes. The skill enforces this in the lint
  pass — see Known gotchas.
- For ongoing incidents. Wait until mitigation is in place and
  the system is stable. A postmortem written during an active
  incident is just stress-induced fiction.

## Automated pass

1. Resolve the incident artifacts. Default layout:
   ```
   incidents/<id>/
     chat.json
     alerts.json
     deploys.json
     notes.md
   ```

2. Build the timeline. Merge events from chat, alerts, and deploys
   into a single chronological list. **All times in UTC.** The
   skill rejects events that lack timezone info — guess-converting
   from local time is how postmortems get the timeline wrong.
   ```sh
   jq -s '
     [.[0][] | {ts: .ts, source: "chat",   text: .text},
      .[1][] | {ts: .ts, source: "alert",  text: .name},
      .[2][] | {ts: .ts, source: "deploy", text: .sha}]
     | sort_by(.ts)
   ' chat.json alerts.json deploys.json > timeline.json
   ```

3. Render the template. The section order is fixed:

   ```markdown
   # Postmortem: <incident title>

   **Status:** draft | review | published
   **Incident ID:** <id>
   **Severity:** sev-1 | sev-2 | sev-3
   **Date:** YYYY-MM-DD
   **Authors:** <names>

   ## Summary
   2-4 sentence narrative for an exec audience. What happened,
   for how long, what the customer saw, what fixed it.

   ## Impact
   - Affected customers: <count or %>
   - Affected services: <list>
   - Customer-visible duration: <HH:MM, UTC start–end>
   - Error budget burn: <% of monthly>
   - Revenue / SLA impact: <if any>

   ## Timeline (UTC)
   | Time (UTC) | Event |
   |---|---|
   | 14:02 | Alert `HighErrorRate` fired |
   | 14:04 | On-call paged, ack'd |
   | 14:09 | Incident channel #inc-1234 opened |
   | 14:23 | Mitigation: rolled back deploy abc1234 |
   | 14:28 | Error rate returned to baseline |
   | 14:45 | Incident closed |

   ## Root cause
   The actual technical cause. Not "human error" — humans operating
   inside a system are the system. Describe what the system allowed
   to fail.

   ## Contributing factors
   What made the incident worse / longer than it had to be.
   Examples: stale runbook, alert misfired, deploy tooling lacked
   guardrail.

   ## What worked
   - Detection time was 2 minutes — alert fired correctly.
   - Rollback procedure was clean.

   ## What didn't work
   - Runbook for `HighErrorRate` was a stub.
   - On-call had to ping #platform for kubectl access.

   ## Action items
   | Item | Owner | Due | Tracker |
   |---|---|---|---|
   | Replace stub runbook for HighErrorRate | @alice | 2026-05-22 | ENG-1234 |
   | Pre-grant kubectl prod-read to on-call | @bob | 2026-05-15 | INF-456 |
   | Add deploy-rate canary check | @carol | 2026-06-01 | ENG-1240 |

   ## Lessons
   1-3 takeaways the team carries forward. Not "be more careful" —
   structural changes, not exhortations.
   ```

4. Lint the draft before writing:
   - timeline times all match `^\d{2}:\d{2}$` UTC format
   - every action item row has a non-empty owner AND a due date
   - no person is named in **Root cause** — the lint flags
     "@<name> caused" / "<Name>'s fault" patterns
   - **Impact** has at least one quantified row (count, %,
     duration, or $) — not just "users affected"

5. Write to
   `postmortems/$(date -u +%Y-%m-%d)-<slug>.md`. Refuse to
   overwrite without `--force`.

## Manual pass

For a quick draft when artifacts aren't easily exportable:

```sh
cp lib/templates/postmortem-template.md postmortems/$(date -u +%Y-%m-%d)-<slug>.md
$EDITOR postmortems/$(date -u +%Y-%m-%d)-<slug>.md
```

…and fill in. The lint above can still run as a pre-commit hook on
the postmortems directory.

## Known gotchas

- **Blameless ≠ accountability-less.** Action items have owners.
  "Owner" is who tracks the fix, not who caused the incident.
  Reviewers sometimes object that naming an owner is "blameful" —
  it isn't. No-owner action items are unowned and don't ship.
- **Timezone bugs.** The single most common postmortem error is
  mixing local and UTC times in the timeline ("14:02 alert" then
  "9:23 mitigation" — was that PT or UTC?). The lint forces UTC
  and rejects unparseable times, but if the source artifact lacks
  TZ info entirely, the operator must annotate by hand. Skill
  flags missing TZ rather than guessing.
- **Action items go stale.** A postmortem with 12 unowned action
  items sitting at "due 2024-Q3" two years later is a process
  smell. Project should run a quarterly action-item audit; the
  postmortem template doesn't enforce this — `lib/skills/agent-audit`
  is process-oriented but doesn't cover this specifically yet.
- **"Root cause" is plural.** Most incidents have a chain of
  contributing factors, not a single cause. The template names
  the field "Root cause" for tradition; treat it as "primary
  technical failure" and use **Contributing factors** for the
  rest. Anti-pattern: forcing the chain into one bullet to fit
  the field name.
- **Customer-visible duration vs total incident duration.** These
  often differ — alert fires at 14:02, customers stop being
  affected at 14:28, incident channel closes at 14:45. The
  Impact section uses customer-visible; the Timeline shows the
  full span. Don't conflate the two.
- **Publishing.** The postmortem is in git but stakeholders may
  not read it there. The team's process should include a "publish
  to wiki + email link to incident-list@" step; the skill does
  not perform either.

## References

- Google SRE Book ch. 15, "Postmortem culture: learning from
  failure."
- Etsy's blameless postmortem essay
  (https://codeascraft.com/2012/05/22/blameless-postmortems/).
- `lib/skills/runbook-author/SKILL.md` — postmortem action items
  often produce new runbook entries; that skill consumes this
  one's output.
- `postmortems/` — project-level archive.
