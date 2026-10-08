# Runtime support matrix

yakOS v0.4 introduced runtime adapters so the framework can launch
sessions on multiple agentic CLIs. This document tracks which features
each adapter supports, what gets soft-degraded, and the operator-facing
trade-offs.

Last updated: 2026-10-05 (K-133/K-134: Go adapters for codex 0.154.0 and
agy 1.2.x; codex runs in its OS sandbox and agy gets `--sandbox`, which K-158
measured not to be containment; the capability matrix drops gemini, whose shim
was removed on 2026-09-01 in favor of agy).

## Capability matrix

| Capability | claude | codex (0.154.0) | agy (1.2.x) |
|---|---|---|---|
| Adapter shipping | v0.3 (always) | v0.4.0 | with the gemini shim's replacement (see CHANGELOG) |
| `inline-agents` (CLI-flag JSON injection) | ✅ `--agents` | ❌ file-based only | ❌ file-based only |
| `path-allowlist-hard` | ✅ `--add-dir` | ✅ the sandbox workspace is the working directory | ⚠ `--add-dir` sets the workspace, but reads and network are not restricted (K-158) |
| `hooks` | ✅ 7 events | ⚠ manual install, 2 of 24 hooks ported | ⚠ manual install, 2 of 24 hooks ported |
| `mcp-flag` (CLI flag) | ✅ `--mcp-config` | ❌ via `config.toml` | ❌ via `.agents/mcp_config.json` |
| `system-prompt-flag` | ✅ `--append-system-prompt` | ❌ no flag; `-c developer_instructions="..."` works (verified) | ❌ no flag; persona prepended to the prompt |
| Model flag | ✅ `--model <tier>` | ✅ `-m <id>` | ✅ `--model <id>` |
| Reasoning effort | ✅ `--effort` | ✅ `-c model_reasoning_effort="..."` (low..max) | ⚠ the model id carries it (`-low`/`-medium`/`-high`); `--effort` only without a suffixed id, and only `low`/`medium`/`high` (`xhigh` and `max` are sent as `high`) |
| `fork-headless` | ✅ `--fork-session` | ✅ `codex fork` | ⚠ unverified — interactive only |
| Non-interactive print mode | ✅ `claude -p` | ✅ `codex exec` | ✅ `agy -p` |
| Machine-readable stream | ✅ `--output-format stream-json` | ✅ `exec --json` (JSONL) | ✅ `--output-format stream-json` (NDJSON, `event` key; recorded) |
| Headless resume | ✅ `--resume <id>` | ✅ `codex exec resume <thread_id>` | ✅ `--conversation <id>` |
| `yakos dispatch` implementation (K-143) | ✅ Go by default | ✅ Go by default | ✅ Go by default |
| Sandbox flag (K-133) | n/a (permission mode) | ✅ `--sandbox workspace-write`: an OS sandbox, network off by default | ⚠ `--sandbox` blocks writes outside the workspace by default only; not a containment boundary (K-158, below) |
| Agent file yakOS writes | (none — JSON injection) | `.codex/agents/yakos-<id>.toml` | `.agents/skills/yakos-<id>/SKILL.md` |
| Output scan (K-146) | ✅ the `output-injection-scan` PostToolUse hook, in-session; the dispatch stream is not re-scanned | ⚠ detect-and-report: every normalized `tool_result` and text event is scanned in dispatch | ⚠ detect-and-report: every normalized `tool_result` and text event is scanned in dispatch |

✅ = supported. ❌ = not supported (degrade or workaround). ⚠ = partial or unverified.

## Sandbox and approvals (K-133)

codex and agy used to be dispatched with approvals and sandbox switched off. The
Go dispatcher (console, MCP, Flows, JSON-RPC, `YAKOS_IMPL=go yakos dispatch`) now
runs codex in its workspace-write sandbox and passes `--sandbox` to agy, unless
the operator opts out. The two are not alike. codex's sandbox is an OS sandbox
with the network off by default, set by the operator at launch, and the model
cannot change it. agy's is not a containment boundary.

