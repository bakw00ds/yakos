# Per-agent token and dollar budgets (K-119, K-136)

A budget caps what one agent may use over a window, as a token limit, a dollar
limit, or both. When an agent reaches either limit, new dispatches for that agent
are refused until the limit is raised, the window is reset, or the window rolls
over. The idea comes from Paperclip's per-agent budget hard stops. It is insurance
against a runaway agent, not a steady-state saving: the efficiency audit of
2026-10-01 found the supervisor at $1,763 and 511 failing runs at $58, none of
which any cap flagged.

Tokens are the primary unit (K-136). A token limit counts the input, output and
cache tokens of every run of the agent, whatever model ran it and however it was
billed. A dollar limit counts only runs billed per API call, never a subscription
harness and never a local model. See "Tokens first, dollars for API runs".

## States

| State | Meaning | Effect |
|---|---|---|
| `off` | No limit configured | Nothing. This is the default for most agents. |
| `ok` | Below the warning percentage | Nothing. |
| `warning` | At or above `warn_pct` (default 80%) of either limit | A line on stderr at dispatch, a warning in `yakos doctor`, a row in `yakos budget status`. |
| `hard_stop` | At or above 100% of either limit | `yakos dispatch` refuses the run and exits 4. |

A run already in flight is never killed. (The supervisor is the one exception to the 1x refusal in `yakos dispatch`; see "The supervisor at hard stop".) Usage is recorded when a run finishes
(the `usage` object and `billing` on its `dispatch_finished` event), so two
dispatches started together near the limit both pass the pre-flight, and the
next dispatch after they finish is refused. The overshoot is bounded by the size
of the runs in flight.

## Tokens first, dollars for API runs

Every `dispatch_finished` event written by the Go dispatcher carries a `billing`
value: `subscription` (the harness ran under the operator's login), `api` (it was
billed per call) or `local`. The dispatcher reads it from the credentials the
harness inherits: an API key of the harness's own provider in its environment
(`ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` or a Bedrock/Vertex/Foundry switch
for claude; `OPENAI_API_KEY` or `CODEX_API_KEY` for codex; `GEMINI_API_KEY`,
`GOOGLE_API_KEY` or `ANTIGRAVITY_API_KEY` for agy) means `api`, and none means
`subscription`. Only the presence of a variable is read, never its value.

| What | Subscription or local run | API run | Row from before K-136 |
|---|---|---|---|
| Tokens (`limit_tokens`) | counted | counted | counted, when it has a `usage` object |
| Dollars (`limit_usd`) | **not counted** | counted | counted, as before |
| The harness's reported cost | kept as `api_equivalent_usd`, `usage.total_cost_usd` is 0 | `usage.total_cost_usd` | `usage.total_cost_usd` |

- `limit_tokens` is the total of fresh input, output, cache-read and
  cache-creation tokens. It trips like `limit_usd`: `ok`, then `warning` at
  `warn_pct`, then `hard_stop` at 100%, and the same window and reset rules.
  Set it with `yakos budget set <agent> --tokens 5m` (also `500k`, `1.5m`,
  `2b`, or a plain number) or in the policy file; `0` turns it off.
- An agent with both limits is at `hard_stop` when either is reached, and the
  refusal names the one that tripped. `pct` in `status` is the larger share.
- `api_equivalent_usd` is what a subscription run would have cost at API
  rates, as the harness reported it (only claude reports a figure). It is
  informational: it is never spend and never counts toward `limit_usd`.
- A row with no `billing` field (written by the bash dispatcher, or by the Go
  dispatcher before K-136) keeps counting its dollars, so a dollar budget does
  not reset itself when you upgrade. The bash dispatcher is still the default
  `yakos dispatch` implementation, so its rows stay on the old rule until the
  Go dispatcher becomes the default.
- Codex and agy report tokens and no dollar figure, so under either billing they
  show tokens and no dollars anywhere (an API-billed run has no figure to count).
  Only a token limit can stop them.
- The built-in supervisor and librarian budgets carry a token limit as well as a
  dollar limit, so they still stop for a subscription operator, whose runs cost no
  dollars and never move `limit_usd`. "Default limits" gives the numbers and the
  arithmetic.

