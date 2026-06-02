#!/usr/bin/env bash
# regen-in-sync-fixture.sh
#
# Regenerates tests/fixtures/refresh/proj-in-sync/ so that every hook script
# matches the current lib/hooks/ sources and every .framework-hash sidecar
# records the correct SHA-256 of that source.
#
# Run this whenever lib/hooks/*.sh changes (e.g. after a framework PR lands)
# and test 5 in run-refresh-test.sh starts failing with "drift detected".
#
# Usage:
#   bash tests/fixtures/refresh/regen-in-sync-fixture.sh
#
# The script is intentionally idempotent: running it twice produces the same
# result.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/../../.." && pwd -P)"
HOOKS_SRC="$REPO_ROOT/lib/hooks"
FIXTURE_HOOKS="$REPO_ROOT/tests/fixtures/refresh/proj-in-sync/scripts/hooks"

echo "regen-in-sync-fixture: repo root = $REPO_ROOT"

updated=0
skipped=0

# Walk every .sh file currently in the fixture's hooks directory.
# For each one, if a matching source exists in lib/hooks/, sync content + hash.
while IFS= read -r fixture_file; do
    rel="${fixture_file#"$FIXTURE_HOOKS/"}"   # e.g. context-threshold.sh or lib/compat.sh
    src="$HOOKS_SRC/$rel"

    if [ ! -f "$src" ]; then
        echo "  skip (no framework source): $rel"
        skipped=$((skipped + 1))
        continue
    fi

    # Sync the script content
    cp "$src" "$fixture_file"

    # Rewrite the .framework-hash sidecar
    hash_file="${fixture_file}.framework-hash"
    shasum -a 256 "$src" | awk '{print $1}' > "$hash_file"

    echo "  updated: $rel"
    updated=$((updated + 1))
done < <(find "$FIXTURE_HOOKS" -name '*.sh' | sort)

echo ""
echo "regen-in-sync-fixture: $updated updated, $skipped skipped"