**agy is not contained.** Under `--sandbox --dangerously-skip-permissions`,
agy's macOS Seatbelt sandbox blocks writes outside the workspace by default but
leaves file reads and outbound network unrestricted, and the model can escalate
out of the sandbox at will via `run_command(BypassSandbox=true)`, which
`--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not a
containment boundary for reads, network or writes and must only receive
non-sensitive work or run inside an external OS sandbox (K-159).

| | default | flags | opt-out |
|---|---|---|---|
| codex | sandboxed, cannot prompt | `exec --sandbox workspace-write -c approval_policy="never"`; `exec resume` takes `-c sandbox_mode="workspace-write"` (it has no `--sandbox`) | `--dangerously-bypass-approvals-and-sandbox` |
| agy | `--sandbox` passed; blocks writes outside the workspace by default, not a containment boundary (K-158) | `--sandbox --dangerously-skip-permissions` | `--dangerously-skip-permissions` only |

Opt-out is one file, read only from `~/.yakos-state/router-policy.yml`:

```yaml
allow_unsandboxed_runtimes: [codex, agy]   # runtimes named here run without their sandbox
```

The file must be a regular file owned by you and not group or world writable, and
not a symlink; otherwise it is ignored (one stderr note says why, and
`yakos doctor` reports it). On Windows the owner and mode checks cannot run, so
only the regular-file and not-a-symlink rules apply. A project `.yakos.yml` cannot enable it, and
`YAKOS_DISPATCH_LOG` does not relocate it (a project can set environment
variables for the processes it starts, K-129). When the bypass is active yakOS
prints one stderr line per process, and `yakos doctor` warns.

What codex's `workspace-write` sandbox does on macOS (checked with `codex
sandbox`): commands may write inside the working directory, `$TMPDIR` and
`/tmp`; writes elsewhere fail with "Operation not permitted"; the project's
`.git` directory is read-only, so an agent cannot `git commit` or switch
branches (read-only git commands work); network access is off, so `npm install`,
`go get` and `git push` fail. Network can be switched on in
`$CODEX_HOME/config.toml` with `[sandbox_workspace_write]` `network_access = true`.
Work that needs git writes or the network is what the opt-out is for; the safer
alternative is to have the lead do the commit and push.

What K-158 measured for agy 1.2.17 under `--sandbox --dangerously-skip-permissions`
(2026-10-05; report `work/current/reports/k158-agy-containment-2026-10-05.md`).
Headless agy has no approval surface, which is why `--dangerously-skip-permissions`
stays (`init.permission_mode` is then `always-proceed`).

- A write outside the workspace, made directly or from a subprocess, fails with
  "Operation not permitted" and creates nothing.
- Reads outside the workspace succeed, including a path that is not a temporary
  directory (`/Users/Shared`), and an outbound fetch of `example.com` succeeds.
  Neither needed any escalation.
- A `run_command` the model sent with `BypassSandbox=true` wrote outside the
  workspace with no prompt, because `--dangerously-skip-permissions` auto-approves
  it. agy's own tool text says the standard sandbox has no network and no access
  outside the workspace, and that the bypass needs manual approval. None of that
  holds headless, which is either an agy 1.2.17 regression or stale documentation.
  Re-check after any agy upgrade.
- `--sandbox` without `--dangerously-skip-permissions` fails closed: headless agy
  cannot prompt, so any tool that needs a permission is auto-denied and the turn
  produces nothing. It is not a usable worker mode.

Keep `--sandbox` on, because it still blocks the default write path, but count it
as defence in depth. A scratch worktree does not help for reads or network, and
yakOS hooks do not see agy's own tool calls. Contain agy with an external OS
sandbox around the whole process (K-159), or send it only non-sensitive work.
Listing agy in `allow_unsandboxed_runtimes` removes the default write block and
changes nothing else.

The bash adapters (`cli/lib/runtimes/{codex,agy}.sh`, used by `yakos dispatch`
only under `YAKOS_IMPL=bash`) still run with the bypass flags. The Go
dispatcher became the default for `yakos dispatch` in K-143 (below).

## What yakOS does per-runtime

### claude

- Composes `--agents` JSON via `cli/lib/agents-compose.sh`, exec's
  `claude --add-dir <repo> --permission-mode bypassPermissions
  --agents <json>`.
- No filesystem materialization — JSON is in-memory only for the
  session's lifetime.
- Auto-detects `<project>/.mcp.json` for `--mcp-config`.

### codex (0.154.0)

Framed dispatch (`yakos dispatch`, Flows, MCP) writes the agent as
`<workdir>/.codex/agents/yakos-<id>.toml` (`agentscompose.MaterializeCodexAgent`)
and runs:

```
codex exec --json [-m <id>] [-c model_reasoning_effort="<level>"] \
  --sandbox workspace-write -c approval_policy="never" -- "<framed prompt>"
codex exec resume --json ... -c sandbox_mode="workspace-write" -c approval_policy="never" -- <thread_id> "<framed prompt>"
```

The framed prompt asks codex to delegate to the subagent named `<id>`; codex
finds the agent file from the working directory (verified in a scratch git
repository: a delegated subagent answered with the token its
`developer_instructions` demanded). codex documents project-scoped config as
loading only for projects it trusts and this was not checked for an untrusted
path, so if a delegation seems to ignore the agent, mark the project trusted
(`[projects."<path>"]` `trust_level = "trusted"`) in the `config.toml` of the
`CODEX_HOME` in use. The resume form is `codex exec resume`; the top-level
`codex resume` is the interactive picker. The thread id is the `thread_id` of the
previous run's `thread.started` event.

Chat (console panes) has no agent file. The persona is passed as
`-c developer_instructions="<TOML-quoted persona>"`; codex has no
`--system-prompt` flag, and the old adapter passed one, which made every agent
chat on codex fail. The persona is encoded as a TOML string because codex parses
the value of `-c` as TOML and a prompt that parses as a number or `true` would
otherwise change type. The encoding was checked by round-tripping a persona with
quotes, backslashes, control characters and unicode through `codex debug
prompt-input`. Chat runs in the project directory (`cmd.Dir`), so the project's
rules load as they do in a terminal. The persona travels in argv, so it is limited
to 64 KiB (the largest framework agent is about 7 KiB); a larger one is refused
with a clear error before any process starts, instead of the operating system's
"argument list too long". For codex the limit applies to the persona and to its
TOML-escaped form, because escaping grows it: a quote, a backslash or a newline
becomes two bytes and another control character six, so 64 KiB of quotes would be
a 128 KiB argument, past the 131072 bytes Linux allows for one. agy writes the
persona as it is, so its limit is the raw size.

- The agent file carries a `# yakos-generated:` first line. A file without it is
  yours and is never overwritten; delete the line to take ownership of a
  generated file. An unchanged file is not rewritten.
