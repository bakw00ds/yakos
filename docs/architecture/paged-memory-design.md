# Paged memory (Tier 3) — design sketch

**Status**: design sketch only. NOT implemented in v0.15.0.0.
Ships only after Tier 1 (context-threshold hook) and Tier 2
(checkpoint subsystem) have produced ≥3 months of real usage
data that justifies the additional complexity.

**Audience**: framework rebuilder; anyone considering implementing
Letta-style hierarchical memory inside yakOS.

**Last updated**: 2026-05-22 (alongside Plan 3 M3 ship).

---

## 1. What problem this solves

Tier 1 (`lib/hooks/context-threshold.sh`) and Tier 2
(`cli/lib/checkpoint.sh`) together handle the "context window is
filling up" pain at 80% of the cost of a Letta-style paged-memory
system. They do this by:

- Tier 1: alerting at 75%, auto-checkpointing at 90%
- Tier 2: snapshotting state for `--fork-session` rewind

What they do NOT solve:

- **Effective infinite context.** Tier 1+2 still hit the window
  ceiling; they just hit it gracefully. Letta-style three-tier
  memory (core / recall / archival) keeps the agent operational
  past the window via paged retrieval.
- **Cross-session semantic recall.** Tier 2 checkpoints are
  whole-session snapshots; agents can't selectively retrieve
  "what did we decide about auth, anywhere across the last 6
  weeks?" without operator scaffolding.
- **Long-running projects with deep history.** A 6-month project
  with 5000 cycles will produce a `lessons.md` and
  `decisions.md` longer than any context window. Today the
  operator (or librarian) curates them by hand.

If Tier 1+2 prove insufficient after months of usage, Tier 3
is the right next step. If they prove sufficient — Tier 3
stays an idea.

## 2. Three-tier memory architecture (borrowed from Letta / MemGPT)

```
┌──────────────────────────────────────────────────────────────┐
│ CORE MEMORY (always in context window)                       │
│   Labeled blocks the agent reads/writes directly.            │
│   Size: 4–8 KB per block; 2–4 blocks per agent.              │
│   Examples: persona, current_focus, user_profile, deadlines  │
│   Persists across sessions; mutable mid-session via tools.   │
└──────────────────────────────────────────────────────────────┘
                              ↓ overflow / promotion
┌──────────────────────────────────────────────────────────────┐
│ RECALL MEMORY (searchable conversation history)              │
│   Full conversation history, NOT in context.                 │
│   Indexed by timestamp + speaker + content.                  │
│   Agent retrieves via `recall_search("query", k=N)` tool.    │
│   Storage: SQLite per project; FTS5 for keyword search;      │
│   optional sqlite-vss for semantic.                          │
└──────────────────────────────────────────────────────────────┘
                              ↓ archive / cold-storage
┌──────────────────────────────────────────────────────────────┐
│ ARCHIVAL MEMORY (cold storage, semantic-only)                │
│   Embedded chunks of important content (decisions, lessons,  │
│   architecture notes). Not full conversation history.        │
│   Indexed by embedding vector (cosine similarity).           │
│   Agent retrieves via `archival_search("query", k=N)` tool.  │
│   Storage: same SQLite + sqlite-vss; OR Ollama-served local  │
│   embedding model + flat file.                               │
└──────────────────────────────────────────────────────────────┘
```

## 3. yakOS integration shape (proposed)

### 3.1 New MCP server

Ship paged memory as an MCP server `yakos-memory-mcp`, NOT as
a yakOS native primitive. Reasons:

- MCP servers are vendor-portable; the same server works for
  Claude / Codex / agy.
- yakOS's audit-trail-first posture doesn't depend on memory
  shape; the server is observable from outside.
- Easy to disable per-project (just remove from `.mcp.json`).

**Language**: Go. Reasons:
- Fast cold start (matters for short-lived MCP invocations)
- SQLite via mattn/go-sqlite3; sqlite-vss bindings available
- Single binary; deploys to `~/.local/bin/` or vendored at
  `lib/mcp-servers/yakos-memory-mcp/`
- Matches yakOS's "bash-first, optional language additions"
  posture (we already accept Python for SDK adapters; Go for
  MCP servers is the same trade-off)

### 3.2 Tool surface (MCP tools)

```
core_view(block_name)             → returns block content
core_edit(block_name, new_text)   → replace block contents
core_append(block_name, text)     → append to block
recall_search(query, k=10)        → returns top-k snippets
archival_search(query, k=5)       → returns top-k embedded chunks
archival_insert(content)          → adds to archival memory
```

Tools follow Letta's API closely. Agents call them like any
other MCP tool; no special yakOS knowledge required.

