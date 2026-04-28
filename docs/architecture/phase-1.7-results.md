# Phase 1.7 — SendMessage Hookability

## Question

Does the peer-message tool call trigger `PreToolUse` hooks?

## Answer

**YES — clean. `SendMessage` is fully hookable.** The PreToolUse hook fires on every SendMessage call (peer-DM, teammate→lead, and lead→teammate alike), and the hook payload includes the full message body. Mailbox-mirror.sh can ship as a YakOS default.

## Tool name

**`SendMessage`** — confirmed by direct observation, not docs alone.

A wildcard PreToolUse probe (no matcher set) captured every PreToolUse event during the test; the message-sending events appear with `tool_name: "SendMessage"`. The targeted matcher `"SendMessage"` fired identically — so the literal string match works as documented.

The sub-agents doc also names this tool:

> When a subagent completes, Claude receives its agent ID. Claude uses the
> `SendMessage` tool with the agent's ID as the `to` field to resume it.
> The `SendMessage` tool is only available when [agent teams](/en/agent-teams)
> are enabled via `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`.
> — `/en/sub-agents#resume-subagents`

So `SendMessage` is the unified mailbox/resume tool. It's exposed on the tool surface like any other tool, and `PreToolUse` matchers see it.

## Method

1. Wrote two probe scripts in `toy-repo/scripts/hooks/`:
   - `probe-sendmessage.sh` — gated by matcher `"SendMessage"`. Logs full env to a per-call file and full stdin JSON to a per-call file.
   - `probe-allpretool.sh` — no matcher (wildcard). Logs every PreToolUse stdin to a single rolling file. Used as a fallback to identify the actual tool name if the targeted matcher missed.
2. Added both probes to `toy-repo/.claude/settings.json` `hooks.PreToolUse`, alongside the existing `path-allowlist.sh`.
3. Truncated all prior log files.
4. Started a fresh interactive `claude` session in `toy-repo/`. Lead created team `phase17-probe`, spawned `api` (toy-api) and `web` (toy-web), instructed api to send a single peer message via SendMessage with body `PHASE17_PROBE_TEST`. Api confirmed; web confirmed receipt; both went idle.
5. Inspected `work/probe-sendmessage-*.log` and `work/probe-allpretool.log`.

## Observations

The `SendMessage` matcher hook fired **4 times** during the test (one log file per fire). The wildcard hook captured the full `PreToolUse` stream, which included those 4 SendMessage calls plus other tool calls during team setup (`ToolSearch`, `TeamCreate`, `Agent` to spawn each teammate).

The four SendMessage events captured:

| # | Direction | Sender session | `agent_type` in JSON | Notes |
|---|---|---|---|---|
| 1 | api → web | api's session | `"toy-api"` | The actual probe — body `PHASE17_PROBE_TEST` |
| 2 | web → team-lead | web's session | `"toy-web"` | Web acknowledging receipt |
| 3 | lead → api | lead's session | **`null`** (field absent) | Lead asking api to report call shape |
| 4 | api → team-lead | api's session | `"toy-api"` | Api's verbose report-back |

All four were fully visible to the hook, with bodies, recipients, and summaries intact in `tool_input`.

### Hook input — exact JSON shape (representative event, api → web)

```json
{
  "session_id": "3d90604f-0d90-4f20-9d5c-9f26c6b99e6e",
  "transcript_path": "/Users/tw/.claude/projects/-Users-tw-github-yakOS-toy-repo/3d90604f-0d90-4f20-9d5c-9f26c6b99e6e.jsonl",
  "cwd": "/Users/tw/github/yakOS/toy-repo",
  "permission_mode": "default",
  "agent_type": "toy-api",
  "hook_event_name": "PreToolUse",
  "tool_name": "SendMessage",
  "tool_input": {
    "to": "web",
    "summary": "phase17 probe",
    "message": "PHASE17_PROBE_TEST",
    "type": "message",
    "recipient": "web",
    "content": "PHASE17_PROBE_TEST"
  },
  "tool_use_id": "toolu_01TvmNqMtjRTgwjmG9riMYET"
}
```

### Env vars visible to the hook script

```
CLAUDECODE=1
CLAUDE_CODE_ENTRYPOINT=cli
CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1
CLAUDE_PROJECT_DIR=/Users/tw/github/yakOS/toy-repo
HOME=/Users/tw
USER=tw
```

Notably absent: `CLAUDE_CODE_AGENT` (which Phase 0 Test 7 documented as set during `claude --agent X` invocations). It's NOT set during in-team SendMessage hook fires. **Implication:** for hooks that need to know which agent is calling, **read `agent_type` from stdin JSON, not from env.** The env approach is unreliable in a team context.

## What the hook sees about (a) sender, (b) recipient, (c) body

- **(a) Sender identity:**
  - `agent_type` field in stdin JSON — `"toy-api"`, `"toy-web"`, etc. for teammates.
  - `agent_type` is **absent** for the lead's SendMessage calls. Same pattern as the lead's Stop hook from Phase 0 Test 7. **The presence/absence of `agent_type` is the cleanest lead-vs-teammate discriminator.**
  - `session_id` is the sender's session, so a hook can correlate with the sender's transcript file at `transcript_path`.
