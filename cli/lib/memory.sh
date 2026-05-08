#!/usr/bin/env bash
# memory.sh — portable yakOS memory across runtimes.
#
# Purpose: store durable operator-private observations at
# ~/.yakos-state/memory/<project>/ as the single source of truth, then
# materialize per-runtime mirrors so claude/codex/gemini sessions all
# see the same memory.
#
# v0.5 ships: list, show, put, migrate-from-claude, sync claude.
# v0.5.1+ adds: sync codex, sync gemini, auto-sync on start.
# See docs/memory-portability.md for the design and threat model.

set -eu

: "${YAKOS_LIB:?memory.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos memory <subcommand> [args...]

Subcommands:
  list [<project>]               List memory keys for the project.
  show <key> [<project>]         Print a memory's body (markdown).
  put <key> <file> [<project>]   Add or replace a memory from a file.
  migrate-from-claude [<project>]  Copy claude's auto-memory into yakOS.
  sync <runtime> [<project>]     Materialize yakOS memory into runtime
                                 native location. v0.5: claude only.

If <project> is omitted, infers from cwd (matches `yakos start`).

Examples:
  yakos memory list myapp
  yakos memory show user_role myapp
  yakos memory put feedback_tests /tmp/note.md myapp
  yakos memory migrate-from-claude myapp
  yakos memory sync claude myapp
EOF
}