Limits of the billing detection: only the environment the dispatcher itself
passes to the harness is read. A harness signed in through a pay-per-token console
login, or given a key by its own settings (an `apiKeyHelper` or an `env` block in
the harness's settings file), has no key in that environment and reads as a
subscription. If that is your setup, set a token limit, which counts every run.
The model registry (plan phase P1) will let you state the billing of a model in a
user-level file.

## Windows

- `monthly` (the default for an agent with no window set): the local calendar
  month. A new month starts at zero.
- `lifetime`: all recorded spend, never rolls over.

## Commands

```
yakos budget status [--json] [--by-project] [--project <path>]
yakos budget set <agent> [<usd>] [--tokens <n>] [--window monthly|lifetime] [--max-model haiku|sonnet|opus|fable]
yakos budget reset <agent>
yakos budget check <agent> [--project <path>] [--json]
```

- `status` shows one row per agent that has a budget, whether it comes from the
  policy file, a built-in default, or only from the current project's
  `agent_budgets:` (the project is `--project`, else the working directory):
  state, tokens used and their limit, dollars spent and their limit, percentage,
  window and where the limit came from. The table leads with `TOKENS` and
  `TOKEN LIMIT`, ahead of the dollar columns.
  `--by-project` lists each agent's dollar spend per project (the `project` recorded on
  each dispatch-log entry). `--json` carries the same, top 10 projects per agent,
  plus `limit_tokens`, `stop_tokens`, `spent_tokens` and `tokens_pct`.
- `set --max-model` also records a model-tier ceiling (see below).
- `set` writes `~/.yakos-state/budget-policy.yml`. `set <agent> 0` turns the
  dollar limit off, including a built-in default. `set <agent> --tokens <n>` sets
  a token limit and leaves the dollar limit as it was; give `<usd>`, `--tokens`,
  or both. The agent has one window, shared by both limits, so `set` without
  `--window` keeps the agent's current window (its own entry's, else the policy
  `default:` window, else monthly): adding a token limit never turns a lifetime
  dollar limit monthly. Give `--window` to change it. The supervisor and
  librarian also have a built-in token
  limit, which stays on when the dollar limit is turned off: `set supervisor 0`
  prints a note saying so, and the budget is off only after
  `set supervisor 0 --tokens 0`.
- `reset` starts the agent's current window over. Spend already logged stops
  counting. The dispatch-log is not edited. A reset belongs to the window it was
  made in and does not carry into the next month. `reset <agent> --project <path>`
  (the working directory by default, as for `status`) reads the project's
  `supervisor: agent:` name, so a renamed supervisor is reset in the window it is
  counted in: the combined one (see "Projects may only lower a limit").
- `check` is the pre-flight for hooks and scripts. Exit 0 means the agent may
  run (also for `warning`, `off`, and any read failure, which fails open). Exit 4
  means `hard_stop`. It never exits 2, because exit 2 is the Claude Code hook
  block code and a budget refusal must never block a tool call. The first line is
  machine-readable and stable:
  `reason=<code> state=<state> agent=<agent> spent_usd=... limit_usd=... window=...`
  with `reason` one of `budget_off`, `budget_ok`, `budget_warning`,
  `budget_exhausted` (`--json` has the same `reason` field). For an agent that
  has a token limit, the supervisor and librarian always, the line ends with
  ` spent_tokens=<n> limit_tokens=<n>`; for every other agent it is unchanged. The
  state is `hard_stop`, and the exit code 4, when either limit is reached.
  `--json` also carries `"read_failed": true`, and only then, when the spend could
  not be read (an unreadable dispatch log, or an internal error): the other
  numbers then describe an empty ledger, not a measurement, and the exit stays 0
  (it fails open). The field is set from the error itself. A hook relies on it and
  never on the words on stderr or in `warnings`, which carry text a project
  controls (a repeated `agent_budgets` key in `.yakos.yml` is echoed back in the
  YAML error). Every number in the JSON is finite (the share used is clamped, an
  off unit prints 0), so it always parses, and a status that could not be encoded
  anyway prints `{"agent": ..., "read_failed": true}`, never an empty line.
