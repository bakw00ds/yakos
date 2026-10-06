# Per-agent dollar budgets (K-119)

A dollar budget caps what one agent may spend over a window. When an agent
reaches its limit, new dispatches for that agent are refused until the limit is
raised, the window is reset, or the window rolls over. The idea comes from
Paperclip's per-agent budget hard stops. It is insurance against a runaway
agent, not a steady-state saving: the efficiency audit of 2026-10-01 found the
supervisor at $1,763 and 511 failing runs at $58, none of which any cap
flagged.

## States

| State | Meaning | Effect |
|---|---|---|
| `off` | No limit configured | Nothing. This is the default for most agents. |
| `ok` | Below the warning percentage | Nothing. |
| `warning` | At or above `warn_pct` (default 80%) | A line on stderr at dispatch, a warning in `yakos doctor`, a row in `yakos budget status`. |
| `hard_stop` | At or above 100% of the limit | `yakos dispatch` refuses the run and exits 4. |

A run already in flight is never killed. (The supervisor is the one exception to the 1x refusal in `yakos dispatch`; see "The supervisor at hard stop".) Spend is recorded when a run finishes
(the `usage.total_cost_usd` on its `dispatch_finished` event), so two
dispatches started together near the limit both pass the pre-flight, and the
next dispatch after they finish is refused. The overshoot is bounded by the cost
of the runs in flight.

## Windows

- `monthly` (default): the local calendar month. A new month starts at zero.
- `lifetime`: all recorded spend, never rolls over.

## Commands

```
yakos budget status [--json] [--by-project] [--project <path>]
yakos budget set <agent> <usd> [--window monthly|lifetime] [--max-model haiku|sonnet|opus|fable]
yakos budget reset <agent>
yakos budget check <agent> [--project <path>] [--json]
```

- `status` shows one row per agent that has a budget, whether it comes from the
  policy file, a built-in default, or only from the current project's
  `agent_budgets:` (the project is `--project`, else the working directory):
  state, spend, limit, percentage, window and where the limit came from.
  `--by-project` lists each agent's spend per project (the `project` recorded on
  each dispatch-log entry). `--json` carries the same, top 10 projects per agent.
- `set --max-model` also records a model-tier ceiling (see below).
- `set` writes `~/.yakos-state/budget-policy.yml`. `set <agent> 0` turns the
  limit off, including a built-in default.
- `reset` starts the agent's current window over. Spend already logged stops
  counting. The dispatch-log is not edited. A reset belongs to the window it was
  made in and does not carry into the next month.
- `check` is the pre-flight for hooks and scripts. Exit 0 means the agent may
  run (also for `warning`, `off`, and any read failure, which fails open). Exit 4
  means `hard_stop`. It never exits 2, because exit 2 is the Claude Code hook
  block code and a budget refusal must never block a tool call. The first line is
  machine-readable and stable:
  `reason=<code> state=<state> agent=<agent> spent_usd=... limit_usd=... window=...`
  with `reason` one of `budget_off`, `budget_ok`, `budget_warning`,
  `budget_exhausted` (`--json` has the same `reason` field). `--json` also
  carries `"read_failed": true`, and only then, when the spend could not be read
  (an unreadable dispatch log, or an internal error): the other numbers then
  describe an empty ledger, not a measurement, and the exit stays 0 (it fails
  open). The field is set from the error itself. A hook relies on it and never on
  the words on stderr or in `warnings`, which carry text a project controls (a
  repeated `agent_budgets` key in `.yakos.yml` is echoed back in the YAML error).
- `yakos doctor` lists agents in `warning` or `hard_stop`, and prints nothing
  about budgets when every agent is healthy. The supervisor is special: at
  `hard_stop` doctor reports an **error**, "LLM supervision disabled: supervisor
  budget exhausted", because `block_on_critical` silently stops protecting
  anything while it is exhausted; at `warning` it is a warning.

## Default limits

Agents have no budget unless one is configured, with two exceptions chosen from
the dispatch-log (efficiency audit 2026-10-01):