- **(b) Recipient:** `tool_input.to` (and a duplicate at `tool_input.recipient`). Values are the names assigned at spawn time (`"api"`, `"web"`) for teammates; the special string `"team-lead"` when a teammate messages the lead.
- **(c) Message body:** `tool_input.message` (and a duplicate at `tool_input.content`). Full body, no truncation.

So the hook has **complete information** to log a structured audit record of every team-internal message — sender role, recipient, summary, body, timestamp (derivable from session_id correlation or the hook's own clock), and source session/transcript path.

## Secondary observations

1. **Tool input has duplicate fields.** Every SendMessage call carried both the canonical `{to, summary, message}` AND duplicate `{recipient, type: "message", content}`. The user's lead reported that the tool ignored the extras and routed off the canonical three. The hook sees both. **For YakOS:** parse the canonical names; the extras are noise but not harmful. Worth a comment in `mailbox-mirror.sh` so future readers don't get confused.

2. **`team-lead` is the recipient name when a teammate addresses the lead.** Not the user-assigned lead name (the user-facing pane says `team-lead❯`). YakOS docs/audit-log readers should know this constant.

3. **The lead's SendMessage to teammates also fires the hook.** This means `mailbox-mirror.sh` will capture lead-issued instructions to teammates, not just peer DMs. Useful for full-team audit. If you want only peer DMs in the audit log, filter on `agent_type != null` in the hook script (i.e., skip lead-originated messages).

4. **Hook fires BEFORE the message is delivered.** Standard PreToolUse semantics — exit 2 would cancel the SendMessage call entirely. So `mailbox-mirror.sh` could not only mirror but also enforce policies (e.g., reject messages with body sizes > N, reject messages to specific recipients, redact secrets before delivery). Same defense-in-depth pattern as Phase 0 Test 5/6.

5. **Wildcard PreToolUse also captured `TeamCreate` and `Agent` tool calls.** Useful for YakOS — the team-lifecycle tools (`TeamCreate`, `Agent` for spawning teammates, presumably `TeamDelete`) are all hookable via PreToolUse. So if YakOS ever needs to log "team X spawned", it's a `PreToolUse` matcher on `TeamCreate`. Free observability.

## Implication for YakOS

**Phase 2 ships `mailbox-mirror.sh` as a default project-level PreToolUse hook on `SendMessage`.** No fallback to convention-only needed.

The reference implementation can be straightforward:

```bash
#!/usr/bin/env bash
# mailbox-mirror.sh — capture every SendMessage call to a durable audit log.
set -euo pipefail
LOG="${CLAUDE_PROJECT_DIR}/work/current/messages.ndjson"
mkdir -p "$(dirname "$LOG")"

INPUT=$(cat)
ts=$(date -u +%FT%TZ)

jq -c \
  --arg ts "$ts" \
  '{
    ts: $ts,
    sender_role: (.agent_type // "lead"),
    sender_session: .session_id,
    sender_transcript: .transcript_path,
    to: .tool_input.to,
    summary: .tool_input.summary,
    body: .tool_input.message
  }' <<< "$INPUT" >> "$LOG"

exit 0
```

This produces an append-only NDJSON audit log of every team-internal message, with sender role normalized (lead → `"lead"`, teammates → their `agent_type`), recipient name, summary, and full body. Phase 0 Test 8's "private content with public metadata" gap closes.

If YakOS wants to distinguish "audit only" from "audit + enforce", `mailbox-mirror.sh` can be split into two PreToolUse entries on the `SendMessage` matcher: one mirror-only (always exit 0), one policy-checker that exits 2 on violations. Both fire; the policy-checker can block delivery while the mirror records the attempt regardless.

## Confidence

**~95% on outcome 1 (clean YES).** Four hook fires, all fully captured, identical structure to other PreToolUse events, matching docs. The only residual uncertainty is whether some path (fork mode, MCP-routed messaging, future SendMessage variants) might bypass this — but for the documented Agent Teams team-internal mailbox, it's solid.

## Docs quotes worth keeping

From `/en/sub-agents#resume-subagents`:

> The `SendMessage` tool is only available when [agent teams](/en/agent-teams) are enabled via `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`.

From `/en/agent-teams#context-and-communication`:

> **Teammate messaging**: send a message to one specific teammate by name.
> To reach everyone, send one message per recipient.

The first quote confirms `SendMessage` is the canonical name and is gated on the team-mode flag (which YakOS already mandates). The second confirms the unicast-only model — no broadcast, so each peer DM is a discrete `SendMessage` call and a discrete hook fire. No batching to worry about.

## Files left for inspection

```
toy-repo/scripts/hooks/probe-sendmessage.sh
toy-repo/scripts/hooks/probe-allpretool.sh
toy-repo/.claude/settings.json              # contains both probe hooks plus existing path-allowlist
toy-repo/work/probe-sendmessage-env-*.log   # 4 files, one per fire
toy-repo/work/probe-sendmessage-stdin-*.log # 4 files, one per fire
toy-repo/work/probe-allpretool.log          # full PreToolUse stream during the test
```

The team `phase17-probe` is still up per the lead's message — let me know if you want it cleaned up or kept for further probes.