- `-m` takes a model id from the account's catalog (`codex debug models`:
  `gpt-6-astra`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`; the
  catalog is per login and the full record is in
  `work/current/reports/codex-models-2026-10-05.txt`). codex rejects an id outside
  the catalog with HTTP 400 ("The '<id>' model is not supported when using Codex
  with a ChatGPT account"). The semantic aliases (`cheap`, `balanced`, `best`,
  `reasoning`, `frontier`) are **empty for codex** in
  `lib/settings/model-aliases.json`: an empty value means the harness default and
  no `-m` is passed, because the catalog gives no documented tiers. The dispatch
  default, the Claude tier `sonnet`, is not a codex model and is dropped. An
  unpinned dispatch therefore passes no model; pin an id in an agent's `model:`
  to choose one. `general-codex` pins `balanced`, so it runs on the default.
- The agent file names a model only when the model is not a Claude tier. The bash
  composer resolves `balanced` to the tier `sonnet` on its own, and an emitter that
  wrote it made codex fail the subagent ("its fixed `sonnet` model is not
  supported with this Codex ChatGPT account", observed with codex-cli 0.154.0
  while reviewing this change); with no line the subagent uses the default model.
  The Go and bash emitters apply the same rule, so neither writes a tier into a
  codex or agy file.
- Control characters, DEL and a lone CR in the agent's text are written as
  `\u00XX` escapes (valid TOML), a NUL byte makes yakOS skip the agent's file
  with a note, and leading line breaks of the prompt are dropped so the bash and
  Go composers yield the same file.
- The effort levels `low|medium|high|xhigh|max` pass through to
  `model_reasoning_effort`; whether a model supports one is codex's call (the
  catalog lists `low` to `max` for most entries, `low` to `xhigh` for `gpt-5.5`,
  and `ultra` for three).
- Stream: `exec --json` emits JSONL (`thread.started`, `turn.started`,
  `item.*`, `turn.completed` with `usage`, `turn.failed`). Recordings and the
  commands are in `tests/fixtures/runtime-streams/`. Until the codex stream parser
  lands (K-135) the Chat pane shows the raw lines as one block.
- Auth: `OPENAI_API_KEY`, or the login in the `CODEX_HOME` dispatch uses. See
  Auth model.

### agy (1.2.x)

Framed dispatch writes `<workdir>/.agents/skills/yakos-<id>/SKILL.md` (and a
`.gitignore` containing `*` beside it, so the generated directory never shows in
`git status`) and runs:

```
agy --add-dir <workdir> --sandbox --dangerously-skip-permissions [--model <id>] [--effort <level>] \
  --output-format stream-json [--conversation <id>] -p "@yakos-<id> <task>"
```

- agy 1.2.x loads a workspace skill from `.agents/skills/<name>/SKILL.md`, a
  directory per skill with the skill's `name` equal to the directory name.
  Verified live with agy 1.2.17: in a git repository holding the generated
  `yakos-probe` skill, `agy -p "/skills"` lists it (`model_invocable: true`), and
  `@yakos-probe hello` makes the model read the file with a `view_file` step and
  answer with the token the skill demands. So the framed `@yakos-<id> <task>` form
  works in print mode; the skill reaches the model by that tool call, so it costs
  one extra step. The old flat `yakos-<id>.md` layout is not discovered (from the
  binary's strings and changelog; not tried). Leftover flat files can be
  deleted (the bash `yakos archive` cleanup removes them; the `.gitignore` entry
  `.agents/skills/yakos-*.md` from `yakos init` is harmless). The skill name carries the `yakos-` prefix so the `@yakos-<id>`
  mention resolves by directory or by name.
- Chat has no skill file: agy has no system-prompt flag, so the persona is
  prepended to the user text under a `---` separator.
- `--model` takes an id from `agy models`. On the checked account these are
  `gemini-3.8-flash-{high,medium,low}`, `gemini-3.7-flash-*`, `gemini-3.6-flash-*`,
  `gemini-3.1-pro-{high,low}`, `claude-opus-5-5-{low,medium,high}`,
  `claude-sonnet-5-5-{low,medium,high}` and `gpt-oss-120b-medium`. The aliases
  resolve to `cheap` `gemini-3.8-flash-low`, `balanced` `gemini-3.8-flash-high`,
  `best` `claude-opus-5-5-medium`, `reasoning` `gemini-3.1-pro-high` and
  `frontier` `claude-opus-5-5-high` (Antigravity can front Anthropic models).
  Claude tiers are dropped, as for codex. `general-agy` pins
  `gemini-3.8-flash-high`; its old pin `gemini-3.5` does not exist.
- **The effort is part of the id.** agy rejects `--effort` next to such an id:
  `--model gemini-3.8-flash-low --effort high` exits 1 with "invalid model
  selection ... conflicts with --effort=high" and a stream-json `result` event of
  status ERROR. `--effort` with no `--model` works (it applies to the default
  model). The adapter therefore passes `--effort` only when the resolved id does
  not end in `-low`, `-medium` or `-high`. To change the effort on agy, choose the
  id with the suffix you want.
- **Without a suffixed id, agy takes `low`, `medium` and `high` only.** The console
  and the dispatch layer offer `xhigh` and `max` for every runtime, and agy 1.2.17
  rejects both before any model call (`invalid model selection (--model ""
  --effort "max"): gemini-3.8-flash has no "max" effort (available: low, medium,
  high)`, exit 1). The adapter sends them as `high` and prints one note per level.
  The reverse also holds: a bare base id needs `--effort` (`--model gemini-3.1-pro`
  alone exits 1 with `requires --effort (available: low, high)`). yakOS never
  builds such an id; an operator who pins one chooses an effort that model offers.
- Resume: `--conversation <id>` keeps the `conversation_id`, continues the step
  numbering, and reports usage cumulatively across turns (recorded). The total
  spans separate processes: turn 2's `result.usage` is turn 1's result plus turn
  2's own step usage. After the first turn, account one run from the DONE step
  usage in its own stream (it equals the run's own usage), not from
  `result.usage` of a resumed turn. On the first turn `result.usage` is already
  the run's own usage.
- **Containment.** Under `--sandbox --dangerously-skip-permissions`, agy's macOS
  Seatbelt sandbox blocks writes outside the workspace by default but leaves
  file reads and outbound network unrestricted, and the model can escalate out
  of the sandbox at will via `run_command(BypassSandbox=true)`, which
  `--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not
  a containment boundary for reads, network or writes and must only receive
  non-sensitive work or run inside an external OS sandbox (K-159). Details are
  in Sandbox and approvals, above.
