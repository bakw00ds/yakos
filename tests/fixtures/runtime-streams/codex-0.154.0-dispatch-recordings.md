# codex 0.154.0 recordings for the sandboxed dispatch adapter (K-133)

Real, unedited stdout of `codex exec --json` from codex-cli 0.154.0 (ChatGPT
login, 2026-10-05), recorded with the argv the Go adapter builds. They pin
what the adapter relies on: the flags are accepted, `exec resume` echoes the
thread id, a delegated subagent runs, and an unauthenticated run fails cleanly.
Stream parsers should also read the other recordings in this directory.

| File | Command (the prompt is the only variable part) |
|---|---|
| `codex-exec-json-0.154.0.ndjson` | `codex exec --json --sandbox workspace-write -c 'approval_policy="never"' -- "Reply with the single word ok."` |
| `codex-exec-json-0.154.0-resume.ndjson` | `codex exec resume --json -c 'sandbox_mode="workspace-write"' -c 'approval_policy="never"' -- <thread_id> "<prompt>"`, resuming the thread recorded in the first file |
| `codex-exec-json-0.154.0-subagent.ndjson` | the first command with the framed prompt `Delegate this task to subagent named 'probe'. Use the agent's discipline and report only the final result.` in a git repository holding `.codex/agents/yakos-probe.toml` (written by the bash emitter); the subagent's `developer_instructions` demanded a fixed token and the final message is that token |
| `codex-exec-json-0.154.0-auth-failure.ndjson` | the first command with an empty `CODEX_HOME` (no login); the requests are rejected with 401, so no model call is made |

## What these show that the other recordings do not

- `exec resume` reports the resumed `thread_id` in `thread.started`, so the id
  captured from a first run is the one to pass again.
- A framed delegation appears as `collab_tool_call` items (`tool`,
  `sender_thread_id`, `receiver_thread_ids`, `agents_states`, `status`) around
  the final `agent_message`; usage is for the whole tree of threads.
- A run with no login emits `error` events (`Reconnecting... n/5`), an
  `item.completed` of type `error` (transport fallback), then `turn.failed` with
  `error.message`, no `turn.completed`, and exits 1. Tracing log lines and
  `Reading additional input from stdin...` go to stderr.

The files contain a thread id, token counts, and (auth failure) the request ids
of rejected unauthenticated requests. The working directory was a scratch
repository and appears in no event.
