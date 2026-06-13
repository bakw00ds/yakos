---
name: prompt-eval
description: Run a prompt against a golden dataset and report per-rubric regression vs the prior pinned version, not just an aggregate score. Use when changing a prompt, to confirm it didn't regress quality on known cases.
allowed-tools: Bash Read Edit
argument-hint: "<prompt-id> [--dataset <name>] [--baseline <git-ref>] [--model <alias>]"
mode: [report]
---

# Prompt Eval

## Purpose

Score a prompt change against a fixed golden dataset and surface
regressions per rubric — the failure modes that hide inside an
aggregate "92% accuracy" number. Pins three things so the run is
reproducible:

- **Model version.** The exact provider model id (e.g.,
  `claude-opus-4-7`, `gpt-5.1-2026-04-15`), not an alias.
- **Rubric version.** A content hash of the rubric file at eval time.
- **Dataset version.** A content hash of the dataset at eval time.

Without those three pins, week-over-week comparisons drift silently
when the model alias rolls forward or someone edits a rubric line.

## Scope

- Reads a prompt from `<project>/prompts/<prompt-id>/{system.md,user.tmpl}`.
- Reads a dataset of `(input, expected, rubric_subset)` triples from
  `<project>/eval/datasets/<dataset>.jsonl`.
- Reads rubrics from `<project>/eval/rubrics/<name>.yaml`. Each rubric
  is a named pass/fail (or 0–1 scalar) check applied to the model's
  output for a given input.
- Runs the prompt over the dataset against the configured model,
  scores each example per rubric, writes results to
  `<project>/eval/runs/<run-id>/`.
- Diffs against the baseline run (default: most recent run on `main`)
  and reports per-rubric deltas. Aggregate score is included but
  secondary.
- Designed for `eval-engineer` and `prompt-engineer` to share. The
  prompt-engineer iterates the prompt; the eval-engineer owns the
  rubric and dataset.

## When to use

- Before merging a prompt change, to confirm no rubric regressed.
- After a model version bump, to re-baseline (run, accept the new
  baseline, commit).
- During prompt iteration, to keep a tight feedback loop on which
  rubric is improving and which is being traded away.
- As the data source for `llm-output-gate` (CI-side gate).

## When NOT to use

- For one-off "does this look right" spot checks — eyeball the output,
  don't run the whole dataset.
- For ranking models against each other (model bake-off) — that's a
  different harness with different controls.
- Without a dataset. If the project hasn't built golden examples yet,
  the answer is "build the dataset first." A 10-example dataset is
  better than no dataset; a zero-example "vibes" eval is worse than
  not running at all.

## Automated pass

1. **Resolve pins.**
   ```sh
   MODEL_ID=$(yakos runtime-info --model "${MODEL:-claude}" --resolve-version)
   RUBRIC_HASH=$(sha256sum eval/rubrics/${RUBRIC:-default}.yaml | cut -c1-12)
   DATASET_HASH=$(sha256sum eval/datasets/${DATASET:-default}.jsonl | cut -c1-12)
   RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-${MODEL_ID}-${DATASET_HASH}"
   ```

2. **Run the prompt over the dataset.** One call per row. Cache the
   system prompt (it's identical across rows) so the cost stays
   reasonable on a 500-row dataset.
   ```sh
   yakos eval run \
       --prompt "$PROMPT_ID" \
       --dataset "eval/datasets/${DATASET}.jsonl" \
       --rubric "eval/rubrics/${RUBRIC}.yaml" \
       --model "$MODEL_ID" \
       --out "eval/runs/${RUN_ID}/"
   ```

3. **Score per rubric.** Each row in the dataset declares which
   rubrics apply (a row testing tone doesn't get scored on factual
   accuracy). Output is `eval/runs/<run-id>/scores.jsonl` with
   `{row_id, rubric, pass, score, notes}` per line.

4. **Diff against baseline.** Default baseline is the most recent run
   tagged on `main`; override with `--baseline <git-ref>` to compare
   against a specific commit's run.
   ```sh
   yakos eval diff \
       --baseline "eval/runs/$(git show "$BASELINE":eval/.latest-run)" \
       --candidate "eval/runs/${RUN_ID}/" \
       --by rubric > eval/runs/${RUN_ID}/diff.md
   ```

5. **Compose the report.** Markdown table:
   - Per rubric: baseline pass-rate, candidate pass-rate, delta, sign.
   - Highlight any rubric with delta < 0 in red (regression).
   - Aggregate at the bottom, but flagged "secondary — rubric deltas
     above are the signal."
   - Pin block at top: `model=<id>`, `rubric=<hash>`,
     `dataset=<hash>`, `run-id=<id>`, `baseline=<git-ref>`.

6. **Exit code.** Zero if no rubric regressed beyond the project's
   tolerance (default: 0% — any rubric regression is a failure). Non-
   zero if regressed. The CI gate (`llm-output-gate`) consumes this
   exit code.

## Manual pass

If the harness isn't wired yet:

```sh
# 1. Pin the run
MODEL_ID=$(yakos runtime-info --model claude --resolve-version)
echo "model=$MODEL_ID rubric=$(sha256sum eval/rubrics/default.yaml | cut -c1-12)"

# 2. Run the prompt over the dataset by hand (small N)
while IFS= read -r row; do
    yakos dispatch prompt-runner "$(echo "$row" | jq -r .input)" \
        --model "$MODEL_ID" --json >> eval/runs/manual.jsonl
done < eval/datasets/default.jsonl

# 3. Open the prior run side-by-side and eyeball per-rubric deltas
diff <(jq -s 'group_by(.rubric) | ...' eval/runs/baseline.jsonl) \
     <(jq -s 'group_by(.rubric) | ...' eval/runs/manual.jsonl)
```

## Known gotchas

- **Model alias drift.** Pinning to `claude-opus-4` (alias) instead of
  `claude-opus-4-7` (concrete version) means a silent re-baseline the
  next time the alias rolls. The skill resolves the alias to a
  concrete id and stores that. If the runtime won't expose the
  concrete id, mark the run `unpinned` in the report and warn loudly.
- **Rubric edits invalidate baselines.** A 1-character change to a
  rubric file changes its hash; the diff against an old baseline now
  spans an apples-to-oranges comparison. The skill refuses to compare
  runs with different `rubric_hash` values unless `--force` is set.
  Editing a rubric should trigger a re-baseline, not a "let me just
  compare anyway."
- **Rubric subset coverage.** If the dataset adds a row that doesn't
  list any rubric, scoring silently skips it. The skill warns when
  any row has zero applicable rubrics.
- **Stochastic outputs.** Even at temperature 0, providers are not
  bitwise-deterministic. The dataset should be large enough that a
  single-row flip doesn't cross the regression threshold. Below ~50
  rows per rubric, consider averaging across N runs.
- **Cost.** A 500-row dataset on opus is ~$5–$15 per run; on cheap
  models it's <$1. Don't run on every commit — gate to PR open or
  prompt-touched paths.
- **Eval set leakage.** If the same examples train your few-shot
  block, the eval is meaningless. Keep `eval/datasets/` separate from
  `prompts/<id>/examples.md`, and document the firewall in the
  project's `decisions.md`.

## References

- `lib/agents/eval-engineer.md` — owns the rubric and dataset.
- `lib/agents/prompt-engineer.md` — owns the prompt body.
- `lib/skills/llm-output-gate/SKILL.md` — CI-side gate that consumes
  this skill's exit code.
- `docs/eval-format.md` — dataset / rubric file schemas.
