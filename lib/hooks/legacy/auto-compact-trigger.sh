#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: auto-compact-trigger.sh — Stop hook that injects /compact when
#          the context-threshold hook has written the .compact-pending marker.
#
# Hook context: Stop — fires after Claude Code finishes a turn.
#
# Mechanism:
#   1. Check for work/current/.compact-pending (written atomically by
#      context-threshold.sh when context_thresholds.auto threshold is crossed).
#   2. If the marker exists:
#      a. Remove the marker (idempotent — next Stop event is a no-op).
#      b. Emit JSON to stdout: {"decision":"block","reason":"/compact",...}
#      Claude Code feeds "reason" back as the next user-turn prompt,
#      causing the /compact slash command to execute automatically.
#   3. If the marker does not exist: exit 0 (no-op, common case).
#
# Opt-in: this hook only fires when the operator has set
#   context_thresholds.auto in ~/.yakos-state/settings.json via:
#   yakos compact threshold --auto 85
#
# Degradation: if this script exits non-zero or Claude Code does not honour
# the "block" decision (older version), the session exits normally. No data
# is lost. The marker remains and is retried on the next Stop event.
#
# JSON contract verified against ralph-loop plugin Stop hook and
# validate-hook-schema.sh (field: decision=block, reason=prompt text,
# systemMessage=advisory string).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
# shellcheck source=lib/hook-input.sh
. "$HOOK_DIR/lib/hook-input.sh"
# shellcheck source=lib/hook-output.sh
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

current_dir="$(yakos_current_dir)"
if [ -z "$current_dir" ] || [ ! -d "$current_dir" ]; then
    exit 0
fi

marker_file="$current_dir/.compact-pending"

# No marker → no-op (common path).
[ -f "$marker_file" ] || exit 0

# Remove the marker before emitting the block. If we crash between removal
# and emission, the next Stop event is a no-op (marker gone). Better to
# miss one compact than to loop indefinitely.
rm -f "$marker_file" 2>/dev/null || true

# Emit the Stop-hook block JSON to stdout. Claude Code reads this and feeds
# "reason" back as the next user-turn prompt.
jq -n \
    --arg reason "/compact" \
    --arg msg "yakos auto-compact: context threshold crossed. Executing /compact to reclaim context window. (yakos compact threshold --auto to adjust or disable)" \
    '{
        "decision": "block",
        "reason": $reason,
        "systemMessage": $msg
    }'

# Log the trigger event to the yakos log.
ho_log "auto-compact-trigger" "INFO" "compact_triggered" \
    "auto-compact triggered; /compact injected as next turn" \
    "{\"marker_removed\": true}"

exit 0
