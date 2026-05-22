#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# task-complete-dispatch.sh — TaskCompleted hook (REPORT-ONLY in v0.1).
#
# Status: REPORT-ONLY (build-prompt §"REPORT-only fallback rule").
#
# Reason for the fallback:
#   - Routing depends on .agent_type being present in TaskCompleted stdin
#     JSON. Phase 0 Test 5 validated TaskCompleted hooks fire and can
#     block, but did NOT dump the TaskCompleted JSON shape — we don't
#     know whether .agent_type is reliably present.
#   - Without that confirmation, dispatching to backend-validate vs
#     frontend-validate based on a guessed-at field would silently route
#     wrong. v0.1 logs the routing decision it WOULD make and exits 0.
#
# Per-domain validators in scripts/hooks/per-domain/ are themselves
# functional — they can be invoked manually or by a future BLOCKING
# version of this dispatcher. v0.1 just records the routing intent.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

agent="$(hi_sender_role)"

# Decide which per-domain validator the agent maps to. If unknown, no-op.
domain=""
case "$agent" in
    go-api|backend|api)              domain="backend" ;;
    flutter-ui|mobile|ios)           domain="mobile" ;;
    nextjs|web|frontend)             domain="frontend" ;;
    db-migrations|db|database)       domain="db-migration" ;;
    *)                                domain="" ;;
esac

would_run=""
[ -n "$domain" ] && would_run="$HOOK_DIR/per-domain/${domain}-validate.sh"

# Bypass-aware (even though we don't actually run anything in v0.1).
bypass_active="false"
if [ -n "$domain" ] && ho_check_bypass "task-complete-dispatch" "$domain"; then
    bypass_active="true"
fi

extra="$(jq -nc \
    --arg mode "report-only" \
    --arg agent "$agent" \
    --arg domain "$domain" \
    --arg would_run "$would_run" \
    --arg bypass "$bypass_active" \
    '{mode: $mode, agent_type: $agent, routed_domain: $domain, would_run: $would_run, bypass_active: $bypass}')"

ho_log "task-complete-dispatch" "REPORT" "pass" "report-only in v0.1 (UNCLEAR — see hook source)" "$extra"
exit 0
