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

Subcommands:
  status [<project>]              Active peer sessions for the project. (M1)
  log [--since <iso>] [<project>] Tail coord/activity.ndjson. (M1)
  claim <file> [<project>]        Manually claim a file (operator override). (M2)
  release <file> [<project>]      Release this session's claim on a file. (M2)
  claims [<project>]              List all active claims for the project. (M2)
  deadlock [<project>]            Compute wait-for graph from activity log. (M2)
  propose-mode --mode <m> --targets <glob>... [--reason <t>] [--timeout <secs>] [<project>]
                                  Propose parallel/serialize/defer for contended
                                  targets; wait synchronously up to 60s (default)
                                  for peer's mode_response. (M3)
  respond-mode --to <proposal-id> --ack|--reject [--reason <t>] [<project>]
                                  Respond to a peer's mode proposal. (M3)

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
    status|log|claim|release|claims|deadlock|propose-mode|respond-mode) ;;
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

# ---- subcommand: claims (list active claims) -------------------------------

if [ "$SUB" = "claims" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    COORD="$(require_coord "$PROJECT")"
    CLAIMS_FILE="$COORD/active-claims.json"
    if [ ! -f "$CLAIMS_FILE" ]; then
        echo "yakos peer claims: no active-claims.json yet (no edits posted)"
        exit 0
    fi
    echo "yakos peer claims — project: $PROJECT"
    echo "generated_at: $(jq -r '.generated_at // "-"' "$CLAIMS_FILE" 2>/dev/null)"
    echo
    printf '  %-50s %-10s %-22s %s\n' "PATH" "STATUS" "EXPIRES" "OWNER"
    jq -r '.claims[]? |
        .path as $p |
        .owners[0] as $o |
        "  \($p) \($o.status) \($o.expires_at) \($o.user)@\($o.host) (agent: \($o.agent // "-"))"' \
        "$CLAIMS_FILE" 2>/dev/null || true
    exit 0
fi

# ---- subcommand: claim (manual claim — operator override) ------------------

if [ "$SUB" = "claim" ]; then
    [ "$#" -ge 1 ] || ct_die "peer claim: <file> required"
    FILE="$1"
    PROJECT="$(resolve_project "${2:-}")"
    COORD="$(require_coord "$PROJECT")"

    me_user="${USER:-$(id -un)}"
    me_host="${HOSTNAME:-$(hostname -s)}"
    me_pid="${YAKOS_SESSION_PID:-$$}"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    exp="$(date -u -j -v '+1800S' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
        || date -u -d '+1800 seconds' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null)"

    event="$(jq -nc \
        --arg ts "$ts" \
        --arg user "$me_user" \
        --arg host "$me_host" \
        --argjson pid "$me_pid" \
        --arg path "$FILE" \
        --arg exp "$exp" \
        '{ts: $ts, kind: "claim_confirmed",
          actor: {user: $user, host: $host, pid: $pid,
                  session_id: "manual", agent: "operator"},
          detail: {path: $path, ttl_seconds: 1800, expires_at: $exp,
                   source: "yakos peer claim"}}')"
    printf '%s\n' "$event" >> "$COORD/activity.ndjson"
    mkdir -p "$COORD/sessions"
    printf '%s\n' "$event" >> "$COORD/sessions/${me_user}@${me_host}-${me_pid}.ndjson"
    echo "claimed: $FILE for ${me_user}@${me_host} (30min TTL)"
    echo "view with: yakos peer claims $PROJECT"
    exit 0
fi

# ---- subcommand: release ---------------------------------------------------

if [ "$SUB" = "release" ]; then
    [ "$#" -ge 1 ] || ct_die "peer release: <file> required"
    FILE="$1"
    PROJECT="$(resolve_project "${2:-}")"
    COORD="$(require_coord "$PROJECT")"

    me_user="${USER:-$(id -un)}"
    me_host="${HOSTNAME:-$(hostname -s)}"
    me_pid="${YAKOS_SESSION_PID:-$$}"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    event="$(jq -nc \
        --arg ts "$ts" \
        --arg user "$me_user" \
        --arg host "$me_host" \
        --argjson pid "$me_pid" \
        --arg path "$FILE" \
        '{ts: $ts, kind: "claim_released",
          actor: {user: $user, host: $host, pid: $pid,
                  session_id: "manual", agent: "operator"},
          detail: {path: $path, source: "yakos peer release"}}')"
    printf '%s\n' "$event" >> "$COORD/activity.ndjson"
    mkdir -p "$COORD/sessions"
    printf '%s\n' "$event" >> "$COORD/sessions/${me_user}@${me_host}-${me_pid}.ndjson"
    echo "released: $FILE (claim removed from active-claims.json on next rebuild)"
    exit 0
