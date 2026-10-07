# Flows: `runtime: auto` and `model: auto`

A workflow node may leave its runtime and model to the router (K-142).

```yaml
nodes:
  - id: review
    agent: reviewer
    runtime: auto      # or omit it: same thing
    model: auto
    prompt: "Review ${inputs.diff}"
    output_limit: 8000
```

## Semantics

- `auto` means **no pin**. The engine hands the dispatch Service an empty
  runtime/model; the Service routes at its usual chokepoint (policy rules R1..R6,
  then the resolve chain). The engine does no resolution of its own.
- A concrete `runtime:` or `model:` is an **explicit pin for that node only**. It
  wins over any policy rule, exactly as `--runtime`/`--model` do on the CLI. A
  pin on one node never affects another.
- Each node is dispatched once per run and its decision is recorded, never
  recomputed: the router cannot move a node mid-run. A resumed run routes the
  nodes it re-runs afresh.
- The route class is `default` until the classifier (K-140) supplies one.
- Route metadata is bookkeeping only. It never enters a prompt, a system
  prompt, `--append-system-prompt` or the `--agents` JSON.

## What is recorded

`runs/<runID>/run.json`, per node, once the dispatch has been routed (omitted
for a node that never reached routing, and absent from runs that predate K-142):

```json
"route": {
  "runtime": "codex", "model": "gpt-5.4",
  "rule": "R1", "reason": "rule R1 ...", "class": "default",
  "policy_sha": "<64 hex>",
  "runtime_requested": "auto", "model_requested": "auto"
}
```

`*_requested` echo the node's YAML (`auto`, a pin, or absent). The run view's
node detail shows the same as a text chip: `[route: codex/gpt-5.4 R1 - reason]`.
The node's dispatch ledger event carries `route_rule`, `route_reason`,
`route_class` and `policy_sha` as for any dispatch.

## Dry run

`yakos workflow run <name> --dry-run` asks the router where each node would run
(the same routing step, probes included; nothing is started, logged or pinned)
and exits non-zero if a node cannot be routed. The task size used is the
unsubstituted prompt's, so a size-keyed rule may decide differently once
upstream output is spliced in at run time.
