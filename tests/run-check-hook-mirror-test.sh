#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Self-check for tests/check-hook-mirror.sh (K-91c drift gate).
#
# Regression coverage for a false positive: the gate used to key off
# refresh.sh's combined "status:" line, which folds settings.json drift
# (untracked, developer-local) into the same verdict as scripts/hooks/
# drift (tracked, the thing this gate exists to catch). A developer with
# a stale local .claude/settings.json saw the gate FAIL even though
# scripts/hooks/ was byte-identical to lib/hooks/.
#
# The fix makes the gate key ONLY on refresh.sh's per-project "hooks:"
# summary line (new=/synced=/ok=). These tests exercise that decision
# directly, against sandboxed copies of tests/fixtures/refresh/proj-in-sync
# so the real repo's mirrors are never touched.
#
# Tests:
#   (a) in-sync scripts/hooks/ + drifted local settings.json → PASS
#   (b) tampered mirror file (content differs from lib/hooks/) → FAIL
#   (c) missing mirror file                                   → FAIL
#   (d) mutation check: re-run scenario (a) against the PRE-FIX
#       "status: in sync" decision logic and confirm THAT fails —
#       proves the fix changes behavior, not just wording.
#
# Usage: bash tests/run-check-hook-mirror-test.sh

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
YAKOS_LIB="$REPO_ROOT/cli/lib"
REFRESH_SH="$YAKOS_LIB/refresh.sh"
GATE_SH="$REPO_ROOT/tests/check-hook-mirror.sh"
FIXTURE="$REPO_ROOT/tests/fixtures/refresh/proj-in-sync"

if [ ! -f "$GATE_SH" ]; then
    echo "run-check-hook-mirror-test: gate script not found at $GATE_SH" >&2
    exit 1
fi
if [ ! -d "$FIXTURE" ]; then
    echo "run-check-hook-mirror-test: fixture not found at $FIXTURE" >&2
    exit 1
fi

WORKDIR="${TMPDIR:-/tmp}/yakos-check-hook-mirror-test.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

TEST_PASS=0
TEST_FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; TEST_PASS=$((TEST_PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; TEST_FAIL=$((TEST_FAIL + 1)); }

# ---------------------------------------------------------------------------
# setup_project: fresh sandboxed copy of the proj-in-sync fixture.
# ---------------------------------------------------------------------------
setup_project() {
    local name="$1" wdir
    wdir="$WORKDIR/$name"
    mkdir -p "$wdir"
    cp -r "$FIXTURE/." "$wdir/"
    echo "$wdir"
}

# ---------------------------------------------------------------------------
# extract_check_project: pulls the check_project() function body out of the
# gate script so it can be exercised against an arbitrary sandboxed project
# dir, without re-running (or disturbing) the gate's own hardcoded 3-mirror
# check list against the real repo.
# ---------------------------------------------------------------------------
extract_check_project() {
    awk '/^check_project\(\) \{/ { p = 1 } p { print } /^}/ { if (p) exit }' "$GATE_SH"
}

CHECK_PROJECT_SRC="$(extract_check_project)"
if [ -z "$CHECK_PROJECT_SRC" ]; then
    echo "run-check-hook-mirror-test: could not extract check_project() from $GATE_SH" >&2
    exit 1
fi

# ---------------------------------------------------------------------------
# gate_verdict: runs the CURRENT (post-fix) check_project() against a
# project dir. Prints its output; exits 0 on [ok], 1 on [FAIL].
# ---------------------------------------------------------------------------
gate_verdict() {
    local project="$1"
    (
        # shellcheck disable=SC2030,SC2031 # deliberately shadows the outer
        # FAIL counter: check_project() sets $FAIL as its drift flag, and
        # this subshell's exit code is how the caller reads it back.
        FAIL=0
        eval "$CHECK_PROJECT_SRC"
        check_project "sandbox" "$project"
        exit "$FAIL"
    )
}

# ---------------------------------------------------------------------------
# prefix_status_verdict: re-implements the PRE-FIX decision (grep for the
# combined "status:   in sync" line) against a project dir, for the
# mutation check. Deliberately independent of check-hook-mirror.sh so a
# future edit to the gate script can't silently make this check a no-op.
# ---------------------------------------------------------------------------
prefix_status_verdict() {
    local project="$1" scratch_home out
    scratch_home="$(mktemp -d)"
    out="$(HOME="$scratch_home" YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$YAKOS_LIB" \
        bash "$REFRESH_SH" --project "$project" --dry-run 2>&1)" || true
    rm -rf "$scratch_home"
    echo "$out" | grep -q 'status:   in sync'
}

