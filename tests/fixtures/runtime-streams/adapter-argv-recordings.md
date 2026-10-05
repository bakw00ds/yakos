# Recordings made with the codex and agy adapters' argv (K-133)

Real stdout of the two harnesses, recorded with the argv the Go adapters build
(codex-cli 0.154.0 and agy 1.2.17, ChatGPT and Antigravity logins, 2026-10-05).
They pin what the adapters rely on: the flags are accepted, resume echoes the
session id, a delegated subagent runs, the sandbox denies a write outside the
workspace, and the failure modes look the way the adapters expect. Stream
parsers should also read the other recordings in this directory.

## codex 0.154.0

| File | Command (the prompt is the only variable part) |
|---|---|
| `codex-exec-json-0.154.0.ndjson` | `codex exec --json --sandbox workspace-write -c 'approval_policy="never"' -- "Reply with the single word ok."` |
| `codex-exec-json-0.154.0-resume.ndjson` | `codex exec resume --json -c 'sandbox_mode="workspace-write"' -c 'approval_policy="never"' -- <thread_id> "<prompt>"`, resuming the thread recorded in the first file |
| `codex-exec-json-0.154.0-subagent.ndjson` | the first command with the framed prompt `Delegate this task to subagent named 'probe'. Use the agent's discipline and report only the final result.` in a git repository holding `.codex/agents/yakos-probe.toml` (written by the bash emitter); the subagent's `developer_instructions` demanded a fixed token and the final message is that token |
| `codex-exec-json-0.154.0-auth-failure.ndjson` | the first command with an empty `CODEX_HOME` (no login); the requests are rejected with 401, so no model call is made |

- `exec resume` reports the resumed `thread_id` in `thread.started`, so the id
  captured from a first run is the one to pass again.
- A framed delegation appears as `collab_tool_call` items (`tool`,
  `sender_thread_id`, `receiver_thread_ids`, `agents_states`, `status`) around
  the final `agent_message`; usage is for the whole tree of threads.
- A run with no login emits `error` events (`Reconnecting... n/5`), an
  `item.completed` of type `error` (transport fallback), then `turn.failed` with
  `error.message`, no `turn.completed`, and exits 1. Tracing log lines and
  `Reading additional input from stdin...` go to stderr.

## agy 1.2.17

Each line is `{"event":"init|step_update|result", "<event>":{...}}`. All of these
used `--output-format stream-json --sandbox --dangerously-skip-permissions
--print-timeout 60s` from a scratch directory.

| File | Command (the prompt is the only variable part) |
|---|---|
| `agy-stream-json-1.2.17-conversation-turn1.ndjson` | `agy -p "reply with the single word ok" --effort high ...` (no `--model`) |
| `agy-stream-json-1.2.17-conversation-turn2.ndjson` | `agy -p "reply with the single word again" --effort high --conversation <conversation_id of turn 1> ...` |
| `agy-stream-json-1.2.17-effort-conflict.ndjson` | `agy -p "reply with the single word ok" --model gemini-3.8-flash-low --effort high ...` |
| `agy-stream-json-1.2.17-sandbox-denied.ndjson` | `agy -p "<run sh -c 'echo x > \"$HOME/p0b-agy-probe.txt\"' once and report its exit code>" --model gemini-3.8-flash-low ...` |
| `agy-stream-json-1.2.17-skill-mention.ndjson` | `agy -p "@yakos-probe hello" --model gemini-3.8-flash-low ...` in a git repository holding `.agents/skills/yakos-probe/SKILL.md` written by the Go materializer; the skill says to answer every message with a fixed token |

- **Resume.** The second turn keeps the `conversation_id`, continues `step_index`
  (turn 1 used 0-1, turn 2 uses 2-4, including a `system_message` step), and its
  `result` reports `num_turns: 2` with usage that is **cumulative** across both
  turns (turn 2's own usage is on its `agent_response` step). The two turns were
  separate processes, so the running total survives a process restart: turn 2's
  `result.usage` is turn 1's result plus turn 2's own step usage, field by field.
  A parser that accounts per run must not take `result.usage` of a resumed turn
  as that turn's usage: use the sum of the DONE step usage seen in the run's own
  stream (equal to the turn's own usage, and to `result.usage` on a first turn),
  or subtract the previous total for the `conversation_id`.
- **Effort and model ids.** agy ids carry their effort as a suffix
  (`gemini-3.8-flash-low`). Combined with `--effort` they are rejected: the
  process exits 1 and the only stdout line is a `result` with `status: "ERROR"`,
  an `error` message (`invalid model selection ... conflicts with --effort=high`),
  an empty `conversation_id`, zero usage and no `init` or step events. `--effort`
  with no `--model` works, and `init` then has no `model` key (the turn files),
  but only for the levels the default model offers: agy takes `low`, `medium` and
  `high` and rejects `xhigh` and `max` (`invalid model selection (--model ""
  --effort "max"): gemini-3.8-flash has no "max" effort (available: low, medium,
  high)`; observed in review of this change, not recorded as a file). A bare base
  id with no suffix needs `--effort`: `--model gemini-3.1-pro` alone exits 1 with
  `requires --effort (available: low, high)`. The adapter sends `xhigh` and `max`
  as `high`.
- **Sandbox.** With `--sandbox --dangerously-skip-permissions` (`init.permission_mode`
  is `always-proceed`) one write outside the workspace, made through the
  `run_command` tool, failed with `Operation not permitted` and exit code 1, and
  no file was created. The recording shows only that denied write; the prompt
  told the model not to retry. K-158 (2026-10-05) probed the rest:
  Under `--sandbox --dangerously-skip-permissions`, agy's macOS Seatbelt sandbox
  blocks writes outside the workspace by default but leaves file reads and
  outbound network unrestricted, and the model can escalate out of the sandbox
  at will via `run_command(BypassSandbox=true)`, which
  `--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not
  a containment boundary for reads, network or writes and must only receive
  non-sensitive work or run inside an external OS sandbox (K-159).

- **Skill discovery and the `@yakos-<id>` mention.** In a repository holding the
  generated `.agents/skills/yakos-probe/SKILL.md`, `agy -p "/skills"` lists it
  (`name: yakos-probe`, `model_invocable: true`) next to agy's built-in and plugin
  skills. `@yakos-probe hello` then makes the model read the file with a
  `view_file` tool step and answer with the token the skill demands, so the
  mention works in print mode. The skill reaches the model by that tool call, not
  by prompt injection.
- **Slash commands in print mode** are handled by agy itself and make no model
  call. `-p "/skills"` prints two lines: `{"event":"command_result","command":{...}}`
  (the payload key is `command`, not `command_result`) and an `event: result`
  whose `result` has `status: SUCCESS`, an empty `conversation_id`, `num_turns: 0`,
  zero usage, the listing text in `response`, and a `command` object
  (`name`, `data`). No `init` event, no steps. It is not recorded here because the
  listing includes the user's installed plugins.

## Provenance and redaction

The codex files are unedited. The agy files have two edits: `init.cwd` (and, in
`skill-mention`, the `view_file` path) is rewritten to `/work/project` (it was the
scratch directory) and, in `sandbox-denied`, the home directory in the error
message is `/Users/user`. The
files contain thread and conversation ids, token counts, tool names and, for the
codex auth failure, the request ids of rejected unauthenticated requests. Nothing
else identifying; checked for credentials, e-mail addresses and the user name.
