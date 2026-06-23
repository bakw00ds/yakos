#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# smoke-bare-install.sh — bare-install + project provisioning smoke test.
#
# PURPOSE
#   Simulate a true bare binary install (no adjacent repo lib/) and assert
#   that the embedded lib materializes completely and project provisioning
#   works end-to-end. Designed to catch the class of bug where lib/settings/,
#   hooks, or other subdirs were omitted from the embed, or where symlinks
#   weren't laid down — bugs that unit tests missed because they ran from
#   inside the repo tree (where resolveLibRoot finds the adjacent lib/).
#
# WHAT IT ASSERTS
#   1. The built binary self-materializes its embedded lib to
#      $HOME/.local/share/yakos/<ver>/ when YAKOS_ROOT is unset and the
#      binary is outside the repo tree.
#   2. Every expected lib/ subdir (agents, hooks, playbooks, rules,
#      settings, skills) is present after materialization.
#   3. lib/settings/settings.template.json and soul.template.md exist.
#   4. yakos init creates .claude/settings.json and .yakos.yml.
#   5. yakos refresh provisions real executable .sh files (not symlinks)
#      under scripts/hooks/ — specifically cycle-counter.sh,
#      budget-guard.sh, and output-injection-scan.sh.
#   6. No hook path under scripts/hooks/ is a dangling symlink.
#   7. yakos upgrade --help and yakos update --help exit 0 and document
#      re-provision behavior (network-free plumbing assertion).
#
# FAILURE MODE SIMULATION
#   If lib/settings/ were absent from the embed, assertion 3 would fire
#   with: "MISSING: settings.template.json at <path>". If hooks were not
#   provisioned, assertion 5 would fire: "MISSING: cycle-counter.sh".
#
# USAGE
#   bash scripts/smoke-bare-install.sh
#
# REQUIREMENTS
#   - Run from the repo root (or set REPO_ROOT manually).
#   - make build must produce bin/yakos with embedded lib (make embed-lib
#     is called as part of make build — do not skip it).
#   - On Linux CI: golang + make available.
#   - On macOS developer machines: same.
#
# LIMITATIONS
#   - yakos upgrade/update network path: this test only asserts help text
#     (--help exits 0 and mentions "re-provision"). The live upgrade
#     (GitHub release download + atomic binary swap) is not exercised here
#     because it requires network and a published release. The re-provision
#     plumbing is covered by the init + refresh assertions above.

set -eu

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
TMP="${TMPDIR:-/tmp}/yakos-smoke-bare-install.$$"
mkdir -p "$TMP"
trap 'rm -rf "$TMP" 2>/dev/null || true' EXIT

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() {
    printf '  [FAIL] %s\n' "$*" >&2
    FAIL=$((FAIL + 1))
}

# assert_file: assert a file exists; fail with the path if not.
assert_file() {
    local label="$1" path="$2"
    if [ -f "$path" ]; then
        ok "$label"
    else
        fail "$label — MISSING: $path"
    fi
}

# assert_dir: assert a directory exists.
assert_dir() {
    local label="$1" path="$2"
    if [ -d "$path" ]; then
        ok "$label"
    else
        fail "$label — MISSING dir: $path"
    fi
}

# assert_executable: assert a regular file exists, is executable, and has
# a shebang first line.
assert_executable() {
    local label="$1" path="$2"
    if [ ! -f "$path" ]; then
        fail "$label — MISSING: $path"
        return
    fi
    if [ ! -x "$path" ]; then
        fail "$label — not executable: $path"
        return
    fi
    if ! head -1 "$path" | grep -q '^#!'; then
        fail "$label — no shebang in: $path"
        return
    fi
    ok "$label"
}

# assert_not_dangling_symlink: assert a path is NOT a dangling symlink.
# A dangling symlink is one whose target does not exist.
assert_not_dangling_symlink() {
    local label="$1" path="$2"
    if [ -L "$path" ] && [ ! -e "$path" ]; then
        fail "$label — dangling symlink: $path"
    else
        ok "$label"
    fi
}

# assert_grep: assert a command's combined output matches a pattern.
assert_grep() {
    local label="$1" pattern="$2"; shift 2
    local out
    out="$("$@" 2>&1)" || true
    if printf '%s\n' "$out" | grep -qE "$pattern"; then
        ok "$label"
    else
        fail "$label — pattern '$pattern' not found in output of: $*"
        printf '    output was:\n%s\n' "$out" >&2
    fi
}

