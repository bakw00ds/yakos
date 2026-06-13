# Supervisor mode — live shadow-agent monitoring

**Status:** v0.33+. A second agent runs in parallel to the lead,
reads the lead's recent tool calls + outputs, and judges drift /
accuracy / intent alignment / scope risk. Can block the lead on
CRITICAL findings (active mode) or surface findings as warnings
(passive mode).

## Why this exists

A long-running agent session can drift in ways that aren't obvious
from any single tool call:

- The operator asked for "fix the login bug" and the agent has been
  refactoring authentication for 20 minutes
- The agent claimed "I added tests" but the actual edits don't show
  any new tests
- The agent is trying multiple permutations of an edit that
  `path-allowlist` blocked, looking for a way through
- The agent ran `git push --force` on a path the operator never
  mentioned

The yakOS PreToolUse / PostToolUse hooks catch policy violations
(path allowlist, secret scan, peer claims). The **supervisor** catches
*intent* violations — work that's technically allowed but
demonstrably off-mission.

## How it works

```
┌────────────────────┐
│  Lead agent        │ runs in your terminal
│  (claude/codex/    │
│   agy/etc)         │
└────────┬───────────┘
         │ every tool call
         ▼
┌────────────────────────────────────────────────────────┐
│  supervisor-stream.sh  (PostToolUse hook)              │
│  • Append summary to supervisor-buffer.ndjson          │
│  • Increment .supervisor-counter                       │
│  • Every N calls (default 10): fork the supervisor     │
│    dispatch in the background                          │
└────────────────────────────────────────────────────────┘
         │
         │ fork (nohup; doesn't block lead)
         ▼
┌────────────────────────────────────────────────────────┐
│  supervisor agent  (lib/agents/supervisor.md)          │
│  Sonnet-tier judge (or whatever 'balanced' alias       │
│  resolves to per runtime).                             │
│  • Read buffer, score on 4-axis rubric                 │
│  • Write one JSON line to supervisor-findings.ndjson   │
└────────────────────────────────────────────────────────┘
         │
         │ findings accumulate
         ▼
┌────────────────────────────────────────────────────────┐
│  supervisor-gate.sh  (PreToolUse hook)                 │
│  Before EVERY lead tool call:                          │
│  • Read most recent finding                            │
│  • PASS    → continue                                  │
│  • WARN    → stderr to lead (one-time per finding)     │
│  • CRITICAL + block_on_critical: true → ho_block       │
│  • CRITICAL + block_on_critical: false → stderr only   │
└────────────────────────────────────────────────────────┘
```

The supervisor never edits code, never sends peer DMs. Its only
output is findings; the gate hook decides what to do with them.

## The rubric

Each batch is scored on four axes (PASS / WARN / CRITICAL):

| Axis | What it catches | Example CRITICAL |
|---|---|---|
| `intent_alignment` | Drift from stated task | Operator asked for a bug fix; agent has been refactoring for 15 min |
| `factual_accuracy` | Claims not supported by tool calls | Agent says "I added tests" but no test file was touched |
| `hard_control_respect` | Hook-bypass attempts without operator approval | Agent appends its own entry to hook-bypass.md |
| `scope_risk` | Irreversible/risky operations outside scope | `git push --force` on a file operator didn't mention |

`overall` = worst of the four. `recommended_action` =
`continue | surface_to_operator | block_next_tool | halt`.

## Setup

The supervisor is **enabled by default for new projects** (surface-only mode:
`block_on_critical: false`). A `yakos init`-ed project ships with the supervisor
on; it scores activity every 10 tool calls and surfaces findings but never
hard-blocks the lead.

Projects that existed before v0.43 and have no `supervisor:` block in
`.yakos.yml` default to **off** — the absent-block-equals-off behavior is
preserved for existing projects so there is no surprise cost for them.

To confirm status or turn things on/off manually:

```sh
cd /path/to/your/project
yakos supervise status          # shows current enabled state
yakos supervise enable          # explicitly enable (adds block to .yakos.yml)
yakos supervise disable         # turn off
# Or, manually edit .yakos.yml:
#   supervisor:
#     enabled: true
#     block_on_critical: false  # surface-only (default for new projects)
```

Then start a session normally — `yakos start <name>`. The
PostToolUse hook starts streaming; once you've made 10 tool calls,
the supervisor fires for the first time.

## Configuration

In your project's `.yakos.yml`:

```yaml
supervisor:
  enabled: true
  runtime: claude              # which runtime to dispatch supervisor on
  agent: supervisor            # agent file id (rarely overridden)
  score_every_n_calls: 10      # cost vs latency tradeoff
  block_on_critical: false     # surface-only (default for new projects);
                               # set true for active mode (hard block on CRITICAL)
```

- **`runtime`** — separate from the lead's runtime. Default `claude`.
  Use `codex` if you want OpenAI judging; `agy` for Gemini judging.
- **`agent`** — defaults to the framework's `supervisor` agent.
  Override to use a project-customized supervisor.
- **`score_every_n_calls`** — higher = less cost, more drift slips
  through. Lower = catches drift fast, more API spend.