- `yakos doctor` lists agents in `warning` or `hard_stop`, and prints nothing
  about budgets when every agent is healthy. An agent with a token limit is
  described in tokens first, with the limit that was reached named (token, dollar
  or both) and the matching flag to raise it: `--tokens <n>` for a token stop,
  `<usd>` for a dollar stop. The supervisor, and the agent a project names as its
  supervisor, are special: at `hard_stop` doctor reports an **error**, "LLM
  supervision disabled: supervisor budget exhausted", because `block_on_critical`
  silently stops protecting anything while it is exhausted; at `warning` it is a
  warning.

## Default limits

Agents have no budget unless one is configured, with two exceptions chosen from
the dispatch-log (efficiency audit 2026-10-01):

| Agent | Default (per month) | Observed | Basis |
|---|---|---|---|
| `supervisor` | $100 and 33,000,000 tokens (dispatch stops at 2x each) | $58 and 15.1M tokens in Sep 2026 (107 calls on haiku); $800-900 and 263M to 292M tokens per month in May and June, before the switch | About 1.7x the current dollar rate and 2.2x the current token volume. It would have stopped the May and June burn in the first week. |
| `librarian` | $40 and 13,000,000 tokens | $22 and 2.9M tokens in May, $159 and 15.4M in Jun (36% of runs failed); none since | The dollar limit is about 1.8x the healthy May figure. The token limit is about 4.5x the healthy May volume and still trips inside June's total. |

The supervisor limit is a placeholder sized for today's rate. Once K-116 and
K-117 land and routine supervisor cost drops, lower it to about $25 (a few times
the new steady state, still far below the old $800 burn). That is the operator's
call, not automatic.

### How the built-in token limits were sized

Dollars mean nothing for a subscription run, so a dollar ceiling alone would leave
the supervisor and librarian with no hard stop for an operator on a subscription.
Each token limit is the dollar ceiling converted at the Sonnet reference rate of $3
per million tokens (the Sonnet price in the efficiency audit of 2026-10-01: $3 input
and $15 output per million, cache reads at 0.1x, cache writes at about 1.25x),
rounded down to a whole million:

```
supervisor   $100 / ($3 per 1M tokens) = 33.3M  ->  33,000,000
librarian     $40 / ($3 per 1M tokens) = 13.3M  ->  13,000,000
```

A token limit counts all four token kinds of every run of the agent, whatever the
run was billed, so it trips for subscription, API and local runs alike. Checked
against the dispatch-log (the four kinds summed, cost as the CLI reported it): the
supervisor's blended cost was $2.76 to $3.83 per million tokens in May, June,
September and early October 2026, so the conversion holds for it and its token
limit lands where its dollar limit would. The librarian ran on a dearer model,
$7.77 and $10.34 per million tokens in May and June, so its token limit is looser
than its dollar limit: it does not trip a healthy month and does trip June.
`TestBuiltinTokenLimits_FollowTheDollarCeilings` ties the numbers in the code to
this arithmetic.

Override either limit in the policy file. `yakos budget set supervisor 0` turns the
dollar limit off, `--tokens 0` turns the token limit off, and the budget is off only
when both are. `yakos budget set supervisor --tokens 60m` raises the token limit. A
malformed or untrusted policy file never disables these defaults.

## Policy file

`~/.yakos-state/budget-policy.yml` (the state directory, `YAKOS_DISPATCH_LOG`
when set). It must be a regular file you own and not group or world writable;
otherwise it is ignored whole (built-in defaults apply) and `yakos doctor`
reports why.

```yaml
default:            # optional global default for agents not listed below
  limit_usd: 25
  window: monthly
  warn_pct: 80
agents:
  supervisor:
    limit_usd: 100
    window: monthly
    warn_pct: 90
  code-reviewer:
    limit_usd: 150
  general-codex:
    limit_tokens: 5000000   # fresh input + output + cache tokens, any billing; 0 = off
```

Resolution order for one agent: its entry under `agents:`, then the built-in
default, then `default:`, otherwise off. The two limits resolve independently, so
an entry may set one and inherit the other. A project cannot set a token limit
(`agent_budgets:` is dollars only), so only this file can.

