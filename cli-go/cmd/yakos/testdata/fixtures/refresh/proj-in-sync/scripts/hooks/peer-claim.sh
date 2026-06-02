#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: peer-claim.sh — PreToolUse hook on Edit|Write|MultiEdit.
#
# Plan 1 M2 — co-pilot mode per-file claim enforcement.
#
# When multi-dev coord is enabled (yakos_coord_enabled returns 0):
#   1. Resolve the target path to repo-relative form.
#   2. Read active-claims.json (rebuild from activity.ndjson tail if
#      missing or stale).
#   3. If another session holds an unexpired claim → check the bypass
#      file; if no bypass → ho_block with explanation citing the owner.
#   4. Else (free or self-held) → append a claim_intent event to the
#      coord activity stream. The companion PostToolUse hook
#      (peer-claim-confirm.sh) flips intent → confirmed after the edit
#      succeeds, and rebuilds active-claims.json.
#
# When coord is disabled (the common single-dev case): pass through.
# This hook is a no-op for vanilla yakOS.
#
# TTL defaults by file type (refreshed by holder on each PreToolUse
# pass; intents older than 60s without a confirm auto-expire):
#   SQL / migrations: 1800s (30 min)
#   scratchpad markdown (decisions/contracts/plan/status/findings): 120s
#   lock files (*.lock, package-lock.json, go.sum, Cargo.lock, Pipfile.lock): 300s
#   everything else: 600s (10 min)

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

# No-op when coord isn't enabled.
if ! command -v yakos_coord_enabled >/dev/null 2>&1 || ! yakos_coord_enabled; then
    exit 0
fi
command -v jq >/dev/null 2>&1 || exit 0

file="$(hi_file_path)"
[ -n "$file" ] || exit 0

