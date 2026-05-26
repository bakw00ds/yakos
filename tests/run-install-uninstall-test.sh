#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Install/uninstall integration tests for the managed-launcher +
#          stale-pointer robustness changes.
# Inputs:  none
# Outputs: stdout: per-test pass/fail; stderr: error context
# Reads:   cli/lib/install.sh, cli/lib/uninstall.sh, cli/lib/compat.sh
# Writes:  $TMPDIR/yakos-install-test.<pid>/* (cleaned at end)
# Exit codes: 0 all pass, 1 any failure.
#
# Tests:
#   1. install: launcher symlink is created at ~/.local/bin/yakos
#   2. install: launcher is recorded in ~/.yakos-state/install-manifest
#   3. install: pointer file written with correct YAKOS_ROOT_ABS
#   4. install: standard lib symlinks created under ~/.claude/
#   5. install: ~/.claude/projects/ is not touched
#   6. uninstall: launcher symlink removed on uninstall
#   7. uninstall: pointer file and manifest removed on uninstall
#   8. uninstall: lib symlinks removed on uninstall
#   9. uninstall: ~/.claude/projects/ is not touched on uninstall
#  10. install: foreign real-file yakos at launcher path is left alone (warn only)
#  11. install: foreign symlink at launcher path is left alone (warn only)
#  12. install: yakOS-owned symlink at launcher path from different root is refreshed
#  13. uninstall: stale pointer (target missing) — warns + cleans without crashing
#  14. uninstall: dangling lib symlinks removed under stale-pointer condition
#  15. uninstall: --root <path> cleans using explicit root instead of pointer
#  16. uninstall: dangling launcher symlink removed even without valid manifest root
#  17. uninstall: foreign launcher symlink NOT removed by uninstall

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

WORKDIR="${TMPDIR:-/tmp}/yakos-install-test.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

# Source compat for ct_realpath etc.
# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"

# ---------------------------------------------------------------------------
# Helper: run install.sh inside a sandboxed HOME and YAKOS_ROOT.
# Args: <fake_home> <fake_root> [extra install args...]
# ---------------------------------------------------------------------------
run_install() {
    local fake_home="$1" fake_root="$2"
    shift 2
    HOME="$fake_home" \
    YAKOS_ROOT="$fake_root" \
    YAKOS_LIB="$fake_root/cli/lib" \
    bash "$REPO_ROOT/cli/lib/install.sh" "$@" 2>/dev/null
}

# ---------------------------------------------------------------------------
# Helper: run uninstall.sh inside a sandboxed HOME.
# The script derives YAKOS_ROOT from ~/.yakos; we can also pass --root.
# ---------------------------------------------------------------------------
run_uninstall() {
    local fake_home="$1"
    shift
    HOME="$fake_home" \
    YAKOS_ROOT="" \
    YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" "$@" 2>/dev/null
}

# ---------------------------------------------------------------------------
# Build a minimal fake YAKOS_ROOT that install.sh can use.
# It must have: cli/yakos, cli/lib/install.sh, cli/lib/compat.sh,
# and at least one lib/agents file so symlink creation is exercised.
# ---------------------------------------------------------------------------
make_fake_root() {
    local root="$1"
    mkdir -p "$root/cli/lib"
    mkdir -p "$root/lib/agents"
    # Minimal cli/yakos (executable stub).
    printf '#!/usr/bin/env bash\necho yakos-stub\n' > "$root/cli/yakos"
    chmod +x "$root/cli/yakos"
    # Point YAKOS_LIB at the REAL lib so compat.sh / install.sh are usable.
    # install.sh itself is sourced from the real repo; we only need the root
    # structure for symlink targets.  For lib/ content we create a stub file.
    printf '# stub agent\n' > "$root/lib/agents/stub-agent.md"
    # compat.sh must exist in the fake root's cli/lib too since install.sh
    # sources it from YAKOS_LIB which is set to fake_root/cli/lib.
    # Easiest: symlink to the real one.
    ln -s "$REPO_ROOT/cli/lib/compat.sh" "$root/cli/lib/compat.sh"
}

echo "yakos install/uninstall tests"
echo

# ===========================================================================
# Test 1: install creates launcher symlink at ~/.local/bin/yakos
# ===========================================================================
echo "Test 1: install creates launcher symlink"
T1="$WORKDIR/t1"
FAKE_ROOT="$WORKDIR/t1-root"
make_fake_root "$FAKE_ROOT"
mkdir -p "$T1/.claude"

run_install "$T1" "$FAKE_ROOT" || true

LAUNCHER="$T1/.local/bin/yakos"
if [ -L "$LAUNCHER" ]; then
    ok "launcher symlink exists at $LAUNCHER"
