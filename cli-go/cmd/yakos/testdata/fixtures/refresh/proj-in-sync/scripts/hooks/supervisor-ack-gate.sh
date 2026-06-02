#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervisor-ack-gate.sh — PreToolUse hook that blocks TeamCreate
# and Agent dispatches when there are unacknowledged supervisor findings
# at or above the surface_to_operator tier.
#
# Finding severity ladder (ordered):
#   continue < surface_to_operator < block_next_tool < halt
#
# The gate fires when ANY finding has recommended_action in:
#   surface_to_operator | block_next_tool | halt
# AND has not been acknowledged in ~/.yakos-state/supervisor-acks.ndjson
# for this project.
#
# Finding ID scheme: "f-<ts>-<project>-<linenum>" where:
#   <ts>       = finding's .ts field (ISO-8601, colons replaced with -)
#   <project>  = YAKOS_PROJECT_NAME (or basename of CLAUDE_PROJECT_DIR)
#   <linenum>  = 1-based line index in supervisor-findings.ndjson
# IDs are stable for the same file content (deterministic, no new field
# required in the findings schema).
#
# Conservative failure mode:
#   - Missing findings file      → log WARN + PASS (no false block)
#   - Malformed JSON lines       → skip that line (log WARN per-line)
#   - Missing ack file           → treat all findings as unacknowledged
#   - jq/yakos_current_dir absent → log WARN + PASS
#
# Per-project opt-out via .yakos.yml:
#   supervise:
#     gate_on_escalation: false
#
# Disabled globally via env:
#   YAKOS_SUPERVISOR_DISABLE=1
#
# Usage in settings.json (wired under PreToolUse > TeamCreate|Agent):
#   {"type": "command", "command": "${CLAUDE_PROJECT_DIR}/scripts/hooks/supervisor-ack-gate.sh"}

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# --- Guard: only fires on TeamCreate and Agent --------------------------------

tool="$(hi_tool)"
case "$tool" in
    TeamCreate|Agent) ;;
    *) exit 0 ;;
esac

# --- Guard: emergency bypass --------------------------------------------------

if [ "${YAKOS_SUPERVISOR_DISABLE:-0}" = "1" ]; then
    exit 0
fi

# --- Guard: per-project opt-out in .yakos.yml ---------------------------------

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"

if [ -f "$yakos_yml" ]; then
    # Look for supervise.gate_on_escalation: false
    # Note: the block is `supervise:` (not `supervisor:`) to differentiate
    # from the existing supervisor-gate.sh config block.
    if grep -A 10 '^[[:space:]]*supervise:' "$yakos_yml" 2>/dev/null \
        | grep -q '^[[:space:]]*gate_on_escalation:[[:space:]]*false[[:space:]]*$'; then
        exit 0
    fi
fi

# --- Hard dependencies --------------------------------------------------------

if ! command -v jq >/dev/null 2>&1; then
    ho_log "supervisor-ack-gate" "WARN" "pass" \
        "jq not available; skipping supervisor-ack-gate" "{}"
    exit 0
fi

if ! command -v yakos_current_dir >/dev/null 2>&1; then
    ho_log "supervisor-ack-gate" "WARN" "pass" \
        "yakos_current_dir not available; skipping supervisor-ack-gate" "{}"
    exit 0
fi

# --- Paths --------------------------------------------------------------------

current_dir="$(yakos_current_dir)"
findings_file="$current_dir/supervisor-findings.ndjson"
acks_file="${HOME}/.yakos-state/supervisor-acks.ndjson"
project_name="$(yakos_project_name)"

# --- Missing findings file: PASS ----------------------------------------------

if [ ! -f "$findings_file" ]; then
    ho_log "supervisor-ack-gate" "REPORT" "pass" \
        "no supervisor-findings.ndjson; nothing to gate on" "{}"
    exit 0
fi

# --- Build the set of acknowledged finding ids for this project ---------------

# acks_set is a newline-separated list of acknowledged finding_ids for this project.
# If the ack file is absent/empty/malformed, we treat nothing as acknowledged.
acks_set=""
if [ -f "$acks_file" ] && [ -s "$acks_file" ]; then
    acks_set="$(jq -r --arg proj "$project_name" \
        'select(.project == $proj) | .finding_id // empty' \
        "$acks_file" 2>/dev/null || true)"
