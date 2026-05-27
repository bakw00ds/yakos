#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: kanban.sh — manage <work>/current/kanban.md as a 3-column board.
#
# Subcommands:
#   yakos kanban                       # render TUI 3 columns
#   yakos kanban --html [<out>]        # render static HTML snapshot
#   yakos kanban serve [--port N]      # live web UI (view + manage)
#   yakos kanban add "<title>" [--category <c>] [--notes "<text>"]
#                                      # append to TODO
#   yakos kanban notes <id> "<text>"   # set/replace notes field (any column)
#   yakos kanban move <id> <col>       # move task between columns
#   yakos kanban done <id>             # shortcut: move <id> to DONE
#   yakos kanban delete <id>           # hard-delete a task (also: rm)
#
# Task block format:
#   - [ ] K-1 — title
#     - category: feature
#     - assigned: unassigned
#     - blockers: none
#     - notes: optional free-form text (single line)
#
# Reads/writes: <work>/current/kanban.md

set -eu

: "${YAKOS_LIB:?kanban.sh: YAKOS_LIB must be set}"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/paths.sh"

# Known/default category set. Arbitrary user values are accepted; these are
# defaults shown in /api/meta and used when no category is specified.
KANBAN_DEFAULT_CATEGORIES="bug feature chore question other"

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
    # Show header lines only (^- lines); category prefix and notes indicator
    # are appended by a second awk pass.
    local todo in_prog done_col
    todo="$(awk '/^## TODO/{flag=1;next}/^## /{flag=0}flag' "$file" \
        | awk '
            /^- / {
                header = $0
                cat = ""; notes_flag = 0
                next
            }
            /^  - category:/ {
                sub(/^  - category:[[:space:]]*/, ""); cat = $0; next
            }
            /^  - notes:[[:space:]]*[^[:space:]]/ { notes_flag = 1; next }
            /^  - / { next }
            {
                prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                suffix = notes_flag ? " *" : ""
                print prefix header suffix
                header = ""; cat = ""; notes_flag = 0
            }
            END {
                if (header != "") {
                    prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                    suffix = notes_flag ? " *" : ""
                    print prefix header suffix
                }
            }
        ' | head -20)"
    in_prog="$(awk '/^## IN PROGRESS/{flag=1;next}/^## /{flag=0}flag' "$file" \
        | awk '
            /^- / {
                header = $0
                cat = ""; notes_flag = 0
                next
            }
            /^  - category:/ {
                sub(/^  - category:[[:space:]]*/, ""); cat = $0; next
            }
            /^  - notes:[[:space:]]*[^[:space:]]/ { notes_flag = 1; next }
            /^  - / { next }
            {
                prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                suffix = notes_flag ? " *" : ""
                print prefix header suffix
                header = ""; cat = ""; notes_flag = 0
            }
            END {
                if (header != "") {
                    prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                    suffix = notes_flag ? " *" : ""
                    print prefix header suffix
                }
            }
        ' | head -20)"
    done_col="$(awk '/^## DONE/{flag=1;next}/^## /{flag=0}flag' "$file" \
        | awk '
            /^- / {
                header = $0
                cat = ""; notes_flag = 0
                next
            }
            /^  - category:/ {
                sub(/^  - category:[[:space:]]*/, ""); cat = $0; next
            }
            /^  - notes:[[:space:]]*[^[:space:]]/ { notes_flag = 1; next }
            /^  - / { next }
            {
                prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                suffix = notes_flag ? " *" : ""
                print prefix header suffix
                header = ""; cat = ""; notes_flag = 0
            }
            END {
                if (header != "") {
                    prefix = (cat != "" && cat != "other") ? "[" cat "] " : ""
                    suffix = notes_flag ? " *" : ""
                    print prefix header suffix
                }
            }
        ' | head -20)"

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
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxNiAxNiIgd2lkdGg9IjE2IiBoZWlnaHQ9IjE2Ij48cmVjdCB4PSIxIiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSI2IiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSIxMSIgeT0iMyIgd2lkdGg9IjQiIGhlaWdodD0iMTAiIHJ4PSIxIiBmaWxsPSJoc2woMjExLDkwJSw0MiUpIi8+PC9zdmc+">
<style>
body { font-family: -apple-system, sans-serif; margin: 2em; }
.cols { display: flex; gap: 1em; }
.col { flex: 1; border: 1px solid #ccc; border-radius: 4px; padding: 1em; min-height: 200px; }
.col h2 { margin-top: 0; }
.col ul { list-style: none; padding: 0; }
.col li { background: #f7f7f7; padding: 0.5em; margin-bottom: 0.5em; border-radius: 2px; }
.col li .cat { font-size: 0.75em; color: #555; background: #e0e0ff;
               border-radius: 3px; padding: 0.1em 0.3em; margin-right: 0.3em; }
.col li .notes { font-size: 0.8em; color: #666; margin-top: 0.2em; }
</style></head>
<body><h1>yakOS kanban</h1>
<div class="cols">
EOF

    for col in TODO "IN PROGRESS" DONE; do
        echo "<div class=\"col\"><h2>$col</h2><ul>" >> "$out"
        awk -v c="$col" '
            $0 == "## " c { in_sec=1; next }
            /^## / { in_sec=0; task_title=""; task_cat=""; task_notes="" }
            in_sec && /^- \[/ {
                if (task_title != "") {
                    catspan = (task_cat != "") ? "<span class=\"cat\">" task_cat "</span>" : ""
                    notesdiv = (task_notes != "") ? "<div class=\"notes\">" task_notes "</div>" : ""
                    print "<li>" catspan task_title notesdiv "</li>"
                }
                line = $0
                # Strip strict form: - [ ] K-3 — title
                gsub(/^- \[.\] K-[0-9]+ [—-] /, "", line)
                # Strip freeform form: - [id] — title  or  - [id] title
                # Remove the leading "- [<id>]" prefix and optional separator.
                gsub(/^- \[[^\]]*\][[:space:]]*(— |—|-[[:space:]])/, "", line)
                gsub(/^- \[[^\]]*\][[:space:]]*/, "", line)
                task_title = line; task_cat = ""; task_notes = ""
                next
            }
            in_sec && /^  - category:/ {
                sub(/^  - category:[[:space:]]*/, ""); task_cat = $0; next
            }
            in_sec && /^  - notes:/ {
                sub(/^  - notes:[[:space:]]*/, ""); task_notes = $0; next
            }
            END {
                if (task_title != "") {
                    catspan = (task_cat != "") ? "<span class=\"cat\">" task_cat "</span>" : ""
                    notesdiv = (task_notes != "") ? "<div class=\"notes\">" task_notes "</div>" : ""
                    print "<li>" catspan task_title notesdiv "</li>"
                }
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
    local category="other" notes="" title=""
    # Parse flags in any position — flags may come before OR after the
    # positional title (`add "fix bug" --category bug` and
    # `add --category bug "fix bug"` both work; the web /api/add shells the
    # flags-first form).
    while [ $# -gt 0 ]; do
        case "$1" in
            --category)   category="${2:?--category needs a value}"; shift 2 ;;
            --category=*) category="${1#*=}"; shift ;;
            --notes)      notes="${2:?--notes needs a value}"; shift 2 ;;
            --notes=*)    notes="${1#*=}"; shift ;;
            --)           shift
                          if [ -z "$title" ] && [ $# -gt 0 ]; then title="$1"; shift; fi ;;
            -*)           ct_die "yakos kanban add: unknown option '$1'" ;;
            *)            if [ -z "$title" ]; then title="$1"; shift
                          else ct_die "yakos kanban add: unexpected argument '$1'"; fi ;;
        esac
    done
    [ -n "$title" ] || ct_die "title required: yakos kanban add \"<title>\""

    _kanban_seed
    local file id
    file="$(_kanban_file)"
    id="$(_kanban_next_id)"

    # Pass values via the environment and read them with ENVIRON[] rather
    # than `awk -v`: -v processes C escape sequences, so a title/notes
    # containing a literal backslash-t or backslash-n would inject a real
    # tab/newline and corrupt the single-line block. ENVIRON values are
    # literal. (sed metacharacters like / & | are also a non-issue here.)
    kb_id="$id" kb_title="$title" kb_cat="$category" kb_notes="$notes" awk '
        /^## TODO/ {
            print
            print "- [ ] " ENVIRON["kb_id"] " \342\200\224 " ENVIRON["kb_title"]
            print "  - category: " ENVIRON["kb_cat"]
            print "  - assigned: unassigned"
            print "  - blockers: none"
            print "  - notes: " ENVIRON["kb_notes"]
            next
        }
        { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
    ct_log "added: $id — $title (category: $category)"
}

# cmd_notes <id> "<text>" — set/replace the notes field for a task.
# Notes are stored as a single line; raw newlines are converted to spaces
# so the markdown line-based format is preserved.
cmd_notes() {
    local id="${1:-}" text="${2:-}"
    [ -n "$id" ]   || ct_die "id required: yakos kanban notes <id> \"<text>\""
    [ -n "$text" ] || ct_die "text required: yakos kanban notes <id> \"<text>\""

    # Normalize: collapse embedded newlines to spaces (markdown is line-based).
    text="$(printf '%s' "$text" | tr '\n\r' '  ')"

    local file
    file="$(_kanban_file)"
    [ -f "$file" ] || ct_die "kanban.md not found"

    # Pass values via ENVIRON[] (not `awk -v`, which would interpret C escape
    # sequences in the value). Match the id as a whole token (" K-1 ") so
    # K-1 doesn't also match K-10/K-11.
    kb_id="$id" kb_newtext="$text" awk '
        BEGIN { id = ENVIRON["kb_id"] }
        /^- \[.\]/ {
            in_task = (index($0, " " id " ") > 0) ? 1 : 0
            print; next
        }
        /^## / { in_task = 0; print; next }
        in_task && /^  - notes:/ {
            print "  - notes: " ENVIRON["kb_newtext"]
            next
        }
        { print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
    ct_log "notes set for $id"
}

cmd_move() {
    local id="$1" target="$2"
    case "$target" in
        TODO|todo) target="TODO" ;;
        "IN PROGRESS"|"in progress"|in-progress|IN-PROGRESS|in_progress|IN_PROGRESS) target="IN PROGRESS" ;;
        DONE|done) target="DONE" ;;
        *) ct_die "unknown column '$target' (TODO | IN PROGRESS | DONE)" ;;
    esac

    local file tmp
    file="$(_kanban_file)"
    tmp="$(mktemp -t yakos-kanban.XXXXXX)"

    # Two-pass move so the target section is always found regardless of
    # whether the task lives before or after it in the file.
    #
    # Pass 1: extract the task block (header + all indented sub-field lines)
    # and write the file with that block removed.
    # Pass 2: insert the block right after the target section header.
    #
    # A block is: the "- [ ]"/"- [x]" header line plus every immediately
    # following "  - ..." sub-field line.

    local blk_file
    blk_file="$(mktemp -t yakos-kanban-blk.XXXXXX)"

    # --- pass 1: strip the task block, save it to blk_file ---
    awk -v id="$id" '
        BEGIN { in_blk = 0 }
        /^- \[.\]/ && index($0, " " id " ") > 0 {
            in_blk = 1
            print > "/dev/stderr"   # write block lines to stderr for capture
            next
        }
        in_blk && /^  - / {
            print > "/dev/stderr"
            next
        }
        { in_blk = 0; print }
    ' "$file" 2>"$blk_file" > "$tmp" && mv "$tmp" "$file"

    # --- pass 2: insert block after the target section header ---
    awk -v tgt="$target" -v blk_file="$blk_file" '
        /^## / {
            cur_sec = $0
            sub("^## ", "", cur_sec)
            print
            if (cur_sec == tgt) {
                while ((getline line < blk_file) > 0)
                    print line
                close(blk_file)
            }
            next
        }
        { print }
    ' "$file" > "$tmp" && mv "$tmp" "$file"

    rm -f "$blk_file"
    ct_log "moved $id to $target"
}

