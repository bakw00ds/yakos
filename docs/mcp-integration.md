# MCP integration — cross-runtime dispatch from a Claude Code session

**Status:** v0.31+. Lets a running Claude Code session call codex /
agy / SDK agents as **native tool calls** instead of shelling out via
the Bash tool. Multi-turn conversations are supported for the
runtimes whose CLIs expose session resume (codex, agy, claude,
claude-sdk).

## Why this exists

Without MCP, cross-runtime dispatch from a Claude session looks like:

```
Bash("yakos dispatch codex-reviewer 'review src/auth/' --runtime codex")
```

That works but is opaque to Claude — it just sees stdout/stderr and a
return code. It can't easily chain multiple calls, can't see structured
telemetry, can't resume a prior agent's conversation.

With MCP installed, the same dispatch looks like:

```
dispatch_codex(agent="codex-reviewer", task="review src/auth/")
```

Claude sees this as a tool call with a structured response containing
the agent's reply + a `conversation_id` + token usage metadata. Multi-
turn is a separate tool:

```
continue_codex(conversation_id="abc123", task="now check the test coverage")
```

## Tools exposed (once installed)

| Tool | Purpose | Multi-turn? |
|---|---|---|
| `dispatch_codex` | Run agent via OpenAI Codex | Yes (`continue_codex`) |
| `dispatch_agy` | Run agent via Google Antigravity (CLI) | Yes (`continue_agy`) |
| `dispatch_claude_sdk` | Run agent via Claude Agent SDK | Yes (`continue_claude_sdk`) |
| `dispatch_antigravity_sdk` | Run agent via Antigravity SDK | **No** — SDK lacks cross-process resume at v0.31 |
| `dispatch_claude` | Run agent in a fresh peer Claude session | Yes (`continue_claude`) |

Each `dispatch_*` returns response text + a `conversation_id`. Pass
that id to the matching `continue_*` to keep the same agent's session
alive across turns.

## Prerequisites

- `python3` 3.10+
- `pip install mcp` (the Anthropic Model Context Protocol Python SDK)
- A runtime CLI installed + authed for whichever runtime(s) you want
  to dispatch to (`yakos auth status` to check)

## Install

Two scopes — **per-project** (recommended) or **global**.

### Per-project

```sh
cd /path/to/your/project
yakos mcp install
```

This writes `/path/to/your/project/.mcp.json` (or merges if it
exists) with:

```json
{
  "mcpServers": {
    "yakos-dispatch": {
      "command": "python3",
      "args": ["/abs/path/to/yakos/cli/lib/mcp/yakos-mcp-server.py"],
      "env": {"YAKOS_ROOT": "/abs/path/to/yakos"}
    }
  }
}
```

Restart your Claude Code session in that project. The new tools become
available to the lead.

### Global

If you want every Claude Code session to see the tools (not just
yakOS-bootstrapped projects):

```sh
yakos mcp install --project ~
```

Writes `~/.mcp.json`. Same restart-required note.

### Verify

```sh
yakos mcp status                 # in cwd
yakos mcp status --project ~/code/myapp
yakos mcp probe                  # verifies 'mcp' python package importable
```

## Use from a Claude session

After install + session restart, the lead can call the tools directly.
Examples the lead might run on your behalf:

```
# Single-turn: have codex review the auth module
dispatch_codex(agent="code-reviewer", task="Review src/auth/login.ts for OWASP top 10 issues")

# Multi-turn: same codex agent, follow up
continue_codex(conversation_id="<id from prior>", task="Now check test coverage for those paths")

# Parallel cross-runtime: dispatch to multiple runtimes simultaneously
# (lead makes both calls in the same response; MCP handles them in parallel)
dispatch_agy(agent="security-reviewer", task="Audit the OAuth flow")
dispatch_claude_sdk(agent="test-runner", task="Run the auth test suite headlessly")
```

The lead can chain these naturally — telling claude "have codex review
this, then ask agy to fuzz the inputs" works because both tools are in
the same response surface.

## Conversation state

MCP server state lives at `~/.yakos-state/mcp-conversations.json`:

```json
{
  "conversations": {
    "abc123def456": {
      "runtime": "codex",
      "agent": "code-reviewer",
      "native_id": "codex-session-xyz",
      "created_at": 1747940000,
      "last_active_at": 1747940123
    }
  }
}
```

The yakOS `conversation_id` is a UUID issued by the MCP server; the
`native_id` is the runtime-specific session id (codex's session UUID,
agy's conversation UUID, claude's session id, claude-sdk's session id).

No automatic cleanup yet — entries persist across MCP server restarts.
If conversations.json grows large, `rm` it; you'll lose resume
capability for past conversations but everything still works for new
ones.

## Limitations

1. **Antigravity SDK has no continue_* tool.** The SDK doesn't expose
   cross-process session resume. If you need multi-turn against
   Gemini, use `agy` (which does).

2. **One-shot calls are blocking.** Each tool call runs the dispatch
   to completion before returning. For agents that take minutes, the
   lead is paused that long. Fork-and-poll wasn't worth the
   complexity at v0.31; revisit if it bites.

3. **No per-tool budget caps.** A runaway agent can burn through
   tokens. Wrap with `--timeout <secs>` in the underlying `yakos
   dispatch` call by setting `YAKOS_DISPATCH_TIMEOUT` env on the
   server (TODO: thread it through).

4. **No streaming back to the lead.** The lead gets the full response
   when the agent finishes. Streaming would require MCP's experimental
   streaming tools surface; deferred.

5. **`mcp install` requires you to restart your Claude Code session**
   for the new server to load. There's no hot-reload at the MCP layer.

## Troubleshooting

**Tools don't show up in Claude:**

- Did you restart the Claude Code session after install? MCP servers
  only load at session start.
- `yakos mcp status` — verify the entry is in `.mcp.json`.
- `yakos mcp probe` — verify `mcp` python package is installed.
- Check Claude Code's MCP logs (usually surfaced via `/mcp` slash
  command inside the session).

**`dispatch_codex` returns an error about missing CLI:**

- `yakos auth status codex` — confirm codex is installed AND authed.
- The MCP server inherits your shell's PATH, which usually has the
  runtime CLIs. If not, set `PATH` explicitly in the `env` block of
  `.mcp.json`.

**`continue_codex` says "no native resume id":**

- The previous dispatch didn't emit a session_id. This can happen if
  codex was invoked without `--json` (telemetry path) or if the codex
  version doesn't surface session_id. Re-dispatch fresh; subsequent
  continues should work.

**Old conversation_ids return "unknown conversation":**

- The `~/.yakos-state/mcp-conversations.json` file may have been
  cleared. Re-dispatch to get a fresh id.
