# Playbook: plan-quality-calibration

**Used by:** `eval-engineer` agent (`references: playbook:plan-quality-calibration`)

Full procedure for owning the plan-quality-eval skill's rubric and judge
calibration: responsibilities, phase timeline, and the quarterly
bias-check ritual. The agent definition keeps the binding rules; this
file holds the step-by-step detail moved out of it to fit the agent
line budget. Nothing here relaxes those rules.

---

## Plan-quality evals

The eval-engineer owns the plan-quality-eval skill's rubric and
judge calibration. Responsibilities:

1. **Own the rubric file** at
   `lib/skills/plan-quality-eval/rubrics/default.yaml`. Any change
   to rubric weights, dimension definitions, or the pass threshold
   (0.75) requires eval-engineer review and a rubric hash bump in
   the promotion log. Changes to rubric content invalidate all
   prior baseline comparisons — document the change date in the
   rubric's `schema_version` field.

2. **Own judge calibration (Phase 3).** When Phase 3 is shipped,
   the eval-engineer maintains a human-rated ground-truth set of
   plans (at `evals/plan-quality/ground-truth.jsonl`) and runs a
   quarterly calibration cycle: score the ground-truth set with
   the 3-judge panel, compare judge verdicts to human labels,
   compute judge accuracy per dimension, and surface systematic
   bias (e.g., one vendor consistently over-scores assumption
   coverage).

3. **Review drift quarterly.** Check `~/.yakos-state/plan-quality-log.ndjson`
   for:
   - Dissent rate trending up (judges diverging — rubric may be
     ambiguous).
   - Aggregate score distribution shifting (plans getting better or
     the rubric is losing discriminative power).
   - Family-drop frequency (are planners always on the same vendor,
     reducing panel diversity?).
   Surface findings to the operator via a quarterly report.

4. **Do NOT promote rubric threshold changes unilaterally.**
   Raising or lowering the 0.75 pass threshold is a human decision.
   The eval-engineer may recommend and provide supporting data,
   but the operator approves the change.

5. **Mock judge contract.** When CI runs `tests/run-plan-quality-eval.sh`,
   it sets `YAKOS_PLAN_JUDGE_MOCK=tests/fixtures/plan-judge-mock/<case>/`.
   The eval-engineer owns the canned JSON verdict files in
   `tests/fixtures/plan-judge-mock/`. Update them when the rubric
   changes or when new fixture plans are added.

### Phase timeline

- **Phase 1 (current):** manual invocation only via
  `bash lib/skills/plan-quality-eval/scripts/score-plan.sh <plan.md>`.
  No auto-fire hooks. Eval-engineer role: rubric ownership.
- **Phase 2:** auto-fire hook wires the skill as a pre-dispatch gate.
  Eval-engineer role: per-project threshold configuration.
- **Phase 3:** outcome telemetry, judge calibration, ground-truth
  dataset, quarterly calibration cycle. Eval-engineer owns all of it.

## Quarterly Bias-Check Ritual (Phase 3)

Run quarterly (every ~90 days) to verify that the judge panel's scoring
remains aligned with human-rated ground truth.

### Steps

1. **Run calibration against the golden set:**
   ```
   yakos skill plan-quality-eval --calibrate
   ```
   This invokes `score-plan.sh --calibrate`, scores all plans under
   `lib/skills/plan-quality-eval/golden/`, compares panel medians to
   `labels.yaml` per dimension, and writes a record to
   `~/.yakos-state/plan-quality-calibration-log.ndjson`.

2. **Evaluate results:**
   - If **overall calibration < 0.85**: file an issue with the calibration
     report output. Propose a rubric revision addressing the dimensions
     with lowest agreement. Do NOT apply changes unilaterally.
   - If **any single dimension < 0.70**: flag that dimension specifically.
     Low per-dimension agreement indicates rubric language that judges
     interpret inconsistently — the scoring_guide or definition needs
     tightening.
   - If **any judge shows consistent bias > ±0.2 signed delta** on a
     dimension: flag the judge + dimension pair. Consider requesting that
     the operator add or replace a judge model for that dimension.

3. **Rotate one bad-fixture plan per quarter:**
   Replace one `bad-*` plan with a new failure pattern drawn from recent
   production incidents (plans that scored well but produced high scope
   creep or rework). This prevents the golden set from ossifying around
   patterns the judges have "memorized."
   - Archive the replaced plan to `lib/skills/plan-quality-eval/golden/archive/`.
   - Update `.version` after any golden-set change:
     ```
     find golden -name 'plan.md' | sort | xargs cat | shasum -a 256
     ```
   - Commit the new `.version` alongside the fixture change.

4. **Re-baseline `.version` on every rubric edit:**
   Any change to `rubrics/default.yaml` invalidates prior calibration
   comparisons. Bump the `schema_version` field in the rubric, recompute
   `.version`, and note the change date in a commit message.

5. **Run `yakos plan score correlate`** to check whether rubric changes
   improve the empirical correlation between plan scores and outcomes:
   ```
   yakos plan score correlate --since <date-of-last-rubric-change>
   ```
   If Pearson r for any dimension drops after a rubric change, the change
   may have reduced discriminative power — review before shipping.

### Thresholds (do not adjust unilaterally)

| Metric | Trustworthy | Needs review | Action required |
|---|---|---|---|
| Overall calibration | >= 0.85 | 0.70–0.85 | File issue |
| Per-dimension agreement | >= 0.70 | < 0.70 | Flag + propose revision |
| Judge signed delta | |delta| < 0.2 | |delta| >= 0.2 | Flag judge |

Raising or lowering the 0.85 trustworthy-gate threshold is an operator
decision. The eval-engineer may recommend and provide supporting data,
but the operator approves the change.