cmd_done() { cmd_move "$1" "DONE"; }

# cmd_delete <id> — hard-delete a task from kanban.md (removes the task
# header line and all its indented sub-field lines).  The deleted task is
# echoed back so the operator can see exactly what was removed.  kanban.md
# is recoverable from git history if a delete was accidental.
cmd_delete() {
    local id="${1:-}"
    [ -n "$id" ] || ct_die "id required: yakos kanban delete <id>"

    local file
    file="$(_kanban_file)"
    [ -f "$file" ] || ct_die "kanban.md not found"

    # Verify the id exists before attempting a rewrite.  Use the same whole-
    # token match (" K-N ") that cmd_notes / cmd_move use so K-1 never
    # accidentally matches K-10 or K-11.
    local match
    match="$(grep -n "^- \[.\]" "$file" | grep " ${id} " || true)"
    [ -n "$match" ] || ct_die "task '$id' not found in kanban.md"

    # Capture the title for the confirmation echo.
    local title
    title="$(printf '%s' "$match" | sed 's/.*'"$id"' [—-] //')"

    # Strip the task block (header + immediately following "  - " sub-field
    # lines) using the same awk pattern as cmd_move pass-1.
    awk -v id="$id" '
        BEGIN { in_blk = 0 }
        /^- \[.\]/ && index($0, " " id " ") > 0 {
            in_blk = 1; next
        }
        in_blk && /^  - / { next }
        { in_blk = 0; print }
    ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"

    ct_log "deleted: $id — $title"
}

# ---- web UI lifecycle helpers ----------------------------------------------
# The running server records {pid,host,port,url,project} in a small state
# file under the session scratchpad so `status`/`stop` and the auto-start
# path can find it and avoid double-binding the same project's board.

_kanban_state_file() { printf '%s/.kanban-serve.json' "$(yakos_current_dir)"; }

_kanban_state_get() {
    # _kanban_state_get <key>  — extract a scalar from the single-line JSON.
    local f; f="$(_kanban_state_file)"
    [ -f "$f" ] || return 1
    case "$1" in
        pid|port) sed -n "s/.*\"$1\":[[:space:]]*\([0-9][0-9]*\).*/\1/p" "$f" | head -1 ;;
        *)        sed -n "s/.*\"$1\":[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$f" | head -1 ;;
    esac
}

_kanban_server_alive() {
    local pid; pid="$(_kanban_state_get pid 2>/dev/null)" || return 1
    [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# _kanban_pid_is_server <pid> — best-effort check that <pid> is one of our
# python servers (not a recycled pid pointing at an unrelated process).
# Used before `stop` kills it. Loopback/local-only, so this is belt-and-
# suspenders, not a security boundary.
_kanban_pid_is_server() {
    local pid="$1"
    [ -n "$pid" ] || return 1
    ps -o command= -p "$pid" 2>/dev/null | grep -q '[p]ython'
}

# _kanban_find_live_pids <canonical_board_file>
#   Print one pid per line for every python process that is a kanban web
#   server for THIS PROJECT'S board, listening on 127.0.0.1.
#
# Project scoping — all four conditions must hold:
#   1. The process is python (ps command= contains "python").
#   2. It is listening on 127.0.0.1 on some TCP port.
#   3. Its parent process command contains "kanban.sh" (belt-and-suspenders).
#   4. Its OWN argv contains the canonical board file path as a token.
#      cmd_serve embeds that path when launching python so it is always
#      visible in ps output.  This is the authoritative project gate: only
#      servers that were started for this exact board file match.
#
# Path canonicalization (via ct_realpath) ensures that case-variant or
# symlink-variant paths — e.g. /github/yakos vs /github/yakOS on a
# case-insensitive FS — resolve to the same string before comparison.
#
# Portability: lsof is available on macOS and most Linux distros; the
# fallback uses ss (iproute2, present on Linux) then netstat. On a system
# with none of these the function emits nothing (safe: callers treat an
# empty result as "no live servers found").
_kanban_find_live_pids() {
    local board_file="$1"
    [ -n "$board_file" ] || return 0

    # Collect python pids listening on 127.0.0.1 via whichever tool exists.
    local listen_pids=""
    if command -v lsof >/dev/null 2>&1; then
        # lsof -F outputs machine-parseable lines: pNNN / fNNN etc.
        # We want the pid column for LISTEN entries on 127.0.0.1.
        listen_pids="$(lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP -F p 2>/dev/null \
            | sed -n 's/^p//p')"
    elif command -v ss >/dev/null 2>&1; then
        # ss output: State Recv-Q Send-Q Local:Port Peer:Port Process
        # The process field (pid=NNN) only appears with -p and when run as
        # the process owner or root; filter to 127.0.0.1 listeners.
        listen_pids="$(ss -tlnp 2>/dev/null \
            | awk '/127\.0\.0\.1/{
                match($0, /pid=([0-9]+)/, a)
                if (a[1]) print a[1]
              }')"
    elif command -v netstat >/dev/null 2>&1; then
        # netstat -tlnp (Linux); -anp (BSD) — try both forms silently.
        listen_pids="$(netstat -tlnp 2>/dev/null \
            | awk '/127\.0\.0\.1.*LISTEN/{
                split($NF, a, "/"); if (a[1]+0 > 0) print a[1]
              }')"
    fi

    [ -n "$listen_pids" ] || return 0

    # For each listening pid: verify python, kanban.sh parent, and — the
    # authoritative project gate — own argv contains this board's path.
    local pid ppid own_cmd parent_cmd
    printf '%s\n' "$listen_pids" | sort -u | while IFS= read -r pid; do
        [ -n "$pid" ] || continue
        own_cmd="$(ps -o command= -p "$pid" 2>/dev/null)"
        # Must be a python process we own.
        printf '%s' "$own_cmd" | grep -q '[p]ython' || continue
        # Own argv must carry this board's canonical path as a token.
        # fgrep (-F) treats the path as a literal string, not a regex.
        printf '%s' "$own_cmd" | grep -qF "$board_file" || continue
        # Belt-and-suspenders: parent must be a kanban.sh invocation.
        ppid="$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')"
        [ -n "$ppid" ] || continue
        parent_cmd="$(ps -o command= -p "$ppid" 2>/dev/null)"
        printf '%s' "$parent_cmd" | grep -q 'kanban\.sh' || continue
        printf '%s\n' "$pid"
    done
}

# _kanban_port_for_pid <pid> — print the TCP port the pid is listening on
# (127.0.0.1 only). Prints nothing if not found. Uses lsof/ss/netstat in
# the same priority order as _kanban_find_live_pids.
_kanban_port_for_pid() {
    local pid="$1"
    [ -n "$pid" ] || return 0
    if command -v lsof >/dev/null 2>&1; then
        lsof -iTCP@127.0.0.1 -sTCP:LISTEN -nP -a -p "$pid" 2>/dev/null \
            | awk 'NR>1{n=split($9,a,":"); if(n>1) print a[n]; exit}'
    elif command -v ss >/dev/null 2>&1; then
        ss -tlnp 2>/dev/null \
            | awk -v p="$pid" '/127\.0\.0\.1/ && $0 ~ "pid=" p "[^0-9]" {
                split($4,a,":"); print a[length(a)]; exit}'
    elif command -v netstat >/dev/null 2>&1; then
        netstat -tlnp 2>/dev/null \
            | awk -v p="$pid" '/127\.0\.0\.1.*LISTEN/ {
                split($NF,a,"/")
                if (a[1]==p) { split($4,b,":"); print b[length(b)]; exit }
              }'
    fi
}

# _kanban_state_recover <pid> <port> — write (or overwrite) the state file
# with the live server's details. Called when the scan finds a live server
# that the state file doesn't know about (stale or missing state file).
_kanban_state_recover() {
    local pid="$1" port="$2"
    local state host url project
    state="$(_kanban_state_file)"
    host="127.0.0.1"
    url="http://${host}:${port}"
    project="${YAKOS_PROJECT_NAME:-$(yakos_project_name)}"
    printf '{"pid":%s,"host":"%s","port":%s,"url":"%s","project":"%s"}\n' \
        "$pid" "$host" "$port" "$url" "$project" > "$state"
}