else
    fail "launcher symlink NOT created at $LAUNCHER"
fi

# ===========================================================================
# Test 2: install records launcher in manifest
# ===========================================================================
echo
echo "Test 2: install records launcher in manifest"
MANIFEST="$T1/.yakos-state/install-manifest"
if [ -f "$MANIFEST" ] && grep -q "^launcher=" "$MANIFEST"; then
    ok "install-manifest written with launcher= entry"
else
    fail "install-manifest missing or no launcher= entry at $MANIFEST"
fi

# ===========================================================================
# Test 3: pointer file written with correct root
# ===========================================================================
echo
echo "Test 3: pointer file written"
POINTER="$T1/.yakos"
if [ -f "$POINTER" ]; then
    POINTED="$(cat "$POINTER")"
    FAKE_ROOT_ABS="$(ct_realpath "$FAKE_ROOT")"
    if [ "$POINTED" = "$FAKE_ROOT_ABS" ]; then
        ok "pointer file contains correct YAKOS_ROOT_ABS"
    else
        fail "pointer file contains '$POINTED' (expected '$FAKE_ROOT_ABS')"
    fi
else
    fail "pointer file not written at $POINTER"
fi

# ===========================================================================
# Test 4: lib symlinks created under ~/.claude/agents/
# ===========================================================================
echo
echo "Test 4: lib symlinks created"
AGENT_LINK="$T1/.claude/agents/stub-agent.md"
if [ -L "$AGENT_LINK" ]; then
    ok "agent symlink created at $AGENT_LINK"
else
    fail "agent symlink NOT created at $AGENT_LINK"
fi

# ===========================================================================
# Test 5: ~/.claude/projects/ not touched by install
# ===========================================================================
echo
echo "Test 5: ~/.claude/projects/ not touched by install"
PROJECTS_DIR="$T1/.claude/projects"
if [ -d "$PROJECTS_DIR" ]; then
    fail "install created ~/.claude/projects/ (should never touch it)"
else
    ok "~/.claude/projects/ not created by install"
fi

# ===========================================================================
# Test 6: uninstall removes launcher symlink
# ===========================================================================
echo
echo "Test 6: uninstall removes launcher symlink"
# Use the same sandbox from T1.
run_uninstall "$T1" || true
if [ ! -e "$LAUNCHER" ]; then
    ok "launcher symlink removed by uninstall"
else
    fail "launcher symlink still exists after uninstall"
fi

# ===========================================================================
# Test 7: uninstall removes pointer and manifest
# ===========================================================================
echo
echo "Test 7: uninstall removes pointer and manifest"
if [ ! -f "$T1/.yakos" ]; then
    ok "pointer file removed"
else
    fail "pointer file still exists after uninstall"
fi
if [ ! -f "$T1/.yakos-state/install-manifest" ]; then
    ok "install-manifest removed"
else
    fail "install-manifest still exists after uninstall"
fi

# ===========================================================================
# Test 8: uninstall removes yakOS lib symlinks
# ===========================================================================
echo
echo "Test 8: uninstall removes lib symlinks"
if [ ! -L "$AGENT_LINK" ]; then
    ok "agent symlink removed by uninstall"
else
    fail "agent symlink still exists after uninstall"
fi

# ===========================================================================
# Test 9: ~/.claude/projects/ not touched by uninstall
# ===========================================================================
echo
echo "Test 9: ~/.claude/projects/ not touched by uninstall"
T9="$WORKDIR/t9"
FAKE_ROOT9="$WORKDIR/t9-root"
make_fake_root "$FAKE_ROOT9"
mkdir -p "$T9/.claude/projects/some-project"
printf 'user data\n' > "$T9/.claude/projects/some-project/MEMORY.md"

run_install "$T9" "$FAKE_ROOT9" || true
run_uninstall "$T9" || true

if [ -f "$T9/.claude/projects/some-project/MEMORY.md" ]; then
    ok "~/.claude/projects/ preserved through install+uninstall"
else
    fail "~/.claude/projects/some-project/MEMORY.md was removed (should be untouched)"
fi

# ===========================================================================
# Test 10: foreign real-file at launcher path is left alone
# ===========================================================================
echo
echo "Test 10: foreign real-file at launcher path left alone"
T10="$WORKDIR/t10"
FAKE_ROOT10="$WORKDIR/t10-root"
make_fake_root "$FAKE_ROOT10"
mkdir -p "$T10/.claude"
LAUNCHER10="$T10/.local/bin/yakos"
mkdir -p "$(dirname "$LAUNCHER10")"
printf '#!/bin/sh\necho foreign\n' > "$LAUNCHER10"
chmod +x "$LAUNCHER10"

run_install "$T10" "$FAKE_ROOT10" 2>/dev/null || true

