#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: retro.sh — manage 10-cycle retrospectives (Plan 3 M1 / Capability C).
#
# The cycle-counter hook (lib/hooks/cycle-counter.sh) increments a counter
# on every UserPromptSubmit; at every 10th cycle it drops a .retro-due
# marker. The lead's rule:retrospective-discipline tells the lead to
# dispatch the `librarian` agent in response.
#
# This CLI gives the operator manual control over the cadence.
#
# Subcommands:
#   yakos retro now                  # write .retro-due marker now (manual trigger)
#   yakos retro last                 # show last retro's outputs from scratchpad
#   yakos retro history              # cadence stats from cycle-counter logs
#   yakos retro disable              # disable auto-trigger (counter still increments)
#   yakos retro enable               # re-enable auto-trigger
#   yakos retro status               # current state (enabled? cycle count? next trigger?)
#
# Reads:  ~/.yakos-state/settings.json (.retro.auto_dispatch, .retro.cycle_length)
#         <work>/current/.cycle-count
#         <work>/current/{lessons,mistakes,skill-candidates,drift-report,soul-proposed-edits}.md
# Writes: ~/.yakos-state/settings.json (toggle)
#         <work>/current/.retro-due (manual trigger)

set -eu

: "${YAKOS_LIB:?retro.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./paths.sh
. "$YAKOS_LIB/paths.sh"

SETTINGS="$HOME/.yakos-state/settings.json"

