# internal/mcpserver — native MCP server (stdio)

`mcpserver` implements the native MCP (Model Context Protocol) server for
yakOS, exposed via `yakos mcp serve`. It speaks JSON-RPC 2.0 over
newline-delimited JSON on stdin/stdout — the MCP stdio transport.

## Invoking

```
yakos mcp serve
```

Add to Claude Code via:

```
claude mcp add yakos -- yakos mcp serve
```

Per decision Q3 (2026-06-02), Phase 2 ships stdio transport only.
Streamable HTTP is a follow-up dispatch.

## Session lifecycle

```
client → {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
server ← {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"yakos","version":"..."}}}
client → {"jsonrpc":"2.0","method":"notifications/initialized"}   (no id; server ignores)
client → {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
server ← {"jsonrpc":"2.0","id":2,"result":{"tools":[...]}}
client → {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"yakos.kanban.list","arguments":{}}}
server ← {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"..."}]}}
```

## Tool surface (Phase 2 first cut — 8 tools)

| Tool | Args | Returns | Idempotent |
|------|------|---------|------------|
| `yakos.dispatch` | `{agent, task, project?, runtime?, model?, timeout?}` | `{exit_code, duration_s, output_bytes, model_resolved}` | No |
| `yakos.kanban.list` | `{column?, limit?}` | `{items:[{id, title, column}]}` | Yes |
| `yakos.kanban.add` | `{title, category?, notes?}` | `{id}` | Conditionally |
| `yakos.kanban.move` | `{id, to}` | `{ok:bool}` | Yes |
| `yakos.kanban.done` | `{id}` | `{ok:bool}` | Yes |
| `yakos.refresh` | `{dryRun?}` | text report | Yes when dryRun=true |
| `yakos.supervise.run` | `{project?, scope?}` | findings text | Yes |
| `yakos.supervise.ack` | `{finding_id, project?, note?}` | confirmation text | Yes |

## Error semantics

- Protocol errors (parse failure, method not found, invalid request):
  JSON-RPC 2.0 error envelope with standard codes (-32700, -32601, etc.)
- Tool execution failures: `ToolsCallResult` with `isError: true`
  (MCP convention; not a protocol error)

## Smoke test

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' | yakos mcp serve
```

Should return:
```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"yakos","version":"..."}}}
```

Tools/list smoke:
```bash
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | yakos mcp serve
```

## Config

The server reads two values from the launching process:

| Source | Used for |
|--------|----------|
| `cfg.WorkspaceRoot` | Kanban path resolution; default dispatch project |
| `cfg.YakosRoot` | Agent composition (dispatch + refresh) |

Both are injected by the `yakos mcp serve` subcommand handler in
`cmd/yakos/main.go`.
