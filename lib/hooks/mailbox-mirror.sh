#!/usr/bin/env bash
# Purpose: mailbox-mirror.sh — PreToolUse hook on SendMessage.
#
# Mirrors every team-internal SendMessage call (peer DM, lead → teammate,
# teammate → lead) to work/current/messages.ndjson. Always exits 0; this
# hook is audit, never policy.
#
# Phase 1.7 confirmed clean: SendMessage is hookable, the hook payload
# carries full body, sender (.agent_type — absent = lead), and recipient.
#
# Companion to this live-fire hook: Phase 0.5 (2026-04-29) found the
# durable on-disk inbox at ~/.claude/teams/<team>/inboxes/<recipient>.json.
# Peer-to-peer DMs that don't transit the lead's hook context still
# land there. session-end-check.sh snapshots those inbox files into
# work/current/team-inboxes/ at session end as a complementary audit
# path.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

# Defensive: only act when this is actually a SendMessage call.
if [ "$(hi_tool)" != "SendMessage" ]; then
    exit 0
fi

# paths.sh is sourced transitively through hook-output.sh; resolver always
# present after that line.
if command -v yakos_messages_log_file >/dev/null 2>&1; then
    MESSAGES_LOG="$(yakos_messages_log_file)"
else
    MESSAGES_LOG="${CLAUDE_PROJECT_DIR:-.}/work/current/messages.ndjson"
fi
mkdir -p "$(dirname -- "$MESSAGES_LOG")" 2>/dev/null || true

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sender="$(hi_sender_role)"
to="$(hi_msg_to)"
summary="$(hi_msg_summary)"
body="$(hi_msg_body)"
session="$(hi_session_id)"
transcript="$(hi_transcript)"

# Append to messages.ndjson — the durable peer-conversation audit log.
jq -nc \
    --arg ts "$ts" \
    --arg from "$sender" \
    --arg to "$to" \
    --arg summary "$summary" \
    --arg body "$body" \
    --arg session "$session" \
    --arg transcript "$transcript" \
    '{ts: $ts, from: $from, to: $to, summary: $summary, body: $body, session_id: $session, transcript_path: $transcript}' \
    >> "$MESSAGES_LOG"

# Also a structured log entry (so hook-counting tooling sees this hook ran).
extra="$(jq -nc --arg from "$sender" --arg to "$to" --arg summary "$summary" \
    '{from: $from, to: $to, summary: $summary}')"
ho_log "mailbox-mirror" "REPORT" "pass" "logged peer message" "$extra"

exit 0
