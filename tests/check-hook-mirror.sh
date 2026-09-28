#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# check-hook-mirror.sh — CI drift gate (K-91c).
#
# Fails if any tracked scripts/hooks/** mirror has drifted from lib/hooks/,
# the source of truth. Three mirrors are checked:
#   1. The framework's own scripts/hooks/ (repo root)
#   2. tests/fixtures/refresh/proj-in-sync/scripts/hooks/
#   3. cli-go/cmd/yakos/testdata/fixtures/refresh/proj-in-sync/scripts/hooks/
#
# The check reuses cli/lib/refresh.sh's own --dry-run hook-sync logic as the
# ground truth for "in sync" rather than reimplementing the comparison —
# this gate can never disagree with the actual sync tooling about what
# counts as drift. Each run is fully sandboxed (scratch $HOME, --dry-run:
# no files are written, no real ~/.claude/agents symlinks are touched).
#
# Usage: bash tests/check-hook-mirror.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
YAKOS_LIB="$REPO_ROOT/cli/lib"
REFRESH_SH="$YAKOS_LIB/refresh.sh"

if [ ! -f "$REFRESH_SH" ]; then
    echo "check-hook-mirror: refresh.sh not found at $REFRESH_SH" >&2
    exit 1
fi

FAIL=0

check_project() {
    local label="$1" project="$2"
    if [ ! -d "$project" ]; then
        printf '  [skip] %s: project dir not found (%s)\n' "$label" "$project"
        return
    fi

    local scratch_home out
    scratch_home="$(mktemp -d)"
    # --dry-run: read-only. Sandboxed HOME so the global agent-symlink phase
    # never touches a real ~/.claude/agents.
    out="$(HOME="$scratch_home" YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$YAKOS_LIB" \
        bash "$REFRESH_SH" --project "$project" --dry-run 2>&1)" || true
    rm -rf "$scratch_home"

    if echo "$out" | grep -q 'status:   in sync'; then
        printf '  [ok]   %s: scripts/hooks in sync with lib/hooks\n' "$label"
        return
    fi

    printf '  [FAIL] %s: scripts/hooks/ has drifted from lib/hooks/\n' "$label" >&2
    echo "$out" | sed 's/^/    /' >&2
    FAIL=1
}

echo "check-hook-mirror: verifying tracked scripts/hooks mirrors are in sync with lib/hooks"
echo ""

check_project "framework root" "$REPO_ROOT"
check_project "tests/fixtures/refresh/proj-in-sync" \
    "$REPO_ROOT/tests/fixtures/refresh/proj-in-sync"
check_project "cli-go/cmd/yakos/testdata/fixtures/refresh/proj-in-sync" \
    "$REPO_ROOT/cli-go/cmd/yakos/testdata/fixtures/refresh/proj-in-sync"

echo ""
if [ "$FAIL" -ne 0 ]; then
    echo "check-hook-mirror: FAILED — one or more tracked scripts/hooks mirrors are stale." >&2
    echo "  Regenerate the drifted mirror(s), e.g.:" >&2
    echo "    HOME=\$(mktemp -d) YAKOS_ROOT=\"$REPO_ROOT\" YAKOS_LIB=\"$YAKOS_LIB\" \\" >&2
    echo "      bash \"$REFRESH_SH\" --project <project-path>" >&2
    echo "  then commit the resulting scripts/hooks/ changes." >&2
    exit 1
fi

echo "check-hook-mirror: all tracked mirrors in sync with lib/hooks."