fi

# ---- subcommand: deadlock --------------------------------------------------

if [ "$SUB" = "deadlock" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    COORD="$(require_coord "$PROJECT")"
    CLAIMS_FILE="$COORD/active-claims.json"
    ACTIVITY_LOG="$COORD/activity.ndjson"

    if [ ! -f "$CLAIMS_FILE" ] || [ ! -f "$ACTIVITY_LOG" ]; then
        echo "yakos peer deadlock: no claims or activity log to analyze"
        exit 0
    fi

    echo "yakos peer deadlock — project: $PROJECT"
    echo
    # Wait-for: any session that posted claim_intent on a path another
    # session currently holds (per active-claims.json).
    blockers="$(jq -r --slurpfile claims "$CLAIMS_FILE" '
        select(.kind == "claim_intent") |
        . as $intent |
        ($claims[0].claims[]? |
            select(.path == $intent.detail.path) |
            .owners[0] as $owner |
            select($owner.user != $intent.actor.user
                   or $owner.host != $intent.actor.host
                   or $owner.pid != $intent.actor.pid) |
            "  \($intent.actor.user)@\($intent.actor.host) wants \($intent.detail.path) — held by \($owner.user)@\($owner.host)"
        )' "$ACTIVITY_LOG" 2>/dev/null | sort -u)"
    if [ -z "$blockers" ]; then
        echo "  (no current wait-for edges; no deadlock candidates)"
    else
        echo "Wait-for edges (current):"
        echo "$blockers"
        echo
        echo "Cycle detection deferred to v0.29; surface to humans for now."
    fi
    exit 0
fi

# ---- subcommand: propose-mode (M3) -----------------------------------------

if [ "$SUB" = "propose-mode" ]; then
    MODE=""
    TARGETS=()
    REASON=""
    TIMEOUT=60
    PROJECT=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --mode) shift; MODE="${1:-}" ;;
            --mode=*) MODE="${1#--mode=}" ;;
            --targets) shift; TARGETS+=("${1:-}") ;;
            --targets=*) TARGETS+=("${1#--targets=}") ;;
            --reason) shift; REASON="${1:-}" ;;
            --reason=*) REASON="${1#--reason=}" ;;
            --timeout) shift; TIMEOUT="${1:-60}" ;;
            --timeout=*) TIMEOUT="${1#--timeout=}" ;;
            -*) ct_die "peer propose-mode: unknown flag '$1'" ;;
            *)
                if [ -z "$PROJECT" ]; then PROJECT="$1"
                else ct_die "peer propose-mode: too many positional args"
                fi
                ;;
        esac
        shift
    done

    [ -n "$MODE" ] || ct_die "peer propose-mode: --mode required (parallel|serialize|defer)"
    case "$MODE" in
        parallel|serialize|defer) ;;
        *) ct_die "peer propose-mode: --mode must be parallel|serialize|defer (got '$MODE')" ;;
    esac
    [ "${#TARGETS[@]}" -gt 0 ] || ct_die "peer propose-mode: at least one --targets required"

    PROJECT="$(resolve_project "$PROJECT")"
    COORD="$(require_coord "$PROJECT")"

    me_user="${USER:-$(id -un)}"
    me_host="${HOSTNAME:-$(hostname -s)}"
    me_pid="${YAKOS_SESSION_PID:-$$}"
    proposal_id="$(date -u +%s)-$me_pid-$RANDOM"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    targets_json="$(printf '%s\n' "${TARGETS[@]}" | jq -R . | jq -sc .)"

    event="$(jq -nc \
        --arg ts "$ts" \
        --arg user "$me_user" \
        --arg host "$me_host" \
        --argjson pid "$me_pid" \
        --arg pid_s "$proposal_id" \
        --arg mode "$MODE" \
        --argjson targets "$targets_json" \
        --arg reason "$REASON" \
        '{ts: $ts, kind: "mode_proposal",
          actor: {user: $user, host: $host, pid: $pid,
                  session_id: "operator", agent: "lead"},
          detail: {proposal_id: $pid_s, proposed_mode: $mode,
                   targets: $targets, reason: $reason}}')"
    printf '%s\n' "$event" >> "$COORD/activity.ndjson"
    mkdir -p "$COORD/sessions"
    printf '%s\n' "$event" >> "$COORD/sessions/${me_user}@${me_host}-${me_pid}.ndjson"

    echo "proposed mode '$MODE' on targets [${TARGETS[*]}] — proposal_id=$proposal_id"
    echo "waiting up to ${TIMEOUT}s for peer mode_response..."

    deadline=$(($(date -u +%s) + TIMEOUT))
    response=""
    while [ "$(date -u +%s)" -lt "$deadline" ]; do
        response="$(jq -c --arg pid "$proposal_id" \
            'select(.kind == "mode_response" and .detail.proposal_id == $pid)' \
            "$COORD/activity.ndjson" 2>/dev/null | tail -n 1)"
        [ -n "$response" ] && break
        sleep 1
    done

    if [ -z "$response" ]; then
        echo "TIMEOUT: no peer response within ${TIMEOUT}s. Defaulting to serialize."
        ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        timeout_event="$(jq -nc \
            --arg ts "$ts" \
            --arg user "$me_user" \
            --arg host "$me_host" \
            --argjson pid "$me_pid" \
            --arg pid_s "$proposal_id" \
            '{ts: $ts, kind: "mode_response",
              actor: {user: $user, host: $host, pid: $pid,
                      session_id: "operator", agent: "lead"},
              detail: {proposal_id: $pid_s, response: "default_serialize",
                       reason: "peer did not respond within timeout",
                       timeout: true}}')"
        printf '%s\n' "$timeout_event" >> "$COORD/activity.ndjson"
        printf '%s\n' "$timeout_event" >> "$COORD/sessions/${me_user}@${me_host}-${me_pid}.ndjson"
        exit 0
    fi

    peer_response="$(printf '%s' "$response" | jq -r '.detail.response')"
    peer_reason="$(printf '%s' "$response" | jq -r '.detail.reason // "(no reason)"')"
    peer_who="$(printf '%s' "$response" | jq -r '.actor.user + "@" + .actor.host')"
    echo "RESPONSE from $peer_who: $peer_response"
    echo "reason: $peer_reason"

    if [ "$peer_response" = "reject" ]; then
        echo "STOP: peer rejected. Coordinate before retrying; don't silently override."
        exit 1
    fi
    exit 0
