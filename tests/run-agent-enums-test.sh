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
#                               regular file inside the framework lib/agents or the
#                               project's .claude/agents (not the project's .env or
#                               .git/config), and an extends: that is a bare agent id
#                               (sec-324; the second fixture project below)
#   agent/skill directory       a project's .claude/agents or .claude/skills that is a
#                               symlink, or sits under a symlinked .claude, is refused,
#                               wherever it leads; the framework's own directories are
#                               never refused (rev-324)
#   FIFO among the files        a FIFO in the agents, rules or skills directory must not
#                               hang validate: bash's playbook pass read every file
#                               with `grep -r`, which blocks on a pipe for good
#
# Asserts on both implementations (Go half only when bin/yakos exists), that
# their findings are byte-identical, and that the shipped framework agents pass
# `validate --strict`. Run under both `bash` and `/bin/bash`. CI sets
# YAKOS_REQUIRE_GO_BINARY=1, so a missing bin/yakos fails the run instead of
# quietly checking only the bash half.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
unset YAKOS_ROOT YAKOS_LIB
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

# Every validator run below has a time limit: a pass that blocks (bash's playbook
# pass once read a FIFO with grep -r and never returned) must fail the test, not
# hang CI until the job times out.
# limited <seconds> <file> <command...> runs a command with a time limit, its output
# in <file>, and returns 124 on a timeout. There is no timeout(1) on macOS. The
# command gets its own process group (set -m), so the kill takes a stuck grep with it.
limited() {
    local secs="$1" out="$2" pid ticks=0
    shift 2
    set -m
    "$@" < /dev/null > "$out" 2>&1 &
    pid=$!
    set +m
    while kill -0 "$pid" 2>/dev/null; do
        ticks=$((ticks + 1))
        if [ "$ticks" -gt $((secs * 5)) ]; then
            { kill -9 -- "-$pid"; kill -9 "$pid"; wait "$pid"; } 2>/dev/null
            return 124
        fi
        sleep 0.2
    done
    wait "$pid"
}
# vlim <seconds> <file> <command...>: limited, where only a timeout is a failure (a
# validate that finds errors exits 1, and that is what most fixtures want).
vlim() {
    local secs="$1" out="$2" rc=0
    shift 2
    limited "$secs" "$out" "$@" || rc=$?
    if [ "$rc" -eq 124 ]; then bad "$1 $2: did not finish in $secs seconds"; fi
    return 0
}

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
if [ "$sides" = "bash" ] && [ "${YAKOS_REQUIRE_GO_BINARY:-}" = 1 ]; then
    printf 'agent-enums: YAKOS_REQUIRE_GO_BINARY=1 but %s is not executable; run make build\n' "$GO_BINARY" >&2
    exit 1
fi
for side in $sides; do
    vlim 60 "$TMP/p.out" "run_$side" "$P"
    out="$(norm < "$TMP/p.out")"
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
# framework lib/agents or the project's .claude/agents. validate reports each as an error, and both twins
# print the same text. The bash twin measures lines with awk and cannot call the Go
# code, so the edges of the line bound are in the fixture: a line of the bound is
# refused, one byte less is not, with LF and CRLF and as the last line of a file.
Q="$TMP/proj2"; A="$Q/.claude/agents"; mkdir -p "$A/sub" "$Q/shared" "$Q/.git" "$TMP/outside"
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
{ head5 shared; printf '%s\n' "$filler"; } > "$Q/shared/shared.md"      # in the project, not in .claude/agents
{ head5 inner;  printf '%s\n' "$filler"; } > "$A/sub/shared.md"          # inside the agent directory
printf 'OPENAI_API_KEY=sk-TOPSECRET-1234\n' > "$Q/.env"
printf '[remote "origin"]\n\turl = https://user:TOKEN-9999@example.com/x.git\n' > "$Q/.git/config"
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
ln -s sub/shared.md "$A/inproject.md"                     2>/dev/null || links=0   # inside .claude/agents: accepted
ln -s ../../shared/shared.md "$A/otherfile.md"            2>/dev/null || links=0   # a project file that is not an agent
ln -s ../../.env "$A/dotenv.md"                           2>/dev/null || links=0   # the project's .env
ln -s ../../.git/config "$A/gitcfg.md"                    2>/dev/null || links=0   # the project's .git/config
ln -s "$REPO_ROOT/lib/rules/INDEX.md" "$A/libother.md"    2>/dev/null || links=0   # in the framework's lib/, not in lib/agents
ln -s "$REPO_ROOT/lib/agents/architect.md" "$A/inlib.md" 2>/dev/null || links=0    # the installed layout: accepted
ln -s ../../nowhere.md "$A/ghost.md"                      2>/dev/null || links=0   # dangling
ln -s "$Q/shared" "$A/dirlink.md"                         2>/dev/null || links=0   # a directory
ln -s "$TMP/outside/outside.md" "$A/leak.md"              2>/dev/null || links=0   # outside both roots
if mkfifo "$Q/pipe" 2>/dev/null; then ln -s "$Q/pipe" "$A/pipelink.md" 2>/dev/null || links=0; else links=0; fi

