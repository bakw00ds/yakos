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
| `yakos.dispatch` | `{agent, task, project?, runtime?, model?, timeout?}` | `{text, scan, exit_code, duration_s, output_bytes, runtime, model_resolved, model_id?, provider?, session_id?, usage?, text_truncated?, error?}` (see below) | No |
| `yakos.kanban.list` | `{column?, limit?}` | `{items:[{id, title, column}]}` | Yes |
| `yakos.kanban.add` | `{title, category?, notes?}` | `{id}` | Conditionally |
| `yakos.kanban.move` | `{id, to}` | `{ok:bool}` | Yes |
| `yakos.kanban.done` | `{id}` | `{ok:bool}` | Yes |
| `yakos.refresh` | `{dryRun?}` | text report | Yes when dryRun=true |
| `yakos.supervise.run` | `{project?, scope?}` | findings text | Yes |
| `yakos.supervise.ack` | `{finding_id, project?, note?}` | confirmation text | Yes |

### `yakos.dispatch` result

The result is the agent's **text**, not the runtime's raw stdout (claude
stream-json, codex JSONL and agy stream-json are parsed by
`internal/runtime`'s `LineParser`; any other runtime's prose is passed
through). It is the same object `yakos.dispatch.run` returns over JSON-RPC
(`dispatch.TransportSummary`).

| Field | Meaning |
|-------|---------|
| `text` | The agent's answer, at most 64 KiB (marker included). For claude it is the final text of the stream's result frame, not the relay's lead-in or a sub-agent's narration; the full join of every assistant message is deliberately not a field of the result. **Untrusted model output.** |
| `text_truncated` | Present and true when `text` is incomplete (cut at 64 KiB here, or at the parser's 1 MiB cap). |
| `scan` | Injection patterns the Go `output-injection-scan` found in `text`; `[]` when clean. Detection only: the text is returned either way. |
| `exit_code`, `duration_s`, `output_bytes` | As before. `output_bytes` is the size of the raw capture, not of `text`. A non-zero `exit_code` is not a tool error. |
| `runtime`, `provider`, `model_resolved`, `model_id` | The runtime that ran, its provider (anthropic, openai, google), the requested tier, and the concrete model id when the stream reported one. |
| `session_id` | The runtime's own session id (claude `session_id`, codex `thread_id`, agy `conversation_id`). The tool does not take a resume id yet. |
| `usage` | `{input_tokens, output_tokens, cache_read, cache_creation}`, plus `total_cost_usd` only for claude (the one harness that reports a dollar figure). `input_tokens` is the fresh prompt for every harness; cached tokens are counted separately. The counts are this call's own, so calls can be added up. agy also reports a conversation total, which the result does not carry. Omitted when the runtime reported no usage. |
| `error` | The failure message the runtime reported, if any (for example a codex `turn.failed`). |

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