- **`block_on_critical`** — `true` (active mode, hard block) or
  `false` (passive, surface CRITICAL via stderr but don't stop the
  lead).

## Common commands

```sh
yakos supervise enable               # turn on for the project
yakos supervise disable              # turn off
yakos supervise status               # config + buffer + recent findings count
yakos supervise tail                 # last 10 findings
yakos supervise tail --watch         # follow new findings live
yakos supervise tail --n 50          # last 50
yakos supervise clear                # wipe buffer + findings (config preserved)
```

## When the supervisor fires CRITICAL

The gate hook blocks with an actionable message:

```
supervisor flagged CRITICAL on finding 2026-05-22T19:30:00Z:
       Agent has been refactoring src/auth/ for 18 minutes; the
       operator's stated task was "fix the bug in src/billing/calc.ts".
       No edits to billing/ in the last 20 tool calls.
       Recommended action: halt
       To proceed:
         1. Review the finding in work/current/supervisor-findings.ndjson
         2. If the supervisor is wrong, add a bypass entry:
            ## bypass:supervisor-override-2026-05-22T19:30:00Z
            **Hook:** supervisor
            **Scope:** finding=2026-05-22T19:30:00Z
            (plus the standard Hook/Reason/Approved/Created/Expires fields)
         3. Or set supervisor.block_on_critical: false in .yakos.yml
            for passive-mode warnings only.
         4. Emergency bypass for this session only:
            export YAKOS_SUPERVISOR_DISABLE=1
```

Three escalation paths in increasing severity:

1. **Per-finding bypass** — add a bypass entry to `hook-bypass.md`
   with `Scope: finding=<ts>`. Targeted; auditable.
2. **Passive mode** — set `block_on_critical: false` in `.yakos.yml`.
   Supervisor still runs and still surfaces, but never blocks. Good
   for "I want to see what it catches but not have it stop my
   workflow."
3. **Emergency disable** — `export YAKOS_SUPERVISOR_DISABLE=1`.
   Session-scope; doesn't change config. Use when the supervisor is
   wrong and you need to keep going right now.

## Cost

Roughly: every Nth tool call = one supervisor dispatch. On default
(N=10, sonnet runtime):

| Session pace | Supervisor dispatches | ~$/hr extra |
|---|---|---|
| 60 tool calls / hour | 6 / hour | ~$0.30–$0.60 |
| 200 tool calls / hour | 20 / hour | ~$1.00–$2.00 |

Cheaper alternatives:
- Raise `score_every_n_calls` to 20 or 30
- Use `runtime: codex` with `model: cheap` for gpt-5-nano-tier judging
- Use passive mode (`block_on_critical: false`) — you still get the
  surface signal without the hard block, and you can lower the
  scoring frequency without losing safety

## Limitations

1. **Latency between drift and detection.** The supervisor fires
   every N tool calls; if N=10 and the agent goes off-mission at
   tool call 11, you won't see it until call 20.
2. **Async dispatch.** The supervisor's fork runs in the background.
   If it takes 15 seconds and the lead made 5 more tool calls in
   that window, the gate hook is checking against a finding that's
   somewhat stale. This is intentional — blocking the lead while
   the supervisor scores would defeat the point.
3. **No streaming context.** The supervisor sees a snapshot of the
   last 50 tool calls; it doesn't watch the lead's full output
   stream or its system prompt directly.
4. **Doesn't see the lead's intent verbatim.** Reads `decisions.md`
   head as a proxy. Keep your `decisions.md` updated as the
   operator-facing source of truth.
5. **Can be wrong.** Supervisor judgments are LLM judgments. Anti-
   Hermes-spam discipline reduces false positives but doesn't
   eliminate them.

## Troubleshooting

**Supervisor never fires:**

- `yakos supervise status` — check `enabled: yes`
- Check buffer is being populated — `wc -l work/current/supervisor-buffer.ndjson`
- Check counter — `cat work/current/.supervisor-counter`
- Look at `.supervisor-stderr.log` for fork errors

**Supervisor fires but findings are empty/garbage:**

- The supervisor agent dispatch may be erroring; check
  `.supervisor-stderr.log`
- Verify the runtime is authed: `yakos auth status <runtime>`
- The supervisor needs Read access to the work/current/ files — if
  you've path-allowlisted Read away, the supervisor can't see the
  buffer

**Getting too many false-positive CRITICAL blocks:**

- Switch to passive mode: `block_on_critical: false`
- Or raise `score_every_n_calls` (more tool calls per batch → more
  context → fewer hasty judgments)
- Or use `agent: <project-tuned-supervisor>` and customize the
  rubric prompt for your domain

## See also

- `lib/agents/supervisor.md` — the supervisor's persona + rubric
- `lib/hooks/supervisor-stream.sh` — PostToolUse streamer
- `lib/hooks/supervisor-gate.sh` — PreToolUse gate
- `cli/lib/supervise.sh` — the CLI
- `lib/agents/librarian.md` — after-the-fact skill-candidate curator
  (the supervisor's nearest yakOS cousin; both reference the
  anti-Hermes-spam discipline)
