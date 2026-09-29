#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: cycle-counter.sh — count user prompts; emit retro signal at every 10th.
#
# Hook context: UserPromptSubmit. Telemetry hook (always exit 0).
# Reads:  <work>/current/.cycle-count
#         ~/.yakos-state/settings.json (optional; .retro.cycle_length, integer
#                                      1..100000, else WARN + default 10)
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
# compat.sh is needed for ct_log; resolve via YAKOS_ROOT.
# This file lives at lib/hooks/legacy/; when invoked directly (not via the
# lib/hooks/<name>.sh symlink) HOOK_DIR resolves to lib/hooks/legacy/ and
# needs three levels of ".." to reach the project root.  When invoked via
# the symlink, HOOK_DIR resolves to lib/hooks/ and two levels suffice.
if [ -z "${YAKOS_ROOT:-}" ]; then
    case "$(basename -- "$HOOK_DIR")" in
        legacy) YAKOS_ROOT="$(cd "$HOOK_DIR/../../.." && pwd -P)" ;;
        *)       YAKOS_ROOT="$(cd "$HOOK_DIR/../.." && pwd -P)" ;;
    esac
fi
YAKOS_LIB="${YAKOS_LIB:-$YAKOS_ROOT/cli/lib}"
if [ -f "$YAKOS_LIB/compat.sh" ]; then
    # shellcheck source=../../cli/lib/compat.sh
    . "$YAKOS_LIB/compat.sh"
else
    # Minimal inline fallback so the hook can still emit diagnostics
    ct_log() { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2; }
fi

hi_init

# Cycle length (default 10, operator-tunable via ~/.yakos-state/settings.json).
#
# K-106: the value feeds `count % CYCLE_LENGTH`, so it must be an integer in
# 1..CYCLE_LENGTH_MAX. Anything else (0, negative, non-integer, empty string,
# boolean, absurdly large like 1e9) falls back to the default with ONE WARN
# naming the value; null/absent is the normal "not set" case and is silent.
# The Go port (cyclecounter.settingsCycleLength) applies the same rules.
CYCLE_LENGTH_DEFAULT=10
CYCLE_LENGTH_MAX=100000
CYCLE_LENGTH=$CYCLE_LENGTH_DEFAULT
settings_file="$HOME/.yakos-state/settings.json"
if [ -f "$settings_file" ] && command -v jq >/dev/null 2>&1; then
    # `|| raw=absent` guards against malformed/wrong-shape settings.json: under
    # `set -eu` a bare assignment aborts the whole script on jq's non-zero
    # exit (parse error, or type error e.g. `.retro` not an object), which
    # would silently disable cycle counting (and retro auto-dispatch) for
    # the rest of the session since the file doesn't change between calls.
    # Falling back to "absent" degrades to the default cadence, matching the Go
    # port's loadSettings/settingsCycleLength graceful-degradation.
    # Numbers are re-rendered via `. + 0` because jq 1.7 preserves the source
    # literal ("1E+9") while the Go side prints 1000000000; the WARN text must
    # match across implementations.
    raw="$(jq -r '.retro.cycle_length | if . == null then "absent" elif type == "number" then "v " + (. + 0 | tostring) else "v " + tostring end' "$settings_file" 2>/dev/null)" || raw="absent"
    case "$raw" in
        "v "*)
            n="${raw#v }"
            n_ok=0
            case "$n" in
                ''|*[!0-9]*) : ;;
                *)
                    # <=6 digits keeps the arithmetic far from overflow; 10#
                    # forces base 10 so "010"/"08" are not octal.
                    if [ "${#n}" -le 6 ]; then
                        v=$((10#$n))
                        if [ "$v" -ge 1 ] && [ "$v" -le "$CYCLE_LENGTH_MAX" ]; then
                            CYCLE_LENGTH="$v"; n_ok=1
                        fi
                    fi
                    ;;
            esac
            if [ "$n_ok" = "0" ]; then
                shown="$(printf '%s' "$n" | tr '\n' ' ')"
                [ -n "$shown" ] || shown='""'
                shown="${shown:0:40}"
                ct_log "WARN: ignoring invalid retro.cycle_length $shown (need an integer 1..$CYCLE_LENGTH_MAX); using default $CYCLE_LENGTH_DEFAULT"
            fi
            ;;
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
    # Same crash guard as the cycle_length read above; fall back to "true"
    # so a malformed/wrong-shape settings.json degrades to auto_retro
    # staying at its default (true), matching the Go port.
    #
    # K-89: do NOT use `// true` here. jq's `//` treats boolean false as
    # null, so an explicit `false` (what `yakos retro disable` writes) fell
    # through to true and auto-dispatch could never be disabled. Only a
    # null/absent value defaults to true; anything else is taken as-is
    # (boolean false and the string "false" both render as `false`).
    val="$(jq -r 'if .retro.auto_dispatch == null then true else .retro.auto_dispatch end' "$settings_file" 2>/dev/null)" || val="true"
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
