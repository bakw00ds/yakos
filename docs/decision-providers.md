# Decision providers

A **decision provider** answers a fixed, reviewed set of typed questions about
a redacted state and returns probabilities, never text. yakOS ships one real
provider, TypeSafe's Jev, plus a deterministic `mock` for CI and a `none`
provider that turns everything off. The design and its alternatives are in
[ADR-0009](adr/ADR-0009.md).

A decision provider is **not an agent runtime**. It cannot run an agent, it is
not in `runtime.Known`, and no agent can be routed to it. See
[runtime-matrix.md](runtime-matrix.md#jev-is-not-a-runtime).

Nothing in this release calls a provider automatically. `yakos decide` is the
only entry point, and no hook uses it yet. Hook wiring lands in later PRs, in
shadow mode first.

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
2. Enable it per project in `.yakos.yml`. The default is `provider: none`.
   See the commented `decisions:` block in `lib/settings/yakos.yml.template`.
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
```

It reads a JSON **object** of named fields on stdin, redacts it, asks the
question set `lib/decisions/<surface>.yaml`, and prints one line of JSON.

Provider selection: `--provider`, then `$YAKOS_DECISION_PROVIDER`, then
`decisions.provider` in `.yakos.yml`, then `none`.

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

**Redaction is best effort.** It is pattern-based, so a secret that matches no
pattern and sits under no `never_paths` entry leaves verbatim. The allowlist,
the previews and the size cap are the primary controls; keep `state_fields`
small and leave `provider: none` on repositories you would not send to a third
party. What is matched:

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

## Logs, breaker, budget

All state lives in `~/.yakos-state/` (or `$YAKOS_DISPATCH_LOG`), mode 0600, and
never in the dispatch-log.

| File | Contents |
|---|---|
| `decision-log.ndjson` | One record per call: surface, question-set hash, model, mode, latency, tokens, cost estimate, answers with confidence, error class. The raw state, the question text, and the key are never logged. |
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

No live call to TypeSafe has been made from this codebase. The client is
written against the published API at https://docs.typesafe.ai/api and tested
against local servers only.