| Agent | Default | Observed | Basis |
|---|---|---|---|
| `supervisor` | $100 per month (dispatch stops at 2x) | $58 in Sep 2026 (107 calls on haiku); $800-900 per month before the switch | About 1.7x the current rate. It would have stopped the May and June burn in the first week. |
| `librarian` | $40 per month | $22 in May, $159 in Jun (36% of runs failed); none since | About 1.8x the healthy May figure. |

The supervisor limit is a placeholder sized for today's rate. Once K-116 and
K-117 land and routine supervisor cost drops, lower it to about $25 (a few times
the new steady state, still far below the old $800 burn). That is the operator's
call, not automatic.

Override either in the policy file, or turn it off with `yakos budget set
supervisor 0`. A malformed or untrusted policy file never disables these two
defaults.

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
```

Resolution order for one agent: its entry under `agents:`, then the built-in
default, then `default:`, otherwise off.

## Projects may only lower a limit

A project `.yakos.yml` can carry:

```yaml
agent_budgets:
  backend: 20
```

This follows the trust rule of the decision provider policy (ADR-0009). A project
value below the user-level limit applies, and so does one for an agent that has
no user-level limit (off becomes limited). A value above the user-level limit, or
zero or negative, is ignored with a warning. A project cannot change the window
or the warning percentage. The user-level file is the only place to raise or
disable a limit.

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

`dispatch.Run` lowers any dearer tier to the ceiling and prints a notice; an
explicit `--model` on the bash passthrough is lowered the same way. A model
that is not one of the four tiers is left alone. Unset means no ceiling.

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

Spend is the sum of `usage.total_cost_usd` over `dispatch_finished` events in
`dispatch-log*.ndjson`, bucketed by agent and by local calendar month. Events
without a cost count as zero.

To keep the pre-flight fast, a derived cache `budget-spend.json` (mode 0600)
holds per-agent totals and the byte offset consumed from the current log. Each
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
| `parse` | what it printed is not a budget: not JSON, JSON without a numeric `limit_usd`, or a failing `jq` | bash |
| `read_error` | the spend could not be read: the CLI's JSON says `read_failed: true` (its numbers then read "ok, nothing spent"), also when it failed inside; the Go twin sees the same error in-process | both |

The bash hook never reads the CLI's stderr: it carries text a project controls,
and a hook that took it for evidence could be made to fail open by a project's own
config. The Go twin evaluates in-process, so only `read_error` exists there. A budget
that is switched off (a limit of 0) is not a failure and logs nothing, and neither
is a CLI that prints nothing and exits 0: it has no budget to report.

## Where it is enforced

- `dispatch.Run` (the Go dispatch, also used by the daemon and MCP paths).
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

The exemption is decided inside the hook. There is no env var or flag that
carries it. To make that work without one, `yakos dispatch` itself refuses the
supervisor only at 2x its limit (every other agent at 1x): the hook is the 1x
gate for routine launches and `dispatch` is the 2x backstop. The Go twin
evaluates the budget in-process. The bash twin cannot, so at each launch
decision (never per event) it runs `yakos budget check supervisor --json`, and
fails open if the CLI is missing or too old to have `budget`. A project's
`agent_budgets:` can only lower the limit, never loosen it. Log records carry a
stable `budget_reason` (`budget_warning`, `budget_exhausted` or, when the
budget could not be read, `budget_unavailable`).

Because `yakos dispatch` refuses the supervisor only at 2x, any same-user caller
can run `yakos dispatch supervisor` between 1x and 2x, so routine supervisor
spend can overshoot its limit by at most one limit. This is accepted: the hook
and agents run as the same user, so no check here could tell them apart. The
`budget-guard` hook does block an agent-issued `yakos dispatch supervisor`,
as a speed bump.

The count ceiling (3x the launch cap) and the dollar ceiling (2x the limit)
each write their own synthetic CRITICAL finding, once per session, with
separate flags, so one never suppresses the other.

Only the LLM tier stops at hard stop. The local pre-filter and its logging, and
the Jev shadow decision, keep running.

## Not covered

- In-flight runs are not stopped.
- Spend is only what the dispatch-log records. Interactive Claude Code sessions
  and teammates that never go through `yakos dispatch` are not counted.
