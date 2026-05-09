#!/usr/bin/env bash
# team.sh — team lifecycle subcommands.
#
# Purpose: archive a project's work/current/ and print relaunch
# instructions. v0.3+ relaunch goes through `yakos start <project>`.
#
# v0.1 ships only `yakos team restart <project>`:
#   1. Pick a tag (auto-generated from timestamp).
#   2. Archive work/current/ via 'yakos archive <project> <tag> --auto-tag'.
#   3. Print relaunch instructions. v0.1 does NOT auto-relaunch claude.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos team'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos team'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<EOF
yakos team <subcommand> [args...]

Subcommands:
  restart <project>     Archive work/current/ and print instructions to
                        relaunch a fresh claude session. Does NOT
                        auto-relaunch in v0.1.

Options on 'restart':
  --tag <tag>           Override the auto-generated archive tag.
  --yes                 Skip the confirmation summary.

Other 'team' subcommands may be added in later versions.
EOF
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    restart) ;;
    *) ct_die "team: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- restart ----------------------------------------------------------------

PROJECT=""
TAG=""
YES=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help)
            cat <<EOF
yakos team restart <project> [--tag <tag>] [--yes]

Archive work/current/ for <project> and print relaunch instructions.
Does NOT auto-launch claude in v0.1.
EOF
            exit 0
            ;;
        --tag)
            shift
            [ "$#" -gt 0 ] || ct_die "team restart: --tag requires a value"
            TAG="$1"
            ;;
        --yes|-y) YES=1 ;;
        --*) ct_die "team restart: unknown flag '$1'" ;;
        *)
            if [ -z "$PROJECT" ]; then PROJECT="$1"
            else ct_die "team restart: too many positional args"
            fi
            ;;
    esac
    shift
done

[ -n "$PROJECT" ] || ct_die "team restart: missing <project>"

CONTROL_DIR="$HOME/agent-control/$PROJECT"
[ -d "$CONTROL_DIR" ] || ct_die "team restart: project '$PROJECT' not found at $CONTROL_DIR"

if [ -z "$TAG" ]; then
    TAG="auto-$(ct_iso_utc)"
fi

# Derive the project path (best-effort) from a marker we wrote at init time.
# v0.1 doesn't track this anywhere; instead, if path-allowlist.json exists
# we know the project hooked up — but we don't know the repo path here.
# So we just read it from $CONTROL_DIR/.project-path if present
# (init can write this in a later version). For now, just print the
# generic relaunch hint.

echo "Restarting team for '$PROJECT'..."
echo "  Auto-tag: $TAG"

# Delegate to archive
bash "$YAKOS_LIB/archive.sh" "$PROJECT" "$TAG" --auto-tag --yes

project_repo="<path-to-project-repo>"
if [ -f "$CONTROL_DIR/.project-path" ]; then
    project_repo="$(cat "$CONTROL_DIR/.project-path")"
fi

cat <<EOF

Team archived. To start a fresh session:
  yakos start $PROJECT

(Or run bare 'yakos' from $CONTROL_DIR or $project_repo — auto-detects.)
EOF
