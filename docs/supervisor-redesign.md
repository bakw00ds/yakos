# Supervisor Redesign — Option 2 (v0.34)

## Problem

The v0.33 supervisor consumed 94% of operator tokens (4-week audit).
Root causes:

1. Matcher `*` fired the hook on every tool call including reads, greps,
   and WebFetch — operations that cannot mutate state.
2. No local triage: every Nth call dispatched an LLM-scored supervisor
   agent regardless of how boring the batch was (pure reads → "exploration
   only" finding → cost paid for noise).
3. Model tier was `balanced` (sonnet-class). Routine drift-detection does
   not require the top tier.

## Architecture after v0.34

```
PostToolUse (matcher: Edit|Write|MultiEdit|Bash)
    │
    ▼
supervisor-stream.sh
    │
    ├─ supervisor.enabled: false? → exit 0
    ├─ YAKOS_SUPERVISOR_DISABLE=1? → exit 0
    │
    ├─ Append event to supervisor-buffer.ndjson (always, for audit trail)
    │
    ├─ pre_filter.enabled: false? ──────────────────────────────────────┐
    │                                                                   │
    ├─ [pre-filter enabled] shell-only triage                           │
    │   a. Sensitive path: file ∈ path-allowlist.json deny-globs?      │
    │   b. Large diff: preview line count > min_diff_lines (def. 20)?  │
    │   c. Out-of-scope: file not in decisions.md / plan.md?           │
    │   d. Risk regex: drop table / force push / rm -rf / etc.?        │
    │                                                                   │
    │   NONE trip → buffer-only return (0 cost, 0 LLM calls)           │
    │   ANY trip  → log trigger + fall through to counter              │
    │                                                                   │
    └───────────────────────────────────────────────────────────────────┘
         │
         ▼ (escalation OR pre-filter disabled)
    Increment escalation counter (.supervisor-counter)
         │
         ├─ counter % score_every_n_calls ≠ 0 → exit 0
         │
         ▼ (threshold reached)
    fork: nohup yakos dispatch supervisor … --model haiku & disown
         │
         └─ PostToolUse hook returns immediately (async; < 5 ms)
              supervisor agent runs in background (5-30 s)
              writes finding to supervisor-findings.ndjson
                   │
                   ▼
              supervisor-gate.sh (PreToolUse — unchanged)
                   blocks lead on CRITICAL
                   ▼
              supervisor-ack-gate.sh (PreToolUse — unchanged, PR #19)
                   blocks TeamCreate|Agent on unacknowledged escalations
```

## The four changes

### 1. Matcher: `*` → `Edit|Write|MultiEdit|Bash`

`lib/settings/settings.template.json` and `.claude/settings.json`:

**Before:**
```json
{"matcher": "*", "hooks": [{"command": "…/supervisor-stream.sh"}, {"command": "…/output-injection-scan.sh"}]}
```

**After:**
```json
{"matcher": "Edit|Write|MultiEdit|Bash", "hooks": [{"command": "…/supervisor-stream.sh"}]},
{"matcher": "*",                          "hooks": [{"command": "…/output-injection-scan.sh"}]}
```

`output-injection-scan.sh` stays on `*` because reads (WebFetch, mcp__*) are
the prompt-injection attack surface. `supervisor-stream.sh` moves to mutation-only.

Per-project override: set `supervisor.matcher` in `.yakos.yml` (documentary
only; the actual Claude Code matcher lives in `.claude/settings.json`).

### 2. Local pre-filter in supervisor-stream.sh

Runs before any LLM dispatch. Four deterministic checks, all in shell:

| Check | Source | Default |
|---|---|---|
| Sensitive path | `.claude/path-allowlist.json` deny-globs | Project-configured |
| Large diff | Line count of preview | > 20 lines |
| Out-of-scope edit | File not in decisions.md / plan.md | When those files exist |
| Risk regex | Built-in + `supervisor.pre_filter.risk_regex` list | Built-in always active |

Built-in risk patterns: `drop[[:space:]]+table`, `force.*push`,
`rm[[:space:]]+-rf`, `chmod[[:space:]]+777`, secret-shaped assignments.

Clean mutations: buffer-only return, 0 LLM cost, hook exits in < 5 ms.
Escalations: log trigger reason, increment counter, proceed to dispatch.

Disable: `supervisor.pre_filter.enabled: false` → reverts to v0.33 behavior
(every Nth mutation dispatches regardless).

### 3. Model tier: `balanced` → `haiku`

`lib/agents/supervisor.md` frontmatter `model:` is now `haiku`.
Override via `.yakos.yml supervisor.model: sonnet` or `opus` for projects
that need higher fidelity. The `--model` flag is passed through the
`yakos dispatch` invocation in `supervisor-stream.sh`.

### 4. Async dispatch (verified + documented)

`supervisor-stream.sh` has always used `nohup … & disown`. Verified:
the pattern matches `retro-dispatch.sh` (PR #20). The PostToolUse hook
exits 0 immediately after `disown`; the LLM call completes in background.
Any stdout/stderr from the dispatch is appended to:

- `work/current/.supervisor-stdout.log`
- `work/current/.supervisor-stderr.log`

## Configuration reference

```yaml
supervisor:
  enabled: true
  runtime: claude                    # runtime to dispatch supervisor on
  agent: supervisor                  # agent file id
  score_every_n_calls: 10            # escalations (not all mutations) before scoring
  block_on_critical: true            # false = surface-only mode
  model: haiku                       # haiku | sonnet | opus
  pre_filter:
    enabled: true
    min_diff_lines: 20
    risk_regex:
      - "drop\\s+table"              # extends the built-in list
```

## What is NOT changed

- `lib/hooks/supervisor-gate.sh` — deterministic PreToolUse block gate.
- `lib/hooks/supervisor-ack-gate.sh` — PR #19 escalation ack gate.
- `lib/hooks/cycle-counter.sh` — K-17 owns that fix.
- `lib/hooks/retro-dispatch.sh` — PR #20.

## Expected cost reduction

Typical session: 200 tool calls. Post-redesign:
- ~120 are reads/WebFetch/SendMessage → matcher rejects, 0 hooks fired.
- ~80 are mutations. Of those, ~60 pass pre-filter (trivial edits). 0 LLM.
- ~20 escalate. With score_every=10, ~2 LLM dispatches at haiku tier.

vs. v0.33: 200 / 10 = 20 LLM dispatches at sonnet tier.
Approximate reduction: 2 haiku calls vs. 20 sonnet calls → ~90% cost cut
(haiku ≈ 1/15 the cost of sonnet for equivalent token counts).