- **Checked live with agy 1.2.17:** the `stream-json` shape
  (`tests/fixtures/runtime-streams/`), the effort conflict, `--effort xhigh|max`
  being rejected, a resumed conversation, skill discovery and the `@yakos-<id>`
  mention in print mode (plain directory and git repository), and, in K-158, the
  containment probes listed above.
- Auth: `agy` signs in once, interactively (browser OAuth into the keychain and
  `~/.gemini/`), or `ANTIGRAVITY_API_KEY` for headless use. yakOS never drives
  or caches that login.

### gemini (removed)

The gemini shim was disabled on 2026-09-01; Gemini CLI stopped serving
individual accounts on 2026-06-18. Use `agy`. `runtime: gemini` in agent
frontmatter still validates, as a deprecation warning.

## Usage fields by harness

A `dispatch_finished` row of the dispatch-log can carry a `usage` object with six
fields: `input_tokens`, `output_tokens`, `cache_read`, `cache_creation`,
`duration_ms` and `total_cost_usd` (`cost.Usage`, aliased as `runtime.Usage`). The
Go dispatcher fills it from the harness's own stdout, through the line parser that
`ParserFor` picks for the runtime. The bash dispatcher fills it from what each
adapter in `cli/lib/runtimes/` writes to `YAKOS_USAGE_OUT`. The two writers read
the same native fields a little differently, and the tables record where.

The Go convention is Anthropic's. `input_tokens` counts only fresh prompt tokens,
those not served from a cache. `cache_read` and `cache_creation` count the cached
remainder, so the whole prompt is `input_tokens + cache_read + cache_creation`.
`output_tokens` includes reasoning tokens. `total_cost_usd` is whatever the harness
itself reported; yakOS never computes a price.

What Go rows contain (`claudeLineParser`, `codexLineParser` and `agyLineParser` in
`cli-go/internal/runtime/`):

| `usage` field | claude (`result` event) | codex (`turn.completed` event) | agy (`step_update` and `result` events) |
|---|---|---|---|
| `input_tokens` | `usage.input_tokens` | `usage.input_tokens` (alias `prompt_tokens`) minus `cached_input_tokens` (alias `cache_read_input_tokens`) minus `cache_write_input_tokens`, never below 0 (`codexUsage.normalize`) | `usage.input_tokens`, which already leaves out the cached part |
| `output_tokens` | `usage.output_tokens` | `usage.output_tokens` (alias `completion_tokens`); `reasoning_output_tokens` is a subset of it and is not added | `usage.output_tokens`, which includes `thinking_tokens` |
| `cache_read` | `usage.cache_read_input_tokens` | `usage.cached_input_tokens` (alias `cache_read_input_tokens`) | `usage.cache_read_tokens` |
| `cache_creation` | `usage.cache_creation_input_tokens` | `usage.cache_write_input_tokens` | not reported, 0 |
| `duration_ms` | `duration_ms` | not reported, 0 | `duration_seconds` times 1000 on a first turn, 0 after it |
| `total_cost_usd` | `total_cost_usd` | not reported, 0 | not reported, 0 |

A codex stream with several `turn.completed` events is summed over them.

What legacy bash rows contain (the usage extraction in `cli/lib/runtimes/claude.sh`,
`codex.sh` and `agy.sh`; the bash writer is unchanged):

| `usage` field | claude | codex | agy |
|---|---|---|---|
| `input_tokens` | `usage.input_tokens` | `usage.input_tokens` (alias `prompt_tokens`): the whole prompt, cached tokens included | an estimate, task bytes divided by 4 |
| `output_tokens` | `usage.output_tokens` | `usage.output_tokens` (alias `completion_tokens`) | an estimate, output bytes divided by 4 |
| `cache_read` | `usage.cache_read_input_tokens` | `usage.cache_read_input_tokens`, a name codex 0.154.0 does not write (it writes `cached_input_tokens`), so 0 | 0 |
| `cache_creation` | `usage.cache_creation_input_tokens` | absent | absent |
| `duration_ms` | `duration_ms` | absent | absent |
| `total_cost_usd` | `total_cost_usd` | absent | absent |