# cmd_serve [--port N] [--host H] [--no-open]
#   Launch a small web UI that renders the board live and lets the
#   operator add/move tasks from the browser. Mutations shell back into
#   this same script (add/move/notes) so kanban.md stays the single source
#   of truth — the CLI, the lifecycle hooks, and the web UI all edit the
#   same markdown. Binds to loopback only by default on a random unused
#   high (OS-assigned ephemeral) port; the board is local session state,
#   not something to expose on the network.
cmd_serve() {
    local host="127.0.0.1" port="0" open_browser="1"
    while [ $# -gt 0 ]; do
        case "$1" in
            --port)   port="${2:?--port needs a value}"; shift 2 ;;
            --port=*) port="${1#*=}"; shift ;;
            --host)   host="${2:?--host needs a value}"; shift 2 ;;
            --host=*) host="${1#*=}"; shift ;;
            --no-open) open_browser="0"; shift ;;
            *) ct_die "yakos kanban serve: unknown option '$1'" ;;
        esac
    done

    command -v python3 >/dev/null 2>&1 \
        || ct_die "yakos kanban serve requires python3 (not found on PATH)"

    # Don't double-bind: if a server is already up for this project's board,
    # point the operator at it instead of starting a second one.
    #
    # Two-layer check:
    #   1. Fast path — state file records a live pid (common case).
    #   2. Scan path — state file is absent/stale but a kanban server is
    #      already running (the orphan-leak window). Recover the state file
    #      so subsequent status/stop calls work, then return without binding.
    if _kanban_server_alive; then
        ct_log "kanban web UI already running → $(_kanban_state_get url)"
        ct_log "(use 'yakos kanban stop' to stop it)"
        return 0
    fi
    # Scan path: state file absent/stale but this board's server may still be
    # running.  Resolve the board path now so the scan is project-scoped.
    # (canon_file is set below after _kanban_seed; we need it here, so compute
    # it early using the raw _kanban_file path — _kanban_seed is a no-op when
    # the file exists, which is true for any board that has had serve called
    # before.)
    local _early_file _early_canon _scan_pid _scan_port _scan_url
    _early_file="$(_kanban_file)"
    _early_canon="$(ct_realpath "$_early_file" 2>/dev/null || printf '%s' "$_early_file")"
    _scan_pid="$(_kanban_find_live_pids "$_early_canon" | head -1)"
    if [ -n "$_scan_pid" ]; then
        _scan_port="$(_kanban_port_for_pid "$_scan_pid")"
        if [ -n "$_scan_port" ]; then
            _scan_url="http://127.0.0.1:${_scan_port}"
            ct_log "kanban web UI already running → ${_scan_url} (pid ${_scan_pid}; state file was stale — recovered)"
            ct_log "(use 'yakos kanban stop' to stop it)"
            _kanban_state_recover "$_scan_pid" "$_scan_port"
            return 0
        fi
    fi

    case "$port" in
        ''|*[!0-9]*) ct_die "yakos kanban serve: --port must be a number (got '$port')" ;;
    esac

    # port "0" → the OS assigns a free ephemeral (high) port; python reports
    # the actual bound port back via the state file. This avoids a TOCTOU
    # between "find a free port" and "bind it".
    case "$host" in
        127.*|::1|localhost) : ;;
        *) ct_log "WARNING: binding non-loopback host '$host' — the web UI is UNAUTHENTICATED and can mutate the board; only do this on a trusted network." ;;
    esac

    _kanban_seed
    local file canon_file project state
    file="$(_kanban_file)"
    # Canonical board path: resolve via ct_realpath so case-variant or
    # symlink-variant paths (e.g. /github/yakos vs /github/yakOS on a
    # case-insensitive FS) normalise to the same string.  This token is
    # embedded in the python argv so _kanban_find_live_pids can match it.
    canon_file="$(ct_realpath "$file" 2>/dev/null || printf '%s' "$file")"
    project="${YAKOS_PROJECT_NAME:-$(yakos_project_name)}"
    state="$(_kanban_state_file)"

    local py_pid="" rc=0
    # On Ctrl-C / TERM while we're waiting on python: signal the server so it
    # shuts down gracefully (its own handler removes the state file), then
    # exit. py_pid is visible here via bash dynamic scope (this handler only
    # runs while cmd_serve is on the stack). The state file is owned by
    # python — it writes it after a successful bind and removes it on exit.
    # shellcheck disable=SC2317
    _kanban_serve_cleanup() {
        [ -n "${py_pid:-}" ] && kill "$py_pid" 2>/dev/null || true
        rm -f "${state:-/nonexistent}" 2>/dev/null || true
        exit 130
    }
    trap _kanban_serve_cleanup INT TERM

    KANBAN_FILE="$file" \
    KANBAN_SCRIPT="$YAKOS_LIB/kanban.sh" \
    KANBAN_HOST="$host" \
    KANBAN_PORT="$port" \
    KANBAN_OPEN="$open_browser" \
    KANBAN_PROJECT="$project" \
    KANBAN_STATE="$state" \
    KANBAN_DEFAULT_CATEGORIES="$KANBAN_DEFAULT_CATEGORIES" \
    YAKOS_WORK_DIR="$(yakos_work_dir)" \
    python3 - "$canon_file" <<'PY' &
import json, os, re, signal, subprocess, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

KANBAN_FILE = os.environ["KANBAN_FILE"]
KANBAN_SCRIPT = os.environ["KANBAN_SCRIPT"]
KANBAN_STATE = os.environ.get("KANBAN_STATE", "")
HOST = os.environ.get("KANBAN_HOST", "127.0.0.1")
PORT = int(os.environ.get("KANBAN_PORT", "0"))
OPEN = os.environ.get("KANBAN_OPEN", "0") == "1"
PROJECT = os.environ.get("KANBAN_PROJECT", "") or "this project"
# Known/default categories — passed in from the bash layer so there's one
# authoritative definition. Split on whitespace.
_DEFAULT_CATS_RAW = os.environ.get("KANBAN_DEFAULT_CATEGORIES", "bug feature chore question other")
DEFAULT_CATEGORIES = _DEFAULT_CATS_RAW.split()

COLUMNS = ["TODO", "IN PROGRESS", "DONE"]
ID_RE = re.compile(r"^K-\d+$")
MAX_BODY = 256 * 1024          # cap POST bodies; titles are tiny
# Hostnames we answer to — defeats DNS-rebinding (a rebound hostname won't
# match). The actual bind host is added at startup.
ALLOWED_HOSTS = {"127.0.0.1", "localhost", "[::1]", "::1"}
_mutate_lock = threading.Lock()

HEAD_RE = re.compile(r"^##\s+(.+?)\s*$")
# Strict form: - [ ] K-3 — title  /  - [x] K-3 — title
# Bracket content is a single char that is a space, x, or X (checkbox).
TASK_STRICT_RE = re.compile(r"^-\s*\[([ xX])\]\s*(K-\d+)\s*[—-]\s*(.*)$")
# Freeform form: - [<id>] <rest>  where <id> is NOT a bare checkbox char.
# Matches any non-empty bracket content that is not solely " ", "x", or "X".
TASK_FREE_RE   = re.compile(r"^-\s*\[([^ xX][^\]]*|[^ xX])\]\s*(.*)$")
ASSIGNED_RE = re.compile(r"^\s+-\s*assigned:\s*(.*)$")
BLOCKERS_RE = re.compile(r"^\s+-\s*blockers:\s*(.*)$")
CATEGORY_RE = re.compile(r"^\s+-\s*category:\s*(.*)$")
NOTES_RE    = re.compile(r"^\s+-\s*notes:\s*(.*)$")
# Separator that freeform titles may begin with (em-dash or ASCII hyphen).
_FREE_SEP_RE = re.compile(r"^[—-]\s*")


def parse_board():
    cols = {c: [] for c in COLUMNS}
    try:
        with open(KANBAN_FILE, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
    except FileNotFoundError:
        return cols
    cur, task = None, None
    for line in lines:
        h = HEAD_RE.match(line)
        if h:
            name = h.group(1).strip()
            cur = name if name in cols else None
            task = None
            continue
        if cur is None:
            continue
        # Try strict form first (checkbox + K-N id).
        m = TASK_STRICT_RE.match(line)
        if m:
            task = {"id": m.group(2), "title": m.group(3).strip(),
                    "done": m.group(1).lower() == "x",
                    "category": "other", "assigned": "",
                    "blockers": "", "notes": ""}
            cols[cur].append(task)
            continue
        # Try freeform form (arbitrary bracket id, no checkbox semantics).
        fm = TASK_FREE_RE.match(line)
        if fm:
            fid   = fm.group(1).strip()
            frest = fm.group(2).strip()
            # Strip a leading em-dash or ASCII hyphen separator if present.
            ftitle = _FREE_SEP_RE.sub("", frest, count=1).strip()
            task = {"id": fid, "title": ftitle,
                    "done": False,
                    "category": "other", "assigned": "",
                    "blockers": "", "notes": ""}
            cols[cur].append(task)
            continue
        if task is not None:
            am = ASSIGNED_RE.match(line)
            bm = BLOCKERS_RE.match(line)
            cm = CATEGORY_RE.match(line)
            nm = NOTES_RE.match(line)
            if am:
                task["assigned"] = am.group(1).strip()
            elif bm:
                task["blockers"] = bm.group(1).strip()
            elif cm:
                task["category"] = cm.group(1).strip() or "other"
            elif nm:
                task["notes"] = nm.group(1).strip()
    return cols


def _board_categories(board):
    """Return de-duped category list: known-first, then any extras found."""
    seen = set(DEFAULT_CATEGORIES)
    extras = []
    for tasks in board.values():
        for t in tasks:
            c = t.get("category", "other") or "other"
            if c not in seen:
                extras.append(c)
                seen.add(c)
    return DEFAULT_CATEGORIES + extras


def run_kanban(args):
    # Serialize mutations: kanban.sh rewrites the whole file, so two
    # concurrent edits would race. One writer at a time.
    with _mutate_lock:
        return subprocess.run(["bash", KANBAN_SCRIPT, *args],
                              capture_output=True, text=True)


PAGE = r"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kanban</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxNiAxNiIgd2lkdGg9IjE2IiBoZWlnaHQ9IjE2Ij48cmVjdCB4PSIxIiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSI2IiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSIxMSIgeT0iMyIgd2lkdGg9IjQiIGhlaWdodD0iMTAiIHJ4PSIxIiBmaWxsPSJoc2woMjExLDkwJSw0MiUpIi8+PC9zdmc+">
<style>
/* ── Design tokens (light / dark) ───────────────────────────────────────── */
:root {
  --kb-bg: hsl(220 14% 96%); --kb-surface: hsl(220 13% 91%); --kb-card: hsl(0 0% 100%);
  --kb-border: hsl(220 12% 82%); --kb-text: hsl(220 20% 16%); --kb-muted: hsl(220 12% 40%);
  --kb-accent: hsl(211 90% 42%); --kb-on-accent: hsl(0 0% 100%);
  --kb-shadow: rgba(30 36 52 / 0.10); --kb-drop-ring: hsl(211 90% 60%);
  --kb-cat-bug: hsl(4 72% 40%); --kb-cat-bug-soft: hsl(4 80% 93%);
  --kb-cat-feature: hsl(211 80% 36%); --kb-cat-feature-soft: hsl(211 85% 92%);
  --kb-cat-chore: hsl(38 80% 36%); --kb-cat-chore-soft: hsl(38 90% 91%);
  --kb-cat-question: hsl(264 60% 40%); --kb-cat-question-soft: hsl(264 70% 93%);
  --kb-cat-other: hsl(220 18% 38%); --kb-cat-other-soft: hsl(220 20% 91%);
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --kb-bg: hsl(220 16% 10%); --kb-surface: hsl(220 14% 14%); --kb-card: hsl(220 13% 18%);
    --kb-border: hsl(220 12% 26%); --kb-text: hsl(220 14% 92%); --kb-muted: hsl(220 10% 58%);
    --kb-accent: hsl(211 88% 62%); --kb-on-accent: hsl(220 20% 10%);
    --kb-shadow: rgba(0 0 0 / 0.40); --kb-drop-ring: hsl(211 88% 70%);
    --kb-cat-bug: hsl(4 82% 65%); --kb-cat-bug-soft: hsl(4 50% 18%);
    --kb-cat-feature: hsl(211 85% 65%); --kb-cat-feature-soft: hsl(211 50% 18%);
    --kb-cat-chore: hsl(38 85% 58%); --kb-cat-chore-soft: hsl(38 50% 16%);
    --kb-cat-question: hsl(264 70% 70%); --kb-cat-question-soft: hsl(264 40% 20%);
    --kb-cat-other: hsl(220 20% 65%); --kb-cat-other-soft: hsl(220 16% 22%);
  }
}
:root[data-theme="dark"] {
  --kb-bg: hsl(220 16% 10%); --kb-surface: hsl(220 14% 14%); --kb-card: hsl(220 13% 18%);
  --kb-border: hsl(220 12% 26%); --kb-text: hsl(220 14% 92%); --kb-muted: hsl(220 10% 58%);
  --kb-accent: hsl(211 88% 62%); --kb-on-accent: hsl(220 20% 10%);
  --kb-shadow: rgba(0 0 0 / 0.40); --kb-drop-ring: hsl(211 88% 70%);
  --kb-cat-bug: hsl(4 82% 65%); --kb-cat-bug-soft: hsl(4 50% 18%);
  --kb-cat-feature: hsl(211 85% 65%); --kb-cat-feature-soft: hsl(211 50% 18%);
  --kb-cat-chore: hsl(38 85% 58%); --kb-cat-chore-soft: hsl(38 50% 16%);
  --kb-cat-question: hsl(264 70% 70%); --kb-cat-question-soft: hsl(264 40% 20%);
  --kb-cat-other: hsl(220 20% 65%); --kb-cat-other-soft: hsl(220 16% 22%);
}
:root[data-theme="light"] {
  --kb-bg: hsl(220 14% 96%); --kb-surface: hsl(220 13% 91%); --kb-card: hsl(0 0% 100%);
  --kb-border: hsl(220 12% 82%); --kb-text: hsl(220 20% 16%); --kb-muted: hsl(220 12% 40%);
  --kb-accent: hsl(211 90% 42%); --kb-on-accent: hsl(0 0% 100%);
  --kb-shadow: rgba(30 36 52 / 0.10); --kb-drop-ring: hsl(211 90% 60%);
  --kb-cat-bug: hsl(4 72% 40%); --kb-cat-bug-soft: hsl(4 80% 93%);
  --kb-cat-feature: hsl(211 80% 36%); --kb-cat-feature-soft: hsl(211 85% 92%);
  --kb-cat-chore: hsl(38 80% 36%); --kb-cat-chore-soft: hsl(38 90% 91%);
  --kb-cat-question: hsl(264 60% 40%); --kb-cat-question-soft: hsl(264 70% 93%);
  --kb-cat-other: hsl(220 18% 38%); --kb-cat-other-soft: hsl(220 20% 91%);
}

