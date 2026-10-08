# Decision providers

A **decision provider** answers a fixed, reviewed set of typed questions about
a redacted state and returns probabilities, never text. yakOS ships one real
provider, TypeSafe's Jev, plus a deterministic `mock` for CI and a `none`
provider that turns everything off. The design and its alternatives are in
[ADR-0009](adr/ADR-0009.md).

A decision provider is **not an agent runtime**. It cannot run an agent, it is
not in `runtime.Known`, and no agent can be routed to it. See
[runtime-matrix.md](runtime-matrix.md#jev-is-not-a-runtime).

`yakos decide` is the entry point. The one automatic caller is the supervisor
pre-filter hook, in shadow mode only (see
[P2b shadow supervisor](#p2b-shadow-supervisor)). With provider `none`, the
default, no hook calls anything.

## Quick start (no key needed)

```sh
YAKOS_DECISION_MOCK=lib/decisions/examples/supervisor-prefilter.mock.json \
  yakos decide supervisor-prefilter --provider mock \
  < lib/decisions/examples/supervisor-prefilter.state.json
```

## Using Jev

1. Export `TYPESAFE_API_KEY` in your shell profile. yakOS reads it at call time
   only. It is never written to `.yakos.yml`, `settings.json`, the decision
   log, or stdout, and it is not forwarded to dispatched runtimes.
2. Turn it on as a **user** decision: `export YAKOS_DECISION_PROVIDER=jev`, or
   put `provider: jev` in `~/.yakos-state/decision-policy.yml`. A project
   `.yakos.yml` cannot enable a provider. It may only set `provider: none` to
   opt that project out of the user-level switch; any other value there is
   ignored, with a warning from `yakos decide` and `yakos doctor`. The default
   is `none`. See the commented `decisions:` block in
   `lib/settings/yakos.yml.template` for the rest of the project settings.
3. Run `yakos doctor --probe-decision` to check the key, config, question-set
   hashes, breaker, and budget. Add `--live` for one real call.

`TYPESAFE_BASE_URL` optionally overrides the endpoint, but only to
`https://*.typesafe.ai` (or a loopback address, for tests and local gateways).
Any other host is refused before a request is built, so a cloned project's
environment cannot point the API key at a collector. Redirects are never
followed.

## `yakos decide`

```
yakos decide <surface> [--provider jev|mock|none] [--shadow]
             [--timeout <dur>] [--session <id>] [--state-file <path>]
             [--sets-dir <dir>] [--config <path>]
             [--local pass|escalate] [--local-trigger <kind>]
yakos decide compare <surface> [--json] [--log <path>] [--sets-dir <dir>]
```

`--local` and `--local-trigger` record what the caller's own heuristic decided
for the same event. They go to the decision log only, never to the provider.

It reads a JSON **object** of named fields on stdin, redacts it, asks the
question set `lib/decisions/<surface>.yaml`, and prints one line of JSON.

Provider selection: `--provider`, then `$YAKOS_DECISION_PROVIDER`, then an
explicit `provider: none` in the project `.yakos.yml` (a veto), then `provider:`
in `~/.yakos-state/decision-policy.yml`, then `none`. A project file never
turns a provider on.

### Output

```json
{"answer":{"risk_class":{"type":"choice","choice":"dangerous","probabilities":{...},"confidence":0.85}, ...},
 "surface":"supervisor-prefilter","schema_id":"supervisor-prefilter@1","schema_hash":"<sha256>",
 "provider":"jev","model":"jev-1.13.0","mode":"prefilter","usage":{...},"cost_usd":0.00003,"latency_ms":212}
```

When no answer is available:

```json
{"answer":null,"reason":"timeout"}
```

`reason` is one of `disabled`, `no_key`, `breaker_open`, `budget`, `timeout`,
`network`, `http_401`, `http_422`, `http_429`, `http_529`, `http_5xx`,
`http_other`, `malformed`, `oversize`, `bad_request`, `internal`.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | A typed answer was printed, or the provider was unavailable and `--shadow` was given. |
| 1 | Usage error: bad flag, missing or extra surface, invalid surface name. No decision was attempted. |
| 3 | The provider was unavailable, timed out, refused, or failed, and `--shadow` was not given. |

`yakos decide` **never exits 2**. Exit 2 is the Claude Code hook block code, and
no hook may block because a third-party API was slow or down. A failure means
"take the existing deterministic path". A panic is converted to the same
failure path, because Go's own panic status is 2.

### Modes

- **shadow** (`--shadow`): advisory. Meant to be started in the background by a
  hook, but the bound does not depend on that: the default deadline is 1.5 s.
- **prefilter** (default): synchronous. Default deadline 1.5 s with at most one
  retry on 408/429/529/5xx, 300 ms apart.

`--timeout` can raise either deadline, never above 10 s.

A provider may add to a deterministic check, never subtract from it, and no
answer may block a tool call. Question sets carry `may_block: false`, and
`yakos validate` refuses `may_block: true` unless a verified promotion exists
for that exact set hash. An operator records one with
`yakos decide promote <surface> --report <eval report>`. The record binds the
surface, the set hash and the sha256 of the eval report, and carries a digest.
That stops typos and casual hand edits. It is not proof against a process
running as you that can call the CLI, which is why agents that can run commands
may never reference a provider (see below).

### Agents

A decision provider is never attached to an agent that could misuse it. An
agent may reference one only if its `tools:` line is present and lists nothing
beyond `Read`, `Grep`, `Glob`, `LS`, `SendMessage` and `TaskList`. A missing
`tools:` line inherits every tool including Bash, so it is refused, as are
`Bash(...)` scopes, `*`, `Agent`, `Task`, `WebFetch`, `NotebookEdit` and any
`mcp__*` tool. `runtime: jev` is always an error.

## Egress: what leaves the machine

Only the fields listed in the question set's `state_fields` leave. Every string
is redacted in full first, then truncated to a preview, then redacted again, so
a token that straddles the cut cannot leave as a fragment. Default level
`strict`: 2 KiB per string. `previews`: 8 KiB. `full`: no per-string cap.
Redaction and the size cap apply at every level.

**Whitespace is normalised before redaction, and the normalised text is what is
sent.** Shell line continuations (backslash, optional CR, LF) are joined, and
every run of whitespace, tabs, CR, LF and Unicode spaces such as U+00A0
included, becomes one space. So a flag cannot be separated from its value by
anything the rules do not expect. The provider only needs the gist of a command
or diff, so the original layout is not preserved.

**Redaction is best effort.** It is pattern-based, so a secret that matches no
pattern and sits under no `never_paths` entry leaves verbatim. The allowlist,
the previews and the size cap are the primary controls; keep `state_fields`
small and leave `provider: none` on repositories you would not send to a third
party. Command lines are the weakest input, so the shapes below are matched
explicitly; anything else on a command line (a password as a bare positional
argument, a custom flag) is not. What is matched:

- credentials on a command line: `curl -u user:pass`, `--user user:pass`,
  `-uuser:pass` (quoted values with spaces included, and tab separators); `--password X` and `--password=X` (and other `--*-secret`,
  `--*-token`, `--api-key`, `--access-key`, `--auth` flags); `mysql`,
  `mysqldump`, `mariadb -pPASS`; `docker`, `podman`, `helm`, `oras login -p X`;
  `sshpass -p X`; `redis-cli -a X`; `htpasswd -b file user pass`;
  `X-Api-Key:`, `X-Auth-Token:` style headers; and TypeSafe's own
  `apikey_...` tokens of any length

- the `secret-scan` table (AWS, GitHub, Slack, Stripe, Anthropic and Google
  keys, PEM headers), plus whole PEM private-key blocks
- egress-only additions: short Stripe and webhook keys, Slack webhook URLs,
  `Authorization` headers (any scheme), `Bearer`/`Basic`/`Token`/`Digest`
  values, JWTs, `scheme://user:pass@host` credentials, and 40-character
  mixed-case base64 tokens (the shape of a bare AWS secret key)
- `name=value` and `name: value` assignments whose name contains a credential
  word (`password`, `pwd`, `secret`, `token`, `api_key`, `auth`, `webhook`,
  `dsn`, ...), and any upper-case env-style line whose name contains `PASS`,
  `PWD`, `SECRET`, `TOKEN`, `KEY`, `AUTH`, `CRED`, `DSN` or `WEBHOOK`
- base64 runs of 400 characters or more
- JSON keys, as well as values. A field whose key looks like a credential is
  replaced wholesale.

`never_paths` is checked on every path-named key (`file_path`, `notebook_path`,
`path`, ...) and command target at any depth. Defaults include `**/.env*`,
`**/*.pem`, `**/*.key`, `**/credentials/**` and `**/secrets/**`. When one
matches, the path still leaves, and every other string in the state is replaced
with `[content withheld: secret path]`. Extend the list under
`decisions.egress.never_paths`.

### Who sets the limits

A project's `.yakos.yml` can only tighten. Budget caps are the minimum of the
project value and the user-level ceiling, the egress level is the more
restrictive of the two, and `never_paths` are the union. The ceiling defaults to
2000 calls per session, $1.00 per day and `strict`. To raise it, write
`~/.yakos-state/decision-policy.yml` (outside any repository):

```yaml
budget: { max_calls_per_session: 5000, max_usd_per_day: 3.00 }
egress: { level: previews }
```

The redacted state is capped at 64 KiB. Over the cap the call is refused as
`oversize` and never sent.

**Data handling, plainly.** TypeSafe is a US-hosted vendor. Its privacy policy
says inputs are retained "as long as reasonably necessary" and processed for
telemetry and abuse monitoring. It says inputs are not used for training. Zero
data retention is available only under an enterprise agreement, not during
early access. Check the current terms before enabling it on a client project.
`provider: none` per project turns it off.

## P2b shadow supervisor

The `supervisor-stream` hook (PostToolUse on `Edit|Write|MultiEdit|Bash`, bash
and Go twin) can ask the `supervisor-prefilter` question set about every
mutation, in **shadow mode**. The local pre-filter still decides everything.
The provider's verdict is only logged, next to the local verdict, so the two
can be compared before anyone lets a provider influence escalation.

Shadow means the call can never block, change or delay a tool call:

- The hook starts `yakos decide supervisor-prefilter --shadow` detached, feeds
  it the event on stdin, and returns. It never waits for or reads the result.
  Exit code, buffer, escalation counter and supervisor dispatch are unchanged.
- Any failure is a silent fail-open: no key, breaker open, budget exhausted,
  timeout (1.5 s default, hard-enforced in the engine), HTTP error, missing
  `yakos` binary. The reason is written to the decision log (`status`), and
  the tool call proceeds.
- With provider `none` (default) the hook does no extra work and is
  byte-for-byte what it was before. Provider `jev` without `TYPESAFE_API_KEY`
  also starts nothing, so a missing key does not spawn a process per tool call.
- A pre-filter that is switched off (`supervisor.pre_filter.enabled: false`)
  has no local verdict to compare, so it makes no shadow call.

### Enable it

The provider is enabled by you, not by a repository. Either export it in your
shell, or put it in the user-level policy file (both hooks read it in block
style, as a top-level `provider:` key):

```sh
export YAKOS_DECISION_PROVIDER=jev     # or mock, for a dry run with no key
```

```yaml
# ~/.yakos-state/decision-policy.yml
provider: jev
```

The policy file is trusted only when it is a regular file (not a symlink), owned
by you, and not group or world writable (`chmod 600`). Otherwise it is ignored
whole, and `yakos decide` and `yakos doctor` say so.

A project can opt out with `decisions: { provider: none }` in its
`.yakos.yml` (block style for the hooks). A project value of `jev` or `mock`
is ignored.

```sh
export TYPESAFE_API_KEY=...            # shell profile only, never in a file
yakos doctor --probe-decision          # key, config, set hash, breaker, budget
```

`YAKOS_DECISION_PROVIDER` overrides the file and `YAKOS_DECISION_DISABLE=1`
turns it off. Budget, breaker and egress rules above apply unchanged.

What leaves the machine per call: `tool`, `file_path`, a preview of the
command or edit text, the head of `decisions.md` (1500 bytes, already sent to
the LLM supervisor today) and `plan_mentions_path`, all redacted and cut to
2 KiB previews under `strict` egress. Files under `never_paths` are sent by
name only.

### Read the results

The state reaches the child in a private 0600 file in the state directory
(`shadow-state-*.json`), which `yakos decide --consume-state-file` deletes on
every exit (read, surface off, config error, disabled, provider failure), since
it holds raw text. It deletes nothing else: only a regular `shadow-state-*.json`
file of yours directly in the state directory. The hook also skips the call, and
the file, when the project config turns the surface off. A pipe would block the hook when the state outgrows the
pipe buffer (about 4 KiB on Windows). Stale files are swept after ten minutes.
The bash hook passes the state on the child's stdin and keeps the raw preview
off every argv.


Each shadow call appends one decision-log record carrying the provider's
answers, latency, cost, and the local verdict (`local_verdict`: `pass` or
`escalate`, plus `local_trigger`: `sensitive-path`, `large-diff`,
`out-of-scope` or `risk-regex`). Then:

```sh
yakos decide compare supervisor-prefilter          # add --json for scripts
```

Mock-provider records and records labelled with `--tag` (use `--tag smoke` for
hand-run smoke tests) are left out, so agreement and the sample gate reflect
real traffic. Narrow further with `--session`, `--exclude-session a,b` and
`--since 72h` (or an RFC 3339 time); `--include-mock` and `--include-tagged`
bring the skipped records back.

```
surface        supervisor-prefilter  (model jev-1.13.0)
question set   <sha256>
shadow calls   412 with a local verdict (0 from other question-set hashes, 0 without a local verdict; not counted)
answered       398   fail-open 14  [timeout=9 http_529=5]
agreement      93.2%  (371 of 398)
  both escalate        41
  both pass            330
  shadow-only escalate 19   (candidates for review: the provider flagged what the local heuristic passed)
  local-only escalate  8    (the provider would have passed what the local heuristic escalated)
local escalations the provider also flags: 83.7%
latency        mean 240 ms, p95 610 ms
cost           $0.017000
sample gate    398 of 200 answered decisions (reached)
```

(Illustrative numbers.) The provider "escalates" by the rule the prefilter
mode would use: `risk_class` is `dangerous` at or above the set's
`min_confidence`, or `bypasses_hard_control` is at or above its
`min_probability`. Only records for the question set's current hash and
`shadow` mode are counted, so re-wording a question or bumping the model
starts a fresh comparison instead of mixing evidence.

### Promotion criteria

Shadow mode is the only mode the hook supports. Promoting the surface to
`prefilter` (a provider hit adds an escalation, never removes one) is a later,
separate change, and it needs, from the K-111 design (section 5):

- at least 200 answered shadow decisions on the current set hash (the
  `sample gate` line), and at least 30 labelled disagreements
- Jev-only precision of at least 0.70 on those labelled disagreements
- no labelled false "benign" on a locally escalated case at 0.9 confidence or
  more
- an eval report recorded with `yakos decide promote supervisor-prefilter`

Labelling disagreements (`yakos decide label`) is not built yet, so the last
three criteria are checked by hand from the `shadow-only escalate` and
`local-only escalate` cases until it is. `compare` supplies the sample count,
agreement, fail-open rate, latency and cost.

## Logs, breaker, budget

All state lives in `~/.yakos-state/` (or `$YAKOS_DISPATCH_LOG`), mode 0600, and
never in the dispatch-log.

| File | Contents |
|---|---|
| `decision-log.ndjson` | One record per call: surface, question-set hash, model, mode, latency, tokens, cost estimate, answers with confidence, error class, and (from a hook) the local heuristic's verdict. The raw state, the question text, and the key are never logged. |
| `decision-breaker.json` | 5 consecutive failures open the circuit for 10 minutes. |
| `decision-budget-<day>.ndjson` | Append-only ledger, one line per reservation or spend, summed on read. Per-session call cap (default 2000) and per-UTC-day spend cap (default $1.00). Cost is estimated from reported input tokens at the documented $0.042 per million. Appends are atomic across hook processes, so concurrent hooks cannot lose counts. The call cap is never exceeded; the dollar cap can be overshot only by calls already in flight, well under $0.002. Ledgers older than two days are deleted. If the ledger cannot be written or read, the call is refused. |

`YAKOS_DECISION_DISABLE=1` turns every provider off.

`yakos doctor --probe-decision --live` makes one call that is not counted
against the budget and does not touch the breaker.

## Cache stability

Decision results never enter a prompt, a system-prompt prefix, an agent body,
the roster, or an always-loaded rule. The call is out of band, so it cannot
disturb the cached prefix (`rule:cache-stability`).

## Pinned models

Question sets pin an exact version such as `jev-1.13.0`. Aliases move on
release and can change answers without a code change, so `yakos validate`
rejects them. `yakos doctor --probe-decision --live` reports drift between the
served and pinned version. A threshold tuned on one model version must be
re-evaluated before it is applied to another.

## Status

The client is written against the published API at
https://docs.typesafe.ai/api and tested against local servers and the mock
provider. CI has no key; live calls happen only on an operator machine.

### Routing surface: `routing-tier`, shadow-only, opt-in

K-177 built the tier-selection surface (k111 design section 2.5) as shadow-only.
It is the one automatic caller outside the supervisor hook, and the only one that
sends task text. It is off by default and enabled only by `routing_shadow: true`
in `~/.yakos-state/decision-policy.yml`; a project file cannot enable it. The
question set is `lib/decisions/routing-tier.yaml` (one `choice` question,
`haiku`, `sonnet` or `opus`). The suggestion is recorded as `tier_suggested_by_jev`
on the ledger row and never changes a route. The payload (agent name, route class,
first 2 KiB of the task), the sensitive-task skip, the 3 second no-retry bound and
the off-critical-path design are in
[routing.md](routing.md#jev-and-routing-shadow-only-opt-in).
