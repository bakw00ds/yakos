---
name: plan-quality-eval
description: Score work/current/plan.md against a 6-dimension rubric using a 3-judge cross-vendor LLM panel and log a structured record. Use before specialists start work, to catch a vague or badly-decomposed plan while fixing it is cheap.
allowed-tools: Bash Read
argument-hint: "<plan.md> [--rubric <path>] [--planner-model <id>] [--dry-run]"
mode: [report]
---

# Plan Quality Eval

## Purpose

Score a plan file against a 6-dimension structural rubric using a 3-judge
cross-vendor LLM panel. Surfaces plans that are too vague, badly
decomposed, or missing assumptions before specialists start work —
when fixing the plan is cheap. Writes a structured NDJSON record so
aggregate scoring trends are trackable over time.

Phase 1 = **manual invocation only**. The auto-fire hook (pre-dispatch
gate) comes in Phase 2. Per-project score thresholds come in Phase 2.
Outcome telemetry aggregation comes in Phase 3.

## Scope

- Reads a plan from the path passed as the first argument (typically
  `work/current/plan.md`).
- Parses it into canonical JSON via `extract-plan.sh`.
- Dispatches `plan-judge` 3× in parallel — one per vendor runtime
  (`claude`/`haiku`, `codex`/`gpt-5-nano`, `gemini`/`gemini-2.5-flash`).
- Computes per-dimension medians and a weighted aggregate.
- Applies family-drop: if the planner model family matches a judge family,
  that judge is dropped and the panel operates at size 2.
- Applies dissent flag: if max–min ≥ 0.5 on any dimension, sets
  `dissent: true` and `recommended_action: surface_to_operator` regardless
  of aggregate score.
- Cost ceiling: $0.15 per run. Hard-fails if estimated cost exceeds.
- Writes one NDJSON record to `~/.yakos-state/plan-quality-log.ndjson`.
- Emits a human-readable markdown report to stdout.

## When to use

- Before dispatching specialists on a new plan — catch structural problems
  when they are cheap to fix.
- After a planner produces a revised plan — confirm the revision resolved
  the flagged dimensions.
- During skill calibration (Phase 3) to compare judge accuracy against
  human-rated ground-truth plans.

## When NOT to use

- For one-off "does this look right" spot checks on short notes — the 3-
  judge panel costs money; eyeball the plan instead.
- On plans that are already being executed (specialists mid-flight). Scoring
  a plan that's 80% done produces noise, not signal.
- Without a properly structured plan file (frontmatter + ## Assumptions +
  ## Tasks + ## Risks sections). The extractor will exit non-zero and
  report the structural problem; fix the plan first.
- In Phase 1: do NOT wire this into any hook or auto-fire path. Hooks belong
  in Phase 2.

## Automated pass

```sh
# Run from the project root or any directory; PLAN_FILE is the plan to score.
PLAN_FILE="work/current/plan.md"

bash lib/skills/plan-quality-eval/scripts/score-plan.sh "$PLAN_FILE"
```

The script:
1. Calls `extract-plan.sh "$PLAN_FILE"` → canonical JSON.
2. Dispatches `plan-judge` 3× in parallel, one per vendor.
3. Gathers verdicts, computes per-dimension medians + weighted aggregate.
4. Applies family-drop and dissent checks.
5. Writes one record to `~/.yakos-state/plan-quality-log.ndjson`.
6. Calls `report-plan-score.sh` to emit a markdown report to stdout.
7. Exits 0 on pass, 1 on fail, 2 on extraction error.

## Manual pass

```sh
# 1. Extract + inspect the canonical JSON first
bash lib/skills/plan-quality-eval/scripts/extract-plan.sh work/current/plan.md | jq .

# 2. Score (with mock judges for cost-free validation)
YAKOS_PLAN_JUDGE_MOCK=tests/fixtures/plan-judge-mock \
    bash lib/skills/plan-quality-eval/scripts/score-plan.sh work/current/plan.md

# 3. Generate a standalone report from a prior log record
bash lib/skills/plan-quality-eval/scripts/report-plan-score.sh \
    ~/.yakos-state/plan-quality-log.ndjson
```

## Mock judge contract

Set `YAKOS_PLAN_JUDGE_MOCK=<dir>` to substitute canned judge output in CI
or local tests without burning API cost. When set, `score-plan.sh` looks
for `<dir>/claude.json`, `<dir>/codex.json`, and `<dir>/gemini.json`
instead of calling the actual judge agent. Each file must be a valid JSON
object matching the judge verdict schema:

```json
{
  "model_id": "haiku",
  "scores": {
    "acceptance_criteria_specificity": 1.0,
    "assumption_surfacing": 0.5,
    "decomposition_granularity": 1.0,
    "dependency_clarity": 1.0,
    "domain_boundaries_respected": 1.0,
    "risk_rollback_honesty": 0.5
  },
  "notes": "One-line summary of any concerns."
}
```

Valid score values: `0`, `0.5`, `1.0` only. Unknown fields are ignored.
Missing score fields default to `0` with a warning.

## Known gotchas

- **Family-drop requires `YAKOS_PLANNER_MODEL` env var** (or the
  `planner_model` key in `.yakos.yml`). If not set, family comparison is
  skipped and panel_size stays at 3. Document the planner model used to
  produce the plan so future re-runs can drop the right judge.
- **Cost ceiling is an estimate**, not a hard API limit. The script computes
  a token-count estimate based on plan length × per-model rate. If the
  estimate exceeds $0.15 it refuses to proceed. The estimate is generous;
  actual cost may be lower. Override with `YAKOS_PLAN_EVAL_MAX_COST_USD`
  (float, default `0.15`).
- **Dissent ≠ fail.** A dissenting panel means judges disagree strongly on
  at least one dimension. The recommended action is to surface to the
  operator, not auto-fail. The aggregate score may still be above the
  pass threshold.
- **Panel size 2 is valid.** When a judge family is dropped, the median is
  the average of the two remaining scores. The record documents
  `panel_size: 2` and `dropped_family` so the operator can audit.
- **The plan format is strict.** `extract-plan.sh` expects a YAML
  frontmatter block with `plan_id`, an `## Assumptions` H2, a `## Tasks`
  H2, and a `## Risks` H2. Deviation → exit 2. See `lib/agents/planner.md`
  "Plan artifact structure" section for the canonical format + example.
- **Judge notes are advisory.** The judge's `notes` field is a free-text
  string surfaced in the report. It is not parsed or scored. Judges may
  hallucinate specific line references; treat notes as hints, not proofs.

## References

- `lib/agents/planner.md` — canonical plan format this skill reads.
- `lib/agents/eval-engineer.md` — owns rubric file and judge calibration.
- `lib/skills/plan-quality-eval/rubrics/default.yaml` — 6-dimension rubric.
- `lib/skills/plan-quality-eval/judges/plan-judge.md` — judge agent
  definition.
- `~/.yakos-state/plan-quality-log.ndjson` — log written by this skill.
