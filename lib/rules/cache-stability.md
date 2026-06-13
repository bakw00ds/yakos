---
name: cache-stability
description: Keep the cached request prefix byte-stable across turns so the provider's prompt cache keeps hitting — cache reads are far cheaper than fresh input tokens.
references:
  - rule:lead-dispatch-discipline
  - skill:finops-review
  - skill:cost-summary
---

# Cache Stability

Always loaded (no `paths:` field). This rule governs how the framework
and its agents construct request context so the Anthropic prompt cache
keeps hitting across the turns of a conversation.

Informed by lessons from
[chopratejas/headroom](https://github.com/chopratejas/headroom)'s
prompt-cache analysis — whose own realignment notes admit a hardcoded
setting *"busts the Anthropic prompt cache for every customer that
triggers it."* The failure mode is always the same: mutating, reordering,
or injecting volatile data into the cached prefix.

## The principle

A request is `prefix + tail`. The **prefix** — system prompt, tool and
agent definitions, stable session-start context (always-loaded rules,
roster) — is what the provider caches. Cache reads cost a fraction of
fresh input tokens. To keep hitting the cache:

- The prefix must stay **byte-stable** across the turns of one
  conversation. Same bytes, same order, every turn.
- New content is **appended at the TAIL** (the new user turn, new tool
  results). Never spliced into or ahead of the cached prefix.
- The prefix is **never reordered or mutated mid-session.** Adding,
  removing, or re-sorting a rule/tool/agent definition invalidates the
  cache from the first changed byte onward.

## Anti-patterns that bust the cache

- **Volatile data in the prefix:** timestamps, run-ids, request-ids,
  random nonces, "current date" lines, per-turn counters — anything that
  changes turn-to-turn. If it must be present, put it in the tail.
- **Non-deterministic ordering:** map/dict iteration order, unsorted
  roster compose, set-derived tool lists. Same inputs must serialize to
  the same bytes every time.
- **Per-turn regeneration:** rebuilding the system prompt, re-rendering
  the agent roster, or re-deriving tool definitions on every turn even
  when nothing changed.
- **Reordering rules/tools:** shuffling always-loaded rules or tool
  definitions between turns.
- **Rewriting the system prompt each turn** instead of holding it fixed
  for the conversation's lifetime.

## yakOS-specific application

- **Chat dispatch (`ChatExecCmd`):** the `--append-system-prompt` content
  (the agent definition body) must be stable turn-to-turn for the same
  pane/conversation. Compose it once per conversation; do not regenerate
  or vary it per turn. See `cli-go/internal/runtime/claude.go`.
- **Framed dispatch (`ExecCmd`):** the framing preamble and `--agents`
  JSON should be deterministic — same agent in, same bytes out.
- **Roster compose:** the agent-roster serialization feeding the prompt
  must be deterministically ordered (sort by a stable key). Map iteration
  order in Go is randomized; never serialize a roster straight from a map.
- **Always-loaded rules/context:** keep them free of volatile data. A
  "current date" or "session id" line in an always-loaded rule bursts the
  cache for every session that loads it.
- **`--exclude-dynamic-system-prompt-sections`** (already passed on every
  Claude call) drops the CLI's own volatile system-prompt sections; this
  rule is the framework-side complement — don't reintroduce volatility in
  the parts yakOS controls.

## The payoff

Cache reads are far cheaper than fresh input tokens, so a stable prefix
turns the large, fixed cost of a long system prompt + roster into a
near-free read on every turn after the first. On long sessions this is
the single largest input-token saving available. Track the win on the
existing cost surface — cache hit rate surfaces via `skill:finops-review`
and the `usage` fields in the dispatch-log.

## Audit findings (2026-06-13)

Checked the dispatch prefix construction against this rule — no
cache-buster found in the parts yakOS controls:

- **`claude.go` is clean.** `ExecCmd` and `ChatExecCmd` build args
  deterministically from request fields; no timestamps, run-ids, or
  conversation-ids are injected into the prefix (the conversation id rides
  `--resume` / `--conversation`, i.e. the tail). The framed preamble is a
  fixed string.
- **Roster compose is deterministic.** `agentscompose.Compose` walks
  `os.ReadDir` (filename-sorted) into an explicit insertion-order list
  (framework agents, then project overrides) — not map-iteration order.
  `AgentToJSON` builds the `--agents` payload with `json.Marshal` (which
  sorts map keys) from only stable agent fields (description, prompt,
  tools, model): same agent in, same bytes out.

This rule therefore stands as **preventive discipline** — keep it in mind
before adding any volatile data (timestamps, run-ids) or map-derived
ordering into a cached prefix in future work.

## Scope

Always-loaded discipline. It governs how the framework and every agent
construct request context, regardless of runtime.
