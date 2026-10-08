# Flows: triggers, enablement and templates

A workflow can start itself on a schedule (`cron`) or from an HTTP call
(`webhook`). A workflow file only DECLARES a trigger. A trigger fires only when
you enable it in a file under your own home directory, so a cloned repository
cannot make your machine run agents.

See also `flows-auto-routing.md` (`runtime: auto`) and `flows-output-scan.md`.

## Declaring triggers

```yaml
version: 1
name: nightly-review
triggers:
  cron: "0 2 * * *"            # 5 fields, see below
  webhook:
    secret_env: YAKOS_HOOK_SECRET  # name of an environment variable
nodes: [...]
```

`secret_env` must match `^[A-Z][A-Z0-9_]{0,63}$` and start with `YAKOS_`. Validation rejects a bad cron
expression or secret name when the workflow is saved or run.

## Enabling them

From the project directory (or with `--project DIR`):

```
yakos flows schedule enable <workflow> [--cron] [--webhook]
yakos flows schedule disable <workflow>
```

`enable` turns on the triggers the workflow declares (or only the ones you name),
pins the workflow file's SHA-256 and writes the schedules file below: mode
`0600`, replaced atomically, trust-checked before it is read (a symlink, another
user's file or one others can write is refused, never overwritten), and recorded
as a `config_changed` line in the dispatch log. Run `enable` again after you
reviewed a changed workflow to re-pin it. `disable` removes the entry. Neither
prints a path. Running `enable` is your consent, so read the workflow first.

### The schedules file

The file is keyed by the project's canonical path, not its folder name, so there
is nothing to migrate: `<hash>` below is derived from the symlink-resolved path
and the file repeats that path in `workspace:`. A hand-edited file still works.

`~/.yakos-state/schedules/<slug>-<hash>.yaml`

```yaml
version: 1
workspace: /Users/me/projects/acme   # required: this workspace's canonical path
timezone: America/New_York      # optional; default is the machine's local zone
workflows:
  nightly-review:
    cron: true
    workflow_sha: 9f2c...        # required: sha256 of the workflow file
  webhook-triage:
    webhook: true
    secret_env: YAKOS_HOOK_SECRET   # must repeat the name the workflow declares
    workflow_sha: 41ab...
```

- The file belongs to one workspace. `<slug>` is the workspace folder's base
  name (lower-cased, runs outside `a-z0-9` replaced by `-`), and `<hash>` is
  the first 12 hex digits of the SHA-256 of the workspace's canonical path
  (symlinks resolved; lower-cased on macOS and Windows). Two workspaces with the
  same folder name therefore never share a file. Compute the name with:
  `p=$(cd /path/to/workspace && pwd -P | tr 'A-Z' 'a-z'); printf %s "$p" | shasum -a 256 | cut -c1-12`
  (on Linux drop the `tr`).
- `workspace:` must be present and name the same directory the daemon serves
  (`os.SameFile`, so a symlink or case alias of the same directory matches). A
  file that names another workspace (for example a copy of another project's
  file) is ignored and the daemon logs "belongs to a different workspace".
- `workflow_sha` pins the workflow: it is the SHA-256 of the exact bytes of
  `<work>/current/workflows/<name>.yaml` (`shasum -a 256 <that file>`). A trigger
  fires only while the file still hashes to that value. After any change (a
  `git pull`, an edit in the console, an agent) the trigger stops, the daemon
  logs `workflow file changed since it was enabled ... set workflow_sha to
  <current hash>` (no path), and `triggers.ndjson` records `"outcome":"refused"`
  with the same text. Review the file, then paste the new hash to re-enable.
  A webhook for a changed workflow answers 404.
- `secret_env` must start with `YAKOS_`, so the daemon never reads an arbitrary
  variable. It also may not name a well-known credential variable (`GITHUB_TOKEN`,
  `AWS_SECRET_ACCESS_KEY`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, ...): the
  secret is shared with every sender. Use a dedicated variable such as
  `YAKOS_WEBHOOK_SECRET`.
- The file is read only when it is a regular file (not a symlink), owned by you,
  mode `0600` (`chmod 600`), in a directory you own that others cannot write,
  and at most 64 KiB (a larger file is refused, not truncated).
  Anything else is ignored: no trigger fires, and the daemon logs the reason
  (never the path). On Windows there are no mode bits, so only the regular-file
  and symlink checks apply.
- The state directory comes from your home directory only; `YAKOS_DISPATCH_LOG`
  does not move it.
- The scheduler re-reads the file every minute, so removing an entry or
  flipping it to `false` takes effect within a minute. Unknown keys make the
  file invalid.
- A missing file, or a workflow not listed, means nothing is enabled.
- A workflow file that is not a regular file (a FIFO or device) is refused at
  once, so it cannot stall the scheduler.
- Node dispatches of triggered runs are logged with `surface=trigger`.

## Cron

Five fields: `minute hour day-of-month month day-of-week`. Each field takes a
comma list of `*`, `n`, `a-b`, with an optional `/step` (`*/15`, `10-50/10`).
Month and weekday accept three-letter names (`jan`, `mon-fri`); weekday `7` is
Sunday. When both day-of-month and day-of-week are restricted, a day matches if
either does (Vixie cron). Times are wall-clock times in the `timezone` above.

Daylight-saving rules:

| Transition | Rule |
|---|---|
| Spring forward: the wall time does not exist (`30 2 * * *` on the gap day) | Fires once, at the first instant after the gap (03:00). Several skipped times (`*/15` over 02:00-02:59) collapse into that one fire. |
| Fall back, fixed hour: the wall time happens twice (`30 1 * * *`) | Fires once, at the first occurrence. |
| Fall back, every-hour schedules (`0 * * * *`, `*/30 * * * *`) | Run on elapsed time, so both passes through the repeated hour fire and the cadence holds. |

If the daemon was asleep through one or more fire times it runs the workflow
once when it wakes, not once per miss.

## One run at a time

A workflow has at most one active run. A trigger that arrives while a run of
the same workflow is in flight is skipped, not queued: a line goes to
`<work>/current/workflows/triggers.ndjson`
(`{"outcome":"skipped","reason":"run already active"}`) and a webhook caller
gets `409`. Started and refused triggers are logged there too.

The guard covers the console and the scheduler only (they share one engine). The JSON-RPC
`workflow.run` and the CLI build their own engines and are not counted, so a
run started that way does not block a trigger (or the reverse).

## Webhook

`POST /flows/api/trigger/<name>` (role `dispatch`)

Why `dispatch` and not the `flows-run` role that `POST /flows/api/run` needs: a
webhook runs only a workflow you enabled, with its content pinned, in the
schedules file. That entry is your explicit consent to run that one workflow
on an external event, so the route does not ask the console user for the
stricter flow-start role. `/flows/api/run` (start any workflow) keeps its
stricter role.

An external sender needs both the console token (or session) that every
console route requires and the signature below.

- Sign the request. Set `X-Yakos-Timestamp` to the current Unix time in
  seconds and `X-Yakos-Signature: sha256=<hex>` where the value is
  `HMAC-SHA256(secret, timestamp + "." + body)` in lowercase hex. The secret is
  read at request time, at least 16 characters, from a file or the daemon's
  environment (see "Where the secret comes from"). There is no bare-secret header.

  ```
  ts=$(date +%s); body='{"issue":"login broken"}'
  sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$SECRET" -hex | sed 's/^.* //')
  curl -X POST -H "Authorization: Bearer $CONSOLE_TOKEN" -H 'Content-Type: application/json' \
    -H "X-Yakos-Timestamp: $ts" -H "X-Yakos-Signature: sha256=$sig" -d "$body" \
    http://127.0.0.1:PORT/flows/api/trigger/webhook-triage
  ```
- The timestamp must be within 5 minutes of the daemon's clock, and each
  signature is accepted once (the last 1000 are remembered): a captured request
  cannot be replayed. A sender's retry must be re-signed with a fresh timestamp.
- At most 6 requests per minute per workflow name (`429`, `Retry-After: 60`),
  counted only for a request whose signature verified and was not a replay, so a
  caller without the secret cannot lock out the real sender and a replayed
  request cannot spend the budget. A request answered `429` has used its
  signature up, so the retry must be re-signed.
- `Content-Type: application/json` (the console's CSRF guard requires it for
  every mutation); the body is the payload, at most 64 KiB, valid UTF-8, and
  optional. An oversized body answers 404; non-UTF-8 answers 400 only once the
  signature verified.
- The payload goes through the same blocking injection scan as node output
  before any node sees it. The scan being unavailable refuses the call.
- An accepted payload becomes the workflow input `payload` (declare
  `inputs: {payload: ""}` to receive it), wrapped as inert delimited data.
  Because the input differs from the saved file, a run started with a payload
  cannot be resumed against the unchanged workflow.
- The run belongs to the calling operator's resolved identity.

| Status | Meaning |
|---|---|
| 202 `{"run_id": ...}` | Started |
| 404 | Not available: workflow missing, no webhook declared, not enabled, `secret_env` mismatch, workflow changed since it was enabled, schedules file untrusted or for another workspace, secret unset or too short, a body over 64 KiB, or a missing, wrong, stale or replayed signature. One answer for all causes, so the endpoint reveals neither whether a webhook is enabled nor whether a secret was right. |
| 409 | A run of this workflow is already active |
| 400 | Body is not valid UTF-8 (only for a correctly signed request) |
| 422 | Payload refused by the injection scan |
| 429 | Rate limit |

### Where the secret comes from

For each `secret_env` name the daemon looks for the file
`~/.yakos-state/webhook-secrets/<secret_env>` first. If it exists it is the
secret and the environment variable is ignored, even when both are set. The file
must be a regular file you own with mode `0600`, in a directory you own that
others cannot write (`mkdir -m 700`, then `umask 077; printf %s "$SECRET" > file`),
at most 4 KiB; one trailing newline is trimmed. A file that exists but fails
those checks keeps the webhook off: the daemon does not fall back to the
environment. With no file, the daemon's environment variable is used as before.

The request body is read (at most 64 KiB; a larger body is a 404 and closes the connection)
before the schedules file is looked at, so the connection behaves the same for
an enabled and a disabled hook.

The call is not idempotent and takes no `Idempotency-Key`; the one-active-run
rule is what keeps a sender's retries from piling up runs.

## Templates

`lib/workflows/templates/*.yaml` ships with the framework:

| Template | What it does |
|---|---|
| `pr-review-multi-model` | Three independent reviewers (`runtime: auto`) and a merge step |
| `nightly-review` | Daily review plus security scan and a digest; declares `cron: "0 2 * * *"` |
| `webhook-triage` | Triage an event posted to the webhook trigger |

In the Flows tab, `Templates` opens a gallery; `Use` saves a copy as a new
workflow (it fails if the name exists). A copy that declares a trigger still
does nothing until you enable it in the schedules file.

## Not yet

`on_files` and `on_kanban_moved` triggers are a follow-up.
