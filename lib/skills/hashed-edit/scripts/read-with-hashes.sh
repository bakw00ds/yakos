#!/usr/bin/env bash
# Purpose: Read a file and tag every line with a content-hash anchor.
#   Output format: <lineno>#<hash>|<content>
#   The hash is a 4-char hex digest of the line content (cksum % 65536).
#   Output is plain UTF-8; lines preserved including blanks.
# Inputs:
#   $1: file path (required)
#   --start <N>: optional start line (1-indexed; default 1)
#   --end <N>:   optional end line   (1-indexed inclusive; default EOF)
# Outputs:
#   stdout: one tagged line per input line
#   stderr: errors only
# Exit codes: 0 success, 2 user error, 4 file error.
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: read-with-hashes.sh <file> [--start N] [--end N]

Reads <file> and outputs every line tagged with a content-hash anchor:
    <lineno>#<hash>|<content>

The hash is a 4-char hex digest (cksum % 65536). Hash mismatches on a
later edit-by-hash call indicate the line was modified since this read,
catching stale-line edit failures before they corrupt the file.
EOF
}

file=""
start_line=1
end_line=""

while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --start) start_line="${2:?}"; shift 2 ;;
        --end)   end_line="${2:?}"; shift 2 ;;
        --) shift; break ;;
        -*) echo "read-with-hashes: unknown flag: $1" >&2; exit 2 ;;
        *)
            if [ -z "$file" ]; then file="$1"; shift
            else echo "read-with-hashes: too many positional args" >&2; exit 2; fi
            ;;
    esac
done

if [ -z "$file" ]; then usage >&2; exit 2; fi
if [ ! -f "$file" ]; then echo "read-with-hashes: not a file: $file" >&2; exit 4; fi

case "$start_line" in (''|*[!0-9]*) echo "read-with-hashes: --start must be int" >&2; exit 2 ;; esac
if [ -n "$end_line" ]; then
    case "$end_line" in (''|*[!0-9]*) echo "read-with-hashes: --end must be int" >&2; exit 2 ;; esac
fi

line_no=0
while IFS= read -r line || [ -n "$line" ]; do
    line_no=$((line_no + 1))
    if [ "$line_no" -lt "$start_line" ]; then continue; fi
    if [ -n "$end_line" ] && [ "$line_no" -gt "$end_line" ]; then break; fi
    # cksum on the line content (no trailing newline) → 32-bit checksum
    # Take low 16 bits, format as 4 hex chars.
    hash="$(printf '%s' "$line" | cksum | awk '{ printf "%04x", $1 % 65536 }')"
    printf '%d#%s|%s\n' "$line_no" "$hash" "$line"
done < "$file"
