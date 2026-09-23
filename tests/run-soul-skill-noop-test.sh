#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-soul-skill-noop-test.sh — bash-side fail-loud coverage for
# `yakos soul approve/reject` and `yakos skill candidates --review`.
#
# S-5 round 2 (2026-09-23): the Go-side fix for these two silent
# no-ops (exit 0 while nothing was approved/rejected/reviewed) has no
# effect under the CLI's default shadow-mode routing, since
# selectImpl() passes through to the bash scripts whenever YAKOS_IMPL
# is unset and a bash tree is present. This suite exercises
# cli/lib/soul.sh and cli/lib/skill.sh directly (the scripts actually
# reached in that default mode) and asserts the same fail-loud
# contract: non-zero exit + an explanatory line on stderr.
#
# Test coverage:
#   1. soul approve <slug>          → rc != 0, stderr explains why
#   2. soul reject <slug>           → rc != 0, stderr explains why
#   3. skill candidates --review, no candidates pending → rc != 0
#   4. skill candidates --review, WITH a pending candidate → rc != 0,
#      AND the real (non-interactive) listing still printed to stdout
#      (the fix must not swallow the real work already done)
#   5. skill candidates (no --review) → rc == 0 (regression guard —
#      the plain listing path must be unaffected)
#
# Scripts under test default to this repo's cli/lib/{soul,skill}.sh,
# but can be overridden via $SOUL_SH / $SKILL_SH so the same assertions
# can be replayed against a pre-fix checkout (e.g. `git show
# <rev>:cli/lib/soul.sh`) to prove the suite catches the regression.
# See tests/run-soul-skill-noop-fail-then-pass.sh for that proof.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
SOUL_SH="${SOUL_SH:-$REPO_ROOT/cli/lib/soul.sh}"
SKILL_SH="${SKILL_SH:-$REPO_ROOT/cli/lib/skill.sh}"

pass=0; fail=0; fail_log=""

note()  { printf '  %s\n' "$1"; }
ok()    { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()   { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# ---------------------------------------------------------------------------
# Shared temp environment
# ---------------------------------------------------------------------------

TMP="$(mktemp -d -t yakos-soul-skill-noop-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

export HOME="$TMP/fakehome"
export YAKOS_LIB="$REPO_ROOT/cli/lib"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_WORK_DIR="$TMP/work"
mkdir -p "$HOME" "$YAKOS_WORK_DIR/current"

CANDIDATES_FILE="$YAKOS_WORK_DIR/current/skill-candidates.md"

note "soul.sh under test: $SOUL_SH"
note "skill.sh under test: $SKILL_SH"

# ---------------------------------------------------------------------------
# Test 1: soul approve → fail loud
# ---------------------------------------------------------------------------
note ""
note "=== Test 1: soul approve <slug> fails loud ==="

rc=0
out="$(bash "$SOUL_SH" approve some-bogus-slug 2>&1)" || rc=$?

if [ "$rc" -ne 0 ]; then
    ok "test 1: rc=$rc (non-zero)"
else
    bad "test 1: expected non-zero rc, got 0 (silent no-op) — output: $out"
fi

case "$out" in
    *"not yet implemented"*) ok "test 1: stderr explains why" ;;
    *) bad "test 1: stderr missing explanation — got: $out" ;;
esac

# ---------------------------------------------------------------------------
# Test 2: soul reject → fail loud
# ---------------------------------------------------------------------------
note ""
note "=== Test 2: soul reject <slug> fails loud ==="

rc=0
out="$(bash "$SOUL_SH" reject some-bogus-slug 2>&1)" || rc=$?

if [ "$rc" -ne 0 ]; then
    ok "test 2: rc=$rc (non-zero)"
else
    bad "test 2: expected non-zero rc, got 0 (silent no-op) — output: $out"
fi

case "$out" in
    *"not yet implemented"*) ok "test 2: stderr explains why" ;;
    *) bad "test 2: stderr missing explanation — got: $out" ;;
esac

# ---------------------------------------------------------------------------
# Test 3: skill candidates --review, nothing pending → still rc == 0.
#
# This mirrors internal/skill.runCandidates on the Go side deliberately:
# both bail out with "(no pending candidates)" + exit 0 BEFORE reaching
# the --review advisory, because there is nothing to interactively
# review — --review's unmet promise ("I'll walk you through each
# candidate") is only broken when there's at least one candidate it
# silently fails to walk through. See Test 4 for that case.
# ---------------------------------------------------------------------------
note ""
note "=== Test 3: skill candidates --review (no pending) is a real no-op ==="

rm -f "$CANDIDATES_FILE"

rc=0
out="$(bash "$SKILL_SH" candidates --review 2>&1)" || rc=$?

if [ "$rc" -eq 0 ]; then
    ok "test 3: rc=0 (nothing pending — matches Go-side runCandidates)"
else
    bad "test 3: expected rc=0 (nothing to review), got rc=$rc — output: $out"
fi

case "$out" in
    *"no pending candidates"*) ok "test 3: explains nothing is pending" ;;
    *) bad "test 3: missing '(no pending candidates)' — got: $out" ;;
esac

# ---------------------------------------------------------------------------
# Test 4: skill candidates --review, WITH a pending candidate → fail loud,
# but the real listing must still print (the fix must not swallow real work).
# ---------------------------------------------------------------------------
note ""
note "=== Test 4: skill candidates --review (pending candidate) fails loud, still lists ==="

cat > "$CANDIDATES_FILE" <<'EOF'
## candidate: totally-real-skill (2026-09-01T00:00:00Z)
**Confidence**: 0.9
**Source evidence**
- Cycle 3
- Cycle 7
EOF

rc=0
out="$(bash "$SKILL_SH" candidates --review 2>&1)" || rc=$?

if [ "$rc" -ne 0 ]; then
    ok "test 4: rc=$rc (non-zero)"
else
    bad "test 4: expected non-zero rc, got 0 (silent no-op) — output: $out"
fi

case "$out" in
    *"totally-real-skill"*) ok "test 4: real candidate listing still printed" ;;
    *) bad "test 4: real listing missing — got: $out" ;;
esac

rm -f "$CANDIDATES_FILE"

# ---------------------------------------------------------------------------
# Test 5: skill candidates (no --review) → unaffected, rc == 0
# ---------------------------------------------------------------------------
note ""
note "=== Test 5: skill candidates (no --review) unaffected ==="

rc=0
out="$(bash "$SKILL_SH" candidates 2>&1)" || rc=$?

if [ "$rc" -eq 0 ]; then
    ok "test 5: rc=0 (plain listing unaffected)"
else
    bad "test 5: expected rc=0, got rc=$rc — output: $out"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
note ""
note "=== Summary ==="
printf '  %d passed, %d failed\n' "$pass" "$fail"
if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    printf '%b' "$fail_log"
    exit 1
fi
exit 0
