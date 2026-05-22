#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: kanban.sh — manage <work>/current/kanban.md as a 3-column board.
#
# Subcommands:
#   yakos kanban                       # render TUI 3 columns
#   yakos kanban --html [<out>]        # render static HTML
#   yakos kanban add "<title>"         # append to TODO
#   yakos kanban move <id> <col>       # move task between columns
#   yakos kanban done <id>             # shortcut: move <id> to DONE
#
# Reads/writes: <work>/current/kanban.md

set -eu

: "${YAKOS_LIB:?kanban.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/paths.sh"

_kanban_file() {
    printf '%s/kanban.md' "$(yakos_current_dir)"
}

_kanban_seed() {
    # Create empty board if missing.
    local file
    file="$(_kanban_file)"
    [ -f "$file" ] && return 0
    cat > "$file" <<EOF
# Kanban — $(basename "$(yakos_current_dir | xargs dirname | xargs dirname)") · session started $(ct_iso_now_z)

## TODO

## IN PROGRESS

## DONE
EOF
}

_kanban_next_id() {
    # Find highest existing task id, return next.
    local file max
    file="$(_kanban_file)"
    max="$(grep -oE 'K-[0-9]+' "$file" 2>/dev/null | sed 's/K-//' | sort -n | tail -1)"
    [ -z "$max" ] && max=0
    printf 'K-%d\n' "$((max + 1))"
}

cmd_render_tui() {
    local file
    file="$(_kanban_file)"
    [ -f "$file" ] || { _kanban_seed; }

    # Extract each column. Use awk to capture sections between H2s.
    local todo in_prog done_col
    todo="$(awk '/^## TODO/{flag=1;next}/^## /{flag=0}flag' "$file" | grep -E '^-' | head -20)"
    in_prog="$(awk '/^## IN PROGRESS/{flag=1;next}/^## /{flag=0}flag' "$file" | grep -E '^-' | head -20)"
    done_col="$(awk '/^## DONE/{flag=1;next}/^## /{flag=0}flag' "$file" | grep -E '^-' | head -20)"

    # Simple side-by-side rendering. TODO(M4): proper ANSI 3-column layout
    # with column width auto-sized to terminal. For draft, vertical sections.
    cat <<EOF

╭─ TODO ──────────────────────────────────────────╮
${todo:-  (empty)}
╰─────────────────────────────────────────────────╯

╭─ IN PROGRESS ───────────────────────────────────╮
${in_prog:-  (empty)}
╰─────────────────────────────────────────────────╯

╭─ DONE ──────────────────────────────────────────╮
${done_col:-  (empty)}
╰─────────────────────────────────────────────────╯

EOF
}

cmd_render_html() {
    local out="${1:-$(yakos_current_dir)/kanban.html}"
    local file
    file="$(_kanban_file)"
    [ -f "$file" ] || ct_die "no kanban.md to render"

    # Inline minimal HTML. TODO(M4): proper template; for draft, basic.
    cat > "$out" <<'EOF'
<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>yakOS kanban</title>
<style>
body { font-family: -apple-system, sans-serif; margin: 2em; }
.cols { display: flex; gap: 1em; }
.col { flex: 1; border: 1px solid #ccc; border-radius: 4px; padding: 1em; min-height: 200px; }
.col h2 { margin-top: 0; }
.col ul { list-style: none; padding: 0; }
.col li { background: #f7f7f7; padding: 0.5em; margin-bottom: 0.5em; border-radius: 2px; }
</style></head>
<body><h1>yakOS kanban</h1>
<div class="cols">
EOF

    for col in TODO "IN PROGRESS" DONE; do
        echo "<div class=\"col\"><h2>$col</h2><ul>" >> "$out"
        awk -v c="$col" '
            $0 == "## " c { in_sec=1; next }
            /^## / { in_sec=0 }
            in_sec && /^-/ {
                line = $0
                gsub(/^- \[.\]/, "", line)
                gsub(/^[ \t]+/, "", line)
                print "<li>" line "</li>"
            }
        ' "$file" >> "$out"
        echo "</ul></div>" >> "$out"
    done

    cat >> "$out" <<EOF
</div>
<p style="color:#888;font-size:0.8em">Rendered $(ct_iso_now_z) from $file</p>
</body></html>
EOF
    ct_log "rendered: $out"
}

cmd_add() {
    local title="$1"
    [ -n "$title" ] || ct_die "title required: yakos kanban add \"<title>\""
    _kanban_seed
    local file id
    file="$(_kanban_file)"
    id="$(_kanban_next_id)"

    # Insert under "## TODO" line. ct_sed_inplace handles macOS/Linux.
    ct_sed_inplace "/^## TODO/a\\
- [ ] $id — $title\\
  - assigned: unassigned\\
  - blockers: none\\
" "$file"
    ct_log "added: $id — $title"
}

cmd_move() {
    local id="$1" target="$2"
    case "$target" in
        TODO|todo) target="TODO" ;;
        in-progress|IN-PROGRESS|in_progress|"in progress") target="IN PROGRESS" ;;
        DONE|done) target="DONE" ;;
        *) ct_die "unknown column '$target' (TODO | IN PROGRESS | DONE)" ;;
    esac

    local file tmp
    file="$(_kanban_file)"
    tmp="$(mktemp -t yakos-kanban.XXXXXX)"

    # Extract the task block (4 lines: header + assigned + blockers + blank).
    # TODO(M4): more robust extraction handling variable task block size.
    awk -v id="$id" -v tgt="$target" '
        BEGIN { found = 0; tgt_seen = 0 }
        /^## / { cur_sec = $0; sub("^## ", "", cur_sec) }
        $0 ~ id && /^- / {
            task_header = $0
            getline a; getline b
            found = 1
            next
        }
        /^## / && cur_sec == tgt && found && !tgt_seen {
            print $0
            print task_header
            print a
            print b
            tgt_seen = 1
            next
        }
        { print }
    ' "$file" > "$tmp" && mv "$tmp" "$file"

    ct_log "moved $id to $target"
}

cmd_done() { cmd_move "$1" "DONE"; }

case "${1:-}" in
    add)      shift; cmd_add "$@" ;;
    move)     shift; cmd_move "$@" ;;
    done)     shift; cmd_done "$@" ;;
    --html)   shift; cmd_render_html "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos kanban — 3-column markdown board in scratchpad

  yakos kanban                      # render TUI
  yakos kanban --html [<out>]       # render HTML
  yakos kanban add "<title>"        # append to TODO
  yakos kanban move <id> <col>      # move between columns
  yakos kanban done <id>            # shortcut to DONE
EOF
        ;;
    "") cmd_render_tui ;;
    *) ct_die "yakos kanban: unknown subcommand '$1'" ;;
esac
