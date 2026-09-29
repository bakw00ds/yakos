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

`TYPESAFE_BASE_URL` optionally overrides the endpoint. It must be `https`
(plain `http` is accepted only for loopback), and redirects are never followed,
so the bearer key cannot be sent in the clear or to another host.

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

- **shadow** (`--shadow`): advisory. Default deadline 10 s. Intended to be
  started in the background by a hook so it adds no latency.
- **prefilter** (default): synchronous. Default deadline 1.5 s with at most one
  retry on 408/429/529/5xx, 300 ms apart.

A provider may add to a deterministic check, never subtract from it, and no
answer may block a tool call. Question sets carry `may_block: false`, and
`yakos validate` refuses `may_block: true` without a recorded promotion for
that exact set hash.

## Egress: what leaves the machine

Only the fields listed in the question set's `state_fields` leave. Before the
request is built, each string is truncated to a preview and redacted. Default
level `strict`: 2 KiB per string. `previews`: 8 KiB. `full`: no per-string cap.
Redaction and the size cap apply at every level.

Redacted shapes: the `secret-scan` table (AWS, GitHub, Slack, Stripe, Anthropic
and Google keys), whole PEM private-key blocks, `name=value` credential
assignments, `scheme://user:pass@host` credentials, JWTs, `Bearer` tokens, and
base64 runs of 400 characters or more. A field whose name looks like a
credential (`password`, `token`, `api_key`, ...) is replaced wholesale.

If `file_path` matches `never_paths` (defaults include `**/.env*`, `**/*.pem`,
`**/*.key`, `**/credentials/**`, `**/secrets/**`; extend under
`decisions.egress.never_paths`), the path is sent and every other string is
replaced with `[content withheld: secret path]`.

The redacted state is capped at 64 KiB. Over the cap the call is refused as
`oversize` and never sent. The vendor retains inputs for an unspecified period
and does not train on them; check the current terms before enabling it on a
client project. `provider: none` per project turns it off.

## Logs, breaker, budget

All state lives in `~/.yakos-state/` (or `$YAKOS_DISPATCH_LOG`), mode 0600, and
never in the dispatch-log.

| File | Contents |
|---|---|
| `decision-log.ndjson` | One record per call: surface, question-set hash, model, mode, latency, tokens, cost estimate, answers with confidence, error class. The raw state, the question text, and the key are never logged. |
| `decision-breaker.json` | 5 consecutive failures open the circuit for 10 minutes. |
| `decision-budget.json` | Per-session call cap (default 2000) and per-UTC-day spend cap (default $1.00). Cost is estimated from reported input tokens at the documented $0.042 per million. |

`YAKOS_DECISION_DISABLE=1` turns every provider off.

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