norm2() { sed "s|$Q|<Q>|g" | grep -E '\[err\]|\[warn\]|Summary' | sort; }
run_bash2() { YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$REPO_ROOT/cli/lib" "${BASH:-bash}" "$REPO_ROOT/cli/lib/validate.sh" "$@" 2>&1; }
run_go2()   { YAKOS_ROOT="$REPO_ROOT" YAKOS_IMPL=go "$GO_BINARY" validate "$@" 2>&1; }
SKIP='the Go dispatcher skips it'
OUTSIDE="symlink resolves outside the framework lib/agents and the project's .claude/agents; $SKIP"
for side in $sides; do
    vlim 60 "$TMP/q.out" "run_${side}2" "$Q"
    out="$(norm2 < "$TMP/q.out")"
    want_err "$out" "agents/bound.md: line 6 is longer than 1048576 bytes; $SKIP; split the line"       "$side: a line of the bound is rejected"
    want_err "$out" "agents/crlf.md: line 6 is longer than 1048576 bytes; $SKIP; split the line"        "$side: a CRLF line of the bound is rejected"
    want_err "$out" "agents/tail-bound.md: line 96 is longer than 1048576 bytes; $SKIP; split the line" "$side: a last line of the bound is rejected"
    want_err "$out" "agents/big.md: file is larger than 4194304 bytes; $SKIP"                           "$side: a file over the size cap is rejected"
    if [ "$links" = 1 ]; then
        want_err "$out" "agents/ghost.md: symlink does not resolve to a regular file; $SKIP"    "$side: a dangling symlink is rejected"
        want_err "$out" "agents/dirlink.md: symlink does not resolve to a regular file; $SKIP"  "$side: a symlink to a directory is rejected"
        want_err "$out" "agents/pipelink.md: symlink does not resolve to a regular file; $SKIP" "$side: a symlink to a FIFO is rejected"
        want_err "$out" "agents/leak.md: $OUTSIDE"      "$side: a symlink outside the roots is rejected"
        want_err "$out" "agents/otherfile.md: $OUTSIDE" "$side: a link to another file of the project is rejected"
        want_err "$out" "agents/dotenv.md: $OUTSIDE"    "$side: a link to the project's .env is rejected"
        want_err "$out" "agents/gitcfg.md: $OUTSIDE"    "$side: a link to the project's .git/config is rejected"
        want_err "$out" "agents/libother.md: $OUTSIDE"  "$side: a link to a framework file outside lib/agents is rejected"
        want_n=18
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
        ok "$side: lines under the bound, links inside the agent directories, and a good file are clean"
    fi
    if printf '%s' "$out" | grep -q 'TOPSECRET\|TOKEN-9999'; then bad "$side: text of the project's own files was printed"; else ok "$side: nothing from the project's .env or .git/config is printed"; fi
    printf '%s' "$out" | grep -q "Summary: $want_n error(s), 0 warning(s)" && ok "$side: exactly $want_n errors for the agent-file fixture" \
        || bad "$side: wrong error count for the agent-file fixture: $(printf '%s' "$out" | grep Summary)"
    printf '%s' "$out" > "$TMP/out2-$side.txt"
done
if [ "$sides" = "bash go" ]; then
    if diff "$TMP/out2-bash.txt" "$TMP/out2-go.txt" >/dev/null; then ok "bash and Go agent-file findings identical"; else bad "bash/go agent-file findings differ:"; diff "$TMP/out2-bash.txt" "$TMP/out2-go.txt"; fi
fi

# ---- project agent and skill directories that are links (rev-324) -----------
# A file seen through a linked directory is a regular file and never reaches the
# rule for symlinked files, so the directory is refused itself: `.claude/agents` or
# `.claude/skills` that is a symlink, or sits under a `.claude` that is one, is
# skipped whole by the dispatcher, wherever it leads (outside the project, into it,
# to nothing, to a file), and validate says so, once, with the one text the
# composers print. Neither twin walks into a linked agents or skills directory
# (find and filepath.WalkDir both stop at a link given as the starting point); a
# linked `.claude` is walked, and is where a pass would read what was refused if it
# were not told to skip it. The framework's own directories are never refused: a
# bare install or a re-pointed upgrade may leave lib/agents, lib/skills or the root
# itself as a link.
#
# vrun <side> <project>: validate the project; VOUT is the findings, with the project
# path as <D>, and VHUNG is 1 when the run did not finish in 30 seconds.
vrun() {
    local rc=0
    VHUNG=0
    limited 30 "$TMP/vrun.out" "run_${1}2" "$2" || rc=$?
    [ "$rc" -eq 124 ] && VHUNG=1
    VOUT="$(sed "s|$2|<D>|g" "$TMP/vrun.out" | grep -E '\[err\]|\[warn\]|Summary' | sort)"
}
dirs_ok=1
lnk() { ln -s "$1" "$2" 2>/dev/null || dirs_ok=0; }
agent_md() { # <dir> <id> [extra frontmatter]
    { printf -- '---\nid: %s\nrole: specialist\n%s---\n# %s\n' "$2" "${3:-}" "$2"; printf '%s\n' "$filler"; } > "$1/$2.md"
}
skill_md() { # <dir> <name>
    mkdir -p "$1/$2"
    { printf -- '---\nname: %s\ndescription: a test skill\n---\n# %s\n' "$2" "$2"; printf '%s\n' "$filler"; } > "$1/$2/SKILL.md"
}
rule_md() { # <dir>: one good rule
    mkdir -p "$1/rules"
    { printf -- '---\nname: note\n---\n# note\n'; awk 'BEGIN { for (i = 0; i < 70; i++) print "filler" }'; } > "$1/rules/note.md"
}
DIRLINK="a symlinked directory is not followed (this directory or .claude is a symlink); $SKIP"

# outside_tree <dir>: an agent and a skill that every pass reading them has something
# to say about: a runtime that is not one, a short file (the line budget), a playbook
# reference, and a model-policy with no eval/ directory. One good rule is there too:
# with agents and skills both refused it is all that is left to validate, and
# validate stops early when there is nothing, which would hide whether the passes
# after that point read what was refused.
outside_tree() {
    mkdir -p "$1/agents" "$1/skills/evil"
    rule_md "$1"
    printf -- '---\nid: evil\nrole: specialist\nruntime: gemni\nmodel-policy: haiku\n---\n# evil\n- playbook:evil-agent-ref\n' > "$1/agents/evil.md"
    printf -- '---\nname: evil\ndescription: a test skill\n---\n# evil\n- playbook:evil-skill-ref\n' > "$1/skills/evil/SKILL.md"
}
# D1: both directories are links to directories outside the project
D1="$TMP/d1"; mkdir -p "$D1/.claude"; outside_tree "$TMP/out1"
lnk "$TMP/out1/agents" "$D1/.claude/agents"; lnk "$TMP/out1/skills" "$D1/.claude/skills"
# D2: `.claude` itself is a link to a directory outside the project, so the two
# directories are real ones that a pass would walk if it were not told to skip them
D2="$TMP/d2"; mkdir -p "$D2"; outside_tree "$TMP/out2"
lnk "$TMP/out2" "$D2/.claude"
# D3: both are links to directories inside the project: refused like the outside ones
D3="$TMP/d3"; mkdir -p "$D3/.claude"; outside_tree "$D3/shared"
lnk ../shared/agents "$D3/.claude/agents"; lnk ../shared/skills "$D3/.claude/skills"
# D4: `.claude` is a link to a directory inside the project
D4="$TMP/d4"; mkdir -p "$D4"; outside_tree "$D4/dotclaude"
lnk dotclaude "$D4/.claude"
# D5: links to nothing, and to a file
D5="$TMP/d5"; mkdir -p "$D5/.claude"; printf 'not a directory\n' > "$D5/notes.txt"
lnk "$D5/does-not-exist" "$D5/.claude/agents"; lnk "$D5/notes.txt" "$D5/.claude/skills"
# D6: a linked `.claude` that has an agents directory and no skills directory is
# refused for the agents directory only
D6="$TMP/d6"; mkdir -p "$D6/dotclaude/agents"; agent_md "$D6/dotclaude/agents" good; rule_md "$D6/dotclaude"; lnk dotclaude "$D6/.claude"
# D7: a linked `.claude` that holds neither has nothing to refuse
D7="$TMP/d7"; mkdir -p "$D7"; rule_md "$D7/dotclaude"; lnk dotclaude "$D7/.claude"

if [ "$dirs_ok" = 1 ]; then
    for side in $sides; do
        vrun "$side" "$D1"; o1="$VOUT"; h1=$VHUNG; printf '%s' "$VOUT" > "$TMP/d1-$side.txt"
        want_err_f "$o1" "<D>/.claude/agents: $DIRLINK" "$side: an agents directory linked outside the project is rejected"
        want_err_f "$o1" "<D>/.claude/skills: $DIRLINK" "$side: a skills directory linked outside the project is rejected"
        printf '%s' "$o1" | grep -q 'Summary: 2 error(s), 0 warning(s)' && ok "$side: the two linked directories are the only findings" || bad "$side: wrong findings for the outside links: $o1"
        vrun "$side" "$D2"; o2="$VOUT"; h2=$VHUNG; printf '%s' "$VOUT" > "$TMP/d2-$side.txt"
        want_err_f "$o2" "<D>/.claude/agents: $DIRLINK" "$side: agents under a .claude linked outside the project is rejected"
        want_err_f "$o2" "<D>/.claude/skills: $DIRLINK" "$side: skills under a .claude linked outside the project is rejected"
        if printf '%s' "$o2" | grep -q 'evil'; then bad "$side: a file under the rejected .claude was read: $o2"; else ok "$side: no pass reads a file under the rejected .claude"; fi
        printf '%s' "$o2" | grep -q 'Summary: 2 error(s), 0 warning(s)' && ok "$side: the two rejected directories are the only findings" || bad "$side: wrong findings for a .claude linked outside: $o2"
        grep -qF 'agents: 0 | skills: 0 | rules: 1' "$TMP/vrun.out" && ok "$side: the rejected directories are not counted" || bad "$side: the rejected directories were counted: $(grep -F 'agents:' "$TMP/vrun.out")"
        vrun "$side" "$D3"; o3="$VOUT"; h3=$VHUNG; printf '%s' "$VOUT" > "$TMP/d3-$side.txt"
        want_err_f "$o3" "<D>/.claude/agents: $DIRLINK" "$side: an agents directory linked to a directory of the project is rejected too"
        want_err_f "$o3" "<D>/.claude/skills: $DIRLINK" "$side: a skills directory linked to a directory of the project is rejected too"
        printf '%s' "$o3" | grep -q 'Summary: 2 error(s), 0 warning(s)' && ok "$side: the two links into the project are the only findings" || bad "$side: wrong findings for links into the project: $o3"
        vrun "$side" "$D4"; o4="$VOUT"; h4=$VHUNG; printf '%s' "$VOUT" > "$TMP/d4-$side.txt"
        want_err_f "$o4" "<D>/.claude/agents: $DIRLINK" "$side: agents under a .claude linked into the project is rejected"
        want_err_f "$o4" "<D>/.claude/skills: $DIRLINK" "$side: skills under a .claude linked into the project is rejected"
        if printf '%s' "$o4" | grep -q 'evil'; then bad "$side: a file under the rejected .claude was read: $o4"; else ok "$side: no pass reads a file under a .claude linked into the project"; fi
        vrun "$side" "$D5"; o5="$VOUT"; h5=$VHUNG; printf '%s' "$VOUT" > "$TMP/d5-$side.txt"
        want_err_f "$o5" "<D>/.claude/agents: $DIRLINK" "$side: an agents directory linked to nothing is rejected"
        want_err_f "$o5" "<D>/.claude/skills: $DIRLINK" "$side: a skills directory linked to a file is rejected"
        vrun "$side" "$D6"; o6="$VOUT"; h6=$VHUNG; printf '%s' "$VOUT" > "$TMP/d6-$side.txt"
        want_err_f "$o6" "<D>/.claude/agents: $DIRLINK" "$side: the agents directory under a linked .claude is rejected"
        printf '%s' "$o6" | grep -q 'Summary: 1 error(s), 0 warning(s)' && ok "$side: a linked .claude without a skills directory is silent about skills" || bad "$side: wrong findings for a .claude with no skills directory: $o6"
        vrun "$side" "$D7"; o7="$VOUT"; h7=$VHUNG; printf '%s' "$VOUT" > "$TMP/d7-$side.txt"
        printf '%s' "$o7" | grep -q 'Summary: 0 error(s), 0 warning(s)' && ok "$side: a linked .claude with neither directory has nothing to refuse" || bad "$side: wrong findings for a linked .claude holding no agents or skills: $o7"
        if [ "$h1$h2$h3$h4$h5$h6$h7" = 0000000 ]; then ok "$side: every directory fixture finished"; else bad "$side: a directory fixture did not finish in 30 seconds"; fi
    done
    # A relative project path with CDPATH set: bash's `cd` then prints the directory
    # it enters, which put a second line into every path the bash twin resolved with
    # `cd -P`, so a link inside an agent directory was refused for the wrong reason.
    # The Go twin never looked at CDPATH. Run from $TMP, with the project named
    # relatively. A directory that is a link is refused without resolving anything,
    # and must stay refused for a relative path too.
    CA="$TMP/cdp-a"; mkdir -p "$CA/.claude"; outside_tree "$CA/shared"
    lnk ../shared/agents "$CA/.claude/agents"; lnk ../shared/skills "$CA/.claude/skills"
    CB="$TMP/cdp-b"; mkdir -p "$CB/.claude/agents/sub"
    agent_md "$CB/.claude/agents" good; agent_md "$CB/.claude/agents/sub" shared
    printf 'OPENAI_API_KEY=sk-TOPSECRET-1234\n' > "$CB/.env"
    lnk sub/shared.md "$CB/.claude/agents/inproject.md"; lnk ../../.env "$CB/.claude/agents/dotenv.md"
    CC="$TMP/cdp-c"; mkdir -p "$CC/.claude"; outside_tree "$TMP/out-cdp"
    lnk "$TMP/out-cdp/agents" "$CC/.claude/agents"; lnk "$TMP/out-cdp/skills" "$CC/.claude/skills"
    cdrun() { # <side> <project relative to $TMP>
        local rc=0
        VHUNG=0
        ( cd "$TMP" && CDPATH=".:/" limited 30 "$TMP/vrun.out" "run_${1}2" "$2" ) || rc=$?
        [ "$rc" -eq 124 ] && VHUNG=1
        VOUT="$(sed "s|$2|<D>|g" "$TMP/vrun.out" | grep -E '\[err\]|\[warn\]|Summary' | sort)"
    }
    for side in $sides; do
        cdrun "$side" cdp-a; oa="$VOUT"; ha=$VHUNG; printf '%s' "$VOUT" > "$TMP/cdp-a-$side.txt"
        want_err_f "$oa" "<D>/.claude/agents: $DIRLINK" "$side: an agents directory linked into the project is refused for a relative path with CDPATH set"
        want_err_f "$oa" "<D>/.claude/skills: $DIRLINK" "$side: a skills directory linked into the project is refused for a relative path with CDPATH set"
        cdrun "$side" cdp-b; ob="$VOUT"; hb=$VHUNG; printf '%s' "$VOUT" > "$TMP/cdp-b-$side.txt"
        want_err_f "$ob" "<D>/.claude/agents/dotenv.md: $OUTSIDE" "$side: a link to .env is refused, for the right reason, for a relative path with CDPATH set"
        printf '%s' "$ob" | grep -q 'Summary: 1 error(s), 0 warning(s)' && ok "$side: a link inside the agent directory is accepted for a relative path with CDPATH set" || bad "$side: CDPATH changed the findings for links inside the agent directory: $ob"
        cdrun "$side" cdp-c; oc="$VOUT"; hc=$VHUNG; printf '%s' "$VOUT" > "$TMP/cdp-c-$side.txt"
        want_err_f "$oc" "<D>/.claude/agents: $DIRLINK" "$side: an agents directory linked outside the project is refused for a relative path with CDPATH set"
        want_err_f "$oc" "<D>/.claude/skills: $DIRLINK" "$side: a skills directory linked outside the project is refused for a relative path with CDPATH set"
        if [ "$ha$hb$hc" = 000 ]; then ok "$side: the CDPATH fixtures finished"; else bad "$side: a CDPATH fixture did not finish in 30 seconds"; fi
    done
    if [ "$sides" = "bash go" ]; then
        for d in cdp-a cdp-b cdp-c; do
            if diff "$TMP/$d-bash.txt" "$TMP/$d-go.txt" >/dev/null; then ok "bash and Go findings identical for $d"; else bad "bash/go findings differ for $d:"; diff "$TMP/$d-bash.txt" "$TMP/$d-go.txt"; fi
        done
    fi
    # The framework's own directories are never subject to the rule, even as links:
    # framework mode (no project path) does not apply it to a linked lib/agents or
    # lib/skills, nor to a root that is reached through a link.
    FW="$TMP/fw"; mkdir -p "$FW/lib/rules" "$TMP/fw-elsewhere/agents"
    agent_md "$TMP/fw-elsewhere/agents" real; skill_md "$TMP/fw-elsewhere/skills" real
    { printf -- '---\nname: r\n---\n# r\n'; awk 'BEGIN { for (i = 0; i < 70; i++) print "filler" }'; } > "$FW/lib/rules/r.md"
    lnk "$TMP/fw-elsewhere/agents" "$FW/lib/agents"; lnk "$TMP/fw-elsewhere/skills" "$FW/lib/skills"
    lnk "$FW" "$TMP/fw-link"
    for side in $sides; do
        for fwroot in "$FW" "$TMP/fw-link"; do
            case "$side" in
                bash) YAKOS_ROOT="$fwroot" YAKOS_LIB="$REPO_ROOT/cli/lib" "${BASH:-bash}" "$REPO_ROOT/cli/lib/validate.sh" > "$TMP/fw-$side.out" 2>&1 < /dev/null ;;
                go)   YAKOS_ROOT="$fwroot" YAKOS_IMPL=go "$GO_BINARY" validate > "$TMP/fw-$side.out" 2>&1 < /dev/null ;;
            esac
            if grep -Fq 'a symlinked directory is not followed' "$TMP/fw-$side.out"; then
                bad "$side: the directory rule was applied in framework mode (root $(basename "$fwroot")): $(grep -F 'not followed' "$TMP/fw-$side.out" | head -2)"
            else
                ok "$side: framework mode does not apply the directory rule to a linked lib/agents and lib/skills (root $(basename "$fwroot"))"
            fi
        done
    done
    # A symlinked SKILL.md that ComposeSkills refuses is read by no pass in either
    # twin: the bash passes only ever read regular files, and the Go passes skip a
    # link the dispatcher refuses. The target would draw a finding from the
    # frontmatter, line-budget and playbook passes if it were read.
    SK="$TMP/sk"; mkdir -p "$SK/.claude/skills/ok" "$SK/.claude/skills/leak" "$TMP/out-sk"
    skill_md "$SK/.claude/skills" ok
    printf -- '---\nname: [unclosed\n---\n\n- playbook:evil-skill-ref\n' > "$TMP/out-sk/evil-skill.md"
    lnk "$TMP/out-sk/evil-skill.md" "$SK/.claude/skills/leak/SKILL.md"
    for side in $sides; do
        vrun "$side" "$SK"; printf '%s' "$VOUT" > "$TMP/sk-$side.txt"
        if printf '%s' "$VOUT" | grep -q 'evil'; then bad "$side: a pass read through a refused SKILL.md link: $VOUT"; else ok "$side: no pass reads through a SKILL.md link the dispatcher refuses"; fi
        printf '%s' "$VOUT" | grep -q 'Summary: 0 error(s), 0 warning(s)' && ok "$side: a refused SKILL.md link adds no finding" || bad "$side: wrong findings with a refused SKILL.md link: $VOUT"
    done
    if [ "$sides" = "bash go" ]; then
        if diff "$TMP/sk-bash.txt" "$TMP/sk-go.txt" >/dev/null; then ok "bash and Go findings identical for sk"; else bad "bash/go findings differ for sk:"; diff "$TMP/sk-bash.txt" "$TMP/sk-go.txt"; fi
    fi
    if [ "$sides" = "bash go" ]; then
        for d in d1 d2 d3 d4 d5 d6 d7; do
            if diff "$TMP/$d-bash.txt" "$TMP/$d-go.txt" >/dev/null; then ok "bash and Go findings identical for $d"; else bad "bash/go findings differ for $d:"; diff "$TMP/$d-bash.txt" "$TMP/$d-go.txt"; fi
        done
    fi
