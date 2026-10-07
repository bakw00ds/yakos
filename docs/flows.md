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
    secret_env: MY_HOOK_SECRET  # name of an environment variable
nodes: [...]
```

`secret_env` must match `^[A-Z][A-Z0-9_]{0,63}$`. Validation rejects a bad cron
expression or secret name when the workflow is saved or run.

## Enabling them: the schedules file

`~/.yakos-state/schedules/<project-slug>.yaml`

```yaml
version: 1
timezone: America/New_York      # optional; default is the machine's local zone
workflows:
  nightly-review:
    cron: true
  webhook-triage:
    webhook: true
    secret_env: MY_HOOK_SECRET   # must repeat the name the workflow declares
```

- `<project-slug>` is the workspace directory's base name, lower-cased, with
  every run of characters outside `a-z0-9` replaced by `-` (`My Project` becomes
  `my-project`).
- The file is read only when it is a regular file (not a symlink), owned by you,
  mode `0600` (`chmod 600`), in a directory you own that others cannot write.
  Anything else is ignored: no trigger fires, and the daemon logs the reason
  (never the path).
- The state directory comes from your home directory only; `YAKOS_DISPATCH_LOG`
  does not move it.
- The scheduler re-reads the file every minute, so removing an entry or
  flipping it to `false` takes effect within a minute. Unknown keys make the
  file invalid.
- A missing file, or a workflow not listed, means nothing is enabled.

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
the same workflow is in flight (started by any path) is skipped, not queued: a
line goes to `<work>/current/workflows/triggers.ndjson`
(`{"outcome":"skipped","reason":"run already active"}`) and a webhook caller
gets `409`. Started triggers are logged there too.

## Webhook

`POST /flows/api/trigger/<name>` (role `dispatch`)

- Header `X-Yakos-Webhook-Secret: <the value of the secret_env variable>`.
  The secret is read from the daemon's environment at request time, must be at
  least 16 characters, and is compared in constant time.
- `Content-Type: application/json` (the console's CSRF guard requires it for
  every mutation); the body is the payload, at most 64 KiB, valid UTF-8, and
  optional.
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
| 401 | Missing or wrong secret |
| 404 | Not available: workflow missing, no webhook declared, not enabled, `secret_env` mismatch, schedules file untrusted, or secret unset or too short. One answer for all causes. |
| 409 | A run of this workflow is already active |
| 413 | Body over 64 KiB |
| 422 | Payload refused by the injection scan |

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
