# Playbook: model-routing-eval

**Used by:** `model-routing-eval` agent, `cli/lib/model-routing.sh`

Step-by-step procedure for running a model-routing eval and producing a
promotion candidate.  The CLI wrapper (`yakos model-routing eval`) handles
argument validation and calls this procedure by dispatching the
`model-routing-eval` agent.

---

## Step 0 — Prerequisites

Before starting a run:

- `jq` must be in PATH (required for log writes and Wilson math).
- `yakos dispatch` must be reachable (YAKOS_ROOT / YAKOS_LIB set).
- The `model-routing-eval-log.ndjson` and
  `model-routing-candidates.ndjson` files live in `~/.yakos-state/`.
  Create the directory if absent.
- A `run_id` is generated once per invocation:
  `run_id = yakos-mr-$(date +%s)-$(head -c8 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c8)`

---

## Step 1 — Validate eval cases

```bash
yakos validate <project-or-framework-root>
```

- Parse `<agent>/eval/case-*.json` for schema conformance.
- Abort on any `[err]` output (schema violations break scoring logic).
- Surface `[warn]` lines but continue.
- Count valid cases.  If `n_cases < min_cases_for_eval` (default 5),
  emit `candidate_refused` with `reason: "min_cases"` and exit.

---

## Step 2 — Choose the judge

Default judge assignment by agent domain:

| Agent domain / mode            | Default judge      | Judge model |
|--------------------------------|--------------------|-------------|
| backend, frontend, mobile,     | `code-reviewer`    | sonnet      |
| database, test-runner          |                    |             |
| cross-cutting with mode:design | `architect`        | opus        |
| all others                     | `code-reviewer`    | sonnet      |

**Hard constraint:** if `--judge <id>` resolves to the same agent id as
the subject, abort immediately with:

```
error: judge and subject are the same agent (<id>); self-evaluation
is forbidden. Use --judge <different-agent>.
```

No `--force` override exists.

---

## Step 3 — Emit eval_run_started

Write to `~/.yakos-state/model-routing-eval-log.ndjson`:

```json
{
  "type": "eval_run_started",
  "ts": "<iso>",
  "run_id": "<uuid>",
  "agent": "<subject-id>",
  "n_cases": 5,
  "tiers": ["haiku", "sonnet", "opus"],
  "judge": "<judge-id>",
  "epsilon": 0.05,
  "max_cost_usd": 5.00
}
```

---

## Step 4 — Run cases

For each `(case_id, tier)` pair (outer loop: cases; inner loop: tiers):

### 4a. Dispatch subject

```bash
yakos dispatch <subject-agent> "<case.task>" \
    --model <tier> \
    --eval-run-id <run_id> \
    --project <project>
```

Capture stdout as `agent_response`.  Record `duration_s` and
`actual_cost_usd` from the dispatch-log's last `dispatch_finished`
record matching this `eval_run_id`.

### 4b. Build judge input

```json
{
  "case_id": "...",
  "task": "...",
  "expected_outcomes": ["..."],
  "rubric": { "criteria": [...] },
  "agent_response": "...",
  "duration_s": 42,
  "actual_cost_usd": 0.003
}
```

### 4c. Dispatch judge

```bash
yakos dispatch <judge-agent> "<judge-input-json>" \
    --project <project>
```

Parse the judge's stdout as JSON.  Expect the judge contract schema:
```json
{
  "case_id": "...", "tier": "...", "composite": 0.87,
  "criteria_scores": [{"name": "...", "score": 1.0}],
  "pass": true, "notes": "..."
}
```

If the judge's response cannot be parsed as valid JSON with a `pass`
field, treat the case as failed and log `judge_parse_error: true`.

### 4d. Emit eval_case record

```json
{
  "type": "eval_case",
  "ts": "<iso>",
  "run_id": "<run_id>",
  "agent": "<subject-id>",
  "case_id": "<case_id>",
  "case_hash": "<sha256 of case JSON>",
  "tier": "<haiku|sonnet|opus>",
  "pass": true,
  "rubric_scores": [{"name": "...", "score": 1.0}],
  "total_cost_usd": 0.003,
  "duration_s": 42,
  "usage": { "input_tokens": 100, "output_tokens": 50 },
  "judge": "<judge-id>",
  "judge_notes": "..."
}
```

### 4e. Cost cap check

After each case, sum all `total_cost_usd` values emitted so far for this
`run_id`.  If `accumulated_cost > max_eval_run_cost_usd`:

```json
{
  "type": "budget_exceeded",
  "ts": "<iso>",
  "run_id": "<run_id>",
  "agent": "<subject-id>",
  "spent_usd": 5.12,
  "cap_usd": 5.00
}
```