The bash adapters keep only the last `result` event (claude) or the last
`turn.completed` event (codex) of a stream. The bash codex and agy objects also
hold a `total_tokens` key, and the agy one a `source` key
(`estimate-only-agy-no-headless-telemetry`); `cost.Usage` has no field for them and
a reader ignores both. The bash `claude-sdk` and `antigravity-sdk` adapters write
their own SDK shapes, which the tables do not cover. The bash `yakos cost` does not
read `usage` at all: it totals the chars/4 estimates `est_input_tokens` and
`est_output_tokens`.

- **Totals agree across the two conventions, in the readers that add all four
  kinds.** The budget aggregate and the cost views (`yakos cost`, the Performance
  dashboard, the Cost tab) add all four kinds (`input_tokens`, `output_tokens`,
  `cache_read` and `cache_creation`), as `cost.TokenTotals.Total` does. The bash
  codex convention (cached tokens inside `input_tokens`, `cache_read` 0) and the Go
  convention (the fresh remainder in `input_tokens`, the cached part in
  `cache_read`) therefore give the same total for the same run. Two older readers
  add only `input_tokens` and `output_tokens`: the metrics collector's tokens per
  task and the token total `work close` records. For a legacy bash codex row they
  count the whole prompt, and for a Go row of the same run only its fresh part, so
  those two totals differ between the writers.
- **Only the split differs.** claude rows agree between the two writers. A legacy
  bash codex row shows the whole prompt as `input_tokens` and no cache read, so a
  reader that reports the input/cache split (a cache hit rate, say) sees different
  numbers for it than for a Go row of the same run. Nothing rewrites old rows.
- **agy reports a running total.** The `result` event's `usage` sums the whole
  conversation, so on a `--conversation` turn after the first it counts every
  earlier turn again. The parser decides from the event's own `num_turns`
  (`agyFrame.firstTurn`: 1 or less). On a first turn `Usage` is the event's counts
  and its duration. After the first turn `Usage` is the sum of the `usage` of the
  `DONE` steps in this run's own stream, with `duration_ms` 0 because the event's
  duration is the session's, and a stream with no step usage reports no tokens
  (`agyTally.own`). A run that ended without a `result` event keeps the sum of its
  steps. The conversation total is kept apart, as `ParseResult.CumulativeUsage`
  (`Result.CumulativeUsage` in the dispatcher): it is for reference and
  cross-checking, it is not written to the log, and it is never added across runs,
  because adding it would count the earlier turns again. On the Go path the agy
  counts are agy's own. Legacy bash agy rows are estimates, because the bash
  adapter ran agy in plain-text mode, where agy reports no usage.
- **Dollars.** Only claude reports a dollar figure, the `total_cost_usd` of its
  `result` event. codex and agy report none: their `total_cost_usd` is 0 on a Go
  row and absent on a bash row. The figure is spend only for a row whose `billing`
  is `api`, and for a row that predates the `billing` field, which keeps counting so
  that a budget does not reset itself on upgrade; the bash writer never sets
  `billing`. A `subscription` or `local` row never counts as spend. For a
  `subscription` row the Go dispatcher stores 0 in `usage.total_cost_usd` and keeps
  the harness's figure as the row's `api_equivalent_usd`, which is informational.
  Go readers take the dollars of a row from `cost.Event.SpendUSD`; a bash reader
  that reads `usage.total_cost_usd` directly gets the same answer for such a row,
  because the 0 is already there. Tokens are the primary unit; dollars matter only
  for runs billed per API call.

## Output scan over normalized events (K-146)

codex and agy have no PostToolUse hook that fires on tool output, so dispatch
scans the events its parsers already normalize. Every `tool_result` event and
every structured text event passes through `outputinjectionscan` and the
supervisor pre-filter's risk patterns (`dispatch/feedscan.go`), in the streaming
path (live) and the one-shot path (after the run, report-only).

| | claude | codex | agy |
|---|---|---|---|
| Scanned in dispatch | no (its hook scans in-session) | yes | yes |
| Finding written | by the hook | `supervisor-findings.ndjson` + `.supervisor-pending.<session>` | same |
| `yakos supervise pending` lists it | n/a | in a "detected (no ack needed)" section | same |
| `kill_on_critical` | n/a | streaming path only | streaming path only |

- **Detect and report, not a boundary.** The default leaves the run untouched. A
  finding is `overall: WARN` and `recommended_action: review`, always: model and
  tool output must never control the lead's ack gates (both ignore `review`), so
  a hostile page cannot halt dispatch. `severity` is `critical` for the injection
  family and `warn` otherwise. `yakos supervise pending` lists these records in a
  separate "detected (no ack needed)" section: they are not counted as pending and
  have no finding ID. A finding carries static labels only: no event content, tool
  name or path. At most 3 findings per run are written; further distinct ones are
  counted in the ledger's `scan_findings` only. Findings are de-duplicated on
  severity, kind and the label set (a model-chosen count inside a label is
  ignored).
- **`kill_on_critical: true`** in the trusted user policy
  (`~/.yakos-state/supervisor-policy.yml`, same trust bar as the launch-gate
  limits; a project `.yakos.yml` cannot set it) cancels the dispatch on a
  critical finding through the process-group kill. The ledger's
  `dispatch_finished` carries `cancel_reason: kill_on_critical:<label>` and
  `scan_findings`.
