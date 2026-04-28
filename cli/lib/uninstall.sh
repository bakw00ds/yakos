#!/usr/bin/env bash
# uninstall.sh — remove YakOS-managed state from $HOME/.claude.
#
# Behavior contract (from the build prompt):
#   1. Remove every symlink under ~/.claude/{agents,skills,rules,playbooks}/
#      that resolves to a path inside the YakOS repo (per ~/.yakos pointer).
#      Leave non-YakOS files alone.
#   2. If $HOME/.claude/.yakos-created-settings exists, remove settings.json
#      (we created it and should remove it on uninstall).
#   3. If --restore-settings is passed AND a YakOS backup exists, restore
#      from the most recent backup. Otherwise, list available backups and
#      leave settings.json alone.
#   4. Remove $HOME/.yakos.
#   5. NEVER touch $HOME/.claude/projects/ (auto-memory). Print a
#      one-line confirmation that it was left intact.

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos uninstall'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

RESTORE_SETTINGS=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos uninstall — remove YakOS-managed symlinks and pointer

Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
that point into the YakOS repo. Files Yakos didn't create are left alone.

Auto-memory at ~/.claude/projects/ is NEVER touched, even with any flag.

Usage: yakos uninstall [--restore-settings]

Options:
    --restore-settings   If a YakOS settings.json backup exists, restore
                         from the most recent one. Default: leave
                         settings.json alone, list backups.
    --help, -h           Print this help

Removes:
    Yakos-owned symlinks under ~/.claude/{agents,skills,rules,playbooks}/
    ~/.claude/settings.json    (only if YakOS created it; marker file present)
    ~/.yakos                   (the pointer file)

Never removes:
    ~/.claude/projects/        (auto-memory; always preserved)
    Files or symlinks that don't point into the YakOS repo
EOF
            exit 0
            ;;
        --restore-settings)
            RESTORE_SETTINGS=1
            ;;
        *)
            ct_die "uninstall: unknown argument '$arg' (try --help)"
            ;;
    esac
done

CLAUDE_DIR="$HOME/.claude"
YAKOS_POINTER="$HOME/.yakos"
SETTINGS_FILE="$CLAUDE_DIR/settings.json"
CREATED_MARKER="$CLAUDE_DIR/.yakos-created-settings"

if [ ! -f "$YAKOS_POINTER" ] && [ ! -d "$CLAUDE_DIR" ]; then
    ct_log "Nothing to uninstall: no $YAKOS_POINTER and no $CLAUDE_DIR"
    exit 0
fi

# Resolve the YakOS root via the pointer (if present). If it's missing,
# we can still walk symlinks and remove ones whose targets no longer
# resolve OR whose target paths are unrecoverable.
YAKOS_ROOT_ABS=""
if [ -f "$YAKOS_POINTER" ]; then
    YAKOS_ROOT_ABS="$(cat "$YAKOS_POINTER" 2>/dev/null || true)"
fi
if [ -z "$YAKOS_ROOT_ABS" ]; then
    # Fall back to YAKOS_ROOT (set by the dispatcher when run from a clone).
    YAKOS_ROOT_ABS="${YAKOS_ROOT:-}"
fi

remove_count=0
keep_count=0

remove_yakos_symlinks_in() {
    local sub="$1"
    local dst_root="$CLAUDE_DIR/$sub"
    [ -d "$dst_root" ] || return 0

    while IFS= read -r link; do
        [ -n "$link" ] || continue
        local target
        target="$(ct_realpath "$link" 2>/dev/null || true)"

        # If we have a pointer, only remove links pointing into it.
        # If we don't, fall back to: remove links whose target is broken
        # AND has YakOS-shaped paths (../lib/agents/...) — but conservatively
        # we just don't remove broken-target links unless we have a pointer.
        if [ -n "$YAKOS_ROOT_ABS" ]; then
            case "$target" in
                "$YAKOS_ROOT_ABS"/*)
                    rm -f "$link"
                    remove_count=$((remove_count + 1))
                    ;;
                *)
                    keep_count=$((keep_count + 1))
                    ;;
            esac
        else
            keep_count=$((keep_count + 1))
        fi
    done < <(find "$dst_root" -type l 2>/dev/null)
}

for sub in agents skills rules playbooks; do
    remove_yakos_symlinks_in "$sub"
done

ct_log "Symlinks: $remove_count removed, $keep_count left in place (not YakOS-owned)"

# ---- settings.json handling -------------------------------------------------

if [ -f "$CREATED_MARKER" ]; then
    if [ -f "$SETTINGS_FILE" ]; then
        rm -f "$SETTINGS_FILE"
        ct_log "Removed $SETTINGS_FILE (we created it during install)"
    fi
    rm -f "$CREATED_MARKER"
elif [ "$RESTORE_SETTINGS" = "1" ]; then
    # Find the most recent yakos backup; restore it.
    latest_backup=""
    while IFS= read -r f; do
        latest_backup="$f"
    done < <(find "$CLAUDE_DIR" -maxdepth 1 -type f -name 'settings.json.yakos-bak-*' 2>/dev/null | sort)
    if [ -n "$latest_backup" ]; then
        cp "$latest_backup" "$SETTINGS_FILE"
        ct_log "Restored $SETTINGS_FILE from $latest_backup"
    else
        ct_log "--restore-settings requested but no settings.json.yakos-bak-* found in $CLAUDE_DIR"
    fi
else
    # List backups so the user knows they exist.
    backups=""
    while IFS= read -r f; do
        backups="${backups}\n  $f"
    done < <(find "$CLAUDE_DIR" -maxdepth 1 -type f -name 'settings.json.yakos-bak-*' 2>/dev/null | sort)
    if [ -n "$backups" ]; then
        printf 'YakOS settings backups available (use --restore-settings to apply the most recent):'
        # shellcheck disable=SC2059
        printf "$backups"
        printf '\n'
    fi
fi

# ---- pointer ----------------------------------------------------------------

if [ -f "$YAKOS_POINTER" ]; then
    rm -f "$YAKOS_POINTER"
    ct_log "Removed $YAKOS_POINTER"
fi

# ---- final note -------------------------------------------------------------

echo "Auto-memory at $CLAUDE_DIR/projects/ left intact (intentional)."
echo "YakOS uninstalled."
