#!/usr/bin/env bash
# Purpose: Manage YakOS git hooks for the current project repo.
#   Subcommands: install | uninstall | status.
#   Installs the pre-push version gate into <repo>/.git/hooks/pre-push
#   (with a .framework-hash sibling so doctor can detect drift).
# Inputs:  $1 subcommand; --force to overwrite an existing hook.
# Outputs: stdout: human-readable status; stderr: errors.
# Reads:   $YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh
# Writes:  <repo>/.git/hooks/pre-push and .framework-hash sibling.
# Exit codes: 0 success, 2 user error, 3 git/repo error, 4 file error.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos git-hooks'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos git-hooks'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos git-hooks {install|uninstall|status} [--force]

Manage YakOS git hooks for the current project repo.

  install     Copy the pre-push version gate into <repo>/.git/hooks/pre-push.
              Refuses if a pre-push hook exists; use --force to overwrite a
              YakOS-owned hook (never overwrites a non-YakOS hook).
  uninstall   Remove the pre-push hook IFF it's YakOS-owned (verified by
              .framework-hash sibling). Refuses to remove non-YakOS hooks.
  status      Report whether the gate is installed and whether it's drift-
              free relative to the framework version.

Hooks honor YAKOS_GATE_DISABLE=1 (bypass + log) and `git push --no-verify`.
See lib/hooks/git/README.md for the full contract.
EOF
}

GATE_SRC="$YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh"

require_repo() {
    if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        ct_die "not inside a git repo (run from a project)"
    fi
    REPO_ROOT="$(git rev-parse --show-toplevel)"
    HOOK_DIR="$REPO_ROOT/.git/hooks"
}

cmd_install() {
    local force=0
    for arg in "$@"; do
        case "$arg" in
            --force) force=1 ;;
            *) ct_die "install: unknown arg: $arg" ;;
        esac
    done
    require_repo
    [ -f "$GATE_SRC" ] || ct_die "gate source not found: $GATE_SRC"
    mkdir -p "$HOOK_DIR"
    local dst="$HOOK_DIR/pre-push"
    local hash_sibling="${dst}.framework-hash"

    if [ -e "$dst" ] && [ "$force" != "1" ]; then
        # Is it a YakOS-owned hook?
        if [ -f "$hash_sibling" ]; then
            local current_hash existing_hash
            current_hash="$(ct_sha256 "$GATE_SRC")"
            existing_hash="$(cat "$hash_sibling" 2>/dev/null || echo "")"
            if [ "$current_hash" = "$existing_hash" ]; then
                ct_log "pre-push gate already installed at $dst (current version); nothing to do"
                return 0
            else
                ct_die "pre-push hook is YakOS-owned but stale (rerun with --force to refresh)"
            fi
        fi
        ct_die "pre-push hook exists at $dst and isn't YakOS-owned (rerun with --force to overwrite — destructive)"
    fi

    cp "$GATE_SRC" "$dst"
    chmod +x "$dst"
    ct_sha256 "$GATE_SRC" > "$hash_sibling"
    ct_log "pre-push gate installed at $dst"
    ct_log "  to override: YAKOS_GATE_DISABLE=1 git push   (logged to ~/.yakos/gate-log.ndjson)"
    ct_log "  to remove:   yakos git-hooks uninstall"
}

cmd_uninstall() {
    require_repo
    local dst="$HOOK_DIR/pre-push"
    local hash_sibling="${dst}.framework-hash"
    if [ ! -e "$dst" ]; then
        ct_log "pre-push hook not present at $dst; nothing to remove"
        return 0
    fi
    if [ ! -f "$hash_sibling" ]; then
        ct_die "pre-push hook at $dst isn't YakOS-owned (no .framework-hash); refusing to remove"
    fi
    rm -f "$dst" "$hash_sibling"
    ct_log "pre-push gate removed from $dst"
}

cmd_status() {
    require_repo
    local dst="$HOOK_DIR/pre-push"
    local hash_sibling="${dst}.framework-hash"
    if [ ! -e "$dst" ]; then
        printf '%s\n' "pre-push gate: NOT INSTALLED"
        printf '%s\n' "  install: yakos git-hooks install"
        return 0
    fi
    if [ ! -f "$hash_sibling" ]; then
        printf '%s\n' "pre-push gate: PRESENT but not YakOS-owned (no .framework-hash sibling)"
        return 0
    fi
    local current_hash existing_hash
    current_hash="$(ct_sha256 "$GATE_SRC")"
    existing_hash="$(cat "$hash_sibling" 2>/dev/null || echo "")"
    if [ "$current_hash" = "$existing_hash" ]; then
        printf '%s\n' "pre-push gate: INSTALLED (current version, no drift)"
    else
        printf '%s\n' "pre-push gate: INSTALLED but DRIFTED from framework"
        printf '%s\n' "  framework hash: $current_hash"
        printf '%s\n' "  installed hash: $existing_hash"
        printf '%s\n' "  to refresh: yakos git-hooks install --force"
    fi
}

cmd="${1:-}"
shift || true
case "$cmd" in
    install)   cmd_install "$@" ;;
    uninstall) cmd_uninstall "$@" ;;
    status)    cmd_status "$@" ;;
    -h|--help|"") usage ;;
    *) usage >&2; exit 2 ;;
esac
