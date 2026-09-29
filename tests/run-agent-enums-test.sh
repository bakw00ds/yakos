#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-agent-enums-test.sh — K-112 (c) regression test for the agent
# frontmatter enum checks in `yakos validate` (bash: cli/lib/validate.sh
# check_agent_enums; Go: internal/validate checkAgentEnums).
#
#   runtime / runtime-fallback  must be a known runtime (gemini = deprecated warn)
#
# Asserts on both implementations (Go half only when bin/yakos exists), that
# their findings are byte-identical, and that the shipped framework agents pass
# `validate --strict`. Run under both `bash` and `/bin/bash`.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
unset YAKOS_ROOT YAKOS_LIB
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-agent-enums-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM
export HOME="$TMP/home"; mkdir -p "$HOME"

filler="$(awk 'BEGIN { for (i = 0; i < 90; i++) print "filler" }')"
P="$TMP/proj"; mkdir -p "$P/.claude/agents"
mk() { printf -- '---\nid: %s\nrole: specialist\n%s---\n# X\n%s\n' "$1" "$2" "$filler" > "$P/.claude/agents/$1.md"; }
mk good-agy        $'runtime: agy\n'
mk good-tier       $'runtime: claude\n'
mk bad-runtime     $'runtime: gemni\n'
mk warn-gemini     $'runtime: gemini # legacy\n'
mk bad-fallback    $'runtime-fallback: [codex, bard]\n'
mk bad-fallback-bl $'runtime-fallback:\n  - codex\n  - "nope"\n'

run_bash() { YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" "${BASH:-bash}" "$REPO_ROOT/cli/lib/validate.sh" "$@" 2>&1; }
run_go()   { YAKOS_IMPL=go "$GO_BINARY" validate "$@" 2>&1; }
norm()     { sed "s|$P|<P>|g" | grep -E '\[err\]|\[warn\]|Summary' | sort; }

want_err() { # <output> <substr> <label>
    printf '%s' "$1" | grep -q -- "\[err\].*$2" && ok "$3" || bad "$3 (no [err] matching: $2)"
}
sides="bash"; [ -x "$GO_BINARY" ] && sides="bash go"
for side in $sides; do
    out="$(run_$side "$P" | norm)"
    want_err "$out" 'bad-runtime.md: runtime: "gemni" is not a known runtime'      "$side: unknown runtime rejected"
    want_err "$out" 'bad-fallback.md: runtime-fallback: "bard" is not a known'     "$side: unknown inline fallback rejected"
    want_err "$out" 'bad-fallback-bl.md: runtime-fallback: "nope" is not a known'  "$side: unknown block fallback rejected"
    printf '%s' "$out" | grep -q '\[warn\].*warn-gemini.md: runtime: gemini is a deprecated shim' \
        && ok "$side: gemini is a deprecation warning" || bad "$side: gemini warning missing"
    if printf '%s' "$out" | grep -Eq 'good-(agy|tier)\.md: (runtime|runtime-fallback|model-policy):'; then bad "$side: valid agent flagged"; else ok "$side: valid agents clean"; fi
    printf '%s' "$out" > "$TMP/out-$side.txt"
done
if [ "$sides" = "bash go" ]; then
    if diff "$TMP/out-bash.txt" "$TMP/out-go.txt" >/dev/null; then ok "bash and Go findings identical"; else bad "bash/go findings differ:"; diff "$TMP/out-bash.txt" "$TMP/out-go.txt"; fi
fi

# The shipped framework passes strict on both sides.
for side in $sides; do
    out="$(cd "$REPO_ROOT" && run_$side --strict)"; rc=$?
    if printf '%s' "$out" | grep -q 'Summary: 0 error(s), 0 warning(s)'; then ok "$side: framework validate --strict clean"; else bad "$side: framework validate --strict not clean"; printf '%s\n' "$out" | grep -E '\[err\]|Summary' | head; fi
done

printf '\nagent-enums: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