A value in the file that is out of range is ignored with a warning, and the limit
it would have replaced stays (the built-in one, or the `default:` entry's), so a
typo or a corrupt edit can neither switch a built-in budget off nor make a limit,
its stop or its percentage infinite. Out of range is:

- a dollar limit that is negative, NaN, positive or negative infinity, above
  $1,000,000,000 (so the stop of twice the limit stays a finite number), or positive
  and below $0.01 (so the share used stays a finite number);
- a token limit that is negative, not a whole number (`1500000.5`), not a number at
  all (`5m`, a list), too large for 64 bits, or above 2^50 (about 10^15).

`0` is how you turn a limit off, on purpose, and a limit exactly at a bound ($0.01,
$1,000,000,000, 2^50 tokens) is accepted. A bad value costs only itself: the rest
of its entry and of the file is read as usual. The warning is printed by
`yakos budget check` and on stderr before a dispatch, and `status --json` carries
it in `warnings`. `yakos budget set` refuses the same dollar values.

## Projects may only lower a limit

A project `.yakos.yml` can carry:

```yaml
agent_budgets:
  backend: 20
```

This follows the trust rule of the decision provider policy (ADR-0009). A project
value below the user-level limit applies, and so does one for an agent that has
no user-level limit (off becomes limited). A value above the user-level limit, or
zero, is ignored with a warning, and so is one outside the range a policy value may
have (negative, NaN, infinite, above $1,000,000,000 or below $0.01): it is never
applied, so a project can lower a limit or give an unlimited agent one, never an
unbounded one. A project cannot change the window or the warning percentage. The
user-level file is the only place to raise or disable a limit.

The project file is read only if it is a regular file of at most 1 MiB. A symlink,
a FIFO, a device or a larger file is refused with a warning, and the project then
contributes no limits and no supervisor name.

A project can also name the agent its supervisor runs as:

```yaml
supervisor:
  agent: watchdog
```

The supervisor hook launches `yakos dispatch watchdog` and asks for the budget of
that name. A committed file must not be able to loosen a budget, so naming an agent
the supervisor never does: the agent is budgeted at the stricter of its own limits
(its entry in the user-level file, else its own built-in, else the `default:` entry)
and the supervisor's (the supervisor's entry, else its built-in), combined into one
limit on the agent's one spend counter, unit by unit:

- **Amount:** the smaller of the two limits, for dollars and for tokens. A unit that
  is not limited on one side (never set, or turned off with `0`) counts as unlimited
  there, so an agent with no limit of its own gains the supervisor's, and an agent
  with a limit keeps it when it is the smaller.
- **Stop:** the smaller of the two sides' dispatch stops, in absolute terms, for each
  unit. A side's stop is its amount times its stop factor (2 for the supervisor, 1 for
  every other agent). It is not the stop of whichever side has the smaller amount: an
  agent with its own 50,000,000 tokens (stop 50,000,000) named as the supervisor gets
  33,000,000 tokens with a stop of 50,000,000, not the supervisor's 66,000,000.
- **Window:** lifetime if a side that has a limit is lifetime, monthly otherwise. A
  side with no limit in either unit contributes no window, so an agent with no limit
  of its own takes the supervisor's window, even under a lifetime `default:`.
- **Warning level:** the earlier of the two.

The result is never looser than checking the agent's own limit and the supervisor's
separately, and in the mixed case it can be stricter, because the agent has one
counter and so one window: an own $200 lifetime limit beside the supervisor's $100
monthly one becomes $100 lifetime. For example, a project that names `backend`
changes nothing for an operator who gave `backend` a limit of $5 (it stays $5 with a
$5 stop), nothing for the librarian (it stays $40 and 13,000,000 tokens, with no
doubled stop), and gives an agent that had no limit the supervisor's $100 and
33,000,000 tokens with the supervisor's stop and the supervisor's window. That is
monthly for the built-in supervisor budget, and lifetime only if the operator's
`supervisor:` entry says lifetime. A `default:` entry that names a lifetime window
does not matter to such an agent, because it has no limit of its own to count. `yakos budget check`, `status`,
`doctor` and `reset` all use the combined limit, and `check --json` prints it, so the
two hooks, the console and `dispatch` agree. Both hooks read the name (the Go hook as
YAML, the bash hook with a line scan), and every name either of them arrives at is
treated as the supervisor. The renamed agent has its own spend counter (K-160 tracks
counting spend against the supervisor role instead of the name). `status`, `reset`
and `doctor` take `--project <dir>`, else the working directory, and list or reset
it for that project; `doctor` uses the project for its Agent budgets section only,
and its project checks (hook drift, hook binaries, the pre-push gate, project rules)
still run only for a positional project path. The model ceiling below takes the project too, so a
renamed supervisor keeps the supervisor's `sonnet` ceiling.

