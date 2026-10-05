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
| Sandbox flag (K-133) | n/a (permission mode) | ✅ `--sandbox workspace-write`: an OS sandbox, network off by default | ⚠ `--sandbox` blocks writes outside the workspace by default only; not a containment boundary (K-158, below) |
| Agent file yakOS writes | (none — JSON injection) | `.codex/agents/yakos-<id>.toml` | `.agents/skills/yakos-<id>/SKILL.md` |

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
when the bash tree is present and `YAKOS_IMPL` is unset) still run with the
bypass flags; that path retires when the Go dispatcher becomes the default
(K-143).

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
  2's own step usage. Account one run from the DONE step usage in its own stream
  (it equals the run's own usage), not from `result.usage` of a resumed turn.
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
lists for an explicit `--runtime` as well. The Go dispatcher does not. K-143
(the parity matrix) must record this as intended rather than port the bash
behavior back.

The state default (`~/.yakos-state/default-runtime`) is trusted only when it is
a regular file owned by you, not group or world writable, in a directory with
the same properties (not a symlink). A file that fails this is reported and
ignored.

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
