#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
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
                                 native location.
  diff <runtime> [<project>]     Diff yakOS canonical memory store
                                 against the runtime's mirror; show
                                 added / removed / modified.

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
    list|show|put|migrate-from-claude|sync|diff) ;;
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
        codex)
            # codex reads <project>/.codex/AGENTS.md as system context
            # (32 KiB cap). Bracket yakos-owned content with markers so
            # re-syncs replace just that section without clobbering the
            # operator's own AGENTS.md.
            target="$project_repo/.codex/AGENTS.md"
            mkdir -p "$project_repo/.codex"
            marker_start='<!-- yakos-memory-start (managed; do not edit by hand) -->'
            marker_end='<!-- yakos-memory-end -->'

            body_tmp="$(mktemp -t yakos-mem-codex.XXXXXX)"
            {
                printf '%s\n' "$marker_start"
                printf '\n# yakOS memory (synced from %s)\n\n' "$src"
                if [ -f "$src/MEMORY.md" ]; then
                    printf '## Memory index\n\n'
                    sed -n '1,50p' "$src/MEMORY.md"
                    printf '\n'
                fi
                printf '## Memories\n\n'
                while IFS= read -r f; do
                    [ -n "$f" ] || continue
                    base="$(basename -- "$f")"
                    case "$base" in MEMORY.md) continue ;; esac
                    printf '### %s\n\n' "$base"
                    cat "$f"
                    printf '\n\n'
                done < <(find "$src" -maxdepth 1 -type f -name '*.md' ! -name 'MEMORY.md' 2>/dev/null | sort)
                printf '%s\n' "$marker_end"
            } > "$body_tmp"

            # Truncate to 28 KiB to leave headroom under codex's 32 KiB
            # AGENTS.md merge cap.
            body_size="$(wc -c < "$body_tmp" | tr -d ' ')"
            if [ "$body_size" -gt 28672 ]; then
                head -c 28000 "$body_tmp" > "${body_tmp}.t" && mv "${body_tmp}.t" "$body_tmp"
                printf '\n... (truncated to fit 32 KiB AGENTS.md cap; full memory at %s)\n%s\n' \
                    "$src" "$marker_end" >> "$body_tmp"
                ct_log "memory sync codex: truncated $body_size B → 28 KiB to fit codex cap"
            fi

            # Splice into AGENTS.md, replacing any prior yakos block.
            if [ ! -f "$target" ]; then
                cp "$body_tmp" "$target"
            else
                merged_tmp="$(mktemp -t yakos-mem-codex-merged.XXXXXX)"
                # Strip any existing yakos block via awk; then append fresh.
                awk -v start="$marker_start" -v end="$marker_end" '
                    $0 == start { in_yakos = 1; next }
                    $0 == end   { in_yakos = 0; next }
                    !in_yakos   { print }
                ' "$target" > "$merged_tmp"
                printf '\n' >> "$merged_tmp"
                cat "$body_tmp" >> "$merged_tmp"
                mv "$merged_tmp" "$target"
            fi
            rm -f "$body_tmp" 2>/dev/null
            echo "synced yakOS memory into $target"
            ;;
        gemini)
            # gemini accepts a system-prompt override via GEMINI_SYSTEM_MD
            # env var pointing at a markdown file. Synthesize that file
            # under <project>/.gemini/yakos-system.md; the gemini adapter's
            # launch path will export GEMINI_SYSTEM_MD when it sees the
            # file. The operator can also `export GEMINI_SYSTEM_MD=...`
            # manually.
            target="$project_repo/.gemini/yakos-system.md"
            mkdir -p "$project_repo/.gemini"
            {
                printf '# yakOS-injected system context for %s\n\n' "$PROJECT"
                printf 'Auto-synthesized %s from %s — do not edit by hand.\n\n' "$(ct_iso_now_z)" "$src"
                if [ -f "$src/MEMORY.md" ]; then
                    printf '## Memory index\n\n'
                    cat "$src/MEMORY.md"
                    printf '\n'
                fi
                printf '## Memories\n\n'
                while IFS= read -r f; do
                    [ -n "$f" ] || continue
                    base="$(basename -- "$f")"
                    case "$base" in MEMORY.md) continue ;; esac
                    printf '### %s\n\n' "$base"
                    cat "$f"
                    printf '\n\n'
                done < <(find "$src" -maxdepth 1 -type f -name '*.md' ! -name 'MEMORY.md' 2>/dev/null | sort)
            } > "$target"
            cat <<EOF
synced yakOS memory into $target

Activate by exporting GEMINI_SYSTEM_MD before launching gemini:
  export GEMINI_SYSTEM_MD="$target"

The gemini adapter's launch path (yakos start --runtime gemini) does
this automatically. Manual gemini sessions need the export.
EOF
            ;;
        *) ct_die "memory sync: unknown runtime '$RUNTIME' (claude|codex|gemini)" ;;
    esac
    exit 0
fi

# ---- subcommand: diff ------------------------------------------------------

