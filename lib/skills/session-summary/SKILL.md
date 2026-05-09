---
name: session-summary
description: End-of-session markdown report — agents dispatched, total cost, key decisions, time-to-completion
allowed-tools: Read Bash
argument-hint: "[<project>]"
mode: [report]
---

# Session Summary

## Purpose

At the end of a yakOS session, generate a markdown summary suitable
for the operator to paste into a team channel or attach to a PR.
Pulls from `~/.yakos-state/dispatch-log.ndjson`,
`work/current/decisions.md`, and the session's launch-log entry.

## Scope

- **In:** dispatched agents + their cost; key decisions from
  decisions.md; session duration; launch + archive metadata.
- **Out:** raw transcripts (use `yakos session export` for full
  archive); per-runtime API billing details (consult the runtime's
  own dashboard).

## When to use

- Closing a long session and the operator needs a paste-able recap.
- Before `yakos archive <project> <tag>` so the archive includes
  the summary as `work/current/SESSION-SUMMARY.md`.
- When opening a PR that landed via dispatched specialist work,
  to document what was delegated.

## When NOT to use

- For incident review — use `yakos session export` for a full
  bundle that includes the summary plus all artifacts.
- For multi-session retrospectives — those want
  `yakos cost --by day --since <date>` instead.

## Automated pass

1. Resolve the project (defaults to inferring from cwd):
   ```sh
   project="${1:-$(yakos status --print-active 2>/dev/null || echo)}"
   [ -n "$project" ] || { echo "no active project"; exit 1; }
   ```

2. Pull this session's launch event (the most recent for this project):
   ```sh
   launch_evt=$(jq -c --arg p "$project" '
       select(.type == "session_launched" and .project == $p)' \
       "$HOME/.yakos-state/launch-log.ndjson" | tail -1)
   start_ts=$(echo "$launch_evt" | jq -r '.ts // empty')
   ```

3. Pull every dispatch event since `start_ts`:
   ```sh
   dispatches=$(jq -c --arg s "$start_ts" --arg p "$(cat ~/agent-control/$project/.project-path)" '
       select(.type == "dispatch_finished" and .ts >= $s and .project == $p)' \
       "$HOME/.yakos-state/dispatch-log.ndjson")
   ```

4. Compose the summary:
   ```markdown
   # Session summary — <project> (<start-ts> → <now>)

   **Runtime**: <claude / mixed>
   **Duration**: <h:mm>
   **Dispatches**: <n agents in m calls>
   **Total cost**: <$x.xx (real)> + <chars/4 estimate for codex/gemini>

   ## Agents dispatched

   | Agent | Runtime | Calls | Tokens (in/out) | Cost |
   |---|---|---|---|---|
   | backend | claude | 3 | 1,234 / 5,678 | $0.12 |
   | code-reviewer | codex | 1 | 800 / 1,500 | (est) |

   ## Key decisions
   <bulleted list lifted from decisions.md headings>

   ## Notable artifacts
   <links to reports/ entries written this session>
   ```

5. Print to stdout. Optionally write to
   `<control>/work/current/SESSION-SUMMARY.md` if `--save` is passed.

## Manual pass

```sh
yakos cost --since "$start_ts" --by agent
yakos cost --since "$start_ts" --by runtime
cat ~/agent-control/<project>/work/current/decisions.md
```

…and compose by hand.

## Known gotchas

- **Cross-machine sessions.** dispatch-log is per-machine; if the
  session spanned multiple operators, totals are partial. Note this
  explicitly in the summary.
- **PII in task previews.** `dispatch_started` events include the
  first 200 chars of the task. The summary skill doesn't surface
  these; if someone hand-rolls a summary from raw events, they
  should scrub.
- **decisions.md rot.** If the lead didn't keep decisions.md
  current, the summary is incomplete. The skill flags this with
  "decisions.md last modified at <ts>" so the operator knows.

## References

- `cli/lib/cost.sh` — the cost aggregation
- `cli/lib/session.sh` — `yakos session export` for full bundles
- `~/.yakos-state/dispatch-log.ndjson` — source data