fi

# ---- subcommand: respond-mode (M3) -----------------------------------------

if [ "$SUB" = "respond-mode" ]; then
    TO=""
    ACK=0
    REJECT=0
    REASON=""
    PROJECT=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --to) shift; TO="${1:-}" ;;
            --to=*) TO="${1#--to=}" ;;
            --ack) ACK=1 ;;
            --reject) REJECT=1 ;;
            --reason) shift; REASON="${1:-}" ;;
            --reason=*) REASON="${1#--reason=}" ;;
            -*) ct_die "peer respond-mode: unknown flag '$1'" ;;
            *)
                if [ -z "$PROJECT" ]; then PROJECT="$1"
                else ct_die "peer respond-mode: too many positional args"
                fi
                ;;
        esac
        shift
    done

    [ -n "$TO" ] || ct_die "peer respond-mode: --to <proposal-id> required (get from 'yakos peer log')"
    [ "$ACK" = "1" ] || [ "$REJECT" = "1" ] || ct_die "peer respond-mode: --ack or --reject required"
    [ "$ACK" = "1" ] && [ "$REJECT" = "1" ] && ct_die "peer respond-mode: --ack and --reject are mutually exclusive"

    PROJECT="$(resolve_project "$PROJECT")"
    COORD="$(require_coord "$PROJECT")"

    me_user="${USER:-$(id -un)}"
    me_host="${HOSTNAME:-$(hostname -s)}"
    me_pid="${YAKOS_SESSION_PID:-$$}"
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    response="ack"
    [ "$REJECT" = "1" ] && response="reject"

    event="$(jq -nc \
        --arg ts "$ts" \
        --arg user "$me_user" \
        --arg host "$me_host" \
        --argjson pid "$me_pid" \
        --arg pid_s "$TO" \
        --arg resp "$response" \
        --arg reason "$REASON" \
        '{ts: $ts, kind: "mode_response",
          actor: {user: $user, host: $host, pid: $pid,
                  session_id: "operator", agent: "lead"},
          detail: {proposal_id: $pid_s, response: $resp, reason: $reason}}')"
    printf '%s\n' "$event" >> "$COORD/activity.ndjson"
    mkdir -p "$COORD/sessions"
    printf '%s\n' "$event" >> "$COORD/sessions/${me_user}@${me_host}-${me_pid}.ndjson"
    echo "responded $response to proposal $TO"
    exit 0
fi

ct_die "peer: unknown subcommand '$SUB' (try --help)"
