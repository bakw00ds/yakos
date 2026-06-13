---
name: mcp-as-agent
description: Wrap an MCP server as a yakOS agent so tool-side and LLM-side specialists share the same dispatch surface. Use when an MCP server takes structured input and returns structured output like a specialist and you want to dispatch to it.
allowed-tools: Read Edit
argument-hint: "<mcp-name> <project-path>"
mode: [design]
---

# MCP-as-Agent

## Purpose

Some MCP servers are functionally equivalent to a specialist agent —
they take a structured input, do work, return a structured output.
Examples:

- A linter MCP that lints files and returns findings.
- A database-explorer MCP that runs read-only queries.
- A repo-search MCP that returns ranked file matches.

When the operator wants to dispatch to "the linter" the same way
they'd dispatch to a `code-reviewer`, treating the MCP as an agent
gives a uniform interface. The lead calls `yakos dispatch
<mcp-as-agent> "..."` whether the implementation is an LLM
specialist or a tool-side MCP.

## Scope

- **In:** wrap one MCP server (or one named tool of an MCP server)
  in an agent file whose body invokes the server. The agent
  dispatches via `yakos dispatch`, which spawns a runtime that has
  the MCP configured, prompts it to call the named tool, and
  returns the tool's result.
- **Out:** running the MCP server itself; that's the operator's
  responsibility (mcp.json registration). Agent stdio adapters; that
  would be a runtime adapter, not a skill.

## When to use

- Project has an MCP server that does deterministic work and would
  benefit from being addressable like a specialist (e.g., the lead
  says "delegate this lint pass to the linter agent").
- Want LLM agents and MCP tool-calls to share an audit trail
  (dispatch-log captures both).
- Cost: the MCP server is cheaper than spinning up a full LLM call
  for the same task.

## When NOT to use

- The MCP server requires complex multi-step reasoning between tool
  calls — that's actual LLM work, not MCP-as-agent.
- The MCP is for read-only context-gathering during another agent's
  work — leave it as a regular MCP, available to all agents.
- The MCP is interactive (UI / approval flows). MCP-as-agent works
  best for one-shot inputs → outputs.

## Automated pass

The skill produces an agent .md file. Steps:

1. **Verify MCP is configured.** Check `<project>/.mcp.json` (or
   the runtime's equivalent) has the named server. If absent,
   instruct the operator to configure it first.

2. **Write the agent file** at
   `<project>/.claude/agents/mcp-<mcp-name>.md`. Body shape:

   ```yaml
   ---
   id: mcp-<mcp-name>
   role: specialist
   domain: tool-call
   mode: [tool]
   tools: [<MCP server's tool name(s)>]
   model: cheap        # the LLM doing the routing is small;
                       # the MCP does the actual work.
   references: []
   ---

   # MCP wrapper: <mcp-name>

   ## Purpose

   Single-shot wrapper around the `<mcp-name>` MCP server.
   Receives a task; routes the task to the MCP; returns the
   tool result verbatim. Adds no LLM reasoning of its own.

   ## Execution

   1. Receive the task as a single-shot input.
   2. Call the named MCP tool with the task as input. Do not
      reformulate, summarize, or interpret.
   3. Return the tool's structured output verbatim. If the tool
      returned an error, return the error string.

   ## Special rules

   - **No editorializing.** This agent is a thin call-site for
      the MCP. The lead is the one that decides what to do with
      the result; this agent's job is to deliver it.
   - **No fan-out.** One task → one tool call → one return. If
      the lead wants multiple calls, dispatch this agent multiple
      times.
   ```

3. **Document in `<project>/decisions.md`** that mcp-<name> exists
   and what it wraps, so the next operator knows where the cost
   savings come from.

## Manual pass

```sh
# 1. Confirm MCP is configured
cat <project>/.mcp.json | jq '.mcpServers | keys'

# 2. Scaffold the agent
yakos agent new mcp-linter \
    --domain tool-call \
    --model cheap \
    --tools "lint_files" \
    --project <project>

# 3. Edit the body to be a thin wrapper (Purpose + Execution sections
#    above)
$EDITOR <project>/.claude/agents/mcp-linter.md

# 4. Verify
yakos agents lint
yakos dispatch mcp-linter "lint pkg/auth/*.go" --project <project>
```

## Known gotchas

- **Tool name vs server name.** The agent's `tools:` array must
  list the exact tool name(s) the MCP server exposes — not the
  server name. Mismatch produces "tool not found" at dispatch.
- **Model for routing.** The LLM that routes the task to the MCP
  tool is small (haiku / gpt-5-nano / gemini-2.5-flash). Don't
  default to opus — the savings vs. a regular agent disappear.
- **Hooks don't fire on MCP calls.** yakOS's PreToolUse hooks
  aren't invoked when an LLM agent calls an MCP tool (those are
  inside the runtime's tool-call path, not yakOS's). Defense in
  depth has to come from the MCP server itself.
- **Output format.** Some MCPs return JSON; others return prose.
  The agent body should not parse — return verbatim. The caller
  decides what to do with the structure.

## Cost contract

A typical MCP-as-agent call costs ~50–200 input tokens + ~20–100
output tokens for the LLM routing layer, plus the MCP server's
own cost (which yakOS doesn't measure). On claude with `cheap`
alias this is roughly $0.001 per call. Compare to a full
specialist call at $0.10–$0.50.

`yakos cost --by agent` shows the routing-layer cost; the MCP's
internal cost (network calls, compute) needs separate accounting
in your MCP server.

## References

- `cli/lib/dispatch.sh` — the dispatch surface.
- `lib/agents/README.md` — agent frontmatter fields.
- `docs/runtime-matrix.md` — which runtimes accept MCP via flag
  (claude `--mcp-config`) vs. inline (codex / gemini).
