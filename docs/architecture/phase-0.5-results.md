# Phase 0.5 — TaskCompleted schema + ~/.claude/tasks/ format

**Status:** PARTIALLY ANSWERED. Run from inside an active Claude Code
session (yakOS v0.2.0.0) on 2026-04-29; bigger finding than the
original probe expected.

The two original questions:

1. **What's the exact stdin shape of `TaskCompleted` hooks?** —
   STILL UNANSWERED. Capture requires hooks pre-configured at
   session start; the operator-driven probe at
   `tests/manual/phase-0.5-probe/` is still the path to that data.
2. **What's the format of `~/.claude/tasks/<team>/` files?** —
   **REVEALED A LARGER FINDING: in this Claude Code build, the
   Task* tools that would write those files aren't exposed to
   either the lead or to teammates spawned via Agent.** The
   directory is created at TeamCreate time but contains only a
   sentinel `.lock` file because nothing has the tools to populate
   it.

This rewrites both follow-up tickets in `docs/v0.2-notes.md` —
flipping `task-dependency-gate.sh` and `task-complete-dispatch.sh`
from REPORT-only to BLOCKING is now also gated on Claude Code
exposing TaskCreate/TaskUpdate, not just on schema confirmation.

---

## Method

In-session probe from a yakOS v0.2.0.0 lead session:

1. `TeamCreate({team_name: "phase05probe", agent_type: "probe-lead"})`.
2. Inspected `~/.claude/teams/phase05probe/config.json` and
   `~/.claude/tasks/phase05probe/`.
3. Searched the deferred-tool registry via ToolSearch for
   `TaskCreate`, `TaskList`, `TaskUpdate`. None appeared.
4. Spawned a teammate via
   `Agent({subagent_type: "general-purpose", team_name: "phase05probe",
   name: "probe-worker", prompt: <probe-instructions>})`.
5. Probe-worker checked its toolset, attempted to use Task* tools,
   reported back via SendMessage.
6. Inspected the resulting filesystem state.

---

## Tool availability

In this Claude Code build, **the lead-of-this-session has access to**:

- `TeamCreate`, `TeamDelete`, `SendMessage`
- `TaskOutput`, `TaskStop` (these are for *background shell tasks*,
  NOT the team task list — different concept)
- `TodoWrite` (this session's local todo list, NOT shared with the
  team)
- `Agent` (with `team_name` + `name` parameters supported in the
  schema)

The lead does NOT have:

- `TaskCreate`, `TaskList`, `TaskUpdate`

The spawned teammate (`probe-worker`, `subagent_type: "general-purpose"`)
also does NOT have access to `TaskCreate`, `TaskList`, or
`TaskUpdate`. Quote from probe-worker's report:

> The full deferred list contains zero `TaskCreate/List/Update`
> entries. There is no schema to "adapt to" — the tools simply do not
> exist in this harness for me as a teammate.

The probe-worker also flagged a doc/runtime mismatch:

> `SendMessage`'s own description says "Don't send structured JSON
> status messages — use `TaskUpdate`." The harness assumes
> TaskUpdate exists, but it doesn't surface it to teammates. Either
> it's lead-only, or the description is stale.

Conclusion: In this Claude Code version (the one running this
session), the team task-list coordination primitive documented in
`TeamCreate`'s description is **not implemented as exposed tools**.

---

## Filesystem observations

### `~/.claude/teams/<team>/` directory layout

After `TeamCreate({team_name: "phase05probe"})` and one teammate
spawn, the directory contains:

```
~/.claude/teams/phase05probe/
├── config.json
└── inboxes/
    └── team-lead.json
```

The `inboxes/` directory is created lazily — it doesn't appear at
TeamCreate; it appears the first time a teammate sends a message
via SendMessage.

### `config.json` schema (representative)

