#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: soul.sh — operator-personal soul files (Plan 3 M1 / Capability A).
#
# A soul is the operator's personal identity/preferences file. yakOS reads
# it at session start and prepends it to the LEAD agent's system prompt
# (not specialists — see framework-internal-plan.md §3.3).
#
# Two layers (project shadows global, per Phase 1.5 §17):
#   ~/.yakos-state/soul/global.md             — user-global; same across projects
#   ~/.yakos-state/soul/<project-slug>.md     — per-project layer
#
# Subcommands:
#   yakos soul show [layer]                   — print current soul (default: global)
#   yakos soul edit [layer]                   — open in $EDITOR
#   yakos soul history [layer]                — list version snapshots
#   yakos soul revert <version> [layer]       — revert to a snapshot
#   yakos soul approve <slug>                 — apply proposed edit (M1+ when librarian runs)
#   yakos soul reject  <slug>                 — discard proposed edit
#   yakos soul pending                        — list pending edits in current session
#
# Reads:  ~/.yakos-state/soul/{global,<project-slug>}.md
#         ~/.yakos-state/soul/history/*.md
#         <work>/current/soul-proposed-edits.md
# Writes: same, plus ~/.yakos-state/soul/history/<layer>-<iso>.md (snapshots)

set -eu

: "${YAKOS_LIB:?soul.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

SOUL_DIR="$HOME/.yakos-state/soul"
HISTORY_DIR="$SOUL_DIR/history"
TEMPLATE="$YAKOS_ROOT/lib/settings/soul.template.md"

