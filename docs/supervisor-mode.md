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
  max_launches_per_session: 30 # routine launches per session (0 = unlimited)
  min_launch_interval_s: 120   # minimum seconds between routine launches (0 = off)
  run_deadline_s: 240          # wall-clock kill for one run (default scales with model)
  session_limit_backoff_min: 30 # pause after an account session-limit failure
  block_on_critical: false     # surface-only (default for new projects);
                               # set true for active mode (hard block on CRITICAL)
```

- **`runtime`** — separate from the lead's runtime. Default `claude`.
  Use `codex` if you want OpenAI judging; `agy` for Gemini judging.
- **`agent`** — defaults to the framework's `supervisor` agent.
  Override to use a project-customized supervisor.
- **`score_every_n_calls`** — higher = less cost, more drift slips
  through. Lower = catches drift fast, more API spend.
- **`max_launches_per_session`**, **`min_launch_interval_s`**,
  **`run_deadline_s`**, **`session_limit_backoff_min`** — the launch gate
  (K-117), per session:
  - A trigger that arrives while a run is in flight launches nothing. Its
    redacted preview is appended to `work/current/.supervisor-pending.<session>`
    (mode 0600) and one follow-up run starts when the first ends; its task
    says how many events were coalesced and points at that file.
  - **High-risk triggers** (a sensitive-path or risk-regex match) bypass the
    cap and the interval, up to a ceiling of 3x the trusted cap, and only
    routine launches count toward the cap, so benign escalations can never
    exhaust supervision. Hitting the cap logs a WARN and prints one stderr
    line; hitting the ceiling also appends a synthetic CRITICAL finding, so
    `block_on_critical` operators are stopped and told.
  - A routine trigger inside the interval starts a deferred run that waits
    out the rest of the interval instead of being dropped.
  - A run past the deadline is killed with its child processes. The default
    is 240 s for haiku, 480 s for sonnet and 600 s for opus/fable: a 240 s
    limit would kill about 9% of historical sonnet-path runs. A deadline
    below 30 s (or 0) is invalid and falls back to the default with a WARN.
  - After an account session-limit failure launches pause for
    `session_limit_backoff_min` minutes.
  - **A project `.yakos.yml` can only make supervision STRICTER**: raise the
    cap (or 0), lower the interval, lengthen the deadline, lower the
    backoff. The reverse is ignored with a WARN, because it would cut
    supervision (a 1 s deadline kills every run). The high-risk ceiling is
    computed from the trusted cap only. To move a limit the other way, use
    the user-level `~/.yakos-state/supervisor-policy.yml` (same keys at the
    top level; it must be your own regular file, not group or world
    writable).
  - Values are plain decimal numbers. A trailing `# comment` is fine;
    quoted, hex or signed values are ignored for that key alone.
  - Everything is fail-open: the hook still exits 0. State lives in
    `work/current/.supervisor-run.<session>`.
- **`model`** — a tier (`haiku|sonnet|opus|fable`) or an alias
  (`cheap`, `balanced`, `best`, `reasoning`, `frontier`); anything else
  falls back to haiku with a WARN (dispatch rejects unknown tiers).
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
- Use `runtime: codex` for the judge (it runs on codex's default model; pin a
  cheaper id from `codex debug models` in the agent's `model:` once you have
  chosen one)
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

## Lock protocol

Several hooks run at once whenever a session runs tool calls in parallel or a
team of agents shares one `work/current/`. One lock, `work/current/.supervisor-counter.lock`,
serialises the three read-modify-write files they share: the escalation
counter (`.supervisor-counter`), each session's run state
(`.supervisor-run.<session>`, with its `.supervisor-pending.<session>` previews)
and the detached wrapper's claims on that state. The bash hook, the Go hook and
both wrappers speak the same protocol, so a mixed fleet still serialises.

- **The lock is a path.** It is created exclusively (an `O_EXCL` file create in
  current hooks, `mkdir` in older ones: both are atomic on a local disk and
  exclude each other) and removed by its creator. A lock older than one minute is
  a crashed holder's: a waiter renames it aside (one winner), re-checks the age of
  what it moved and deletes it, so a lock another hook has just created is never
  taken. Keep `work/current/` on a local disk: `O_EXCL` is not reliable on NFSv2
  or on some FUSE and SMB mounts where `mkdir` is.
- **A hold is short.** Only the counter read/write, the run-state read/write
  (each a temp file and a rename, so a reader never sees a torn file) and the
  wrapper spawn happen under it. The budget CLI (`yakos budget check`, up to
  2 s, wall-clock bounded), every `jq`, every log record, stderr line and
  synthetic finding run before the lock is taken or after it is dropped. Before
  K-128 a bash gate hold was 100-300 ms of forks and ten concurrent hooks
  exhausted the wait on a slow runner.
- **Waiters back off.** A hook that finds the lock held sleeps 5 ms, then 10,
  20 ... up to 160 ms, each with +-50 % jitter, and polls with a builtin test
  rather than forking a doomed create. It checks the lock's age on its fifth
  miss and every eighth after, not on its first. The wait ends after 3 s at the
  latest (the hard ceiling; in bash `$SECONDS` ticks in whole seconds, so 2 to
  3 s).
- **A tick is never dropped.** A hook whose wait expires leaves an owner-only
  record in `work/current/` and exits 0; the next lock holder folds it in:

  | Record | Content | Folded by | Effect |
  |---|---|---|---|
  | `.supervisor-counter.add.<pid>.<n>` | `1` | the next counter holder (256 per fold) | counter +1. If that moves the counter past a `score_every_n_calls` multiple, the folder covers the crossing, because the hook that owed it has exited. |
  | `.supervisor-run.<session>.add.<pid>.<n>` | `high=<0\|1>`, then the event preview | the next gate holder of that session, and that session's wrapper (32 per fold) | `pending` +1, `high` +1 when flagged, preview appended to the pending file |

  Records are created exclusively (a planted symlink is never followed), read
  with bounded reads, folded only when complete and well formed, and removed only
  after the file they were folded into has been written (at-least-once). A hook
  that misses the counter lock while handling a high-risk trigger leaves both
  records. At most 512 records of a kind wait at once; past that a hook drops its
  tick, as before K-128, and says so in the hook log. A trigger whose gate wait
  expired is recorded as pending, not launched on the spot: the next run, at the
  next crossing, covers it.
- **Observing it.** `YAKOS_TEST_SEAMS=1 YAKOS_TEST_LOCK_STATS=1` (tests only)
  appends one line per lock take to `work/current/.supervisor-lock-stats`:
  label, wait, retries and hold in microseconds. Leftover `.add.` files mean the
  wait expired and nothing has folded them yet; `yakos supervise clear` removes
  the counter's with the counter.

## Troubleshooting

**Supervisor never fires:**

- `yakos supervise status` — check `enabled: yes`
- Check buffer is being populated — `wc -l work/current/supervisor-buffer.ndjson`
- Check counter — `cat work/current/.supervisor-counter` (a hook that could
  not take the lock within 3 s leaves a `.supervisor-counter.add.*` record
  that the next hook folds in; see "Lock protocol")
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
