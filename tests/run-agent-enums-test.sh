#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-agent-enums-test.sh — K-112 (c)+(d) regression test for the agent
# frontmatter enum checks in `yakos validate` (bash: cli/lib/validate.sh
# check_agent_enums; Go: internal/validate checkAgentEnums).
#
#   runtime / runtime-fallback  must be a known runtime (gemini = removed, warn)
#   agent id                    must not be a runtime name (claude, codex, agy)
#   model-policy                must be a model tier (haiku|sonnet|opus|fable)
#   agent file                  must be one the Go dispatcher reads: no line of 1 MiB or
#                               more, at most 4 MiB, a symlink that resolves to a
#                               regular file inside the framework lib/ or the project,
#                               and an extends: that is a bare agent id (sec-324; the
#                               second fixture project below)
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
mk good-tier       $'runtime: claude\nmodel-policy: haiku\n'
mk bad-runtime     $'runtime: gemni\n'
mk warn-gemini     $'runtime: gemini # legacy\n'
mk bad-fallback    $'runtime-fallback: [codex, bard]\n'
mk bad-fallback-bl $'runtime-fallback:\n  - codex\n  - "nope"\n'
mk bad-policy      $'model-policy: pinned\n'
mk bad-policy2     $'model-policy: eval-driven\n'
mk claude          $'runtime: codex\n'