## Model ceiling

A project's `.yakos.yml` can name a dearer model for an agent (the supervisor
hook passes `supervisor.model`), multiplying cost against a budget that every
project shares. A user-level `max_model` caps that:

```yaml
agents:
  supervisor:
    limit_usd: 100
    max_model: haiku    # haiku < sonnet < opus < fable
```

The supervisor has a built-in ceiling of `sonnet`, so a project's
`supervisor.model: opus` cannot drain the shared budget out of the box. Only the
user-level policy changes it (`yakos budget set supervisor <usd> --max-model
fable` lifts it).

`dispatch.Run` lowers any dearer model to the ceiling and prints a notice; an
explicit `--model` on the bash passthrough is lowered the same way. The ceiling is
a cost class, so it governs every runtime, using the model registry's ranking
(`docs/routing.md`, "Tier classes and Clamp"): on agy a dearer model is replaced by the agy
model of the ceiling's class, and a model the registry cannot rank (an id no alias
names, the harness default, any codex model until you map codex aliases) is
refused with an error that names the model, the ceiling and `yakos models show
<id>`, never run unchecked. Unset means no ceiling. An agent a project names as its
supervisor gets the lower of its own ceiling and the supervisor's.

## Operator-only controls

`yakos budget set` and `reset`, and edits to the budget state files
(`budget-policy.yml`, `budget-spend.json`, `budget-resets.json`, `budget.lock`,
and the dispatch log `dispatch-log*.ndjson`, whose truncation would wipe spend),
and `yakos dispatch supervisor`, are blocked for agents by the `budget-guard` hook (both twins), in every
project and without any `.yakos.yml`. There is no `hook-bypass.md` scope for it,
because an agent can write that file. Read-only commands (`budget status`,
`budget check`, `cat` of a state file) pass. The operator runs the blocked ones
from their own shell. Quotes and backslashes are stripped before matching, so `yakos budget "set"` and
`re\set` are caught. Variable indirection and `$(...)` are not: they are inherent
limits of matching command text. This is a speed bump against an agent lifting its
own stop, not a sandbox: any same-user code can still edit the state directory.
Appends by yakos itself are unaffected, since they do not go through tool calls.

## How spend is computed

Usage is summed over `dispatch_finished` events in `dispatch-log*.ndjson`,
bucketed by agent and by local calendar month. Tokens are the four counts of the
`usage` object (`input_tokens`, `output_tokens`, `cache_read`, `cache_creation`)
of every event, whatever its billing; an event without a `usage` object adds none
(a size estimate such as `est_input_tokens` is not a count and is never used).
Dollars are `usage.total_cost_usd`, counted only for an event whose `billing` is
`api` or that has no `billing` field (see "Tokens first, dollars for API runs");
events without a cost count as zero. A count that is negative or beyond any real
run is read as 0, so one corrupt line cannot move a total. Codex rows written by the
bash dispatcher hold the cached tokens inside `input_tokens` while Go rows put them
in `cache_read`; the total adds all four, so both agree (docs/runtime-matrix.md,
"Usage fields by harness").

To keep the pre-flight fast, a derived cache `budget-spend.json` (mode 0600)
holds per-agent totals (dollars and tokens) and the byte offset consumed from the
current log. A cache written before token limits existed is rebuilt from the log
once. Each
pre-flight stats the log and reads only the bytes appended since the last one.
The cache is rebuilt from the log (rotated archives included) when it is missing,
corrupt, untrusted (a symlink, owned by another user, or group/world writable,
the same check as the policy file), built in a different local time zone, or the
log was rotated or truncated, detected by a hash of the log's first bytes. A trailing partial line is left for the next pass. Resets live in
`budget-resets.json`, separate from the cache, so a rebuild never loses them. An
untrusted resets file is ignored (it cannot be rebuilt) with a warning, so a
planted file cannot lift a stop. A reset belongs to the window kind it was made
in: switching an agent from monthly to lifetime drops it.