# ===========================================================================
# (a) in-sync scripts/hooks/ + drifted local settings.json → PASS
# ===========================================================================
PROJ_A="$(setup_project proj-a)"
# Drift the local settings.json: drop one hook registration so
# _merge_settings reports added>0 (settings drift), while scripts/hooks/
# stays byte-identical to lib/hooks/.
python3 - "$PROJ_A/.claude/settings.json" <<'PYEOF'
import json
import sys

path = sys.argv[1]
with open(path) as f:
    data = json.load(f)

hooks = data.get("hooks", {})
ups = hooks.get("UserPromptSubmit", [])
if ups and "hooks" in ups[0]:
    ups[0]["hooks"] = [
        h for h in ups[0]["hooks"]
        if "retro-dispatch.sh" not in h.get("command", "")
    ]

with open(path, "w") as f:
    json.dump(data, f, indent=2)
PYEOF

if gate_verdict "$PROJ_A" >/tmp/gate-a.$$ 2>&1; then
    ok "(a) in-sync mirror + drifted settings.json -> gate PASSes"
else
    fail "(a) in-sync mirror + drifted settings.json -> gate FAILed (should PASS)"
fi
sed 's/^/    /' /tmp/gate-a.$$
rm -f /tmp/gate-a.$$

# ===========================================================================
# (b) tampered mirror file (content differs) → FAIL
# ===========================================================================
PROJ_B="$(setup_project proj-b)"
TAMPER_TARGET="$PROJ_B/scripts/hooks/budget-guard.sh"
if [ ! -f "$TAMPER_TARGET" ]; then
    fail "(b) fixture missing expected file: $TAMPER_TARGET"
else
    printf '\n# tampered for test (b)\n' >> "$TAMPER_TARGET"
    if gate_verdict "$PROJ_B" >/tmp/gate-b.$$ 2>&1; then
        fail "(b) tampered mirror file -> gate PASSed (should FAIL)"
    else
        ok "(b) tampered mirror file -> gate FAILs"
    fi
    sed 's/^/    /' /tmp/gate-b.$$
    rm -f /tmp/gate-b.$$
fi

# ===========================================================================
# (c) missing mirror file → FAIL
# ===========================================================================
PROJ_C="$(setup_project proj-c)"
MISSING_TARGET="$PROJ_C/scripts/hooks/budget-guard.sh"
if [ ! -f "$MISSING_TARGET" ]; then
    fail "(c) fixture missing expected file: $MISSING_TARGET"
else
    rm -f "$MISSING_TARGET" "${MISSING_TARGET}.framework-hash"
    if gate_verdict "$PROJ_C" >/tmp/gate-c.$$ 2>&1; then
        fail "(c) missing mirror file -> gate PASSed (should FAIL)"
    else
        ok "(c) missing mirror file -> gate FAILs"
    fi
    sed 's/^/    /' /tmp/gate-c.$$
    rm -f /tmp/gate-c.$$
fi

# ===========================================================================
# (d) mutation check: scenario (a) against the PRE-FIX decision logic must
# FAIL, proving the fix actually changes the verdict (not just the message).
# ===========================================================================
if prefix_status_verdict "$PROJ_A"; then
    fail "(d) mutation check: pre-fix 'status: in sync' logic PASSed on drifted-settings project (expected it to FAIL, same as the reported bug)"
else
    ok "(d) mutation check: pre-fix 'status: in sync' logic FAILs on drifted-settings project, confirming the fix changes the verdict"
fi

# ===========================================================================
# Summary
# ===========================================================================
echo ""
echo "check-hook-mirror self-check: $TEST_PASS passed, $TEST_FAIL failed"
[ "$TEST_FAIL" -eq 0 ] && exit 0 || exit 1
