#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: gemini.sh — DEPRECATION SHIM. Routes all --runtime gemini
# invocations through agy.sh and emits a one-time deprecation NOTE.
#
# Gemini CLI stops serving requests 2026-06-18 (Google AI Pro/Ultra/free
# tier). agy is the successor.
#
# Removal date: 2026-09-01 (3 months after Gemini CLI shutdown). Until
# then, existing user configs with `runtime: gemini` continue to work
# without code changes; the underlying calls hit `agy`.
#
# On/after the removal date, every verb that would talk to agy (check_cli,
# check_auth, materialize_agents, launch, dispatch) instead logs the
# migration message and returns non-zero — it does NOT exit/ct_die. This
# adapter is sourced into the caller's shell, not exec'd in a subshell, so
# an unconditional `exit` here would kill the *caller* (yakos auth status,
# the runtime-fixtures test runner, etc.), not just this verb. Returning
# non-zero lets callers that iterate "all known runtimes" treat gemini as
# unavailable/removed and continue, the same way they already handle any
# other CLI that isn't installed.
#
# YAKOS_GEMINI_SHIM_FORCE=1 is the operator override for the emergency case
# where migration is mid-flight: it lets verbs proceed through to agy past
# the removal date. The override is logged so the audit trail shows the
# shim was used past EOL.
#
# To migrate explicitly:
#   - .yakos.yml: set `default-runtime: agy`
#   - agent frontmatter: change `runtime: gemini` to `runtime: agy`
#   - Project hook configs (`yakos hooks install gemini`):
#     re-run as `yakos hooks install agy` (when supported)
#
# See agy-adapter-plan.md + v0.21.0.0 CHANGELOG entry for the full
# migration recipe.

set -eu

: "${YAKOS_LIB:?gemini.sh: YAKOS_LIB must be set}"
# shellcheck source=./agy.sh
. "$YAKOS_LIB/runtimes/agy.sh"

_YK_RT_GEMINI_REMOVAL_DATE="2026-09-01"

_yk_rt_gemini_past_removal() {
    # Returns 0 if today is on/after the removal date.
    local today
    today="$(date -u +%Y-%m-%d)"
    [ "$today" \> "$_YK_RT_GEMINI_REMOVAL_DATE" ] || \
        [ "$today" = "$_YK_RT_GEMINI_REMOVAL_DATE" ]
}

# _yk_rt_gemini_warn
#   Past the removal date: without YAKOS_GEMINI_SHIM_FORCE=1, logs the
#   migration message (once per session) and returns 1 — callers must NOT
#   go on to call agy. With the override set, logs a one-time "continuing
#   under override" note and returns 0.
#   Before the removal date: logs the one-time deprecation NOTE (if not
#   already warned) and returns 0.
#   IMPORTANT: never calls ct_die/exit — this function is sourced into the
#   caller's shell, so exiting here would kill the caller, not just this
#   verb. Every call site below MUST check the return value
#   (`_yk_rt_gemini_warn || return 1`), not just call it as a bare
#   statement, so this stays safe even outside an if/&&/|| context.
_yk_rt_gemini_warn() {
    if _yk_rt_gemini_past_removal; then
        if [ -n "${YAKOS_GEMINI_SHIM_FORCE:-}" ]; then
            if [ -z "${YAKOS_GEMINI_FORCE_WARNED:-}" ]; then
                ct_log "NOTE: --runtime gemini is past its removal date ($_YK_RT_GEMINI_REMOVAL_DATE)."
                ct_log "      Continuing under YAKOS_GEMINI_SHIM_FORCE=1 override — not recommended;"
                ct_log "      the shim will be deleted in a future release."
                export YAKOS_GEMINI_FORCE_WARNED=1
            fi
            return 0
        fi
        if [ -z "${YAKOS_GEMINI_REMOVAL_WARNED:-}" ]; then
            ct_log "ERROR: --runtime gemini was scheduled for removal on $_YK_RT_GEMINI_REMOVAL_DATE."
            ct_log "       Gemini CLI stopped serving requests on 2026-06-18; agy is the successor."
            ct_log "       Migration steps:"
            ct_log "         1. Update .yakos.yml: set 'default-runtime: agy'"
            ct_log "         2. Update each agent's frontmatter: 'runtime: gemini' → 'runtime: agy'"
            ct_log "         3. Re-run hook install: 'yakos hooks install agy'"
            ct_log "       Override (NOT recommended; shim will be deleted in a future release):"
            ct_log "         export YAKOS_GEMINI_SHIM_FORCE=1"
            export YAKOS_GEMINI_REMOVAL_WARNED=1
        fi
        return 1
    elif [ -z "${YAKOS_GEMINI_DEPRECATION_WARNED:-}" ]; then
        ct_log "NOTE: --runtime gemini is deprecated."
        ct_log "      Gemini CLI stops serving requests 2026-06-18."
        ct_log "      Routing to 'agy' (Antigravity CLI; Gemini CLI successor)."
        ct_log "      Update .yakos.yml + agent frontmatter to 'agy' before $_YK_RT_GEMINI_REMOVAL_DATE"
        ct_log "      (shim removal). See agy-adapter-plan.md migration recipe."
        export YAKOS_GEMINI_DEPRECATION_WARNED=1
    fi
    return 0
}

# The 8-verb contract. Each verb delegates to agy.sh; the warn fires once
# per session on any invocation. id() stays "gemini" so existing configs
# continue to resolve correctly until operators migrate. Past the removal
# date without the force override, the warn-guarded verbs return 1 instead
# of reaching agy at all.

yk_rt_gemini_id()                  { printf 'gemini\n'; }
yk_rt_gemini_capabilities()        { yk_rt_agy_capabilities "$@"; }
yk_rt_gemini_check_cli()           { _yk_rt_gemini_warn || return 1; yk_rt_agy_check_cli "$@"; }
yk_rt_gemini_check_auth()          { _yk_rt_gemini_warn || return 1; yk_rt_agy_check_auth "$@"; }
yk_rt_gemini_materialize_agents()  { _yk_rt_gemini_warn || return 1; yk_rt_agy_materialize_agents "$@"; }
yk_rt_gemini_cleanup_agents()      { yk_rt_agy_cleanup_agents "$@"; }
yk_rt_gemini_launch()              { _yk_rt_gemini_warn || return 1; yk_rt_agy_launch "$@"; }
yk_rt_gemini_dispatch()            { _yk_rt_gemini_warn || return 1; yk_rt_agy_dispatch "$@"; }