_retro_current_dir() {
    # Resolve the active session's work/current/. Returns empty if no active project.
    local slug ac_root="$HOME/agent-control"
    if [ -n "${YAKOS_PROJECT_NAME:-}" ]; then
        slug="$YAKOS_PROJECT_NAME"
    else
        local cwd_real
        cwd_real="$(cd "$PWD" 2>/dev/null && pwd -P)"
        case "$cwd_real" in
            "$ac_root"/*)
                local rest="${cwd_real#$ac_root/}"
                slug="${rest%%/*}"
                ;;
            *)
                # Walk .project-path files
                local cd_path proj proj_real
                for cd_path in "$ac_root"/*/.project-path; do
                    [ -f "$cd_path" ] || continue
                    proj="$(head -1 "$cd_path" 2>/dev/null)"
                    proj_real="$(cd "$proj" 2>/dev/null && pwd -P || true)"
                    [ -n "$proj_real" ] || continue
                    case "$cwd_real" in
                        "$proj_real"|"$proj_real"/*)
                            slug="$(basename -- "$(dirname -- "$cd_path")")"
                            break
                            ;;
                    esac
                done
                ;;
        esac
    fi
    [ -n "${slug:-}" ] || { echo ""; return; }
    printf '%s/%s/work/current\n' "$ac_root" "$slug"
}

_retro_settings_get() {
    local jq_path="$1" default="$2"
    if [ -f "$SETTINGS" ] && command -v jq >/dev/null 2>&1; then
        local v
        v="$(jq -r "$jq_path // empty" "$SETTINGS" 2>/dev/null)"
        [ -n "$v" ] && [ "$v" != "null" ] && { printf '%s\n' "$v"; return; }
    fi
    printf '%s\n' "$default"
}

_retro_settings_set() {
    local jq_path="$1" value="$2"
    mkdir -p "$(dirname "$SETTINGS")"
    if [ -f "$SETTINGS" ] && command -v jq >/dev/null 2>&1; then
        ct_json_merge "$SETTINGS" "$jq_path = $value"
    else
        # Fresh settings file.
        echo "{\"retro\": {}}" > "$SETTINGS"
        ct_json_merge "$SETTINGS" "$jq_path = $value"
    fi
}

cmd_now() {
    local cur
    cur="$(_retro_current_dir)"
    [ -n "$cur" ] && [ -d "$cur" ] || ct_die "retro: no active session detected"
    touch "$cur/.retro-due"
    ct_log "retro: marker written at $cur/.retro-due"
    ct_log "      lead should detect the marker and dispatch 'librarian' on next action"
}

cmd_last() {
    local cur
    cur="$(_retro_current_dir)"
    [ -n "$cur" ] && [ -d "$cur" ] || ct_die "retro: no active session detected"
    for f in lessons.md mistakes.md skill-candidates.md drift-report.md soul-proposed-edits.md; do
        local path="$cur/$f"
        if [ -f "$path" ]; then
            echo "=== $f ==="
            tail -40 "$path"
            echo ""
        else
            echo "=== $f === (none yet)"
            echo ""
        fi
    done
}

cmd_history() {
    local cur
    cur="$(_retro_current_dir)"
    [ -n "$cur" ] && [ -d "$cur" ] || ct_die "retro: no active session detected"
    local log="$cur/logs/cycle-counter.ndjson"
    [ -f "$log" ] || { echo "(no cycle-counter log yet)"; return 0; }

    if command -v jq >/dev/null 2>&1; then
        local total retro_count
        total="$(wc -l < "$log" | tr -d ' ')"
        retro_count="$(jq -r 'select(.retro_due == true) | .cycle' "$log" 2>/dev/null | wc -l | tr -d ' ')"
        local current_cycle next_retro cycle_length
        current_cycle="$(cat "$cur/.cycle-count" 2>/dev/null || echo 0)"
        cycle_length="$(_retro_settings_get .retro.cycle_length 10)"
        next_retro=$(( ((current_cycle / cycle_length) + 1) * cycle_length ))
        cat <<EOF
Retro cadence stats (this session):
  Current cycle:    $current_cycle
  Cycle length:     $cycle_length
  Next retro at:    cycle $next_retro
  Retros triggered: $retro_count
  Total prompts:    $total
EOF
    else
        wc -l "$log"
    fi
}

cmd_disable() {
    _retro_settings_set ".retro = ((.retro // {}) + {auto_dispatch: false})"
    ct_log "retro: auto-dispatch DISABLED (cycle counter still increments)"
}

cmd_enable() {
    _retro_settings_set ".retro = ((.retro // {}) + {auto_dispatch: true})"
    ct_log "retro: auto-dispatch ENABLED"
}

cmd_status() {
    local auto cycle_length cur current_cycle
    auto="$(_retro_settings_get .retro.auto_dispatch true)"
    cycle_length="$(_retro_settings_get .retro.cycle_length 10)"
    cur="$(_retro_current_dir)"
    if [ -n "$cur" ] && [ -d "$cur" ]; then
        current_cycle="$(cat "$cur/.cycle-count" 2>/dev/null || echo 0)"
        cat <<EOF
Retro status:
  Active project session: $(basename "$(dirname "$(dirname "$cur")")")
  Auto-dispatch:          $auto
  Cycle length:           $cycle_length prompts
  Current cycle:          $current_cycle
  .retro-due marker:      $([ -f "$cur/.retro-due" ] && echo "PRESENT (lead will dispatch librarian)" || echo "absent")
EOF
    else
        cat <<EOF
Retro status:
  Active project session: (none detected)
  Auto-dispatch:          $auto
  Cycle length:           $cycle_length prompts
EOF
    fi
}

case "${1:-help}" in
    now)      shift; cmd_now "$@" ;;
    last)     shift; cmd_last "$@" ;;
    history)  shift; cmd_history "$@" ;;
    disable)  shift; cmd_disable "$@" ;;
    enable)   shift; cmd_enable "$@" ;;
    status)   shift; cmd_status "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos retro — manage 10-cycle retrospectives

  yakos retro now           # manual trigger; writes .retro-due marker
  yakos retro last          # show last retro's outputs from scratchpad
  yakos retro history       # cadence stats from cycle-counter logs
  yakos retro status        # current state
  yakos retro disable       # disable auto-trigger
  yakos retro enable        # re-enable auto-trigger

The 10-cycle cadence is enforced by lib/hooks/cycle-counter.sh
(UserPromptSubmit hook). Lead behavior on the marker is governed
by lib/rules/retrospective-discipline.md.
EOF
        ;;
    *) ct_die "yakos retro: unknown subcommand '$1' (try 'yakos retro help')" ;;
esac
