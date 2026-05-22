---
name: multi-dev-coord
description: How a lead behaves when peer sessions are active on the same dev box
paths:
  # Project-imports this rule into CLAUDE.md when yakos init --multi-dev
  # provisions a project; absent that import, the rule does not load.
  - CLAUDE.md
version: 1
references:
  - rule:lead-dispatch-discipline
  - rule:git-hygiene
---

# Multi-dev coordination

**When this loads:** the project ran `yakos init --multi-dev` which
appended an import line to `CLAUDE.md`. Single-developer sessions never
see this rule.

**Why:** two human developers can be working in the same yakOS project
on the same shared dev box simultaneously. The framework provides per-
file claim hooks (Plan 1 M2) and a shared activity stream
(`/var/lib/yakos/<project>/coord/`), but the lead persona has to know
how to coordinate, not just observe.

## The protocol

### 1. Session-start awareness

Before the first dispatch in a new session, run `yakos peer status`. If
any peer session is active:

- Run `yakos peer log --since <ts-of-your-session-launched>` to see
  what they've been doing in the last few minutes.
- Run `yakos peer claims` to see what files they currently hold.
- Decide whether to deconflict by file ownership BEFORE dispatching.

The cost of 10 seconds of peer-context check is much less than the cost
of dispatching a specialist into a file the peer holds.

### 2. Mode proposal before contended dispatches

When you're about to dispatch a specialist that may touch a path a peer
is active on:

```
yakos peer propose-mode --mode serialize --targets "src/auth/**" --reason "alice is mid-refactor on login"
```

This emits a `mode_proposal` event to the coord activity stream and
**waits synchronously up to 60s** for the peer's `mode_response`. The
peer's hook surfaces the proposal to their lead at the next PreToolUse
fire; their lead acks (`yakos peer respond-mode --ack`) or rejects
(`yakos peer respond-mode --reject --reason "..."`).

**On timeout (60s, no response):** default to serialize the proposed
targets. Logged with `timeout: true` in the activity stream so the
audit trail shows the decision was made without peer ack.

**On reject:** surface to the operator. Do NOT silently override.

### 3. Mode kinds

- **`parallel`** — both sessions proceed concurrently. Use when work
  is genuinely independent (different files, different domains).
- **`serialize`** — one session works the contended targets, the other
  waits. Use when work overlaps and merge cost is high.
- **`defer`** — pause this dispatch entirely; revisit after the peer's
  current task completes. Use when their work supersedes yours.

### 4. Audit trail

Every `mode_proposal` / `mode_response` event is in `coord/activity.
ndjson` with full reason text. Decisions made via mode-negotiation MUST
also be mirrored to your `work/current/decisions.md` (the local audit
trail) — same rule as peer DMs.

## Anti-patterns

- **Dispatching first, checking peer status second.** If a hook block
  catches it, you wasted the dispatch. Check first.
- **Silently overriding a peer's reject.** The reject is the peer's
  human-in-the-loop signal. Surface to your operator; let them
  coordinate via Slack / SendMessage.
- **Pairwise assumption with 3 peers.** v0.29 protocol language is
  pairwise; if a 3rd operator joins, escalate to your human — the
  framework can't safely negotiate a 3-way without more thought.
- **Treating `yakos peer status` as gospel.** It's a snapshot. By the
  time you finish reading it, a peer could have started a new task.
  Re-check after long planning conversations.

## Related

- `docs/co-pilot-mode.md` — full operator guide
- `lib/skills/peer-sync/SKILL.md` — invokable session-start banner
- `[[lead-template]]` § "Peer coordination" section
