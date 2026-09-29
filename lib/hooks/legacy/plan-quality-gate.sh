#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: plan-quality-gate.sh — PreToolUse gate that blocks TeamCreate|Agent
# while a work/current/.plan-blocked marker is present. FAILS CLOSED.
#
# Split from the old dual-purpose script (K-81). The PostToolUse scorer that
# writes the marker now lives in plan-quality-score.sh and keeps its
# conservative "never block a save" posture. This gate is the opposite: a
# gate that silently passes when it cannot decide is not a gate.
#
# Fail-closed contract — every one of these exits 2 with a stderr reason:
#   - jq missing / stdin empty / not JSON / not a JSON object (hi_init, with
#     HOOK_FAIL_CLOSED=1)
#   - the payload names no tool at all
#   - work/current (or its nearest existing ancestor) is not searchable, so
#     "no marker" cannot be told apart from "cannot look"
#   - the marker exists but is not a regular file (a directory, a symlink)
#   - a helper library that fails to load (checked explicitly: bash 3.2 does
#     not abort on a failed `.` even under `set -e`)
#   - ANY unexpected internal failure or unbound variable (EXIT trap below:
#     a non-0/non-2 status is rewritten to 2, so a crash can never surface
#     as the non-blocking exit 1)
#
# Passes (exit 0): tool is not exactly TeamCreate or Agent; no marker; marker
# present but .yakos.yml sets plan_quality.enabled: false (marker cleared);
# YAKOS_PLAN_QUALITY_DISABLE=1 (the one emergency switch; it is checked
# before jq is needed). YAKOS_HOOKS_FAIL_OPEN=1 lets hi_init continue past
# degraded input, but with no readable tool_name this gate still blocks.
#
# Override a block:  yakos plan score override <plan_id> --reason "..."
# (removes the marker). A marker that is a directory is not removed by the
# override; the block message names the path to delete by hand.
#
# Registered in settings.json under:
#   PreToolUse  TeamCreate|Agent → gate
#
# Bash 3.2 (macOS /bin/bash) compatible: no associative arrays, no ${x,,},
# no `local -n`, no empty-array expansion under `set -u`.

set -eu

# ---- fail-closed on any crash --------------------------------------------------
# `set -e` / `set -u` / a failed `.` source all exit 1, which Claude Code treats
# as NON-blocking. Rewrite every exit status other than 0 (pass) and 2 (block)
# to 2 so a crash in this script blocks instead of waving the dispatch through.
# A readable helper lib with a SYNTAX error makes bash 3.2 exit 0 outright, which
# the status check above cannot see. So a pass must also be an explicit decision:
# every intended `exit 0` goes through _pqg_pass, and an exit 0 without it is
# rewritten to 2.
_pqg_decided=0
_pqg_pass() {
    _pqg_decided=1
    exit 0
}
_pqg_on_exit() {
    _pqg_rc=$?
    # This trap runs under the script's `set -e`. A failing `echo >&2` in here
    # (stderr closed -> rc 1) would exit the trap with that status, and any
    # status other than 2 is NON-blocking in Claude Code. So: no errexit, no
    # SIGPIPE, and every write is best effort. The decision below never depends
    # on whether the message could be delivered.
    set +e
    trap '' PIPE
    if [ "$_pqg_rc" -eq 0 ] && [ "$_pqg_decided" -ne 1 ]; then
        echo "plan-quality-gate: BLOCKED — exited without a gate decision (a helper library likely failed to parse); failing closed." >&2 || true
        echo "plan-quality-gate: emergency override: export YAKOS_PLAN_QUALITY_DISABLE=1" >&2 || true
        exit 2
    fi
    if [ "$_pqg_rc" -ne 0 ] && [ "$_pqg_rc" -ne 2 ]; then
        echo "plan-quality-gate: BLOCKED — internal error (exit $_pqg_rc); failing closed rather than passing the dispatch." >&2 || true
        echo "plan-quality-gate: emergency override: export YAKOS_PLAN_QUALITY_DISABLE=1" >&2 || true
        exit 2
    fi
}
trap _pqg_on_exit EXIT
# SIGPIPE (closed reader on stderr/stdout) would kill the script with 141 and
# skip nothing useful; ignore it so a failed write is just a failed write.
# SIGTERM/SIGHUP/SIGINT would exit 143/129/130, all non-blocking: turn them
# into the blocking status. (`exit 2` still runs the EXIT trap, with rc 2.)
trap '' PIPE
trap 'exit 2' TERM HUP INT

