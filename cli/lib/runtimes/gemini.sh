#!/usr/bin/env bash
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

_yk_rt_gemini_warn() {
    if [ -z "${YAKOS_GEMINI_DEPRECATION_WARNED:-}" ]; then
        ct_log "NOTE: --runtime gemini is deprecated."
        ct_log "      Gemini CLI stops serving requests 2026-06-18."
        ct_log "      Routing to 'agy' (Antigravity CLI; Gemini CLI successor)."
        ct_log "      Update .yakos.yml + agent frontmatter to 'agy' before 2026-09-01"
        ct_log "      (shim removal). See agy-adapter-plan.md migration recipe."
        export YAKOS_GEMINI_DEPRECATION_WARNED=1
    fi
}

# The 8-verb contract. Each verb delegates to agy.sh; the warn fires once
# per session on any invocation. id() stays "gemini" so existing configs
# continue to resolve correctly until operators migrate.

yk_rt_gemini_id()                  { printf 'gemini\n'; }
yk_rt_gemini_capabilities()        { yk_rt_agy_capabilities "$@"; }
yk_rt_gemini_check_cli()           { _yk_rt_gemini_warn; yk_rt_agy_check_cli "$@"; }
yk_rt_gemini_check_auth()          { _yk_rt_gemini_warn; yk_rt_agy_check_auth "$@"; }
yk_rt_gemini_materialize_agents()  { _yk_rt_gemini_warn; yk_rt_agy_materialize_agents "$@"; }
yk_rt_gemini_cleanup_agents()      { yk_rt_agy_cleanup_agents "$@"; }
yk_rt_gemini_launch()              { _yk_rt_gemini_warn; yk_rt_agy_launch "$@"; }
yk_rt_gemini_dispatch()            { _yk_rt_gemini_warn; yk_rt_agy_dispatch "$@"; }
