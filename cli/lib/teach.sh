#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: append a "Lessons learned" section to a project agent's body
# so the operator can evolve framework agents without forking the source.
#
# Usage:
#   yakos teach <agent-name> <lesson-file> [--project <path>] [--section <name>]
#
# What this does:
#   - Appends the contents of <lesson-file> under a new bullet in
#     ## Lessons learned in <project>/.claude/agents/<agent>.md.
#   - Creates the section if absent.
#   - Stamps each lesson with an ISO date prefix so newer lessons sort
#     last and the operator can prune old ones manually.
#   - Backs up the agent file before editing.
#
# This is intentionally additive — the framework template stays
# pristine; teaching happens in the project copy. Re-running on the
# same lesson file appends a new bullet (lessons aren't deduped on
# content; the operator decides when to prune).

set -eu

: "${YAKOS_ROOT:?teach.sh: YAKOS_ROOT must be set}"
: "${YAKOS_LIB:?teach.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

usage() {
    cat <<'EOF'
yakos teach <agent-name> <lesson-file> [flags]

Append a lesson to a project agent's body so the operator can evolve
framework agents without forking. Use this when an operator notices
a specialist consistently making a mistake the agent body could
have prevented.

Arguments:
  <agent-name>      The id of the project agent (must exist at
                    <project>/.claude/agents/<name>.md). To teach a
                    framework agent, run 'yakos agent new <name>
                    --extends <framework-name>' first to create the
                    project copy.
  <lesson-file>     Path to a markdown file with the lesson body.
                    Will be appended under '## Lessons learned'.

Flags:
  --project <path>  Project root (defaults to inferred from cwd).
  --section <name>  Section heading (default: 'Lessons learned').
  --dry-run         Print what would be appended; don't write.

Examples:
  yakos teach backend /tmp/dont-skip-audit-log.md
  yakos teach myapp-frontend ./lessons/typed-client-drift.md \
      --section "Project gotchas"
EOF
}

NAME=""
LESSON=""
PROJECT=""
SECTION="Lessons learned"
DRY_RUN=0

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --project) shift; PROJECT="$1" ;;
        --project=*) PROJECT="${1#--project=}" ;;
        --section) shift; SECTION="$1" ;;
        --section=*) SECTION="${1#--section=}" ;;
        --dry-run) DRY_RUN=1 ;;
        --*) ct_die "teach: unknown flag '$1'" ;;
        *)
            if [ -z "$NAME" ]; then NAME="$1"
            elif [ -z "$LESSON" ]; then LESSON="$1"
            else ct_die "teach: too many positional args"
            fi
            ;;
    esac
    shift
done

[ -n "$NAME" ] || { usage >&2; ct_die "teach: <agent-name> required"; }
[ -n "$LESSON" ] || { usage >&2; ct_die "teach: <lesson-file> required"; }
[ -f "$LESSON" ] || ct_die "teach: lesson file not found: $LESSON"

if [ -z "$PROJECT" ]; then
    cwd_real="$(ct_realpath "$PWD")"
    if [ -d "$HOME/agent-control" ]; then
        for cd_path in "$HOME/agent-control"/*/.project-path; do
            [ -f "$cd_path" ] || continue
            p="$(head -1 "$cd_path")"; pr="$(ct_realpath "$p" 2>/dev/null || true)"
            case "$cwd_real" in
                "$pr"|"$pr"/*) PROJECT="$pr"; break ;;
            esac
        done
    fi
fi
[ -n "$PROJECT" ] || ct_die "teach: --project required"

agent_file="$PROJECT/.claude/agents/${NAME}.md"
[ -f "$agent_file" ] || ct_die "teach: project agent not found at $agent_file (run 'yakos agent new $NAME --extends <framework-name>' to bootstrap)"

# Render the lesson body with an ISO date prefix.
ts="$(ct_iso_utc | cut -c1-10)"     # YYYY-MM-DD
lesson_body="$(cat "$LESSON")"
formatted="
- **${ts}** — $(printf '%s' "$lesson_body" | head -1)
$(printf '%s' "$lesson_body" | tail -n +2 | sed 's/^/  /')
"

if [ "$DRY_RUN" = "1" ]; then
    echo "would append to $agent_file under '## $SECTION':"
    printf '%s\n' "$formatted" | sed 's/^/  /'
    exit 0
fi

# Backup.
cp "$agent_file" "$agent_file.yakos-bak-$(ct_iso_utc)"

# If section exists, append under it. If not, append a new section
# at the end of the file.
if grep -q "^## ${SECTION}$" "$agent_file"; then
    awk -v section="## ${SECTION}" -v body="$formatted" '
        BEGIN { in_section = 0; appended = 0 }
        { print }
        $0 == section { in_section = 1; next }
        in_section && /^## / && $0 != section && !appended {
            # We hit the next H2 — backtrack and append before it.
            # (awk has already printed the next-H2 line; we cant
            # easily insert above it, so use the END trick below.)
        }
        END {
            if (!appended) {
                # If section was last, append at EOF; otherwise above.
                # Simpler: re-walk the file with a second pass.
            }
        }
    ' "$agent_file" > /dev/null

    # Two-pass approach: collect lines, find the section, insert body
    # right before the next H2 (or at EOF if section is last).
    tmp_out="$(mktemp -t yakos-teach.XXXXXX)"
    awk -v section="## ${SECTION}" -v body="$formatted" '
        {
            if ($0 == section) {
                in_section = 1
                print
                next
            }
            if (in_section && /^## /) {
                # Next H2 reached; insert body, then continue normally.
                print body
                in_section = 0
            }
            print
        }
        END {
            if (in_section) print body
        }
    ' "$agent_file" > "$tmp_out"
    mv "$tmp_out" "$agent_file"
else
    {
        printf '\n## %s\n' "$SECTION"
        printf '%s\n' "$formatted"
    } >> "$agent_file"
fi

echo "appended lesson to $agent_file under '## $SECTION'"
echo "  backup: $agent_file.yakos-bak-*"