Abort the run.  Do not dispatch further cases.

---

## Step 5 — Compute Wilson 95% CI lower bounds

For each tier, compute:

```
k     = number of passing cases
n     = total cases run for this tier
phat  = k / n
z     = 1.96   (95% confidence)
denom = 1 + z^2 / n
center = (phat + z^2 / (2*n)) / denom
margin = z * sqrt( phat*(1-phat)/n + z^2/(4*n^2) ) / denom
lower  = center - margin
```

Implemented via `awk` — no `scipy` or Python dependencies required.

---

## Step 6 — Promotion decision

Determine the "current tier" from the agent's `model:` frontmatter
(or `model-policy:` if already promoted).

**Case A — sufficient cases (`n_cases >= min_cases_for_confidence`, default 12):**

For each candidate tier cheaper than current:

```
if ci_lower[candidate] >= pass_rate[current] - epsilon:
    emit candidate
else:
    emit candidate_refused (reason: low_confidence)
```

**Case B — few cases (5 ≤ n_cases < 12, strict floor):**

A candidate requires BOTH:
- Cost saving ≥ 2× (mean cost of candidate ≤ mean cost of current / 2)
- Pass-rate margin ≥ 0.10 (pass_rate[candidate] ≥ pass_rate[current] + 0.10)

If both conditions are not met:

```json
{"type":"candidate_refused", ..., "reason":"cost_only_no_quality"}
```

**Never emit two candidates in one run** (pick the cheapest tier that
qualifies, or none).

---

## Step 7 — Emit run-finished and candidate

### eval_run_finished

```json
{
  "type": "eval_run_finished",
  "ts": "<iso>",
  "run_id": "<run_id>",
  "agent": "<subject-id>",
  "tier_pass_rates":  {"haiku": 0.40, "sonnet": 0.92, "opus": 0.93},
  "tier_mean_costs":  {"haiku": 0.001, "sonnet": 0.005, "opus": 0.020},
  "tier_ci_lower":    {"haiku": 0.18, "sonnet": 0.72, "opus": 0.74},
  "candidate_emitted": true,
  "candidate_tier": "sonnet",
  "candidate_reason": "ci_lower[sonnet]=0.72 >= pass_rate[opus]=0.93 - 0.05"
}
```

### candidate record (when emitted)

Append to `~/.yakos-state/model-routing-candidates.ndjson`:

```json
{
  "agent": "<id>",
  "current_model": "opus",
  "suggested_model": "sonnet",
  "evidence": {
    "pass_rates":  {"haiku": 0.40, "sonnet": 0.92, "opus": 0.93},
    "ci_lower":    {"haiku": 0.18, "sonnet": 0.72, "opus": 0.74},
    "mean_costs":  {"haiku": 0.001, "sonnet": 0.005, "opus": 0.020},
    "n_cases": 14,
    "eval_run_id": "<run_id>",
    "epsilon_used": 0.05,
    "judge": "code-reviewer"
  },
  "estimated_monthly_savings_usd": 23.40,
  "generated_at": "<iso>"
}
```

Latest-per-agent semantics: `yakos model-routing list` and `show` use
the record with the highest `generated_at` per agent.

---

## Step 8 — Human-readable summary

Print to stdout:

```
model-routing eval: <agent>  run=<run_id>
  cases: <n>  judge: <judge>  budget: $<spent>/$<cap>

  tier     pass-rate  ci-lower  mean-cost
  haiku      40%        18%      $0.001
  sonnet     92%        72%      $0.005
  opus       93%        74%      $0.020

  candidate: sonnet  (savings ≈ $23.40/mo)
    reason: ci_lower[sonnet]=0.72 ≥ pass_rate[opus]=0.93 − ε=0.05
```

Or, if refused:

```
  candidate: refused
    reason: low_confidence — ci_lower[sonnet]=0.65 < 0.88
```

---

## Settings reference

Defaults apply when `~/.yakos-state/settings.json` is absent or lacks
the `model_routing` key.

| Key | Default | Meaning |
|-----|---------|---------|
| `epsilon_pass_rate` | 0.05 | Max acceptable pass-rate drop when promoting down |
| `min_cases_for_eval` | 5 | Minimum cases required to start a run |
| `min_cases_for_confidence` | 12 | Cases required to use CI-only gate (vs strict floor) |
| `max_eval_run_cost_usd` | 5.00 | Hard spend cap per eval run |
| `weekly_max_cost_usd` | 50.00 | Rolling 7-day spend limit (enforced at run-start) |