* { box-sizing: border-box; }

html, body { height: 100%; }

body {
  margin: 0;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, system-ui, sans-serif;
  font-size: 14px;
  line-height: 1.45;
  background: var(--kb-bg);
  color: var(--kb-text);
  -webkit-font-smoothing: antialiased;
}

button, input, select, textarea { font: inherit; color: inherit; }

/* ── Header ─────────────────────────────────────────────────────────────── */
.app-header {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1rem 1.5rem;
  background: var(--kb-surface);
  border-bottom: 1px solid var(--kb-border);
}
.brand { display: flex; flex-direction: column; min-width: 0; }
.brand .kicker {
  font: 600 .68rem ui-monospace, SFMono-Regular, Menlo, monospace;
  letter-spacing: .12em;
  text-transform: uppercase;
  color: var(--kb-muted);
}
.brand h1 {
  margin: .1rem 0 0;
  font-size: 1.25rem;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.brand .filemeta {
  font: .72rem ui-monospace, monospace;
  color: var(--kb-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.header-spacer { flex: 1; }

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.2rem;
  height: 2.2rem;
  border: 1px solid var(--kb-border);
  border-radius: 8px;
  background: var(--kb-card);
  cursor: pointer;
  transition: background .12s ease, border-color .12s ease;
}
.icon-btn:hover { border-color: var(--kb-accent); }
.icon-btn:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }
.icon-btn svg { width: 1.1rem; height: 1.1rem; }
/* Show sun in dark mode (offer light), moon otherwise (offer dark). */
.theme-toggle .ico-moon { display: inline; }
.theme-toggle .ico-sun  { display: none; }
:root[data-theme="dark"] .theme-toggle .ico-moon { display: none; }
:root[data-theme="dark"] .theme-toggle .ico-sun  { display: inline; }
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) .theme-toggle .ico-moon { display: none; }
  :root:not([data-theme="light"]) .theme-toggle .ico-sun  { display: inline; }
}

/* ── Toolbar (add form + filter bar) ────────────────────────────────────── */
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: .75rem 1rem;
  padding: .85rem 1.5rem;
  background: var(--kb-surface);
  border-bottom: 1px solid var(--kb-border);
}
.add-form {
  display: flex;
  align-items: center;
  gap: .5rem;
  flex: 1 1 22rem;
  min-width: 16rem;
}
.add-form .field { display: flex; flex-direction: column; }
.add-form input[type="text"] {
  flex: 1;
  min-width: 10rem;
  padding: .5rem .65rem;
  border: 1px solid var(--kb-border);
  border-radius: 8px;
  background: var(--kb-card);
}
.add-form select {
  padding: .5rem .55rem;
  border: 1px solid var(--kb-border);
  border-radius: 8px;
  background: var(--kb-card);
}
.add-form input:focus, .add-form select:focus,
textarea:focus {
  outline: 2px solid var(--kb-accent);
  outline-offset: 1px;
  border-color: var(--kb-accent);
}
.btn {
  border: 1px solid var(--kb-border);
  background: var(--kb-card);
  border-radius: 8px;
  padding: .5rem .8rem;
  cursor: pointer;
  transition: background .12s ease, border-color .12s ease, opacity .12s ease;
}
.btn:hover { border-color: var(--kb-accent); }
.btn:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }
.btn.primary {
  background: var(--kb-accent);
  color: var(--kb-on-accent);
  border-color: var(--kb-accent);
}
.btn.primary:hover { opacity: .92; }

/* Filter chips */
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: .4rem;
}
.filter-bar .filter-label {
  font: 600 .68rem ui-monospace, monospace;
  letter-spacing: .08em;
  text-transform: uppercase;
  color: var(--kb-muted);
  margin-right: .15rem;
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: .3rem;
  padding: .28rem .6rem;
  border: 1px solid var(--kb-border);
  border-radius: 999px;
  background: var(--kb-card);
  color: var(--kb-text);
  font-size: .8rem;
  cursor: pointer;
  transition: background .12s ease, border-color .12s ease, box-shadow .12s ease;
}
.chip:hover { border-color: var(--kb-accent); }
.chip:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }
.chip .dot {
  width: .6rem; height: .6rem; border-radius: 50%;
  background: var(--chip-strong, var(--kb-muted));
}
.chip[aria-pressed="true"] {
  background: var(--chip-soft, var(--kb-accent));
  border-color: var(--chip-strong, var(--kb-accent));
  box-shadow: 0 0 0 1px var(--chip-strong, var(--kb-accent)) inset;
  font-weight: 600;
}

/* Sort control */
.sort-control {
  display: flex;
  align-items: center;
  gap: .4rem;
}
.sort-control .sort-label {
  font: 600 .68rem ui-monospace, monospace;
  letter-spacing: .08em;
  text-transform: uppercase;
  color: var(--kb-muted);
  white-space: nowrap;
}
.sort-control select {
  padding: .28rem .5rem;
  border: 1px solid var(--kb-border);
  border-radius: 8px;
  background: var(--kb-card);
  font-size: .8rem;
  cursor: pointer;
  transition: border-color .12s ease;
}
.sort-control select:hover { border-color: var(--kb-accent); }
.sort-control select:focus-visible {
  outline: 2px solid var(--kb-accent);
  outline-offset: 1px;
  border-color: var(--kb-accent);
}

/* ── Board ──────────────────────────────────────────────────────────────── */
.board {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  padding: 1.25rem 1.5rem 2rem;
  align-items: start;
}
.column {
  background: var(--kb-surface);
  border: 1px solid var(--kb-border);
  border-radius: 12px;
  padding: .75rem;
  min-height: 50vh;
  transition: box-shadow .12s ease, background .12s ease;
}
.column.drop-over {
  box-shadow: 0 0 0 2px var(--kb-drop-ring) inset;
  background: var(--kb-bg);
}
.column-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: .15rem .35rem .7rem;
}
.column-head h2 {
  margin: 0;
  font-size: .72rem;
  font-weight: 700;
  letter-spacing: .08em;
  text-transform: uppercase;
  color: var(--kb-muted);
}
.count {
  min-width: 1.5rem;
  text-align: center;
  font: 600 .72rem ui-monospace, monospace;
  color: var(--kb-muted);
  background: var(--kb-bg);
  border: 1px solid var(--kb-border);
  border-radius: 999px;
  padding: .05rem .45rem;
}
.list { display: flex; flex-direction: column; gap: .6rem; min-height: 2.5rem; }

.col-empty {
  color: var(--kb-muted);
  font-size: .82rem;
  text-align: center;
  padding: 1.25rem .5rem;
  border: 1px dashed var(--kb-border);
  border-radius: 8px;
}

/* ── Card ───────────────────────────────────────────────────────────────── */
.card {
  background: var(--kb-card);
  border: 1px solid var(--kb-border);
  border-left: 4px solid var(--cat-strong, var(--kb-border));
  border-radius: 10px;
  padding: .65rem .7rem;
  box-shadow: var(--kb-shadow);
  cursor: grab;
  transition: box-shadow .12s ease, transform .12s ease, opacity .12s ease;
}
.card:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }
.card.dragging { opacity: .45; cursor: grabbing; }
.card[hidden] { display: none; }