if [ "$SUB" = "diff" ]; then
    RUNTIME="${1:-}"
    PROJECT="${2:-$(infer_project || true)}"
    [ -n "$RUNTIME" ] || ct_die "memory diff: <runtime> required"
    [ -n "$PROJECT" ] || ct_die "memory diff: project name required"

    cd_path="$HOME/agent-control/$PROJECT/.project-path"
    [ -f "$cd_path" ] || ct_die "memory diff: $cd_path missing"
    project_repo="$(head -1 "$cd_path")"
    src="$(mem_dir_for_project "$PROJECT")"
    [ -d "$src" ] || { echo "no yakos memory at $src — nothing to diff"; exit 0; }

    echo "yakos memory diff — $PROJECT vs $RUNTIME mirror"
    echo "  source:  $src"

    case "$RUNTIME" in
        claude)
            encoded="$(claude_encode_path "$project_repo")"
            target="$HOME/.claude/projects/$encoded"
            echo "  mirror:  $target"
            echo
            if [ ! -d "$target" ]; then
                echo "  (mirror does not exist; sync has not run)"
                exit 0
            fi
            yakos_n=0
            mirror_n=0
            same=0
            modified=0
            only_yakos=()
            only_mirror=()
            modified_files=()

            while IFS= read -r f; do
                [ -n "$f" ] || continue
                base="$(basename -- "$f")"
                yakos_n=$((yakos_n + 1))
                if [ -f "$target/$base" ]; then
                    if cmp -s "$f" "$target/$base"; then
                        same=$((same + 1))
                    else
                        modified=$((modified + 1))
                        modified_files+=("$base")
                    fi
                else
                    only_yakos+=("$base")
                fi
            done < <(find "$src" -maxdepth 1 -type f -name '*.md' 2>/dev/null)

            while IFS= read -r f; do
                [ -n "$f" ] || continue
                base="$(basename -- "$f")"
                mirror_n=$((mirror_n + 1))
                [ -f "$src/$base" ] || only_mirror+=("$base")
            done < <(find "$target" -maxdepth 1 -type f -name '*.md' 2>/dev/null)

            echo "  yakos store: $yakos_n file(s); claude mirror: $mirror_n file(s); same: $same; modified: $modified"
            if [ "${#only_yakos[@]}" -gt 0 ]; then
                echo "  only in yakos store (run 'yakos memory sync claude $PROJECT'):"
                for b in "${only_yakos[@]}"; do printf '    + %s\n' "$b"; done
            fi
            if [ "${#only_mirror[@]}" -gt 0 ]; then
                echo "  only in claude mirror (claude wrote them; not in yakos store):"
                for b in "${only_mirror[@]}"; do printf '    - %s\n' "$b"; done
            fi
            if [ "${#modified_files[@]}" -gt 0 ]; then
                echo "  modified (yakos and mirror differ):"
                for b in "${modified_files[@]}"; do printf '    ~ %s\n' "$b"; done
            fi
            ;;
        codex)
            target="$project_repo/.codex/AGENTS.md"
            echo "  mirror:  $target (yakos block only)"
            echo
            if [ ! -f "$target" ]; then
                echo "  (mirror does not exist; sync has not run)"
                exit 0
            fi
            block_present=0
            grep -qF '<!-- yakos-memory-start' "$target" 2>/dev/null && block_present=1
            if [ "$block_present" = "0" ]; then
                echo "  (no yakos-memory block in AGENTS.md; sync has not run or it was removed)"
                exit 0
            fi
            mirror_age="$(stat -f '%m' "$target" 2>/dev/null || stat -c '%Y' "$target" 2>/dev/null || echo 0)"
            src_newest=0
            while IFS= read -r f; do
                [ -n "$f" ] || continue
                ft="$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f" 2>/dev/null || echo 0)"
                [ "$ft" -gt "$src_newest" ] && src_newest="$ft"
            done < <(find "$src" -maxdepth 1 -type f -name '*.md' 2>/dev/null)
            if [ "$src_newest" -gt "$mirror_age" ]; then
                echo "  STALE — yakos memory was modified after the last sync"
                echo "  re-run: yakos memory sync codex $PROJECT"
            else
                echo "  in sync (mirror modified at $mirror_age; newest yakos file at $src_newest)"
            fi
            ;;
        gemini)
            target="$project_repo/.gemini/yakos-system.md"
            echo "  mirror:  $target"
            echo
            if [ ! -f "$target" ]; then
                echo "  (mirror does not exist; sync has not run)"
                exit 0
            fi
            mirror_age="$(stat -f '%m' "$target" 2>/dev/null || stat -c '%Y' "$target" 2>/dev/null || echo 0)"
            src_newest=0
            while IFS= read -r f; do
                [ -n "$f" ] || continue
                ft="$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f" 2>/dev/null || echo 0)"
                [ "$ft" -gt "$src_newest" ] && src_newest="$ft"
            done < <(find "$src" -maxdepth 1 -type f -name '*.md' 2>/dev/null)
            if [ "$src_newest" -gt "$mirror_age" ]; then
                echo "  STALE — yakos memory was modified after the last sync"
                echo "  re-run: yakos memory sync gemini $PROJECT"
            else
                echo "  in sync"
            fi
            ;;
        *) ct_die "memory diff: unknown runtime '$RUNTIME' (claude|codex|gemini)" ;;
    esac
    exit 0
fi
