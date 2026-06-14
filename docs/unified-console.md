# Unified console — operator guide

**Introduced in:** v0.40.0.0
**Networked multi-operator mode:** v0.44.0.0+
**Audience:** operators who want to use the browser-based console for
interactive Chat, workflow orchestration, or unified dashboard access.

Companion docs: [overview.md](overview.md) for architecture context,
[docs/adr/ADR-0003.md](adr/ADR-0003.md) for the Flows engine design,
[docs/adr/ADR-0004.md](adr/ADR-0004.md) for the networked-identity design,
[runtime-matrix.md](runtime-matrix.md) for per-runtime streaming behavior.

---

## Starting the console

### See the URL before starting

`yakos start` prints the web console URL in its preflight banner on every
launch, regardless of whether the daemon is running:

```
yakos start — preflight
  ...
  web console:    http://127.0.0.1:7890/#token=<token>  (run 'yakos serve' or 'yakos start --no-repl' to start)
```

If the daemon is already listening, the banner shows `(running)` next to
the URL instead of the hint.

### Web-only mode (`--no-repl`)

To skip the REPL entirely and bring up the console in the foreground:

```sh
yakos start myapp --no-repl    # preflight + daemon + console, no REPL
yakos start myapp --web        # same flag, shorter alias
```

`--no-repl` runs the full preflight (project resolution, banner, agents),
then hands off to `yakos serve` internally. It blocks in the foreground
like `yakos serve`. Use this when you want the browser-based console
without an interactive terminal session.

`--no-repl --dry-run` prints what would happen (preflight + serve intent)
without binding any port.

### Starting with the full daemon

```sh
yakos serve
# Prints: yakos serve: console: http://127.0.0.1:7890/#token=<token>
# Open the printed URL in a browser.
```

The token in the URL fragment is loaded into browser memory and stripped
from the address bar. It is never sent as a query parameter. The console
binds `127.0.0.1` only and cannot be reached from another machine.

The token is stored at `~/.yakos-state/console-token` (mode 0600).
Rotate it with:

```sh
yakos serve --rotate-console-token
```

To disable the console and run the daemon without it:

```sh
yakos serve --no-console
```

---

## Networked multi-operator mode

By default the console is loopback-only. If your team needs multiple
operators on different machines to share one daemon, bind to a routable
address. The daemon enforces mutual TLS and role-based access
automatically — there is no plain-HTTP-over-network path.

### When to use this vs the multi-instance model

Use **networked console** when you want a single shared daemon: one
Kanban board, one Activity feed, one cost ledger, operators joining from
different machines in real time.

Use the **multi-instance model** (independent daemons, git + `work/`
coordination) when operators need independent project checkouts, air-gap
constraints, or latency-tolerant async collaboration. Both remain
supported; this section covers the networked-console path only.

### Binding to a specific IP

```sh
yakos serve --console-bind 192.168.1.50:7890
```

The daemon binds on that address and port. The auto-generated CA and
server cert's SAN list is set to `192.168.1.50`. Connecting clients
must present a valid client certificate signed by the daemon's CA.

### Binding to a wildcard address (requires `--console-external-host`)

```sh
yakos serve --console-bind 0.0.0.0:7890 \
            --console-external-host console.example.internal:7890
```

Wildcard binds (`0.0.0.0:7890` or `[::]:7890`) require
`--console-external-host` — one or more routable hostnames or
IP:port pairs, comma-separated or via repeated flags. This value
drives two things:

1. The TLS server certificate's Subject Alternative Names.
2. The WebSocket `Origin` allow-list (requests from unlisted origins are
   rejected).

Without `--console-external-host` on a wildcard bind, the daemon **refuses
to start** (fail-closed). This is intentional: a wildcard bind with no
declared external host cannot issue a useful TLS cert or validate origins.

Multiple external hosts (e.g., both a hostname and a bare IP fallback):

```sh
yakos serve --console-bind 0.0.0.0:7890 \
            --console-external-host console.example.internal:7890 \
            --console-external-host 10.0.1.20:7890
```

