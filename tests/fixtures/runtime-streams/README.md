# runtime-streams fixtures

Recorded or documented stdout captures of the harnesses yakOS drives, used by
the `LineParser` goldens in `cli-go/internal/runtime` (K-135) and by the
dispatch, MCP, JSON-RPC and Flows tests that replay them through a fake
binary. A filename says how trustworthy a capture is:

| Marker in the name | Meaning |
|---|---|
| none (`codex-exec-json-<version>-<scenario>.ndjson`, `agy-stream-json-<version>-<scenario>.ndjson`) | **Real.** Recorded from that harness version under the operator's login. Not edited, except where the README says a field was redacted. |
| `SYNTHETIC` | Hand-written, or copied from the vendor's documentation, for a case that was not recorded live. Treat it as a statement of intent, not evidence of the wire format. |

## Files

| File | Provenance | Covers |
|---|---|---|
| `codex-exec-json-0.154.0-ok.ndjson` | real, codex 0.154.0 | one agent message, usage with cached tokens |
| `codex-exec-json-0.154.0-command.ndjson` | real, codex 0.154.0 | narration, a `command_execution` item (started then completed), a final message |
| `codex-exec-json-0.154.0-failed.ndjson` | real, codex 0.154.0 | `-m` with an unsupported model: a warning `error` item, a top-level `error` event whose message is a JSON document, `turn.failed`, no usage |
| `codex-exec-json-0.154.0-SYNTHETIC-items.ndjson` | synthetic | `reasoning`, `file_change`, a failing command, `mcp_tool_call`, `web_search`, `todo_list` items and cache-write usage |
| `agy-stream-json-1.2.17-ok.ndjson` | real, agy 1.2.17, `gemini-3.8-flash-low` | `init` with the model id, an ACTIVE text fragment then a DONE step carrying the trailing newline, `result` with usage |
| `agy-stream-json-1.2.17-tool.ndjson` | real, agy 1.2.17, `gemini-3.8-flash-low` | an `agent_response` step with usage and no text (the tool call), a `run_command` tool step seen ACTIVE then DONE, the answer, `result` usage equal to the two model steps' sum |
| `agy-stream-json-1.2.17-conversation-turn1.ndjson` | real, agy 1.2.17, recorded by wp-p0b (K-133, copied unchanged from `feat/routing-p0b-adapters`) | first turn of a conversation: a reply with usage |
| `agy-stream-json-1.2.17-conversation-turn2.ndjson` | real, agy 1.2.17, recorded by wp-p0b (same source) | second turn with `--conversation`: a `system_message` step, and a result whose usage is **cumulative** (turn 1's 12859 input plus its own 13091, `num_turns` 2) |
| `agy-stream-json-1.2.17-effort-conflict.ndjson` | real, agy 1.2.17, recorded by wp-p0b (same source) | a run refused before it started: a lone `result` frame with status `ERROR`, an `error` message, an empty `conversation_id`, no init and no steps |
| `agy-stream-json-1.2.17-sandbox-denied.ndjson` | real, agy 1.2.17, recorded by wp-p0b (same source) | a tool step whose command the sandbox refused: no `error` object, the refusal is in the output |
| `agy-stream-json-1.2.17-SYNTHETIC-checkpoint.ndjson` | vendor example | a `checkpoint` step with usage, text delivered in one DONE step |
| `agy-stream-json-1.2.17-SYNTHETIC-multiturn.ndjson` | vendor example | two turns in one process (`--input-format stream-json`): two result frames, the second totalling both turns (`num_turns` 2), `init.model` |
| `agy-stream-json-1.2.17-SYNTHETIC-tool-error.ndjson` | vendor example + invention | a tool step that failed (`tool_info.error`); the successful step is the vendor's |
| `agy-json-1.2.17-SYNTHETIC-envelope.ndjson` | vendor example | the `--output-format json` single envelope |
| `claude-stream-json-oneshot-SYNTHETIC.ndjson` | synthetic | a framed one-shot run (no partial messages): init, thinking, text, `tool_use`, an error `tool_result`, final text, result with cache usage |
| `claude-stream-json-error-SYNTHETIC.ndjson` | synthetic | a run that failed before answering (`is_error` result) |
| `claude-stream-json-subagent-SYNTHETIC.ndjson` | synthetic | a framed run with the sub-agent's text forwarded (`CLAUDE_CODE_FORWARD_SUBAGENT_TEXT`): assistant lines carrying `parent_tool_use_id`, the sub-agent's tool calls, the relay's lead-in and final report, a result frame |

The vendor examples are from <https://antigravity.google/docs/cli/headless/>.
The older partial-messages claude fixtures live in
`cli-go/internal/runtime/testdata/`.

## Redaction

The two agy files recorded for this folder (`-ok`, `-tool`) had `init.cwd` (the
scratch directory the run happened in, a local path) rewritten to
`/work/project`. Nothing else was changed. The four agy files from wp-p0b are
copied byte for byte from its branch, where `cwd` and the home directory in the
sandbox output were already neutral. None of them contains an account
identifier, token or credential, which was checked before they were committed;
they do contain conversation ids and the harness's tool list. The codex files
are unedited and contain thread ids and token counts only.

## Usage conventions the goldens pin

Every parser reports tokens in the Anthropic convention (see `runtime.Usage`):
`input_tokens` is the fresh, uncached prompt only.

- codex reports `input_tokens` as the whole prompt with `cached_input_tokens`
  inside it (verified against the recordings above: a two-request turn reported
  30394 input of which 27392 cached). The parser subtracts.
- agy reports `input_tokens` without `cache_read_tokens` (the vendor's second
  turn shows 278 input beside 30214 cache read) and `output_tokens` including
  `thinking_tokens`. Nothing to adjust. The terminal `result` usage equals the
  sum of the DONE steps' usage for a first turn; `agy-stream-json-1.2.17-tool.ndjson`
  shows it (12870 + 13088 input, 127 + 1 output).
- agy's `result` usage is **cumulative over the conversation**, not per turn
  (`num_turns` counts the conversation's turns). In the recorded
  `conversation-turn1/turn2` pair, turn 2 reports 25950 input and 719 output:
  turn 1's 12859 and 26 plus its own 13091 and 693. `ParseResult.Usage` is
  therefore the sum of the usage carried by the DONE steps in the stream (13091
  and 693 for turn 2, equal to the result frame on every first-turn recording),
  and the frame's total is exposed as `ParseResult.CumulativeUsage`. The
  dispatch layer reports the total when the harness was not handed a native
  session to continue and the step sum when it was, so adding runs up needs no
  subtraction. The single
  envelope has no steps: its `Usage` is zero and only the total is exposed. The
  frame's `duration_seconds` is the session's clock (turn 2 reports 35.9 s for a
  step of about 4 s), so `Usage.DurationMs` is left zero after the first turn.

## Claude text

`ParseResult.Text` for claude is the result frame's final text; the relay's
lead-in and a sub-agent's narration are not part of it (see
`claude-stream-json-oneshot-SYNTHETIC.ndjson` and
`claude-stream-json-subagent-SYNTHETIC.ndjson`). `TextAll` keeps every assistant
text block for a terminal. Without a result frame (a killed run) `Text` falls
back to the top-level assistant text.

## Model ids

agy reports the model it ran in `init.model` verbatim, effort suffix included
(`gemini-3.8-flash-low`); `ParseResult.ModelID` carries it unchanged. Codex
streams carry no model id.

## Re-recording

```sh
cd "$(mktemp -d)"   # never inside a worktree
codex exec --json --skip-git-repo-check --ephemeral --sandbox read-only \
  --color never "reply with the single word ok" > ok.ndjson
agy -p "reply with the single word ok" --output-format stream-json \
  --model gemini-3.8-flash-low --sandbox --dangerously-skip-permissions \
  --print-timeout 60s > ok.ndjson
```

Keep the harness version in the filename and redact `cwd`. Change the golden
expectations in the same commit that changes a fixture.
