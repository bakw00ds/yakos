#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: compact.sh — manual + threshold-configurable context compaction.
#
# Subcommands:
#   yakos compact now                   # invoke runtime's /compact
#   yakos compact threshold [N]         # show or set notice threshold
#   yakos compact history               # past compactions
#
# Reads:  ~/.yakos-state/settings.json
#         ~/.yakos-state/compact-log.ndjson
# Writes: ~/.yakos-state/settings.json (threshold set)
#         ~/.yakos-state/compact-log.ndjson (append)

set -eu

: "${YAKOS_LIB:?compact.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"

SETTINGS="$HOME/.yakos-state/settings.json"
COMPACT_LOG="$HOME/.yakos-state/compact-log.ndjson"

_log_compaction() {
    local kind="$1" pct="${2:-}" runtime="${3:-}"
    mkdir -p "$(dirname "$COMPACT_LOG")"
    local entry
    entry="$(jq -nc \
        --arg ts "$(ct_iso_now_z)" --arg k "$kind" \
        --arg p "$pct" --arg r "$runtime" --arg u "$(id -un)" \
        '{ts: $ts, kind: $k, pct: $p, runtime: $r, by_user: $u}')"
    printf '%s\n' "$entry" >> "$COMPACT_LOG"
}

cmd_now() {
    # Invoke the active runtime's /compact slash command.
    # TODO(M3.1): this requires the ability to send into a RUNNING
    # session. Three options:
    #   1. Detect a tmux session and send-keys "/compact\n"
    #   2. Detect a Claude Code IPC socket if exposed
    #   3. Print the command + ask the user to paste it
    # Start with option 3; smarter options as M3.1 iterates.
    cat <<'EOF'
yakos compact now — manual compaction.

Currently this prints the slash command for you to paste into the
active session. Future versions may auto-send via tmux integration.

For Claude Code:    /compact
For Codex:          /compact (verify in M3.1)
For agy:            /compact (verify in M3.1)

Recommendation: also run `yakos checkpoint now` first to snapshot
state if you want to be able to rewind from this point.
EOF
    _log_compaction "manual-recommend" "" "${YAKOS_RUNTIME:-claude}"
}

cmd_threshold() {
    if [ "$#" -eq 0 ]; then
        # Show current threshold.
        if [ -f "$SETTINGS" ] && command -v jq >/dev/null 2>&1; then
            jq -r '"notice = " + (.context_thresholds.notice // 75 | tostring) + "%, warning = " + (.context_thresholds.warning // 90 | tostring) + "%"' "$SETTINGS" 2>/dev/null
        else
            echo "notice = 75% (default), warning = 90% (default)"
        fi
        return 0
    fi

    # Set notice threshold to the given percentage.
    local new_notice="$1"
    case "$new_notice" in
        ''|*[!0-9]*) ct_die "threshold must be a number (1-99)" ;;
    esac
    [ "$new_notice" -gt 0 ] && [ "$new_notice" -lt 100 ] || ct_die "threshold must be 1-99"

    mkdir -p "$(dirname "$SETTINGS")"
    if [ -f "$SETTINGS" ]; then
        ct_json_merge "$SETTINGS" \
            ".context_thresholds = ((.context_thresholds // {}) + {notice: $new_notice})"
    else
        printf '{"context_thresholds": {"notice": %d}}\n' "$new_notice" > "$SETTINGS"
    fi
    ct_log "notice threshold set to $new_notice%"
}

cmd_history() {
    [ -f "$COMPACT_LOG" ] || { echo "(no compaction history)"; return 0; }
    jq -r '.ts + " " + .kind + " " + (.pct // "-") + " " + .runtime' "$COMPACT_LOG" 2>/dev/null | tail -50
}

case "${1:-help}" in
    now)        shift; cmd_now "$@" ;;
    threshold)  shift; cmd_threshold "$@" ;;
    history)    shift; cmd_history "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos compact — manage context compaction

  yakos compact now              # invoke runtime's /compact (currently prints; M3.1 will auto-send)
  yakos compact threshold [N]    # show or set notice threshold (default 75)
  yakos compact history          # past compactions
EOF
        ;;
    *) ct_die "yakos compact: unknown subcommand '$1'" ;;
esac