.card-top { display: flex; align-items: center; gap: .5rem; }
.id-badge {
  font: 600 .7rem ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--kb-muted);
  background: var(--kb-bg);
  border-radius: 5px;
  padding: .05rem .35rem;
}
.cat-chip {
  margin-left: auto;
  font-size: .68rem;
  font-weight: 600;
  letter-spacing: .02em;
  padding: .1rem .45rem;
  border-radius: 999px;
  background: var(--cat-soft, var(--kb-bg));
  color: var(--cat-strong, var(--kb-text));
}
.card-title {
  margin: .45rem 0 .35rem;
  font-size: .92rem;
  font-weight: 600;
  word-break: break-word;
}
.tags { display: flex; flex-wrap: wrap; gap: .35rem; margin-bottom: .4rem; }
.tag {
  font-size: .72rem;
  padding: .08rem .4rem;
  border-radius: 5px;
  background: var(--kb-bg);
  color: var(--kb-muted);
  border: 1px solid var(--kb-border);
}
.tag.blocker {
  background: var(--kb-cat-bug-soft);
  color: var(--kb-cat-bug);
  border-color: var(--kb-cat-bug-soft);
}

/* Notes */
.notes { margin-top: .35rem; }
.notes-view {
  display: flex;
  align-items: flex-start;
  gap: .4rem;
}
.notes-text {
  flex: 1;
  font-size: .8rem;
  color: var(--kb-muted);
  white-space: pre-wrap;
  word-break: break-word;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.notes-text.empty { font-style: italic; }
.notes-edit-btn {
  flex: none;
  border: none;
  background: none;
  color: var(--kb-accent);
  font-size: .74rem;
  cursor: pointer;
  padding: .05rem .2rem;
  border-radius: 5px;
}
.notes-edit-btn:hover { text-decoration: underline; }
.notes-edit-btn:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }
.notes-editor { display: none; flex-direction: column; gap: .4rem; }
.notes.editing .notes-view { display: none; }
.notes.editing .notes-editor { display: flex; }
.notes-editor textarea {
  width: 100%;
  min-height: 4.5rem;
  resize: vertical;
  padding: .4rem .5rem;
  border: 1px solid var(--kb-border);
  border-radius: 7px;
  background: var(--kb-card);
  font-size: .8rem;
}
.notes-editor .editor-actions { display: flex; gap: .4rem; }
.notes-editor .btn { padding: .3rem .6rem; font-size: .78rem; }

/* Per-card keyboard move fallback */
.move-actions {
  display: flex;
  flex-wrap: wrap;
  gap: .35rem;
  margin-top: .55rem;
  padding-top: .5rem;
  border-top: 1px dashed var(--kb-border);
}
.move-actions .btn {
  font-size: .72rem;
  padding: .22rem .5rem;
}

/* ── Delete affordance ──────────────────────────────────────────────────── */
/* Idle "Delete" trigger — subtle; sits at the bottom of each card. */
.delete-wrap { margin-top: .4rem; }
.delete-trigger {
  border: 1px solid transparent;
  background: none;
  color: var(--kb-muted);
  border-radius: 7px;
  padding: .22rem .5rem;
  font-size: .72rem;
  cursor: pointer;
  text-align: left;
  transition: background .12s ease, color .12s ease, border-color .12s ease;
}
.delete-trigger:hover {
  background: var(--kb-cat-bug-soft);
  color: var(--kb-cat-bug);
  border-color: var(--kb-cat-bug-soft);
}
.delete-trigger:focus-visible { outline: 2px solid var(--kb-cat-bug); outline-offset: 2px; }
/* Confirm row: revealed on first click, contains Confirm + Cancel. */
.delete-confirm {
  display: none;
  align-items: center;
  gap: .4rem;
  flex-wrap: wrap;
  margin-top: .3rem;
}
.delete-confirm.visible { display: flex; }
.delete-confirm-label {
  font-size: .72rem;
  color: var(--kb-cat-bug);
  font-weight: 600;
  flex: 1 1 100%;
}
.delete-confirm-yes {
  border: 1px solid var(--kb-cat-bug);
  background: var(--kb-cat-bug-soft);
  color: var(--kb-cat-bug);
  border-radius: 7px;
  padding: .22rem .55rem;
  font-size: .72rem;
  font-weight: 600;
  cursor: pointer;
  transition: opacity .12s ease;
}
.delete-confirm-yes:hover { opacity: .82; }
.delete-confirm-yes:focus-visible { outline: 2px solid var(--kb-cat-bug); outline-offset: 2px; }
.delete-confirm-no {
  border: 1px solid var(--kb-border);
  background: var(--kb-card);
  color: var(--kb-muted);
  border-radius: 7px;
  padding: .22rem .55rem;
  font-size: .72rem;
  cursor: pointer;
  transition: border-color .12s ease;
}
.delete-confirm-no:hover { border-color: var(--kb-accent); }
.delete-confirm-no:focus-visible { outline: 2px solid var(--kb-accent); outline-offset: 2px; }

