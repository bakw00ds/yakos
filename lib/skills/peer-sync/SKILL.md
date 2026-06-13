---
name: peer-sync
description: Read recent peer activity for a yakOS multi-dev session and summarize it for the lead. Use at session start in multi-dev mode, to catch up on what peers changed while you were away.
tier: sonnet
invocable_by: lead
domains: [coordination, multi-dev]
version: 1
references:
  - rule:multi-dev-coord
  - rule:lead-dispatch-discipline
---

# peer-sync

## Purpose

When the lead starts a new session and `yakos peer status` shows other
peer sessions active, this skill produces a concise summary of what
the peers have been doing — recent decisions, active claims, in-flight
mode proposals — so the lead doesn't dispatch into a peer's
in-progress work.

## Scope

- Reads `/var/lib/yakos/<project>/coord/activity.ndjson` (tail)
- Reads `/var/lib/yakos/<project>/coord/active-claims.json` (projection)
- Reads each peer session's `coord/sessions/<user>@<host>-<pid>.ndjson`
  (last ~5 events per session)
- Output is a single context-block summary, < 500 tokens

Does NOT:
- Modify any coord state
- Send peer messages
- Make dispatch decisions (that's the lead's job after reading this)

## Automated pass

Lead invokes via `bash`:

```bash
yakos peer status
yakos peer log --since "$(date -u -j -v '-10M' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -d '-10 minutes' +%Y-%m-%dT%H:%M:%SZ)"
yakos peer claims
```

The three outputs together form the peer-context picture. This skill
exists to *synthesize* them into a lead-friendly summary, not to
gather them — the gathering is already CLI-supported.

Synthesis format (skill output):

```
Peer context (last 10 min):
- N peer session(s) active: <user1>@<host>, <user2>@<host>
- Recent decisions:
  - alice (frontend-pro): refactored src/auth/login.ts to use OAuth fallback
  - bob (backend-pro): added /v1/users endpoint
- Active claims (M peers holding K files):
  - alice holds src/auth/login.ts (frontend-pro, 8min remaining)
  - bob holds db/migrations/004_users.sql (backend-pro, 25min remaining)
- In-flight mode proposals:
  - alice proposed serialize on src/auth/** at <ts> — no response yet
- Dispatch advisory:
  - SAFE: anything outside src/auth/** and db/migrations/004*
  - CONTENDED: src/auth/** (alice active; consider respond-mode --ack)
  - WAIT: db/migrations/004* (bob holds; will release on team_deleted)
```

## Manual pass

If the automated CLI outputs are missing (coord not enabled, no peers
active), the skill outputs:

```
Peer context: no other sessions active. Proceed as normal.
```

Lead should still read the most recent peer DMs in
`work/current/messages.ndjson` even when peer-sync reports clean — those
are a separate channel.

## Known gotchas

- **Stale snapshot.** Peer activity from >10 min ago is excluded;
  re-invoke if your planning conversation exceeds that.
- **Active-claims rebuild lag.** `active-claims.json` is rebuilt by the
  peer-claim-confirm.sh hook; if a peer just emitted a claim_intent
  but hasn't completed the edit yet, the projection may not show it.
  The activity log tail does.
- **Cross-host attribution.** The shared dev box is single-host by
  design; if you see events from a host you don't recognize, surface
  to operator — could be a stale ledger from a renamed machine.

## Tier rationale

Sonnet is enough — this is a synthesis task on structured input
(NDJSON + JSON projections), not generation. Haiku is too small for
the multi-source reconciliation; Opus is overkill.