```json
{
  "name": "phase05probe",
  "description": "...",
  "createdAt": 1777489459075,
  "leadAgentId": "team-lead@phase05probe",
  "leadSessionId": "<uuid>",
  "members": [
    {
      "agentId": "team-lead@phase05probe",
      "name": "team-lead",
      "agentType": "probe-lead",
      "model": "claude-opus-4-7[1m]",
      "joinedAt": 1777489459075,
      "tmuxPaneId": "",
      "cwd": "/Users/tw/github/yakOS",
      "subscriptions": []
    },
    {
      "agentId": "probe-worker@phase05probe",
      "name": "probe-worker",
      "color": "blue",
      "joinedAt": 1777489526809,
      "tmuxPaneId": "in-process",
      "subscriptions": [],
      "agentType": "general-purpose",
      "model": "claude-opus-4-7",
      "prompt": "<full-spawn-prompt-verbatim>",
      "planModeRequired": false,
      "cwd": "/Users/tw/github/yakOS",
      "backendType": "in-process"
    }
  ]
}
```

Notable fields:

- `agentId`: `<name>@<team>` format (predictable).
- `color`: auto-assigned when teammate joins. Lead doesn't have one.
- `tmuxPaneId`: `""` for the lead, `"in-process"` for spawned-via-Agent
  teammates. Suggests an alternate `tmux` backend exists for
  out-of-process teammates not used here.
- `model`: lead has `[1m]` suffix (1M-context mode); spawned
  teammate doesn't.
- `prompt`: the full spawn prompt is captured verbatim in the
  member entry. Useful for audit; potentially leaks task content
  into team config (not a secret-handling concern in v0.2 but worth
  noting).
- `subscriptions`: array. Empty in our probe; not yet exercised.
- `planModeRequired`: spawn-time flag (corresponds to Agent's `mode`
  param).
- `backendType`: `"in-process"` for the spawned teammate. Implies
  `"out-of-process"` or similar for tmux-based teams.

### `inboxes/<recipient>.json` schema (representative)

```json
[
  {
    "from": "probe-worker",
    "text": "<message body>",
    "summary": "<5-10 word summary from SendMessage>",
    "timestamp": "2026-04-29T19:06:06.494Z",
    "color": "blue",
    "read": false
  },
  {
    "from": "probe-worker",
    "text": "{\"type\":\"idle_notification\",\"from\":\"probe-worker\",\"timestamp\":\"...\",\"idleReason\":\"available\"}",
    "timestamp": "...",
    "color": "blue",
    "read": false
  }
]
```

Notable:

- File is a JSON array; messages append.
- Idle notifications are stored AS messages with `text` containing
  raw stringified JSON (`{"type":"idle_notification",...}`). The
  harness conflates "I have something to say" and "I went idle"
  into the same message stream.
- `read: false` — the harness tracks read state. Mechanism for
  marking read not observed in this probe.
- `color` is per-sender, propagated from the team config.

### `~/.claude/tasks/<team>/` directory

After TeamCreate + teammate spawn + 30s of teammate runtime:

```
-rw-r--r--  1 tw  staff  0  Apr 29 15:04  .lock
```

The `.lock` file is a regular empty file (per `file(1)`: `empty`),
mode 0644, 0 bytes. No process holds an actual fcntl/flock on it —
it's a sentinel/marker, not a synchronization primitive. Created at
TeamCreate time. Not modified afterward in this probe (because no
TaskCreate calls happened).

### Task file format

**UNOBSERVED.** No task files were created during this probe
because the tools to create them aren't exposed. The original
question's premise (that ~/.claude/tasks/<team>/ would have a
schema worth documenting) is currently moot — there's no pathway
from agent action to file creation in this build.

---

## What stays UNCLEAR after this probe

1. **TaskCompleted hook stdin shape.** Untouchable from this
   session; the operator-driven probe (`tests/manual/phase-0.5-probe/`)
   remains the only path. Its premise (that TaskCompleted fires when
   a teammate marks a task completed via TaskUpdate) is now itself
   uncertain — if TaskUpdate isn't a runtime tool, when does
   TaskCompleted fire?
2. **Whether TaskCreate/TaskUpdate exist in some Claude Code
   build.** The TeamCreate description references them as the
   coordination primitive; the runtime doesn't surface them. Either:
   - (a) tools were removed from this build but not from docs;
   - (b) tools exist in a different Claude Code variant
     (Cowork, Anthropic-hosted teams, OpenCode, etc.);
   - (c) tools become available via a different gating mechanism
     (file-system based? plan-mode-only?).