### Auto-generated CA and server certificate

On the first networked start, the daemon generates and persists:

```
~/.yakos-state/mtls/
  ca.crt          # self-signed CA certificate (PEM)
  ca.key          # CA private key (mode 0600)
  server.crt      # server certificate signed by the CA
  server.key      # server private key (mode 0600)
  roles.json      # CN → role mapping
```

The startup banner prints the CA's SHA-256 fingerprint:

```
yakos serve: console (mTLS): https://192.168.1.50:7890
  CA fingerprint: SHA256:AB:12:...
  bootstrap cert: ~/.yakos-state/mtls/client-admin.p12
```

**Verify this fingerprint** out of band before installing client certs on
any operator machine. The fingerprint is also available any time via:

```sh
yakos mtls show-ca
```

Or in PEM form:

```sh
yakos mtls show-ca --pem
```

### Bootstrap admin client certificate

By default, the daemon auto-issues one **admin** client cert on the first
networked start. The Common Name defaults to the OS username, or `admin`
if that is unavailable. The cert and key are written to:

```
~/.yakos-state/mtls/client-<name>.crt   (PEM, mode 0600)
~/.yakos-state/mtls/client-<name>.key   (PEM, mode 0600)
~/.yakos-state/mtls/ca.crt              (PEM)
```

The startup banner prints the path to the bundle. To suppress bootstrap
cert issuance:

```sh
yakos serve --console-bind 192.168.1.50:7890 --no-bootstrap-cert
```

To override the CN used for the bootstrap cert:

```sh
yakos serve --console-bind 192.168.1.50:7890 --console-bootstrap-cert alice
```

### `yakos mtls` — managing operator certs

#### Issue a client certificate

```sh
yakos mtls issue-client alice --role dispatch --out ./certs
```

Writes `client-alice.crt`, `client-alice.key` (mode 0600), and `ca.crt`
to `./certs/`. Also prints an openssl one-liner to bundle them as a `.p12`
for browser import:

```
To create a .p12 for browser import:
  openssl pkcs12 -export -in ./certs/client-alice.crt \
    -infile ./certs/client-alice.key -certfile ./certs/ca.crt \
    -out client-alice.p12
```

Use `--force` to re-issue a cert for a CN that already exists.

**Transmit the key file securely.** The key is written to disk in the
`--out` directory; transfer it to the operator via an encrypted channel,
then delete the copy on the issuing machine.

#### List issued clients

```sh
yakos mtls list-clients
```

Prints each issued CN and its current role:

```
alice    dispatch
bob      read
carol    admin
```

#### Show CA fingerprint

```sh
yakos mtls show-ca
```

```
CA path:        ~/.yakos-state/mtls/ca.crt
SHA-256:        AB:12:CD:34:...
```

#### Set or change a role

```sh
yakos mtls set-role alice flows-run
```

Updates `roles.json`. The change takes effect on the operator's next
request (no daemon restart required).

### Roles and what they allow

| Role | Access |
|---|---|
| `read` | Overview, Cost, Perf, Kanban; own and shared transcripts (view only). Default for any CN not in `roles.json`. |
| `dispatch` | All `read` access, plus: open Chat panes, run dispatches. |
| `flows-run` | All `dispatch` access, plus: trigger and resume Flows workflows. |
| `admin` | All `flows-run` access, plus: cert and role management via `yakos mtls`. |

Roles are enforced at the console edge and at the dispatch facade. A
`dispatch`+ role is required to issue any agent command — the networked
surface is RCE-capable for those roles, which is why the whole path
requires mTLS.

### Installing a client cert on an operator machine

1. Receive `client-<name>.crt`, `client-<name>.key`, and `ca.crt` from
   the daemon operator via a secure channel.
2. Verify the CA fingerprint matches the one printed in the daemon's
   startup banner (or from `yakos mtls show-ca` on the server):

   ```sh
   openssl x509 -in ca.crt -noout -fingerprint -sha256
   ```