infer_project() {
    # Mirrors the inference in start.sh / dispatch.sh: cwd inside
    # ~/agent-control/<name>/ → name; cwd inside a tracked repo with
    # a matching .project-path → the matching name.
    local cwd_real ac_root
    cwd_real="$(ct_realpath "$PWD")"
    ac_root="$HOME/agent-control"
    case "$cwd_real" in
        "$ac_root"/*)
            local rest="${cwd_real#$ac_root/}"
            printf '%s\n' "${rest%%/*}"
            return 0
            ;;
    esac
    if [ -d "$ac_root" ]; then
        for cd_path in "$ac_root"/*/.project-path; do
            [ -f "$cd_path" ] || continue
            local p pr
            p="$(head -1 "$cd_path")"
            pr="$(ct_realpath "$p" 2>/dev/null || true)"
            case "$cwd_real" in
                "$pr"|"$pr"/*)
                    basename -- "$(dirname -- "$cd_path")"
                    return 0
                    ;;
            esac
        done
    fi
    return 1
}

# Encode a project repo path the way claude does for ~/.claude/projects/<encoded>/.
# Used by migrate-from-claude. claude's encoding: replace / with -.
claude_encode_path() {
    printf '%s\n' "$1" | tr '/' '-' | sed 's/^-//'
}

mem_dir_for_project() {
    local name="$1"
    printf '%s\n' "$HOME/.yakos-state/memory/$name"
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    list|show|put|migrate-from-claude|sync) ;;
    *) ct_die "memory: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- subcommand: list ------------------------------------------------------

if [ "$SUB" = "list" ]; then
    PROJECT="${1:-$(infer_project || true)}"
    [ -n "$PROJECT" ] || ct_die "memory list: project name required (or run from inside a project)"
    dir="$(mem_dir_for_project "$PROJECT")"
    if [ ! -d "$dir" ]; then
        echo "(no memory yet for '$PROJECT' at $dir)"
        exit 0
    fi
    echo "yakos memory — $PROJECT  ($dir)"
    echo
    if [ -f "$dir/MEMORY.md" ]; then
        echo "  Index (MEMORY.md):"
        sed 's/^/    /' "$dir/MEMORY.md" | head -50
        echo
    fi
    echo "  Files:"
    find "$dir" -maxdepth 1 -type f -name '*.md' ! -name 'MEMORY.md' \
        | sort | sed "s|$dir/|    |"
    exit 0
fi

# ---- subcommand: show ------------------------------------------------------

if [ "$SUB" = "show" ]; then
    KEY="${1:-}"
    PROJECT="${2:-$(infer_project || true)}"
    [ -n "$KEY" ] || ct_die "memory show: <key> required"
    [ -n "$PROJECT" ] || ct_die "memory show: project name required"
    dir="$(mem_dir_for_project "$PROJECT")"
    candidate="$dir/${KEY}.md"
    [ -f "$candidate" ] || candidate="$dir/${KEY}"
    [ -f "$candidate" ] || ct_die "memory show: '$KEY' not found in $dir"
    cat "$candidate"
    exit 0
fi

# ---- subcommand: put -------------------------------------------------------

if [ "$SUB" = "put" ]; then
    KEY="${1:-}"
    FILE="${2:-}"
    PROJECT="${3:-$(infer_project || true)}"
    [ -n "$KEY" ] || ct_die "memory put: <key> required"
    [ -n "$FILE" ] || ct_die "memory put: <file> required"
    [ -f "$FILE" ] || ct_die "memory put: $FILE not found"
    [ -n "$PROJECT" ] || ct_die "memory put: project name required"

    dir="$(mem_dir_for_project "$PROJECT")"
    mkdir -p "$dir"
    target="$dir/${KEY}.md"

    if [ -f "$target" ]; then
        bak_ts="$(ct_iso_utc)"
        cp "$target" "$target.yakos-bak-$bak_ts"
    fi
    cp "$FILE" "$target"
    echo "wrote $target"

    # Update MEMORY.md index (append if not already there).
    index="$dir/MEMORY.md"
    if [ ! -f "$index" ]; then
        printf '# Memory index for %s\n\n' "$PROJECT" > "$index"
    fi
    if ! grep -qF "[$(basename -- "$target")]" "$index" 2>/dev/null; then
        printf -- '- [%s](%s)\n' "$KEY" "$(basename -- "$target")" >> "$index"
    fi
    exit 0
fi

# ---- subcommand: migrate-from-claude ---------------------------------------

if [ "$SUB" = "migrate-from-claude" ]; then
    PROJECT="${1:-$(infer_project || true)}"
    [ -n "$PROJECT" ] || ct_die "memory migrate-from-claude: project name required"

    cd_path="$HOME/agent-control/$PROJECT/.project-path"
    [ -f "$cd_path" ] || ct_die "memory migrate-from-claude: $cd_path missing — run 'yakos init' first"
    project_repo="$(head -1 "$cd_path")"
    encoded="$(claude_encode_path "$project_repo")"
    src="$HOME/.claude/projects/$encoded"

    if [ ! -d "$src" ]; then
        echo "no claude memory at $src to migrate"
        exit 0
    fi

    dir="$(mem_dir_for_project "$PROJECT")"
    mkdir -p "$dir"

    local_count=0
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        target="$dir/$(basename -- "$f")"
        if [ -f "$target" ]; then
            ct_log "skip (exists): $target"
            continue
        fi
        cp "$f" "$target"
        local_count=$((local_count + 1))
    done < <(find "$src" -maxdepth 1 -type f -name '*.md' 2>/dev/null)

    echo "migrated $local_count memory file(s) from $src to $dir"
    exit 0
fi

# ---- subcommand: sync ------------------------------------------------------

if [ "$SUB" = "sync" ]; then
    RUNTIME="${1:-}"
    PROJECT="${2:-$(infer_project || true)}"
    [ -n "$RUNTIME" ] || ct_die "memory sync: <runtime> required"
    [ -n "$PROJECT" ] || ct_die "memory sync: project name required"

    cd_path="$HOME/agent-control/$PROJECT/.project-path"
    [ -f "$cd_path" ] || ct_die "memory sync: $cd_path missing"
    project_repo="$(head -1 "$cd_path")"
    src="$(mem_dir_for_project "$PROJECT")"
    [ -d "$src" ] || { echo "no yakos memory at $src — nothing to sync"; exit 0; }

    case "$RUNTIME" in
        claude)
            encoded="$(claude_encode_path "$project_repo")"
            target="$HOME/.claude/projects/$encoded"
            mkdir -p "$target"
            count=0
            while IFS= read -r f; do
                [ -n "$f" ] || continue
                base="$(basename -- "$f")"
                # Don't clobber claude-side edits without --force; check mtime.
                if [ -f "$target/$base" ]; then
                    if cmp -s "$f" "$target/$base"; then continue; fi
                    ct_log "claude-side $target/$base differs from yakos-side; skipping (use --force to override)"
                    continue
                fi
                cp "$f" "$target/$base"
                count=$((count + 1))
            done < <(find "$src" -maxdepth 1 -type f -name '*.md' 2>/dev/null)
            echo "synced $count file(s) → $target"
            ;;
        codex|gemini)
            cat <<EOF
memory sync $RUNTIME: not yet implemented in v0.5.0.
Planned for v0.5.1 (see docs/memory-portability.md):
  codex:  appends yakOS memory index + key files into <project>/.codex/AGENTS.md
  gemini: synthesizes <project>/.gemini/system.md and points GEMINI_SYSTEM_MD at it

For now, the project's source-of-truth memory is at:
  $src
EOF
            ;;
        *) ct_die "memory sync: unknown runtime '$RUNTIME' (claude|codex|gemini)" ;;
    esac
    exit 0
fi