- **Bounds.** At most 32 KiB per event (head and tail), scanned in 8 KiB chunks
  with a 500 ms deadline each, and 4 MiB per run. A chunk that overruns skips the
  rest of that event; the third overrun, or the byte budget, switches the feed off
  for the rest of the run. The switch-off is recorded: `scan_off_reason`
  (`budget` or `deadline`) on the ledger's `dispatch_finished` and one `review`
  finding `event-scan-disabled:<reason>`. Budget exhaustion is attacker-reachable
  (enough benign output ahead of a payload turns the feed off); that is a
  documented limit of detect-and-report. Nothing is buffered except the last 256
  bytes of the previous text event, which lets a phrase split across streaming
  fragments (agy `text_delta`) match.
- **Known gaps.** The middle of an event larger than 32 KiB is not scanned, and
  the parser drops everything past 256 KiB first. There is no encoding
  normalisation: newline or NBSP between words, homoglyphs, a zero-width
  character inside a word, short base64 and HTML entities evade the scanner.
  Prose-only runtimes (`Plain` events) are not scanned. An assistant `text`
  event is kill-eligible under `kill_on_critical` like a `tool_result`.
- **Findings need a work directory**, resolved from the dispatch request's
  project path, not the daemon's environment: `<project>/work/current` with
  `YAKOS_INPLACE_WORK=1`, else `$HOME/agent-control/<project name>/work/current`
  (`YAKOS_WORK_DIR` applies only when `YAKOS_PROJECT_NAME` names that same
  project). Without one the scan still counts findings into the ledger. The
  pending and findings files are opened without following links and are written
  only if they are regular files.

## Soft-degrade rules

When the operator passes a flag the chosen runtime can't honor,
`yakos start` prints a NOTE-level warning and proceeds without
that flag. Examples:

- `--ide` is claude-only. On codex/agy, prints
  `NOTE: --ide is claude-specific; ignored for <runtime>.`
- `--bare` is claude-only. Same treatment.
- `--strict-mcp` is claude-only.
- `--continue` works only for claude. codex has session-resumption
  via `codex resume` (different shape).

Hard controls (path-allowlist, secret-scan) that depend on hooks
behave differently per runtime:

- claude: hook stdin/stdout shape documented; yakOS's reference
  hooks under `lib/hooks/` are written against this contract.