/* ── Toast ──────────────────────────────────────────────────────────────── */
.toast-stack {
  position: fixed;
  right: 1rem;
  bottom: 1rem;
  display: flex;
  flex-direction: column;
  gap: .5rem;
  z-index: 50;
  max-width: min(28rem, 90vw);
}
.toast {
  background: var(--kb-cat-bug, #d6455b);
  color: #fff;
  padding: .55rem .8rem;
  border-radius: 8px;
  font-size: .82rem;
  box-shadow: var(--kb-shadow);
  opacity: 0;
  transform: translateY(.4rem);
  transition: opacity .18s ease, transform .18s ease;
}
.toast.show { opacity: 1; transform: translateY(0); }

/* ── Status / footer ────────────────────────────────────────────────────── */
.statusbar {
  display: flex;
  align-items: center;
  gap: .4rem;
  padding: 0 1.5rem 1.5rem;
  color: var(--kb-muted);
  font-size: .74rem;
}
.dot-live {
  width: .5rem; height: .5rem; border-radius: 50%;
  background: var(--kb-cat-feature, #2f8f5b);
}
.dot-live.stale { background: var(--kb-cat-chore, #8a7321); }

/* ── Responsive: columns stack on narrow screens ────────────────────────── */
@media (max-width: 760px) {
  .board { grid-template-columns: 1fr; }
  .column { min-height: auto; }
  .add-form { flex: 1 1 100%; }
}

@media (prefers-reduced-motion: reduce) {
  * { transition: none !important; }
}
</style>
</head>
<body>

<header class="app-header">
  <div class="brand">
    <span class="kicker">yakOS · kanban</span>
    <h1 id="project-name">Loading…</h1>
    <span class="filemeta" id="board-file"></span>
  </div>
  <div class="header-spacer"></div>
  <button id="theme-toggle" class="icon-btn theme-toggle" type="button"
          aria-label="Toggle light and dark theme" title="Toggle theme">
    <svg class="ico-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"></path>
    </svg>
    <svg class="ico-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor"
         stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="4"></circle>
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"></path>
    </svg>
  </button>
</header>

<div class="toolbar">
  <form class="add-form" id="add-form" autocomplete="off">
    <label class="field" style="flex:1;">
      <span class="sr-only" style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);">New task title</span>
      <input type="text" id="add-title" placeholder="New task title — press Enter to add"
             aria-label="New task title">
    </label>
    <label class="field">
      <span class="sr-only" style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);">Category</span>
      <select id="add-category" aria-label="Task category"></select>
    </label>
    <button type="submit" class="btn primary">Add</button>
  </form>

  <div class="filter-bar" id="filter-bar" role="group" aria-label="Filter by category">
    <span class="filter-label">Filter</span>
    <!-- chips injected: "All" + one per category -->
  </div>

  <div class="sort-control">
    <label class="sort-label" for="sort-by">Sort</label>
    <select id="sort-by" aria-label="Sort cards by">
      <option value="recent">Recent</option>
      <option value="category">Category</option>
      <option value="title">Title A&#x2013;Z</option>
      <option value="id">ID</option>
    </select>
  </div>
</div>

<main class="board" id="board" aria-label="Kanban board">
  <!-- columns injected -->
</main>

<div class="statusbar">
  <span class="dot-live" id="live-dot" title="auto-refresh"></span>
  <span id="status-text">Local board · 127.0.0.1 only</span>
</div>

<div class="toast-stack" id="toast-stack" aria-live="polite" aria-atomic="false"></div>

<script>
"use strict";

const COLUMNS = ["TODO", "IN PROGRESS", "DONE"];
const POLL_MS = 3000;
const LS_THEME = "kanban-theme";
const LS_FILTER = "kanban-filter";
const LS_SORT  = "kanban-sort";
const SORT_OPTIONS = ["recent", "category", "title", "id"];

let META = { project: "kanban", file: "", columns: COLUMNS, categories: [] };
let activeFilter = "all";          // "all" or a category name
let activeSort = "recent";         // "recent" | "category" | "title" | "id"
let lastBoard = null;              // last fetched board data; re-sorted on sort change
let openEditors = new Set();       // card ids with their notes editor open
let dragId = null;
let pollTimer = null;
let liveErrShown = false;          // guard: show poll-failure toast only once per error run

const slug = c => c.toLowerCase().replace(/[^a-z0-9]+/g, "-");

/* ── Safe DOM helpers ─────────────────────────────────────────────────────
   All server-provided strings reach the DOM via textContent / setAttribute.
   No innerHTML is ever fed raw data. ------------------------------------- */
function el(tag, opts = {}) {
  const node = document.createElement(tag);
  if (opts.class) node.className = opts.class;
  if (opts.text != null) node.textContent = opts.text;
  if (opts.attrs) for (const [k, v] of Object.entries(opts.attrs)) node.setAttribute(k, v);
  if (opts.children) for (const c of opts.children) if (c) node.appendChild(c);
  return node;
}

/* Category vars are validated against meta.categories so we only ever emit a
   token name we trust; falls back to "other". */
function catVars(category) {
  const known = META.categories.includes(category) ? category : "other";
  const safe = slug(known);
  return {
    strong: `var(--kb-cat-${safe}, var(--kb-muted))`,
    soft: `var(--kb-cat-${safe}-soft, var(--kb-bg))`,
    name: known
  };
}

/* ── Toast (unobtrusive error surface; never alert()) ─────────────────────── */
function toast(msg) {
  const stack = document.getElementById("toast-stack");
  const t = el("div", { class: "toast", text: String(msg || "Something went wrong") });
  stack.appendChild(t);
  requestAnimationFrame(() => t.classList.add("show"));
  setTimeout(() => {
    t.classList.remove("show");
    setTimeout(() => t.remove(), 220);
  }, 4000);
}

/* ── API: mutations surface errors; reads on the poll path stay silent ────── */
async function post(path, body) {
  let res;
  try {
    res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
  } catch (e) {
    toast("Network error — is the board server still running?");
    return null;
  }
  let data = null;
  try { data = await res.json(); } catch (e) { /* tolerate empty body */ }
  if (!res.ok) {
    toast((data && data.error) ? data.error : `Request failed (${res.status})`);
    return null;
  }
  return data; // updated board
}

async function getJSON(path) {
  const res = await fetch(path);            // may throw — callers decide
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

/* ── Theme toggle ─────────────────────────────────────────────────────────
   On load: apply stored value if present; otherwise leave dataset.theme
   UNSET so CSS follows the OS preference media query. ---------------------- */
function initTheme() {
  const stored = localStorage.getItem(LS_THEME);
  if (stored === "light" || stored === "dark") {
    document.documentElement.dataset.theme = stored;
  }
  document.getElementById("theme-toggle").addEventListener("click", () => {
    const cur = document.documentElement.dataset.theme;
    let next;
    if (cur === "dark") next = "light";
    else if (cur === "light") next = "dark";
    else {
      // No explicit theme yet: flip away from the current OS preference.
      const osDark = window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: dark)").matches;
      next = osDark ? "light" : "dark";
    }
    document.documentElement.dataset.theme = next;
    localStorage.setItem(LS_THEME, next);
  });
}

/* ── Meta + add-form + filter bar ─────────────────────────────────────────── */
async function loadMeta() {
  let res, m;
  try {
    res = await fetch("/api/meta");
  } catch (e) {
    // Network-level failure: fetch itself threw (e.g. TypeError from a
    // browser extension blocking loopback requests, or the server stopped).
    const msg = "Can't reach the kanban server — a browser extension may be"
      + " blocking localhost requests, or the server stopped."
      + " Open this page via http://127.0.0.1:<port> directly.";
    document.getElementById("project-name").textContent = "Connection error";
    toast(msg);
    return;
  }
  if (!res.ok) {
    let errMsg;
    if (res.status === 403) {
      let rejectedHost = "";
      try {
        const body = await res.json();
        if (body && body.host) rejectedHost = " (rejected host: " + body.host + ")";
      } catch (_) { /* ignore parse failure */ }
      errMsg = "Server rejected the request (forbidden host" + rejectedHost + ")."
        + " Open this page via http://127.0.0.1:<port> or http://localhost:<port>.";
    } else {
      let detail = "";
      try { const body = await res.json(); if (body && body.error) detail = ": " + body.error; }
      catch (_) { /* ignore */ }
      errMsg = "Server error loading board metadata (HTTP " + res.status + detail + ").";
    }
    document.getElementById("project-name").textContent = "Load error";
    toast(errMsg);
    return;
  }
  try { m = await res.json(); } catch (e) {
    document.getElementById("project-name").textContent = "Load error";
    toast("Server returned unreadable metadata — check server logs.");
    return;
  }
  META = {
    project: m.project || "kanban",
    file: m.file || "",
    columns: Array.isArray(m.columns) && m.columns.length ? m.columns : COLUMNS,
    categories: Array.isArray(m.categories) ? m.categories : []
  };
  document.getElementById("project-name").textContent = META.project;
  document.getElementById("board-file").textContent = META.file;
  document.title = META.project + " · kanban";
  buildCategorySelect();
  buildFilterBar();
}

function buildCategorySelect() {
  const sel = document.getElementById("add-category");
  sel.replaceChildren();
  const cats = META.categories.length ? META.categories : ["other"];
  for (const c of cats) sel.appendChild(el("option", { text: c, attrs: { value: c } }));
}

function buildFilterBar() {
  const bar = document.getElementById("filter-bar");
  // Keep the label, drop old chips.
  bar.querySelectorAll(".chip").forEach(n => n.remove());

  const stored = localStorage.getItem(LS_FILTER);
  if (stored && (stored === "all" || META.categories.includes(stored))) activeFilter = stored;
  else activeFilter = "all";

  bar.appendChild(makeChip("all", "All", null));
  for (const c of META.categories) {
    const v = catVars(c);
    bar.appendChild(makeChip(c, c, v.strong, v.soft));
  }
  reflectFilterState();
}

function makeChip(value, label, strong, soft) {
  const chip = el("button", {
    class: "chip",
    attrs: { type: "button", "data-filter": value, "aria-pressed": "false" }
  });
  if (strong) {
    chip.style.setProperty("--chip-strong", strong);
    chip.style.setProperty("--chip-soft", soft);
    chip.appendChild(el("span", { class: "dot", attrs: { "aria-hidden": "true" } }));
  }
  chip.appendChild(document.createTextNode(label));
  chip.addEventListener("click", () => {
    activeFilter = value;
    localStorage.setItem(LS_FILTER, value);
    reflectFilterState();
    applyFilter();
  });
  return chip;
}

function reflectFilterState() {
  document.querySelectorAll("#filter-bar .chip").forEach(chip => {
    chip.setAttribute("aria-pressed",
      chip.dataset.filter === activeFilter ? "true" : "false");
  });
}

/* Filtering ONLY hides cards (hidden attr => display:none). The cards stay in
   the DOM and remain draggable, so drag-and-drop of visible cards is intact. */
function applyFilter() {
  document.querySelectorAll(".card").forEach(card => {
    const show = activeFilter === "all" || card.dataset.category === activeFilter;
    card.hidden = !show;
  });
  updateCounts();
}

function updateCounts() {
  for (const col of COLUMNS) {
    const list = document.getElementById("col-" + slug(col));
    if (!list) continue;
    const visible = list.querySelectorAll(".card:not([hidden])").length;
    const badge = document.getElementById("cnt-" + slug(col));
    if (badge) badge.textContent = String(visible);
    const empty = list.querySelector(".col-empty");
    if (empty) empty.hidden = visible > 0;
  }
}

/* ── Sort control ─────────────────────────────────────────────────────────── */
function initSort() {
  const stored = localStorage.getItem(LS_SORT);
  if (stored && SORT_OPTIONS.includes(stored)) activeSort = stored;
  else activeSort = "recent";
  const sel = document.getElementById("sort-by");
  sel.value = activeSort;
  sel.addEventListener("change", () => {
    activeSort = SORT_OPTIONS.includes(sel.value) ? sel.value : "recent";
    localStorage.setItem(LS_SORT, activeSort);
    if (lastBoard) renderBoard(lastBoard);
  });
}

/* Sort a copy of a column's items array per activeSort.
   "recent" is a no-op (preserves server order).
   "category" uses a stable index-keyed composite sort.
   "title" and "id" are case-insensitive / numeric. */
function sortItems(items) {
  if (activeSort === "recent") return items.slice();
  const indexed = items.map((t, i) => ({ t, i }));
  if (activeSort === "category") {
    indexed.sort((a, b) => {
      const ca = (a.t.category || "other").toLowerCase();
      const cb = (b.t.category || "other").toLowerCase();
      if (ca < cb) return -1;
      if (ca > cb) return 1;
      return a.i - b.i;   // stable: preserve server order within category
    });
  } else if (activeSort === "title") {
    indexed.sort((a, b) => {
      const ta = (a.t.title || "").toLowerCase();
      const tb = (b.t.title || "").toLowerCase();
      if (ta < tb) return -1;
      if (ta > tb) return 1;
      return a.i - b.i;
    });
  } else if (activeSort === "id") {
    // K-<num> ids sort numerically; freeform ids (e.g. "#219", "gov",
    // "71.2") fall back to stable insertion order via string comparison
    // rather than coercing everything to 0 and losing order.
    indexed.sort((a, b) => {
      const ia = String(a.t.id || "");
      const ib = String(b.t.id || "");
      const ka = /^K-(\d+)$/.exec(ia);
      const kb = /^K-(\d+)$/.exec(ib);
      if (ka && kb) return parseInt(ka[1], 10) - parseInt(kb[1], 10);
      // Mixed or both freeform: numeric K-N beats freeform (comes first),
      // two freeform ids are compared lexicographically for a stable sort.
      if (ka) return -1;
      if (kb) return 1;
      if (ia < ib) return -1;
      if (ia > ib) return 1;
      return a.i - b.i;
    });
  }
  return indexed.map(x => x.t);
}

/* ── Board scaffold + render ──────────────────────────────────────────────── */
function buildBoard() {
  const board = document.getElementById("board");
  board.replaceChildren();
  for (const col of COLUMNS) {
    const head = el("div", { class: "column-head", children: [
      el("h2", { text: col === "IN PROGRESS" ? "In Progress" : titleCase(col) }),
      el("span", { class: "count", attrs: { id: "cnt-" + slug(col),
                   "aria-label": "visible cards" }, text: "0" })
    ]});
    const list = el("div", { class: "list", attrs: { id: "col-" + slug(col) } });
    const section = el("section", {
      class: "column",
      attrs: { "data-col": col, "aria-label": col, role: "list" },
      children: [head, list]
    });
    wireDropTarget(section, col);
    board.appendChild(section);
  }
}

function titleCase(s) { return s.charAt(0) + s.slice(1).toLowerCase(); }

function renderBoard(data) {
  lastBoard = data;
  for (const col of COLUMNS) {
    const list = document.getElementById("col-" + slug(col));
    if (!list) continue;
    list.replaceChildren();
    const items = sortItems(Array.isArray(data[col]) ? data[col] : []);
    for (const t of items) list.appendChild(buildCard(t, col));
    // Empty-state placeholder (Nielsen #1 visibility of system status):
    list.appendChild(el("div", {
      class: "col-empty", text: "No cards here yet",
      attrs: { "aria-hidden": "true" }
    }));
  }
  // Re-open any editors the user had open before this poll repaint.
  restoreOpenEditors();
  applyFilter();
}

function buildCard(t, col) {
  const id = String(t.id == null ? "" : t.id);
  const v = catVars(t.category);

  const card = el("article", {
    class: "card",
    attrs: {
      "data-id": id,
      "data-category": v.name,
      draggable: "true",
      role: "listitem",
      tabindex: "0",
      "aria-label": id + ": " + (t.title || "untitled")
    }
  });
  card.style.setProperty("--cat-strong", v.strong);
  card.style.setProperty("--cat-soft", v.soft);

  // Top row: id badge + category chip
  const top = el("div", { class: "card-top", children: [
    el("span", { class: "id-badge", text: id }),
    el("span", { class: "cat-chip", text: v.name })
  ]});
  card.appendChild(top);

  card.appendChild(el("h3", { class: "card-title", text: t.title || "(untitled)" }));

  // Tags: @assigned, blocked
  const tags = el("div", { class: "tags" });
  if (t.assigned && t.assigned !== "unassigned")
    tags.appendChild(el("span", { class: "tag", text: "@" + t.assigned }));
  if (t.blockers && t.blockers !== "none")
    tags.appendChild(el("span", { class: "tag blocker", text: "⚠ " + t.blockers }));
  if (tags.childNodes.length) card.appendChild(tags);

  card.appendChild(buildNotes(id, t.notes));
  card.appendChild(buildMoveActions(id, col));
  card.appendChild(buildDeleteButton(id, t.title));

  // Drag wiring
  card.addEventListener("dragstart", e => {
    dragId = id;
    e.dataTransfer.setData("text/plain", id);
    e.dataTransfer.effectAllowed = "move";
    card.classList.add("dragging");
  });
  card.addEventListener("dragend", () => {
    dragId = null;
    card.classList.remove("dragging");
  });

  return card;
}

/* Notes: truncated view + inline textarea editor (Save/Cancel). */
function buildNotes(id, rawNotes) {
  const notes = String(rawNotes == null ? "" : rawNotes);
  const wrap = el("div", { class: "notes", attrs: { "data-notes-for": id } });

  const view = el("div", { class: "notes-view" });
  const text = el("div", {
    class: "notes-text" + (notes.trim() ? "" : " empty"),
    text: notes.trim() ? notes : "No notes"
  });
  const editBtn = el("button", {
    class: "notes-edit-btn",
    attrs: { type: "button", "aria-label": "Edit notes for " + id },
    text: "edit"
  });
  view.append(text, editBtn);

  const editor = el("div", { class: "notes-editor" });
  const ta = el("textarea", { attrs: { "aria-label": "Notes for " + id } });
  ta.value = notes;
  const actions = el("div", { class: "editor-actions" });
  const save = el("button", { class: "btn primary", attrs: { type: "button" }, text: "Save" });
  const cancel = el("button", { class: "btn", attrs: { type: "button" }, text: "Cancel" });
  actions.append(save, cancel);
  editor.append(ta, actions);

  editBtn.addEventListener("click", () => {
    openEditors.add(id);
    wrap.classList.add("editing");
    ta.focus();
  });
  cancel.addEventListener("click", () => {
    ta.value = notes;            // revert
    openEditors.delete(id);
    wrap.classList.remove("editing");
  });
  save.addEventListener("click", async () => {
    save.disabled = true;
    const data = await post("/api/notes", { id, notes: ta.value });
    save.disabled = false;
    openEditors.delete(id);
    wrap.classList.remove("editing");
    if (data) renderBoard(data);
  });

  wrap.append(view, editor);
  return wrap;
}

/* Keyboard-free / no-drag fallback: explicit move buttons to the other two
   columns. Also POST /api/move. (WCAG 2.2 - 2.5.7 dragging movements: provide
   a single-pointer alternative to drag.) */
function buildMoveActions(id, col) {
  const wrap = el("div", { class: "move-actions" });
  for (const target of COLUMNS.filter(c => c !== col)) {
    const label = target === "DONE" ? "✓ Done"
      : target === "IN PROGRESS" ? "→ In Progress"
      : "← To Do";
    const btn = el("button", {
      class: "btn",
      attrs: { type: "button", "aria-label": "Move " + id + " to " + target },
      text: label
    });
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      const data = await post("/api/move", { id, col: target });
      if (data) renderBoard(data); else btn.disabled = false;
    });
    wrap.appendChild(btn);
  }
  return wrap;
}

