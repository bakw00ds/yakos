# internal/serve — yakos daemon

`serve` is the Phase 2 daemon package. It listens on a JSON-RPC 2.0 socket
(Unix domain socket on Linux/macOS; named pipe on Windows) and exposes yakOS
operations as namespaced RPC methods.

## Starting the daemon

```
yakos serve [--socket <path>] [--pidfile <path>]
```

The daemon is **off by default** (per decision Q1). Enable via:

```
export YAKOS_DAEMON=on
yakos serve &        # foreground with backgrounding
```

Or for graceful fallback mode:

```
export YAKOS_DAEMON=auto   # uses daemon if running; falls back to in-process
```

## Socket location

| Platform | Default path |
|----------|-------------|
| Linux | `$XDG_RUNTIME_DIR/yakos/<hash>.sock` (fallback: `/tmp/yakos-<uid>/<hash>.sock`) |
| macOS | `$TMPDIR/yakos/<hash>.sock` |
| Windows | `\\.\pipe\yakos-<uid>-<hash>` |

`<hash>` = first 16 hex chars of SHA-256 of the absolute workspace root path.
Stable per workspace; no collisions in practice.

## RPC methods (Phase 2 foundation — 3 methods)

| Method | Params | Returns | Idempotent |
|--------|--------|---------|------------|
| `yakos.version` | none | `{version: string}` | Yes |
| `yakos.kanban.summary` | none | `{summary, todo, in_progress, done}` | Yes |
| `yakos.dispatch.run` | `{agent, task, project?, runtime?, model?, timeout?}` | `{exit_code, duration_s, output_bytes, model_resolved}` | No (each call spawns) |

All methods follow JSON-RPC 2.0. Error codes:

| Code | Meaning |
|------|---------|
| -32700 | Parse error |
| -32600 | Invalid request |
| -32601 | Method not found |
| -32602 | Invalid params |
| -32603 | Internal error |
| -32000 | Workspace mismatch |
| -32001 | Concurrent write conflict |
| -32002 | Dispatch runtime not available |
| -32003 | State-file corrupt |

## Debugging with socat (Unix)

```
socat - UNIX-CONNECT:/tmp/yakos/<hash>.sock
{"jsonrpc":"2.0","id":1,"method":"yakos.version"}
```

## PID file

Written at daemon start; removed on clean shutdown. Path mirrors the socket
path with `.pid` extension. A stale PID file from an unclean shutdown is
detected via `kill(pid, 0)` on POSIX and `tasklist` on Windows; if stale,
it is removed and the new daemon starts normally.

## Follow-up work

The three foundation methods prove the end-to-end RPC surface. Subsequent
Phase 2 dispatches add:

- `yakos.kanban.list / .add / .move / .done / .blocker`
- `yakos.dispatch.status / .cancel`
- `yakos.workdir.current / .archive`
- `yakos.supervise.run / .stream`
- `yakos.refresh.run`
- `yakos.system.ping / .shutdown`
