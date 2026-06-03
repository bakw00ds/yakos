# internal/mcpserver — MCP server (stdio + streamable HTTP)

`mcpserver` implements the MCP (Model Context Protocol) server for yakOS.
Two transports are available:

| Transport | Surface | Auth |
|-----------|---------|------|
| **stdio** | `yakos mcp serve` (launched by Claude Code via `claude mcp add`) | none (process isolation) |
| **streamable HTTP** | `yakos serve --mcp-http-addr 127.0.0.1:7894` | Bearer write token |

Both transports share the same tool surface and tool implementations.

## stdio transport

```
yakos mcp serve
```

Add to Claude Code via:

```
claude mcp add yakos -- yakos mcp serve
```

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

## Streamable HTTP transport (Q3 override)

The daemon exposes a streamable HTTP MCP endpoint at `127.0.0.1:7894` by
default (set `--mcp-http-addr -` to disable).

### Protocol

Single `POST /mcp` endpoint. The request body is one or more NDJSON
(newline-delimited JSON) frames; the response is an NDJSON stream with one
response frame per request frame. `Content-Type: application/x-ndjson`.

```
POST /mcp HTTP/1.1
Authorization: Bearer <write-token>
Content-Type: application/x-ndjson

{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
```

Response (chunked, NDJSON):
```
{"jsonrpc":"2.0","id":1,"result":{...}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[...]}}
```

- Notifications (requests without `"id"`) produce no response frame.
- Parse errors produce a single error frame with `"id": null`.
- Each frame is flushed immediately (`http.Flusher`) for low-latency streaming.

### Auth

`Authorization: Bearer <write-token>` header. The write token is the same as
the REST API write token (`~/.yakos-state/rest-write-token`). Missing or
wrong token returns HTTP 401. No read-token path: all MCP tool calls are
treated as write-level operations.

### Rate limiting

Inherits the daemon's default rate-limit class. No per-tool rate limiting.

### Client usage

```go
client := &mcpserver.StreamHTTPClient{
    BaseURL:    "http://127.0.0.1:7894",
    Token:      writeToken,
    HTTPClient: http.DefaultClient,
}
resp, err := client.Call(ctx, "tools/list", 1, nil)
```

## Config

The server reads two values from the launching process:

| Source | Used for |
|--------|----------|
| `cfg.WorkspaceRoot` | Kanban path resolution; default dispatch project |
| `cfg.YakosRoot` | Agent composition (dispatch + refresh) |

Both are injected by the `yakos mcp serve` subcommand handler and by the
daemon's `serve.Run` for the HTTP transport.
