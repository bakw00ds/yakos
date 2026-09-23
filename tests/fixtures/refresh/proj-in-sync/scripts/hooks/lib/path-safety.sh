#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: path-safety.sh — path normalization + symlink-escape detection
# shared by hooks that make allow/deny decisions based on a file path
# (currently path-allowlist.sh).
#
# Security review C3 / M7 (2026-09-14): path-allowlist.sh matched glob
# patterns against the raw path with no normalization, so
# "api/../../../../etc/cron.d/pwn" satisfied an allow pattern that scoped
# to "api/**", and a symlink placed inside an allowed directory but
# pointing outside it was never resolved before the match.
#
# Usage:
#     . "$HOOK_DIR/lib/path-safety.sh"
#     norm="$(ps_lexical_normalize "$rel_file")"
#     case "$norm" in ..|../*) : escapes root, block ;; esac
#     real="$(ps_realpath "$abs_file")"
#     ps_is_within "$real_root" "$real" || : symlink escapes root, block

if [ "${PS_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
PS_LOADED=1

# ps_lexical_normalize <path>
#   Collapse "." segments, drop duplicate "/"s, and resolve ".." against
#   preceding segments — WITHOUT touching the filesystem (works for paths
#   that don't exist yet, e.g. a Write that will create a new file).
#
#   Any ".." that cannot be resolved against a real preceding segment (i.e.
#   the path tries to climb above where it started) survives at the FRONT
#   of the result, contiguous — callers detect an escape with:
#       case "$result" in ..|../*) escapes ;; esac
#   A leading "/" is treated as a bare separator (this helper only ever
#   receives already project-relative paths in this codebase).
ps_lexical_normalize() {
    local input="$1"
    local -a parts=()
    # `read -ra` splits on IFS without pathname expansion (unlike an
    # unquoted `(${input})` array assignment, which would glob-expand any
    # '*'/'?' in the path against the cwd — exactly the kind of surprise
    # this hook exists to avoid).
    local old_ifs="$IFS"
    IFS='/'
    read -r -a parts <<< "$input"
    IFS="$old_ifs"
    local -a out=()
    local n=0
    local seg
    for seg in "${parts[@]}"; do
        case "$seg" in
            ''|'.') continue ;;
            '..')
                if [ "$n" -gt 0 ] && [ "${out[$((n - 1))]}" != '..' ]; then
                    unset "out[$((n - 1))]"
                    out=("${out[@]}")
                    n=$((n - 1))
                else
                    out[n]='..'
                    n=$((n + 1))
                fi
                ;;
            *)
                out[n]="$seg"
                n=$((n + 1))
                ;;
        esac
    done
    local result="" i
    for ((i = 0; i < n; i++)); do
        if [ -z "$result" ]; then
            result="${out[$i]}"
        else
            result="$result/${out[$i]}"
        fi
    done
    printf '%s' "$result"
}

# ps_escapes_root <normalized-path>
#   Returns 0 (true) if the ALREADY-NORMALIZED path still climbs above its
#   starting point (leading ".." segment).
ps_escapes_root() {
    case "$1" in
        ..|../*) return 0 ;;
        *) return 1 ;;
    esac
}

# ps_realpath <path>
#   Best-effort resolution of <path> to its real, symlink-free absolute
#   form. Unlike plain `realpath`, this must not require the full path to
#   exist (a Write's target file usually doesn't yet) — only pre-existing
#   symlinked components need resolving. Tries, in order:
#     1. GNU realpath -m (lexical resolution, no existence requirement)
#     2. python3 os.path.realpath (resolves existing symlinks; leaves any
#        non-existent tail as-is — same semantics as GNU realpath -m)
#     3. Manual fallback: walk up to the deepest existing ancestor,
#        `cd` there and read `pwd -P` (resolves symlinks in the existing
#        prefix), then re-append the non-existent tail unresolved.
#   Never fails the caller — on total failure, echoes the input unchanged
#   so callers fail closed on the containment check instead of erroring.
ps_realpath() {
    local p="$1"
    # Named distinctly from ps_lexical_normalize's `out` (an array) to avoid
    # a spurious array-vs-string type warning from static analysis tools.
    local resolved_path
    if command -v realpath >/dev/null 2>&1; then
        if resolved_path="$(realpath -m -- "$p" 2>/dev/null)"; then
            printf '%s' "$resolved_path"
            return 0
        fi
        if resolved_path="$(realpath -- "$p" 2>/dev/null)"; then
            printf '%s' "$resolved_path"
            return 0
        fi
    fi
    if command -v python3 >/dev/null 2>&1; then
        if resolved_path="$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$p" 2>/dev/null)"; then
            printf '%s' "$resolved_path"
            return 0
        fi
    fi
    # Manual fallback: resolve any symlink chain on the FINAL path component
    # first (security review N3 — the loop below only tests `-d`, so a
    # symlink to a plain FILE, or a dangling symlink, was peeled off into
    # `tail` unresolved and never followed; on macOS, where the two
    # `realpath` branches above always fail — BSD `realpath` has no `-m`,
    # and plain `realpath` errors on a non-existent target — this manual
    # path is reached whenever python3 is unavailable, making it load-
    # bearing rather than a rare fallback). Bounded to 40 hops to avoid an
    # infinite loop on a symlink cycle; `readlink` (no `-f`) is available on
    # both BSD and GNU.
    p="$(_ps_resolve_final_symlink "$p")"
    # Manual fallback: resolve the deepest existing ancestor with cd+pwd -P,
    # then tack the unresolved tail back on.
    local dir="$p" tail=""
    while [ -n "$dir" ] && [ ! -d "$dir" ]; do
        tail="$(basename -- "$dir")/${tail}"
        dir="$(dirname -- "$dir")"
    done
    if [ -n "$dir" ] && [ -d "$dir" ]; then
        local resolved
        resolved="$(cd "$dir" 2>/dev/null && pwd -P)"
        if [ -n "$resolved" ]; then
            tail="${tail%/}"
            if [ -n "$tail" ]; then
                printf '%s/%s' "$resolved" "$tail"
            else
                printf '%s' "$resolved"
            fi
            return 0
        fi
    fi
    printf '%s' "$p"
    return 0
}

# _ps_resolve_final_symlink <path>
#   If <path> is itself a symlink (to a file, a directory, or nothing —
#   dangling), follow the chain (bounded, cycle-safe) and return the final
#   target. A relative target is resolved against its symlink's own
#   directory, matching POSIX symlink semantics. If <path> is not a
#   symlink, or once the chain ends, returns the path unchanged.
_ps_resolve_final_symlink() {
    local p="$1" hops=0 t
    while [ -L "$p" ] && [ "$hops" -lt 40 ]; do
        t="$(readlink -- "$p" 2>/dev/null || true)"
        [ -n "$t" ] || break
        case "$t" in
            /*) p="$t" ;;
            *) p="$(dirname -- "$p")/$t" ;;
        esac
        hops=$((hops + 1))
    done
    printf '%s' "$p"
}

# ps_is_within <root> <path>
#   Returns 0 if <path> (both already resolved/absolute) is <root> itself
#   or a descendant of it. Pure string comparison — resolve both sides with
#   ps_realpath first.
ps_is_within() {
    local root="$1" path="$2"
    root="${root%/}"
    [ "$path" = "$root" ] && return 0
    case "$path" in
        "$root"/*) return 0 ;;
        *) return 1 ;;
    esac
}