# Emergency disable is checked BEFORE hi_init so it stays reachable when jq is
# broken (same ordering the other blocking hooks use).
HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
if [ "${YAKOS_PLAN_QUALITY_DISABLE:-0}" = "1" ]; then
    # Leave a record of the bypass, best effort (libs may be what is broken).
    _pqg_decided=1
    { . "$HOOK_DIR/lib/hook-output.sh" && ho_log "plan-quality-gate" "WARN" "pass" \
        "YAKOS_PLAN_QUALITY_DISABLE=1: gate bypassed" "{}"; } 2>/dev/null || true
    exit 0
fi
# shellcheck disable=SC2034  # read by hi_init in lib/hook-input.sh
HOOK_FAIL_CLOSED=1
# A failed `.` does NOT trip `set -e` on bash 3.2 (macOS /bin/bash): the script
# would carry on without the library and could pass. Check each source explicitly.
_pqg_source() {
    # $2 names the sentinel variable the library sets on its LAST line. bash 5
    # returns 0 from `.` even when the file has a syntax error partway through
    # (it keeps executing after the bad command), so a zero status alone does
    # not prove the library loaded; the sentinel does.
    # shellcheck disable=SC1090  # path is one of three fixed lib files below
    if [ ! -r "$1" ] || ! . "$1" || [ "${!2:-0}" != "1" ]; then
        echo "plan-quality-gate: BLOCKED — cannot load helper library '$1' (failed to parse or run to completion); failing closed." >&2 || true
        exit 2
    fi
}
_pqg_source "$HOOK_DIR/lib/hook-input.sh" HI_LOADED
_pqg_source "$HOOK_DIR/lib/hook-output.sh" HO_LOADED
_pqg_source "$HOOK_DIR/lib/paths.sh" YAKOS_PATHS_LOADED
for _pqg_fn in hi_init hi_tool ho_log ho_block yakos_current_dir; do
    if ! command -v "$_pqg_fn" >/dev/null 2>&1; then
        echo "plan-quality-gate: BLOCKED — helper '$_pqg_fn' is not defined after loading libraries; failing closed." >&2
        exit 2
    fi
done

hi_init

tool="$(hi_tool)"
if [ -z "$tool" ]; then
    # hi_init already rejected non-JSON / non-object input. A valid object with
    # no tool_name cannot be evaluated; the matcher only routes named tools here.
    echo "plan-quality-gate: BLOCKED — payload has no tool_name; cannot decide whether this dispatch is gated." >&2
    exit 2
fi

# Exact match only: "AgentX" or "TeamCreateFoo" are not gated.
case "$tool" in
    TeamCreate|Agent) ;;
    *) _pqg_pass ;;
esac

current_dir="$(yakos_current_dir)"
blocked_marker="$current_dir/.plan-blocked"

# ---- can we actually look? -----------------------------------------------------
# `[ -e marker ]` is false both when the marker is absent AND when a parent
# directory is unsearchable. Tell them apart: find the nearest existing
# ancestor of work/current and require it to be searchable.
_probe="$current_dir"
while [ ! -d "$_probe" ]; do
    if [ -e "$_probe" ] || [ -L "$_probe" ]; then
        echo "plan-quality-gate: BLOCKED — '$_probe' exists but is not a directory; unable to tell whether '$blocked_marker' exists." >&2
        exit 2
    fi
    _next="$(dirname -- "$_probe")"
    if [ "$_next" = "$_probe" ]; then
        break
    fi
    _probe="$_next"