3. Bundle as `.p12` for browser import (Chrome, Firefox, Safari all
   support PKCS#12):

   ```sh
   openssl pkcs12 -export \
     -in client-<name>.crt \
     -infile client-<name>.key \
     -certfile ca.crt \
     -out client-<name>.p12
   ```

4. Import `client-<name>.p12` into your browser's certificate store
   (Keychain on macOS / Certificate Manager on Windows / `certutil` on
   Linux for Firefox).

5. Navigate to `https://<console-external-host>`. Your browser will
   prompt you to select the client certificate for this site. Select the
   one you just imported.

### Residual limits

- **Out-of-band cert distribution.** There is no enrollment endpoint.
  Operators receive certs from the daemon operator via secure file
  transfer.
- **No CRL / OCSP.** Revocation is accomplished by removing the CN from
  `roles.json` (which degrades access to `read`) or by re-generating
  the CA (`--no-bootstrap-cert` + manual reissue for all operators).
  A full CA rotation is the nuclear option for a compromised key.
- **Single CA per daemon.** The CA is scoped to the daemon instance at
  `~/.yakos-state/mtls/`. Multi-daemon federations each have their own
  CA.

See [docs/adr/ADR-0004.md](adr/ADR-0004.md) for the design decisions
behind this identity model.

---

## Trust model and honest limits

The console operates in one of two modes, with meaningfully different
identity and access guarantees. The mode is determined at startup by
whether `--console-bind` is set to a non-loopback address.

### Loopback mode (default)

Without `--console-bind`, the console binds `127.0.0.1` only and uses a
**single shared bearer token**.

- All operators on the same machine share the token and are uid-equivalent
  — they have equal access to all tabs, Flows runs, and Chat transcripts.
- `operator_id` is **self-asserted attribution**, not a cryptographic
  identity. It labels dispatch log entries and presence chips so
  uid-equivalent teammates can see who did what. It does not restrict
  access.
- The residual security assumption is same-machine trust. Do not expose
  this mode to a network via port forwarding or reverse proxy — doing so
  would expose RCE-capable dispatch with no real authentication boundary.

### Networked mode (mTLS, multi-operator)

With `--console-bind <addr>`, the console binds to a routable address and
upgrades to **mutual TLS with per-cert identity and RBAC**. See
[Networked multi-operator mode](#networked-multi-operator-mode) below for
full setup instructions.

In networked mode:

- Identity is **cryptographically bound** to the client certificate's
  Common Name. `operator_id` is non-forgeable off-loopback.
- Access is governed by four roles (`read`, `dispatch`, `flows-run`,
  `admin`). A CN with no role entry defaults to `read` (fail-closed).
- There is **no plain-HTTP-over-network option**. Non-loopback bind always
  requires mTLS (RequireAndVerifyClientCert, TLS 1.2+).

### Honest limits (both modes)

- Client certificates are distributed **out of band** — there is no
  enrollment endpoint. The auto-issued bootstrap admin cert is the
  recommended on-ramp for the first operator; subsequent certs are issued
  via `yakos mtls issue-client` and transmitted by the operator (e.g.,
  secure file transfer, encrypted email).
- The alternative to networked-console collaboration is the
  **multi-instance model** (independent daemons coordinating via git and
  `work/` artifacts). That model remains fully supported and is not
  replaced by this feature — choose based on your team's topology.

---

## Console tabs

### Overview

The Overview tab shows two panels:

- **Now** — in-flight dispatches (agent, model, elapsed), running workflow
  nodes, and online operators with their presence colors.
- **Activity feed** — a chronological, operator-color-coded stream of
  `dispatch.*`, `kanban.*`, and `workflow.*` events from the WebSocket
  bus.

The feed draws from the bus's 1000-event replay ring. Events older than
the ring are not available without restarting the daemon.

### Chat

The Chat tab provides per-model REPL panes. Each pane is independently
configured:

- **Runtime:** claude / codex / agy / gemini
- **Model tier:** haiku / sonnet / opus / fable (fable requires explicit
  opt-in; see [runtime-matrix.md](runtime-matrix.md))

**Streaming behavior:** claude panes stream tokens as they arrive
(`--include-partial-messages` unframed mode). codex, agy, and gemini
panes receive a single buffered response. The UI labels buffered panes
so you know to wait for the full response.

Each pane is **multi-turn** with a persisted transcript at
`<work>/current/chats/<conversationID>.ndjson`. Refreshing the browser
restores prior turns.

Panes are private by default. Click **Share pane** to make a pane
visible to other operators on the same console (the server enforces
ownership; other operators get a 403 on your unshared session stream).

### Flows

See the full [Flows orchestration](#flows-orchestration) section below.

### Kanban

The Kanban tab is the same drag-and-drop board as `yakos kanban serve`,
served under the console's single token. Mutations use the same CLI
internally (`yakos kanban add/move`) — no second source of truth.

This tab closed the prior no-token mutational gap: kanban mutations
previously required no authentication on the standalone `yakos kanban
serve` server. Under the unified console, the edge token gate covers all
mutations.

### Cost

Token spending over time, same data as `yakos metrics report`. Per-agent
and per-runtime breakdowns. Not a live stream — refreshes on tab open.

### Performance

Dispatch latency percentiles and per-agent time-series from the
performance dashboard. Not a live stream.

---

## Flows orchestration

### What Flows is

Flows is a headless DAG executor that runs multi-agent workflows defined
as YAML files. Each node dispatches one agent; `needs:` edges define
execution order. Independent nodes run in parallel; dependent nodes wait
for their declared `needs` to complete. Upstream node output can be
threaded verbatim into downstream node prompts.

Workflows are authored in the console Flows tab (YAML editor with live
SVG canvas) or by writing YAML files directly. They run headlessly via
the CLI or from the console.

### Workflow file location

```
<work>/current/workflows/<name>.yaml
```

`<work>` is `~/agent-control/<project-name>/work`.

### Schema reference

Top-level fields:

| Field | Required | Description |
|---|---|---|
| `version` | yes | Always `1`. |
| `name` | yes | Workflow identifier. Must match `^[a-z0-9][a-z0-9-]{0,63}$`. |
| `inputs` | no | Map of key → default value. Referenced as `${inputs.<key>}`. |
| `nodes` | yes | List of node objects (see below). |

Node fields:

| Field | Required | Description |
|---|---|---|
| `id` | yes | Node identifier. Must match `^[a-z0-9][a-z0-9-]{0,63}$`. Unique within the workflow. |
| `agent` | yes | Agent name (must exist in the agent roster). |
| `runtime` | no | Runtime override (`claude`/`codex`/`agy`/`gemini`). Resolved from agent frontmatter when absent. |
| `model` | no | Model tier/alias (`haiku`/`sonnet`/`opus`/`fable`). Resolved from agent frontmatter when absent. |
| `timeout` | no | Per-node dispatch timeout in seconds. Default: 600. Use 900 for long synthesis nodes. |
| `prompt` | yes | Task prompt. Supports `${inputs.<key>}` and `${nodes.<id>.output}` substitution (only declared references are valid). |
| `output_limit` | yes | Total tail-truncate budget in bytes for all upstream outputs substituted into this node's prompt. Mandatory — validate rejects a missing or zero value. |
| `needs` | no | List of node IDs that must complete before this node starts. Empty or absent means root (no dependencies). |

### Variable substitution

Two substitution forms are supported in `prompt`:

- `${inputs.<key>}` — replaced with the value of the named input at run
  time (defaults from the YAML, overridable at the CLI).
- `${nodes.<id>.output}` — replaced with the truncated stdout of the
  named upstream node. Only nodes listed in `needs` (directly or
  transitively) may be referenced; the validator rejects forward
  references and undeclared IDs.

**Prompt injection caveat:** `${nodes.<id>.output}` threads an upstream
LLM node's output verbatim into the downstream prompt. If an upstream
node processes external content (e.g., web fetches, user input), treat
that output as untrusted. The `output_limit` budget limits the maximum
bytes substituted but does not sanitize the content.

### Engine semantics

- **Kahn topological scheduling.** Nodes with no `needs` form the root
  set and run immediately. As nodes complete, their dependents whose
  `needs` are all satisfied are added to the ready set.
- **Fan-out:** multiple root nodes (or nodes with disjoint `needs`) run
  in parallel under a per-run `max_parallel` semaphore (default 4 slots),
  which sits inside the global dispatch governor (default 8 slots across
  all transports).
- **Fan-in:** a node with multiple `needs` waits for all of them before
  starting.
- **Failure propagation:** if a node fails (Go error or non-zero exit
  code), it is marked `failed`. All transitive dependents are marked
  `skipped`. Independent branches already running or in the ready set
  complete normally. The run is marked `failed` when the graph drains.
- **`output_limit` enforcement:** if the sum of upstream outputs exceeds
  a node's `output_limit`, the engine tail-truncates proportionally. A
  `workflow.node.truncated` bus event is emitted (byte counts only, no
  content). The bus never carries node output content.

### Run artifacts

```
<work>/current/workflows/runs/<runId>/
  run.json                    # run status, timing, node states (debounced writes)
  nodes/<id>.stdout           # per-node captured output
```

`run.json` is written atomically via temp-file + rename. State is
debounced ~200ms to avoid high-frequency I/O during parallel runs.

### CLI reference

```sh
# Run a workflow headlessly (blocks until the graph drains)
yakos workflow run <name>
yakos workflow run <name> --run-id <custom-id>
yakos workflow run <name> --operator <operator-id>

# Resume a failed run (re-runs failed/skipped nodes; reuses completed outputs)
yakos workflow resume <name> \
  --prior-run-id <original-run-id> \
  --new-run-id <new-run-id>

# Print the run.json for a given run ID
yakos workflow status <run-id>
```

Runs and state live at `<work>/current/workflows/runs/<runId>/`.

### Resume-from-failure

When a run fails partway through, `yakos workflow resume` re-runs only
the failed and skipped nodes, reusing the completed nodes' outputs:

- **Output pinning:** completed nodes' stdout files are copied into the
  new run directory. The same upstream output is threaded into downstream
  prompts — not a fresh re-dispatch. This preserves expensive partial
  state.
- **New run ID:** resume forks a new `runID` with `parent_run_id`
  pointing at the original. Both the original partial run and the resumed
  run are independently inspectable.
- **YAML-hash pin:** resume computes the SHA-256 of the current workflow
  YAML and compares it against the hash recorded in `run.json`. If the
  YAML has changed (even whitespace), resume fails loudly. Start a new
  run if the graph has changed.

**Non-determinism caveat:** LLM nodes are not reproducible. Re-running a
whole graph re-spends tokens. Resume reuses pinned completed outputs
precisely to avoid re-spending on nodes that already succeeded.

---

## Example workflows

### Example A — parallel review + synthesize (fan-out → fan-in)

```yaml
version: 1
name: pr-review
inputs:
  branch: main
nodes:
  - id: security
    agent: security-reviewer
    model: sonnet
    output_limit: 8000
    prompt: "Review branch ${inputs.branch} for security and data-handling issues."
  - id: quality
    agent: code-reviewer
    output_limit: 8000
    prompt: "Review branch ${inputs.branch} for correctness and code quality."
  - id: synthesize
    agent: release-manager
    timeout: 900
    needs: [security, quality]
    output_limit: 16000
    prompt: |
      Combine these into one prioritized report.
      Security: ${nodes.security.output}
      Quality:  ${nodes.quality.output}
```

Run: `yakos workflow run pr-review`

`security` and `quality` run in parallel (no `needs`). `synthesize` waits
for both, then combines their outputs into a single prioritized report.

### Example B — linear pipeline (research → draft → review)

```yaml
version: 1
name: doc-pipeline
inputs:
  topic: "agent orchestration"
nodes:
  - id: research
    agent: ux-researcher
    output_limit: 4000
    prompt: "Gather the key points a reader needs on: ${inputs.topic}."
  - id: draft
    agent: doc-writer
    needs: [research]
    output_limit: 8000
    prompt: "Write a concise explainer using these points:\n${nodes.research.output}"
  - id: review
    agent: code-reviewer
    needs: [draft]
    output_limit: 8000
    prompt: "Critique this draft for accuracy and clarity:\n${nodes.draft.output}"
```

Run: `yakos workflow run doc-pipeline --run-id doc-run-001`

Each node feeds its output into the next. A linear pipeline is the
degenerate case of Flows — no parallelism, but still benefits from
resume-from-failure if `draft` or `review` fails.

### Example C — multi-model bake-off (fan-out, same task to two models, then compare)

```yaml
version: 1
name: model-bakeoff
inputs:
  task: "Implement a token-bucket rate limiter in Go with tests."
nodes:
  - id: opus
    agent: backend
    model: opus
    output_limit: 6000
    prompt: "${inputs.task}"
  - id: fable
    agent: backend
    model: fable
    output_limit: 6000
    prompt: "${inputs.task}"
  - id: compare
    agent: architect
    needs: [opus, fable]
    output_limit: 12000
    prompt: |
      Compare these two implementations; pick the stronger one and say why.
      Opus:  ${nodes.opus.output}
      Fable: ${nodes.fable.output}
```

Run: `yakos workflow run model-bakeoff`

`opus` and `fable` run in parallel against the same task. `compare` waits
for both, then synthesizes a pick with rationale.

---

## Caveats and known limits

- **Loopback mode: single shared token, self-asserted identity.**
  In default loopback mode, `operator_id` is self-asserted attribution, not
  an auth boundary. Do not expose the loopback console to a network via port
  forwarding or reverse proxy. For multi-machine access, use networked mode
  with mTLS (see [Networked multi-operator mode](#networked-multi-operator-mode)).
- **Networked mode: out-of-band cert distribution.** There is no enrollment
  endpoint. Client certs must be issued via `yakos mtls issue-client` and
  transmitted to operators via secure file transfer. No CRL or OCSP — to
  revoke, remove the CN from `roles.json` or rotate the CA.
- **Transitive prompt injection.** `${nodes.<id>.output}` is substituted
  verbatim. If an upstream node processes external content, treat downstream
  prompts as potentially influenced by untrusted data.
- **Non-determinism.** LLM nodes are not reproducible. A full re-run of a
  workflow re-spends tokens on every node. Use resume to recover partial
  expensive runs.
- **output_limit is mandatory.** Every node requires a non-zero
  `output_limit`. The validator rejects missing or zero values because
  unbounded upstream output substitution can exceed LLM context windows
  silently.
- **YAML-hash pin is strict.** Even a whitespace edit to the workflow YAML
  invalidates outstanding resumes. Plan your graph edits before running if
  you anticipate needing resume.
- **Per-run concurrency.** The per-run semaphore defaults to 4 parallel
  nodes. This sits inside the global dispatch governor (default 8 slots).
  Deep workflow nesting (a node that itself triggers sub-workflows) can
  exhaust the governor cap. Raise `MaxConcurrent` in `ServiceConfig` if
  stalls occur.

---

## Where to go next

- [docs/adr/ADR-0003.md](adr/ADR-0003.md) — design decisions for the
  Flows engine (concurrency model, resume semantics, YAML-hash pin, bus
  invariants).
- [docs/adr/ADR-0004.md](adr/ADR-0004.md) — design decisions for the
  networked-console identity model (mTLS, RBAC, dual identity regime,
  fail-closed bind).
- [runtime-matrix.md](runtime-matrix.md) — per-runtime streaming behavior
  and model tier reference.
- [overview.md](overview.md) — architecture overview including operator
  identity and the dispatch.Service facade.
