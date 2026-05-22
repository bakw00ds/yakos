#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: quickstart.sh — one command to go from "fresh clone" to
# "session started against the cwd".
#
# Detects three states and does the right thing for each:
#
#   1. yakOS not installed (~/.yakos missing or stale)
#      → run 'yakos install'
#   2. cwd is a git repo not yet bootstrapped
#      → run 'yakos init <basename> --project <cwd>'
#   3. project already bootstrapped (or cwd is the agent-control dir)
#      → run 'yakos start <name>'
#
# Each step is skipped if it has already been done. Idempotent;
# safe to re-run.
#
# Optional flags forwarded to underlying commands:
#   --runtime <id>        forwarded to start
#   --multi-dev           forwarded to init (one-time per project)
#   --safe                forwarded to start
#   --dry-run             forwarded to start; quickstart still runs
#                         install/init in dry-mode (prints what would happen)

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos quickstart'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos quickstart'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos quickstart [--runtime <id>] [--multi-dev] [--safe] [--dry-run]

One command to go from "fresh clone" to "session started against the cwd".

Detects what's been done and runs only what's needed:
  1. yakOS not installed → 'yakos install'
  2. cwd is a git repo not yet bootstrapped → 'yakos init'
  3. project already bootstrapped → 'yakos start'

Idempotent; safe to re-run.

Examples:
  cd ~/code/myapp
  yakos quickstart                  # install if needed, init, start
  yakos quickstart --multi-dev      # bootstrap with co-pilot mode
  yakos quickstart --runtime codex  # start with codex instead of claude
EOF
}

RUNTIME=""
MULTI_DEV=0
SAFE=0
DRY_RUN=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --runtime) shift; RUNTIME="${1:-}" ;;
        --runtime=*) RUNTIME="${1#--runtime=}" ;;
        --multi-dev) MULTI_DEV=1 ;;
        --safe) SAFE=1 ;;
        --dry-run) DRY_RUN=1 ;;
        *) ct_die "quickstart: unknown argument '$1' (try --help)" ;;
    esac
    shift
done

cwd_real="$(cd "$PWD" && pwd -P)"
ac_root="$HOME/agent-control"
yakos_pointer="$HOME/.yakos"

echo "yakos quickstart"
echo "  cwd:        $cwd_real"
echo "  yakos root: $YAKOS_ROOT"
echo

# ---- step 1: ensure yakOS is installed --------------------------------------
needs_install=0
if [ ! -f "$yakos_pointer" ]; then
    needs_install=1
    reason="$yakos_pointer does not exist"
elif [ "$(cat "$yakos_pointer" 2>/dev/null)" != "$YAKOS_ROOT" ]; then
    needs_install=1
    reason="$yakos_pointer points to a different yakOS install ($(cat "$yakos_pointer"))"
fi

if [ "$needs_install" = "1" ]; then
    echo "step 1/3: install yakOS — $reason"
    if [ "$DRY_RUN" = "1" ]; then
        echo "  (dry-run) would run: $YAKOS_ROOT/cli/yakos install"
    else
        bash "$YAKOS_LIB/install.sh"
        echo
    fi
else
    echo "step 1/3: install — already done (pointer at $yakos_pointer)"
fi
echo

# ---- step 2: figure out the project ----------------------------------------
# Two cases:
#   A) cwd is ~/agent-control/<name>/ → name is the basename
#   B) cwd is a git repo → name is the basename; might need init
#   C) cwd is inside a tracked project repo → walk back to find which agent-control owns it

NAME=""
PROJECT_ABS=""
needs_init=0
init_reason=""

case "$cwd_real" in
    "$ac_root"/*)
        rest="${cwd_real#$ac_root/}"
        NAME="${rest%%/*}"
        cd_path="$ac_root/$NAME/.project-path"
        if [ -f "$cd_path" ]; then
            PROJECT_ABS="$(head -1 "$cd_path")"
            echo "step 2/3: project '$NAME' — already bootstrapped (cwd is agent-control dir)"
        else
            ct_die "quickstart: $ac_root/$NAME exists but .project-path missing; run 'yakos init $NAME --project <repo>' to repair"
        fi
        ;;
esac

if [ -z "$NAME" ] && [ -d "$ac_root" ]; then
    # Maybe cwd is inside a bootstrapped project repo
    for cd_path in "$ac_root"/*/.project-path; do
        [ -f "$cd_path" ] || continue
        p="$(head -1 "$cd_path" 2>/dev/null)"
        pr="$(cd "$p" 2>/dev/null && pwd -P || true)"
        [ -n "$pr" ] || continue
        case "$cwd_real" in
            "$pr"|"$pr"/*)
                NAME="$(basename -- "$(dirname -- "$cd_path")")"
                PROJECT_ABS="$pr"
                echo "step 2/3: project '$NAME' — already bootstrapped (cwd inside project repo)"
                break
                ;;
        esac
    done
fi

if [ -z "$NAME" ]; then
    # cwd isn't agent-control and isn't a tracked project. Is it a git repo?
    if git -C "$cwd_real" rev-parse --git-dir >/dev/null 2>&1; then
        PROJECT_ABS="$(git -C "$cwd_real" rev-parse --show-toplevel)"
        NAME="$(basename -- "$PROJECT_ABS")"
        # Validate name
        case "$NAME" in
            *[!A-Za-z0-9_-]*)
                ct_die "quickstart: inferred project name '$NAME' has invalid characters. Run 'yakos init <name> --project $PROJECT_ABS' explicitly with a valid name."
                ;;
        esac
        if [ -d "$ac_root/$NAME" ]; then
            echo "step 2/3: project '$NAME' — already bootstrapped"
        else
            needs_init=1
            init_reason="cwd is git repo '$PROJECT_ABS' but $ac_root/$NAME does not exist"
        fi
    else
        ct_die "quickstart: cwd ($cwd_real) is not a git repo and not a known yakos project. Either:
  - cd into a git repo and re-run 'yakos quickstart', OR
  - run 'yakos init <name> --project <repo-path>' explicitly"
    fi
fi

if [ "$needs_init" = "1" ]; then
    echo "step 2/3: init project '$NAME' — $init_reason"
    init_args=("$NAME" --project "$PROJECT_ABS")
    [ "$MULTI_DEV" = "1" ] && init_args+=(--multi-dev)
    if [ "$DRY_RUN" = "1" ]; then
        echo "  (dry-run) would run: $YAKOS_ROOT/cli/yakos init ${init_args[*]}"
    else
        bash "$YAKOS_LIB/init.sh" "${init_args[@]}"
    fi
fi
echo

# ---- step 3: start the session ---------------------------------------------
echo "step 3/3: start session for '$NAME'"
start_args=("$NAME")
[ -n "$RUNTIME" ] && start_args+=(--runtime "$RUNTIME")
[ "$SAFE" = "1" ] && start_args+=(--safe)
[ "$DRY_RUN" = "1" ] && start_args+=(--dry-run)
exec bash "$YAKOS_LIB/start.sh" "${start_args[@]}"