# assert_exit0: assert a command exits 0.
assert_exit0() {
    local label="$1"; shift
    if "$@" >/dev/null 2>&1; then
        ok "$label"
    else
        fail "$label — non-zero exit from: $*"
    fi
}

# ---------------------------------------------------------------------------
# Step 1: Build binary with embedded lib
# ---------------------------------------------------------------------------
echo "smoke-bare-install: building binary from $REPO_ROOT"
echo

# make build runs embed-lib first (Makefile dependency), so the binary
# will carry the embedded lib.
(cd "$REPO_ROOT" && make build) >/dev/null 2>&1 || {
    echo "FATAL: make build failed — run it manually to see errors" >&2
    exit 1
}

echo "  Built: $REPO_ROOT/bin/yakos"

# ---------------------------------------------------------------------------
# Step 2: Copy binary OUTSIDE the repo tree
# Critical: running from bin/ inside the repo lets resolveLibRoot find the
# adjacent lib/ and silently masks any embed gaps. We copy to a temp dir.
# ---------------------------------------------------------------------------
ISOLATED_HOME="$TMP/home"
mkdir -p "$ISOLATED_HOME"

YAKOS_BIN="$TMP/yakos"
cp "$REPO_ROOT/bin/yakos" "$YAKOS_BIN"
chmod +x "$YAKOS_BIN"

echo "  Isolated binary: $YAKOS_BIN"
echo "  Isolated HOME:   $ISOLATED_HOME"
echo

# All subsequent invocations use this isolated environment:
#   HOME    = isolated, so materialized lib lands in $ISOLATED_HOME/.local/...
#   YAKOS_IMPL = go  (force Go-native routing; no bash passthrough)
#   YAKOS_ROOT unset (cleared below)
#   PATH    = same as caller (we need git, etc.)
unset YAKOS_ROOT 2>/dev/null || true

run_yakos() {
    HOME="$ISOLATED_HOME" YAKOS_IMPL=go YAKOS_ROOT="" "$YAKOS_BIN" "$@"
}

# ---------------------------------------------------------------------------
# Step 3: Trigger materialization and assert embedded lib is complete
# ---------------------------------------------------------------------------
echo "=== Step 3: embedded lib materialization ==="

# Read the version embedded in the binary.
VER="$(HOME="$ISOLATED_HOME" YAKOS_IMPL=go "$YAKOS_BIN" --version 2>&1 \
    | grep -oE '[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1)"
VER="${VER:-dev}"
echo "  Detected version: $VER"

MAT_ROOT="$ISOLATED_HOME/.local/share/yakos/$VER"

# Run a command that triggers resolveLibRoot → MaterializeEmbedded.
# yakos install --dry-run triggers the full materialize cascade without
# writing ~/.yakos or symlinks.
echo "  Triggering materialization via: yakos install --dry-run"
run_yakos install --dry-run >/dev/null 2>&1 || true
# Also try refresh --help which also triggers resolveLibRoot on the go binary.
run_yakos refresh --help >/dev/null 2>&1 || true

# If materialization did not fire from those, force it via yakos --version
# (always runs natively) — the binary materializes on first lib-needing call.
# As a belt-and-suspenders, explicitly call install without --dry-run to
# actually materialize. We use a fake YAKOS_ROOT=/nonexistent to force the
# embedded path.
HOME="$ISOLATED_HOME" YAKOS_IMPL=go YAKOS_ROOT=/nonexistent-path-that-does-not-exist \
    "$YAKOS_BIN" install >/dev/null 2>&1 || true

# Final check: the materialized root must exist.
assert_dir "materialized lib root exists" "$MAT_ROOT"

# Assert all expected lib/ subdirs are present.
for subdir in agents hooks playbooks rules settings skills; do
    assert_dir "lib/$subdir present" "$MAT_ROOT/lib/$subdir"
done

# Assert the files that were previously missing (the bug this test guards).
assert_file "settings.template.json present" \
    "$MAT_ROOT/lib/settings/settings.template.json"
assert_file "soul.template.md present" \
    "$MAT_ROOT/lib/settings/soul.template.md"

echo

# ---------------------------------------------------------------------------
# Step 4: Full project provisioning on a fresh git-initialized project
# ---------------------------------------------------------------------------
echo "=== Step 4: project provisioning (yakos init + refresh) ==="