done
if [ ! -d "$_probe" ] || [ ! -x "$_probe" ]; then
    echo "plan-quality-gate: BLOCKED — cannot inspect '$_probe' (not a searchable directory); unable to tell whether '$blocked_marker' exists." >&2
    ho_log "plan-quality-gate" "BLOCK" "block" \
        "cannot inspect marker directory; failing closed" "{}" 2>/dev/null || true
    exit 2
fi
if [ "$_probe" = "$current_dir" ] && [ ! -r "$current_dir" ]; then
    echo "plan-quality-gate: BLOCKED — '$current_dir' is not readable; unable to tell whether '$blocked_marker' exists." >&2
    exit 2
fi

# ---- marker present? -----------------------------------------------------------
# -e follows symlinks; -L catches a dangling one. Anything that exists in any
# form counts as a marker (a directory must NOT read as "no marker").
if [ ! -e "$blocked_marker" ] && [ ! -L "$blocked_marker" ]; then
    ho_log "plan-quality-gate" "REPORT" "pass" \
        "no .plan-blocked marker; proceeding" "{}" 2>/dev/null || true
    _pqg_pass
fi

# ---- read the marker (best effort; never changes the block decision) ----------
reason_text=""
plan_id_from_marker=""
if [ -f "$blocked_marker" ]; then
    if command -v jq >/dev/null 2>&1 && jq -e . "$blocked_marker" >/dev/null 2>&1; then
        plan_id_from_marker="$(jq -r '.plan_id // empty' "$blocked_marker" 2>/dev/null || true)"
        reason_text="$(jq -r '.reason // empty' "$blocked_marker" 2>/dev/null || true)"
    else
        reason_text="$(head -n 3 "$blocked_marker" 2>/dev/null || true)"
    fi
else
    reason_text="marker '$blocked_marker' exists but is not a regular file; remove it by hand"
fi

# ---- per-project opt-out (plan_quality.enabled: false) -------------------------
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
if [ -f "$yakos_yml" ]; then
    # Scope to the plan_quality: section (stop at the next top-level key) so an
    # `enabled: false` belonging to another section does not disable the gate.
    if awk '/^[[:space:]]*plan_quality[[:space:]]*:/ { b = 1; next }
            b && /^[^[:space:]#]/ { exit }
            b && /^[[:space:]]*enabled:[[:space:]]*false[[:space:]]*$/ { f = 1 }
            END { exit !f }' "$yakos_yml" 2>/dev/null; then
        rm -f "$blocked_marker" 2>/dev/null || true
        ho_log "plan-quality-gate" "REPORT" "pass" \
            "plan_quality.enabled=false; .plan-blocked marker cleared" "{}" 2>/dev/null || true
        _pqg_pass
    fi
fi

if [ -n "$plan_id_from_marker" ]; then
    override_hint="  yakos plan score override $plan_id_from_marker --reason \"reviewed\""
else
    override_hint="  yakos plan score override <plan_id> --reason \"reviewed\""
fi

block_extra="$(jq -nc \
    --arg pid "${plan_id_from_marker:-unknown}" \
    --arg reason "${reason_text:-plan scored below threshold}" \
    '{plan_id: $pid, reason: $reason}' 2>/dev/null || echo '{}')"
ho_log "plan-quality-gate" "BLOCK" "block" \
    "plan quality gate: .plan-blocked marker present" "$block_extra" 2>/dev/null || true

ho_block "plan-quality-gate" \
"Plan quality gate: plan scored below threshold — dispatch blocked.
Plan ID: ${plan_id_from_marker:-unknown}
Reason:  ${reason_text:-plan scored below threshold}
Override with:
${override_hint}
Or review the score:  yakos plan score show
Or view history:      yakos plan score history"