/* Delete affordance: idle trigger + explicit inline Confirm/Cancel pair.
   First click on “Delete”: hides the trigger, reveals a confirm row with
   a labelled “Confirm delete” button (red) and a “Cancel” button.  The
   confirm button POSTs to /api/delete.  Cancel (or a 6s auto-timeout)
   reverts to idle.  No document-level capture listeners, no text-swap
   on the same element — the confirm is a separate clickable button with
   its own handler, so there is no event-ordering race. */
function buildDeleteButton(id, title) {
  const wrap = el("div", { class: "delete-wrap" });

  // Idle trigger.
  const trigger = el("button", {
    class: "delete-trigger",
    attrs: { type: "button", "aria-label": "Delete task " + id },
    text: "Delete"
  });

  // Confirm row (hidden until first click).
  const confirmRow = el("div", { class: "delete-confirm" });
  const label = el("div", {
    class: "delete-confirm-label",
    text: "Delete \u201c" + (title || id) + "\u201d?"
  });
  const yesBtn = el("button", {
    class: "delete-confirm-yes",
    attrs: { type: "button", "aria-label": "Confirm delete task " + id },
    text: "Confirm delete"
  });
  const noBtn = el("button", {
    class: "delete-confirm-no",
    attrs: { type: "button", "aria-label": "Cancel delete task " + id },
    text: "Cancel"
  });
  confirmRow.append(label, yesBtn, noBtn);
  wrap.append(trigger, confirmRow);

  let autoResetTimer = null;

  function showIdle() {
    if (autoResetTimer) { clearTimeout(autoResetTimer); autoResetTimer = null; }
    trigger.hidden = false;
    confirmRow.classList.remove("visible");
    yesBtn.disabled = false;
  }

  function showConfirm() {
    trigger.hidden = true;
    confirmRow.classList.add("visible");
    yesBtn.focus();
    // Auto-revert after 6s if operator walks away.
    autoResetTimer = setTimeout(showIdle, 6000);
  }

  trigger.addEventListener("click", e => {
    e.stopPropagation();
    showConfirm();
  });

  noBtn.addEventListener("click", e => {
    e.stopPropagation();
    showIdle();
  });

  yesBtn.addEventListener("click", async e => {
    e.stopPropagation();
    if (autoResetTimer) { clearTimeout(autoResetTimer); autoResetTimer = null; }
    yesBtn.disabled = true;
    const data = await post("/api/delete", { id });
    // If the POST failed, data is null and toast() was already called.
    // Re-enable so the operator can try again.
    if (!data) { yesBtn.disabled = false; return; }
    renderBoard(data);
  });

  return wrap;
}