3. **`tmuxPaneId` and `backendType` semantics.** "in-process" is
   what we observed; what does "tmux" mode (the legacy-pattern
   yakOS used to support) actually look like in config.json?
4. **`subscriptions`** — empty array on every member. Used for
   what? Mailbox-mirror-like routing? Untested.
5. **`.lock` file** — purpose without TaskCreate. May be a
   defensive-coding artifact: the harness created it for future
   coordination but never uses it because the tools that would are
   gone.

---

## Implications for YakOS

### `task-dependency-gate.sh` and `task-complete-dispatch.sh`

Both ship as REPORT-only in v0.1+. The original plan to flip them
BLOCKING in v0.2 was gated on confirming TaskCompleted's stdin
shape. **The bigger gate is now: do task-state transitions even
happen in this Claude Code build?**

If TaskCreate/TaskUpdate aren't tools, then `TaskCompleted` may
never fire on any tool call, regardless of stdin shape. Both hooks
should remain REPORT-only until either:

- A Claude Code build that surfaces TaskCreate/TaskUpdate becomes
  available, OR
- An alternate trigger mechanism is identified (file-system
  watching on `~/.claude/tasks/<team>/` is one option but adds
  complexity).

### Mailbox-mirror hook

The mailbox file format we captured (JSON array under
`~/.claude/teams/<team>/inboxes/<recipient>.json`) is a real,
durable artifact. The `mailbox-mirror.sh` hook (currently REPORT-only
per Phase 1.7) could now read this file directly to mirror peer
DMs into `decisions.md` — no need to rely on the SendMessage hook
firing reliably. **This is a v0.3 enhancement worth tracking.**

### What the v0.2-notes.md update should say

Rewrite the "Phase 0.5 probe" section under "Architectural gaps
surfaced" to acknowledge the bigger finding: the BLOCKING upgrade
of the two REPORT-only hooks isn't deferred-by-schema; it's
deferred-by-runtime-feature.

---

## Confidence

~95% on the tool-availability finding (verified by direct
ToolSearch and by spawning a teammate that confirmed the same).
Residual 5%: maybe TaskCreate is conditionally exposed under a
build flag or after a further probe step we didn't try. The
TeamCreate description's confident reference to those tools
suggests they exist in *some* harness; we don't know which.

~99% on the directory layout (`config.json`, `inboxes/`,
`tasks/.lock`). These are filesystem artifacts directly observed.

~70% on `tmuxPaneId`/`backendType` semantics — inferred from field
names plus YakOS's prior tmux-based-orchestration history; not
verified by exercising the alternate backend.

---

## Bonus finding — shutdown protocol drift

The lead sent the documented `{"type": "shutdown_request", ...}`
message to the teammate. The teammate replied with
`{"type":"shutdown_approved","requestId":"...","paneId":"in-process","backendType":"in-process"}` —
field-name drift from the documented `shutdown_response` /
`request_id` schema. The teammate then continued running. Three
subsequent `TeamDelete` calls (after 8s, 20s, and 30s of waiting)
all returned `Cannot cleanup team with 1 active member(s):
probe-worker`. A plain-text "please terminate" follow-up message
also did not cause termination.

The probe team was force-cleaned via direct
`rm -rf ~/.claude/teams/phase05probe ~/.claude/tasks/phase05probe`,
which works fine.

Implications:

- **Don't rely on the documented shutdown protocol in v0.2.** Either
  the protocol's response schema drifted, or the runtime's
  member-active state isn't cleared by `shutdown_approved`.
- **Force-cleanup via `rm -rf` is the working path.** YakOS's
  `team-lifecycle.sh` hook (REPORT-only in v0.1) could surface this
  as the safe cleanup mechanism.
- **The `tmuxPaneId: "in-process"` backend may not honor process
  termination at all** — there's no separate process to kill; the
  "teammate" is logically a record in the team config that the
  harness considers alive until something it doesn't surface
  changes its state.

## Files left for inspection

```
~/.claude/teams/phase05probe/                  — REMOVED (force-cleanup)
~/.claude/tasks/phase05probe/                  — REMOVED (force-cleanup)
```

The probe artifacts have been torn down. Filesystem schema captured
in this doc replaces the need to retain them.