- codex: hook surface is similar; hooks need conversion to codex's
  config.toml format. **Out of scope for v0.4.0** — operator can
  install yakOS hooks manually per
  [codex hooks docs](https://developers.openai.com/codex/hooks).

## Auth model

Implemented in [`cli/lib/auth.sh`](../cli/lib/auth.sh) and `internal/auth`. yakOS
NEVER stores or rotates credentials. `yakos auth login <runtime>` shells into the
runtime's own login flow:

- claude: prints `/login` instructions (no headless login flag).
- codex: exec's `codex login` against a yakOS-owned profile, below.
- agy: prints the one-time interactive sign-in steps (`agy`, complete the
  browser OAuth) and the `ANTIGRAVITY_API_KEY` alternative.

`yakos auth status` reports per-runtime CLI presence + auth
configuration without revealing credentials.

### codex: the yakOS-owned `CODEX_HOME`

`yakos auth login codex` creates `~/.yakos-state/codex-home` (mode 0700) and runs
`codex login` with `CODEX_HOME` pointed at it. Once that directory holds an
`auth.json`, dispatch (Go and bash), Go chat and the bash `yakos start` run codex
under it (an inherited `CODEX_HOME` is replaced, with a stderr note) and
your own `~/.codex` login is never touched. Go `yakos start` does not use the
profile yet: it launches the interactive codex with your own `CODEX_HOME`. Until
you run the command nothing changes: dispatch keeps using `$CODEX_HOME` or
`~/.codex`, and `yakos doctor` prints a hint.

The reason is that a yakOS dispatch and your interactive codex would otherwise
share one `auth.json`, and concurrent token refreshes, or a login call that
rewrites the shared file ([openai/codex#48465](https://github.com/openai/codex/issues/48465)),
can sign you out of one of them. For the same reason yakOS never calls the codex
app-server `account/login` method, and it runs only the official `codex login`.
The profile's `config.toml` is separate from `~/.codex/config.toml`: put settings
you want for yakOS dispatches (for example `[sandbox_workspace_write]`) there.
`yakos auth logout codex` signs out of the yakOS profile only.

The profile location is always `$HOME/.yakos-state/codex-home`, not
`YAKOS_DISPATCH_LOG`'s directory: a `CODEX_HOME` carries a `config.toml` (notify
commands, MCP servers), so a project must not be able to point codex at one.

## Mixed-runtime dispatch (v0.4.2, planned)

In v0.4.2, agent frontmatter gains a `runtime:` field:

```yaml
---
id: backend
runtime: codex          # default: claude
model: o4-mini
---
```

`yakos dispatch <agent-name> "<task>"` reads the field, spawns the
right CLI in non-interactive mode, captures the output, and returns
to the caller. The lead (in any runtime) calls this via Bash. This
lets a project mix runtimes per-agent — e.g., orchestration on
claude, code-review on codex, doc-writing on agy.

### Go dispatch now honors `runtime:`

On every Go transport (daemon, MCP, console chat, Flows, `YAKOS_IMPL=go`
CLI) the runtime is picked in this order, highest first, the same as
`cli/lib/dispatch.sh`:

1. An explicit runtime: `yakos dispatch --runtime`, `Params.Runtime` (MCP,
   JSON-RPC, REST), or a console pane set to a specific runtime.
2. The agent's `runtime:` frontmatter.
3. `.yakos.yml` `per-domain.<agent domain>`.
4. `.yakos.yml` `default-runtime`.
5. `YAKOS_RUNTIME` (CLI one-shot path only; the daemon never reads it).
6. `~/.yakos-state/default-runtime`.
7. `claude`.

A bare agent name that is itself a runtime (`yakos dispatch codex "..."`)
selects that runtime when the agent has no pin of its own, and counts as
explicit. Agent files named after a runtime (`claude.md`, `codex.md`,
`agy.md`) are skipped with a warning, and `yakos validate` rejects them,
because one would shadow that runtime's own agent.

For a pin or a project default the candidate chain is the chosen runtime,
then the agent's `runtime-fallback`, then `.yakos.yml` `default-fallback`.
The first candidate whose CLI is on PATH and that looks signed in wins:

| Runtime | Looks signed in when |
|---|---|
| claude | The CLI is installed (credentials can live in the keychain or env, so they are not probed). |
| codex | `OPENAI_API_KEY` is set, or `$CODEX_HOME/auth.json` exists. |
| agy | `ANTIGRAVITY_API_KEY` or `GEMINI_API_KEY` is set, a yakos keyring entry exists, or `~/.gemini/antigravity-cli/` exists. |

If nothing passes, dispatch fails fast naming each skipped runtime and why
(e.g. `agy: not signed in; run: yakos auth login agy`). A fallback prints
one line on stderr and is recorded in the dispatch-log (`runtime_chosen_by`,
`fallback_from`). A long-lived daemon reuses the probe's answer for 30 seconds
(5 seconds for a runtime that could not run, so a retry right after a login is
not told the old answer for long), and the agy keyring lookup in it is bounded
to 2 seconds and ends when the dispatch is cancelled. `gemini` is no longer a Go runtime; use
`agy`. Upgrade impact: [UPGRADING.md](../UPGRADING.md).

#### A runtime you name does not fall back

A runtime the operator names (rule 1, or a runtime name used as the agent) is
operator intent, including intent about where the task is sent, so it never
quietly becomes another vendor. If it is not installed or not signed in,
dispatch fails with the runtime, the reason, and the fallbacks it did not use:

```
dispatch: runtime codex was requested explicitly but cannot run: not signed in; run: yakos auth login codex. Not falling back to claude: an explicit runtime does not use the agent's or the project's fallback list
dispatch: to allow a fallback for this run, pass --runtime-fallback claude
```

Only the CLI can opt in: `yakos dispatch ... --runtime codex --runtime-fallback
claude`. For a named runtime the list replaces the (unused) agent and project
lists; for any other choice it is tried after them. MCP, JSON-RPC, REST and
the console have no opt-in. Pins, `.yakos.yml` defaults, `YAKOS_RUNTIME` and the
state default keep walking the fallback lists, as `cli/lib/dispatch.sh` does.

**Deliberate divergence from bash.** `cli/lib/dispatch.sh` walks the fallback
lists for an explicit `--runtime` as well. The Go dispatcher does not. The K-143 parity case
([below](#which-implementation-runs-yakos-dispatch-k-143)) records it as intended
(D2) rather than porting the bash behavior back.

The state default (`~/.yakos-state/default-runtime`) is trusted only when it is
a regular file owned by you, not group or world writable, in a directory with
the same properties (not a symlink). A file that fails this is reported and
ignored.

## Which implementation runs `yakos dispatch` (K-143)

`yakos dispatch` runs the Go dispatcher unless `YAKOS_IMPL=bash` is set. The
`yakos` binary decides: `YAKOS_IMPL=bash` hands the call to `cli/yakos`
(`cli/lib/dispatch.sh`); anything else, including unset, runs the Go path. The
bash tree stays as the oracle for the parity case and as the way back. Other
commands keep their own routing (unset means bash when the bash tree is
installed, except `hook`, `decide`, `budget`, `models` and `router`, which are
always Go, and `doctor`, which is Go unless `YAKOS_IMPL=bash`). `yakos doctor` prints an `Implementation` line saying
which one dispatch uses; `yakos doctor --policy` flags `YAKOS_IMPL=bash` when
codex or agy is installed (no sandbox flags on that path).

| `YAKOS_IMPL` | `yakos dispatch` | `yakos dispatch --explain`, `yakos router` |
|---|---|---|
| unset | Go | works |
| `go` | Go | works |
| `bash` | bash (`dispatch.sh`) | `--explain` is an unknown flag; `router` needs Go |

The bash script itself never reads the variable, and neither does the dispatch
code inside Go: it only chooses which entry point starts.

**Parity.** The case `dispatch-dry-run-parity` (`cli-go/internal/paritytest`,
CI job `dispatch parity`) resolves every agent in `lib/agents` plus fixture
project agents with no router policy file, once through the real `dispatch.sh`
against stub runtime CLIs and once through `yakos dispatch --explain` plus the
real Go dispatch, and compares runtime, model (claude), and the shape of the
claude argv. It runs under the project-config variants none, `default-runtime`,
`per-domain`, `default-fallback`, `router.disable_runtimes` and an unavailable
explicit `--runtime`. The first unlisted divergence fails it with a table of
rows. The divergences are intended; the case checks that each row diverges in
the documented kind (a different divergence on a listed row fails), and that
the observed set is exactly D1 to D4:

| id | Go behavior | bash behavior |
|---|---|---|
| D1 | `router.disable_runtimes` in `.yakos.yml` skips or refuses a runtime | key not read |
| D2 | an explicit `--runtime` that cannot run fails unless `--runtime-fallback` | walks the fallback lists |
| D3 | model of a non-claude runtime is that runtime's own model (registry id, or the harness default when the registry has none, as for codex) | the tier name |
| D4 | `model-policy:` frontmatter is not read | overrides `model:` |

Go-only and so not exercised (no policy file): router rules, cooldown, sticky
conversations, and `--explain`.

**Known limit of the bash path (K-169).** On Linux with GNU coreutils `timeout`
on PATH, `YAKOS_IMPL=bash yakos dispatch` fails in `dispatch.sh`: `ct_timeout`
cannot run the shell function `yk_rt_dispatch` (exit 127). It predates K-143; the
parity case strips `timeout` and `gtimeout` from the PATH it gives the oracle, so
the oracle runs untimed. Until K-169 fixes it, the `YAKOS_IMPL=bash` way back does
not work on such a host.

## Model tiers

yakOS routes dispatches through four tiers, lowest to highest:

| Tier | Aliases |
|---|---|
| `haiku` | `cheap` |
| `sonnet` | `balanced` |
| `opus` | `best`, `reasoning` |
| `fable` | `frontier` |

Framework agents declare a maximum tier in their frontmatter. `fable` requires
explicit opt-in (agents cap at `opus` by default). Override per-dispatch with
`--model <tier>` or per-pane in the console Chat tab.

## Console Chat streaming behavior (v0.40.0.0+)

The unified console Chat tab uses an unframed execution mode (agent definition
as system prompt, direct `-p`, `--include-partial-messages`):

| Runtime | Streaming behavior |
|---|---|
| `claude` | Token-by-token streaming via SSE. First token latency governed by `--include-partial-messages` mode. |
| `codex` | Buffered — the full response arrives as one chunk + summary. The chunk is codex's raw `--json` lines until the stream parser lands (K-135). |
| `agy` | Buffered — the full response arrives as one chunk + summary. The chunk is agy's raw `stream-json` lines until the stream parser lands (K-135). |

Partial streaming is a claude-specific capability. The Chat UI labels buffered
runtimes clearly so operators know to expect a single response rather than a
live stream.

## Interactive chat engines (K-137)

The console Chat pane's interactive mode has two engines, both for claude. The
CLI engine runs `claude --print --input-format stream-json` itself, so it is
Claude Code under your own login and works for subscription users. The SDK
engine (`yakos serve --console-structured-questions`) runs a Node sidecar built
on the Anthropic Agent SDK so `AskUserQuestion` can be answered in the browser.
Anthropic's terms of 2026-02-19 allow a subscription login only in Claude Code
and claude.ai, not in the Agent SDK, so the SDK engine is hard-gated: it starts
only when `ANTHROPIC_API_KEY` holds an API key, it never receives
`CLAUDE_CODE_OAUTH*` variables or OAuth tokens, and without a key the console
shows the refusal instead of falling back to the CLI engine or to your login.
Subscription users stay on the CLI engine. `yakos doctor --policy` reports an SDK
engine that can be selected without a key. The bash `claude-sdk` runtime (the
Python Agent SDK, `yakos dispatch --runtime claude-sdk`) follows the same rule:
its dispatch refuses unless `ANTHROPIC_API_KEY` holds an API key, and the python
it starts inherits no OAuth variables; its `launch` is claude.sh and is
unchanged.

### Codex and agy panes (K-147)

With the Interactive toggle on, a codex or agy pane keeps its context across
turns. It has no persistent process: each turn is one dispatch, and the
harness's own session id (codex `thread_id`, agy `conversation_id`) is stored
with the conversation and passed to the next turn (`codex exec resume`, agy
`--conversation`), also after a console restart. Each turn writes its own
dispatch-log event pair. A pane is pinned to its runtime: dispatching a
different kind of runtime (claude into a codex or agy pane, or the reverse)
into a conversation that has a live engine is refused with 409. Structured
questions stay claude-only.

With the toggle off (one-shot panes), codex and agy do not resume: every send
starts a fresh harness session with no memory of the earlier turns.

At an agent's hard budget stop the two kinds differ. A claude pane answers a
follow-up send with 429 and the budget text. A codex or agy pane has no
pre-flight at the send: the turn runs through the dispatch layer, whose budget
check refuses it before the harness starts, and the refusal arrives as a turn
error on the pane's stream and in its transcript.

## Jev is not a runtime

TypeSafe's Jev is deliberately absent from this matrix. It has no session, no
agent prompt, no tools, and no text output: it evaluates typed questions
against a redacted state and returns probabilities. yakOS integrates it as a
*decision provider* behind `yakos decide` (see
[decision-providers.md](decision-providers.md) and
[ADR-0009](adr/ADR-0009.md)), so it can never be selected with `--runtime`,
named in `runtime:` or `runtime-fallback:` frontmatter, or offered in the
console chat runtime list. `yakos validate` and `yakos agent lint` reject
`runtime: jev` with a hard error.

## Adding a new runtime

See [`cli/lib/runtimes/README.md`](../cli/lib/runtimes/README.md).
