#!/usr/bin/env bash
# archive.sh — roll work/current/ → work/archive/<tag>/ for a project.
#
# Behavior (build prompt §"yakos archive — required behavior"):
#   1. Validate <project> exists in ~/agent-control/
#   2. Validate <tag> is alphanumeric + dots/hyphens (no spaces or slashes)
#   3. Read work/current/hook-bypass.md; abort if any active entries have
#      expired (Expires: field in the past)
#   4. mv work/current/ → work/archive/<tag>/  (atomic where possible)
#   5. Recreate work/current/{logs,artifacts,reports}/ empty
#   6. Append archive entry to work/sessions.ndjson
#   7. Print summary

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos archive'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos archive'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

PROJECT=""
TAG=""
AUTO_TAG=0
YES=0

usage() {
    cat <<EOF
yakos archive <project> <tag> [--auto-tag] [--yes]

Roll work/current/ into work/archive/<tag>/ for a project under
~/agent-control/. Refuses to archive while expired hook-bypass entries
remain.

Arguments:
  <project>      Project name (subdir of ~/agent-control/)
  <tag>          Archive tag (alphanumeric + dots/hyphens)

Options:
  --auto-tag     Treat <tag> as a hint; on conflict, append a suffix.
                 Used internally by 'yakos team restart'.
  --yes          Skip confirmation prompts.
  --help, -h     Print this help.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --auto-tag) AUTO_TAG=1 ;;
        --yes|-y) YES=1 ;;
        --*) ct_die "archive: unknown flag '$1'" ;;
        *)
            if [ -z "$PROJECT" ]; then PROJECT="$1"
            elif [ -z "$TAG" ]; then TAG="$1"
            else ct_die "archive: too many positional args"
            fi
            ;;
    esac
    shift
done

[ -n "$PROJECT" ] || { usage >&2; ct_die "archive: missing <project>"; }
[ -n "$TAG" ] || { usage >&2; ct_die "archive: missing <tag>"; }

# Validate tag — alphanumeric, dots, hyphens, underscores. No slashes/spaces.
case "$TAG" in
    *[!A-Za-z0-9._-]*)
        ct_die "archive: <tag> may only contain alphanumeric, dot, hyphen, underscore: got '$TAG'"
        ;;
esac

CONTROL_DIR="$HOME/agent-control/$PROJECT"
[ -d "$CONTROL_DIR" ] || ct_die "archive: project '$PROJECT' not found at $CONTROL_DIR (run 'yakos init' first)"

# Resolve through the canonical resolver so we agree with init/status/hooks.
# shellcheck source=./paths.sh
. "$YAKOS_LIB/paths.sh"
export YAKOS_PROJECT_NAME="$PROJECT"
unset YAKOS_WORK_DIR YAKOS_INPLACE_WORK 2>/dev/null || true

WORK_DIR="$(yakos_work_dir)"
CURRENT_DIR="$(yakos_current_dir)"
ARCHIVE_DIR="$WORK_DIR/archive"
DEST_DIR="$ARCHIVE_DIR/$TAG"

[ -d "$CURRENT_DIR" ] || ct_die "archive: $CURRENT_DIR does not exist (nothing to archive)"

# --auto-tag: append a numeric suffix on conflict
if [ -e "$DEST_DIR" ] && [ "$AUTO_TAG" = "1" ]; then
    suffix=2
    while [ -e "${DEST_DIR}-${suffix}" ]; do
        suffix=$((suffix + 1))
    done
    DEST_DIR="${DEST_DIR}-${suffix}"
    TAG="${TAG}-${suffix}"
fi
[ ! -e "$DEST_DIR" ] || ct_die "archive: $DEST_DIR already exists; pick a different tag or use --auto-tag"

# ---- expired-bypass check ---------------------------------------------------

BYPASS_FILE="$(yakos_bypass_file)"
expired_entries=""
if [ -f "$BYPASS_FILE" ]; then
    # Only count `## bypass:` headings that appear AFTER `## Active entries`.
    # This skips the format example in the template's documentation block.
    bypass_pairs="$(awk '
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active = 1; next }
        active && /^##[[:space:]]+bypass:/ {
            id = $0
            sub(/^##[[:space:]]+bypass:/, "", id)
            sub(/^[[:space:]]+/, "", id)
            next
        }
        active && /^\*\*Expires:\*\*/ {
            line = $0
            sub(/^\*\*Expires:\*\*[[:space:]]*/, "", line)
            split(line, parts, /[[:space:]]/)
            print id "\t" parts[1]
        }
    ' "$BYPASS_FILE" || true)"

    if [ -n "$bypass_pairs" ]; then
        while IFS=$'\t' read -r id exp; do
            [ -n "$exp" ] || continue
            # jq returns "true" if expiry < now
            is_expired="$(printf '%s' "$exp" | jq -Rr 'try (fromdateiso8601 < now | tostring) catch "parse_error"')"
            case "$is_expired" in
                true)
                    expired_entries="${expired_entries}  - bypass:${id} (expired ${exp})\n"
                    ;;
                false)
                    : # active, fine
                    ;;
                *)
                    expired_entries="${expired_entries}  - bypass:${id} (unparseable Expires: ${exp})\n"
                    ;;
            esac
        done <<< "$bypass_pairs"
    fi
fi

if [ -n "$expired_entries" ]; then
    echo "Expired bypass entries must be removed before archiving:" >&2
    # shellcheck disable=SC2059
    printf "$expired_entries" >&2
    echo "" >&2
    echo "Edit $BYPASS_FILE and re-run." >&2
    exit 1
fi

# ---- size summary (before move, for the report) -----------------------------

current_size=""
if command -v du >/dev/null 2>&1; then
    current_size="$(du -sh "$CURRENT_DIR" 2>/dev/null | awk '{print $1}')"
fi

# ---- move + recreate --------------------------------------------------------

mkdir -p "$ARCHIVE_DIR"
mv "$CURRENT_DIR" "$DEST_DIR"
mkdir -p "$CURRENT_DIR/logs" "$CURRENT_DIR/artifacts" "$CURRENT_DIR/reports"

# Restore the bypass file template + empty NDJSON session history in fresh current/
cp "$YAKOS_ROOT/lib/settings/hook-bypass.template.md" "$(yakos_bypass_file)"
: > "$(yakos_session_history_file)"

# ---- append session ledger entry --------------------------------------------

SESSIONS_LOG="$(yakos_sessions_log_file)"
ts="$(ct_iso_now_z)"
jq -nc \
    --arg ts "$ts" \
    --arg project "$PROJECT" \
    --arg tag "$TAG" \
    --arg dest "$DEST_DIR" \
    --arg size "${current_size:-unknown}" \
    '{ts: $ts, action: "archive", project: $project, tag: $tag, dest: $dest, size: $size}' \
    >> "$SESSIONS_LOG"

cat <<EOF

Archived '$PROJECT' work/current/ → work/archive/$TAG/
  size:    ${current_size:-unknown}
  source:  $CURRENT_DIR (now empty)
  dest:    $DEST_DIR
  ledger:  $SESSIONS_LOG
EOF
