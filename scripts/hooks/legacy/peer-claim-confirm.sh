#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: peer-claim-confirm.sh — PostToolUse hook on Edit|Write|MultiEdit.
#
# Plan 1 M2 — companion to peer-claim.sh. When the edit succeeds,
# this hook:
#   1. Emits a claim_confirmed event for the file.
#   2. Rebuilds active-claims.json atomically (write to .tmp, mv).
#
# No-op when coord isn't enabled. Always exits 0 — this is telemetry,
# not policy. The PreToolUse gate (peer-claim.sh) is where blocking
# happens; by the time we run, the edit has already landed.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    Edit|Write|MultiEdit) ;;
    *) exit 0 ;;
esac

if ! command -v yakos_coord_enabled >/dev/null 2>&1 || ! yakos_coord_enabled; then
    exit 0
fi
command -v jq >/dev/null 2>&1 || exit 0

file="$(hi_file_path)"
[ -n "$file" ] || exit 0

rel_file="$file"
if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    case "$file" in
        "$CLAUDE_PROJECT_DIR"/*) rel_file="${file#$CLAUDE_PROJECT_DIR/}" ;;
    esac
fi

# Per-file TTL — same defaults as peer-claim.sh.
case "$rel_file" in
    *.sql|*/migrations/*) TTL=1800 ;;
    decisions.md|contracts.md|plan.md|status.md|findings.md) TTL=120 ;;
    *.lock|*-lock.*|go.sum) TTL=300 ;;
    *) TTL=600 ;;
esac

YAKOS_AGENT_ID="$(hi_sender_role)"
export YAKOS_AGENT_ID
now_ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
exp_iso="$(date -u -j -v "+${TTL}S" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -d "+${TTL} seconds" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"

# Emit the confirmation event.
yakos_coord_emit "claim_confirmed" "$(jq -nc \
    --arg p "$rel_file" \
    --argjson ttl "$TTL" \
    --arg exp "$exp_iso" \
    '{path: $p, ttl_seconds: $ttl, expires_at: $exp}' 2>/dev/null)"

# Rebuild active-claims.json atomically. Same fold as peer-claim.sh's
# self-healing rebuild, but always — this is the canonical writer.
CLAIMS_FILE="$(yakos_coord_claims_file)"
ACTIVITY_LOG="$(yakos_coord_activity_log)"

if [ -f "$ACTIVITY_LOG" ]; then
    tmp="${CLAIMS_FILE}.tmp.$$"
    jq -s --arg now "$now_ts" '
        map(select(.kind | startswith("claim_") or . == "team_deleted" or . == "session_ended"))
        | reduce .[] as $e ({};
            . as $acc
            | ($e.kind) as $k
            | if $k == "team_deleted" or $k == "session_ended" then
                with_entries(
                    select(.value.owners | all(
                        (.user != $e.actor.user) or
                        (.host != $e.actor.host) or
                        (.pid != $e.actor.pid)
                    ))
                )
              elif $k == "claim_released" then
                del(.[$e.detail.path])
              else
                .[$e.detail.path] = {
                    path: $e.detail.path,
                    owners: [{
                        user: $e.actor.user,
                        host: $e.actor.host,
                        pid: $e.actor.pid,
                        session_id: $e.actor.session_id,
                        agent: $e.actor.agent,
                        status: (if $k == "claim_confirmed" then "confirmed" else "intent" end),
                        claimed_at: $e.ts,
                        renewed_at: $e.ts,
                        expires_at: $e.detail.expires_at,
                        ttl_seconds: $e.detail.ttl_seconds
                    }]
                }
              end
        )
        | with_entries(
            select(.value.owners[0].expires_at > $now)
        )
        | {generated_at: $now, claims: (to_entries | map(.value))}
    ' "$ACTIVITY_LOG" > "$tmp" 2>/dev/null && mv "$tmp" "$CLAIMS_FILE" 2>/dev/null \
        || rm -f "$tmp" 2>/dev/null
fi

extra="$(jq -nc --arg f "$rel_file" --argjson ttl "$TTL" \
    '{file: $f, ttl_seconds: $ttl}')"
ho_log "peer-claim-confirm" "REPORT" "pass" "claim confirmed" "$extra"

exit 0
