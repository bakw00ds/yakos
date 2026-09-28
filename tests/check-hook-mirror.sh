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
# The gate keys ONLY on refresh.sh's per-project "hooks:" summary line
# (new=/synced=/ok=), which is the tracked scripts/hooks/ vs lib/hooks/
# comparison this gate exists to enforce. It deliberately ignores the
# "settings:" line and the global "Agent symlinks" phase: those reflect
# a developer's untracked local .claude/settings.json and the sandboxed
# $HOME's (absent) ~/.claude/agents, neither of which has anything to do
# with the tracked hook mirror — including them made this gate fail on a
# clean checkout whenever the developer had local settings.json drift.
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

    # Decide only on the "hooks:" summary line (new=N synced=N ok=N), not
    # the combined "status:" line, which also folds in settings.json drift
    # and is unrelated to the tracked scripts/hooks/ mirror this gate checks.
    local hooks_line new_count sync_count
    hooks_line="$(echo "$out" | grep -m1 '^    hooks:')" || true

    if [ -z "$hooks_line" ]; then
        printf '  [FAIL] %s: could not find a hooks: summary in refresh --dry-run output\n' "$label" >&2
        # shellcheck disable=SC2001 # multi-line prefix; no pure param-expansion equivalent
        echo "$out" | sed 's/^/    /' >&2
        FAIL=1
        return
    fi

    # [0-9][0-9]* (not \+) — BSD sed (macOS default) doesn't support the \+
    # BRE extension; this pattern is portable to both BSD and GNU sed.
    new_count="$(echo "$hooks_line" | sed -n 's/.*new=\([0-9][0-9]*\).*/\1/p')"
    sync_count="$(echo "$hooks_line" | sed -n 's/.*synced=\([0-9][0-9]*\).*/\1/p')"

    if [ "${new_count:-0}" -eq 0 ] && [ "${sync_count:-0}" -eq 0 ]; then
        printf '  [ok]   %s: scripts/hooks in sync with lib/hooks (%s)\n' "$label" "${hooks_line#    }"
        return
    fi

    printf '  [FAIL] %s: scripts/hooks/ has drifted from lib/hooks/ (%s)\n' "$label" "${hooks_line#    }" >&2
    # shellcheck disable=SC2001 # multi-line prefix; no pure param-expansion equivalent
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
