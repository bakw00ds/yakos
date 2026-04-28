#!/usr/bin/env bash
# db-migration-validate.sh — per-domain validator for DB migrations.
#
# v0.1 checks naming conventions only (project-portable). Real schema
# validation (parse SQL, run against staging DB) belongs in CI; this
# hook is the cheap gate that catches obvious authoring mistakes early.
#
# Pattern enforced for files under api/migrations/, migrations/, or db/migrations/:
#   <NNN>_<snake_case_description>.{up,down}.sql
#
# Where NNN is at least 3 digits, monotonically increasing within the
# directory.

set -eu

TARGET="${1:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

emit_json() {
    local decision="$1" reason="$2" extra="${3:-}"
    [ -z "$extra" ] && extra='{}'
    jq -nc \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg validator "db-migration" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg target "$TARGET" \
        --argjson extra "$extra" \
        '{ts: $ts, validator: $validator, decision: $decision, reason: $reason, target: $target} + $extra'
}

cd "$PROJECT_DIR" 2>/dev/null || { emit_json "pass" "no project dir"; exit 0; }

mig_dir=""
for d in api/migrations db/migrations migrations; do
    if [ -d "$d" ]; then mig_dir="$d"; break; fi
done
if [ -z "$mig_dir" ]; then
    emit_json "pass" "no migrations directory found"
    exit 0
fi

bad_names=""
while IFS= read -r f; do
    [ -n "$f" ] || continue
    base="$(basename "$f")"
    case "$base" in
        [0-9][0-9][0-9]_*.up.sql|[0-9][0-9][0-9]_*.down.sql) ;;
        [0-9][0-9][0-9][0-9]_*.up.sql|[0-9][0-9][0-9][0-9]_*.down.sql) ;;
        *) bad_names="${bad_names}${base} " ;;
    esac
done < <(find "$mig_dir" -maxdepth 1 -type f -name '*.sql' 2>/dev/null)

if [ -n "$bad_names" ]; then
    extra="$(jq -nc --arg dir "$mig_dir" --arg bad "${bad_names% }" \
        '{migrations_dir: $dir, malformed_names: $bad}')"
    emit_json "block" "migrations have non-conforming filenames (expected NNN_name.{up,down}.sql)" "$extra"
    exit 2
fi

emit_json "pass" "migration filenames conform to NNN_name.{up,down}.sql"
exit 0
