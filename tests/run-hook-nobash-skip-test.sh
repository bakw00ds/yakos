#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-nobash-skip-test.sh — K-110: the hook fixture runners must SKIP
# (exit 0, say so) rather than fail when there is no bash on PATH, as on a
# Windows runner without Git-bash. Each runner is started by /bin/sh with a
# PATH that holds no bash, the way such a runner would meet it.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-nobash-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM
EMPTY="$TMP/empty-path"; mkdir -p "$EMPTY"

for script in run-hook-fixtures.sh run-hook-parity.sh run-hook-hardening-test.sh; do
    rc=0
    out="$(cd "$TMP" && env -i PATH="$EMPTY" /bin/sh "$REPO_ROOT/tests/$script" 2>&1)" || rc=$?
    if [ "$rc" = 0 ] && printf '%s' "$out" | grep -q '^SKIP: '"$script"': no bash on PATH'; then
        ok "$script: no bash on PATH -> SKIP, exit 0"
    else
        bad "$script: rc=$rc out=[$out]"
    fi
    # The same runner honours YAKOS_HOOK_BASH: a missing interpreter also skips.
    rc=0
    out="$(cd "$TMP" && env YAKOS_HOOK_BASH="$TMP/no-such-bash" /bin/sh "$REPO_ROOT/tests/$script" 2>&1)" || rc=$?
    if [ "$rc" = 0 ] && printf '%s' "$out" | grep -q '^SKIP: '; then
        ok "$script: YAKOS_HOOK_BASH missing -> SKIP, exit 0"
    else
        bad "$script: YAKOS_HOOK_BASH missing rc=$rc out=[$out]"
    fi
done

echo
echo "hook-nobash-skip: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