fi

# Helper: check if a finding_id is in acks_set
is_acked() {
    local fid="$1"
    case "$acks_set" in
        "$fid"|"$fid"$'\n'*|*$'\n'"$fid"|*$'\n'"$fid"$'\n'*) return 0 ;;
    esac
    return 1
}

# --- Severity ladder: which recommended_actions require acknowledgement -------

needs_ack() {
    # Returns 0 if the recommended_action meets the escalation threshold.
    local action="$1"
    case "$action" in
        surface_to_operator|block_next_tool|halt) return 0 ;;
        *) return 1 ;;
    esac
}

# --- Scan findings for unacknowledged escalations ----------------------------

pending_ids=""
pending_summaries=""
linenum=0

while IFS= read -r line; do
    linenum=$((linenum + 1))
    [ -n "$line" ] || continue

    # Validate JSON
    if ! printf '%s' "$line" | jq empty 2>/dev/null; then
        ho_log "supervisor-ack-gate" "WARN" "pass" \
            "skipping malformed JSON on line $linenum of supervisor-findings.ndjson" \
            "$(jq -nc --argjson n "$linenum" '{line: $n}')"
        continue
    fi

    recommended="$(printf '%s' "$line" | jq -r '.recommended_action // "continue"' 2>/dev/null || true)"
    needs_ack "$recommended" || continue

    # Derive the stable finding id
    raw_ts="$(printf '%s' "$line" | jq -r '.ts // ""' 2>/dev/null || true)"
    # Replace colons (and any char that'd be awkward in shell) with hyphens
    safe_ts="${raw_ts//:/-}"
    finding_id="f-${safe_ts}-${project_name}-${linenum}"

    is_acked "$finding_id" && continue

    # Collect pending
    rationale="$(printf '%s' "$line" | jq -r '.rationale // "(no rationale)"' 2>/dev/null || true)"
    # Truncate long rationales for the block message
    rationale_short="$(printf '%s' "$rationale" | head -c 120)"
    if [ "${#rationale}" -gt 120 ]; then
        rationale_short="${rationale_short}..."
    fi

    if [ -n "$pending_ids" ]; then
        pending_ids="${pending_ids}
${finding_id}"
        pending_summaries="${pending_summaries}
  [${finding_id}] ${recommended}: ${rationale_short}"
    else
        pending_ids="${finding_id}"
        pending_summaries="  [${finding_id}] ${recommended}: ${rationale_short}"
    fi
done < "$findings_file"

# --- No pending escalations: PASS --------------------------------------------

if [ -z "$pending_ids" ]; then
    ho_log "supervisor-ack-gate" "REPORT" "pass" \
        "no unacknowledged supervisor escalations" \
        "$(jq -nc --arg proj "$project_name" '{project: $proj}')"
    exit 0
fi

# --- Pending escalations: log + BLOCK ----------------------------------------

# Count them for the log record
pending_count=0
while IFS= read -r id; do
    [ -n "$id" ] && pending_count=$((pending_count + 1))
done <<< "$pending_ids"

# Build the first pending id for a quick ack example in the message
first_id="$(printf '%s' "$pending_ids" | head -n 1)"

ho_log "supervisor-ack-gate" "BLOCK" "block" \
    "blocking $pending_count unacknowledged supervisor escalation(s)" \
    "$(jq -nc \
        --arg proj "$project_name" \
        --argjson count "$pending_count" \
        --arg ids "$pending_ids" \
        '{project: $proj, pending_count: $count, finding_ids: $ids}')"

ho_block "supervisor-ack-gate" \
"Supervisor escalation pending — acknowledge before next dispatch:
${pending_summaries}
Acknowledge with:
  yakos supervise ack ${first_id} [--note \"rationale\"]
Or list pending:  yakos supervise pending
Or ack all:       yakos supervise ack-all [--note \"...\"]
Per-project disable: set supervise.gate_on_escalation: false in .yakos.yml
Emergency (session only): export YAKOS_SUPERVISOR_DISABLE=1"
