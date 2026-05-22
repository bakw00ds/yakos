#!/usr/bin/env bash
# Purpose: checkpoint.sh — snapshot session state; resume from snapshot.
#
# Subcommands:
#   yakos checkpoint now                   # create snapshot
#   yakos checkpoint list                  # list existing snapshots
#   yakos checkpoint resume <id>           # resume via --fork-session
#   yakos checkpoint clean [--age <days>]  # GC old (default >30d)
#
# Snapshot layout: <work>/current/checkpoints/<iso>/
#   summary.md         — 1-paragraph state digest (TODO: agent-generated)
#   scratchpad/        — copy of plan/decisions/contracts/status .md
#   token-snapshot.txt — rough token-usage estimate at checkpoint time
#   session-id.txt     — runtime session_id (for --fork-session)
#   manifest.json      — full checkpoint metadata

set -eu

: "${YAKOS_LIB:?checkpoint.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/paths.sh"

_checkpoints_dir() {
    printf '%s/checkpoints' "$(yakos_current_dir)"
}

cmd_now() {
    local cur ts dir
    cur="$(yakos_current_dir)"
    ts="$(ct_iso_utc)"
    dir="$cur/checkpoints/$ts"

    mkdir -p "$dir/scratchpad"
    for f in plan.md decisions.md contracts.md status.md kanban.md; do
        [ -f "$cur/$f" ] && cp "$cur/$f" "$dir/scratchpad/" 2>/dev/null
    done

    # Session id (for --fork-session). Read from environment if hook context;
    # otherwise from .session-started-history.ndjson tail.
    local session_id
    session_id="${CLAUDE_SESSION_ID:-}"
    if [ -z "$session_id" ] && [ -f "$cur/.session-started-history.ndjson" ]; then
        session_id="$(jq -r 'select(.session_id) | .session_id' "$cur/.session-started-history.ndjson" 2>/dev/null | tail -1)"
    fi
    printf '%s\n' "${session_id:-unknown}" > "$dir/session-id.txt"

    # Manifest.
    jq -nc \
        --arg ts "$(ct_iso_now_z)" \
        --arg session "$session_id" \
        --arg runtime "${YAKOS_RUNTIME:-claude}" \
        --arg user "$(id -un)" \
        '{created: $ts, session_id: $session, runtime: $runtime, by_user: $user}' \
        > "$dir/manifest.json"

    # Token snapshot — best effort. context-threshold.sh has a real probe;
    # for now just record the timestamp.
    printf 'checkpoint at %s\n' "$(ct_iso_now_z)" > "$dir/token-snapshot.txt"

    # Summary.md — TODO(M3.2): dispatch librarian to write a one-paragraph
    # state digest. For draft, write a placeholder.
    cat > "$dir/summary.md" <<EOF
# Checkpoint summary

**Created:** $(ct_iso_now_z)
**Session:** ${session_id:-unknown}
**Runtime:** ${YAKOS_RUNTIME:-claude}

(librarian-generated digest goes here in M3.2 — for the draft,
this is the placeholder. Inspect scratchpad/ for full state.)
EOF

    ct_log "checkpoint created: $dir"
    printf '%s\n' "$ts"
}

cmd_list() {
    local dir
    dir="$(_checkpoints_dir)"
    [ -d "$dir" ] || { echo "(no checkpoints)"; return 0; }

    # List checkpoints with size + age.
    find "$dir" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | sort | while read -r cp; do
        local size age_days
        size="$(ct_dir_size_bytes "$cp")"
        local mtime now
        mtime="$(date -r "$cp" '+%s' 2>/dev/null || stat -c '%Y' "$cp" 2>/dev/null || echo 0)"
        now="$(date '+%s')"
        age_days=$(( (now - mtime) / 86400 ))
        printf '  %s  (%s bytes, %d days ago)\n' "$(basename "$cp")" "$size" "$age_days"
    done
}

cmd_resume() {
    local id="$1"
    local dir
    dir="$(_checkpoints_dir)/$id"
    [ -d "$dir" ] || ct_die "checkpoint '$id' not found"

    local session_id summary
    session_id="$(cat "$dir/session-id.txt" 2>/dev/null || echo unknown)"
    [ "$session_id" = "unknown" ] && ct_die "checkpoint '$id' has no session-id; cannot resume"

    summary="$(cat "$dir/summary.md" 2>/dev/null)"
    cat <<EOF
Resuming from checkpoint $id (session $session_id).

To complete the resume:
  yakos start --runtime ${YAKOS_RUNTIME:-claude} --resume $session_id

The new session will load with the original session context. The
checkpoint's scratchpad/ contents are preserved at $dir/scratchpad/.

Summary:
$summary
EOF
}

cmd_clean() {
    local age_days=30
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --age) age_days="$2"; shift 2 ;;
            *) shift ;;
        esac
    done
    case "$age_days" in
        ''|*[!0-9]*) ct_die "--age must be a number (days)" ;;
    esac

    local dir
    dir="$(_checkpoints_dir)"
    [ -d "$dir" ] || return 0

    local removed=0
    find "$dir" -maxdepth 1 -mindepth 1 -type d -mtime "+$age_days" 2>/dev/null | while read -r cp; do
        rm -rf "$cp"
        removed=$((removed + 1))
    done
    ct_log "removed $removed checkpoint(s) older than $age_days days"
}

case "${1:-help}" in
    now)     shift; cmd_now "$@" ;;
    list)    shift; cmd_list "$@" ;;
    resume)  shift; cmd_resume "$@" ;;
    clean)   shift; cmd_clean "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos checkpoint — snapshot + restore session state

  yakos checkpoint now                    # create snapshot
  yakos checkpoint list                   # list existing
  yakos checkpoint resume <id>            # resume via --fork-session
  yakos checkpoint clean [--age <days>]   # GC old (default >30d)

See lib/skills/checkpoint-context/SKILL.md for the full discipline.
EOF
        ;;
    *) ct_die "yakos checkpoint: unknown subcommand '$1'" ;;
esac
