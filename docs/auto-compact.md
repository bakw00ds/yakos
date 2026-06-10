# Auto-compact (M3.1)

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
