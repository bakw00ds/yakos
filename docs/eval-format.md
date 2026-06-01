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

---

## Phase 2: eval harness mechanics

Phase 2 ships `yakos model-routing eval` — operator runs it to produce a
promotion candidate (or a refused-reason).  Promotion itself is Phase 3.

### Running an eval

```bash
yakos model-routing eval <agent-id> [--judge <agent>] \
                                     [--max-cost-usd <n>] \
                                     [--project <path>]
```

The harness:

1. Validates `<agent>/eval/` cases against the schema above.
2. Refuses if `n_cases < min_cases_for_eval` (default 5).
3. Dispatches each case at haiku / sonnet / opus using
   `yakos dispatch --model <tier> --eval-run-id <run_id>`.
4. Scores each response with a judge agent (never the same agent as the
   subject — this is a hard constraint with no `--force` override).
5. Computes Wilson 95% CI lower bounds on per-tier pass-rates.
6. Emits a candidate to `~/.yakos-state/model-routing-candidates.ndjson`
   or a `candidate_refused` record with a machine-readable reason.

### Anti-self-congratulation constraint

Enforced at two layers:

- **CLI layer** (`cli/lib/model-routing.sh`): if `--judge` resolves to
  the same agent id as the subject, the command exits with a fatal error
  before any dispatch.
- **Agent definition** (`lib/agents/model-routing-eval.md`): tools list
  is `[Read, Bash, Grep]` — no `Edit` or `Write`.  No path through Phase 2
  can rewrite an agent's `model:` frontmatter.

### Wilson 95% CI guard

Candidate emission requires the lower bound of the candidate tier's Wilson
confidence interval to be within epsilon of the current tier's observed
pass-rate.  For runs with fewer than `min_cases_for_confidence` (default 12)
cases, a stricter floor applies: ≥2× cost saving AND ≥0.10 pass-rate margin.

Formula (pure awk, no scipy):

```
phat   = k / n
z      = 1.96
denom  = 1 + z^2/n
center = (phat + z^2/(2n)) / denom
margin = z * sqrt(phat*(1-phat)/n + z^2/(4*n^2)) / denom
lower  = center - margin
```

### Reviewing candidates

```bash
yakos model-routing list           # latest candidate per agent
yakos model-routing show <agent>   # full evidence + last 3 run records
```

### Log files

| File | Contents |
|------|----------|
| `~/.yakos-state/model-routing-eval-log.ndjson` | `eval_run_started`, `eval_case`, `eval_run_finished`, `candidate_refused`, `budget_exceeded` records |
| `~/.yakos-state/model-routing-candidates.ndjson` | One record per candidate; latest-per-agent semantics for `list`/`show` |

### model_routing settings

These keys live under `model_routing:` in `~/.yakos-state/settings.json`.
Defaults apply when the file is absent or the key is missing — the CLI
never requires the file to exist.

```json
{
  "model_routing": {
    "epsilon_pass_rate":        0.05,
    "min_cases_for_eval":       5,
    "min_cases_for_confidence": 12,
    "max_eval_run_cost_usd":    5.00,
    "weekly_max_cost_usd":      50.00
  }
}
```

| Key | Default | Meaning |
|-----|---------|---------|
| `epsilon_pass_rate` | 0.05 | Maximum acceptable pass-rate drop when promoting to a cheaper tier |
| `min_cases_for_eval` | 5 | Minimum cases to start a run; fewer cases → `candidate_refused` with reason `min_cases` |
| `min_cases_for_confidence` | 12 | Cases required for the CI-only gate; below this the strict floor applies |
| `max_eval_run_cost_usd` | 5.00 | Hard per-run spend cap; run aborts mid-case if exceeded |
| `weekly_max_cost_usd` | 50.00 | Rolling 7-day spend limit across all eval runs |

### Phase 3 preview

`yakos model-routing promote <agent> [--tier <tier>]` — writes
`model-policy: <tier>` into the agent frontmatter after operator review.
`yakos model-routing reject  <agent>` — records a rejection in the log
and suppresses future candidates for a configurable cool-down period.
`yakos model-routing history [<agent>]` — tabular view of all past runs.
