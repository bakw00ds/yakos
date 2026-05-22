#!/usr/bin/env bash
# Purpose: cycle-counter.sh — count user prompts; emit retro signal at every 10th.
#
# Hook context: UserPromptSubmit. Telemetry hook (always exit 0).
# Reads:  <work>/current/.cycle-count
#         ~/.yakos-state/settings.json (optional, for cycle_length override)
# Writes: <work>/current/.cycle-count
#         <work>/current/.retro-due (marker; created at every 10th cycle)
#         <work>/current/logs/cycle-counter.ndjson
#
# At cycle 10, 20, 30, ...:
# - touch the .retro-due marker file
# - emit a NOTE to stderr (visible to the lead's context)
# - log REPORT-severity NDJSON entry
#
# The lead's persona (rule:retrospective-discipline) detects the
# marker on its next action and dispatches the librarian agent.
# After retrospective completes, the lead removes the marker.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
# shellcheck source=lib/hook-input.sh
. "$HOOK_DIR/lib/hook-input.sh"
# shellcheck source=lib/hook-output.sh
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# Cycle length (default 10, operator-tunable via ~/.yakos-state/settings.json)
CYCLE_LENGTH=10
settings_file="$HOME/.yakos-state/settings.json"
if [ -f "$settings_file" ] && command -v jq >/dev/null 2>&1; then
    n="$(jq -r '.retro.cycle_length // empty' "$settings_file" 2>/dev/null)"
    case "$n" in
        ''|*[!0-9]*) : ;;            # invalid / empty — keep default
        *) CYCLE_LENGTH="$n" ;;
    esac
fi

current_dir="$(yakos_current_dir)"
counter_file="$current_dir/.cycle-count"
marker_file="$current_dir/.retro-due"

# Defensive: if paths.sh returns empty (TTY init, missing project), no-op.
[ -n "$current_dir" ] || exit 0
[ -d "$current_dir" ] || exit 0

# Increment counter atomically (read-modify-write under flock if available).
count=0
[ -f "$counter_file" ] && count="$(cat "$counter_file" 2>/dev/null || echo 0)"
case "$count" in
    ''|*[!0-9]*) count=0 ;;          # corrupted — reset
esac
count=$((count + 1))
printf '%d\n' "$count" > "$counter_file"

# Is auto-retro enabled? (operator can disable via `yakos retro disable`)
auto_retro=true
if [ -f "$settings_file" ] && command -v jq >/dev/null 2>&1; then
    val="$(jq -r '.retro.auto_dispatch // true' "$settings_file" 2>/dev/null)"
    [ "$val" = "false" ] && auto_retro=false
fi

retro_due=false
if [ "$auto_retro" = "true" ] && [ "$((count % CYCLE_LENGTH))" = "0" ]; then
    touch "$marker_file"
    retro_due=true
    # NOTE goes to stderr; the lead's runtime captures it via the
    # UserPromptSubmit hook output channel.
    ct_log "NOTE: cycle $count — retrospective due. Lead should dispatch 'librarian' agent. (See rule:retrospective-discipline.)"
fi

ho_log "cycle-counter" REPORT "counted" \
    "cycle=$count cycle_length=$CYCLE_LENGTH retro_due=$retro_due" \
    "{\"cycle\": $count, \"cycle_length\": $CYCLE_LENGTH, \"retro_due\": $retro_due, \"auto_retro\": $auto_retro}"

exit 0
