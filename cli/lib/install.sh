#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: install.sh — first-time install for YakOS.
#
# Behavior:
#   1. Per-FILE symlinks from $HOME/.claude/{agents,skills,rules,playbooks}/
#      → $YAKOS_ROOT/lib/<subdir>/<file>. Existing user files in those
#      directories are preserved (we only create new symlinks, never
#      overwrite without --force).
#   2. Writes $HOME/.yakos containing the absolute path to YAKOS_ROOT
#      (used by uninstall to identify our symlinks).
#   3. Creates a managed launcher symlink at $HOME/.local/bin/yakos
#      → $YAKOS_ROOT_ABS/cli/yakos. Only created or refreshed if the
#      existing entry is either absent or a yakOS-owned symlink (i.e.
#      it already resolves into this repo). Foreign binaries and foreign
#      symlinks are left alone with a warning. The launcher path is
#      recorded in $HOME/.yakos-state/install-manifest so uninstall
#      removes exactly that entry. If $HOME/.local/bin is not on $PATH,
#      a one-line instruction is printed.
#   4. Safely merges the experimental-agent-teams env var into
#      $HOME/.claude/settings.json:
#        - If file doesn't exist: creates it with just the env block and
#          touches $HOME/.claude/.yakos-created-settings as a marker
#          (so uninstall knows we own the file).
#        - If file exists: validates it is valid JSON; if not, aborts.
#          Creates a timestamped backup, then merges via jq, preserving
#          all unknown keys.
#
# Never touches $HOME/.claude/projects/ (auto-memory).

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos install'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos install'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

FORCE=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos install — install YakOS into ~/.claude

Creates per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
that point into this repo's lib/. Existing files in those directories are
preserved (use --force to overwrite YakOS-owned symlinks).

Creates a managed launcher symlink at ~/.local/bin/yakos → this repo's
cli/yakos. The symlink is only created (or refreshed) when there is no
existing 'yakos' entry at that path or it already points into this repo.
Foreign binaries/symlinks are left alone with a warning. Records the
launcher path in ~/.yakos-state/install-manifest for clean uninstall.

Safely merges the experimental-agent-teams env var into
~/.claude/settings.json. If the file already exists, it is validated as
JSON and a timestamped backup is created before merge.

Usage: yakos install [--force]

Options:
    --force    Overwrite existing symlinks if they already point into
               this YakOS repo. Never overwrites non-symlink files or
               symlinks pointing outside this repo.
    --help, -h Print this help

Never touches:
    ~/.claude/projects/    (auto-memory)
    Unknown keys in settings.json
    Foreign yakos binaries or symlinks at ~/.local/bin/yakos
EOF
            exit 0
            ;;
        --force)
            FORCE=1
            ;;
        *)
            ct_die "install: unknown argument '$arg' (try --help)"
            ;;
    esac
done

CLAUDE_DIR="$HOME/.claude"
YAKOS_POINTER="$HOME/.yakos"
SETTINGS_FILE="$CLAUDE_DIR/settings.json"
CREATED_MARKER="$CLAUDE_DIR/.yakos-created-settings"
YAKOS_STATE_DIR="$HOME/.yakos-state"
INSTALL_MANIFEST="$YAKOS_STATE_DIR/install-manifest"
LAUNCHER_DIR="$HOME/.local/bin"
LAUNCHER_PATH="$LAUNCHER_DIR/yakos"

YAKOS_ROOT_ABS="$(ct_realpath "$YAKOS_ROOT")"

ct_log "Installing YakOS from $YAKOS_ROOT_ABS into $CLAUDE_DIR"

mkdir -p "$CLAUDE_DIR"
mkdir -p "$YAKOS_STATE_DIR"

# ---- 0. preflight: bail before touching state if existing settings.json is invalid

if [ -f "$SETTINGS_FILE" ] && ! ct_json_valid "$SETTINGS_FILE"; then
    ct_die "$SETTINGS_FILE is not valid JSON. Aborting install. Fix or remove it and re-run."
fi

# ---- 1. write the pointer file -----------------------------------------------

printf '%s\n' "$YAKOS_ROOT_ABS" > "$YAKOS_POINTER"
ct_log "Wrote $YAKOS_POINTER"