run_bash() { YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" "${BASH:-bash}" "$REPO_ROOT/cli/lib/validate.sh" "$@" 2>&1; }
run_go()   { YAKOS_IMPL=go "$GO_BINARY" validate "$@" 2>&1; }
norm()     { sed "s|$P|<P>|g" | grep -E '\[err\]|\[warn\]|Summary' | sort; }

want_err_f() { # <output> <fixed text of one [err] line> <label>
    printf '%s\n' "$1" | grep -F -- "$2" | grep -q '\[err\]' && ok "$3" || bad "$3 (no [err] line containing: $2)"
}
want_err() { # <output> <substr> <label>
    printf '%s' "$1" | grep -q -- "\[err\].*$2" && ok "$3" || bad "$3 (no [err] matching: $2)"
}
sides="bash"; [ -x "$GO_BINARY" ] && sides="bash go"
for side in $sides; do
    out="$(run_$side "$P" | norm)"
    want_err "$out" 'bad-runtime.md: runtime: "gemni" is not a known runtime'      "$side: unknown runtime rejected"
    want_err "$out" 'bad-fallback.md: runtime-fallback: "bard" is not a known'     "$side: unknown inline fallback rejected"
    want_err "$out" 'bad-fallback-bl.md: runtime-fallback: "nope" is not a known'  "$side: unknown block fallback rejected"
    want_err "$out" 'bad-policy.md: model-policy: pinned is not a model tier'      "$side: model-policy pinned rejected"
    want_err "$out" 'bad-policy2.md: model-policy: eval-driven is not a model tier' "$side: model-policy eval-driven rejected"
    printf '%s' "$out" | grep -q '\[warn\].*warn-gemini.md: runtime: gemini was removed; use agy' \
        && ok "$side: gemini is a removal warning" || bad "$side: gemini warning missing"
    want_err "$out" 'claude.md: agent id "claude" is a runtime name'             "$side: an agent named after a runtime rejected"
    if printf '%s' "$out" | grep -Eq 'good-(agy|tier)\.md: (runtime|runtime-fallback|model-policy):'; then bad "$side: valid agent flagged"; else ok "$side: valid agents clean"; fi
    printf '%s' "$out" > "$TMP/out-$side.txt"
done
if [ "$sides" = "bash go" ]; then
    if diff "$TMP/out-bash.txt" "$TMP/out-go.txt" >/dev/null; then ok "bash and Go findings identical"; else bad "bash/go findings differ:"; diff "$TMP/out-bash.txt" "$TMP/out-go.txt"; fi
fi

# ---- agent files the Go dispatcher skips (sec-324) ---------------------------
# Compose leaves out a file with a line of 1048576 bytes or more (a carriage return
# before the newline counts), a file over 4194304 bytes, anything that is not a
# regular file, and a symlink that does not end at a regular file inside the
# framework lib/ or the project. validate reports each as an error, and both twins
# print the same text. The bash twin measures lines with awk and cannot call the Go
# code, so the edges of the line bound are in the fixture: a line of the bound is
# refused, one byte less is not, with LF and CRLF and as the last line of a file.
Q="$TMP/proj2"; A="$Q/.claude/agents"; mkdir -p "$A" "$Q/shared" "$TMP/outside"
head5()    { printf -- '---\nid: %s\nrole: specialist\n---\n# %s\n' "$1" "$1"; }
longline() { head -c "$1" /dev/zero | tr '\0' 'a'; }
{ head5 good;       printf '%s\n' "$filler"; } > "$A/good.md"
{ head5 under;      longline 1048575; printf '\n';   printf '%s\n' "$filler"; } > "$A/under.md"
{ head5 bound;      longline 1048576; printf '\n';   printf '%s\n' "$filler"; } > "$A/bound.md"
{ head5 crlf-under; longline 1048574; printf '\r\n'; printf '%s\n' "$filler"; } > "$A/crlf-under.md"
{ head5 crlf;       longline 1048575; printf '\r\n'; printf '%s\n' "$filler"; } > "$A/crlf.md"
{ head5 tail-under; printf '%s\n' "$filler"; longline 1048575; } > "$A/tail-under.md"
{ head5 tail-bound; printf '%s\n' "$filler"; longline 1048576; } > "$A/tail-bound.md"
# 5 MiB in lines each one byte under the bound, so only the size cap can refuse it.
{ head5 big; printf '%s\n' "$filler"; for _i in 1 2 3 4 5; do longline 1048575; printf '\n'; done; } > "$A/big.md"
{ head5 shared; printf '%s\n' "$filler"; } > "$Q/shared/shared.md"
# extends: is a bare agent id. The value is shown as the dispatcher's warning shows
# it, so non-ASCII bytes and a long value are in the fixture on purpose. (A tab is
# not: PyYAML rejects one in a plain scalar and Go's parser does not, which is a
# difference of the frontmatter pass. The tab is in the Go validator's own test and
# in the composer parity test.)
head5x() { printf -- '---\nid: %s\nrole: specialist\nextends: %s\n---\n# %s\n' "$1" "$2" "$1"; }
for ext in 'ext-up|../outside' 'ext-abs|/etc/passwd' 'ext-sub|sub/dir' 'ext-dot|.hidden' $'ext-utf|caf\xc3\xa9' "ext-long|$(longline 80)/x" 'ext-ok|backend' 'ext-ok2|a_b.c-d'; do
    { head5x "${ext%%|*}" "${ext#*|}"; printf '%s\n' "$filler"; } > "$A/${ext%%|*}.md"
done
{ head5 outside; printf '%s\n' "$filler"; } > "$TMP/outside/outside.md"
links=1
ln -s ../../shared/shared.md "$A/inproject.md"            2>/dev/null || links=0   # inside the project: accepted
ln -s "$REPO_ROOT/lib/agents/architect.md" "$A/inlib.md" 2>/dev/null || links=0    # the installed layout: accepted
ln -s ../../nowhere.md "$A/ghost.md"                      2>/dev/null || links=0   # dangling
ln -s "$Q/shared" "$A/dirlink.md"                         2>/dev/null || links=0   # a directory
ln -s "$TMP/outside/outside.md" "$A/leak.md"              2>/dev/null || links=0   # outside both roots
if mkfifo "$Q/pipe" 2>/dev/null; then ln -s "$Q/pipe" "$A/pipelink.md" 2>/dev/null || links=0; else links=0; fi

norm2() { sed "s|$Q|<Q>|g" | grep -E '\[err\]|\[warn\]|Summary' | sort; }
run_bash2() { YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" "${BASH:-bash}" "$REPO_ROOT/cli/lib/validate.sh" "$@" 2>&1; }
run_go2()   { YAKOS_ROOT="$REPO_ROOT" YAKOS_IMPL=go "$GO_BINARY" validate "$@" 2>&1; }
SKIP='the Go dispatcher skips it'
for side in $sides; do
    out="$(run_${side}2 "$Q" | norm2)"
    want_err "$out" "agents/bound.md: line 6 is longer than 1048576 bytes; $SKIP; split the line"       "$side: a line of the bound is rejected"
    want_err "$out" "agents/crlf.md: line 6 is longer than 1048576 bytes; $SKIP; split the line"        "$side: a CRLF line of the bound is rejected"
    want_err "$out" "agents/tail-bound.md: line 96 is longer than 1048576 bytes; $SKIP; split the line" "$side: a last line of the bound is rejected"
    want_err "$out" "agents/big.md: file is larger than 4194304 bytes; $SKIP"                           "$side: a file over the size cap is rejected"
    if [ "$links" = 1 ]; then
        want_err "$out" "agents/ghost.md: symlink does not resolve to a regular file; $SKIP"    "$side: a dangling symlink is rejected"
        want_err "$out" "agents/dirlink.md: symlink does not resolve to a regular file; $SKIP"  "$side: a symlink to a directory is rejected"
        want_err "$out" "agents/pipelink.md: symlink does not resolve to a regular file; $SKIP" "$side: a symlink to a FIFO is rejected"
        want_err "$out" "agents/leak.md: symlink resolves outside the framework lib/ and the project directory; $SKIP" "$side: a symlink outside the roots is rejected"
        want_n=14
    else
        want_n=10   # no symlinks or FIFOs here (the file system refused them)
    fi
    EXTRULE='is not a bare agent id (1 to 128 of A-Z a-z 0-9 . _ -, starting with a letter or digit, no ".."); '"$SKIP"
    want_err_f "$out" "agents/ext-up.md: extends value \"../outside\" $EXTRULE"   "$side: extends with .. is rejected"
    want_err_f "$out" "agents/ext-abs.md: extends value \"/etc/passwd\" $EXTRULE"  "$side: an absolute extends is rejected"
    want_err_f "$out" "agents/ext-sub.md: extends value \"sub/dir\" $EXTRULE"      "$side: extends with a slash is rejected"
    want_err_f "$out" "agents/ext-dot.md: extends value \".hidden\" $EXTRULE"      "$side: a leading dot is rejected"
    want_err_f "$out" "agents/ext-utf.md: extends value \"caf??\" $EXTRULE"        "$side: non-ASCII bytes are shown as ?, one each"
    want_err_f "$out" "agents/ext-long.md: extends value \"$(longline 64)...\" $EXTRULE" "$side: a long value is cut at 64 bytes"
    if printf '%s' "$out" | grep -Eq "agents/(good|under|crlf-under|tail-under|inproject|inlib|ext-ok|ext-ok2)\.md"; then
        bad "$side: an agent file the dispatcher reads was flagged"; printf '%s\n' "$out" | grep -E 'agents/(good|under|crlf-under|tail-under|inproject|inlib|ext-ok|ext-ok2)\.md' | head -3
    else
        ok "$side: lines under the bound, links inside the project and the framework lib, and a good file are clean"
    fi
    printf '%s' "$out" | grep -q "Summary: $want_n error(s), 0 warning(s)" && ok "$side: exactly $want_n errors for the agent-file fixture" \
        || bad "$side: wrong error count for the agent-file fixture: $(printf '%s' "$out" | grep Summary)"
    printf '%s' "$out" > "$TMP/out2-$side.txt"
done
if [ "$sides" = "bash go" ]; then
    if diff "$TMP/out2-bash.txt" "$TMP/out2-go.txt" >/dev/null; then ok "bash and Go agent-file findings identical"; else bad "bash/go agent-file findings differ:"; diff "$TMP/out2-bash.txt" "$TMP/out2-go.txt"; fi
fi

# The shipped framework passes strict on both sides.
for side in $sides; do
    out="$(cd "$REPO_ROOT" && run_$side --strict)"
    if printf '%s' "$out" | grep -q 'Summary: 0 error(s), 0 warning(s)'; then ok "$side: framework validate --strict clean"; else bad "$side: framework validate --strict not clean"; printf '%s\n' "$out" | grep -E '\[err\]|Summary' | head; fi
done

printf '\nagent-enums: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
