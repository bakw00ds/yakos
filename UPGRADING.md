# Upgrading yakOS

How to move an existing yakOS install from any older version up to the
current release, what survives, and how to fully uninstall when needed.

This doc is the **upgrade authority** — `yakos --help`, README, and
CHANGELOG point here. Last updated for v0.39.

## Unreleased: the Anthropic gateway and `yakos start --routed` (K-151)

New and optional; nothing changes until you turn it on.

- `yakos serve --gateway` serves the pass-through gateway on `127.0.0.1:7897`
  (flag `--gateway-passthrough-subscription` lets a subscription OAuth token
  through; leave it off unless you have read ADR-0011). The policy key
  `anthropic_gateway: true` in `~/.yakos-state/router-policy.yml` does the same
  as the first flag. A project `.yakos.yml` cannot turn it on.
- The daemon needs `ANTHROPIC_API_KEY` in its environment; the gateway attaches
  it to upstream requests.
- Every time the gateway starts, the daemon writes a fresh token to
  `~/.yakos-state/gateway-token` (0600). A daemon restart therefore ends
  running `yakos start --routed` sessions: they get 401 ("gateway token
  rotated") until you quit and run `yakos start --routed` again. `--routed`
  cannot be combined with `--no-repl` (there is no Claude Code child to give
  the token to).
- `yakos start --routed` (Claude only) sets `ANTHROPIC_BASE_URL`,
  `CLAUDE_CODE_GATEWAY_HINT_HEADERS=1` and `ANTHROPIC_AUTH_TOKEN=<gateway token>`
  in the Claude Code child and removes `ANTHROPIC_API_KEY` and any own
  `ANTHROPIC_AUTH_TOKEN` from it. It refuses to launch unless the daemon of the
  current directory reports the gateway bound on 7897, so run it from the
  directory where `yakos serve --gateway` runs. It always uses the vendor TUI,
  not the REPL.
- If port 7897 is taken by another program the gateway stays off and says so;
  `--routed` then refuses. Free the port and restart the daemon.
- Anything else that calls the gateway needs the token as
  `Authorization: Bearer <token>`; without it the reply is 401.

## Unreleased: `yakos start` opens the yakOS REPL (K-154)

With `YAKOS_IMPL=go`, `yakos start` on a terminal now opens the yakOS REPL
instead of exec-ing the vendor TUI. It talks to the console daemon (started for
you when absent, and left running when you exit) and shares its conversation
with the browser Chat pane. Nothing needs doing to adopt it.

To keep the old behavior:

- `yakos start --native <runtime>` runs the vendor TUI exactly as
  `--runtime <runtime>` did (the argv is unchanged). Scripts and aliases that
  rely on the TUI should add `--native`.
- `--no-repl`, `--dry-run`, `--print-agents`, `--print-env`,
  `--share-terminal`, `--direct`, the session flags (`--continue`, `--resume`,
  `--fork`, ...) and any non-terminal stdin keep the exec path without changes.
- The bash launcher (`YAKOS_IMPL=bash`) always starts the native TUI.

If `yakos start` refuses with "the process on the console address is not this
project's yakOS daemon" or "from another build", a daemon from another project
or an older install holds the console port: run `yakos serve stop` in the
project that started it, then retry. This is deliberate: the console token is
never sent to a daemon that cannot prove, over its owner-only socket, that it
serves this workspace and this build.

## Unreleased: `yakos dispatch` is Go by default

`yakos dispatch` now runs the Go dispatcher when `YAKOS_IMPL` is unset. Before,
a checkout with the bash tree sent it to `cli/lib/dispatch.sh`. What changes on
that path: router policy, budgets and `--explain` apply; codex runs with
`--sandbox workspace-write` and agy with `--sandbox`; an explicit `--runtime`
that cannot run fails instead of falling back (add `--runtime-fallback`); agents
pinning `claude-sdk`, `antigravity-sdk` or a plugin runtime are skipped; and
`model-policy:` frontmatter is not read. The full list of intended differences,
and the case that holds the two paths to the same resolution otherwise, is in
[docs/runtime-matrix.md](docs/runtime-matrix.md#which-implementation-runs-yakos-dispatch-k-143).

To keep the old behavior for a shell or a project, set `YAKOS_IMPL=bash`
(`yakos doctor` then warns, and `doctor --policy` flags it when codex or agy is
installed). Nothing else needs doing: Go-only installs already ran the Go path.
Known issue (K-169): on Linux with GNU coreutils `timeout` on PATH,
`YAKOS_IMPL=bash yakos dispatch` fails in `dispatch.sh` (`ct_timeout` cannot run
`yk_rt_dispatch`, exit 127), so the way back does not work there yet.

## Unreleased: model ceilings across runtimes, `--extends` (K-139c)

- **Ceilinged codex agents are refused until you map codex aliases.** A
  `max_model` ceiling is a cost class that now governs every runtime. The registry
  ranks no codex model out of the box, so a codex agent under a ceiling is refused
  with an error that names the model, the ceiling, `yakos models show <id>` and the
  overlay file. Map `cheap`, `balanced` and `frontier` for codex in
  `model-registry.yml` (mode 0600, in the yakOS state directory) to rank them. A
  codex agent with no pinned `model:` never ranks, even with the overlay: pin one.
  A routing rule with fallbacks skips the codex candidate and lands on the next.
- **`yakos agent --extends` no longer accepts a project agent**, and `yakos agent
  lint` errors on a project-local `extends:`. Only a bare id under the framework's
  `lib/agents` is read. Copy the template into the agent instead of extending it.

## Upgrading to v0.62.0.0

v0.62.0.0 is a minor release. A v0.61.0.0 binary upgrades in place with
`yakos upgrade`. Then refresh every project once, so the supervisor hook picks up
the token-aware launch gate (item 7):

```sh
yakos refresh --project <path>      # or: yakos refresh --all
```

Rolling back a binary install to v0.61.0.0:

- Run the installer pinned to the old version. It also re-points the framework
  links:
  `curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh -s -- --version 0.61.0.0`
- If a release has to be withdrawn, mark its GitHub release as a pre-release. The
  `latest` release, which `yakos upgrade` and the installer follow, is then v0.61.0.0
  again.
- A v0.61.0.0 binary sums only `usage.total_cost_usd`, which is 0 for subscription
  runs, so its dollar budgets read those runs as free (item 7). The refreshed
  supervisor hook then gates on dollars only.

Changes since v0.61.0.0 that may need action. Items 1 to 7 and the two sections
after item 8 (the codex and agy sandbox, the SDK sidecar API key) are behavior
changes; item 8 needs no action.

### 1. Agents with `runtime:` now run on that runtime

`general-codex`, `general-agy` and any project agent with `runtime:` set
now run on the runtime they declare instead of claude. Before, the Go
dispatcher (the daemon, MCP, console chat, Flows and the `YAKOS_IMPL=go`
CLI) ignored `runtime:` and ran everything on claude (K-127). The full
resolution order is in
[docs/runtime-matrix.md](docs/runtime-matrix.md#go-dispatch-now-honors-runtime).

To keep the old behavior:

```sh
# one call
yakos dispatch general-codex "<task>" --runtime claude
```

- In the console, set the Chat pane runtime to `claude`. New panes default to
  `auto`, which follows the agent's pin.
- To keep it permanently, remove the `runtime:` line from the agent's
  frontmatter.

### 2. A runtime that is not signed in now fails fast

If codex or agy is not installed or not signed in, dispatch now fails fast
(or falls back per the agent's `runtime-fallback`, then the project's
`default-fallback`) instead of running on claude. The error names each
runtime it skipped and why:

```text
agy: not signed in; run: yakos auth login agy
```

To have a pinned agent fall back to claude instead of failing, add this to
its frontmatter:

```yaml
runtime: codex
runtime-fallback: [claude]
```

**A runtime you name yourself never falls back.** `--runtime codex`, a
console pane set to codex, the `runtime` parameter of an MCP, JSON-RPC or
REST call, and `yakos dispatch codex "..."` all mean "send this to codex".
If codex cannot run, dispatch fails with the reason and the fallbacks it did
not use, even when the agent or `.yakos.yml` lists some, because answering
from another vendor is not what you asked for:

```text
dispatch: runtime codex was requested explicitly but cannot run: not signed in; run: yakos auth login codex. Not falling back to claude: an explicit runtime does not use the agent's or the project's fallback list
dispatch: to allow a fallback for this run, pass --runtime-fallback claude
```

On the CLI, `--runtime codex --runtime-fallback claude` opts in. (The bash
`yakos dispatch` still falls back for `--runtime`; this is a deliberate
difference.) Pins and `.yakos.yml` defaults keep falling back as above.

### 3. Pins the Go dispatcher skips

Agents that pin `runtime: claude-sdk`, `antigravity-sdk` or a plugin id are
skipped by the Go dispatcher, because those runtimes only exist in the bash
path. Give such an agent a `runtime-fallback`, or use `YAKOS_IMPL=bash`.

### 4. `gemini` is gone from the Go side

`gemini` is no longer a runtime in the Go runtime registry, the console
runtime selector or `yakos start`'s known runtimes.
Change `runtime: gemini` pins to `runtime: agy`. `yakos validate` still
accepts `runtime: gemini` in agent frontmatter, as a warning, for one more
release. A dispatch to gemini, or to an agent still pinned to it, fails with
`gemini was removed; use agy`.

### 5. Agent files named after a runtime are skipped

An agent whose file is named `claude.md`, `codex.md` or `agy.md` would shadow
the runtime's own agent (what `yakos dispatch codex "..."` and the console's
default pane resolve to), so a cloned repository could use one to send them to
another vendor. The Go dispatcher now skips such a file with a warning, and
`yakos validate` reports it as an error. Rename the file.

### 6. The default runtime file must be yours

`~/.yakos-state/default-runtime` (written by `yakos auth set-default`) is used
only when it is a regular file owned by you that no one else can write, in a
directory with the same properties, not a symlink. A file that fails this is
ignored with a one-line notice. A file `yakos auth set-default` wrote passes.

### 7. Dollar budgets ignore subscription runs; token limits are new (K-136)

Tokens are now the primary unit. The Go dispatcher (the console, MCP, Flows,
JSON-RPC, REST, gRPC and `yakos dispatch` with `YAKOS_IMPL=go`) records how each
run was billed. A run on a harness with no API key in its environment is a
`subscription` run: its tokens are counted, its dollar figure is kept only as
`api_equivalent_usd`, and it no longer counts toward `limit_usd`. To keep the
built-in budgets tripping for subscription operators, the supervisor and
librarian now also have a built-in monthly token limit, which counts every run
whatever its billing (the dollar ceilings converted at $3 per million tokens, the
Sonnet reference rate: $100 becomes 33,000,000 tokens and $40 becomes
13,000,000; the arithmetic is in [docs/budgets.md](docs/budgets.md)).

What changes for you:

- **Subscription operators:** `limit_usd` never trips for your runs, but the
  built-in token limits do: the supervisor stops routine launches at 33,000,000
  tokens a month and the librarian at 13,000,000. `yakos budget status` now shows
  tokens used and the token limit first. If your normal month is larger, raise the
  limit, and set limits for other agents the same way:

  ```sh
  yakos budget set supervisor --tokens 60m
  yakos budget set general-codex --tokens 5m
  ```

  Codex and agy report tokens and no dollars, so a token limit is the only budget
  that can stop them.
- **Turning a built-in budget off:** `yakos budget set supervisor 0` now turns off
  the dollar limit only, and prints a note that the token limit remains. Use
  `yakos budget set supervisor 0 --tokens 0` to turn the whole budget off.
  `yakos budget check` exits 4 when either limit is reached.
- **The supervisor hook's launch gate now reads tokens too** (both twins, K-136).
  It used to be dollar-gated: off when `limit_usd` was 0, with a 2x high-risk
  ceiling that compared dollars only. It now decides from the unified status of
  `yakos budget check --json`: the supervisor budget is off only with no limit of
  either kind, routine launches are refused at the limit of either unit,
  high-risk launches are blocked at 2x either unit, and a token ceiling writes
  the synthetic CRITICAL finding (`Supervisor token-budget ceiling (N tokens)
  reached`). Hook log records for token messages carry `spent_tokens`,
  `limit_tokens` and `ceiling_tokens`; dollar records are unchanged. Upgrade the
  `yakos` binary and refresh the hooks (`yakos refresh`) together: the bash hook
  reads the token fields from the binary, and an older binary that prints none
  leaves the gate dollar-only.
- **A malformed limit in the policy file is now ignored, not read as "off".** A
  negative or NaN `limit_usd`, or a negative `limit_tokens`, used to switch the
  limit off, including a built-in one, and an infinite or huge value gave a limit
  or a stop that could never be reached. Out of range is now a `limit_usd` that is
  negative, NaN, infinite, above $1,000,000,000 or positive and below $0.01, and a
  `limit_tokens` that is negative, not a whole number, not a number or above 2^50.
  Such a value is ignored with a warning and the limit it would have replaced
  stays, and a project's `agent_budgets:` value gets the same check. To turn a
  limit off, set it to `0`.
- **A project can rename its supervisor and keep its budget, never a looser
  one.** The agent a project names as its supervisor (`supervisor: agent:` in
  `.yakos.yml`) is now budgeted at the stricter of its own limits and the
  supervisor's, whatever you set for `supervisor` in your own policy file
  included: per unit the smaller amount and the smaller stop, a lifetime window if
  a side that has a limit is lifetime (a side with no limit contributes no
  window). An agent with no limits of its own gets the supervisor's, window
  included (before, a renamed agent had no budget), and one with a limit keeps it
  when it is the smaller. The result is never looser than checking both
  separately, and can be stricter in the mixed case (an own $200 lifetime limit
  beside the supervisor's $100 monthly one is $100 lifetime). `yakos budget
  status`, `yakos doctor` and `yakos budget reset` take the project into account
  (`--project <dir>`, else the working directory); `yakos doctor` uses it for its
  Agent budgets section only, and still runs its project checks only for a
  positional project path.
- **API-key operators:** your dollars count as before. A run with
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` (or their siblings) in
  its environment is `api`, and its dollars count toward `limit_usd`. What is new
  for you is the token side: the built-in token limits count every run whatever
  its billing, so the supervisor and librarian also stop at 33,000,000 and
  13,000,000 tokens a month, and a month with unusually many tokens per dollar
  could trip a token limit before the dollar one. Rows written before this
  release, and every row the bash dispatcher writes (still the default for
  `yakos dispatch`), have no `billing` field and keep counting their dollars, so
  no budget resets itself on upgrade.
- **Console-login (pay-per-token) users:** a harness signed in that way, or given
  a key by its own settings file, has no key in the environment yakos passes to
  it, so it reads as a subscription and `limit_usd` will not see it. Use a token
  limit.
- The budget spend cache is rebuilt from the log once, on first use.
- `yakos cost` prints exactly what it printed unless the log holds rows with a
  `billing` field; then it adds token columns, in the Go `yakos cost` (under
  `YAKOS_IMPL=go` or a Go-only install; with the bash tree present and
  `YAKOS_IMPL` unset it is still served by bash and prints the old table until
  the Go dispatcher becomes the default). The `efficiency.total_cost_usd`
  trend in the metrics dashboard steps down after the upgrade, because older
  snapshots summed every dollar figure.
- Interactive Chat turns are now in the dispatch-log (surface `console-chat`),
  so the Cost views include them, and they are held to the budget like any other
  dispatch: a new interactive session, and each follow-up message to a live one,
  is refused when the agent is at its hard stop (the pane shows the refusal; a
  follow-up gets an HTTP 429). A session keeps the agent it started as.
- `yakos budget set` without `--window` now keeps the agent's current window
  instead of writing `monthly`. If you relied on a plain `set` to turn a
  `lifetime` window monthly, pass `--window monthly`.

Downgrading: the new log keys are additive and an older yakos ignores them, but
an older yakos sums `usage.total_cost_usd`, which is 0 for subscription rows, so
its dollar budgets will read those runs as free.

### 8. `lib/settings/model-catalog.json` supersedes `model-aliases.json` (no action needed)

The model registry (`yakos models`, K-138) reads a new catalog,
`lib/settings/model-catalog.json`, which the Go binary embeds. It lists each model
by harness with its billing, effort levels and limits, and its `aliases` key
carries the same semantic alias table (`cheap`, `balanced`, `best`, `reasoning`,
`frontier`) as `lib/settings/model-aliases.json`. Nothing you run changes:

- `model-aliases.json` stays. The bash CLI still reads it
  (`cli/lib/project-config.sh`), and the Go dispatcher still resolves aliases
  through its own embedded copy. A test keeps the catalog's `aliases` key equal to
  the file, so bash and Go cannot resolve an alias differently.
- If you edited `lib/settings/model-aliases.json` in a checkout or a fork, make
  the same edit to the `aliases` key of `model-catalog.json` and refresh the
  embedded copies:

  ```sh
  cp lib/settings/model-aliases.json cli-go/internal/runtime/model-aliases.json
  cp lib/settings/model-catalog.json cli-go/internal/modelreg/model-catalog.json
  ```

  The drift tests (`TestEmbeddedAliasTableMatchesLib`,
  `TestCatalogAliasesMatchLegacyFile`, `TestEmbeddedCatalogMatchesLib`) fail until
  all of them agree.
- `~/.yakos-state/model-registry.yml` is optional; without it the catalog's
  defaults apply. If you add one, keep it owner-only: a symlink, another user's
  file, or one writable by group or others is ignored with a warning.
- `yakos models list`, `show <id>` and `probe` are new; see
  [docs/routing.md](docs/routing.md#model-registry).

## v0.62.0.0: codex runs in an OS sandbox, agy gets `--sandbox` but is not contained (K-133)

The Go dispatcher (the console, MCP, Flows, JSON-RPC, and `yakos dispatch` with
`YAKOS_IMPL=go`) now runs codex with `--sandbox workspace-write` and an approval
policy that cannot prompt, and agy with `--sandbox`. Before, both ran with
approvals and sandbox off. The bash `yakos dispatch` path, used when the
bash tree is present and `YAKOS_IMPL` is unset, is unchanged until the Go
dispatcher becomes the default.

What you will notice with codex:

- Commands the model runs can write only inside the project, `$TMPDIR` and
  `/tmp`. Other writes fail with "Operation not permitted".
- The project's `.git` directory is read-only, so a codex agent cannot commit or
  switch branches. `git status`, `git diff` and `git log` work. Have the lead do
  the commit.
- The network is off, so `npm install`, `go get` and `git push` fail. To allow
  it, set `[sandbox_workspace_write]` `network_access = true` in the
  `config.toml` of the `CODEX_HOME` yakOS uses.

What to know about agy, which none of the above applies to:

- agy still gets `--sandbox`, because it blocks the default write path.
  Under `--sandbox --dangerously-skip-permissions`, agy's macOS Seatbelt sandbox
  blocks writes outside the workspace by default but leaves file reads and
  outbound network unrestricted, and the model can escalate out of the sandbox
  at will via `run_command(BypassSandbox=true)`, which
  `--dangerously-skip-permissions` auto-approves; agy dispatch is therefore not
  a containment boundary for reads, network or writes and must only receive
  non-sensitive work or run inside an external OS sandbox (K-159).

To opt a runtime out, create `~/.yakos-state/router-policy.yml`:

```yaml
allow_unsandboxed_runtimes: [codex, agy]
```

```sh
chmod 600 ~/.yakos-state/router-policy.yml
```

The file must be a regular file you own, not group or world writable, and not a
symlink; otherwise it is ignored and `yakos doctor` says why. A project
`.yakos.yml` cannot turn the sandbox off, and `YAKOS_DISPATCH_LOG` does not move
this file. While a runtime is unsandboxed, yakOS prints a line on stderr once per
process and `yakos doctor` warns.

Other changes in this release for codex and agy:

- **Own codex login (optional).** Run `yakos auth login codex` once to give
  yakOS its own login in `~/.yakos-state/codex-home`, so dispatches and your
  interactive codex stop sharing one `auth.json`. Dispatch (Go and bash), Go chat
  and the bash `yakos start` use it; Go `yakos start` still launches the
  interactive codex with your own `CODEX_HOME`. Until you run the command,
  dispatch keeps using `$CODEX_HOME` or `~/.codex`. `yakos doctor` prints a hint.
- **agy skill files move.** The generated skills are now
  `.agents/skills/yakos-<id>/SKILL.md` directories, the layout agy 1.2.x loads.
  Old flat `yakos-<id>.md` files are not used and can be deleted; they are
  already gitignored. The new directories ignore themselves, so no `.gitignore`
  change is needed.
- **Generated agent files are protected.** `.codex/agents/yakos-<id>.toml` and
  the agy skills start with a `yakos-generated:` marker. A file without it is
  yours and is never overwritten; delete the marker line to keep edits to a
  generated file.
- **Model ids.** The semantic aliases (`cheap`, `balanced`, `best`, `reasoning`,
  `frontier`) now mean "the harness default" for codex (no `-m`), because the old
  ids (`gpt-5`, `gpt-5-mini`, ...) are not in the current codex catalog
  (`codex debug models`) and codex answers an unknown id with HTTP 400. For agy
  they map to real `agy models` ids, whose `-low`/`-medium`/`-high` suffix is the
  reasoning effort: choose the effort by choosing the id, because agy rejects
  `--effort` next to such an id and yakOS no longer passes it then. With no
  suffixed id, agy takes `--effort low|medium|high` only; yakOS sends the
  console's `xhigh` and `max` as `high` (with one stderr note) because agy exits
  1 on them. `general-codex` now pins `balanced` and `general-agy` pins
  `gemini-3.8-flash-high` (its old pin, `gemini-3.5`, does not exist). To pick a
  specific model, put its id in an agent's `model:`.
- **Agent files never name a Claude tier.** The generated codex and agy files
  carry a `model` line only for a model that is not `haiku`, `sonnet`, `opus` or
  `fable`. Before, an agent pinned to an alias (`general-codex` pins `balanced`)
  got `model = "sonnet"` from the bash emitter, and codex refused to run it.
  Run any dispatch once and the files are rewritten; nothing to do by hand.
- **Odd agent text.** A control character in an agent's text is written as a
  `\u00XX` escape instead of producing an invalid file, an agent whose text
  holds a NUL byte is skipped with a note, and chat on codex and agy refuses an
  agent persona over 64 KiB with a clear error (for codex, 64 KiB after the
  persona is escaped for the command line, which grows quotes, backslashes,
  newlines and control characters).

## v0.62.0.0: the SDK sidecar needs `ANTHROPIC_API_KEY` (K-137)

`yakos serve --console-structured-questions` runs a Node sidecar built on the
Anthropic Agent SDK so the console can show `AskUserQuestion` as an answerable
widget. Anthropic's terms of 2026-02-19 allow a Pro or Max subscription's login
only in Claude Code and claude.ai, not in the Agent SDK, and until now the
sidecar fell back to your claude.ai login when no API key was set.

It no longer does. The sidecar starts only when `ANTHROPIC_API_KEY` is set to an
API key in the daemon's environment (Anthropic Console billing applies). With no
key, a blank one, or an OAuth token (`sk-ant-oat...`) in the variable, a chat
dispatch with `structuredQuestions: true` fails at once and the pane shows an
error that begins like this:

```text
interactive: SDK start failed: ... ANTHROPIC_API_KEY is not set: the Agent SDK engine does not run on a claude.ai subscription login ...
```

Nothing falls back to another engine, and the daemon itself starts as before.

What to do:

- **Subscription login only:** use the CLI engine, which is interactive chat
  without structured questions. It runs the `claude` CLI, Claude Code itself,
  under your own login and does not change. `AskUserQuestion` shows as text.
- **You have an API key:** export `ANTHROPIC_API_KEY` in the shell that starts
  `yakos serve`.
- **Bedrock or Vertex:** the sidecar checks `ANTHROPIC_API_KEY` only, so those
  deployments use the CLI engine too.

The sidecar's environment also drops every `CLAUDE_CODE_OAUTH*` variable and any
value that holds an OAuth token, except variables named `YAKOS_*`: those are
yakOS's own and are never dropped for what they contain, so never put a
credential in a `YAKOS_*` variable; it is passed through unchanged. Runs of the
`claude` CLI are unaffected.

The bash `claude-sdk` runtime, which runs the Python Agent SDK, has the same
rule. `yakos dispatch --runtime claude-sdk` on the bash CLI now stops with one
line (`claude-sdk: refusing to run: ANTHROPIC_API_KEY is not set; ...`) unless
`ANTHROPIC_API_KEY` holds an API key, and the python it starts gets no
`CLAUDE_CODE_OAUTH*` or OAuth-token variables. Export a key, or use
`--runtime claude`, which is Claude Code itself. `yakos start --runtime
claude-sdk` still launches Claude Code; only its auth hint changed, and both
CLIs now say `yakos auth login claude`.

**Known limit on Linux:** the bash `claude-sdk` runtime cannot yet dispatch the
full framework roster there. It hands the roster to python in one environment
string, which is over the 128 KiB Linux allows for a single string, so the exec
fails with `Argument list too long`. This is tracked on K-144; a key alone is not
enough on Linux until it is fixed.

`yakos auth` follows: `yakos auth status claude-sdk` reports whether
`ANTHROPIC_API_KEY` is set (never its value) and says the claude login is not
used by the SDK engine, `yakos auth login claude-sdk` prints how to set the key
instead of routing through the claude login flow, and `yakos auth logout
claude-sdk` no longer removes `~/.claude/auth.json`; use `yakos auth logout
claude` for that. The bash and Go CLIs print the same text.

`yakos doctor --policy` mentions an SDK sidecar that is installed without a key
as a low heads-up, along with the other risky settings it finds (see CHANGELOG).

## Upgrading to v0.61.0.0

v0.61.0.0 is a minor release. A v0.60.1.0 binary upgrades in place with
`yakos upgrade` (a v0.60.0.0 binary cannot; see the v0.60.1.0 note below).
Then do the per-project step.

### 1. Refresh every project once

```sh
yakos refresh --project <path>      # or: yakos refresh --all
```

Use `--dry-run` first to see what would change. One refresh picks up:

- **Hybrid Go hooks.** `path-allowlist`, `secret-scan`,
  `output-injection-scan` and `supervisor-stream` now run through
  `yakos hook run --impl go`, with a bash fallback guard on the enforcing
  ones. `--hooks-impl bash` is the escape hatch; the choice is saved to the
  project's `.yakos.yml` as `hooks_impl`.
- **Auto-compaction.** `autoCompactWindow: 150000` is written to
  `.claude/settings.json` when the key is absent. Tune or disable it with
  `auto_compact_window: <tokens>|off` in `.yakos.yml`.
- **Managed specialist rules.** The five rules (git-hygiene, commit-format,
  pr-conventions, secret-handling, verification-discipline) are copied into
  `.claude/rules/`. Files without the trailing `yakos:managed` marker are
  project-owned and left alone.

Hybrid hooks apply only to Go-native installs or when `YAKOS_IMPL=go` is
set. With the bash CLI tree present and `YAKOS_IMPL` unset, refresh is
proxied to bash and stays all-bash. A yakos binary that looks temporary is
never pinned into a project; the refresh output says so.

### 2. Dollar budgets are on for two agents

Defaults: `supervisor` $100/month and `librarian` $40/month, in local
calendar months. At 80% you get a warning. At 100% new dispatches are
refused with exit code 4. A run in flight is never killed.

```sh
yakos budget status [--json] [--by-project]
yakos budget set <agent> <usd> [--window monthly|lifetime]
```

`yakos budget set <agent> 0` switches a limit off. A project `.yakos.yml`
`agent_budgets:` block can only lower a limit. See `docs/budgets.md`.

### 3. Policy files are now stricter-only per project

- **Supervisor launch limits** (`max_launches_per_session`,
  `min_launch_interval_s`, `run_deadline_s`) in a project `.yakos.yml` can
  only make supervision stricter. To loosen them, edit the user-level
  `~/.yakos-state/supervisor-policy.yml`. A deadline under 30 s is invalid.
- **Decision provider.** A project `.yakos.yml` cannot enable one (it may set
  `provider: none`). Only `YAKOS_DECISION_PROVIDER` or
  `~/.yakos-state/decision-policy.yml` can. This has been the rule since
  0.60.x; nothing to migrate unless a project relied on the old behavior.

### 4. New doctor checks

Run `yakos doctor <project-path>` after refreshing. It now reports:

- **Project rules drift.** A "Project rules (.claude/rules)" section warns
  when a managed rule is missing, edited, stale or a symlink. Fix with
  `yakos refresh --project <path>`.
- **Hook fallback log.** A warning when `~/.yakos-state/hook-fallback.log`
  has entries from the last 7 days, meaning a Go hook crashed and the guard
  absorbed it.
- **Budgets.** Agents at warning or hard stop. An exhausted supervisor
  budget is an error ("LLM supervision disabled").

## Specialist rules now live in each project (K-116)

Framed `yakos dispatch` runs claude with `--setting-sources project`, so
dispatched specialists no longer read `~/.claude/rules`. `yakos refresh`
now copies the specialist rules (git-hygiene, commit-format,
pr-conventions, secret-handling, verification-discipline) into each
project's `.claude/rules/`. Copies carry a trailing `yakos:managed` marker
and are updated on every refresh; a file without the marker is
project-owned and never overwritten. They are copies, not symlinks, because
claude ignores a project rule that symlinks outside the project.

Existing projects must re-run `yakos refresh --project <path>` (or `yakos refresh --all`) once
after upgrading, or their dispatched specialists will run without these rules.

## Binary install upgrade (curl|sh — recommended)

If you installed via `scripts/install.sh`, the simplest upgrade is to
re-run the installer:

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

The installer downloads the new binary, verifies the SHA256 checksum,
runs `yakos install` (which materializes the updated embedded lib to
`~/.local/share/yakos/<new-version>/` and refreshes `~/.claude`
symlinks), and updates `export YAKOS_IMPL=go` in your shell profile
if the line is not already present.

To install a specific version:

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh -s -- --version 0.39.0
```

After the installer exits, open a new terminal (or `source` the
profile it printed) and verify:

```sh
yakos --version     # should show 0.39.0.0 (go)
yakos doctor        # environment health check
```

The framework lib lives at `~/.local/share/yakos/<version>/`. Each
binary version has its own slot; old versions are left in place until
you delete them. `YAKOS_IMPL=go` is set by the installer — you do not
need to export it manually unless you skipped the installer.

For each project that has been bootstrapped:

```sh
yakos doctor <name> --fix      # auto-remediate gitignore, hashes, dirs
yakos migrate <name>           # bump .yakos.yml schema if present
```

> Trust note: `yakos upgrade` verifies a SHA-256 from an unsigned
> `checksums.txt`. See [docs/selfupdate-trust-boundary.md](docs/selfupdate-trust-boundary.md).

### v0.60.0.0 users: `yakos upgrade` cannot self-update

The v0.60.0.0 binary's self-updater rejects GitHub's new release-asset
host (`release-assets.githubusercontent.com`) before any download, so
`yakos upgrade` fails with `redirect to disallowed host ... rejected`.
The fix ships in v0.60.1.0, but a v0.60.0.0 binary cannot fetch it.
Reinstall once via the installer (curl follows the redirect fine):

```sh
curl -fsSL https://raw.githubusercontent.com/bakw00ds/yakos/main/scripts/install.sh | sh
```

or download the release asset manually and verify it against
`checksums.txt` yourself. `yakos upgrade` works again from v0.60.1.0
onward.

## Cloned-repo / dev upgrade

Use this path when you work with a live `lib/` tree — edits to agents,
hooks, and rules are picked up immediately without reinstalling.

```sh
# 1. Pull the new framework code
cd ~/code/yakos          # wherever you cloned yakos
git pull --ff-only

# 2. Refresh the install: re-link symlinks, update settings env block
./cli/yakos update

# 3. Verify environment + autodetect what changed
./cli/yakos doctor
./cli/yakos doctor --probe-runtime

# 4. For each project that has been bootstrapped, migrate config + state:
for cd in ~/agent-control/*/; do
    proj_path="$(head -1 "$cd/.project-path" 2>/dev/null)"
    [ -d "$proj_path" ] || continue
    ./cli/yakos doctor "$proj_path" --fix     # auto-remediate gitignore, hashes, dirs
    ./cli/yakos migrate "$(basename "$cd")"   # bump .yakos.yml schema if present
done
```

That's it. Sessions started after step 3 use the new release. Open
sessions keep running on the old framework until you restart them
(`yakos team restart <project>` archives the work area cleanly).

## What an upgrade actually does

### Binary install path

The installer re-runs `yakos install`, which performs two operations:

1. **Lib materialization.** The new binary embeds the full `lib/`
   (agents, skills, rules, hooks) via `go:embed`. `yakos install`
   extracts it to `~/.local/share/yakos/<version>/` and refreshes
   `~/.claude/{agents,skills,rules,playbooks}/` symlinks to point at
   the new version. The old version's slot is left intact — you can
   roll back by reinstalling the previous binary.
2. **`~/.claude/settings.json` env merge.** Same as the cloned-repo
   path: yakOS re-merges its env block, preserving all other keys. A
   timestamped backup is written first.

### Cloned-repo path

`yakos update` performs three operations:

1. **Symlink refresh.** `~/.claude/{agents,skills,rules,playbooks}/`
   contain per-file symlinks pointing into the framework's `lib/`. After
   `git pull`, the symlinks resolve to the new files automatically — no
   recreation needed unless a file was renamed/deleted. `update` walks
   the symlink tree and removes dangling links + adds new ones.
2. **`~/.claude/settings.json` env merge.** yakOS owns one block of env
   vars in your settings.json (the Agent Teams experimental flag, etc.).
   `update` re-merges that block, leaving every other key untouched.
   A timestamped backup is written at `~/.claude/settings.json.yakos-bak-<iso>`
   first.
3. **Change report.** Lists which files changed since the previous
   yakos commit on this machine and whether any of them were ones you
   had locally modified.

`yakos doctor` runs the standard health check; `doctor --fix` (v0.7+)
auto-remediates common issues.

`yakos migrate <project>` (v0.9+) upgrades the project's `.yakos.yml`
schema in place if needed. Pre-v0.7 projects don't have a `.yakos.yml`
and don't need migration.

## What survives an upgrade

- **Auto-memory** at `~/.claude/projects/<encoded>/MEMORY.md` and
  per-project entries — never touched by yakOS update or uninstall.
  This rule supersedes everything else.
- **`~/.yakos-state/`** — gate-log, dispatch-log, launch-log,
  runtime-probes, and the canonical memory store. Untouched by update.
- **Per-project `~/agent-control/<name>/`** work directories,
  decisions.md, archived sessions. Untouched.
- **Project repos.** All hooks, settings, and agent files in your
  project's `.claude/`, `.codex/`, `.gemini/` are project-owned. yakOS
  never edits them on update.
- **Custom frame-overrides.** Files you wrote at `~/.claude/agents/`
  that aren't yakOS symlinks survive untouched.

## What `update` does NOT migrate automatically

- **Hook script content.** If yakOS's reference hook scripts in
  `lib/hooks/` change, project copies at `<project>/scripts/hooks/`
  stay on the old version (yakOS treats those as project-owned after
  init copy). `yakos doctor <project>` reports the drift; `yakos init
  <project> --force` overwrites them. `yakos doctor <project> --fix`
  refreshes the `.framework-hash` siblings only when the project's
  hook content already matches the new framework src — preserving
  intentional project drift.
- **Project `.claude/settings.json`.** Project-owned; never edited.
- **External runtime CLIs** (claude / codex / gemini). Each has its
  own update path — `npm install -g @anthropic-ai/claude-code` etc.
  yakOS doesn't bundle or pin them.

## Schema migrations

When yakOS bumps its `.yakos.yml` schema (e.g. `yakos: 0.7` → `0.8`),
`yakos migrate <project>` does the in-place upgrade. Each migration
is documented with its concrete change so the operator can audit
before applying:

| From | To | What changed |
|---|---|---|
| (no version) → 0.7 | First release of `.yakos.yml`. Identity migrate; just stamp `yakos: 0.7`. |
| 0.7 → 0.8 | Added optional `max-cost-per-task` and `max-duration-s` agent frontmatter (v0.8). No `.yakos.yml` change required. Identity migrate. |
| 0.8 → 0.9 | No schema change. Identity migrate. |
| 0.9 → 0.39 | No schema change. Binary-install path now available via `curl\|sh`; lib is embedded in the Go binary and materialized to `~/.local/share/yakos/<version>/`. The `YAKOS_IMPL=go` var is persisted by the installer. Identity migrate for `.yakos.yml`. |

Migrations are idempotent and back up the original to
`<project>/.yakos.yml.yakos-bak-<iso>` before any edit.

## Skip-the-rest path: jumping >2 versions

The path above (`git pull` + `update` + per-project `doctor --fix` +
`migrate`) is monotonic: jumping from v0.2 directly to v0.9 runs the
same steps and produces the same result as walking through each
intermediate version. There is no "upgrade only one minor at a time"
constraint.

The one caveat: **if you customized framework files** (rare —
discouraged in PHILOSOPHY.md), the changes get clobbered by symlink
refresh. If you've forked yakOS, follow your fork's git workflow
instead of `yakos update`.

## Uninstall

```sh
# Remove yakOS-owned symlinks + ~/.yakos pointer + env block
~/code/yakos/cli/yakos uninstall
```

This is reversible — `yakos install` from a yakos clone reinstates it.
What `uninstall` does:

- Removes `~/.claude/{agents,skills,rules,playbooks}/<name>` symlinks
  whose targets are inside the yakos repo.
- Removes the `~/.yakos` pointer file.
- Reverts the yakOS-owned env block in `~/.claude/settings.json`,
  preserving everything else; backup at `settings.json.yakos-bak-<iso>`.

What `uninstall` does **not** touch (you have to do these manually
if you want a complete wipe):

- `~/.yakos-state/` — audit logs, runtime probes, memory store.
- `~/agent-control/` — per-project work directories.
- `~/.claude/projects/` — auto-memory. **NEVER touched** by yakOS,
  no matter what flags you pass. This is by design.
- The yakos repo clone itself — `rm -rf ~/code/yakos` if you want
  the source gone.
- Project repos' `.claude/`, `.codex/`, `.gemini/` directories.
  Those are owned by the project; remove them per-project if you're
  retiring the project.

### Full nuclear wipe

```sh
~/code/yakos/cli/yakos uninstall
rm -rf ~/.yakos-state ~/agent-control ~/code/yakos

# Optional: per-project residue
# (only if you're retiring the project)
for proj in /path/to/proj1 /path/to/proj2; do
    rm -rf "$proj/.claude" "$proj/.codex" "$proj/.gemini" "$proj/scripts/hooks"
done
```

Auto-memory at `~/.claude/projects/` is left intact even after a
nuclear wipe — you have to remove that explicitly:

```sh
# DESTRUCTIVE: removes every project's memory across every CLI tool
# that uses ~/.claude/projects. Don't run this casually.
# rm -rf ~/.claude/projects
```

## Rollback

If an upgrade breaks something:

```sh
cd ~/code/yakos
git log --oneline                # find a known-good commit
git checkout <sha>                # roll back the framework code
./cli/yakos update                # re-link to the rolled-back version
```

`~/.yakos-state/launch-log.ndjson` and `dispatch-log.ndjson` will
keep recording entries with the old version stamps so the audit trail
shows when you rolled back. Schema migrations are idempotent — a v0.9
project running against v0.8 yakos uses the v0.8 reader, which ignores
unknown fields gracefully.

## When to re-init a project

`yakos init <name> --project <path>` is normally a one-time bootstrap.
Re-running it is **safe**:

- Existing files in `<project>/scripts/hooks/` are skipped unless
  `--force`.
- Existing `.claude/settings.json`, `.claude/path-allowlist.json`,
  `~/agent-control/<name>/settings.local.json`, and
  `~/.claude/projects/<encoded>/MEMORY.md` are preserved if present.

Re-init is the right move when:

- You want fresh hook scripts (use `--force`).
- The project repo moved on disk (just re-init at the new path).
- You upgraded yakOS through several major versions and want
  belt-and-braces certainty that all bootstrap files exist.

## See also

- [README.md](README.md) — install / first-time bootstrap
- [docs/runtime-matrix.md](docs/runtime-matrix.md) — runtime
  capabilities (relevant when adding a new runtime to an existing
  project)
- [docs/memory-portability.md](docs/memory-portability.md) — how
  memory survives across runtimes during upgrades
- [PHILOSOPHY.md](PHILOSOPHY.md) — the trust-but-verify posture
  that informs why uninstall is conservative by default
