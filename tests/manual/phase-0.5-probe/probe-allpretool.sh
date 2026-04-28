#!/usr/bin/env bash
# Purpose: Wildcard PreToolUse capture. Sanity check that we're not
#          missing task-related events under unexpected names. Catches
#          TaskUpdate, TaskCreate, TaskList, TaskDelete (if they exist
#          as PreToolUse-visible tool calls) — orthogonal to the
#          TaskCreated/TaskCompleted lifecycle hooks.
# Inputs:  Stdin JSON from any PreToolUse event (no matcher).
# Outputs: $YAKOS_PROBE_DIR/allpretool.ndjson — append-only NDJSON,
#          one record per fire with tool_name + raw stdin.
# Exit codes: ALWAYS 0.

set -eu

PROBE_DIR="${YAKOS_PROBE_DIR:-${CLAUDE_PROJECT_DIR:-$PWD}/work/probe}"
mkdir -p "$PROBE_DIR" 2>/dev/null || true

log_file="$PROBE_DIR/allpretool.ndjson"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

input="$(cat)"

# Wrap raw stdin under a record envelope. If jq is available, validate
# and pretty-encode; otherwise raw-quote.
if command -v jq >/dev/null 2>&1; then
    jq -nc \
        --arg ts "$ts" \
        --arg pid "$$" \
        --argjson input "$input" \
        '{ts: $ts, pid: $pid, input: $input}' \
        >> "$log_file" 2>/dev/null \
        || printf '{"ts":"%s","pid":"%s","input":%s}\n' "$ts" "$$" "$input" >> "$log_file"
else
    printf '{"ts":"%s","pid":"%s","input":%s}\n' "$ts" "$$" "$input" >> "$log_file"
fi

exit 0