# The real file must still be a regular file (not replaced by symlink).
if [ -f "$LAUNCHER10" ] && [ ! -L "$LAUNCHER10" ]; then
    ok "foreign real-file at launcher path left untouched"
else
    fail "foreign real-file was replaced or removed (should be left alone)"
fi

# ===========================================================================
# Test 11: foreign symlink at launcher path is left alone
# ===========================================================================
echo
echo "Test 11: foreign symlink at launcher path left alone"
T11="$WORKDIR/t11"
FAKE_ROOT11="$WORKDIR/t11-root"
make_fake_root "$FAKE_ROOT11"
mkdir -p "$T11/.claude"
LAUNCHER11="$T11/.local/bin/yakos"
mkdir -p "$(dirname "$LAUNCHER11")"
FOREIGN_TARGET="$WORKDIR/t11-foreign-yakos"
printf '#!/bin/sh\necho foreign\n' > "$FOREIGN_TARGET"
ln -s "$FOREIGN_TARGET" "$LAUNCHER11"

run_install "$T11" "$FAKE_ROOT11" 2>/dev/null || true

# The symlink must still point at the foreign target.
if [ -L "$LAUNCHER11" ]; then
    CURRENT_TARGET="$(readlink "$LAUNCHER11")"
    if [ "$CURRENT_TARGET" = "$FOREIGN_TARGET" ]; then
        ok "foreign symlink at launcher path left untouched"
    else
        fail "foreign symlink was redirected (points to '$CURRENT_TARGET', expected '$FOREIGN_TARGET')"
    fi
else
    fail "foreign symlink was removed or replaced"
fi

# ===========================================================================
# Test 12: yakOS-owned symlink from a different root is refreshed
# ===========================================================================
echo
echo "Test 12: yakOS symlink from different root refreshed on install"
T12="$WORKDIR/t12"
FAKE_ROOT12="$WORKDIR/t12-root"
FAKE_ROOT12_OLD="$WORKDIR/t12-root-old"
make_fake_root "$FAKE_ROOT12"
make_fake_root "$FAKE_ROOT12_OLD"
mkdir -p "$T12/.claude"
LAUNCHER12="$T12/.local/bin/yakos"
mkdir -p "$(dirname "$LAUNCHER12")"
# Simulate a prior yakOS install at an old root — symlink has yakOS shape.
ln -s "$FAKE_ROOT12_OLD/cli/yakos" "$LAUNCHER12"

run_install "$T12" "$FAKE_ROOT12" 2>/dev/null || true

if [ -L "$LAUNCHER12" ]; then
    NEW_TARGET="$(readlink "$LAUNCHER12")"
    EXPECTED="$(ct_realpath "$FAKE_ROOT12")/cli/yakos"
    if [ "$NEW_TARGET" = "$EXPECTED" ]; then
        ok "yakOS-owned symlink from old root refreshed to new root"
    else
        fail "launcher points to '$NEW_TARGET' (expected '$EXPECTED')"
    fi
else
    fail "launcher symlink missing after install over old yakOS symlink"
fi

# ===========================================================================
# Test 13: stale pointer (target missing) — warns + exits 0, no crash
# ===========================================================================
echo
echo "Test 13: stale pointer (target missing) — warns without crashing"
T13="$WORKDIR/t13"
mkdir -p "$T13/.claude/agents"
# Write a pointer to a path that doesn't exist.
printf '/nonexistent/yakos-root-that-is-gone\n' > "$T13/.yakos"

rc=0
HOME="$T13" YAKOS_ROOT="" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" 2>/dev/null || rc=$?

if [ "$rc" = "0" ]; then
    ok "uninstall exits 0 with stale pointer (no crash)"
else
    fail "uninstall exited $rc with stale pointer (expected 0)"
fi
# Pointer should be removed even when stale.
if [ ! -f "$T13/.yakos" ]; then
    ok "stale pointer file removed"
else
    fail "stale pointer file still present after uninstall"
fi

# ===========================================================================
# Test 14: dangling lib symlinks removed under stale-pointer condition
# ===========================================================================
echo
echo "Test 14: dangling lib symlinks removed when pointer is stale"
T14="$WORKDIR/t14"
mkdir -p "$T14/.claude/agents"
# Write stale pointer.
printf '/nonexistent/yakos-root-old\n' > "$T14/.yakos"
# Plant a dangling symlink (target does not exist).
ln -s "/nonexistent/yakos-root-old/lib/agents/some.md" "$T14/.claude/agents/some.md"
# Plant a live symlink that is NOT yakOS-owned.
REAL_FILE="$WORKDIR/t14-live-file"
printf 'user data\n' > "$REAL_FILE"
ln -s "$REAL_FILE" "$T14/.claude/agents/user-agent.md"