### 3.3 Storage layout

```
~/.yakos-state/memory-db/
├── <project-slug>.sqlite        # per-project DB
│   tables:
│     core_blocks (name, content, updated_at)
│     recall (id, ts, speaker, content, tsvector_idx)
│     archival (id, content, embedding BLOB, sha)
│     metadata (key, value)
```

Per-project DB; not shared. Multi-dev coord plan (rosy-crafting-candy)
would extend this by mirroring core_blocks to the shared coord
dir, but recall + archival stay per-user.

### 3.4 Embedding strategy

Two paths, operator-choosable:

**Path A (default)**: Ollama-served local embeddings.
- Model: `nomic-embed-text` or similar small embedding model
- No external API; runs entirely on the dev box
- Cold-start latency acceptable since archival_search is rare

**Path B (opt-in)**: cloud embeddings.
- Cohere / OpenAI / Anthropic embeddings via API
- Faster + better quality
- Costs $; requires API key
- Set via `~/.yakos-state/settings.json` `paged_memory.embeddings.provider`

## 4. Migration path

If Tier 3 ships, existing Tier 1+2 users migrate via:

1. `yakos memory paged-init <project>` — creates SQLite DB,
   seeds core_blocks from `~/.yakos-state/memory/<project>/`
   files (one block per md file).
2. Recall memory bootstrap: walk all existing
   `<work>/archive/*/messages.ndjson` files; ingest into
   recall.
3. Archival memory bootstrap: ingest existing `lessons.md`,
   `decisions.md`, ADRs, playbooks; embed; insert.

Backward compat: with paged memory enabled, the existing
`yakos memory {show, put}` commands write to core_blocks
instead of files. The CLI shape is unchanged.

## 5. Composition with shipped Tier 1+2

| Capability | Tier 1+2 (shipped v0.15.0.0) | Tier 3 (sketch) |
|---|---|---|
| Notice context filling | hook NOTE at 75% | unchanged; complementary |
| Auto-checkpoint | at 90% | unchanged |
| Resume from checkpoint | `--fork-session` | unchanged |
| Semantic recall | no | YES (archival_search) |
| Effective infinite context | no | YES (recall + archival offload) |
| Cross-session memory | yes (`~/.yakos-state/memory/`) | YES (SQLite, more powerful) |
| Operator-curated memory | yes (manual edits) | YES + agent-mutable |
| Vendor coupling | none | low (MCP server) |
| Implementation cost | ~2 weeks (done) | ~2–4 months estimated |

## 6. Decision criteria for shipping Tier 3

Don't start Tier 3 until ALL of these hold:

1. ≥3 months of real Tier 1+2 usage data
2. ≥5 documented instances where context-window overflow
   degraded session quality despite checkpoint discipline
3. ≥1 documented instance where cross-session semantic recall
   would have prevented a wasted re-derivation
4. Operator has bandwidth for a Go binary as a yakOS dependency
   (vs. the current bash-only framework surface)

If any criterion fails, defer Tier 3 indefinitely.

## 7. Anti-patterns to avoid

- **Don't make Tier 3 the default.** Tier 1+2 are cheap and
  cover 80% of the pain. Make Tier 3 opt-in per `.yakos.yml`
  `paged_memory.enabled: true`.
- **Don't ship without checkpoint integration.** Operator must
  be able to roll Tier 3 state back to a known-good point.
  Pair with Tier 2's checkpoint mechanism.
- **Don't replace files with database for everything.**
  `decisions.md`, ADRs, playbooks STAY as files. The Tier 3
  DB indexes them; it doesn't own them. Files remain the
  audit-trail-friendly source of truth.
- **Don't expose the SQLite DB directly to operator edits.**
  Operator interacts via `yakos memory` CLI; the DB schema is
  internal.

## 8. References / inspiration

- Letta (formerly MemGPT) — three-tier memory architecture
  ([Letta v1 agent loop](https://www.letta.com/blog/letta-v1-agent))
- MemGPT paper (Packer et al. 2023) — hierarchical memory
  pattern
- Anthropic memory tool (`memory_20250818`) — client-side
  `/memories/` directory pattern for comparison
- yakOS framework-internal-plan.md §6 (Capability D, Tier 3)

## 9. What this doc does NOT do

- Specify the Go MCP server's full API
- Pick a specific embedding model
- Define the SQLite schema in detail
- Estimate latency budgets
- Plan rollout to existing yakOS users

Those land in a follow-on design doc IF Tier 3 ever ships. This
doc captures the shape so the framework rebuilder knows what's
on the road and what's been considered.
