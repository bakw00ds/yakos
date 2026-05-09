#!/usr/bin/env bash
# Purpose: migrate.sh — upgrade a project's .yakos.yml schema between versions.
#
# Each migration is identity unless the field set actually changed. The
# migration table is the single source of truth — bump it as schema
# fields are added/renamed/removed.
#
# Usage:
#   yakos migrate <project> [--dry-run] [--from <ver>] [--to <ver>]
#
# Backs up the original to <project>/.yakos.yml.yakos-bak-<iso> before
# any edit. Idempotent: re-running on an already-current file is a no-op.

set -eu

: "${YAKOS_LIB:?migrate.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./project-config.sh
. "$YAKOS_LIB/project-config.sh"

usage() {
    cat <<EOF
yakos migrate <project> [flags]

Upgrade a project's .yakos.yml schema to the current version.

Arguments:
  <project>            Project name (looks up .project-path under
                       ~/agent-control/<project>/).

Flags:
  --dry-run            Print the planned migration; don't write.
  --from <ver>         Override detected source version.
  --to <ver>           Override target version (default: current).
  --help, -h           This help.

Schema versions yakOS knows: $YK_PCFG_SCHEMA_KNOWN
Current target version: $YK_PCFG_SCHEMA_CURRENT

The migration table:
  (none) → 0.7   stamp 'yakos: 0.7'; identity otherwise (first .yakos.yml)
  0.7    → 0.8   identity (added optional agent fields, no .yakos.yml change)
  0.8    → 0.9   identity (no .yakos.yml change in v0.9)

Backups: <project>/.yakos.yml.yakos-bak-<iso>.
Examples:
  yakos migrate myapp
  yakos migrate myapp --dry-run
EOF
}

PROJECT=""
DRY_RUN=0
FROM_OVERRIDE=""
TO_OVERRIDE=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --dry-run) DRY_RUN=1 ;;
        --from) shift; FROM_OVERRIDE="$1" ;;
        --from=*) FROM_OVERRIDE="${1#--from=}" ;;
        --to) shift; TO_OVERRIDE="$1" ;;
        --to=*) TO_OVERRIDE="${1#--to=}" ;;
        --*) ct_die "migrate: unknown flag '$1'" ;;
        *)
            if [ -z "$PROJECT" ]; then PROJECT="$1"
            else ct_die "migrate: too many positional args"
            fi
            ;;
    esac
    shift
done

[ -n "$PROJECT" ] || { usage >&2; ct_die "migrate: <project> required"; }

CONTROL_DIR="$HOME/agent-control/$PROJECT"
[ -d "$CONTROL_DIR" ] || ct_die "migrate: project '$PROJECT' not bootstrapped"
PROJECT_PATH_FILE="$CONTROL_DIR/.project-path"
[ -f "$PROJECT_PATH_FILE" ] || ct_die "migrate: $PROJECT_PATH_FILE missing"
PROJECT_REPO="$(head -1 "$PROJECT_PATH_FILE")"
[ -d "$PROJECT_REPO" ] || ct_die "migrate: project repo not at $PROJECT_REPO"

CFG="$PROJECT_REPO/.yakos.yml"

# Detect source version.
if [ -n "$FROM_OVERRIDE" ]; then
    FROM="$FROM_OVERRIDE"
elif [ -f "$CFG" ]; then
    FROM="$(yk_pcfg_get "$PROJECT_REPO" "yakos")"
    [ -n "$FROM" ] || FROM="0.7"
else
    FROM="(none)"
fi

TO="${TO_OVERRIDE:-$YK_PCFG_SCHEMA_CURRENT}"

echo "yakos migrate — $PROJECT"
echo "  config:   $CFG$( [ -f "$CFG" ] || printf ' (does not exist)' )"
echo "  from:     $FROM"
echo "  to:       $TO"

if [ "$FROM" = "$TO" ]; then
    echo "already at $TO; nothing to do."
    exit 0
fi

# ---- migration steps ------------------------------------------------------

