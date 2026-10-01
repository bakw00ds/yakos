# Auto-compact

## Default: the harness compacts at ~150K tokens (K-118)

`yakos refresh` writes Claude Code's own `autoCompactWindow` setting into the
project's `.claude/settings.json` when the key is absent:

```json
{ "autoCompactWindow": 150000 }
```

Claude Code then summarizes the conversation by itself as context approaches
that window. This is a harness feature (setting, `--autocompact`, the
`/autocompact` command and the `CLAUDE_CODE_AUTO_COMPACT_WINDOW` env var), so
no hook and no `/compact` injection is involved. Measured on Claude Code
2.1.286 with `--debug`: a project with the key logs `autocompact: ...
effectiveWindow=130000` (the window minus the harness's summary buffer), and
`DISABLE_AUTO_COMPACT=1` silences it. The harness sets the exact
trigger point; expect it at or below the configured window.

Why: the lead averaged 447K cache-read tokens per turn over 832 turns, and the
cache-read bill grows with context. Compacting near 150K cuts it by roughly
two thirds.

It applies to every Claude Code session in the project, including dispatched
`claude -p` runs, not only the interactive lead. The allowed range is 100K to
1M tokens.

| Want | Do |
|---|---|
| Default | nothing; refresh adds `autoCompactWindow: 150000` if absent |
| Another window | `auto_compact_window: 200k` (or `200000`) in `<project>/.yakos.yml`, then `yakos refresh` |
| Off | `auto_compact_window: off` in `.yakos.yml`, then `yakos refresh`; refresh removes the key and the harness default applies |
| One session only | `claude --autocompact auto` or `/autocompact`; `CLAUDE_CODE_AUTO_COMPACT_WINDOW` beats every setting |
| Never compact | `DISABLE_AUTO_COMPACT=1` in the environment (harness switch) |

A value you set by hand in `settings.json` is kept; refresh only overwrites it
when `.yakos.yml` names one. A bad `.yakos.yml` value aborts the whole refresh with a non-zero exit and the
allowed range, before anything is written. `auto` and an empty value mean the
yakos default (150000), not Claude Code's own `auto`. The file mode of
`settings.json` is preserved. `yakos refresh`
prints `auto-compact-window: <value> (<source>)` per project.

## The older marker/Stop-hook path (M3.1) is opt-in and unverified

The rest of this page describes an earlier mechanism: `context-threshold`
writes `.compact-pending` and a Stop hook answers `{"decision":"block",
"reason":"/compact"}`. Claude Code feeds a Stop hook's `reason` back to the
model as text; a hook cannot run a built-in slash command, and nothing here
shows the harness executing `/compact` from it. It also sizes context from
transcript bytes, and a transcript keeps growing after a compaction, so it can
re-trigger. It stays off unless `context_thresholds.auto` is set, and it is not
what the default relies on. Prefer the native setting above.

---

# Auto-compact via marker and Stop hook (M3.1, opt-in)

yakOS M3.1 adds automatic context compaction: when the context window crosses
a configurable threshold, the yakos hook system automatically injects `/compact`
as the next Claude Code turn — no operator action required.

## Threshold ladder

| Threshold | Default | Purpose |
|---|---|---|
| `notice` | 75% | Advisory NOTE to stderr — "consider /compact" |
| `auto-compact` | OFF (opt-in) | Write `.compact-pending` marker; Stop hook injects `/compact` |
| `warning` | 90% | Hard WARN + auto-checkpoint of scratchpad files |

The three thresholds are independent. Auto-compact sits between notice and
warning: it gives the compaction a chance to run before the warning checkpoint
fires and before the context window fills.

## How it works

Two hooks cooperate via a filesystem marker:

### 1. `context-threshold` hook (UserPromptSubmit)

Runs before every user prompt. When `context_thresholds.auto` is set and the
estimated context fill crosses that value:

- Writes `work/current/.compact-pending` atomically (temp-rename, Q8).
- Continues to emit the advisory stderr message as today.
- Does NOT block the current prompt.

### 2. `auto-compact-trigger` hook (Stop)

Runs after Claude Code finishes each turn. Checks for the marker:

- **Marker absent**: exit 0 (no-op). This is the common case.
- **Marker present**:
  1. Removes the marker (idempotent — next Stop event is a no-op).
  2. Emits JSON to stdout:
     ```json
     {
       "decision": "block",
       "reason": "/compact",
       "systemMessage": "yakos auto-compact: ..."
     }
     ```
  3. Claude Code feeds `reason` back as the next user-turn prompt, executing
     `/compact` automatically.

## JSON contract

The Stop hook uses the same JSON contract as the `ralph-loop` plugin
(verified against `validate-hook-schema.sh`):

```json
{
  "decision": "block",
  "reason": "<prompt to inject as next turn>",
  "systemMessage": "<optional system message>"
}
```

**Why Stop, not UserPromptSubmit?** The `UserPromptSubmit` hook fires before
the user's text is processed — we can inject `additionalContext` there but
cannot replace the prompt. The `Stop` hook fires after a turn completes and
supports `decision: "block"` with a `reason` that becomes the next turn's
prompt. That is the only hook event where injecting `/compact` as a full turn
is possible.

## Configuration

### Enable auto-compact

```sh
yakos compact threshold --auto 85
```

Sets `context_thresholds.auto = 85` in `~/.yakos-state/settings.json`.
The Stop hook is registered via `settings.template.json` for all projects.

### Show all thresholds

```sh
yakos compact threshold show
# notice = 75%, warning = 90%, auto-compact = 85%
```

### Disable auto-compact

```sh
yakos compact disable-auto
```

Writes `compact_auto_disabled: true` sentinel to settings.json. The
context-threshold hook checks this sentinel before writing the marker.
Both hooks respect it.

Re-enable by running `yakos compact threshold --auto N` again (which also
clears the sentinel).

### Emergency bypass (without changing settings)

Delete the marker manually:

```sh
rm -f "$(yakos work dir)/.compact-pending"
```

## Backward compatibility

- **Default OFF**: `context_thresholds.auto` is not set by default.
  The context-threshold hook never writes the marker. The Stop hook is
  registered but is always a no-op (marker absent → exit 0).
- **Older Claude Code**: if the running Claude Code version does not honour
  the Stop hook's `decision: "block"` output, the session exits normally.
  The marker is left in place and retried on the next turn. No data is lost.
  The system degrades to advisory mode silently.

## Files

| File | Purpose |
|---|---|
| `lib/hooks/legacy/auto-compact-trigger.sh` | Bash (legacy/Tier-2) Stop hook |
| `cli-go/internal/hooks/autocompacttrigger/autocompacttrigger.go` | Go (Tier-0) Stop hook |
| `cli-go/internal/hooks/contextthreshold/contextthreshold.go` | Updated to write marker |
| `lib/settings/settings.template.json` | Registers Stop hook |
| `~/.yakos-state/settings.json` | Runtime config (`context_thresholds.auto`, `compact_auto_disabled`) |
| `work/current/.compact-pending` | Transient marker (deleted after each trigger) |

## Audit log

Each auto-compact trigger appends to `work/current/logs/auto-compact-trigger.ndjson`:

```json
{"ts":"2026-06-03T14:22:00Z","hook":"auto-compact-trigger","severity":"INFO","action":"compact_triggered","session_id":"sess-abc"}
```

## Sequence diagram

```
User prompt N
  └─ UserPromptSubmit hooks fire
       └─ context-threshold.sh / contextthreshold.go
            ├─ probe context fill: 87%
            ├─ 87% >= auto threshold (85%) → write .compact-pending
            └─ emit advisory to stderr

Claude processes prompt N → turn complete

  └─ Stop hooks fire
       └─ auto-compact-trigger.sh / autocompacttrigger.go
            ├─ .compact-pending exists → remove it
            └─ emit {"decision":"block","reason":"/compact",...}

Claude Code feeds "/compact" back as next turn
  └─ /compact executes → context window compacted

Next user prompt N+1 continues in compact session
```
