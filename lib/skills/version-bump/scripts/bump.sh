#!/usr/bin/env bash
# Purpose: Bump four-part VERSION, prepend a CHANGELOG entry, and (optionally)
#          commit the change with a standard "chore(version): bump to X.Y.Z.W"
#          message.
# Inputs:  CLI args:
#            --component {major|minor|patch|hotfix}   (required)
#            --message <text>                         (optional bullet/body)
#            --no-commit                              (skip git add/commit)
#          Env vars:
#            YAKOS_VERSION_PATH    default: ./VERSION
#            YAKOS_CHANGELOG_PATH  default: ./CHANGELOG.md
# Outputs: stdout: the new version string (e.g. "1.2.3.0"). This IS the
#          machine API; callers parse it. All human/diagnostic output goes
#          to stderr via ct_log.
# Reads:   $YAKOS_VERSION_PATH, $YAKOS_CHANGELOG_PATH, `git log` and
#          `git describe --tags` (best-effort) when auto-generating message.
# Writes:  $YAKOS_VERSION_PATH (overwrite with new version + newline),
#          $YAKOS_CHANGELOG_PATH (prepend new entry under [Unreleased]),
#          and a git commit on the current branch unless --no-commit.
# Exit codes:
#   0   success
#   2   user/input error (bad/missing args, malformed VERSION)
#   3   git/repo error (not a git repo, commit failed)
#   4   file error (VERSION/CHANGELOG missing or unwritable)

set -euo pipefail

# ---- locate compat.sh ------------------------------------------------------
#
# When invoked from a YakOS context $YAKOS_LIB is set; otherwise resolve
# relative to this script's location: lib/skills/version-bump/scripts/bump.sh
# is three dirs above cli/lib/compat.sh's parent. To avoid hard-coding that
# layout, walk up until we find cli/lib/compat.sh.

SCRIPT_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"

_find_compat() {
    if [ -n "${YAKOS_LIB:-}" ] && [ -f "$YAKOS_LIB/compat.sh" ]; then
        printf '%s\n' "$YAKOS_LIB/compat.sh"
        return 0
    fi
    local d="$SCRIPT_DIR"
    local i=0
    while [ "$d" != "/" ] && [ "$i" -lt 8 ]; do
        if [ -f "$d/cli/lib/compat.sh" ]; then
            printf '%s\n' "$d/cli/lib/compat.sh"
            return 0
        fi
        d="$(dirname -- "$d")"
        i=$((i + 1))
    done
    return 1
}

COMPAT_PATH="$(_find_compat || true)"
if [ -z "$COMPAT_PATH" ]; then
    printf 'bump.sh: cannot locate compat.sh (set YAKOS_LIB or run from a YakOS checkout)\n' >&2
    exit 4
fi
# shellcheck source=/dev/null
. "$COMPAT_PATH"

# ---- arg parsing -----------------------------------------------------------

COMPONENT=""
MESSAGE=""
HAS_MESSAGE=0
NO_COMMIT=0

usage() {
    cat >&2 <<'EOF'
bump.sh — bump VERSION + prepend CHANGELOG entry

Usage:
    bump.sh --component {major|minor|patch|hotfix} [--message <text>] [--no-commit]

Env:
    YAKOS_VERSION_PATH    path to VERSION   (default: ./VERSION)
    YAKOS_CHANGELOG_PATH  path to CHANGELOG (default: ./CHANGELOG.md)
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --component)
            if [ "$#" -lt 2 ]; then
                ct_log "ERROR: --component requires a value"
                usage
                exit 2
            fi
            COMPONENT="$2"
            shift 2
            ;;
        --component=*)
            COMPONENT="${1#--component=}"
            shift
            ;;
        --message)
            if [ "$#" -lt 2 ]; then
                ct_log "ERROR: --message requires a value"
                usage
                exit 2
            fi
            MESSAGE="$2"
            HAS_MESSAGE=1
            shift 2
            ;;
        --message=*)
            MESSAGE="${1#--message=}"
            HAS_MESSAGE=1
            shift
            ;;
        --no-commit)
            NO_COMMIT=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            ct_log "ERROR: unknown argument: $1"
            usage
            exit 2
            ;;
    esac
done

case "$COMPONENT" in
    major|minor|patch|hotfix) ;;
    "")
        ct_log "ERROR: --component is required"
        usage
        exit 2
        ;;
    *)
        ct_log "ERROR: --component must be one of {major,minor,patch,hotfix}; got '$COMPONENT'"
        exit 2
        ;;
esac

# ---- resolve paths ---------------------------------------------------------

VERSION_PATH="${YAKOS_VERSION_PATH:-./VERSION}"
CHANGELOG_PATH="${YAKOS_CHANGELOG_PATH:-./CHANGELOG.md}"

if [ ! -f "$VERSION_PATH" ]; then
    ct_log "ERROR: VERSION file not found at: $VERSION_PATH"
    exit 4
fi
if [ ! -f "$CHANGELOG_PATH" ]; then
    ct_log "ERROR: CHANGELOG file not found at: $CHANGELOG_PATH"
    exit 4
