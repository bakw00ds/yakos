#!/usr/bin/env bash
# Purpose: update.sh — pull the YakOS repo and refresh symlinks.
#
# Behavior:
#   1. cd $YAKOS_ROOT && git pull (with --ff-only by default).
#   2. Re-run install (idempotent — refreshes any added or removed symlinks).
#   3. Report changed files since the prior HEAD.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos update'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos update'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

ALLOW_NON_FF=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos update — pull the framework repo and refresh symlinks

Runs:
  git pull --ff-only         in $YAKOS_ROOT
  yakos install              (refreshes symlinks idempotently)

Then prints commits applied since the previous HEAD and a list of
changed files under lib/ (which is what install/uninstall consume).

Options:
  --allow-non-ff   Allow non-fast-forward pulls (default: refuses).
  --help, -h       Print this help.
EOF
            exit 0
            ;;
        --allow-non-ff) ALLOW_NON_FF=1 ;;
        *) ct_die "update: unknown flag '$arg'" ;;
    esac
done

if [ ! -d "$YAKOS_ROOT/.git" ]; then
    ct_die "update: $YAKOS_ROOT is not a git repository (was YakOS installed from a git clone?)"
fi

prev_head="$(git -C "$YAKOS_ROOT" rev-parse HEAD)"

if [ "$ALLOW_NON_FF" = "1" ]; then
    git -C "$YAKOS_ROOT" pull
else
    git -C "$YAKOS_ROOT" pull --ff-only
fi

new_head="$(git -C "$YAKOS_ROOT" rev-parse HEAD)"

if [ "$prev_head" = "$new_head" ]; then
    ct_log "Already up to date at $(git -C "$YAKOS_ROOT" describe --always --tags 2>/dev/null || echo "$new_head")"
    exit 0
fi

ct_log "Pulled commits $prev_head..$new_head"
echo
echo "Commits applied:"
git -C "$YAKOS_ROOT" log --oneline "$prev_head..$new_head"
echo

changed_lib="$(git -C "$YAKOS_ROOT" diff --name-only "$prev_head..$new_head" -- lib/ 2>/dev/null || true)"
if [ -n "$changed_lib" ]; then
    echo "Changed under lib/ (consumed by install/uninstall):"
    printf '  %s\n' $changed_lib
    echo
fi

echo "Refreshing symlinks via 'yakos install'..."
bash "$YAKOS_LIB/install.sh"