# ---- 1a. managed launcher symlink -------------------------------------------
# Target: $HOME/.local/bin/yakos → $YAKOS_ROOT_ABS/cli/yakos
# Rules:
#   - If no entry at LAUNCHER_PATH: create symlink, record in manifest.
#   - If existing entry is a symlink resolving into YAKOS_ROOT_ABS (or any
#     yakOS root, identified by the same canonical cli/yakos target name):
#     refresh it to point at this root, record in manifest.
#   - If existing entry is a real file or a symlink pointing outside this
#     repo: warn and leave it alone.

LAUNCHER_TARGET="$YAKOS_ROOT_ABS/cli/yakos"
LAUNCHER_CREATED=0

mkdir -p "$LAUNCHER_DIR"

if [ -L "$LAUNCHER_PATH" ]; then
    existing_target="$(ct_realpath "$LAUNCHER_PATH" 2>/dev/null || true)"
    # Check if it resolves into this yakOS root specifically, or if it resolves
    # into any path whose basename is "yakos" inside a "cli/" directory
    # (i.e. a yakOS-shaped launcher from a different clone location).
    case "$existing_target" in
        "$YAKOS_ROOT_ABS"/*)
            # Already owned by this exact root — refresh.
            rm -f "$LAUNCHER_PATH"
            ln -s "$LAUNCHER_TARGET" "$LAUNCHER_PATH"
            ct_log "Refreshed launcher symlink: $LAUNCHER_PATH → $LAUNCHER_TARGET"
            LAUNCHER_CREATED=1
            ;;
        */cli/yakos)
            # Looks like a yakOS launcher from a different clone — we own it
            # by shape. Refresh to point at this root.
            rm -f "$LAUNCHER_PATH"
            ln -s "$LAUNCHER_TARGET" "$LAUNCHER_PATH"
            ct_log "Refreshed yakOS launcher symlink (was: $existing_target): $LAUNCHER_PATH → $LAUNCHER_TARGET"
            LAUNCHER_CREATED=1
            ;;
        *)
            ct_log "WARNING: $LAUNCHER_PATH is a symlink pointing to '$existing_target' (not yakOS-owned). Leaving it alone. To use this yakOS install, add $YAKOS_ROOT_ABS/cli to your PATH or remove/rename the existing launcher."
            ;;
    esac
elif [ -e "$LAUNCHER_PATH" ]; then
    # Real file — never touch it.
    ct_log "WARNING: $LAUNCHER_PATH exists and is not a symlink (real file). Leaving it alone. To use this yakOS install, add $YAKOS_ROOT_ABS/cli to your PATH or remove/rename the existing binary."
else
    # Nothing there — create it.
    ln -s "$LAUNCHER_TARGET" "$LAUNCHER_PATH"
    ct_log "Created launcher symlink: $LAUNCHER_PATH → $LAUNCHER_TARGET"
    LAUNCHER_CREATED=1
fi

if [ "$LAUNCHER_CREATED" = "1" ]; then
    # Record in manifest so uninstall removes exactly this.
    printf 'launcher=%s\n' "$LAUNCHER_PATH" > "$INSTALL_MANIFEST"
    ct_log "Recorded launcher in $INSTALL_MANIFEST"
    # PATH guidance if the dir is not on PATH.
    case ":${PATH}:" in
        *":$LAUNCHER_DIR:"*) : ;;
        *)
            printf '\nNOTE: %s is not on your PATH.\n' "$LAUNCHER_DIR"
            printf 'Add this line to your shell profile (~/.bashrc, ~/.zshrc, etc.):\n'
            printf '    export PATH="%s:$PATH"\n\n' "$LAUNCHER_DIR"
            ;;
    esac
fi

# ---- 2. per-file symlinks ----------------------------------------------------

link_count=0
skip_count=0
overwrite_count=0

