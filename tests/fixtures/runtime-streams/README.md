# runtime-streams fixtures

Recorded or documented stdout captures of the harnesses yakOS drives, used by
the `LineParser` goldens in `cli-go/internal/runtime` (K-135) and by the
dispatch, MCP, JSON-RPC and Flows tests that replay them through a fake
binary. A filename says how trustworthy a capture is:

| Marker in the name | Meaning |
|---|---|
| `codex-exec-json-<version>-<scenario>.ndjson` (no marker) | **Real.** Recorded from that codex version with `codex exec --json --skip-git-repo-check --ephemeral --sandbox read-only` (ChatGPT login). Not edited. |
| `SYNTHETIC` | Hand-written from the harness's documented event shapes for a case that was not recorded live. Treat it as a statement of intent, not evidence of the wire format. |
| `SYNTHETIC-PENDING-SIGN-IN` | agy only. `agy` was not signed in on the build machine, so nothing was recorded. The events are the vendor's own examples from <https://antigravity.google/docs/cli/headless/> (agy 1.2.17), copied verbatim or minimally extended. **Re-record after an interactive `agy` sign-in** and drop the marker. |

## Files

| File | Provenance | Covers |
|---|---|---|
| `codex-exec-json-0.154.0-ok.ndjson` | real, codex 0.154.0 | one agent message, usage with cached tokens |
| `codex-exec-json-0.154.0-command.ndjson` | real, codex 0.154.0 | narration, a `command_execution` item (started then completed), a final message |
| `codex-exec-json-0.154.0-failed.ndjson` | real, codex 0.154.0 | `-m` with an unsupported model: a warning `error` item, a top-level `error` event whose message is a JSON document, `turn.failed`, no usage |
| `codex-exec-json-0.154.0-SYNTHETIC-items.ndjson` | synthetic | `reasoning`, `file_change`, a failing command, `mcp_tool_call`, `web_search`, `todo_list` items and cache-write usage |
| `claude-stream-json-oneshot-SYNTHETIC.ndjson` | synthetic | a framed one-shot run (no partial messages): init, thinking, text, `tool_use`, an error `tool_result`, final text, result with cache usage |
| `claude-stream-json-error-SYNTHETIC.ndjson` | synthetic | a run that failed before answering (`is_error` result) |
| `agy-stream-json-1.2.17-SYNTHETIC-PENDING-SIGN-IN.ndjson` | vendor example | single turn: init, steps, result |
| `agy-stream-json-1.2.17-SYNTHETIC-PENDING-SIGN-IN-multiturn.ndjson` | vendor example | ACTIVE/DONE text fragments, two turns in one process (cumulative usage), `init.model` |
| `agy-stream-json-1.2.17-SYNTHETIC-PENDING-SIGN-IN-tool.ndjson` | vendor example + extension | tool steps (success from the vendor, failure invented) |
| `agy-json-1.2.17-SYNTHETIC-PENDING-SIGN-IN.ndjson` | vendor example | the `--output-format json` single envelope |

The older partial-messages claude fixtures live in
`cli-go/internal/runtime/testdata/`.

## Usage conventions the goldens pin

Every parser reports tokens in the Anthropic convention (see `runtime.Usage`):
`input_tokens` is the fresh, uncached prompt only.

- codex reports `input_tokens` as the whole prompt with `cached_input_tokens`
  inside it (verified against the recordings above: a two-request turn reported
  30394 input of which 27392 cached). The parser subtracts.
- agy reports `input_tokens` without `cache_read_tokens` (the vendor's second
  turn shows 278 input beside 30214 cache read) and `output_tokens` including
  `thinking_tokens`. Nothing to adjust.

## Re-recording

```sh
cd "$(mktemp -d)"
codex exec --json --skip-git-repo-check --ephemeral --sandbox read-only \
  --color never "reply with the single word ok" > ok.ndjson
```

Keep the codex version in the filename. Change the golden expectations in the
same commit that changes a fixture.
