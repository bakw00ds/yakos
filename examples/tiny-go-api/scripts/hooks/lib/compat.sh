#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# compat.sh — cross-platform helpers used by every yakos CLI subcommand.
#
# This file is meant to be SOURCED, not executed:
#     source "$YAKOS_LIB/compat.sh"
#
# It targets bash 3.2 (the macOS system shell) — no associative arrays,
# no `[[ -v ]]`, no `mapfile`. Linux bash 4+ also works.

# Idempotent source guard.
if [ "${YAKOS_COMPAT_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
YAKOS_COMPAT_LOADED=1

# ---- logging --------------------------------------------------------------

ct_log() {
    # Writes a UTC-timestamped line to stderr.
    printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2
}

ct_die() {
    # Logs a fatal message and exits 1.
    ct_log "FATAL: $*"
    exit 1
}

# ---- path helpers ---------------------------------------------------------

ct_realpath() {
    # Canonicalize a path. Falls through GNU realpath → coreutils greadlink
    # → pure-bash. The pure-bash form does not resolve symlinks but does
    # produce an absolute, normalized path — good enough for symlink targets
    # at install time.
    local p="$1"
    if command -v realpath >/dev/null 2>&1; then
        realpath "$p" 2>/dev/null && return 0
    fi
    if command -v greadlink >/dev/null 2>&1; then
        greadlink -f "$p" 2>/dev/null && return 0
    fi
    # Pure-bash fallback.
    if [ -d "$p" ]; then
        ( cd "$p" 2>/dev/null && pwd -P ) && return 0
    fi
    if [ -e "$p" ] || [ -L "$p" ]; then
        local d="$(dirname -- "$p")"
        local b="$(basename -- "$p")"
        ( cd "$d" 2>/dev/null && printf '%s/%s\n' "$(pwd -P)" "$b" ) && return 0
    fi
    # Path doesn't exist; print the absolute form anyway.
    case "$p" in
        /*) printf '%s\n' "$p" ;;
        *) printf '%s/%s\n' "$(pwd -P)" "$p" ;;
    esac
}

# ---- timeout --------------------------------------------------------------

ct_timeout() {
    # Run a command with a timeout. macOS doesn't ship `timeout`; coreutils
    # provides `gtimeout`. If neither exists, run the command unguarded and
    # log a one-time warning.
    if command -v timeout >/dev/null 2>&1; then
        timeout "$@"
        return $?
    fi
    if command -v gtimeout >/dev/null 2>&1; then
        gtimeout "$@"
        return $?
    fi
    if [ "${YAKOS_TIMEOUT_WARNED:-0}" != "1" ]; then
        ct_log "WARNING: neither 'timeout' nor 'gtimeout' is installed. Commands will run without a timeout. Install coreutils: brew install coreutils"
        YAKOS_TIMEOUT_WARNED=1
    fi
    # First arg is the timeout; drop it and run the rest.
    shift
    "$@"
}

# ---- sed in-place ---------------------------------------------------------

ct_sed_inplace() {
    # GNU sed accepts `sed -i 'expr' file`; BSD sed (macOS) requires
    # `sed -i '' 'expr' file`. ct_sed_inplace abstracts that.
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

# ---- json helpers ---------------------------------------------------------

ct_json_get() {
    # Read a value from a JSON file using a jq path expression.
    #     ct_json_get path/to/file.json '.env.FOO'
    # Prints the result to stdout; returns 0 on success, non-zero on jq error.
    local file="$1" expr="$2"
    if [ ! -f "$file" ]; then
        ct_die "ct_json_get: file not found: $file"
    fi
    if ! command -v jq >/dev/null 2>&1; then
        ct_die "ct_json_get: jq is required (brew install jq)"
    fi
    jq -r "$expr" "$file"
}

ct_json_merge() {
    # Merge a JSON snippet into a JSON file by jq expression.
    #     ct_json_merge path/to/file.json '.env = (.env // {}) + {"FOO":"1"}'
    # Writes through a tmp file (atomic-ish replace).
    local file="$1" expr="$2"
    if [ ! -f "$file" ]; then
        ct_die "ct_json_merge: file not found: $file"
    fi
    if ! command -v jq >/dev/null 2>&1; then
        ct_die "ct_json_merge: jq is required (brew install jq)"
    fi
    local tmp="${file}.yakos-tmp-$$"
    if ! jq "$expr" "$file" > "$tmp"; then
        rm -f "$tmp"
        ct_die "ct_json_merge: jq failed on $file with expression: $expr"
    fi
    mv "$tmp" "$file"
}

ct_json_valid() {
    # Returns 0 if the file is valid JSON, non-zero otherwise.
    local file="$1"
    [ -f "$file" ] || return 1
    jq empty "$file" >/dev/null 2>&1
}

# ---- iso timestamp --------------------------------------------------------

ct_iso_utc() {
    # Compact UTC timestamp suitable for filenames: 20260428T091500Z
    date -u +%Y%m%dT%H%M%SZ
}

ct_iso_now_z() {
    # Full ISO-8601 with Z suffix: 2026-04-28T09:15:00Z
    date -u +%Y-%m-%dT%H:%M:%SZ
}

ct_iso_to_epoch() {
    # Parse an ISO-8601 timestamp into seconds-since-epoch.
    # Accepts forms produced by ct_iso_now_z (UTC, "Z" suffix). Falls
    # through GNU `date -d` and BSD `date -j -f`.
    local iso="$1"
    [ -n "$iso" ] || { printf '0'; return; }
    if date -u -d "$iso" +%s >/dev/null 2>&1; then
        date -u -d "$iso" +%s
    elif date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$iso" +%s >/dev/null 2>&1; then
        date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$iso" +%s
    else
        printf '0'
    fi
}

ct_dir_size_bytes() {
    # Portable directory-size in bytes. GNU du has -b (apparent size in
    # bytes); BSD/macOS du does not, so we fall back to -k (kilobytes)
    # and multiply.
    local dir="$1"
    [ -d "$dir" ] || { printf '0'; return; }
    local n
    n="$(du -sb "$dir" 2>/dev/null | awk 'NR==1 {print $1}')"
    if [ -n "$n" ] && [ "$n" -eq "$n" ] 2>/dev/null; then
        printf '%s' "$n"
        return
    fi
    n="$(du -sk "$dir" 2>/dev/null | awk 'NR==1 {print $1}')"
    if [ -n "$n" ] && [ "$n" -eq "$n" ] 2>/dev/null; then
        printf '%d' "$((n * 1024))"
    else
        printf '0'
    fi
}

# ---- hashing --------------------------------------------------------------

ct_sha256() {
    # Print the hex SHA-256 of a file. shasum (-a 256) is present on macOS
    # and most Linux distros via perl-shasum or openssl wrappers.
    local f="$1"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$f" | awk '{print $NF}'
    else
        ct_die "ct_sha256: no shasum/sha256sum/openssl found"
    fi
}

# ---- project path encoding -----------------------------------------------

ct_encode_project_path() {
    # Claude Code encodes project paths into ~/.claude/projects/<encoded>/ by
    # replacing '/' and '.' with '-'. This helper does the same.
    #
    # Example: /Users/tw/code/panda-os-3.0 → -Users-tw-code-panda-os-3-0
    #
    # The leading slash becomes a leading hyphen.
    local p="$1"
    # First normalize to absolute (resolves './' and trailing slash)
    case "$p" in
        /*) : ;;
        *)  p="$(cd "$p" 2>/dev/null && pwd -P)" ;;
    esac
    # Strip trailing slash
    p="${p%/}"
    # Replace '/' and '.' with '-'
    p="${p//\//-}"
    p="${p//./-}"
    printf '%s\n' "$p"
}
