# eval-format — Golden-Case Schema and Eval Harness Contract

## Overview

The model-routing eval harness uses hand-authored golden-case files to
measure whether a given model tier (haiku / sonnet / opus) meets quality
and cost requirements for a specific agent's domain.

Each agent that participates in model routing must have an `eval/`
directory co-located with its agent definition.  The eval harness runs
the cases, a judge agent scores the results, and the operator reviews
per-tier evidence before promoting a routing policy via
`yakos model-routing promote <agent>`.

## Directory layout

```
lib/agents/
  test-runner.md              # agent definition (stays at top level)
  test-runner/
    eval/
      case-01-go-mod-vendored.json
      case-02-pytest-conftest-failure.json
      ...

# Project override path
<project>/.claude/agents/
  my-agent.md
  my-agent/
    eval/
      case-01-*.json
```

The harness always checks the project-level override path first; if a
case id appears in both, the project-level file wins (same precedence as
agent definitions).

## Golden-case JSON schema

Each file is named `case-<NN>-<slug>.json` and must validate against the
following schema.  `yakos validate --strict` errors on violations;
default mode warns.

```json
{
  "case_id":   "<string — unique within this agent's eval/ dir>",
  "task":      "<string — the exact prompt sent to the agent under test>",

  "rubric": {
    "criteria": [
      {
        "name":   "<string — short label for the criterion>",
        "weight": <number in (0, 1] — weights need not sum to 1;
                   the judge normalizes>,
        "type":   "binary | scalar_0_1"
      }
    ]
  },

  "expected_outcomes": ["<string>", ...],
  "context_files":     ["<relative path>", ...],
  "max_duration_s":    <integer > 0>,
  "max_cost_usd":      <number  > 0>,
  "tags":              ["<string>", ...]
}
```

### Field reference

| Field | Type | Meaning |
|---|---|---|
| `case_id` | string | Stable identifier for this case. Must be unique within the agent's `eval/` dir. Used in dispatch-log entries (`eval_run_id` prefix). |
| `task` | string | The prompt sent verbatim to the agent during an eval run. Write it as a real operator task — not a synthetic test. |
| `rubric.criteria` | array | Ordered list of scoring dimensions the judge evaluates. |
| `criteria[].name` | string | Short label shown in eval reports. |
| `criteria[].weight` | number (0,1] | Relative importance. The judge normalises across criteria before computing a composite score. |
| `criteria[].type` | enum | `"binary"` — the criterion is either met (1.0) or not (0.0). `"scalar_0_1"` — the judge assigns a continuous score. |
| `expected_outcomes` | string[] | Plain-English statements of what a correct response must include. The judge uses these as ground truth. Must be non-empty. |
| `context_files` | string[] | Paths relative to the project root that the agent should have access to. May be empty; the harness makes them readable. |
| `max_duration_s` | integer > 0 | Wall-clock budget for this case. Harness kills + fails the run if exceeded. |
| `max_cost_usd` | number > 0 | USD budget for this case. Harness records a budget_violation if the actual cost exceeds this. |
| `tags` | string[] | Free-form labels for filtering (e.g. `"go"`, `"flake-detection"`, `"ci"`). May be empty. |

## Bootstrap workflow

1. **Hand-author cases first.** Write 3–7 cases that cover the agent's
   core scenarios: the happy path, a failure the agent must diagnose,
   and an edge case the agent must NOT misclassify.

2. **Run `yakos validate`** to check schema conformance before wiring
   the cases into an eval run.

3. *(Phase 2 — forthcoming)* `yakos model-routing bootstrap-cases
   <agent> --from-log` mines `~/.yakos-state/dispatch-log.ndjson` for
   successful runs of the agent and generates case stubs.  Review and
   tighten the stubs before treating them as golden truth.

4. **Run the eval harness** (Phase 2) with
   `yakos model-routing eval <agent> --tiers haiku,sonnet,opus`.
   Results land in `work/current/model-routing/<agent>/`.

5. **Review and promote** (Phase 3) with
   `yakos model-routing promote <agent> --tier sonnet` once evidence
   shows the cheaper tier meets the rubric at acceptable cost.

## Judge contract

The judge agent receives one JSON object per eval run:

```json
{
  "case_id":           "...",
  "task":              "...",
  "expected_outcomes": ["..."],
  "rubric":            { "criteria": [...] },
  "agent_response":    "...",
  "duration_s":        42,
  "actual_cost_usd":   0.003
}
```

The judge must return a JSON object:

```json
{
  "case_id":        "...",
  "tier":           "haiku | sonnet | opus",
  "composite":      0.87,
  "criteria_scores": [
    { "name": "...", "score": 1.0 }
  ],
  "pass":           true,
  "notes":          "optional free-text rationale"
}
```

Full judge mechanics (prompting strategy, multi-turn clarification,
disagreement resolution) land in Phase 2.  The schema above is stable
and will not change between phases.

## Worked example

See `lib/agents/test-runner/eval/` for five hand-authored cases covering
the test-runner agent's domain:

- `case-01-go-mod-vendored.json` — vendored Go module, flake detection
- `case-02-pytest-conftest-failure.json` — fixture collection failure
- `case-03-flaky-snapshot.json` — snapshot mismatch on CI
- `case-04-no-tests-found.json` — exit-0 false positive
- `case-05-segfault-in-cgo.json` — cgo SIGSEGV diagnosis
