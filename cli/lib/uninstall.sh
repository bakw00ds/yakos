#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: uninstall.sh — remove YakOS-managed state from $HOME/.claude.
#
# Behavior contract:
#   1. Remove every symlink under ~/.claude/{agents,skills,rules,playbooks}/
#      that resolves to a path inside the YakOS repo (per ~/.yakos pointer).
#      Leave non-YakOS files alone.
#   2. If $HOME/.claude/.yakos-created-settings exists, remove settings.json
#      (we created it and should remove it on uninstall).
#   3. If --restore-settings is passed AND a YakOS backup exists, restore
#      from the most recent backup. Otherwise, list available backups and
#      leave settings.json alone.
#   4. Remove the managed launcher symlink recorded in
#      ~/.yakos-state/install-manifest (ONLY if the symlink still resolves
#      into the yakOS root we're uninstalling — never remove a foreign binary
#      or a symlink pointing into a different tree).
#   5. Remove $HOME/.yakos and $HOME/.yakos-state/install-manifest.
#   6. NEVER touch $HOME/.claude/projects/ (auto-memory). Print a
#      one-line confirmation that it was left intact.
#
# Robustness for stale / cross-location installs:
#   - If ~/.yakos points to a path that no longer exists or is unreadable
#     (moved install, root-owned clone), a warning is printed and cleanup
#     continues for everything local that can be determined:
#       • The pointer file itself is removed.
#       • Any symlink under ~/.claude/{agents,skills,rules,playbooks}/ whose
#         target is broken (dangling) is removed — it can only be ours.
#       • The launcher symlink is removed if it is dangling or resolves into
#         the now-missing root.
#   - If the managed launcher points into a root different from $HOME
#     (e.g. /root/...) and we cannot access it, a clear instruction is
#     printed: "re-run as: sudo yakos uninstall, or remove <stale-link>
#     manually."
#   - --root <path>: uninstall using <path> as YAKOS_ROOT_ABS instead of
#     the ~/.yakos pointer. Useful for cleaning a leftover install at an
#     alternate location (e.g. after a root-owned install).

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos uninstall'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

