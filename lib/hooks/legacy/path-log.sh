#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: path-log.sh — PreToolUse companion to path-allowlist.sh.
#
# Always logs, never blocks. Defense-in-depth: if path-allowlist.sh is
# accidentally disabled or misconfigured, this hook still records every
# file-write attempt for forensic review. Configure both behind the same
# Edit|Write matcher in settings.json.

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

agent="$(hi_sender_role)"
file="$(hi_file_path)"

extra="$(jq -nc --arg agent "$agent" --arg file "$file" --arg tool "$tool" \
    '{agent_type: $agent, file_path: $file, tool: $tool}')"
ho_log "path-log" "REPORT" "pass" "logged file-write attempt" "$extra"

exit 0