`budget set` and `reset` take the same lock, so parallel sets never lose an
update. Updates take a lock file in the state directory (created exclusively, broken
after 10 s, the same on every OS). If the lock cannot be taken within 2 s the
pre-flight computes the answer in memory and skips the cache write.

Measured on a synthetic log of about 4.5 MB (20,000 lines; the real log is
about 11,400 lines of the same size):

| Operation | Time |
|---|---|
| Pre-flight, steady state | about 0.1 ms |
| Pre-flight after one new event | about 0.5 ms |
| Cold rebuild from the whole log | about 30 ms, once |

An agent with no budget costs only a policy-file read.

## Failure posture

The budget is a cost guard, not a security control, so it fails open: an
unreadable log, an untrusted policy file or a failed cache write prints a notice
and the dispatch proceeds. Only a computed `hard_stop` refuses.

The supervisor hook (bash and Go twin) fails open the same way when it cannot
read the budget at a launch decision, and says so: one WARN record in the hook
log, `supervisor budget unavailable (cause: <cause>)`, with `budget_reason:
budget_unavailable` and a `cause` that names what failed. It comes ahead of the
launch's own record, goes to the hook log only (nothing on stderr), and does not
change the decision.

| `cause` | Meaning | Twin |
|---|---|---|
| `timeout` | the `yakos budget check` child outlived its 2 s wall-clock bound and was killed | bash |
| `no_output` | it printed nothing and exited non-zero, or could not run (a CLI too old to have `budget`, one that crashed, a missing binary) | bash |
| `parse` | what it printed is not a budget: not JSON, JSON without a numeric `limit_usd`, `spent_usd` and `stop_usd` and a string `state` (the token fields are optional: one that is absent or not a number reads as 0, except `stop_tokens`, which then reads as the token limit), or a failing `jq` | bash |
| `read_error` | the spend could not be read: the CLI's JSON says `read_failed: true` (its numbers then read "ok, nothing spent"), also when it failed inside, an unencodable status included; the Go twin sees the same error in-process | both |

The bash hook never reads the CLI's stderr: it carries text a project controls,
and a hook that took it for evidence could be made to fail open by a project's own
config. A CLI built before the `read_failed` field existed is a normal CLI to the
hook: the absence of the field is an ordinary read, so everything still works, and
the bash hook simply cannot report an unreadable spend log then (the Go twin still
does, in-process). The Go twin evaluates in-process, so only `read_error` exists
there. A budget that is switched off, with no limit of either kind (a dollar limit
of 0 and a token limit of 0 or absent), is not a failure and logs nothing, and
neither is a CLI that prints nothing and exits 0: it has no budget to report. The
bash hook tests `read_failed` before any limit, so a zero limit can never turn an
unreadable spend log into "off".

## Where it is enforced

- `dispatch.Run` (the Go dispatch, also used by the daemon and MCP paths).
- `dispatch.RunStream`, the console's one-shot Chat turns and the gRPC `Stream`.
- The console's interactive Chat sessions, on both engines, before a new session
  starts and before each follow-up message is delivered (`dispatch.PreflightBudget`,
  the same check as the two above).
- `yakos dispatch` handed to the bash implementation: `main` checks the budget
  before the passthrough, so the bash path cannot bypass it.
- The supervisor hook launches the supervisor through `yakos dispatch`, so a
  refused supervisor run exits 4 from that child. The hook itself stays fail-open
  and never exits 2. A hook that wants to skip the launch cleanly can call
  `yakos budget check supervisor`, which exits 0 or 4 and never 2.

### The supervisor at hard stop

The supervisor-stream hook (bash and Go twin) wires the budget into its launch
gate, next to the launch cap:

| Supervisor spend | Routine launch | High-risk launch (risk regex or sensitive path) |
|---|---|---|
| below the warning level | runs | runs |
| warning (default 80%) | runs, with a WARN in the hook log and one stderr line | same |
| at the limit (`hard_stop`) | **refused**: WARN in the hook log, one stderr line, hook exits 0, no "forked async" | runs, with a WARN noting the exemption |
| at 2x the limit | refused | **refused**, and one synthetic CRITICAL finding is written so `block_on_critical` operators are alerted |

