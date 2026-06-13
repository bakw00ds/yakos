# Unified console — operator guide

**Introduced in:** v0.40.0.0
**Audience:** operators who want to use the browser-based console for
interactive Chat, workflow orchestration, or unified dashboard access.

Companion docs: [overview.md](overview.md) for architecture context,
[docs/adr/ADR-0003.md](adr/ADR-0003.md) for the Flows engine design,
[runtime-matrix.md](runtime-matrix.md) for per-runtime streaming behavior.

---

## Starting the console

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

## Trust model and honest limits

The console is a **same-host, loopback-only, single shared token** server.

- All operators opening the console in browsers on the same machine are
  uid-equivalent — they share the same bearer token and have equal access
  to all tabs including Flows runs and Chat transcripts.
- `operator_id` is **self-asserted attribution**, not an authentication
  boundary. It labels dispatch log entries and presence chips so uid-
  equivalent teammates can see who did what. It does not restrict access.
- Do not expose the daemon port to a network (via port forwarding, proxy,
  or non-loopback bind). Doing so would expose browser-driven, unsandboxed
  `bypassPermissions` dispatch with no real authentication boundary.

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

- **Loopback-only, single shared token.** Do not expose the daemon port to
  a network. operator_id is self-asserted attribution, not an auth boundary.
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
- [runtime-matrix.md](runtime-matrix.md) — per-runtime streaming behavior
  and model tier reference.
- [overview.md](overview.md) — architecture overview including operator
  identity and the dispatch.Service facade.
