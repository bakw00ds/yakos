#!/usr/bin/env bash
# Purpose: plugin.sh — manage external runtime adapters.
#
# Plugins live at ~/.yakos/plugins/<id>/ and provide a runtime.sh that
# implements the same contract as cli/lib/runtimes/<built-in>.sh
# (see cli/lib/runtimes/README.md). Once installed, the plugin's id
# becomes addressable via `yakos start --runtime <id>` and via agent
# frontmatter `runtime: <id>`.
#
# Usage:
#   yakos plugin install <git-url-or-local-path> [--id <id>] [--force]
#   yakos plugin list
#   yakos plugin remove <id>

set -eu

: "${YAKOS_ROOT:?plugin.sh: YAKOS_ROOT must be set}"
: "${YAKOS_LIB:?plugin.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

PLUGINS_DIR="$HOME/.yakos/plugins"

usage() {
    cat <<EOF
yakos plugin <subcommand> [args...]

Subcommands:
  install <source> [--id <id>] [--force]
                       Install a runtime plugin from <source>:
                         - git URL: https://… or git@…
                         - local path: a directory containing runtime.sh
                       --id overrides the inferred plugin id (default:
                       basename of source).
                       --force overwrites an existing plugin.

  list                 List installed plugins.

  remove <id>          Remove the plugin at ~/.yakos/plugins/<id>/.

Plugins live at ~/.yakos/plugins/<id>/runtime.sh.
The runtime.sh contract is documented in
  $YAKOS_ROOT/cli/lib/runtimes/README.md
and  $YAKOS_ROOT/docs/plugin-spec.md.

Examples:
  yakos plugin install https://github.com/user/yakos-cursor-runtime
  yakos plugin install ~/code/my-cursor-adapter --id cursor
  yakos plugin list
  yakos plugin remove cursor
EOF
}

plugin_validate() {
    local id="$1"
    local dir="$2"
    if [ ! -f "$dir/runtime.sh" ]; then
        ct_log "plugin '$id': missing required runtime.sh"
        return 1
    fi
    # Sanity-check that the adapter exports the expected functions.
    # This is a soft check — the runtime is sourced lazily, so a
    # syntax error or missing function only surfaces on use.
    local missing=0
    local v
    for v in id check_cli check_auth capabilities materialize_agents \
             cleanup_agents launch dispatch; do
        if ! grep -qE "^[[:space:]]*yk_rt_${id}_${v}[[:space:]]*\\(" "$dir/runtime.sh"; then
            ct_log "plugin '$id': missing yk_rt_${id}_${v}() in runtime.sh"
            missing=$((missing + 1))
        fi
    done
    [ "$missing" = "0" ]
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    install|list|remove) ;;
    *) ct_die "plugin: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- subcommand: list -----------------------------------------------------

if [ "$SUB" = "list" ]; then
    if [ ! -d "$PLUGINS_DIR" ]; then
        echo "no plugins installed (dir $PLUGINS_DIR does not exist)"
        exit 0
    fi
    echo "yakos plugins ($PLUGINS_DIR):"
    found=0
    for d in "$PLUGINS_DIR"/*; do
        [ -d "$d" ] || continue
        id="$(basename -- "$d")"
        if [ -f "$d/runtime.sh" ]; then
            ver="(unversioned)"
            [ -f "$d/VERSION" ] && ver="$(cat "$d/VERSION")"
            printf '  %-20s %s\n' "$id" "$ver"
            found=1
        else
            printf '  %-20s (no runtime.sh — broken plugin)\n' "$id"
        fi
    done
    [ "$found" = "0" ] && echo "  (none)"
    exit 0
fi

# ---- subcommand: install --------------------------------------------------

if [ "$SUB" = "install" ]; then
    SOURCE=""
    ID_OVERRIDE=""
    FORCE=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -h|--help) usage; exit 0 ;;
            --id) shift; ID_OVERRIDE="$1" ;;
            --id=*) ID_OVERRIDE="${1#--id=}" ;;
            --force) FORCE=1 ;;
            -*) ct_die "plugin install: unknown flag '$1'" ;;
            *)
                if [ -z "$SOURCE" ]; then SOURCE="$1"
                else ct_die "plugin install: too many positional args"
                fi
                ;;
        esac
        shift
    done
    [ -n "$SOURCE" ] || { usage >&2; ct_die "plugin install: <source> required"; }

    mkdir -p "$PLUGINS_DIR"

    # Infer id from source if not overridden.
    if [ -n "$ID_OVERRIDE" ]; then
        ID="$ID_OVERRIDE"
    else
        case "$SOURCE" in
            *.git)        ID="$(basename -- "${SOURCE%.git}")" ;;
            http*|git@*)  ID="$(basename -- "$SOURCE" .git)" ;;
            *)            ID="$(basename -- "$SOURCE")" ;;
        esac
        # Strip a "yakos-" or "yakos-runtime-" prefix when present.
        ID="${ID#yakos-runtime-}"
        ID="${ID#yakos-}"
    fi

    case "$ID" in
        *[!A-Za-z0-9_-]*) ct_die "plugin install: id '$ID' must be alphanumeric (-_)" ;;
        claude|codex|gemini)
            ct_die "plugin install: '$ID' shadows a built-in runtime; pass --id <other>"
            ;;
    esac

    DEST="$PLUGINS_DIR/$ID"
    if [ -e "$DEST" ] && [ "$FORCE" != "1" ]; then
        ct_die "plugin install: $DEST already exists (use --force to overwrite)"
    fi
    [ -e "$DEST" ] && rm -rf "$DEST"

    case "$SOURCE" in
        http*|git@*)
            command -v git >/dev/null || ct_die "plugin install: git required for URL sources"
            git clone --depth 1 "$SOURCE" "$DEST" || ct_die "plugin install: git clone failed"
            ;;
        *)
            [ -d "$SOURCE" ] || ct_die "plugin install: local path '$SOURCE' is not a directory"
            cp -R "$SOURCE" "$DEST"
            ;;
    esac

    if ! plugin_validate "$ID" "$DEST"; then
        ct_log "plugin install: validation failed; rolling back"
        rm -rf "$DEST"
        exit 1
    fi
    chmod +x "$DEST/runtime.sh" 2>/dev/null || true
    echo "installed plugin: $ID at $DEST"
    echo "  Use via: yakos start --runtime $ID  /  yakos dispatch <agent> ... --runtime $ID"
    echo "  Or set in agent frontmatter: runtime: $ID"
    exit 0
fi

# ---- subcommand: remove ---------------------------------------------------

if [ "$SUB" = "remove" ]; then
    ID="${1:-}"
    [ -n "$ID" ] || ct_die "plugin remove: <id> required"
    case "$ID" in
        claude|codex|gemini) ct_die "plugin remove: '$ID' is a built-in, cannot remove" ;;
    esac
    DEST="$PLUGINS_DIR/$ID"
    [ -d "$DEST" ] || ct_die "plugin remove: $DEST not found"
    rm -rf "$DEST"
    echo "removed plugin: $ID"
    exit 0
fi