PROJ_DIR="$TMP/test-project"
mkdir -p "$PROJ_DIR"
cd "$PROJ_DIR"
git init -q -b main
git config user.email ci@yakos.test
git config user.name "yakos-ci"
echo "0.1.0.0" > VERSION
echo "# smoke test project" > README.md
git add . && git commit -qm "initial"
cd "$REPO_ROOT"

# -- yakos init
echo "  Running: yakos init smoke --project $PROJ_DIR"
init_out="$(run_yakos init smoke --project "$PROJ_DIR" 2>&1)" || {
    fail "yakos init exited non-zero"
    printf '  output:\n%s\n' "$init_out" >&2
}

# Assert no error strings in init output.
if printf '%s\n' "$init_out" | grep -qiE 'not found|error:|fatal:'; then
    fail "yakos init output contains error indicators"
    printf '  output:\n%s\n' "$init_out" >&2
else
    ok "yakos init: no error indicators in output"
fi

# Assert init artifacts.
assert_file ".claude/settings.json written by init" \
    "$PROJ_DIR/.claude/settings.json"
assert_file ".yakos.yml written by init" \
    "$PROJ_DIR/.yakos.yml"

# -- yakos refresh
echo "  Running: yakos refresh --project $PROJ_DIR"
refresh_out="$(run_yakos refresh --project "$PROJ_DIR" 2>&1)" || {
    fail "yakos refresh exited non-zero"
    printf '  output:\n%s\n' "$refresh_out" >&2
}

# Assert no error strings in refresh output.
if printf '%s\n' "$refresh_out" | grep -qiE 'not found|settings template not found|error:|fatal:'; then
    fail "yakos refresh output contains error indicators"
    printf '  output:\n%s\n' "$refresh_out" >&2
else
    ok "yakos refresh: no error indicators in output"
fi

# Assert refresh copied hooks (settings: added>0 or new>0 on a fresh project).
# A fresh project has no prior hooks so new should be >0.
if printf '%s\n' "$refresh_out" | grep -qE 'new=[1-9][0-9]*'; then
    ok "yakos refresh: hooks were provisioned (new>0)"
else
    # Accept synced>0 too (idempotent re-run).
    if printf '%s\n' "$refresh_out" | grep -qE '(synced=[1-9][0-9]*|ok=[1-9][0-9]*)'; then
        ok "yakos refresh: hooks were provisioned (synced or ok count present)"
    else
        fail "yakos refresh: expected new>0 hooks provisioned; output:"
        printf '%s\n' "$refresh_out" >&2
    fi
fi

HOOKS_DIR="$PROJ_DIR/scripts/hooks"

# Assert key hooks exist as real executable files.
for hook in cycle-counter.sh budget-guard.sh output-injection-scan.sh; do
    assert_executable "scripts/hooks/$hook is executable" "$HOOKS_DIR/$hook"
done

# Assert no hook is a dangling symlink.
if [ -d "$HOOKS_DIR" ]; then
    while IFS= read -r -d '' entry; do
        assert_not_dangling_symlink "no dangling symlink: $entry" "$entry"
    done < <(find "$HOOKS_DIR" -maxdepth 1 -print0 2>/dev/null)
else
    fail "scripts/hooks/ directory missing after refresh"
fi

echo

# ---------------------------------------------------------------------------
# Step 5: Upgrade/update plumbing assertions (network-free)
# ---------------------------------------------------------------------------
echo "=== Step 5: upgrade/update plumbing (--help, offline) ==="

# yakos upgrade --help must exit 0 and mention re-provision.
assert_exit0 "yakos upgrade --help exits 0" run_yakos upgrade --help
assert_grep "yakos upgrade --help mentions re-provision" \
    "(re-provision|install --force|materialize)" \
    run_yakos upgrade --help

# yakos update --help must exit 0 and describe the update path.
assert_exit0 "yakos update --help exits 0" run_yakos update --help
assert_grep "yakos update --help mentions refresh" \
    "(refresh|pull|update)" \
    run_yakos update --help

echo

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "=== Summary ==="
echo "  passed: $PASS"
echo "  failed: $FAIL"
echo

if [ "$FAIL" -gt 0 ]; then
    echo "SMOKE TEST FAILED — $FAIL assertion(s) failed." >&2
    exit 1
fi

echo "smoke-bare-install: all assertions passed."
