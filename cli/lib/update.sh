#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
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
ALL_PROJECTS=0
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
  --all            After the framework update, walk every
                   ~/agent-control/*/ and run 'yakos doctor <repo> --fix'
                   + 'yakos migrate <name>' on each. Surfaces drift
                   and refreshes per-project hook copies in one shot.
  --help, -h       Print this help.
EOF
            exit 0
            ;;
        --allow-non-ff) ALLOW_NON_FF=1 ;;
        --all) ALL_PROJECTS=1 ;;
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

# ---- optional: --all → per-project migrate + doctor ------------------------
if [ "$ALL_PROJECTS" = "1" ]; then
    ac_root="$HOME/agent-control"
    if [ ! -d "$ac_root" ]; then
        echo
        echo "no projects to update (no $ac_root)"
        exit 0
    fi
    echo
    echo "Updating per-project state (--all)..."
    n_total=0; n_ok=0; n_skipped=0; n_failed=0
    for cd_dir in "$ac_root"/*/; do
        [ -d "$cd_dir" ] || continue
        n_total=$((n_total + 1))
        proj_name="$(basename -- "$cd_dir")"
        cd_path="$cd_dir/.project-path"
        if [ ! -f "$cd_path" ]; then
            ct_log "  $proj_name: SKIP (no .project-path; orphaned control dir?)"
            n_skipped=$((n_skipped + 1))
            continue
        fi
        proj_repo="$(head -1 "$cd_path")"
        if [ ! -d "$proj_repo" ]; then
            ct_log "  $proj_name: SKIP (project repo $proj_repo not present on this host)"
            n_skipped=$((n_skipped + 1))
            continue
        fi
        echo
        echo "  $proj_name → $proj_repo"
        # doctor --fix is forgiving — it reports drift, doesn't fail the loop
        bash "$YAKOS_LIB/doctor.sh" "$proj_repo" --fix 2>&1 | sed 's/^/    /' || true
        # migrate is the real work — if it fails, count and continue
        if [ -f "$YAKOS_LIB/migrate.sh" ]; then
            if bash "$YAKOS_LIB/migrate.sh" "$proj_name" 2>&1 | sed 's/^/    /'; then
                n_ok=$((n_ok + 1))
            else
                n_failed=$((n_failed + 1))
            fi
        else
            n_ok=$((n_ok + 1))
        fi
    done
    echo
    echo "update --all summary: $n_total project(s), $n_ok ok, $n_skipped skipped, $n_failed failed"
    [ "$n_failed" -eq 0 ] || exit 1
fi
