#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: session.sh — session lifecycle commands beyond start.
#
# Subcommands:
#   yakos session export <project> <tag>  Bundle session artifacts
#                                         into a tar.gz for incident
#                                         review or operator handoff.
#   yakos session list <project>          List archived sessions.
#
# An "export" includes:
#   work/current/decisions.md (or current archive's decisions.md)
#   work/current/reports/*
#   work/current/team-inboxes/*  (mailbox snapshots)
#   the launch-log.ndjson entries for this session
#   the dispatch-log.ndjson entries for this session
#   the project's memory snapshot (~/.yakos-state/memory/<project>/)
#   the project's runtime-probe snapshots

set -eu

: "${YAKOS_ROOT:?session.sh: YAKOS_ROOT must be set}"
: "${YAKOS_LIB:?session.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos session <subcommand> [args...]

Subcommands:
  export <project> [<tag>]     Bundle the current/named session into a
                               tar.gz under <project>/work/exports/.
                               Includes decisions, reports, mailbox
                               snapshots, audit-log entries, memory.
                               Default <tag>: current ISO timestamp.
  list <project>               List session archives + the current one.

Examples:
  yakos session export myapp                 # exports work/current/
  yakos session export myapp post-incident-1 # named export
  yakos session list myapp
EOF
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    export|list) ;;
    *) ct_die "session: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- subcommand: list -----------------------------------------------------

if [ "$SUB" = "list" ]; then
    PROJECT="${1:-}"
    [ -n "$PROJECT" ] || ct_die "session list: <project> required"
    CONTROL_DIR="$HOME/agent-control/$PROJECT"
    [ -d "$CONTROL_DIR" ] || ct_die "session list: project '$PROJECT' not found"

    echo "yakos session list — $PROJECT"
    echo
    echo "  current:"
    if [ -d "$CONTROL_DIR/work/current" ]; then
        echo "    $CONTROL_DIR/work/current/"
        if [ -f "$CONTROL_DIR/work/current/decisions.md" ]; then
            sz="$(wc -c < "$CONTROL_DIR/work/current/decisions.md" | tr -d ' ')"
            echo "      decisions.md: ${sz}B"
        fi
    fi
    echo
    echo "  archived:"
    if [ -d "$CONTROL_DIR/work/archive" ]; then
        find "$CONTROL_DIR/work/archive" -maxdepth 1 -type d ! -path "$CONTROL_DIR/work/archive" \
            | sort | sed "s|$CONTROL_DIR/work/archive/|    |"
    fi
    echo
    echo "  exports:"
    if [ -d "$CONTROL_DIR/work/exports" ]; then
        ls -1 "$CONTROL_DIR/work/exports" 2>/dev/null | sed 's|^|    |'
    else
        echo "    (none yet)"
    fi
    exit 0
fi

# ---- subcommand: export ---------------------------------------------------

if [ "$SUB" = "export" ]; then
    PROJECT="${1:-}"
    TAG="${2:-}"
    [ -n "$PROJECT" ] || { usage >&2; ct_die "session export: <project> required"; }

    CONTROL_DIR="$HOME/agent-control/$PROJECT"
    [ -d "$CONTROL_DIR" ] || ct_die "session export: project '$PROJECT' not found"

    [ -n "$TAG" ] || TAG="$(ct_iso_utc)"
    case "$TAG" in
        *[!A-Za-z0-9_.-]*) ct_die "session export: <tag> must be alphanumeric (-_.)" ;;
    esac

    EXPORT_DIR="$CONTROL_DIR/work/exports"
    mkdir -p "$EXPORT_DIR"
    BUNDLE="$EXPORT_DIR/${PROJECT}-${TAG}.tar.gz"

    # Stage everything into a temp directory so the tar has a clean root.
    stage="$(mktemp -d -t yakos-export.XXXXXX)"
    trap 'rm -rf "$stage" 2>/dev/null || true' EXIT
    bundle_root="$stage/${PROJECT}-${TAG}"
    mkdir -p "$bundle_root"

    # 1. decisions, reports, mailbox snapshots
    if [ -d "$CONTROL_DIR/work/current" ]; then
        cp -R "$CONTROL_DIR/work/current/." "$bundle_root/work-current/" 2>/dev/null || true
    fi

    # 2. audit-log entries — slice by project
    state_dir="$HOME/.yakos-state"
    if [ -f "$state_dir/launch-log.ndjson" ]; then
        jq -c --arg p "$PROJECT" 'select(.project == $p)' \
            "$state_dir/launch-log.ndjson" 2>/dev/null \
            > "$bundle_root/launch-log.ndjson" || true
    fi
    if [ -f "$state_dir/dispatch-log.ndjson" ]; then
        proj_repo="$(head -1 "$CONTROL_DIR/.project-path" 2>/dev/null || echo)"
        jq -c --arg pr "$proj_repo" 'select(.project == $pr)' \
            "$state_dir/dispatch-log.ndjson" 2>/dev/null \
            > "$bundle_root/dispatch-log.ndjson" || true
    fi

    # 3. memory snapshot
    mem_src="$state_dir/memory/$PROJECT"
    if [ -d "$mem_src" ]; then
        cp -R "$mem_src" "$bundle_root/memory" 2>/dev/null || true
    fi

    # 4. runtime-probe history
    if [ -d "$state_dir/runtime-probes" ]; then
        cp -R "$state_dir/runtime-probes" "$bundle_root/runtime-probes" 2>/dev/null || true
    fi

    # 5. manifest
    {
        printf '# yakOS session export\n\n'
        printf 'project: %s\n' "$PROJECT"
        printf 'tag: %s\n' "$TAG"
        printf 'exported: %s\n' "$(ct_iso_now_z)"
        printf 'yakos-version: %s\n' "${YAKOS_VERSION:-unknown}"
        printf '\nContents:\n'
        find "$bundle_root" -mindepth 1 -maxdepth 2 | sort \
            | sed "s|$bundle_root/||" | sed 's/^/  /'
    } > "$bundle_root/MANIFEST.md"

    # Bundle.
    ( cd "$stage" && tar czf "$BUNDLE" "${PROJECT}-${TAG}" )
    sz="$(wc -c < "$BUNDLE" | tr -d ' ')"
    echo "wrote $BUNDLE (${sz}B)"
    echo "review: tar tzf $BUNDLE | head"
    exit 0
fi