# Each step is a function that takes <cfg-path> and produces the new
# version's content on stdout. Functions named: migrate_<from>_to_<to>.
# All migrations are pure-text edits to .yakos.yml.

migrate_none_to_07() {
    # First time .yakos.yml is created. Seed with a stamp + sane defaults.
    cat <<EOF
# yakOS project config — see UPGRADING.md and docs/runtime-matrix.md
yakos: 0.7

# default-runtime: claude            # claude | codex | gemini
# default-fallback: [claude]
# per-domain:
#   code-review: codex
EOF
}

migrate_07_to_08() {
    # 0.7 → 0.8 added optional agent frontmatter (max-cost-per-task,
    # max-duration-s) but did NOT change .yakos.yml. Just bump the stamp.
    awk '
        BEGIN { stamped = 0 }
        /^yakos[[:space:]]*:/ {
            print "yakos: 0.8"; stamped = 1; next
        }
        { print }
        END {
            if (!stamped) print "yakos: 0.8"
        }
    ' "$1"
}

migrate_08_to_09() {
    # 0.8 → 0.9 made no .yakos.yml schema changes. Just stamp.
    awk '
        BEGIN { stamped = 0 }
        /^yakos[[:space:]]*:/ {
            print "yakos: 0.9"; stamped = 1; next
        }
        { print }
        END {
            if (!stamped) print "yakos: 0.9"
        }
    ' "$1"
}

# yk_migrate_step <from> <to> [<cfg-path>]
#   Run the matching migrate_X_to_Y on cfg, write result back to cfg.
#   Honors DRY_RUN.
yk_migrate_step() {
    local from="$1"; local to="$2"; local cfg="$3"

    local fn
    case "${from}::${to}" in
        "(none)::0.7") fn="migrate_none_to_07" ;;
        "0.7::0.8") fn="migrate_07_to_08" ;;
        "0.8::0.9") fn="migrate_08_to_09" ;;
        *) ct_die "migrate: no migration registered for $from -> $to" ;;
    esac

    local out
    if [ "$from" = "(none)" ]; then
        out="$($fn)"
    else
        [ -f "$cfg" ] || ct_die "migrate: cfg missing for $from → $to"
        out="$($fn "$cfg")"
    fi

    if [ "$DRY_RUN" = "1" ]; then
        echo "  --- $from → $to (dry-run, would write to $cfg) ---"
        printf '%s\n' "$out" | sed 's/^/    /' | head -20
        return 0
    fi

    if [ -f "$cfg" ]; then
        cp "$cfg" "$cfg.yakos-bak-$(ct_iso_utc)"
    fi
    printf '%s\n' "$out" > "$cfg"
    echo "  applied: $from → $to (backup: $cfg.yakos-bak-* if file existed)"
}

# ---- walk the migration ladder -------------------------------------------

# Compute the steps from FROM → TO. Order matters; each step assumes
# the previous one completed.
LADDER="(none) 0.7 0.8 0.9"

# Find FROM's index in the ladder; TO's index. Apply each pair in order.
from_idx=-1
to_idx=-1
i=0
for v in $LADDER; do
    if [ "$v" = "$FROM" ]; then from_idx=$i; fi
    if [ "$v" = "$TO" ]; then to_idx=$i; fi
    i=$((i + 1))
done
[ "$from_idx" -ge 0 ] || ct_die "migrate: FROM '$FROM' not in ladder"
[ "$to_idx" -ge 0 ] || ct_die "migrate: TO '$TO' not in ladder"
[ "$from_idx" -lt "$to_idx" ] || ct_die "migrate: cannot migrate downward ($FROM → $TO)"

# Walk pairwise.
prev=""
i=0
for v in $LADDER; do
    if [ -n "$prev" ] && [ "$i" -gt "$from_idx" ] && [ "$i" -le "$to_idx" ]; then
        yk_migrate_step "$prev" "$v" "$CFG"
    fi
    prev="$v"
    i=$((i + 1))
done

echo
if [ "$DRY_RUN" = "1" ]; then
    echo "(dry-run — no files written)"
else
    echo "$PROJECT migrated to schema $TO."
fi
