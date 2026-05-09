#!/usr/bin/env bash
# Purpose: task-dependency-gate.sh — TaskCompleted hook (REPORT-ONLY in v0.1).
#
# Status: REPORT-ONLY (build-prompt §"REPORT-only fallback rule").
#
# Reason for the fallback:
#   - Phase 0 Test 4 confirmed that the runtime's `blockedBy` field is
#     advisory only — the gate must be enforced by a hook.
#   - Phase 0 Test 5 confirmed TaskCompleted hooks CAN block (exit 2).
#   - But Phase 0 did NOT dump the TaskCompleted stdin schema, and the
#     team task list at ~/.claude/tasks/<team>/ is documented as
#     "auto-generated and not safe to pre-author or hand-edit" — the
#     file format is undocumented.
#   - Without a confirmed schema for either the hook stdin OR the task
#     list, this hook cannot make authoritative block/pass decisions.
#
# v0.1 behavior: log the would-be decision (PASS or WARN-with-suspect-block)
# based on whatever .task / .blockedBy fields are present in stdin JSON.
# Always exits 0. Mark records with mode="report-only" so dashboards can
# distinguish "this hook is enforcing" from "this hook is observing".
#
# Upgrading to BLOCKING in v0.2 needs:
#   - A Phase 0.5 probe that dumps the actual TaskCompleted JSON shape.
#   - Schema for ~/.claude/tasks/<team>/ files (or an alternative way to
#     read team task state from inside the hook).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

# Best-effort schema guess — these field names are plausible but unverified.
# A wrong guess just means the suspect_block hint is empty; we never block.
task_id="$(hi_field '.task.id // .task_id // .tool_input.id // empty')"
blocked_by="$(hi_field '.task.blockedBy // .blockedBy // empty')"
agent="$(hi_sender_role)"

# If we have a blockedBy list AND we have visibility into task states,
# we'd compute "would_block". v0.1 has no such visibility, so the hint is
# always "unknown". Severity stays REPORT.
would_block="unknown"
suspect_block_reason=""
if [ -n "$blocked_by" ] && [ "$blocked_by" != "[]" ] && [ "$blocked_by" != "null" ]; then
    suspect_block_reason="task declares blockedBy: $blocked_by (cannot verify resolution in v0.1)"
fi

extra="$(jq -nc \
    --arg mode "report-only" \
    --arg agent "$agent" \
    --arg task_id "$task_id" \
    --arg blocked_by "$blocked_by" \
    --arg would_block "$would_block" \
    --arg suspect "$suspect_block_reason" \
    '{mode: $mode, agent_type: $agent, task_id: $task_id, blocked_by: $blocked_by, would_block: $would_block, suspect_block_reason: $suspect}')"

ho_log "task-dependency-gate" "REPORT" "pass" "report-only in v0.1 (UNCLEAR — see hook source)" "$extra"
exit 0
