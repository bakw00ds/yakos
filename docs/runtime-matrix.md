# Runtime support matrix

yakOS v0.4 introduced runtime adapters so the framework can launch
sessions on multiple agentic CLIs. This document tracks which features
each adapter supports, what gets soft-degraded, and the operator-facing
trade-offs.

Last updated: 2026-06-12 (v0.40.0.0 — unified console + Flows; fable tier
added in v0.38; codex adapter shipped in v0.4.0; gemini in v0.4.1).

## Capability matrix

| Capability | claude | codex | gemini |
|---|---|---|---|
| Adapter shipping | v0.3 (always) | **v0.4.0** | **v0.4.1** |
| `inline-agents` (CLI-flag JSON injection) | ✅ `--agents` | ❌ file-based only | ❌ file-based only |
| `path-allowlist-hard` | ✅ `--add-dir` | ✅ `--add-dir` | ✅ `--include-directories` |
| `hooks` | ✅ 7 events | ✅ 6 events | ✅ 11 events |
| `mcp-flag` (CLI flag) | ✅ `--mcp-config` | ❌ via `config.toml` | ❌ inline in `settings.json` |
| `system-prompt-flag` | ✅ `--system-prompt` / `--append-system-prompt` | ❌ via `AGENTS.md` / `-c` | ❌ via `GEMINI_SYSTEM_MD` env var |
| `fork-headless` | ✅ `--fork-session` | ✅ `codex fork` | ⚠ unverified — interactive only |
| Non-interactive print mode | ✅ `claude -p` | ✅ `codex exec` | ✅ `gemini -p` |
| Default agent file location | (none — JSON injection) | `.codex/agents/*.toml` | `.gemini/agents/*.md` |
| yakOS-emitted file prefix | n/a | `yakos-*.toml` | `yakos-*.md` |

✅ = supported. ❌ = not supported (degrade or workaround). ⚠ = unverified.

## What yakOS does per-runtime

### claude

- Composes `--agents` JSON via `cli/lib/agents-compose.sh`, exec's
  `claude --add-dir <repo> --permission-mode bypassPermissions
  --agents <json>`.
- No filesystem materialization — JSON is in-memory only for the
  session's lifetime.
- Auto-detects `<project>/.mcp.json` for `--mcp-config`.

### codex (v0.4.0)

- Materializes each yakOS agent as a TOML file at
  `<project>/.codex/agents/yakos-<name>.toml` (gitignored at init).
- Schema: `name`, `description`, `developer_instructions` (the
  agent body), optional `model`.
- Exec's `codex --add-dir <repo>
  --dangerously-bypass-approvals-and-sandbox`.
- One-shot dispatch via `codex exec` (used by `yakos dispatch` in
  v0.4.2).
- Auth detected at `$CODEX_HOME/auth.json` or `OPENAI_API_KEY`.

### gemini (v0.4.1)

- Materializes to `<project>/.gemini/agents/yakos-<name>.md`
  (markdown with YAML frontmatter — closest format to yakOS
  source; minimal translation needed).
- When `<project>/.mcp.json` is present, merges its `mcpServers`
  block into `<project>/.gemini/settings.json` (gemini-cli has no
  `--mcp-config` flag — MCP is inline). A timestamped backup is
  written before the merge.
- Exec's `gemini --include-directories <repo> --approval-mode=yolo`.
- Dispatch (v0.4.2) uses gemini's native `@<agent-name>`
  delegation syntax: `gemini -p "@yakos-<agent> <task>"`.
- Auth: OAuth (free tier, `~/.gemini/` creds files),
  `GEMINI_API_KEY` env, or Vertex AI
  (`GOOGLE_GENAI_USE_VERTEXAI=true` + gcloud).

## Soft-degrade rules

When the operator passes a flag the chosen runtime can't honor,
`yakos start` prints a NOTE-level warning and proceeds without
that flag. Examples:

- `--ide` is claude-only. On codex/gemini, prints
  `NOTE: --ide is claude-specific; ignored for <runtime>.`
- `--bare` is claude-only. Same treatment.
- `--strict-mcp` is claude-only.
- `--continue` works only for claude. codex has session-resumption
  via `codex resume` (different shape); gemini has `-r/--resume`.

Hard controls (path-allowlist, secret-scan) that depend on hooks
behave differently per runtime:

- claude: hook stdin/stdout shape documented; yakOS's reference
  hooks under `lib/hooks/` are written against this contract.
- codex: hook surface is similar; hooks need conversion to codex's
  config.toml format. **Out of scope for v0.4.0** — operator can
  install yakOS hooks manually per
  [codex hooks docs](https://developers.openai.com/codex/hooks).
- gemini: 11-event hook surface, JSON I/O. yakOS conversion
  planned for v0.4.1.

## Auth model

Implemented in [`cli/lib/auth.sh`](../cli/lib/auth.sh). yakOS NEVER
stores or rotates credentials. `yakos auth login <runtime>` shells
into the runtime's own login flow:

- claude: prints `/login` instructions (no headless login flag).
- codex: exec's `codex login`.
- gemini: prints OAuth / API key / Vertex AI options.

`yakos auth status` reports per-runtime CLI presence + auth
configuration without revealing credentials.

## Mixed-runtime dispatch (v0.4.2, planned)

In v0.4.2, agent frontmatter gains a `runtime:` field:

```yaml
---
id: backend
runtime: codex          # default: claude
model: o4-mini
---
```

`yakos dispatch <agent-name> "<task>"` reads the field, spawns the
right CLI in non-interactive mode, captures the output, and returns
to the caller. The lead (in any runtime) calls this via Bash. This
lets a project mix runtimes per-agent — e.g., orchestration on
claude, code-review on codex, doc-writing on gemini.

## Model tiers

yakOS routes dispatches through four tiers, lowest to highest:

| Tier | Aliases |
|---|---|
| `haiku` | `cheap` |
| `sonnet` | `balanced` |
| `opus` | `best`, `reasoning` |
| `fable` | `frontier` |

Framework agents declare a maximum tier in their frontmatter. `fable` requires
explicit opt-in (agents cap at `opus` by default). Override per-dispatch with
`--model <tier>` or per-pane in the console Chat tab.

## Console Chat streaming behavior (v0.40.0.0+)

The unified console Chat tab uses an unframed execution mode (agent definition
as system prompt, direct `-p`, `--include-partial-messages`):

| Runtime | Streaming behavior |
|---|---|
| `claude` | Token-by-token streaming via SSE. First token latency governed by `--include-partial-messages` mode. |
| `codex` | Buffered — full response arrives as one chunk + summary. |
| `agy` | Buffered — full response arrives as one chunk + summary. |
| `gemini` | Buffered — full response arrives as one chunk + summary. |

Partial streaming is a claude-specific capability. The Chat UI labels buffered
runtimes clearly so operators know to expect a single response rather than a
live stream.

## Adding a new runtime

See [`cli/lib/runtimes/README.md`](../cli/lib/runtimes/README.md).
