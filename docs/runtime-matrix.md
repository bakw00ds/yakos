# Runtime support matrix

yakOS v0.4 introduced runtime adapters so the framework can launch
sessions on multiple agentic CLIs. This document tracks which features
each adapter supports, what gets soft-degraded, and the operator-facing
trade-offs.

Last updated: 2026-10-05 (K-132 P0a — Go dispatch honors `runtime:`, a
named runtime does not fall back, gemini removed; v0.40.0.0 — unified
console + Flows; fable tier added in v0.38; codex adapter shipped in
v0.4.0).

## Capability matrix

| Capability | claude | codex |
|---|---|---|
| Adapter shipping | v0.3 (always) | **v0.4.0** |
| `inline-agents` (CLI-flag JSON injection) | ✅ `--agents` | ❌ file-based only |
| `path-allowlist-hard` | ✅ `--add-dir` | ✅ `--add-dir` |
| `hooks` | ✅ 7 events | ✅ 6 events |
| `mcp-flag` (CLI flag) | ✅ `--mcp-config` | ❌ via `config.toml` |
| `system-prompt-flag` | ✅ `--system-prompt` / `--append-system-prompt` | ❌ via `AGENTS.md` / `-c` |
| `fork-headless` | ✅ `--fork-session` | ✅ `codex fork` |
| Non-interactive print mode | ✅ `claude -p` | ✅ `codex exec` |
| Default agent file location | (none — JSON injection) | `.codex/agents/*.toml` |
| yakOS-emitted file prefix | n/a | `yakos-*.toml` |

The `gemini` runtime was removed: the Gemini CLI was sunset on 2026-06-18 and
its deprecation shim was past its removal date. Use `agy` (Antigravity CLI),
which succeeded it.

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

## Soft-degrade rules

When the operator passes a flag the chosen runtime can't honor,
`yakos start` prints a NOTE-level warning and proceeds without
that flag. Examples:

- `--ide` is claude-only. On codex/agy, prints
  `NOTE: --ide is claude-specific; ignored for <runtime>.`
- `--bare` is claude-only. Same treatment.
- `--strict-mcp` is claude-only.
- `--continue` works only for claude. codex has session-resumption
  via `codex resume` (different shape).

Hard controls (path-allowlist, secret-scan) that depend on hooks
behave differently per runtime:

- claude: hook stdin/stdout shape documented; yakOS's reference
  hooks under `lib/hooks/` are written against this contract.
- codex: hook surface is similar; hooks need conversion to codex's
  config.toml format. **Out of scope for v0.4.0** — operator can
  install yakOS hooks manually per
  [codex hooks docs](https://developers.openai.com/codex/hooks).

## Auth model

Implemented in [`cli/lib/auth.sh`](../cli/lib/auth.sh). yakOS NEVER
stores or rotates credentials. `yakos auth login <runtime>` shells
into the runtime's own login flow:

- claude: prints `/login` instructions (no headless login flag).
- codex: exec's `codex login`.

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
claude, code-review on codex, doc-writing on agy.

### Go dispatch now honors `runtime:`

On every Go transport (daemon, MCP, console chat, Flows, `YAKOS_IMPL=go`
CLI) the runtime is picked in this order, highest first, the same as
`cli/lib/dispatch.sh`:

1. An explicit runtime: `yakos dispatch --runtime`, `Params.Runtime` (MCP,
   JSON-RPC, REST), or a console pane set to a specific runtime.
2. The agent's `runtime:` frontmatter.
3. `.yakos.yml` `per-domain.<agent domain>`.
4. `.yakos.yml` `default-runtime`.
5. `YAKOS_RUNTIME` (CLI one-shot path only; the daemon never reads it).
6. `~/.yakos-state/default-runtime`.
7. `claude`.

A bare agent name that is itself a runtime (`yakos dispatch codex "..."`)
selects that runtime when the agent has no pin of its own, and counts as
explicit. Agent files named after a runtime (`claude.md`, `codex.md`,
`agy.md`) are skipped with a warning, and `yakos validate` rejects them,
because one would shadow that runtime's own agent.

For a pin or a project default the candidate chain is the chosen runtime,
then the agent's `runtime-fallback`, then `.yakos.yml` `default-fallback`.
The first candidate whose CLI is on PATH and that looks signed in wins:

| Runtime | Looks signed in when |
|---|---|
| claude | The CLI is installed (credentials can live in the keychain or env, so they are not probed). |
| codex | `OPENAI_API_KEY` is set, or `$CODEX_HOME/auth.json` exists. |
| agy | `ANTIGRAVITY_API_KEY` or `GEMINI_API_KEY` is set, a yakos keyring entry exists, or `~/.gemini/antigravity-cli/` exists. |

If nothing passes, dispatch fails fast naming each skipped runtime and why
(e.g. `agy: not signed in; run: yakos auth login agy`). A fallback prints
one line on stderr and is recorded in the dispatch-log (`runtime_chosen_by`,
`fallback_from`). A long-lived daemon reuses the probe's answer for 30 seconds
(5 seconds for a runtime that could not run, so a retry right after a login is
not told the old answer for long), and the agy keyring lookup in it is bounded
to 2 seconds and ends when the dispatch is cancelled. `gemini` is no longer a Go runtime; use
`agy`. Upgrade impact: [UPGRADING.md](../UPGRADING.md).

#### A runtime you name does not fall back

A runtime the operator names (rule 1, or a runtime name used as the agent) is
operator intent, including intent about where the task is sent, so it never
quietly becomes another vendor. If it is not installed or not signed in,
dispatch fails with the runtime, the reason, and the fallbacks it did not use:

```
dispatch: runtime codex was requested explicitly but cannot run: not signed in; run: yakos auth login codex. Not falling back to claude: an explicit runtime does not use the agent's or the project's fallback list
dispatch: to allow a fallback for this run, pass --runtime-fallback claude
```

Only the CLI can opt in: `yakos dispatch ... --runtime codex --runtime-fallback
claude`. For a named runtime the list replaces the (unused) agent and project
lists; for any other choice it is tried after them. MCP, JSON-RPC, REST and
the console have no opt-in. Pins, `.yakos.yml` defaults, `YAKOS_RUNTIME` and the
state default keep walking the fallback lists, as `cli/lib/dispatch.sh` does.

**Deliberate divergence from bash.** `cli/lib/dispatch.sh` walks the fallback
lists for an explicit `--runtime` as well. The Go dispatcher does not. K-143
(the parity matrix) must record this as intended rather than port the bash
behavior back.

The state default (`~/.yakos-state/default-runtime`) is trusted only when it is
a regular file owned by you, not group or world writable, in a directory with
the same properties (not a symlink). A file that fails this is reported and
ignored.

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

Partial streaming is a claude-specific capability. The Chat UI labels buffered
runtimes clearly so operators know to expect a single response rather than a
live stream.

## Jev is not a runtime

TypeSafe's Jev is deliberately absent from this matrix. It has no session, no
agent prompt, no tools, and no text output: it evaluates typed questions
against a redacted state and returns probabilities. yakOS integrates it as a
*decision provider* behind `yakos decide` (see
[decision-providers.md](decision-providers.md) and
[ADR-0009](adr/ADR-0009.md)), so it can never be selected with `--runtime`,
named in `runtime:` or `runtime-fallback:` frontmatter, or offered in the
console chat runtime list. `yakos validate` and `yakos agent lint` reject
`runtime: jev` with a hard error.

## Adding a new runtime

See [`cli/lib/runtimes/README.md`](../cli/lib/runtimes/README.md).