else
    printf '  SKIP the directory fixtures: this file system refused symlinks\n'
fi

# ---- a FIFO among the files must not hang validate (rev-324) -------------------
# bash's playbook-reference pass read every file under agents, rules and skills with
# `grep -r`, which opens a FIFO and waits for a writer that never comes. The Go
# validator returned at once. Now the pass reads regular files only, and the run
# finishes with the same findings on both sides: the FIFO that is an agent file is
# an error, the others are ignored. Each FIFO is named differently, since grep -r
# reads any name.
F="$TMP/fifo"; mkdir -p "$F/.claude/agents" "$F/.claude/rules" "$F/.claude/skills/x"
agent_md "$F/.claude/agents" ok
fifos=1
mkfifo "$F/.claude/agents/pipe.md" "$F/.claude/agents/notes.txt" "$F/.claude/rules/pipe.md" "$F/.claude/skills/x/SKILL.md" "$F/.claude/skills/x/NOTES" 2>/dev/null || fifos=0
if [ "$fifos" = 1 ]; then
    for side in $sides; do
        vrun "$side" "$F"; printf '%s' "$VOUT" > "$TMP/fifo-$side.txt"
        if [ "$VHUNG" = 1 ]; then
            bad "$side: validate blocked on a FIFO among the agent, rule and skill files"
        else
            ok "$side: validate finishes with a FIFO in the agents, rules and skills directories"
            want_err_f "$VOUT" "<D>/.claude/agents/pipe.md: not a regular file; $SKIP" "$side: a FIFO that is an agent file is rejected"
            printf '%s' "$VOUT" | grep -q 'Summary: 1 error(s), 0 warning(s)' && ok "$side: the FIFO agent file is the only finding" || bad "$side: wrong findings with FIFOs present: $VOUT"
        fi
    done
    if [ "$sides" = "bash go" ]; then
        if diff "$TMP/fifo-bash.txt" "$TMP/fifo-go.txt" >/dev/null; then ok "bash and Go findings identical with FIFOs present"; else bad "bash/go findings differ with FIFOs present:"; diff "$TMP/fifo-bash.txt" "$TMP/fifo-go.txt"; fi
    fi
else
    printf '  SKIP the FIFO fixtures: mkfifo is not available here\n'
fi

# The shipped framework passes strict on both sides.
run_strict() { (cd "$REPO_ROOT" && "run_$1" --strict); }
for side in $sides; do
    vlim 180 "$TMP/strict.out" run_strict "$side"
    out="$(cat "$TMP/strict.out")"
    if printf '%s' "$out" | grep -q 'Summary: 0 error(s), 0 warning(s)'; then ok "$side: framework validate --strict clean"; else bad "$side: framework validate --strict not clean"; printf '%s\n' "$out" | grep -E '\[err\]|Summary' | head; fi
done

printf '\nagent-enums: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