# Convert to repo-relative form (claim keys are repo-relative; different
# users have different absolute CLAUDE_PROJECT_DIR paths but the same
# repo content).
rel_file="$file"
if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    case "$file" in
        "$CLAUDE_PROJECT_DIR"/*) rel_file="${file#$CLAUDE_PROJECT_DIR/}" ;;
    esac
fi

# Per-file-type TTL defaults.
case "$rel_file" in
    *.sql|*/migrations/*) TTL=1800 ;;
    decisions.md|contracts.md|plan.md|status.md|findings.md) TTL=120 ;;
    *.lock|*-lock.*|go.sum) TTL=300 ;;
    *) TTL=600 ;;
esac

# Caller identity. Export YAKOS_AGENT_ID so yakos_coord_emit attributes
# the event to the right agent (hi_sender_role returns "lead" when
# .agent_type is absent).
me_user="${USER:-$(id -un 2>/dev/null || echo unknown)}"
me_host="${HOSTNAME:-$(hostname -s 2>/dev/null || echo unknown)}"
me_pid="${YAKOS_SESSION_PID:-$$}"
me_agent="$(hi_sender_role)"
export YAKOS_AGENT_ID="$me_agent"
now_ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

CLAIMS_FILE="$(yakos_coord_claims_file)"
ACTIVITY_LOG="$(yakos_coord_activity_log)"

# --- Rebuild active-claims.json if missing or stale -----------------
# Reader is allowed to rebuild; writer (peer-claim-confirm.sh) rebuilds
# after every confirmation. We rebuild here as a self-healing measure
# when the projection is missing.
_rebuild_claims() {
    [ -f "$ACTIVITY_LOG" ] || { printf '{"claims":[]}' > "$CLAIMS_FILE" 2>/dev/null; return; }
    # Fold the activity log into a claims projection. For each path, the
    # most recent claim_intent / claim_confirmed wins; claim_released
    # clears it. Expiry computed against the event's TTL.
    local tmp="${CLAIMS_FILE}.tmp.$$"
    jq -s --arg now "$now_ts" '
        # Filter to claim_* events only
        map(select(.kind | startswith("claim_") or . == "team_deleted" or . == "session_ended"))
        # Group by target.path; keep last-wins state per path
        | reduce .[] as $e ({};
            . as $acc
            | ($e.kind) as $k
            | if $k == "team_deleted" or $k == "session_ended" then
                # Drop all claims by this session
                with_entries(
                    select(.value.owners | all(
                        (.user != $e.actor.user) or
                        (.host != $e.actor.host) or
                        (.pid != $e.actor.pid)
                    ))
                )
              elif $k == "claim_released" then
                (.[$e.detail.path] // empty | $acc) | del(.[$e.detail.path])
              else
                # claim_intent / claim_confirmed / claim_renewed
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
        # Drop expired
        | with_entries(
            select(.value.owners[0].expires_at > $now)
        )
        | {generated_at: $now, claims: (to_entries | map(.value))}
    ' "$ACTIVITY_LOG" > "$tmp" 2>/dev/null && mv "$tmp" "$CLAIMS_FILE" 2>/dev/null \
        || rm -f "$tmp" 2>/dev/null
}

if [ ! -f "$CLAIMS_FILE" ]; then
    _rebuild_claims
fi

# --- Look up existing claim for this path ---------------------------
existing_owner_json="null"
if [ -f "$CLAIMS_FILE" ]; then
    existing_owner_json="$(jq -c --arg p "$rel_file" --arg now "$now_ts" \
        '.claims[]? | select(.path == $p) | .owners[0] // empty | select(.expires_at > $now)' \
        "$CLAIMS_FILE" 2>/dev/null | head -n 1)"
    [ -z "$existing_owner_json" ] && existing_owner_json="null"
fi

# Is this me? Match on (user, host, pid) — the durable session id.
is_me="false"
if [ "$existing_owner_json" != "null" ]; then
    is_me="$(printf '%s' "$existing_owner_json" | jq -r --arg u "$me_user" --arg h "$me_host" --argjson p "$me_pid" \
        '(.user == $u and .host == $h and .pid == $p)' 2>/dev/null || echo false)"
fi

if [ "$existing_owner_json" != "null" ] && [ "$is_me" != "true" ]; then
    # Conflict — another session holds this file.
    peer_user="$(printf '%s' "$existing_owner_json" | jq -r '.user' 2>/dev/null)"
    peer_host="$(printf '%s' "$existing_owner_json" | jq -r '.host' 2>/dev/null)"
    peer_agent="$(printf '%s' "$existing_owner_json" | jq -r '.agent // "unknown"' 2>/dev/null)"
    peer_expires="$(printf '%s' "$existing_owner_json" | jq -r '.expires_at' 2>/dev/null)"
    bypass_scope="file=$rel_file peer=$peer_user@$peer_host"

    if ho_check_bypass "peer-claim" "$bypass_scope"; then
        extra="$(jq -nc --arg f "$rel_file" --argjson o "$existing_owner_json" \
            '{file: $f, peer_owner: $o, bypass: true}')"
        ho_log "peer-claim" "WARN" "pass" "peer-held but bypass active" "$extra"
        # Emit a claim_intent anyway — the bypass authorizes the override
        # but we still want the trail.
        yakos_coord_emit "claim_intent" "$(jq -nc \
            --arg p "$rel_file" --argjson ttl "$TTL" \
            --arg exp "$(date -u -j -v "+${TTL}S" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "+${TTL} seconds" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)" \
            --arg byp "true" \
            '{path: $p, ttl_seconds: $ttl, expires_at: $exp, via_bypass: ($byp == "true")}' 2>/dev/null)"
        exit 0
    fi

    extra="$(jq -nc --arg f "$rel_file" --argjson o "$existing_owner_json" \
        '{file: $f, peer_owner: $o}')"
    ho_log "peer-claim" "BLOCK" "block" "peer holds claim" "$extra"
    ho_block "peer-claim" \
"file '$rel_file' is claimed by $peer_user@$peer_host (agent: $peer_agent, expires: $peer_expires).
       Options:
         1. wait for the claim to expire or be released
         2. coordinate via 'yakos peer log' + a SendMessage
         3. bypass: add an entry to work/current/hook-bypass.md with
            Hook: peer-claim
            Scope: $bypass_scope"
fi

# --- Free or self-held: post claim_intent (or claim_renewed if self) -
exp_iso="$(date -u -j -v "+${TTL}S" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
    || date -u -d "+${TTL} seconds" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"
event_kind="claim_intent"
[ "$is_me" = "true" ] && event_kind="claim_renewed"

yakos_coord_emit "$event_kind" "$(jq -nc \
    --arg p "$rel_file" \
    --argjson ttl "$TTL" \
    --arg exp "$exp_iso" \
    '{path: $p, ttl_seconds: $ttl, expires_at: $exp}' 2>/dev/null)"

extra="$(jq -nc --arg f "$rel_file" --argjson ttl "$TTL" --arg ek "$event_kind" \
    '{file: $f, ttl_seconds: $ttl, event: $ek}')"
ho_log "peer-claim" "REPORT" "pass" "claim posted" "$extra"

exit 0
