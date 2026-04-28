#!/usr/bin/env bash
# install.sh — first-time install for YakOS.
#
# Behavior:
#   1. Per-FILE symlinks from $HOME/.claude/{agents,skills,rules,playbooks}/
#      → $YAKOS_ROOT/lib/<subdir>/<file>. Existing user files in those
#      directories are preserved (we only create new symlinks, never
#      overwrite without --force).
#   2. Writes $HOME/.yakos containing the absolute path to YAKOS_ROOT
#      (used by uninstall to identify our symlinks).
#   3. Safely merges the experimental-agent-teams env var into
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

YAKOS_ROOT_ABS="$(ct_realpath "$YAKOS_ROOT")"

ct_log "Installing YakOS from $YAKOS_ROOT_ABS into $CLAUDE_DIR"

mkdir -p "$CLAUDE_DIR"

# ---- 0. preflight: bail before touching state if existing settings.json is invalid

if [ -f "$SETTINGS_FILE" ] && ! ct_json_valid "$SETTINGS_FILE"; then
    ct_die "$SETTINGS_FILE is not valid JSON. Aborting install. Fix or remove it and re-run."
fi

# ---- 1. write the pointer file -----------------------------------------------

printf '%s\n' "$YAKOS_ROOT_ABS" > "$YAKOS_POINTER"
ct_log "Wrote $YAKOS_POINTER"

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

cat <<EOF

YakOS install complete.
    Pointer:       $YAKOS_POINTER → $YAKOS_ROOT_ABS
    Symlinks:      $link_count created, $overwrite_count refreshed, $skip_count skipped
    Settings:      $SETTINGS_FILE

Next steps:
    yakos doctor                              Verify everything resolved
    yakos init <name> --project <path>        Bootstrap a project on top of YakOS

Auto-memory at $CLAUDE_DIR/projects/ was not touched.
EOF
