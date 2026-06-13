---
name: cost-summary
description: Roll up the yakos dispatch-log into a per-runtime / per-agent / per-day cost summary, optionally posting to a webhook. Use when reviewing spend, before a budget check, or when asked "what did this cost?".
allowed-tools: Bash Read
argument-hint: "[--since <ISO>] [--by agent|runtime|day] [--post]"
mode: [report]
---

# Cost Summary

## Purpose

Generate a human-readable cost summary from the yakos dispatch-log
(`~/.yakos-state/dispatch-log*.ndjson`) over a configurable window.
Designed for daily/weekly check-ins — operator runs the skill,
gets a markdown summary, optionally posts to a Slack/Discord webhook.

## Scope

- Reads the dispatch-log via `yakos cost --json`.
- Formats a markdown summary with per-runtime, per-agent, and
  per-day breakdowns.
- If `YAKOS_COST_WEBHOOK` is set in the operator's environment AND
  `--post` is passed, POSTs the summary to that webhook (Slack /
  Discord / Mattermost / generic JSON receiver). The webhook URL
  format is up to the operator; the skill emits a JSON body of
  `{"text": "<markdown>"}` which most chat receivers accept.
- yakOS itself does NOT bundle a webhook secret; the operator
  supplies the URL via env var.

## When to use

- Weekly / monthly cost review across runtimes.
- After a heavy work week, to see which agents consumed the most.
- For project hand-off documentation: include the cost summary so
  the next operator knows the burn rate.

## When NOT to use

- For real-time per-call inspection — `tail -1
  ~/.yakos-state/dispatch-log.ndjson | jq` is faster.
- For accurate billing — yakOS estimates are best-effort. Real
  per-runtime token counts are emitted by the v0.6+ telemetry path
  for claude (and v0.6.x+ for codex/gemini); use the runtime's own
  billing dashboard for authoritative numbers.

## Automated pass

1. Read the runtime probe to confirm yakos is configured:
   ```sh
   yakos doctor --probe-runtime | head -20
   ```

2. Generate the JSON cost data for the requested window:
   ```sh
   yakos cost --since "${SINCE:-$(date -u -v-7d +%Y-%m-%d 2>/dev/null || date -u -d '7 days ago' +%Y-%m-%d)}" --json --by runtime > /tmp/cost-runtime.json
   yakos cost --since "${SINCE}" --json --by agent   > /tmp/cost-agent.json
   yakos cost --since "${SINCE}" --json --by day     > /tmp/cost-day.json
   ```

3. Compose the markdown summary. Before the tables, emit a one-line
   pending model-routing notice:
   ```sh
   MR_CANDS="${HOME}/.yakos-state/model-routing-candidates.ndjson"
   if [ -s "$MR_CANDS" ]; then
       n="$(jq -rs '[.[].agent] | unique | length' "$MR_CANDS" 2>/dev/null || echo 0)"
       echo "pending model-routing candidates: $n (run \`yakos model-routing list\` to see)"
   fi
   ```
   Then include three tables (runtime, agent, day) and the totals.
   Mark the est-tokens columns "estimate" so readers know to consult
   the runtime's own billing for billable numbers.

4. Print to stdout. If `--post` is set AND `YAKOS_COST_WEBHOOK` is
   non-empty, also POST:
   ```sh
   if [ -n "${YAKOS_COST_WEBHOOK:-}" ] && [ "${POST:-0}" = "1" ]; then
       curl -fsS -X POST -H 'Content-Type: application/json' \
            --data "$(jq -Rs '{text: .}' < /tmp/summary.md)" \
            "$YAKOS_COST_WEBHOOK"
   fi
   ```

5. Clean up tempfiles.

## Manual pass

If automation is overkill, the operator runs:

```sh
yakos cost --since 2026-05-01 --by agent
yakos cost --since 2026-05-01 --by runtime
yakos cost --since 2026-05-01 --by day --json | jq
```

…and pastes the relevant table into a chat or weekly note.

## Known gotchas

- **chars/4 estimate is rough.** Real token counts are populated
  for claude dispatches in the `usage` field of each
  `dispatch_finished` event. Codex + gemini real counts arrive in
  v0.6.x+. The skill's totals mix both — note this in the output.
- **Rotated logs.** `yakos cost` reads all
  `~/.yakos-state/dispatch-log*.ndjson` (current + rotated
  archives). After heavy use, the windowed total covers archives
  too. If the window predates the oldest archive, the tail is
  silently missing — call this out in the summary if `--since`
  predates the oldest log file.
- **Multiple operators.** dispatch-log is per-machine. Aggregating
  across machines requires copying logs to a central host first.
- **PII.** dispatch_started events record a `task_preview` (first
  200 chars of the task). If the cost summary is posted to a
  shared channel, scrub or summarize before posting; do not raw-
  paste task previews to a public webhook.

## References

- `cli/lib/cost.sh` — the underlying command.
- `~/.yakos-state/dispatch-log.ndjson` — the source data.
- `docs/runtime-matrix.md` — what real telemetry is available
  per-runtime.
