#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: peer.sh — multi-dev coordination CLI (Plan 1 / co-pilot mode).
#
# Subcommands at M1 (v0.27):
#   status                Show active peer sessions on this dev box for the
#                         current project. Reads coord/sessions/*.ndjson
#                         tails + coord/activity.ndjson last events.
#   log [--since <iso>]   Tail coord/activity.ndjson for the current project.
#
# Subcommands wired in later milestones:
#   M2: claim, release, deadlock
#   M3: propose-mode, respond-mode
#
# All subcommands no-op cleanly when multi-dev coord is not configured
# (the coord dir doesn't exist or isn't writable). yakOS works the same
# with or without multi-dev — that's a load-bearing guarantee.

set -eu

: "${YAKOS_LIB:?peer.sh: YAKOS_LIB must be set; run via 'yakos peer'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./paths.sh
. "$YAKOS_LIB/paths.sh"

usage() {
    cat <<'EOF'
yakos peer <subcommand> [args...]

Multi-developer coordination on a shared dev box (Plan 1 / co-pilot mode).
Reads / appends shared state at /var/lib/yakos/<project>/coord/.

Subcommands (v0.27 — M1 awareness only):
  status [<project>]              Active peer sessions for the project.
  log [--since <iso>] [<project>] Tail coord/activity.ndjson.

Setup:
  Each developer runs 'yakos init --multi-dev <name> --project <path>'.
  Coord dir must exist and be writable (group yakos-coord). See
  docs/co-pilot-mode.md for the dev-box admin recipe.

Examples:
  yakos peer status                  # auto-detect project from cwd
  yakos peer status myapp
  yakos peer log --since 2026-05-22T15:00:00Z myapp
EOF
}

# Project inference matches start.sh / memory.sh.
infer_project() {
    local cwd_real ac_root
    cwd_real="$(ct_realpath "$PWD")"
    ac_root="$HOME/agent-control"
    case "$cwd_real" in
        "$ac_root"/*)
            local rest="${cwd_real#$ac_root/}"
            printf '%s\n' "${rest%%/*}"
            return 0
            ;;
    esac
    if [ -d "$ac_root" ]; then
        for cd_path in "$ac_root"/*/.project-path; do
            [ -f "$cd_path" ] || continue
            local p pr
            p="$(head -1 "$cd_path")"
            pr="$(ct_realpath "$p" 2>/dev/null || true)"
            case "$cwd_real" in
                "$pr"|"$pr"/*)
                    basename -- "$(dirname -- "$cd_path")"
                    return 0
                    ;;
            esac
        done
    fi
    return 1
}

resolve_project() {
    local arg="${1:-}"
    if [ -n "$arg" ]; then
        printf '%s\n' "$arg"
        return 0
    fi
    local p
    p="$(infer_project || true)"
    if [ -z "$p" ]; then
        ct_die "peer: could not infer project from cwd; pass <name> explicitly"
    fi
    printf '%s\n' "$p"
}

coord_dir_for() {
    YAKOS_PROJECT_NAME="$1" yakos_coord_dir
}

require_coord() {
    local proj="$1" d
    d="$(coord_dir_for "$proj")"
    if [ ! -d "$d" ]; then
        cat >&2 <<EOF
peer: coord not configured for '$proj'.
peer: expected directory: $d
peer: run 'yakos init --multi-dev $proj --project <path>' to provision it.
peer: see docs/co-pilot-mode.md for the dev-box admin recipe (groups, perms).
EOF
        exit 64
    fi
    if [ ! -w "$d" ]; then
        cat >&2 <<EOF
peer: coord directory exists but is not writable by you ($USER):
peer:   $d
peer: ensure you are in the 'yakos-coord' group (or whoever owns the dir),
peer: then log out + back in (or 'newgrp yakos-coord') for the group
peer: membership to apply to your shell.
EOF
        exit 77
    fi
    printf '%s\n' "$d"
}

# ---- subcommand dispatch ----------------------------------------------------

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    status|log) ;;
    claim|release|deadlock)
        ct_die "peer: '$SUB' is a Plan 1 M2 subcommand (v0.28+); not available in this build"
        ;;
    propose-mode|respond-mode)
        ct_die "peer: '$SUB' is a Plan 1 M3 subcommand (v0.29+); not available in this build"
        ;;
    *) ct_die "peer: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- subcommand: status -----------------------------------------------------

if [ "$SUB" = "status" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    COORD="$(require_coord "$PROJECT")"
    SESSIONS_DIR="$COORD/sessions"

    echo "yakos peer status — project: $PROJECT"
    echo "coord dir: $COORD"
    echo

    if [ ! -d "$SESSIONS_DIR" ] || [ -z "$(ls -A "$SESSIONS_DIR" 2>/dev/null)" ]; then
        echo "  (no peer sessions on record)"
        echo
        echo "  Coord is configured but no session has emitted yet. Either you"
        echo "  haven't started a session since enabling --multi-dev, or no one"
        echo "  else has either. Start one: yakos start $PROJECT"
        exit 0
    fi

    # Each session file = one user@host-pid. List with most-recent event ts.
    printf '  %-32s %-22s %-12s %s\n' "SESSION" "LAST_EVENT" "EVENT_KIND" "AGENT"
    for f in "$SESSIONS_DIR"/*.ndjson; do
        [ -f "$f" ] || continue
        local_name="$(basename -- "$f" .ndjson)"
        last_event="$(tail -n 1 "$f" 2>/dev/null || true)"
        if [ -z "$last_event" ]; then
            printf '  %-32s %-22s %-12s %s\n' "$local_name" "(empty)" "-" "-"
            continue
        fi
        ts="$(printf '%s' "$last_event" | jq -r '.ts // "-"' 2>/dev/null || echo "-")"
        kind="$(printf '%s' "$last_event" | jq -r '.kind // "-"' 2>/dev/null || echo "-")"
        agent="$(printf '%s' "$last_event" | jq -r '.actor.agent // "-"' 2>/dev/null || echo "-")"
        printf '  %-32s %-22s %-12s %s\n' "$local_name" "$ts" "$kind" "$agent"
    done

    exit 0
fi

# ---- subcommand: log --------------------------------------------------------

if [ "$SUB" = "log" ]; then
    SINCE=""
    PROJECT=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --since)
                shift
                [ "$#" -gt 0 ] || ct_die "peer log: --since requires a value"
                SINCE="$1"
                ;;
            --since=*) SINCE="${1#--since=}" ;;
            -*) ct_die "peer log: unknown flag '$1'" ;;
            *)
                if [ -z "$PROJECT" ]; then PROJECT="$1"
                else ct_die "peer log: too many positional args"
                fi
                ;;
        esac
        shift
    done

    PROJECT="$(resolve_project "$PROJECT")"
    COORD="$(require_coord "$PROJECT")"
    LOG="$COORD/activity.ndjson"

    if [ ! -f "$LOG" ]; then
        echo "yakos peer log: no activity yet ($LOG missing)"
        exit 0
    fi

    if [ -n "$SINCE" ]; then
        # Filter by ts >= SINCE (lexical comparison works for ISO-8601 UTC).
        jq -c --arg s "$SINCE" 'select(.ts >= $s)' "$LOG"
    else
        # Default: last 50 events.
        tail -n 50 "$LOG"
    fi

    exit 0
fi

ct_die "peer: unknown subcommand '$SUB' (try --help)"
