---
name: llm-output-gate
description: CI hook that refuses to ship if prompt-eval golden set regresses past threshold or prompt-injection-test fails on HIGH severity
allowed-tools: Bash Read Edit
argument-hint: "[--threshold <pct>] [--injection-severity high|medium|low] [--report <path>]"
mode: [gate]
---

# LLM Output Gate

## Purpose

Pair with `prompt-eval` and `prompt-injection-test` to enforce a CI-
side gate: the build fails if the prompt change regresses the golden
set beyond `<threshold>` percent on any rubric, OR if any injection
payload at or above `<injection-severity>` succeeded.

This is the "don't ship a worse prompt" lever. The gate is wired by
the `eval-engineer` into the project's CI workflow (GitHub Actions,
GitLab pipelines, etc.) and runs on PRs that touch `prompts/**`,
`.claude/agents/**`, or any rubric/dataset/corpus path.

## Scope

- Wraps `prompt-eval` + `prompt-injection-test` invocations and
  composes their results into a single CI verdict.
- Reads project config from `<project>/eval/.gate.yaml`:
  - `regression_threshold`: max acceptable per-rubric drop (default 0%).
  - `aggregate_threshold`: max acceptable aggregate drop (default 2%).
  - `injection_severity_floor`: severity at which a single hit fails
    the gate (default `high`).
  - `paths`: which file changes trigger the gate.
  - `agents`: which agents to test (defaults to all production agents).