fi
if [ ! -w "$VERSION_PATH" ]; then
    ct_log "ERROR: VERSION file not writable: $VERSION_PATH"
    exit 4
fi
if [ ! -w "$CHANGELOG_PATH" ]; then
    ct_log "ERROR: CHANGELOG file not writable: $CHANGELOG_PATH"
    exit 4
fi

# ---- read + parse VERSION --------------------------------------------------

# Strip trailing whitespace/newlines from the raw file so 1.2.3.4\n parses.
CURRENT_VERSION="$(tr -d '[:space:]' < "$VERSION_PATH")"

if [ -z "$CURRENT_VERSION" ]; then
    ct_log "ERROR: VERSION file is empty: $VERSION_PATH"
    exit 2
fi

# Bash 3.2 compatible regex check via [[ =~ ]] is safe (3.2+). The four
# captured groups land in ${BASH_REMATCH[1..4]}.
if [[ ! "$CURRENT_VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
    ct_log "ERROR: VERSION malformed (expected major.minor.patch.hotfix): '$CURRENT_VERSION'"
    exit 2
fi

CUR_MAJOR="${BASH_REMATCH[1]}"
CUR_MINOR="${BASH_REMATCH[2]}"
CUR_PATCH="${BASH_REMATCH[3]}"
CUR_HOTFIX="${BASH_REMATCH[4]}"

# ---- compute new version ---------------------------------------------------

NEW_MAJOR="$CUR_MAJOR"
NEW_MINOR="$CUR_MINOR"
NEW_PATCH="$CUR_PATCH"
NEW_HOTFIX="$CUR_HOTFIX"

case "$COMPONENT" in
    major)
        NEW_MAJOR=$((CUR_MAJOR + 1))
        NEW_MINOR=0
        NEW_PATCH=0
        NEW_HOTFIX=0
        ;;
    minor)
        NEW_MINOR=$((CUR_MINOR + 1))
        NEW_PATCH=0
        NEW_HOTFIX=0
        ;;
    patch)
        NEW_PATCH=$((CUR_PATCH + 1))
        NEW_HOTFIX=0
        ;;
    hotfix)
        NEW_HOTFIX=$((CUR_HOTFIX + 1))
        ;;
esac

NEW_VERSION="${NEW_MAJOR}.${NEW_MINOR}.${NEW_PATCH}.${NEW_HOTFIX}"

ct_log "version-bump: $CURRENT_VERSION -> $NEW_VERSION ($COMPONENT)"

# ---- determine changelog bullet --------------------------------------------

# CHANGELOG_BULLETS is a single string with one bullet per line; the
# CHANGELOG insertion code joins them under "### Changed". We avoid
# associative arrays / mapfile to stay 3.2-compatible.
CHANGELOG_BULLETS=""

if [ "$HAS_MESSAGE" -eq 1 ]; then
    CHANGELOG_BULLETS="- ${MESSAGE}"
else
    # Best-effort auto-generate from `git log <last_tag>..HEAD --oneline`.
    # If anything fails, fall back to "Initial release".
    last_tag=""
    if command -v git >/dev/null 2>&1; then
        last_tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
    fi
    log_lines=""
    if [ -n "$last_tag" ]; then
        log_lines="$(git log "$last_tag..HEAD" --oneline 2>/dev/null | head -n 5 || true)"
    fi
    if [ -z "$log_lines" ]; then
        CHANGELOG_BULLETS="- Initial release"
    else
        # Prefix each non-empty line with "- ". Strip the leading short SHA
        # so the bullet reads as a sentence ("feat(api): add foo" rather
        # than "abc1234 feat(api): add foo").
        CHANGELOG_BULLETS="$(printf '%s\n' "$log_lines" \
            | awk 'NF { sub(/^[a-f0-9]+ +/, ""); print "- " $0 }')"
    fi
fi

# ---- prepend CHANGELOG entry ----------------------------------------------
#
# Approach: write the new entry block to a tmp file, then stream the
# existing CHANGELOG through awk. awk reads the entry tmp file via
# `getline ... < entry_file` when it hits the insertion point. This
# avoids awk's `-v` limitation (no embedded newlines) and avoids the
# BSD/GNU sed multi-line-insert minefield.
#
# Insertion rule:
#   - if a line beginning "## [Unreleased]" exists: insert entry directly
#     under it (with a blank-line separator either side)
#   - otherwise: prepend the entry at the very top of the file.

DATE_TODAY="$(ct_iso_now_z | cut -c1-10)"

ENTRY_HEADER="## [${NEW_VERSION}] — ${DATE_TODAY}"
NEW_ENTRY_FILE="${CHANGELOG_PATH}.yakos-entry-$$"
TMP_CHANGELOG="${CHANGELOG_PATH}.yakos-tmp-$$"
trap 'rm -f "$NEW_ENTRY_FILE" "$TMP_CHANGELOG"' EXIT