function restoreOpenEditors() {
  for (const id of openEditors) {
    const wrap = document.querySelector(".notes[data-notes-for=\"" + cssEscape(id) + "\"]");
    if (wrap) wrap.classList.add("editing"); else openEditors.delete(id);
  }
}
function cssEscape(s) {
  return (window.CSS && CSS.escape) ? CSS.escape(s) : String(s).replace(/["\\]/g, "\\$&");
}

/* ── Drag drop targets ────────────────────────────────────────────────────── */
function wireDropTarget(section, col) {
  section.addEventListener("dragover", e => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    section.classList.add("drop-over");
  });
  section.addEventListener("dragleave", e => {
    if (!section.contains(e.relatedTarget)) section.classList.remove("drop-over");
  });
  section.addEventListener("drop", async e => {
    e.preventDefault();
    section.classList.remove("drop-over");
    const id = e.dataTransfer.getData("text/plain") || dragId;
    if (!id) return;
    const data = await post("/api/move", { id, col });
    if (data) renderBoard(data);
  });
}

/* ── Add ──────────────────────────────────────────────────────────────────── */
function initAddForm() {
  const form = document.getElementById("add-form");
  const input = document.getElementById("add-title");
  const sel = document.getElementById("add-category");
  form.addEventListener("submit", async e => {
    e.preventDefault();                       // Enter in the input submits here
    const title = input.value.trim();
    if (!title) return;
    const data = await post("/api/add", { title, category: sel.value });
    if (data) {
      input.value = "";
      input.focus();
      renderBoard(data);
    }
  });
}

/* ── Poll loop ──────────────────────────────────────────────────────────────── */
async function refresh() {
  let res, board;
  try {
    res = await fetch("/api/board");
  } catch (e) {
    // Network-level failure (fetch threw). Show one toast per consecutive run
    // of failures so the retry loop doesn't spam.
    if (!liveErrShown) {
      liveErrShown = true;
      toast("Can't reach the kanban server — a browser extension may be blocking"
        + " localhost requests, or the server stopped.");
    }
    setLive(false);
    return;
  }
  if (!res.ok) {
    if (!liveErrShown) {
      liveErrShown = true;
      if (res.status === 403) {
        let rejectedHost = "";
        try {
          const body = await res.json();
          if (body && body.host) rejectedHost = " (rejected host: " + body.host + ")";
        } catch (_) { /* ignore */ }
        toast("Server rejected the request (forbidden host" + rejectedHost + ")."
          + " Open this page via http://127.0.0.1:<port> or http://localhost:<port>.");
      } else {
        toast("Board refresh failed (HTTP " + res.status + "). Retrying…");
      }
    }
    setLive(false);
    return;
  }
  try { board = await res.json(); } catch (e) {
    if (!liveErrShown) { liveErrShown = true; toast("Board response unreadable — check server logs."); }
    setLive(false);
    return;
  }
  liveErrShown = false;   // clear flag: connection is healthy again
  setLive(true);
  renderBoard(board);
}
function setLive(ok) {
  const dot = document.getElementById("live-dot");
  const txt = document.getElementById("status-text");
  dot.classList.toggle("stale", !ok);
  txt.textContent = ok
    ? "Local board · auto-refresh " + (POLL_MS / 1000) + "s · 127.0.0.1 only"
    : "Reconnecting… (board server unreachable — see error message above)";
}

/* ── Boot ───────────────────────────────────────────────────────────────── */
async function boot() {
  initTheme();
  initSort();
  buildBoard();
  initAddForm();
  await loadMeta();      // categories must exist before the first render
  await refresh();
  pollTimer = setInterval(refresh, POLL_MS);
}
boot();
</script>
</body>
</html>
"""


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _host_ok(self):
        # Reject requests whose Host header isn't a name we bind. This is the
        # cheap, effective defense against DNS-rebinding: a malicious page
        # that rebinds a hostname to 127.0.0.1 still sends ITS hostname in
        # Host, which won't be in ALLOWED_HOSTS.
        host = (self.headers.get("Host", "") or "").rsplit(":", 1)[0]
        if host not in ALLOWED_HOSTS:
            self._send(403, json.dumps({"error": "forbidden host", "host": host}))
            return False
        return True

    def _send(self, code, body, ctype="application/json"):
        data = body.encode("utf-8") if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype + "; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    def do_GET(self):
        if not self._host_ok():
            return
        path = self.path.split("?", 1)[0]
        if path == "/":
            self._send(200, PAGE, "text/html")
        elif path == "/api/board":
            self._send(200, json.dumps(parse_board()))
        elif path == "/api/meta":
            board = parse_board()
            cats = _board_categories(board)
            self._send(200, json.dumps({"project": PROJECT, "file": KANBAN_FILE,
                                        "columns": COLUMNS, "categories": cats}))
        elif path == "/favicon.ico":
            self._send(204, "")
        else:
            self._send(404, json.dumps({"error": "not found"}))

    def do_POST(self):
        if not self._host_ok():
            return
        try:
            length = int(self.headers.get("Content-Length", "0") or 0)
        except ValueError:
            length = 0
        if length > MAX_BODY:
            return self._send(413, json.dumps({"error": "request too large"}))
        raw = self.rfile.read(length) if length else b""
        try:
            data = json.loads(raw or b"{}")
        except ValueError:
            return self._send(400, json.dumps({"error": "invalid JSON"}))
        if not isinstance(data, dict):
            return self._send(400, json.dumps({"error": "expected object"}))

        if self.path == "/api/add":
            title = str(data.get("title", "")).strip()
            if not title:
                return self._send(400, json.dumps({"error": "title required"}))
            if len(title) > 500:
                return self._send(400, json.dumps({"error": "title too long"}))
            if "\n" in title or "\r" in title:
                return self._send(400, json.dumps({"error": "title must be one line"}))
            # Backslash is a sed metacharacter at the cmd_add sink — reject it
            # rather than risk silent corruption / a broken sed expression.
            if "\\" in title:
                return self._send(400, json.dumps({"error": "title may not contain backslashes"}))
            category = str(data.get("category", "other") or "other").strip()
            if "\\" in category or "\n" in category or "\r" in category:
                return self._send(400, json.dumps({"error": "invalid category value"}))
            result = run_kanban(["add", "--category", category, title])
        elif self.path == "/api/move":
            tid = str(data.get("id", "")).strip()
            col = str(data.get("col", "")).strip()
            if not ID_RE.match(tid):
                return self._send(400, json.dumps({"error": "bad task id"}))
            if col not in COLUMNS:
                return self._send(400, json.dumps({"error": "bad column"}))
            result = run_kanban(["move", tid, col])
        elif self.path == "/api/notes":
            # Accept {id, notes}. Validate id format; notes capped at 2000
            # chars, backslashes rejected (awk sink). Raw newlines are
            # converted to spaces at the bash layer (cmd_notes uses tr),
            # so we also do it here for consistency when the client sends
            # JSON with embedded \n sequences.
            tid = str(data.get("id", "")).strip()
            if not ID_RE.match(tid):
                return self._send(400, json.dumps({"error": "bad task id"}))
            notes = str(data.get("notes", "") or "")
            # Collapse any literal newlines to spaces — notes are single-line
            # in the markdown; the bash cmd_notes does the same via tr.
            notes = notes.replace("\r\n", " ").replace("\n", " ").replace("\r", " ")
            if len(notes) > 2000:
                return self._send(400, json.dumps({"error": "notes too long (max 2000 chars)"}))
            if "\\" in notes:
                return self._send(400, json.dumps({"error": "notes may not contain backslashes"}))
            result = run_kanban(["notes", tid, notes])
        elif self.path == "/api/delete":
            # Accept {id}. Validate id format; shell into cmd_delete which
            # validates existence and echoes the removed task.
            tid = str(data.get("id", "")).strip()
            if not ID_RE.match(tid):
                return self._send(400, json.dumps({"error": "bad task id"}))
            result = run_kanban(["delete", tid])
        else:
            return self._send(404, json.dumps({"error": "not found"}))

        if result.returncode != 0:
            msg = (result.stderr or "command failed").strip()[:500]
            return self._send(500, json.dumps({"error": msg}))
        self._send(200, json.dumps(parse_board()))


def write_state(port):
    if not KANBAN_STATE:
        return
    payload = {"pid": os.getpid(), "host": HOST, "port": port,
               "url": "http://%s:%d" % (HOST, port), "project": PROJECT}
    tmp = KANBAN_STATE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        fh.write(json.dumps(payload) + "\n")
    os.replace(tmp, KANBAN_STATE)


def remove_state():
    if KANBAN_STATE:
        try:
            os.remove(KANBAN_STATE)
        except OSError:
            pass


def main():
    try:
        httpd = ThreadingHTTPServer((HOST, PORT), Handler)
    except OSError as exc:
        sys.stderr.write("kanban serve: cannot bind %s:%s (%s)\n" % (HOST, PORT, exc))
        sys.exit(1)

    bound_port = httpd.server_address[1]
    ALLOWED_HOSTS.add(HOST)
    # Now that we're actually bound, publish state. Its existence means the
    # server is up — the launcher polls for it, so writing it only here (not
    # speculatively in bash) prevents reporting a URL that isn't serving.
    write_state(bound_port)

    # SIGTERM (from `yakos kanban stop` / the bash trap) should shut down
    # gracefully so the finally-block removes the state file.
    def _graceful(signum, frame):
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, _graceful)

    if OPEN:
        try:
            import webbrowser
            webbrowser.open("http://%s:%d" % (HOST, bound_port))
        except Exception:
            pass
    try:
        httpd.serve_forever()
    except (KeyboardInterrupt, SystemExit):
        sys.stderr.write("\nkanban serve: stopped\n")
    finally:
        remove_state()
        httpd.server_close()


main()
PY
    py_pid=$!

    # python writes the state file only after it successfully binds. Poll for
    # it (≈3s) so we report a URL that's actually serving — not one we hoped
    # for. If python failed to bind, the file never appears and we say so.
    local i=0 url=""
    while [ "$i" -lt 30 ]; do
        if kill -0 "$py_pid" 2>/dev/null; then
            if [ -f "$state" ]; then
                url="$(_kanban_state_get url 2>/dev/null || true)"
                [ -n "$url" ] && break
            fi
        else
            break   # python exited (bind failure) — stop waiting
        fi
        i=$((i + 1)); sleep 0.1
    done

    if [ -n "$url" ]; then
        ct_log "kanban web UI → $url   (project: $project)"
        ct_log "press Ctrl-C to stop"
    else
        ct_log "kanban web UI: server failed to start (could not bind)"
    fi

    wait "$py_pid"; rc=$?
    trap - INT TERM
    rm -f "$state" 2>/dev/null || true
    return "$rc"
}

# cmd_serve_status — print whether a web UI is running for this board.
cmd_serve_status() {
    if _kanban_server_alive; then
        printf 'kanban web UI: running\n  url:     %s\n  pid:     %s\n  project: %s\n' \
            "$(_kanban_state_get url)" "$(_kanban_state_get pid)" "$(_kanban_state_get project)"
    else
        ct_log "kanban web UI: not running"
        return 1
    fi
}

# cmd_serve_stop — stop ALL kanban web servers for THIS PROJECT'S board.
#
# Strategy:
#   1. Collect pids from the state file (fast, common case) and from a
#      project-scoped live process scan (catches orphans the state file
#      doesn't know about).  The scan filters by this board's canonical
#      file path, so servers for OTHER projects are never in the candidate
#      set and are never killed.
#   2. For each candidate pid, verify with _kanban_pid_is_server before
#      killing — never kill a recycled/unrelated pid.
#   3. Report each pid terminated via ct_log.
#   4. Remove the state file at the end regardless.
cmd_serve_stop() {
    local found=0

    # Resolve this board's canonical path for project-scoped scan.
    local stop_file stop_canon
    stop_file="$(_kanban_file)"
    stop_canon="$(ct_realpath "$stop_file" 2>/dev/null || printf '%s' "$stop_file")"

    # Candidate set: state-file pid (may be empty/stale) + project-scoped scan.
    local state_pid scan_pids all_pids
    state_pid="$(_kanban_state_get pid 2>/dev/null || true)"
    scan_pids="$(_kanban_find_live_pids "$stop_canon")"

    # Merge into a deduplicated list.
    all_pids="$(printf '%s\n%s\n' "$state_pid" "$scan_pids" \
        | tr -s '\n' '\n' | grep -E '^[0-9]+$' | sort -nu)"

    local pid
    for pid in $all_pids; do
        # Safety gate: only kill if the pid still looks like our python server.
        if _kanban_pid_is_server "$pid"; then
            kill "$pid" 2>/dev/null || true
            ct_log "kanban web UI: stopped (pid $pid)"
            found=$((found + 1))
        else
            ct_log "kanban web UI: pid $pid is not our server (stale or recycled); skipping"
        fi
    done

    rm -f "$(_kanban_state_file)" 2>/dev/null || true

    if [ "$found" -eq 0 ]; then
        ct_log "kanban web UI: not running"
    fi
}

case "${1:-}" in
    add)            shift; cmd_add "$@" ;;
    notes)          shift; cmd_notes "$@" ;;
    move)           shift; cmd_move "$@" ;;
    done)           shift; cmd_done "$@" ;;
    delete|rm)      shift; cmd_delete "$@" ;;
    --html)         shift; cmd_render_html "$@" ;;
    serve|--serve)  shift; cmd_serve "$@" ;;
    status)         shift; cmd_serve_status "$@" ;;
    stop)           shift; cmd_serve_stop "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos kanban — 3-column markdown board in scratchpad

  yakos kanban                       # render TUI
  yakos kanban --html [<out>]        # render static HTML snapshot
  yakos kanban serve [--port N]      # live web UI: view + manage
                                     #   [--host H] [--no-open]
                                     #   (default: random high port on 127.0.0.1)
  yakos kanban status                # is the web UI running? print its URL
  yakos kanban stop                  # stop the running web UI
  yakos kanban add "<title>"         # append to TODO
                [--category <c>]     #   category (default: other)
                [--notes "<text>"]   #   initial notes (single line)
  yakos kanban notes <id> "<text>"   # set/replace notes field (any column)
  yakos kanban move <id> <col>       # move between columns
  yakos kanban done <id>             # shortcut to DONE
  yakos kanban delete <id>           # hard-delete a task (also: rm)

Known categories: bug  feature  chore  question  other  (arbitrary values accepted)
EOF
        ;;
    "") cmd_render_tui ;;
    *) ct_die "yakos kanban: unknown subcommand '$1'" ;;
esac