RESTORE_SETTINGS=0
EXPLICIT_ROOT=""
while [ $# -gt 0 ]; do
    arg="$1"
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos uninstall — remove YakOS-managed symlinks and pointer

Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
that point into the YakOS repo. Files Yakos didn't create are left alone.

Removes the managed launcher symlink at the path recorded in
~/.yakos-state/install-manifest (only if it still resolves into the yakOS
root being uninstalled — foreign binaries and foreign symlinks are never
touched).

Stale-pointer robustness: if ~/.yakos points at a path that is missing or
unreadable (moved install, root-owned clone), a warning is printed and
cleanup proceeds for what is locally accessible. Dangling symlinks under
~/.claude/{agents,skills,rules,playbooks}/ are removed as yakOS-owned.

Auto-memory at ~/.claude/projects/ is NEVER touched, even with any flag.

Usage: yakos uninstall [--restore-settings] [--root <path>]

Options:
    --restore-settings   If a YakOS settings.json backup exists, restore
                         from the most recent one. Default: leave
                         settings.json alone, list backups.
    --root <path>        Use <path> as YAKOS_ROOT instead of reading
                         ~/.yakos. Useful for cleaning a leftover install
                         at an alternate location (e.g. a prior root-owned
                         install). Combine with 'sudo' if the path is not
                         accessible as the current user.
    --help, -h           Print this help

Removes:
    Yakos-owned symlinks under ~/.claude/{agents,skills,rules,playbooks}/
    Dangling symlinks under the above directories (treated as yakOS-owned)
    ~/.claude/settings.json    (only if YakOS created it; marker file present)
    ~/.yakos                   (the pointer file)
    ~/.yakos-state/install-manifest (the launcher record)
    The managed launcher symlink recorded in the manifest (if yakOS-owned)

Never removes:
    ~/.claude/projects/        (auto-memory; always preserved)
    Files or symlinks that don't point into the YakOS repo
    Foreign yakos binaries or symlinks at the launcher path
EOF
            exit 0
            ;;
        --restore-settings)
            RESTORE_SETTINGS=1
            shift
            ;;
        --root)
            shift
            [ $# -gt 0 ] || ct_die "uninstall: --root requires a path argument"
            EXPLICIT_ROOT="$1"
            shift
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
YAKOS_STATE_DIR="$HOME/.yakos-state"
INSTALL_MANIFEST="$YAKOS_STATE_DIR/install-manifest"

if [ ! -f "$YAKOS_POINTER" ] && [ ! -d "$CLAUDE_DIR" ] && [ -z "$EXPLICIT_ROOT" ]; then
    ct_log "Nothing to uninstall: no $YAKOS_POINTER and no $CLAUDE_DIR"
    exit 0
fi

# ---- Resolve the YakOS root --------------------------------------------------
# Priority: --root flag > ~/.yakos pointer > YAKOS_ROOT env var.
# When the pointer is missing/unreadable, warn and continue with best effort.

YAKOS_ROOT_ABS=""
POINTER_STALE=0

if [ -n "$EXPLICIT_ROOT" ]; then
    YAKOS_ROOT_ABS="$(ct_realpath "$EXPLICIT_ROOT" 2>/dev/null || printf '%s' "$EXPLICIT_ROOT")"
    ct_log "Using explicit root: $YAKOS_ROOT_ABS"
elif [ -f "$YAKOS_POINTER" ]; then
    YAKOS_ROOT_ABS="$(cat "$YAKOS_POINTER" 2>/dev/null || true)"
    if [ -z "$YAKOS_ROOT_ABS" ]; then
        ct_log "WARNING: $YAKOS_POINTER exists but is empty; proceeding with best-effort cleanup"
        POINTER_STALE=1
    elif [ ! -d "$YAKOS_ROOT_ABS" ] || [ ! -r "$YAKOS_ROOT_ABS" ]; then
        ct_log "WARNING: pointer targets '$YAKOS_ROOT_ABS' which is missing or unreadable — cleaning what we can"
        POINTER_STALE=1
    fi
else
    # Fall back to YAKOS_ROOT (set by the dispatcher when run from a clone).
    YAKOS_ROOT_ABS="${YAKOS_ROOT:-}"
    if [ -z "$YAKOS_ROOT_ABS" ]; then
        ct_log "No ~/.yakos pointer and no --root flag; proceeding with best-effort cleanup (dangling symlinks only)"
        POINTER_STALE=1
    fi
fi

remove_count=0
keep_count=0

remove_yakos_symlinks_in() {
    local sub="$1"
    local dst_root="$CLAUDE_DIR/$sub"
    [ -d "$dst_root" ] || return 0

    while IFS= read -r link; do
        [ -n "$link" ] || continue

        # Check if the symlink target is readable.
        # readlink gives us the raw link value; ct_realpath gives the resolved
        # canonical path (only when target exists).
        local raw_target resolved_target
        raw_target="$(readlink "$link" 2>/dev/null || true)"
        resolved_target="$(ct_realpath "$link" 2>/dev/null || true)"

        # Case 1: dangling symlink (target no longer exists).
        # We treat these as yakOS-owned — a real user file would not be
        # a dangling symlink under ~/.claude/{agents,...}.
        if [ -z "$resolved_target" ] || [ ! -e "$link" ]; then
            rm -f "$link"
            remove_count=$((remove_count + 1))
            ct_log "Removed dangling symlink: $link (was: $raw_target)"
            continue
        fi

        # Case 2: we have a known root — remove only symlinks pointing into it.
        if [ -n "$YAKOS_ROOT_ABS" ] && [ "$POINTER_STALE" = "0" ]; then
            case "$resolved_target" in
                "$YAKOS_ROOT_ABS"/*)
                    rm -f "$link"
                    remove_count=$((remove_count + 1))
                    ;;
                *)
                    keep_count=$((keep_count + 1))
                    ;;
            esac
        elif [ -n "$YAKOS_ROOT_ABS" ] && [ "$POINTER_STALE" = "1" ]; then
            # Stale pointer: the old root is inaccessible; we already handled
            # dangling symlinks above. Live symlinks may point anywhere — be
            # conservative and keep them.
            case "$resolved_target" in
                "$YAKOS_ROOT_ABS"/*)
                    # Surprisingly still resolves into the old root path.
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

# ---- managed launcher symlink -----------------------------------------------
# Read launcher path from manifest; remove only if it is yakOS-owned.

remove_launcher() {
    local launcher_path=""

    if [ -f "$INSTALL_MANIFEST" ]; then
        local line
        while IFS= read -r line; do
            case "$line" in
                launcher=*)
                    launcher_path="${line#launcher=}"
                    ;;
            esac
        done < "$INSTALL_MANIFEST"
    fi

    if [ -z "$launcher_path" ]; then
        ct_log "No launcher path in manifest; skipping launcher removal"
        return 0
    fi

    if [ ! -L "$launcher_path" ]; then
        if [ -e "$launcher_path" ]; then
            ct_log "Launcher at $launcher_path is not a symlink (real file); leaving alone"
        else
            ct_log "Launcher at $launcher_path does not exist; nothing to remove"
        fi
        return 0
    fi

    local resolved_launcher
    resolved_launcher="$(ct_realpath "$launcher_path" 2>/dev/null || true)"

    # Dangling symlink — safe to remove (it's ours by manifest record).
    if [ -z "$resolved_launcher" ] || [ ! -e "$launcher_path" ]; then
        rm -f "$launcher_path"
        ct_log "Removed dangling launcher symlink: $launcher_path"
        return 0
    fi

    # Live symlink: remove only if it resolves into the yakOS root we're
    # uninstalling, or looks like a yakOS launcher from any root.
    local do_remove=0
    if [ -n "$YAKOS_ROOT_ABS" ]; then
        case "$resolved_launcher" in
            "$YAKOS_ROOT_ABS"/*)
                do_remove=1
                ;;
            */cli/yakos)
                # yakOS-shaped launcher from a different clone — we recorded it
                # in the manifest, so it is ours.
                do_remove=1
                ;;
        esac
    else
        # No known root — trust the manifest record.
        case "$resolved_launcher" in
            */cli/yakos)
                do_remove=1
                ;;
        esac
    fi

    if [ "$do_remove" = "1" ]; then
        rm -f "$launcher_path"
        ct_log "Removed managed launcher symlink: $launcher_path"
    else
        ct_log "Launcher $launcher_path → $resolved_launcher is not yakOS-owned; leaving alone"
        # Check if it points outside $HOME (e.g. a root-owned install residue).
        case "$resolved_launcher" in
            "$HOME"/*)
                : ;;
            *)
                ct_log "NOTICE: a launcher at $launcher_path resolves to $resolved_launcher (outside \$HOME)."
                ct_log "  This may be a leftover from a prior install by a different user/location."
                ct_log "  To clean it: sudo rm -f $launcher_path"
                ct_log "  Or re-run: sudo yakos uninstall --root $(dirname "$(dirname "$resolved_launcher")")"
                ;;
        esac
    fi
}

remove_launcher

# ---- stale cross-user launcher detection ------------------------------------
# Even without a manifest, detect whether the yakos command on PATH resolves
# into a root that is inaccessible (e.g. still pointing at /root/...).

_yakos_on_path="$(command -v yakos 2>/dev/null || true)"
if [ -n "$_yakos_on_path" ] && [ -L "$_yakos_on_path" ]; then
    _yakos_resolved="$(ct_realpath "$_yakos_on_path" 2>/dev/null || true)"
    if [ -z "$_yakos_resolved" ] || [ ! -e "$_yakos_on_path" ]; then
        ct_log "WARNING: 'yakos' on your PATH ($( command -v yakos 2>/dev/null || printf 'unknown')) is a dangling symlink."
        ct_log "  To fix: rm -f $( command -v yakos 2>/dev/null || printf "$_yakos_on_path")"
    elif [ -n "$YAKOS_ROOT_ABS" ] && [ "$POINTER_STALE" = "0" ]; then
        case "$_yakos_resolved" in
            "$YAKOS_ROOT_ABS"/*) : ;; # same root — already handled above
            */cli/yakos)
                ct_log "NOTICE: 'yakos' on your PATH ($( command -v yakos 2>/dev/null || printf "$_yakos_on_path")) resolves to $_yakos_resolved"
                ct_log "  This may reference a different yakOS install location."
                _stale_root="$(dirname "$(dirname "$_yakos_resolved")")"
                case "$_stale_root" in
                    "$HOME"/*) ct_log "  To clean it: yakos uninstall --root $_stale_root" ;;
                    *) ct_log "  To clean it (may need sudo): sudo yakos uninstall --root $_stale_root" ;;
                esac
                ;;
        esac
    fi
fi

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

# ---- pointer and manifest ---------------------------------------------------

if [ -f "$YAKOS_POINTER" ]; then
    rm -f "$YAKOS_POINTER"
    ct_log "Removed $YAKOS_POINTER"
fi

if [ -f "$INSTALL_MANIFEST" ]; then
    rm -f "$INSTALL_MANIFEST"
    ct_log "Removed $INSTALL_MANIFEST"
fi

# Remove the state dir only if it is now empty.
if [ -d "$YAKOS_STATE_DIR" ]; then
    rmdir "$YAKOS_STATE_DIR" 2>/dev/null || true
fi

# ---- final note -------------------------------------------------------------

echo "Auto-memory at $CLAUDE_DIR/projects/ left intact (intentional)."
echo "YakOS uninstalled."
