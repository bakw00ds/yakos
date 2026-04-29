#!/usr/bin/env bash
# Purpose: Apply an in-place line replacement to a file, anchored by a
#   content hash. Refuses the edit if the hash of the current line at
#   the target line number doesn't match — that mismatch means the
#   line was modified since the agent saw it, and a blind edit would
#   corrupt the file.
# Inputs:
#   $1: file path (required)
#   $2: anchor in <lineno>#<hash> form (required)
#   $3: new content for that line (required; may be empty for delete)
#   --delete: if set, $3 is ignored and the line is deleted
# Outputs:
#   stdout: nothing on success
#   stderr: hash-mismatch diff on failure (current line vs anchor)
# Exit codes:
#   0 success (file modified)
#   2 user error (bad args)
#   4 file error (missing file, anchor parse failure)
#   5 hash mismatch (line at lineno has different content than anchor)
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: edit-by-hash.sh <file> <lineno>#<hash> <new-content> [--delete]

Replaces the line at <lineno> with <new-content> IFF the current line's
content-hash matches <hash>. Otherwise, refuses the edit and shows a
diff (current line vs anchor).

Anchors come from read-with-hashes.sh output. The hash is the 4 chars
between # and | in that output.
EOF
}

# Parse args
file=""
anchor=""
new_content=""
delete_mode=0
got_new_content=0

while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --delete) delete_mode=1; shift ;;
        *)
            if [ -z "$file" ]; then file="$1"; shift
            elif [ -z "$anchor" ]; then anchor="$1"; shift
            elif [ "$got_new_content" -eq 0 ]; then new_content="$1"; got_new_content=1; shift
            else echo "edit-by-hash: too many positional args" >&2; exit 2; fi
            ;;
    esac
done

if [ -z "$file" ] || [ -z "$anchor" ]; then usage >&2; exit 2; fi
if [ "$delete_mode" -eq 0 ] && [ "$got_new_content" -eq 0 ]; then
    echo "edit-by-hash: missing <new-content>; use --delete to remove the line" >&2
    exit 2
fi
if [ ! -f "$file" ]; then echo "edit-by-hash: not a file: $file" >&2; exit 4; fi

# Parse anchor: <lineno>#<hash>
case "$anchor" in
    *'#'*) ;;
    *) echo "edit-by-hash: bad anchor: $anchor (expected <lineno>#<hash>)" >&2; exit 4 ;;
esac
anchor_line="${anchor%%#*}"
anchor_hash="${anchor#*#}"
case "$anchor_line" in (''|*[!0-9]*) echo "edit-by-hash: anchor lineno not numeric: $anchor_line" >&2; exit 4 ;; esac
if [ -z "$anchor_hash" ]; then echo "edit-by-hash: anchor hash empty" >&2; exit 4; fi

# Read the current line at anchor_line and compute its hash
current_line="$(awk -v n="$anchor_line" 'NR==n { print; exit }' "$file")"
total_lines="$(wc -l < "$file" | tr -d ' ')"
# Account for files with no trailing newline
if [ "$anchor_line" -gt "$total_lines" ]; then
    # Could still be valid if the file has content on the last line w/o newline
    if [ "$anchor_line" -eq "$((total_lines + 1))" ]; then
        # Try reading via tail in case awk missed a no-newline last line
        current_line="$(tail -c +1 "$file" | awk -v n="$anchor_line" 'NR==n { print; exit }')"
    fi
    if [ -z "$current_line" ]; then
        echo "edit-by-hash: line $anchor_line out of range ($total_lines lines)" >&2
        exit 4
    fi
fi

current_hash="$(printf '%s' "$current_line" | cksum | awk '{ printf "%04x", $1 % 65536 }')"

if [ "$current_hash" != "$anchor_hash" ]; then
    {
        echo "edit-by-hash: HASH MISMATCH at line $anchor_line"
        echo "  anchor: $anchor"
        echo "  actual: ${anchor_line}#${current_hash}|${current_line}"
        echo
        echo "The line content changed since the read that produced the anchor."
        echo "Re-read the file with read-with-hashes.sh and retry."
    } >&2
    exit 5
fi

# Apply the edit. Use awk to rewrite the file in a tmp, then mv.
tmp="$(mktemp "${file}.edit-by-hash.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

if [ "$delete_mode" -eq 1 ]; then
    awk -v n="$anchor_line" 'NR != n' "$file" > "$tmp"
else
    awk -v n="$anchor_line" -v repl="$new_content" 'NR == n { print repl; next } { print }' "$file" > "$tmp"
fi

mv "$tmp" "$file"
trap - EXIT