{
    printf '%s\n' "$ENTRY_HEADER"
    printf '\n'
    printf '### Changed\n'
    printf '\n'
    printf '%s\n' "$CHANGELOG_BULLETS"
    printf '\n'
} > "$NEW_ENTRY_FILE"

# Three paths:
#   (1) No [Unreleased] heading at all: prepend new entry at top.
#   (2) [Unreleased] heading exists AND has substantive content (any
#       non-blank line between it and the next `## [` heading): PROMOTE
#       — rename [Unreleased] to [VERSION] — DATE, add a fresh empty
#       [Unreleased] above. The promoted content becomes the new
#       version's body; --message and auto-generated bullets are
#       ignored (they'd be redundant — the body already describes the
#       work).
#   (3) [Unreleased] heading exists but is empty: use the original
#       insert-under-[Unreleased] path so --message / auto-generated
#       bullets land in the new version's body.
if grep -q '^## \[Unreleased\]' "$CHANGELOG_PATH"; then
    UNRELEASED_HAS_CONTENT="$(awk '
        BEGIN { in_unreleased = 0; has_content = 0 }
        /^## \[Unreleased\]/ { in_unreleased = 1; next }
        in_unreleased && /^## \[/ { in_unreleased = 0 }
        in_unreleased && /[^[:space:]]/ { has_content = 1 }
        END { print (has_content ? 1 : 0) }
    ' "$CHANGELOG_PATH")"

    if [ "$UNRELEASED_HAS_CONTENT" = "1" ]; then
        # Promote: rename [Unreleased] to [VERSION] — DATE, add fresh
        # empty [Unreleased] above. Skip the auto-generated entry file
        # (the promoted content IS the entry).
        ct_log "[Unreleased] section has content; promoting to [$NEW_VERSION] — $DATE_TODAY (--message ignored)"
        awk -v new_header="$ENTRY_HEADER" '
            /^## \[Unreleased\]/ && !replaced {
                print "## [Unreleased]"
                print ""
                print new_header
                replaced = 1
                next
            }
            { print }
        ' "$CHANGELOG_PATH" > "$TMP_CHANGELOG"
    else
        # Empty [Unreleased]: insert auto-generated entry under it.
        awk -v entry_file="$NEW_ENTRY_FILE" '
            BEGIN { inserted = 0 }
            /^## \[Unreleased\]/ && inserted == 0 {
                print
                # Consume one trailing blank line if present.
                if ((getline nl) > 0) {
                    if (nl == "") {
                        print ""
                    } else {
                        pending = nl
                    }
                }
                while ((getline line < entry_file) > 0) {
                    print line
                }
                close(entry_file)
                if (pending != "") {
                    print pending
                    pending = ""
                }
                inserted = 1
                next
            }
            { print }
        ' "$CHANGELOG_PATH" > "$TMP_CHANGELOG"
    fi
else
    # No [Unreleased] heading — prepend at top.
    {
        cat "$NEW_ENTRY_FILE"
        cat "$CHANGELOG_PATH"
    } > "$TMP_CHANGELOG"
fi

# Atomic-ish replace.
if ! mv "$TMP_CHANGELOG" "$CHANGELOG_PATH"; then
    ct_log "ERROR: failed to write CHANGELOG: $CHANGELOG_PATH"
    exit 4
fi
rm -f "$NEW_ENTRY_FILE"
trap - EXIT

# ---- write VERSION ---------------------------------------------------------

TMP_VERSION="${VERSION_PATH}.yakos-tmp-$$"
trap 'rm -f "$TMP_VERSION"' EXIT
printf '%s\n' "$NEW_VERSION" > "$TMP_VERSION"
if ! mv "$TMP_VERSION" "$VERSION_PATH"; then
    ct_log "ERROR: failed to write VERSION: $VERSION_PATH"
    exit 4
fi
trap - EXIT

# ---- commit (unless --no-commit) ------------------------------------------

if [ "$NO_COMMIT" -eq 0 ]; then
    if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        ct_log "ERROR: not inside a git repo; cannot commit (re-run with --no-commit to skip)"
        exit 3
    fi

    # Git writes status to stdout by default; redirect to stderr so the
    # script's stdout remains the machine API (just the new version).
    if ! git add -- "$VERSION_PATH" "$CHANGELOG_PATH" >&2; then
        ct_log "ERROR: 'git add' failed for VERSION/CHANGELOG"
        exit 3
    fi

    commit_subject="chore(version): bump to $NEW_VERSION"
    if [ "$HAS_MESSAGE" -eq 1 ]; then
        if ! git commit -m "$commit_subject" -m "$MESSAGE" >&2; then
            ct_log "ERROR: 'git commit' failed"
            exit 3
        fi
    else
        if ! git commit -m "$commit_subject" >&2; then
            ct_log "ERROR: 'git commit' failed"
            exit 3
        fi
    fi
    ct_log "version-bump: committed '$commit_subject'"
fi

# ---- output (stdout = machine API) ----------------------------------------

printf '%s\n' "$NEW_VERSION"
exit 0