"The limit" is either limit, and "2x the limit" is 2x either limit (K-136). The
supervisor's `hard_stop` state is reached at 100% of its dollar limit or of its
token limit (33,000,000 tokens a month by default), and the hook decides from that
state: a supervisor on a subscription, which spends no dollars, has its routine
launches refused at its token limit and its high-risk launches blocked at 2x its
tokens, with the CRITICAL finding. The budget is off only when the supervisor has
no limit of either kind: turning off the dollar limit alone leaves the built-in
token limit gating, and a token-only budget (a dollar limit of 0) is gated at 1x
and 2x its tokens. `yakos dispatch` stays the 2x backstop for both units.

Each hook message names one unit, tokens first. At the limit it is tokens when the
token limit itself has been reached, else dollars; at the warning level it is the
unit with the larger share of its limit (a tie goes to tokens); at the 2x ceiling
it is the unit that is past its own stop. A dollar message and its log record are
the ones they always were. A token message has the same shape with `spent_tokens`
and `limit_tokens` (and `ceiling_tokens` where the dollar record has
`ceiling_usd`) in place of the dollar fields, whole numbers, and its stderr line
reads `850 of 1000 tokens`. The CRITICAL finding reads `Supervisor token-budget
ceiling (N tokens) reached` or `Supervisor dollar-budget ceiling ($N) reached`; a
session gets one budget CRITICAL, whichever ceiling is reached first. The two hook
twins write byte-identical records (a parity test compares them), and an older
`yakos` that prints no token fields leaves the gate dollar-only, as before.

The exemption is decided inside the hook. There is no env var or flag that
carries it. To make that work without one, `yakos dispatch` itself refuses the
supervisor only at 2x its limit (every other agent at 1x): the hook is the 1x
gate for routine launches and `dispatch` is the 2x backstop. The Go twin
evaluates the budget in-process. The bash twin cannot, so at each launch
decision (never per event) it runs `yakos budget check <agent> --json` (the
supervisor, or the agent the project names as its supervisor), and fails open if the
CLI is missing or too old to have `budget`. A project's
`agent_budgets:` can only lower the limit, never loosen it. Log records carry a
stable `budget_reason` (`budget_warning`, `budget_exhausted` or, when the
budget could not be read, `budget_unavailable`).

Because `yakos dispatch` refuses the supervisor only at 2x, any same-user caller
can run `yakos dispatch supervisor` between 1x and 2x, so routine supervisor
spend can overshoot its limit by at most one limit. This is accepted: the hook
and agents run as the same user, so no check here could tell them apart. The
`budget-guard` hook does block an agent-issued `yakos dispatch supervisor`,
as a speed bump.

The count ceiling (3x the launch cap) and the budget ceiling (2x the limit, in
dollars or in tokens) each write their own synthetic CRITICAL finding, once per
session, with separate flags, so one never suppresses the other.

Only the LLM tier stops at hard stop. The local pre-filter and its logging, and
the Jev shadow decision, keep running.

## Not covered

- In-flight runs are not stopped.
- Usage is only what the dispatch-log records. Interactive Claude Code sessions
  you run yourself in a terminal and teammates that never go through
  `yakos dispatch` are not counted. Turns of the console's Chat pane are: each
  interactive turn writes one event pair (surface `console-chat`).
- A Chat pane turn is refused at a hard stop like any other dispatch, whether the
  pane streams one-shot turns or keeps a persistent session, on either engine (the
  Claude CLI engine or the Agent SDK engine). A new interactive session is refused
  before its process starts, and so is every follow-up message to a live one, with
  the one-shot turn's own text (`dispatch failed: budget: dispatch refused: ...`
  in the pane, as an HTTP 429 with that text for a follow-up). The session stays
  alive and takes the next message once the limit is raised or the window is
  reset. A session is held to the agent it started as: a later dispatch on the same
  conversation cannot name another agent to escape its limit, and its turns are
  accounted to the agent that started it. A turn already running is not stopped.
- The Agent SDK engine needs an API key, so its turns are `api` turns: their
  tokens and dollars both count.