- Emits a comment-friendly markdown summary for the PR ("X rubrics
  regressed; Y injection payloads succeeded; gate: FAIL").
- Exit code: 0 = green, 1 = regressed/jailbroken, 2 = config error.
- Designed for `eval-engineer` to wire into CI. The lead does not
  invoke this skill in normal sessions.

## When to use

- As a required CI status check on PRs touching prompt / agent /
  rubric / dataset paths.
- As a manual pre-merge confirmation before promoting a prompt to
  production.
- After accepting a new model baseline, to confirm the gate passes
  against the new pinned baseline before re-enabling on the main
  branch.

## When NOT to use

- During prompt iteration (use `prompt-eval` directly — the gate is
  for the merge boundary, not the inner loop).
- On commits that don't touch prompts/agents/rubrics. The gate skips
  itself when `paths` doesn't match; if you're invoking it manually
  on unrelated changes, you're wasting CI minutes.
- As the only gate. Pair with the project's normal test suite; the
  LLM gate doesn't catch type errors, broken builds, or unrelated
  regressions.

## Automated pass

1. **Detect changed paths.** Skip cleanly if no LLM-relevant path
   changed.
   ```sh
   CHANGED=$(git diff --name-only "$BASE_REF"...HEAD)
   if ! echo "$CHANGED" | grep -qE '^(prompts/|.claude/agents/|eval/(rubrics|datasets|injection-corpus)/)'; then
       echo "no LLM-relevant changes; gate skipped"
       exit 0
   fi
   ```

2. **Load gate config.**
   ```sh
   yq '.' eval/.gate.yaml > /tmp/gate.json
   THRESHOLD=$(jq -r .regression_threshold /tmp/gate.json)
   SEVERITY=$(jq -r .injection_severity_floor /tmp/gate.json)
   ```

3. **Run prompt-eval against the baseline.**
   ```sh
   yakos skill prompt-eval "$PROMPT_ID" \
       --baseline "$BASE_REF" \
       --out /tmp/eval-report.md \
       || EVAL_EXIT=$?
   ```
   Per-rubric deltas are in `/tmp/eval-report.md` and machine-
   readable scores in `eval/runs/<id>/scores.jsonl`.

4. **Run prompt-injection-test on each affected agent.**
   ```sh
   for agent in $(yakos agents list --changed-since "$BASE_REF"); do
       yakos skill prompt-injection-test "$agent" \
           --severity "$SEVERITY" \
           --report /tmp/inj-${agent}.md \
           || INJ_EXIT=$?
   done
   ```

5. **Compose the gate verdict.** Three outcomes:
   - **PASS:** no rubric regressed past `regression_threshold`,
     aggregate change >= `-aggregate_threshold`, no injection at or
     above `injection_severity_floor` succeeded.
   - **FAIL — regression:** at least one rubric regressed too far.
     Report names the rubrics, baseline pass-rate, candidate pass-
     rate, delta.
   - **FAIL — jailbreak:** at least one injection at or above floor
     succeeded. Report names the payload ids, agent ids, what the
     agent did.
   - **FAIL — config:** baseline missing, pin mismatch, etc. Treat
     as gate error, not a prompt failure.

6. **Emit PR comment / job summary.** GitHub Actions example:
   ```sh
   {
       echo "## LLM Output Gate"
       echo ""
       echo "**Verdict:** $VERDICT"
       echo ""
       cat /tmp/eval-report.md
       echo ""
       cat /tmp/inj-*.md
   } >> "$GITHUB_STEP_SUMMARY"
   ```
   Plus PR-comment via `gh pr comment` if the project wires it.

7. **Exit non-zero on FAIL.** CI marks the check failed; the merge
   button is blocked until the prompt is fixed or the baseline
   accepted (with an explicit `RE-BASELINE` commit message that the
   gate recognizes).

## Manual pass

```sh
# 1. Pretend you're CI
BASE_REF="origin/main"
yakos skill prompt-eval "<prompt-id>" --baseline "$BASE_REF"
yakos skill prompt-injection-test "<agent-id>" --severity high

# 2. Read both reports; decide if you'd merge
```

This is what the eval-engineer does locally before pushing — same
two skills, just without the gate's wrapper.

## CI wiring example (GitHub Actions)

```yaml
name: llm-output-gate
on:
  pull_request:
    paths:
      - "prompts/**"
      - ".claude/agents/**"
      - "eval/**"
jobs:
  gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - run: yakos skill llm-output-gate
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          BASE_REF: origin/${{ github.base_ref }}
```

## Known gotchas

- **Re-baselining.** Accepting a new baseline (model bump, dataset
  growth) requires an explicit signal — the project convention is a
  commit message containing `RE-BASELINE: <reason>`. The gate sees
  the marker, runs the eval, stores the new baseline, and passes.
  Without that marker, an intentional baseline change looks like a
  regression and blocks the PR.
- **Threshold of zero is harsh.** Stochastic flips on small datasets
  cross any nonzero threshold. Either grow the dataset, average
  across N runs, or accept a 1–2% threshold. Be explicit in
  `eval/.gate.yaml` about which.
- **Cost on every PR.** A 500-row dataset eval per PR adds up.
  Mitigations: gate only on the touched prompt's dataset (not all),
  cache the system prompt aggressively, run injection corpus only on
  changed agents.
- **Injection corpus secrecy.** If the corpus is checked into a
  public repo, attackers can craft payloads outside the corpus that
  the gate doesn't catch. The framework default corpus is public
  (intentional, OWASP-aligned); project-specific payloads should be
  in a private corpus path the gate also reads.
- **Flaky failures.** A single injection success out of 200 payloads
  can be a real bug or a model stochasticity flicker. The gate
  retries failures once before reporting; persistent failure is a
  block, transient is a warning. Configurable.
- **Pin drift.** If the baseline run was done against a model alias
  that has since rolled, the comparison is meaningless. The gate
  refuses the comparison and emits "config error — re-baseline
  required" rather than silently passing or failing.
- **Local-only dispatch.** The gate calls real APIs. Don't run it on
  forked PRs from untrusted contributors without a maintainer-
  approved workflow trigger; otherwise the API key leaks via fork
  CI.

## References

- `lib/skills/prompt-eval/SKILL.md` — the eval half of the gate.
- `lib/skills/prompt-injection-test/SKILL.md` — the safety half.
- `lib/agents/eval-engineer.md` — wires this gate into CI.
- `eval/.gate.yaml` — per-project gate config.
- `docs/ci-llm-gates.md` — recipes for GH Actions / GitLab / Buildkite.
