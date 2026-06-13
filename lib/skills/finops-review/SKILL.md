---
name: finops-review
description: Analyze the dispatch-log for per-feature spend, cache hit rate, and model routing, surfacing optimization opportunities. Use when costs look high, before a budget review, or when hunting for ways to cut LLM spend.
allowed-tools: Bash Read
argument-hint: "[--since <ISO>] [--feature <tag>] [--top N]"
mode: [report]
---

# FinOps Review

## Purpose

Look at the dispatch-log with a finance hat on. Answer:

- Where is the money going? Per feature, per agent, per model.
- What fraction of input tokens are hitting cache? (Anything below
  ~70% on a stable system prompt is a smell.)
- Is the routing sensible? Are opus calls doing work that haiku /
  gpt-5-nano / gemini-flash could do for 1/30th the cost?
- Are there workloads on the realtime API that should be on the
  batch API (50% discount, 24h SLA)?

The output is a list of *opportunities*, ranked by estimated monthly
savings, not a blame report. Owned by the `ai-finops` agent.

## Scope

- Reads `~/.yakos-state/dispatch-log*.ndjson` (current + rotated).
- Joins with the agent registry (model alias per agent) and the
  runtime billing snapshot to compute per-call cost.
- Computes:
  - **Spend by feature.** Tag-based: each dispatch carries a
    `feature_tag` (set by the lead or inferred from the calling
    agent's domain).
  - **Cache hit rate per system prompt.** Grouped by `system_prompt_hash`.
    Low hit rates point at unstable prompts (date-stamped headers,
    shuffled examples, etc.).
  - **Model routing audit.** For each agent, the distribution of
    model choices. Opus on a `cheap`-eligible agent is flagged.
  - **Batch-eligible candidates.** Workloads with high volume + low
    latency-sensitivity (offline rubric scoring, summarization
    backfills, etc.) that are running on the realtime API.
- Output is a markdown report with three sections: top spend,
  optimization opportunities, and recommended next actions.

## When to use

- Monthly finops review, before the spend report goes to the budget
  owner.
- After a usage spike, to find the cause.
- Before a pricing renegotiation with a provider — bring real
  numbers to the meeting.
- When a feature flag rolls out and you want to know its cost
  fingerprint before going to GA.
- As input to the quarterly model-routing review (which agents
  should be downgraded / upgraded).

## When NOT to use

- For real-time per-call cost lookup — `yakos cost --tail` does that.
- As a substitute for the runtime's billing dashboard. yakOS
  estimates are best-effort; the provider invoice is authoritative.
  This skill finds *patterns* the dashboard doesn't surface.
- For projects with <100 dispatches in the window — the noise floor
  is too high to draw conclusions.

## Automated pass

1. **Pull raw dispatch data.**
   ```sh
   SINCE="${SINCE:-$(date -u -v-30d +%Y-%m-%d 2>/dev/null || date -u -d '30 days ago' +%Y-%m-%d)}"
   yakos cost --since "$SINCE" --json --raw > /tmp/dispatches.jsonl
   ```

2. **Spend by feature.** Group by `feature_tag`, sum cost. Top N
   features get listed; tail is "other."
   ```sh
   jq -s 'group_by(.feature_tag) | map({feature: .[0].feature_tag, cost: map(.cost_usd) | add, calls: length}) | sort_by(-.cost)' \
       /tmp/dispatches.jsonl > /tmp/by-feature.json
   ```

3. **Cache hit rate per system prompt.** Pull
   `usage.cache_read_input_tokens` vs.
   `usage.cache_creation_input_tokens` vs. `usage.input_tokens` per
   `system_prompt_hash`. Flag prompts where
   `cache_read / (cache_read + uncached) < 0.7`.

4. **Routing audit.** For each agent_id, list the models actually
   used and the model declared in its agent file.
   - `agent.model: cheap` but actual = `claude-opus-4-7` → flag
     "model override" (lead manually upgraded; check rationale).
   - `agent.model: opus` but task prompt is <500 tokens and output is
     <100 tokens → flag "best-when-cheap-would-do."

5. **Batch eligibility.** Heuristic: an agent's calls are batch-
   eligible if (a) volume > 100/day, (b) p99 user-facing latency
   tolerance > 1h (declared in agent frontmatter), (c) calls are
   independent (no chaining). The skill emits a candidate list.

6. **Pending routing candidates.**
   Read `~/.yakos-state/model-routing-candidates.ndjson` and surface
   any pending model-routing opportunities as part of the review.
   ```sh
   # List pending candidates, ranked by estimated monthly savings.
   MR_CANDS="${HOME}/.yakos-state/model-routing-candidates.ndjson"
   if [ -s "$MR_CANDS" ]; then
       echo "### Pending model-routing candidates"
       jq -rs '
           group_by(.agent) |
           map(sort_by(.generated_at) | last) |
           sort_by(-.estimated_monthly_savings_usd) |
           .[] |
           "  \(.agent): \(.current_model) -> \(.suggested_model)" +
           "  est. savings=~$\(.estimated_monthly_savings_usd)/mo" +
           "  n=\(.evidence.n_cases)  run=\(.evidence.eval_run_id)"
       ' "$MR_CANDS"
       echo
       echo "  Promote via: yakos model-routing promote <agent-id>"
       echo "  Reject via:  yakos model-routing reject  <agent-id> [--note \"reason\"]"
   fi
   ```
   Each candidate entry includes `estimated_monthly_savings_usd` (from
   the eval run), the evidence `n_cases`, and the eval run id so the
   operator can cross-reference the eval log. List ranked by savings
   desc; tail roll into "and N more" for long lists (> 10).

7. **Compose the report.**
   - **Headline:** total spend, vs prior period delta.
   - **Top features by spend:** table, with "% of total" column.
   - **Optimization opportunities:** ranked by est. monthly savings.
     Each item: what to change, why, est. $/mo saved, est. effort
     (hours). Include pending model-routing candidates from step 6
     in this ranking.
   - **Routing audit:** agents whose actual model differs from
     declared; agents that should be downgraded.
   - **Cache health:** prompts under 70% hit rate, with the
     suspected cause (volatile prefix, low call volume, etc.).
   - **Batch candidates:** list with current realtime cost vs.
     batch-equivalent.
   - Pin block: window, dispatch count, source log version.

8. Optionally post to `$YAKOS_FINOPS_WEBHOOK` if `--post` is set.

## Manual pass

```sh
# 1. Top features by spend
yakos cost --since 2026-04-01 --by feature --json | jq 'sort_by(-.cost_usd) | .[0:10]'

# 2. Cache hit rate (claude only — others lack the field as of v0.6)
yakos cost --since 2026-04-01 --raw | \
    jq -s 'group_by(.system_prompt_hash) | map({hash: .[0].system_prompt_hash, hit_rate: ((map(.cache_read) | add) / ((map(.cache_read) | add) + (map(.input_uncached) | add)))})'

# 3. Eyeball routing
yakos cost --since 2026-04-01 --by agent --by model
```

Skim for the obvious wins — usually one feature accounts for 60%+
of spend, and within that feature, one agent or one prompt is the
hot spot.

## Known gotchas

- **Estimate vs. actual.** Costs are computed from `usage` fields if
  present, otherwise from chars/4 estimates. Mixing the two in one
  report is misleading. The skill marks each row source = `actual`
  or `estimate` and reports them separately when the mix is large.
- **Cache fields are runtime-specific.** Claude reports
  `cache_read_input_tokens` since v0.5; codex/gemini have different
  shapes (or none). The skill normalizes via the runtime adapter;
  agents on a runtime without cache reporting are listed as
  "cache-unknown" not "cache-cold."
- **Feature tagging discipline.** `feature_tag` is only as good as
  the leads who set it. If 40% of dispatches are tagged
  `untagged`, the top-features view is useless. Recommend the
  project enforce tagging via a pre-dispatch hook (separate skill).
- **Batch eligibility false positives.** Marking work batch-eligible
  doesn't mean the API supports it for that runtime + workload. Some
  tools / multi-turn flows aren't batch-able. The skill produces a
  candidate list; the human confirms eligibility per workload.
- **Best-when-cheap-would-do detection.** The heuristic (small
  prompt, small output) misses cases where opus is genuinely needed
  for reasoning quality. Treat the flag as "investigate," not
  "downgrade now." Pair with prompt-eval to confirm haiku doesn't
  regress before downgrading.
- **PII in feature tags.** Some leads embed customer ids in tags.
  Don't post the report to a shared webhook without scrubbing —
  same caveat as `cost-summary`.
- **Multi-machine.** dispatch-log is per-machine. For org-wide
  finops, ship logs to a central host before running this skill.

## References

- `lib/agents/ai-finops.md` — owns this skill.
- `lib/skills/cost-summary/SKILL.md` — daily/weekly summary; pair
  with finops-review for the deeper cut.
- `cli/lib/cost.sh` — underlying cost command.
- `docs/runtime-matrix.md` — which runtimes report cache and real
  token counts.
- `docs/batch-api.md` — batch-eligibility heuristics in detail.
