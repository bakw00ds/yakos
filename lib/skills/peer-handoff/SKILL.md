---
name: peer-handoff
description: Hand work to another developer cleanly in multi-dev co-pilot mode, packaging state, context, and next steps. Use when transferring an in-progress task to a peer so they can pick it up without losing context.
tier: sonnet
invocable_by: lead
domains: [coordination, multi-dev]
version: 1
references:
  - rule:multi-dev-coord
  - rule:lead-dispatch-discipline
---

# peer-handoff

## Purpose

In multi-dev co-pilot mode, two developers share a project across
parallel sessions. yakOS already has `peer status`, `peer log`,
`peer claims`, `peer propose-mode` for coordination — but no
documented "I'm done, you continue from here" handoff protocol.

This skill defines the handoff. Sender signals completion +
context; receiver runs `peer-sync` to catch up + acknowledges.

## Scope

- Applies only when `yakos peer status` shows at least one other
  active peer session
- Uses the existing coord activity stream
  (`/var/lib/yakos/<project>/coord/activity.ndjson`)
- Does NOT replace mode negotiation — propose-mode/respond-mode
  is for "should we work in parallel"; handoff is for "I'm done,
  your turn"

## Automated pass

### Sender (the dev finishing their slice)

Lead invokes via `bash`:

```bash
yakos peer handoff \
    --to alice@dev01 \
    --completed-scope 'src/auth/login.ts, src/auth/oauth.ts' \
    --notes 'OAuth flow works; loginWithPassword still has the email-case bug from the original ticket' \
    --next-action 'finish the email-case fix; tests are in tests/auth_test.go'
```

This emits a `peer_handoff` event to the coord activity stream
with the structured fields above. The lead also:

1. Updates `work/current/decisions.md` with a 2-3 line summary
   citing the handoff (operator-visible record; coord stream is
   shared but decisions.md is the local audit)
2. Releases any held claims on the completed scope:
   `yakos peer release src/auth/login.ts` for each file

### Receiver (the dev picking up the work)

Receiver's lead, at the start of their next dispatch:

1. `yakos peer log --since <handoff-ts>` to see the handoff event
2. Run `peer-sync` skill to summarize the broader peer context
3. Read `decisions.md` (operator-visible record from sender)
4. Acknowledge: `yakos peer handoff --ack <handoff-id>` so the
   sender's audit trail shows the handoff was received

If the receiver disagrees with the scope ("you said you finished
login.ts but the OAuth flow is incomplete"):
`yakos peer handoff --reject <handoff-id> --reason "..."` and
surface to both operators via SendMessage.

## Manual pass

If `yakos peer handoff` CLI is unavailable (older yakOS), the
sender appends a manual entry to coord/activity.ndjson:

```json
{"ts":"2026-05-22T...","kind":"peer_handoff","actor":{...},
 "detail":{"to":"alice@dev01","completed_scope":"...",
 "notes":"...","next_action":"...","handoff_id":"manual-..."}}
```

And updates decisions.md manually.

## Output format (handoff event)

```json
{
  "ts": "...",
  "kind": "peer_handoff",
  "actor": {"user": "bob", "host": "dev01", "pid": 1234, ...},
  "detail": {
    "to": "alice@dev01",
    "handoff_id": "<ts>-<pid>-<random>",
    "completed_scope": "src/auth/login.ts, src/auth/oauth.ts",
    "notes": "OAuth flow works; loginWithPassword has email-case bug",
    "next_action": "finish the email-case fix; tests in tests/auth_test.go"
  }
}
```

The receiver's ack/reject is a `peer_handoff_response` event with
the same handoff_id.

## Known gotchas

- **Releasing claims is part of the handoff.** If you handoff
  `src/auth/login.ts` but keep the claim, the receiver's lead
  blocks on `peer-claim` when they try to edit. Always release
  the claims for the completed scope.
- **decisions.md is the operator-visible record.** The coord
  stream is shared (and machine-readable); decisions.md is what
  humans + leads in future sessions read. Both must be updated.
- **Don't handoff in-progress work.** If you stopped mid-task,
  call that out explicitly: "completed: login.ts; partial:
  oauth.ts (callback URL handling unfinished)". Don't pretend
  partial is complete.
- **Three-way handoffs are not supported.** v0.29 mode protocol
  is pairwise. If a third developer joins, escalate to humans
  rather than attempting an automated handoff among 3+.

## When NOT to use this skill

- Single-developer session (no peer to hand to)
- The work is genuinely complete and won't continue (use a normal
  `yakos archive` instead)
- The handoff is to a specialist agent on a different runtime —
  that's `yakos dispatch` or MCP `dispatch_<runtime>`, not peer

## Tier rationale

Sonnet — synthesizing scope + notes + remaining work into a
coherent handoff requires judgment about what level of detail
the receiver needs. Haiku is too narrow for the "what did I
actually accomplish" summary work.