_soul_resolve_project_slug() {
    # Try to resolve the current project slug. Reads $YAKOS_PROJECT_DIR if
    # set; otherwise looks for the most recently active control dir.
    if [ -n "${YAKOS_PROJECT_DIR:-}" ]; then
        basename -- "$YAKOS_PROJECT_DIR"
        return
    fi
    local ac_root="$HOME/agent-control"
    [ -d "$ac_root" ] || { echo ""; return; }
    local cwd_real
    cwd_real="$(cd "$PWD" 2>/dev/null && pwd -P)"
    case "$cwd_real" in
        "$ac_root"/*)
            local rest="${cwd_real#$ac_root/}"
            printf '%s\n' "${rest%%/*}"
            return
            ;;
    esac
    # Fall back to walking .project-path files.
    local cd_path proj proj_real
    for cd_path in "$ac_root"/*/.project-path; do
        [ -f "$cd_path" ] || continue
        proj="$(head -1 "$cd_path" 2>/dev/null)"
        proj_real="$(cd "$proj" 2>/dev/null && pwd -P || true)"
        [ -n "$proj_real" ] || continue
        case "$cwd_real" in
            "$proj_real"|"$proj_real"/*)
                basename -- "$(dirname -- "$cd_path")"
                return
                ;;
        esac
    done
}

_soul_resolve_layer_file() {
    # Map layer arg to a file path. Defaults to global.
    local layer="${1:-global}"
    case "$layer" in
        global) printf '%s/global.md\n' "$SOUL_DIR" ;;
        project)
            local slug
            slug="$(_soul_resolve_project_slug)"
            [ -n "$slug" ] || ct_die "soul: cannot resolve project layer (no active project detected)"
            printf '%s/%s.md\n' "$SOUL_DIR" "$slug"
            ;;
        *) ct_die "soul: unknown layer '$layer' (use 'global' or 'project')" ;;
    esac
}

_soul_snapshot() {
    # Snapshot a layer file to history before overwriting it.
    local layer="$1" file
    file="$(_soul_resolve_layer_file "$layer")"
    [ -f "$file" ] || return 0
    mkdir -p "$HISTORY_DIR"
    local ts
    ts="$(ct_iso_utc)"
    cp "$file" "$HISTORY_DIR/${layer}-${ts}.md"
}

cmd_show() {
    local layer="${1:-global}" file
    file="$(_soul_resolve_layer_file "$layer")"
    if [ ! -f "$file" ]; then
        ct_log "soul: no soul file at $file (run 'yakos soul edit $layer' to create)"
        return 0
    fi
    cat "$file"
}

cmd_edit() {
    local layer="${1:-global}" file
    file="$(_soul_resolve_layer_file "$layer")"
    mkdir -p "$SOUL_DIR"

    # If file doesn't exist, seed from template.
    if [ ! -f "$file" ]; then
        if [ -f "$TEMPLATE" ]; then
            cp "$TEMPLATE" "$file"
            # Fill in {{layer}} and {{ts}} sentinels if present.
            ct_sed_inplace "s/{{layer}}/${layer}/g" "$file"
            ct_sed_inplace "s/{{ts}}/$(ct_iso_now_z)/g" "$file"
        else
            # Bare default if template missing.
            printf '# Soul (%s) — %s\n\n## About me\n\n## Voice / tone preferences\n\n## Recurring concerns\n\n## Stack expertise\n\n' \
                "$layer" "$(id -un)" > "$file"
        fi
    fi

    # Snapshot before edit (so revert works even if operator forgets to confirm).
    _soul_snapshot "$layer"

    # Open in $EDITOR (defaults to vi).
    local editor="${EDITOR:-${VISUAL:-vi}}"
    "$editor" "$file"
    ct_log "soul: edited $file"
}

cmd_history() {
    local layer="${1:-global}"
    [ -d "$HISTORY_DIR" ] || { echo "(no history)"; return 0; }
    find "$HISTORY_DIR" -maxdepth 1 -type f -name "${layer}-*.md" 2>/dev/null | sort | while read -r snap; do
        local ts size
        ts="$(basename "$snap" | sed -e "s/^${layer}-//" -e 's/\.md$//')"
        size="$(wc -c < "$snap" 2>/dev/null | tr -d ' ')"
        printf '  %s  (%s bytes)\n' "$ts" "$size"
    done
}

cmd_revert() {
    local version="$1" layer="${2:-global}" file
    file="$(_soul_resolve_layer_file "$layer")"
    local snap="$HISTORY_DIR/${layer}-${version}.md"
    [ -f "$snap" ] || ct_die "soul: no snapshot '${layer}-${version}.md' in $HISTORY_DIR"

    # Snapshot current before reverting.
    _soul_snapshot "$layer"
    cp "$snap" "$file"
    ct_log "soul: reverted $layer to $version"
}

cmd_pending() {
    # List pending edits from the librarian's proposed-edits file in
    # the active session's scratchpad.
    local slug current_dir pending
    slug="$(_soul_resolve_project_slug)"
    [ -n "$slug" ] || ct_die "soul: no active project session detected"
    current_dir="$HOME/agent-control/$slug/work/current"
    pending="$current_dir/soul-proposed-edits.md"
    if [ ! -f "$pending" ]; then
        echo "(no pending soul-edit proposals)"
        return 0
    fi
    grep -E '^## edit: ' "$pending" 2>/dev/null || echo "(no pending edits in $pending)"
}

cmd_approve() {
    ct_log "soul approve: not yet implemented (M1 ships read/edit/history; approve/reject deferred to when librarian is dispatched)"
    ct_log "      proposed-edit format: see librarian agent + framework-internal-plan.md §3.4"
    exit 0
}

cmd_reject() {
    ct_log "soul reject: not yet implemented (same status as 'soul approve' above)"
    exit 0
}

case "${1:-help}" in
    show)     shift; cmd_show "$@" ;;
    edit)     shift; cmd_edit "$@" ;;
    history)  shift; cmd_history "$@" ;;
    revert)   shift; cmd_revert "$@" ;;
    pending)  shift; cmd_pending "$@" ;;
    approve)  shift; cmd_approve "$@" ;;
    reject)   shift; cmd_reject "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos soul — operator-personal soul files (loaded by lead at session start)

  yakos soul show [global|project]              # print current
  yakos soul edit [global|project]              # open in \$EDITOR (creates from template if absent)
  yakos soul history [global|project]           # list version snapshots
  yakos soul revert <version> [global|project]  # revert to snapshot
  yakos soul pending                            # list librarian-proposed edits in current session
  yakos soul approve <edit-slug>                # apply pending edit (M1+ when librarian dispatched)
  yakos soul reject <edit-slug>                 # discard pending edit

Soul files live at ~/.yakos-state/soul/{global,<project-slug>}.md
Splicing into the lead's system prompt happens at session start
(see cli/lib/agents-compose.sh::yk_agents_apply_soul).
EOF
        ;;
    *) ct_die "yakos soul: unknown subcommand '$1' (try 'yakos soul help')" ;;
esac