link_files_in() {
    # Per-file symlinks for one subdir name (e.g. "agents").
    # We mirror nested layout (e.g. lib/skills/pre-commit/SKILL.md →
    # ~/.claude/skills/pre-commit/SKILL.md).
    local sub="$1"
    local src_root="$YAKOS_ROOT_ABS/lib/$sub"
    local dst_root="$CLAUDE_DIR/$sub"

    [ -d "$src_root" ] || return 0
    mkdir -p "$dst_root"

    # find all files except .gitkeep
    while IFS= read -r src; do
        [ -n "$src" ] || continue
        local rel="${src#$src_root/}"
        local dst="$dst_root/$rel"
        mkdir -p "$(dirname -- "$dst")"

        if [ -L "$dst" ]; then
            local existing
            existing="$(ct_realpath "$dst")"
            case "$existing" in
                "$YAKOS_ROOT_ABS"/*)
                    if [ "$FORCE" = "1" ]; then
                        rm -f "$dst"
                        ln -s "$src" "$dst"
                        overwrite_count=$((overwrite_count + 1))
                    else
                        # Already a YakOS-owned symlink; refresh idempotently
                        # only if the target differs.
                        if [ "$existing" != "$src" ]; then
                            rm -f "$dst"
                            ln -s "$src" "$dst"
                            overwrite_count=$((overwrite_count + 1))
                        else
                            skip_count=$((skip_count + 1))
                        fi
                    fi
                    ;;
                *)
                    ct_log "skip: $dst is a symlink pointing outside YakOS ($existing); leaving alone"
                    skip_count=$((skip_count + 1))
                    ;;
            esac
        elif [ -e "$dst" ]; then
            ct_log "skip: $dst already exists and is not a symlink (use --force? we still wouldn't overwrite a real file; rename/remove manually)"
            skip_count=$((skip_count + 1))
        else
            ln -s "$src" "$dst"
            link_count=$((link_count + 1))
        fi
    done < <(find "$src_root" -type f ! -name '.gitkeep' 2>/dev/null)
}

for sub in agents skills rules playbooks; do
    link_files_in "$sub"
done

ct_log "Symlinks: $link_count created, $overwrite_count refreshed, $skip_count skipped"

# ---- 3. settings.json safe merge --------------------------------------------

merge_settings() {
    local key='CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS'
    local value='1'

    if [ ! -f "$SETTINGS_FILE" ]; then
        # Create with just the env block, mark that we own it.
        printf '{\n  "env": {\n    "%s": "%s"\n  }\n}\n' "$key" "$value" > "$SETTINGS_FILE"
        : > "$CREATED_MARKER"
        ct_log "Created $SETTINGS_FILE (marker: $CREATED_MARKER)"
        return 0
    fi

    if ! ct_json_valid "$SETTINGS_FILE"; then
        ct_die "$SETTINGS_FILE is not valid JSON. Aborting install. Fix or remove it and re-run."
    fi

    # Skip work if the env var is already set to the desired value AND
    # nothing else needs changing.
    local current
    current="$(jq -r ".env.${key} // empty" "$SETTINGS_FILE" 2>/dev/null || true)"
    if [ "$current" = "$value" ]; then
        ct_log "$SETTINGS_FILE already has env.$key = $value (no change)"
        return 0
    fi

    local ts backup
    ts="$(ct_iso_utc)"
    backup="${SETTINGS_FILE}.yakos-bak-${ts}"
    cp "$SETTINGS_FILE" "$backup"
    ct_log "Backup: $backup"

    ct_json_merge "$SETTINGS_FILE" \
        ".env = (.env // {}) + {\"${key}\": \"${value}\"}"
    ct_log "Merged env.$key into $SETTINGS_FILE"
}

merge_settings

# ---- 4. summary -------------------------------------------------------------

_launcher_note="not managed (foreign or real-file collision — see warnings above)"
if [ "$LAUNCHER_CREATED" = "1" ]; then
    _launcher_note="$LAUNCHER_PATH → $LAUNCHER_TARGET"
fi

cat <<EOF

YakOS install complete.
    Pointer:       $YAKOS_POINTER → $YAKOS_ROOT_ABS
    Launcher:      $_launcher_note
    Manifest:      $INSTALL_MANIFEST
    Symlinks:      $link_count created, $overwrite_count refreshed, $skip_count skipped
    Settings:      $SETTINGS_FILE

Next steps:
    yakos doctor                              Verify everything resolved
    yakos init <name> --project <path>        Bootstrap a project on top of YakOS

Auto-memory at $CLAUDE_DIR/projects/ was not touched.
EOF