HOME="$T14" YAKOS_ROOT="" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" 2>/dev/null || true

if [ ! -L "$T14/.claude/agents/some.md" ]; then
    ok "dangling yakOS symlink removed"
else
    fail "dangling yakOS symlink still present"
fi
if [ -L "$T14/.claude/agents/user-agent.md" ]; then
    ok "live non-yakOS symlink preserved"
else
    fail "live non-yakOS symlink was removed (should be preserved)"
fi

# ===========================================================================
# Test 15: --root <path> cleans using explicit root
# ===========================================================================
echo
echo "Test 15: --root <path> cleans leftover install at explicit root"
T15="$WORKDIR/t15"
FAKE_ROOT15="$WORKDIR/t15-root"
make_fake_root "$FAKE_ROOT15"
mkdir -p "$T15/.claude/agents"
FAKE_ROOT15_ABS="$(ct_realpath "$FAKE_ROOT15")"

# Plant a symlink that resolves into FAKE_ROOT15 (simulating a prior install).
TARGET15="$FAKE_ROOT15_ABS/lib/agents/stub-agent.md"
ln -s "$TARGET15" "$T15/.claude/agents/stub-agent.md"
# Write pointer referencing the explicit root.
printf '%s\n' "$FAKE_ROOT15_ABS" > "$T15/.yakos"

# Run uninstall with --root pointing at FAKE_ROOT15.
HOME="$T15" YAKOS_ROOT="" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" --root "$FAKE_ROOT15_ABS" 2>/dev/null || true

if [ ! -L "$T15/.claude/agents/stub-agent.md" ]; then
    ok "--root <path>: symlink into explicit root removed"
else
    fail "--root <path>: symlink into explicit root still present"
fi

# ===========================================================================
# Test 16: dangling launcher symlink removed even without valid manifest root
# ===========================================================================
echo
echo "Test 16: dangling launcher symlink removed"
T16="$WORKDIR/t16"
mkdir -p "$T16/.claude"
LAUNCHER16="$T16/.local/bin/yakos"
mkdir -p "$(dirname "$LAUNCHER16")"
# Dangling launcher — points at a non-existent path.
ln -s "/nonexistent/old-root/cli/yakos" "$LAUNCHER16"
# Write manifest so uninstall knows the launcher path.
mkdir -p "$T16/.yakos-state"
printf 'launcher=%s\n' "$LAUNCHER16" > "$T16/.yakos-state/install-manifest"
# Write a stale pointer so we at least have some root reference.
printf '/nonexistent/old-root\n' > "$T16/.yakos"

HOME="$T16" YAKOS_ROOT="" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" 2>/dev/null || true

if [ ! -e "$LAUNCHER16" ] && [ ! -L "$LAUNCHER16" ]; then
    ok "dangling launcher symlink removed"
else
    fail "dangling launcher symlink still exists"
fi

# ===========================================================================
# Test 17: foreign launcher symlink NOT removed by uninstall
# ===========================================================================
echo
echo "Test 17: foreign launcher symlink not removed by uninstall"
T17="$WORKDIR/t17"
FAKE_ROOT17="$WORKDIR/t17-root"
make_fake_root "$FAKE_ROOT17"
mkdir -p "$T17/.claude"
FAKE_ROOT17_ABS="$(ct_realpath "$FAKE_ROOT17")"
LAUNCHER17="$T17/.local/bin/yakos"
mkdir -p "$(dirname "$LAUNCHER17")"
# Foreign target — a completely unrelated binary in a different location.
FOREIGN17="$WORKDIR/t17-foreign-bin"
printf '#!/bin/sh\necho foreign\n' > "$FOREIGN17"
chmod +x "$FOREIGN17"
ln -s "$FOREIGN17" "$LAUNCHER17"
# Write manifest pointing at the launcher (as if we mistakenly recorded it).
mkdir -p "$T17/.yakos-state"
printf 'launcher=%s\n' "$LAUNCHER17" > "$T17/.yakos-state/install-manifest"
# Write pointer.
printf '%s\n' "$FAKE_ROOT17_ABS" > "$T17/.yakos"

HOME="$T17" YAKOS_ROOT="" YAKOS_LIB="$REPO_ROOT/cli/lib" \
    bash "$REPO_ROOT/cli/lib/uninstall.sh" 2>/dev/null || true

if [ -L "$LAUNCHER17" ] && [ "$(readlink "$LAUNCHER17")" = "$FOREIGN17" ]; then
    ok "foreign launcher symlink left untouched by uninstall"
else
    fail "foreign launcher symlink was removed or modified (should be left alone)"
fi

# ===========================================================================
# Summary
# ===========================================================================
echo
echo "yakos install/uninstall tests: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
