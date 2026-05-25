#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervisor-gate.sh — PreToolUse hook that consults supervisor
# findings and blocks on critical drift (v0.33+).
#
# Reads the most recent finding from supervisor-findings.ndjson and:
#   - PASS    → exit 0 (allow)
#   - WARN    → exit 0 (allow), surface rationale via stderr to the lead
#   - CRITICAL → check block_threshold; if exceeded, ho_block with the
#                rationale. Otherwise surface via stderr.
#
# Always honors hook-bypass.md (scope: "supervisor finding=<finding_ts>")
# so operators can override after reviewing the finding.
#
# Disabled when:
#   - YAKOS_SUPERVISOR_DISABLE=1 (env override; emergency bypass)
#   - .yakos.yml has supervisor.enabled: false
#   - .yakos.yml has supervisor.block_on_critical: false (passive mode)

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# Emergency bypass
if [ "${YAKOS_SUPERVISOR_DISABLE:-0}" = "1" ]; then
    exit 0
fi

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
if [ -f "$yakos_yml" ]; then
    if grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -q '^[[:space:]]*enabled:[[:space:]]*false[[:space:]]*$'; then
        exit 0
    fi
fi

command -v jq >/dev/null 2>&1 || exit 0
command -v yakos_current_dir >/dev/null 2>&1 || exit 0

current_dir="$(yakos_current_dir)"
findings="$current_dir/supervisor-findings.ndjson"
[ -f "$findings" ] || exit 0

# Get the most recent finding
last="$(tail -n 1 "$findings" 2>/dev/null)"
[ -n "$last" ] || exit 0

# Validate it parses as JSON
if ! printf '%s' "$last" | jq empty 2>/dev/null; then
    ho_log "supervisor-gate" "WARN" "pass" \
        "most-recent finding is not valid JSON; ignoring" "{}"
    exit 0
fi

overall="$(printf '%s' "$last" | jq -r '.overall // "PASS"')"
rationale="$(printf '%s' "$last" | jq -r '.rationale // "(no rationale)"')"
finding_ts="$(printf '%s' "$last" | jq -r '.ts // "unknown"')"
recommended="$(printf '%s' "$last" | jq -r '.recommended_action // "continue"')"

# Idempotency: don't re-surface the same finding to the lead more than
# once. Track the last-surfaced finding_ts in a marker file.
marker="$current_dir/.supervisor-gate-last-surfaced"
last_surfaced=""
[ -f "$marker" ] && last_surfaced="$(cat "$marker" 2>/dev/null || echo '')"

case "$overall" in
    PASS)
        # All good. No surfacing needed.
        ho_log "supervisor-gate" "REPORT" "pass" \
            "supervisor finding: PASS" \
            "$(jq -nc --arg ts "$finding_ts" '{finding_ts: $ts, overall: "PASS"}')"
        exit 0
        ;;
    WARN)
        if [ "$finding_ts" != "$last_surfaced" ]; then
            ho_log "supervisor-gate" "WARN" "pass" \
                "supervisor WARN: $rationale" \
                "$(jq -nc --arg ts "$finding_ts" --arg r "$rationale" \
                    '{finding_ts: $ts, overall: "WARN", rationale: $r}')"
            # Surface to the lead via stderr (Claude Code shows stderr from hooks)
            cat >&2 <<EOF
supervisor-gate: WARN from supervisor (finding $finding_ts):
  $rationale
  Recommended action: $recommended
  (passing through; this is informational only)
EOF
            printf '%s\n' "$finding_ts" > "$marker" 2>/dev/null || true
        fi
        exit 0
        ;;
    CRITICAL)
        # Check block_on_critical config
        block_on_critical="true"
        if [ -f "$yakos_yml" ]; then
            b="$(grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
                | grep -E '^[[:space:]]*block_on_critical:[[:space:]]*(true|false)' \
                | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
            [ "$b" = "false" ] && block_on_critical="false"
        fi

        # Bypass check: did the operator add a bypass entry for this
        # finding?
        if ho_check_bypass "supervisor" "finding=$finding_ts"; then
            ho_log "supervisor-gate" "WARN" "pass" \
                "supervisor CRITICAL but bypass active for finding=$finding_ts" \
                "$(jq -nc --arg ts "$finding_ts" --arg r "$rationale" \
                    '{finding_ts: $ts, overall: "CRITICAL", rationale: $r, bypass: true}')"
            exit 0
        fi

        if [ "$block_on_critical" = "false" ]; then
            # Passive mode — surface but don't block
            if [ "$finding_ts" != "$last_surfaced" ]; then
                cat >&2 <<EOF
supervisor-gate: CRITICAL from supervisor (finding $finding_ts):
  $rationale
  Recommended action: $recommended
  (passive mode: supervisor.block_on_critical=false; passing through)
EOF
                printf '%s\n' "$finding_ts" > "$marker" 2>/dev/null || true
            fi
            ho_log "supervisor-gate" "WARN" "pass" \
                "supervisor CRITICAL but block_on_critical=false; surfaced only" \
                "$(jq -nc --arg ts "$finding_ts" --arg r "$rationale" \
                    '{finding_ts: $ts, overall: "CRITICAL", rationale: $r, blocked: false}')"
            exit 0
        fi

        # Active mode — block
        ho_log "supervisor-gate" "BLOCK" "block" \
            "supervisor CRITICAL; blocking" \
            "$(jq -nc --arg ts "$finding_ts" --arg r "$rationale" \
                '{finding_ts: $ts, overall: "CRITICAL", rationale: $r, blocked: true}')"
        ho_block "supervisor-gate" \
"supervisor flagged CRITICAL on finding $finding_ts:
       $rationale
       Recommended action: $recommended
       To proceed:
         1. Review the finding in work/current/supervisor-findings.ndjson
         2. If the supervisor is wrong, add a bypass entry:
            ## bypass:supervisor-override-$finding_ts
            **Hook:** supervisor
            **Scope:** finding=$finding_ts
            (plus the standard Hook/Reason/Approved/Created/Expires fields)
         3. Or set supervisor.block_on_critical: false in .yakos.yml
            for passive-mode warnings only.
         4. Emergency bypass for this session only:
            export YAKOS_SUPERVISOR_DISABLE=1"
        ;;
    *)
        # Unknown severity — log and pass
        ho_log "supervisor-gate" "WARN" "pass" \
            "unknown supervisor overall: '$overall'" "{}"
        exit 0
        ;;
esac
