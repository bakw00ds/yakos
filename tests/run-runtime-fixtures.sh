#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Smoke + golden tests for the v0.4 runtime adapter layer.
# Inputs:  none
# Outputs: stdout: per-test pass/fail; stderr: error context
# Reads:   cli/lib/agents-compose.sh, cli/lib/runtime-resolve.sh,
#          cli/lib/runtimes/{claude,codex,gemini}.sh,
#          tests/fixtures/runtime/<test>/...
# Writes:  $TMPDIR/yakos-runtime-test.<pid>/* (cleaned at end)
# Exit codes: 0 all pass, 1 any failure.
#
# Tests:
#   1. agents-compose: emits expected agent ids for the framework alone
#   2. agents-compose: project agent overrides framework on id collision
#   3. agents-compose: extends: resolution prepends framework body
#   4. agents-compose: cache returns same value on second call
#   5. codex emitter: TOML has required keys (name, description,
#      developer_instructions)
#   6. gemini emitter: markdown has frontmatter and body separator
#      (materializes under YAKOS_GEMINI_SHIM_FORCE=1, since the shim is
#      hardcoded past its removal date; deterministic regardless of
#      wall-clock date — see 6b)
#   6b. gemini shim past removal date: returns non-zero without the
#       force override, prints a migration hint, writes no agent files,
#       and — critically — does not exit the test runner process
#   7. runtime-resolve: yk_rt_default falls back to claude
#   8. runtime-resolve: yk_rt_capability returns 0/1 correctly
#  11. general-codex / general-agy model pins and the alias file's agy / codex columns;
#      11b. a pinned alias never puts a Claude tier into a codex or agy agent file
#  17. codex emitter: marker, operator files left alone, legacy upgrade (K-134);
#      Claude tier omitted, control characters escaped, NUL refused, python == jq
#  18. agy emitter: <skills>/yakos-<id>/SKILL.md layout, marker, .gitignore, cleanup;
#      the same model, escaping and refusal rules
#  19. Go materializers write the same files as the bash emitters under YAKOS_IMPL=go,
#      for a probe agent and for real framework agents; codex dispatch is
#      sandboxed by default and agy gets --sandbox (which is not containment,
#      K-158). Needs bin/yakos (`make build`); skipped when absent unless
#      YAKOS_REQUIRE_GO_BINARY is set, which CI sets so it cannot be skipped.
#  20. the claude-sdk runtime (the Python Agent SDK) refuses to run without an
#      Anthropic API key and starts python without subscription OAuth variables:
#      the bash twin of the Go SDK sidecar gate (K-137)
set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

WORKDIR="${TMPDIR:-/tmp}/yakos-runtime-test.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

# Source the framework helpers in this shell.
# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../cli/lib/agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=../cli/lib/runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

echo "yakos runtime fixtures"
echo

# ---- 1. framework-only compose -----------------------------------------------
echo "Test 1: framework agents compose"
out="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null)"
n="$(printf '%s' "$out" | jq 'length')"
if [ "$n" -ge 11 ]; then
    ok "framework compose returned $n agents (expected ≥ 11)"
else
    fail "framework compose returned $n agents (expected ≥ 11)"
fi
# Expected core ids
for id in backend frontend mobile database planner code-reviewer \
          security-reviewer test-runner troubleshooter doc-writer maintainer \
          architect incident-responder release-manager; do
    if printf '%s' "$out" | jq -e --arg n "$id" 'has($n)' >/dev/null; then
        ok "  has agent: $id"
    else
        fail "  missing agent: $id"
    fi
done

# ---- 2. project-override semantics --------------------------------------------
echo
echo "Test 2: project agent overrides framework on id collision"
fake_proj="$WORKDIR/fake-project"
mkdir -p "$fake_proj/.claude/agents"
cat > "$fake_proj/.claude/agents/backend.md" <<'EOF'
---
id: backend
role: specialist
domain: my-stack
mode: [feature]
tools: [Read]
model: haiku
references: []
---

# Project-overridden backend

## Purpose

This is the project's override.
EOF

# Force a fresh shell to avoid the cache from Test 1 polluting the result.
override_out="$(bash -c '
    set -eu
    export YAKOS_ROOT="'"$REPO_ROOT"'"
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/agents-compose.sh"
    yk_agents_compose "$YAKOS_ROOT" "'"$fake_proj"'"
')"
override_model="$(printf '%s' "$override_out" | jq -r '.backend.model')"
if [ "$override_model" = "haiku" ]; then
    ok "project override won (model=haiku); framework default was sonnet"
else
    fail "project override did not win (model=$override_model)"
fi

# ---- 3. extends: resolution --------------------------------------------------
echo
echo "Test 3: extends: prepends framework body"
mkdir -p "$fake_proj/.claude/agents"
cat > "$fake_proj/.claude/agents/myapp-backend.md" <<'EOF'
---
id: myapp-backend
role: specialist
domain: my-stack
extends: backend
mode: [feature]
tools: [Read, Edit]
model: sonnet
references: []
---

# MyApp Backend (project-specific)

## Purpose

Project-specific delta on top of the framework backend.
EOF
extends_out="$(bash -c '
    set -eu
    export YAKOS_ROOT="'"$REPO_ROOT"'"
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/agents-compose.sh"
    yk_agents_compose "$YAKOS_ROOT" "'"$fake_proj"'"
')"
extends_prompt="$(printf '%s' "$extends_out" | jq -r '."myapp-backend".prompt')"
if printf '%s' "$extends_prompt" | grep -q "Backend Specialist"; then
    ok "extends: prepended framework backend body"
else
    fail "extends: did NOT prepend framework body"
fi
if printf '%s' "$extends_prompt" | grep -q "MyApp Backend"; then
    ok "extends: project body present after framework body"
else
    fail "extends: project body missing"
fi

# ---- 4. compose cache --------------------------------------------------------
echo
echo "Test 4: compose cache returns identical result"
out_a="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null | jq -S 'keys')"
out_b="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null | jq -S 'keys')"
if [ "$out_a" = "$out_b" ]; then
    ok "cache returns identical result on second call"
else
    fail "cache returns different result"
fi

# ---- 5. codex TOML emitter ---------------------------------------------------
echo
echo "Test 5: codex TOML emitter"
# shellcheck source=../cli/lib/runtimes/codex.sh
. "$YAKOS_LIB/runtimes/codex.sh"
codex_out="$WORKDIR/codex-agents"
mkdir -p "$codex_out"
yk_rt_codex_materialize_agents "$REPO_ROOT" "" "$codex_out" >/dev/null 2>&1 || true
emitted_count="$(find "$codex_out" -name 'yakos-*.toml' -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ "$emitted_count" -ge 11 ]; then
    ok "codex emitter wrote $emitted_count TOML files (expected ≥ 11)"
else
    fail "codex emitter wrote $emitted_count TOML files (expected ≥ 11)"
fi
# The bash composer resolves model aliases to Claude tiers, and codex fails a
# subagent whose model it does not have, so no generated file may name one.
if grep -lE '^model = ' "$codex_out"/yakos-*.toml >/dev/null 2>&1; then
    fail "a codex agent file names a model: $(grep -lE '^model = ' "$codex_out"/yakos-*.toml | tr '\n' ' ')"
else
    ok "no codex agent file written for the framework roster names a model"
fi
sample="$codex_out/yakos-architect.toml"
if [ -f "$sample" ]; then
    if grep -qE '^name = "architect"$' "$sample" \
       && grep -qE '^description = ' "$sample" \
       && grep -qE '^developer_instructions = """$' "$sample"; then
        ok "codex TOML has required fields (name, description, developer_instructions)"
    else
        fail "codex TOML missing required fields:"
        head -10 "$sample" | sed 's/^/    /' >&2
    fi
else
    fail "codex sample file not found at $sample"
fi

# ---- 6. gemini markdown emitter ----------------------------------------------
echo
echo "Test 6: gemini markdown emitter (under YAKOS_GEMINI_SHIM_FORCE=1 override)"
# shellcheck source=../cli/lib/runtimes/gemini.sh
. "$YAKOS_LIB/runtimes/gemini.sh"
gemini_out="$WORKDIR/gemini-agents"
mkdir -p "$gemini_out"
# The shim's removal date (2026-09-01) is hardcoded and in the past as of
# any CI run from here on, so exercise the materialize path under the
# documented operator override rather than relying on the wall clock.
YAKOS_GEMINI_SHIM_FORCE=1 yk_rt_gemini_materialize_agents "$REPO_ROOT" "" "$gemini_out" >/dev/null 2>&1 || true
# The gemini shim delegates to the agy materializer, which (K-134) writes one
# <out>/yakos-<id>/SKILL.md per agent, the layout agy 1.2.x discovers.
emitted_count="$(find "$gemini_out" -name 'SKILL.md' -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ "$emitted_count" -ge 11 ]; then
    ok "gemini emitter wrote $emitted_count markdown files (expected ≥ 11)"
else
    fail "gemini emitter wrote $emitted_count markdown files (expected ≥ 11)"
fi
if grep -lE '^model: ' "$gemini_out"/yakos-*/SKILL.md >/dev/null 2>&1; then
    fail "an agy skill names a model: $(grep -lE '^model: ' "$gemini_out"/yakos-*/SKILL.md | tr '\n' ' ')"
else
    ok "no agy skill written for the framework roster names a model"
fi
sample="$gemini_out/yakos-architect/SKILL.md"
if [ -f "$sample" ]; then
    # Frontmatter open + name + frontmatter close + body
    if grep -qE '^---$' "$sample" \
       && grep -qE '^name: yakos-architect$' "$sample" \
       && grep -qE '^description: ' "$sample"; then
        ok "gemini markdown has frontmatter (---, name, description)"
    else
        fail "gemini markdown missing frontmatter shape"
        head -10 "$sample" | sed 's/^/    /' >&2
    fi
else
    fail "gemini sample file not found at $sample"
fi

# ---- 6b. gemini shim past removal date, no override ---------------------
echo
echo "Test 6b: gemini shim past removal date (no override) returns non-zero, does not exit"
blocked_out="$WORKDIR/gemini-agents-blocked"
mkdir -p "$blocked_out"
blocked_err="$WORKDIR/gemini-blocked.stderr"
unset YAKOS_GEMINI_SHIM_FORCE YAKOS_GEMINI_FORCE_WARNED YAKOS_GEMINI_REMOVAL_WARNED YAKOS_GEMINI_DEPRECATION_WARNED 2>/dev/null || true
# This call is deliberately NOT wrapped in `|| true` alone at top level of a
# subshell trick — it's a plain `if`, which is enough now that the shim
# returns 1 instead of calling exit. If a regression reintroduces exit/
# ct_die here, this whole script dies right here and Tests 7+ never run —
# the same failure signature the CI diagnosis identified.
if yk_rt_gemini_materialize_agents "$REPO_ROOT" "" "$blocked_out" >/dev/null 2>"$blocked_err"; then
    fail "gemini shim past removal date unexpectedly succeeded without YAKOS_GEMINI_SHIM_FORCE"
else
    ok "gemini shim past removal date returns non-zero without YAKOS_GEMINI_SHIM_FORCE (script continues)"
fi
if grep -qi "migration\|removal date\|removed" "$blocked_err" 2>/dev/null; then
    ok "gemini shim prints a migration hint on past-removal call"
else
    fail "gemini shim did not print an expected migration hint"
    sed 's/^/    /' "$blocked_err" >&2
fi
blocked_count="$(find "$blocked_out" \( -name 'yakos-*.md' -o -name 'SKILL.md' \) -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ "$blocked_count" -eq 0 ]; then
    ok "gemini shim past removal date wrote no agent files (no silent partial materialize)"
else
    fail "gemini shim past removal date unexpectedly wrote $blocked_count agent files"
fi
# Proof-of-life: if the process had been killed above, this line — and
# every test after it — would never run.
ok "test runner process is still alive after the past-removal-date call"

# ---- 7. runtime-resolve default ----------------------------------------------
echo
echo "Test 7: runtime-resolve default"
default_in_clean_env="$(env -u YAKOS_RUNTIME bash -c '
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/runtime-resolve.sh"
    yk_rt_default
' 2>/dev/null)"
if [ "$default_in_clean_env" = "claude" ]; then
    ok "yk_rt_default = claude in clean env"
else
    fail "yk_rt_default = '$default_in_clean_env' (expected 'claude')"
fi

# Env var override
override_default="$(YAKOS_RUNTIME=codex bash -c '
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/runtime-resolve.sh"
    yk_rt_default
' 2>/dev/null)"
if [ "$override_default" = "codex" ]; then
    ok "YAKOS_RUNTIME=codex overrides default"
else
    fail "YAKOS_RUNTIME=codex did not override (got '$override_default')"
fi

# ---- 8. runtime capability check --------------------------------------------
echo
echo "Test 8: runtime capability checks"
if yk_rt_capability claude inline-agents; then
    ok "claude has 'inline-agents' capability"
else
    fail "claude is missing 'inline-agents' capability"
fi
if yk_rt_capability codex inline-agents; then
    fail "codex should NOT advertise 'inline-agents'"
else
    ok "codex correctly does NOT have 'inline-agents'"
fi
if yk_rt_capability gemini hooks; then
    ok "gemini has 'hooks' capability"
else
    fail "gemini is missing 'hooks' capability"
fi

# ---- 9. claude dispatch arg array contains --exclude-dynamic-system-prompt-sections
echo
echo "Test 9: yk_rt_claude_dispatch includes --exclude-dynamic-system-prompt-sections"
# shellcheck source=../cli/lib/runtimes/claude.sh
. "$YAKOS_LIB/runtimes/claude.sh"
claude_sh="$YAKOS_LIB/runtimes/claude.sh"
hit_count="$(grep -c -- '--exclude-dynamic-system-prompt-sections' "$claude_sh" || true)"
# There are exactly two -p invocation sites in yk_rt_claude_dispatch;
# each must carry the flag.
if [ "${hit_count:-0}" -ge 2 ]; then
    ok "claude.sh yk_rt_claude_dispatch has --exclude-dynamic-system-prompt-sections on both -p invocations ($hit_count occurrences)"
else
    fail "claude.sh missing --exclude-dynamic-system-prompt-sections on one or more -p invocations (found $hit_count; need >= 2)"
fi
# Also confirm the flag is NOT present in yk_rt_claude_launch (the exec
# path above the dispatch function, which must never carry it).
launch_block="$(sed -n '/^yk_rt_claude_launch/,/^yk_rt_claude_dispatch/p' "$claude_sh")"
if printf '%s\n' "$launch_block" | grep -q -- '--exclude-dynamic-system-prompt-sections'; then
    fail "yk_rt_claude_launch unexpectedly contains --exclude-dynamic-system-prompt-sections (must be dispatch-only)"
else
    ok "yk_rt_claude_launch correctly does NOT carry the flag"
fi

# ---- 10. general-codex + general-agy agent files exist with valid fm ------
echo
echo "Test 10: general-{codex,gemini} agents exist with valid frontmatter"
for agent_id in general-codex general-agy; do
    agent_file="$REPO_ROOT/lib/agents/${agent_id}.md"
    if [ ! -f "$agent_file" ]; then
        fail "$agent_id: file not found at $agent_file"
        continue
    fi
    ok "$agent_id: file exists"

    # Frontmatter has opening and closing ---
    if head -n 1 "$agent_file" | grep -q '^---$' \
       && head -n 200 "$agent_file" | tail -n +2 | grep -qx '^---$'; then
        ok "$agent_id: frontmatter delimiters present"
    else
        fail "$agent_id: missing frontmatter --- delimiters"
    fi

    # Required fields present
    fm="$(yk_agents_extract_frontmatter "$agent_file")"
    for field in id role domain runtime model version; do
        val="$(yk_agents_fm_get "$fm" "$field")"
        if [ -n "$val" ]; then
            ok "$agent_id: frontmatter field '$field' = $val"
        else
            fail "$agent_id: frontmatter missing required field '$field'"
        fi
    done

    # Body has ## Purpose section
    body="$(yk_agents_extract_body "$agent_file")"
    if printf '%s\n' "$body" | grep -qE '^##[[:space:]]+Purpose[[:space:]]*$'; then
        ok "$agent_id: body has '## Purpose' section"
    else
        fail "$agent_id: body missing '## Purpose' section"
    fi
done

# ---- 11. runtime: + model: fields pinned to expected values -----------------
echo
echo "Test 11: runtime: pinned (not claude); model: matches what the runtime can use"
# general-codex pins the alias `balanced`: lib/settings/model-aliases.json maps
# every codex alias to "" (harness default) because the ChatGPT-login catalog has
# no documented tiers and codex rejects an unknown id with HTTP 400. general-agy
# pins a real id from `agy models` (gemini-3.8-flash-high is the `balanced` alias).
# Format: "agent-id:expected-runtime:expected-model"
for check in "general-codex:codex:balanced" "general-agy:agy:gemini-3.8-flash-high"; do
    agent_id="${check%%:*}"
    rest="${check#*:}"
    expected_rt="${rest%%:*}"
    expected_model="${rest##*:}"
    agent_file="$REPO_ROOT/lib/agents/${agent_id}.md"
    if [ ! -f "$agent_file" ]; then
        fail "$agent_id: file not found (skip runtime+model field check)"
        continue
    fi
    fm="$(yk_agents_extract_frontmatter "$agent_file")"

    # runtime check
    actual_rt="$(yk_agents_fm_get "$fm" "runtime")"
    if [ "$actual_rt" = "$expected_rt" ]; then
        ok "$agent_id: runtime = $actual_rt (expected $expected_rt)"
    else
        fail "$agent_id: runtime = '$actual_rt' (expected '$expected_rt')"
    fi
    if [ "$actual_rt" = "claude" ]; then
        fail "$agent_id: runtime must not be 'claude' (defeats the purpose)"
    fi

    actual_model="$(yk_agents_fm_get "$fm" "model")"
    if [ "$actual_model" = "$expected_model" ]; then
        ok "$agent_id: model = $actual_model (expected $expected_model)"
    else
        fail "$agent_id: model = '$actual_model' (expected '$expected_model')"
    fi
    # The pinned model must resolve to something real for that runtime: an alias
    # whose column is "" means the harness default; any other value must be the
    # id the alias file lists for that runtime, or an id that is not an alias.
    aliases_file="$REPO_ROOT/lib/settings/model-aliases.json"
    case "$actual_model" in
        cheap|balanced|best|reasoning|frontier)
            mapped="$(jq -r --arg a "$actual_model" --arg r "$actual_rt" '.aliases[$a][$r] // "<unmapped>"' "$aliases_file")"
            if [ "$actual_rt" = "codex" ] && [ -z "$mapped" ]; then
                ok "$agent_id: alias '$actual_model' deliberately maps to the codex harness default"
            elif [ "$mapped" != "<unmapped>" ] && [ -n "$mapped" ]; then
                ok "$agent_id: alias '$actual_model' maps to '$mapped' for $actual_rt"
            else
                fail "$agent_id: alias '$actual_model' has no mapping for $actual_rt in model-aliases.json"
            fi
            ;;
        *)
            ok "$agent_id: model '$actual_model' is a concrete model ID (not a semantic alias)"
            ;;
    esac
done
# The ids the alias file names for agy must be ones `agy models` lists (2026-10-05).
for alias_name in cheap balanced best reasoning frontier; do
    got="$(jq -r --arg a "$alias_name" '.aliases[$a].agy' "$REPO_ROOT/lib/settings/model-aliases.json")"
    case "$got" in
        gemini-3.8-flash-low|gemini-3.8-flash-high|claude-opus-5-5-medium|claude-opus-5-5-high|gemini-3.1-pro-high)
            ok "model-aliases.json: agy $alias_name = $got (a real agy model id)" ;;
        *) fail "model-aliases.json: agy $alias_name = '$got' is not an id agy lists" ;;
    esac
done
for alias_name in cheap balanced best reasoning frontier; do
    got="$(jq -r --arg a "$alias_name" '.aliases[$a].codex' "$REPO_ROOT/lib/settings/model-aliases.json")"
    if [ -z "$got" ]; then
        ok "model-aliases.json: codex $alias_name is empty (harness default)"
    else
        fail "model-aliases.json: codex $alias_name = '$got' (codex aliases must stay empty until the registry fills them)"
    fi
done

# ---- 11b. a pinned alias never puts a Claude tier into an agent file --------
echo
echo "Test 11b: a pinned alias never puts a Claude tier into a codex or agy agent file"
# general-codex pins the alias balanced. The bash composer resolves aliases to Claude
# tiers itself, so the composed agent carries model "sonnet", and the emitter used to
# write model = "sonnet" into .codex/agents/yakos-general-codex.toml. codex-cli 0.154.0
# then failed the subagent: its fixed `sonnet` model is not supported with this Codex
# ChatGPT account. A file must name a model only when it is one the runtime has.
# shellcheck source=../cli/lib/runtimes/codex.sh
. "$YAKOS_LIB/runtimes/codex.sh"
# shellcheck source=../cli/lib/runtimes/agy.sh
. "$YAKOS_LIB/runtimes/agy.sh"

# t_emit <py|jq> <codex|agy> <id> <agent-json> <out-dir>
#   Run one bash emitter on the python path, or on the jq fallback (python3 reported
#   absent). The emitter's stderr goes to <out-dir>/.last.err.
t_emit() {
    local mode="$1" rt="$2" id="$3" json="$4" out="$5"
    mkdir -p "$out"
    (
        if [ "$mode" = jq ]; then yk_emit_check_python() { return 1; }; fi
        case "$rt" in
            codex) yk_rt_codex_emit_toml "$id" "$json" "$out" ;;
            agy)   yk_rt_agy_emit_md "$id" "$json" "$out" ;;
        esac
    ) >/dev/null 2>"$out/.last.err"
}
# t_file <codex|agy> <id> <out-dir>: where the emitter put the agent file
t_file() {
    case "$1" in
        codex) printf '%s\n' "$3/yakos-$2.toml" ;;
        agy)   printf '%s\n' "$3/yakos-$2/SKILL.md" ;;
    esac
}
# t_has_model_line <file>: 0 when the file sets a model (TOML `model = ` or YAML `model: `)
t_has_model_line() { grep -Eq '^model( =|:) ' "$1"; }

t11="$WORKDIR/t11"
t11_roster="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null)"
t11_gc="$(printf '%s' "$t11_roster" | jq -c '."general-codex"')"
for mode in py jq; do
    for rt in codex agy; do
        t_emit "$mode" "$rt" general-codex "$t11_gc" "$t11/gc-$mode-$rt" || true
        f="$(t_file "$rt" general-codex "$t11/gc-$mode-$rt")"
        if [ ! -f "$f" ]; then
            fail "general-codex: the $rt emitter ($mode) wrote no file"
        elif t_has_model_line "$f"; then
            fail "general-codex: the $rt file ($mode) names a model:"; grep -E '^model( =|:) ' "$f" | sed 's/^/    /' >&2
        else
            ok "general-codex: the $rt file ($mode) names no model (composed model: '$(printf '%s' "$t11_gc" | jq -r '.model // "none"')')"
        fi
    done
done
for tier in haiku sonnet opus fable; do
    for mode in py jq; do
        for rt in codex agy; do
            t_emit "$mode" "$rt" tier "{\"description\":\"d\",\"prompt\":\"p\",\"model\":\"$tier\"}" "$t11/tier-$tier-$mode-$rt" || true
            f="$(t_file "$rt" tier "$t11/tier-$tier-$mode-$rt")"
            if [ -f "$f" ] && ! t_has_model_line "$f"; then
                ok "model $tier is not written to the $rt file ($mode)"
            else
                fail "model $tier reached the $rt file ($mode)"
            fi
        done
    done
done
for mode in py jq; do
    for rt in codex agy; do
        t_emit "$mode" "$rt" real '{"description":"d","prompt":"p","model":"gemini-3.8-flash-high"}' "$t11/real-$mode-$rt" || true
        f="$(t_file "$rt" real "$t11/real-$mode-$rt")"
        if [ -f "$f" ] && grep -Eq '^model( =|:) "gemini-3.8-flash-high"$' "$f"; then
            ok "a model that is not a Claude tier is still written to the $rt file ($mode)"
        else
            fail "a non-tier model was dropped from the $rt file ($mode)"
        fi
    done
done

# ---- 12. find_agent_file resolves general-codex + general-agy ------------
echo
echo "Test 12: find_agent_file resolves general-codex and general-agy"
# Inline a minimal find_agent_file using the helpers already sourced above.
_find_agent_fw() {
    local id="$1"
    local d f fm fm_id
    d="$REPO_ROOT/lib/agents"
    for f in "$d"/*.md; do
        [ -f "$f" ] || continue
        case "$(basename -- "$f")" in README.md|lead-template.md) continue ;; esac
        fm="$(yk_agents_extract_frontmatter "$f")"
        fm_id="$(yk_agents_fm_get "$fm" "id")"
        if [ "$fm_id" = "$id" ] || [ "${f%.md}" = "$d/$id" ]; then
            printf '%s\n' "$f"
            return 0
        fi
    done
    return 1
}
for agent_id in general-codex general-agy; do
    resolved="$(_find_agent_fw "$agent_id" || true)"
    if [ -n "$resolved" ] && [ -f "$resolved" ]; then
        ok "$agent_id: find_agent_file resolved to $resolved"
    else
        fail "$agent_id: find_agent_file did not resolve"
    fi
done

# ---- 13. compose includes general-codex + general-agy --------------------
echo
echo "Test 13: yk_agents_compose includes general-codex and general-agy"
# Use a fresh shell to avoid cache pollution from Test 1.
composed="$(bash -c '
    set -eu
    export YAKOS_ROOT="'"$REPO_ROOT"'"
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/agents-compose.sh"
    yk_agents_compose "$YAKOS_ROOT" ""
' 2>/dev/null)"
for agent_id in general-codex general-agy; do
    if printf '%s' "$composed" | jq -e --arg n "$agent_id" 'has($n)' >/dev/null; then
        ok "compose includes agent: $agent_id"
    else
        fail "compose missing agent: $agent_id"
    fi
done

# ---- 14. yakos validate --strict passes on both new agents ------------------
echo
echo "Test 14: yakos validate --strict passes on general-codex and general-agy"
# Run validate in framework mode (no project path) and capture per-agent output.
validate_out="$(YAKOS_ROOT="$REPO_ROOT" \
    bash "$YAKOS_LIB/validate.sh" --strict 2>&1 || true)"
for agent_id in general-codex general-agy; do
    agent_file="$REPO_ROOT/lib/agents/${agent_id}.md"
    # validate emits "[ok]   <path>" on success; "[err]  <path>:" on failure.
    if printf '%s\n' "$validate_out" | grep -qE "^\s+\[ok\]\s+${agent_file}$"; then
        ok "$agent_id: yakos validate --strict passed"
    elif printf '%s\n' "$validate_out" | grep -qE "^\s+\[err\].*${agent_id}"; then
        fail "$agent_id: yakos validate --strict reported an error"
        printf '%s\n' "$validate_out" | grep -E "${agent_id}" | sed 's/^/    /' >&2
    else
        fail "$agent_id: could not find validate output for agent"
        printf '%s\n' "$validate_out" | grep -E "general-" | sed 's/^/    /' >&2
    fi
done

# ---- 15. dual-stat portability: BSD stat succeeds (macOS happy-path) --------
echo
echo "Test 15: dual-stat portability — BSD stat first-wins (macOS)"
_stat_test_dir="$WORKDIR/stat-test-bsd"
mkdir -p "$_stat_test_dir"
_stat_test_file="$_stat_test_dir/probe.pb"
printf 'hello' > "$_stat_test_file"

# Exercise the same pattern used in the fixed code paths: BSD first, GNU fallback.
_bsd_result="$(stat -f '%m' "$_stat_test_file" 2>/dev/null || stat -c '%Y' "$_stat_test_file" 2>/dev/null || true)"
if [ -n "$_bsd_result" ] && printf '%s' "$_bsd_result" | grep -qE '^[0-9]+$'; then
    ok "dual-stat: BSD 'stat -f %m' returns numeric mtime ($( printf '%s' "$_bsd_result" | head -c 15 ))"
else
    fail "dual-stat: BSD 'stat -f %m' did not return a numeric mtime (got '$_bsd_result')"
fi

# Confirm a find+loop using the dual-stat pattern produces non-empty mtime list.
_mtime_list=""
while IFS= read -r -d '' _f; do
    _mt="$(stat -f '%m' "$_f" 2>/dev/null || stat -c '%Y' "$_f" 2>/dev/null || true)"
    [ -n "$_mt" ] && _mtime_list="${_mtime_list}${_mt} ${_f}"$'\n'
done < <(find "$_stat_test_dir" -maxdepth 1 -type f -name '*.pb' -print0 2>/dev/null)
_found="$(printf '%s' "$_mtime_list" | sort -n -r | head -1 | awk '{print $2}')"
if [ -n "$_found" ] && [ -f "$_found" ]; then
    ok "dual-stat loop: find+loop selects correct file ($( basename "$_found" ))"
else
    fail "dual-stat loop: find+loop returned empty or non-existent file ('$_found')"
fi

# ---- 16. dual-stat portability: GNU stat fallback path ----------------------
echo
echo "Test 16: dual-stat portability — GNU stat fallback (simulated Linux)"
# Build a fake 'stat' that simulates GNU behavior:
#   -f flag → exits non-zero with an error message (BSD-only flag; GNU rejects it)
#   -c '%Y' → prints a known synthetic mtime (1700000042) regardless of file
# This lets the test run without an actual Linux box or GNU stat binary.
_fake_stat_dir="$WORKDIR/fake-stat-bin"
mkdir -p "$_fake_stat_dir"
cat > "$_fake_stat_dir/stat" <<'FAKESTAT'
#!/usr/bin/env bash
# Simulated GNU stat: -f flag unsupported; -c '%Y' emits synthetic mtime.
for arg in "$@"; do
    case "$arg" in
        -f) printf 'stat: invalid option -- '\''f'\''\n' >&2; exit 1 ;;
    esac
done
# For -c '%Y' <file>: emit a fixed synthetic epoch so the test is deterministic.
printf '1700000042\n'
FAKESTAT
chmod +x "$_fake_stat_dir/stat"

_gnu_test_file="$WORKDIR/stat-test-bsd/probe.pb"   # reuse file from Test 15
# Run the dual-stat expression with the fake stat shadowing the real one.
_gnu_result="$(PATH="$_fake_stat_dir:$PATH" bash -c '
    stat -f "%m" "$1" 2>/dev/null || stat -c "%Y" "$1" 2>/dev/null || true
' -- "$_gnu_test_file")"
if [ -n "$_gnu_result" ] && printf '%s' "$_gnu_result" | grep -qE '^[0-9]+$'; then
    ok "dual-stat: GNU fallback 'stat -c %Y' returns numeric mtime (${_gnu_result})"
else
    fail "dual-stat: GNU fallback did not return a numeric mtime (got '$_gnu_result')"
fi

# Verify the fallback value matches the synthetic mtime from the fake stat.
if [ "$_gnu_result" = "1700000042" ]; then
    ok "dual-stat: confirmed GNU fallback path was taken (synthetic mtime matches)"
else
    fail "dual-stat: unexpected mtime '$_gnu_result' — BSD path may have run when GNU was expected"
fi

# Repeat the find+loop pattern with the fake stat on PATH; confirm non-empty output.
_mtime_list2=""
while IFS= read -r -d '' _f; do
    _mt="$(PATH="$_fake_stat_dir:$PATH" bash -c '
        stat -f "%m" "$1" 2>/dev/null || stat -c "%Y" "$1" 2>/dev/null || true
    ' -- "$_f")"
    [ -n "$_mt" ] && _mtime_list2="${_mtime_list2}${_mt} ${_f}"$'\n'
done < <(find "$WORKDIR/stat-test-bsd" -maxdepth 1 -type f -name '*.pb' -print0 2>/dev/null)
_found2="$(printf '%s' "$_mtime_list2" | sort -n -r | head -1 | awk '{print $2}')"
if [ -n "$_found2" ] && [ -f "$_found2" ]; then
    ok "dual-stat loop (GNU fallback): find+loop returns non-empty file path ($( basename "$_found2" ))"
else
    fail "dual-stat loop (GNU fallback): find+loop returned empty or non-existent file ('$_found2')"
fi

# ---- 17. codex emitter: marker, operator files, legacy upgrade (K-134) -------
echo
echo "Test 17: codex emitter marker, no-overwrite of operator files, legacy upgrade"
t17="$WORKDIR/t17"
mkdir -p "$t17"
t17_json='{"description":"Probe.","prompt":"# Probe\n\nBody.\n","tools":[]}'
t17_marker='# yakos-generated: rewritten on every dispatch. Delete this line to keep your edits.'
t17_file="$(yk_rt_codex_emit_toml probe "$t17_json" "$t17" 2>/dev/null)"
if [ "$t17_file" = "$t17/yakos-probe.toml" ] && [ "$(head -n 1 "$t17_file")" = "$t17_marker" ]; then
    ok "generated TOML starts with the yakos-generated marker"
else
    fail "generated TOML missing the marker (path='$t17_file')"
fi
cp "$t17_file" "$t17/first.copy"
yk_rt_codex_emit_toml probe "$t17_json" "$t17" >/dev/null 2>&1
if cmp -s "$t17_file" "$t17/first.copy"; then
    ok "re-emitting an unchanged agent yields identical bytes"
else
    fail "re-emitting changed the bytes"
fi
printf 'name = "mine"\ndescription = "hand written"\ndeveloper_instructions = """\nmy rules\n"""\n' > "$t17/yakos-own.toml"
cp "$t17/yakos-own.toml" "$t17/own.before"
yk_rt_codex_emit_toml own "$t17_json" "$t17" >/dev/null 2>"$t17/own.err"
if cmp -s "$t17/yakos-own.toml" "$t17/own.before" && grep -q "not overwriting" "$t17/own.err"; then
    ok "an operator's yakos-*.toml without the marker is left alone and the skip is logged"
else
    fail "an operator's yakos-*.toml was overwritten or the skip was not logged"
fi
printf 'name = "legacy"\ndescription = "old"\ndeveloper_instructions = """\nold\n"""\n' > "$t17/yakos-legacy.toml"
yk_rt_codex_emit_toml legacy "$t17_json" "$t17" >/dev/null 2>&1
if [ "$(head -n 1 "$t17/yakos-legacy.toml")" = "$t17_marker" ]; then
    ok "a legacy generated file (no marker, name = \"<id>\" first) is upgraded in place"
else
    fail "a legacy generated file was not upgraded"
fi

# Escaping, refusal and normalisation. The python path and the jq fallback (python3
# absent) must write the same bytes, so a host without python3 gets the same file.
# t_toml_ok <file>: 0 when the file is valid TOML. python 3.11+ parses it; older
# pythons get the weaker check that no raw control character other than TAB, LF and
# CR is present.
t_toml_ok() {
    if python3 -c 'import tomllib' 2>/dev/null; then
        python3 -c 'import sys, tomllib; tomllib.load(open(sys.argv[1], "rb"))' "$1" 2>/dev/null
    else
        ! LC_ALL=C grep -q "[$(printf '\001-\010\013\014\016-\037\177')]" "$1"
    fi
}
t17_nasty='{"description":"d\u0001e \"q\" \\ \u001b","prompt":"\n\n# Probe\r\nlone\rcr \u0001 \u007f \u001b[0m\n\"\"\"\" tab\t end\r\n\n","model":"sonnet"}'
for mode in py jq; do
    t_emit "$mode" codex nasty "$t17_nasty" "$t17/nasty-$mode" || true
    f="$t17/nasty-$mode/yakos-nasty.toml"
    if [ -f "$f" ] && t_toml_ok "$f"; then
        ok "codex ($mode): control characters, a lone CR and quotes produce valid TOML"
    else
        fail "codex ($mode): the TOML for a persona with control characters is missing or invalid"
        cat -v "$f" 2>/dev/null | sed 's/^/    /' >&2
    fi
    if [ -f "$f" ] && grep -Fq 'lone\u000Dcr \u0001 \u007F \u001B[0m' "$f" && ! t_has_model_line "$f"; then
        ok "codex ($mode): escapes are \u00XX in upper-case hex, and the tier model is omitted"
    else
        fail "codex ($mode): expected \\u00XX escapes and no model line"
    fi
done
if cmp -s "$t17/nasty-py/yakos-nasty.toml" "$t17/nasty-jq/yakos-nasty.toml"; then
    ok "codex: the jq fallback writes the same bytes as the python path"
else
    fail "codex: the jq fallback and the python path differ"
    diff "$t17/nasty-py/yakos-nasty.toml" "$t17/nasty-jq/yakos-nasty.toml" | sed 's/^/    /' >&2 || true
fi
for mode in py jq; do
    rc=0
    t_emit "$mode" codex nul '{"description":"d","prompt":"a\u0000b"}' "$t17/nul-$mode" || rc=$?
    if [ "$rc" -eq 0 ] && [ ! -e "$t17/nul-$mode/yakos-nul.toml" ] && grep -q 'NUL byte' "$t17/nul-$mode/.last.err"; then
        ok "codex ($mode): a persona with a NUL byte is refused, nothing is written, the status is 0"
    else
        fail "codex ($mode): NUL handling wrong (status $rc, file present: $([ -e "$t17/nul-$mode/yakos-nul.toml" ] && echo yes || echo no))"
    fi
done
t_emit py codex lead '{"description":"d","prompt":"\r\n\n# X\n\n"}' "$t17/lead-a" || true
t_emit py codex lead '{"description":"d","prompt":"# X"}' "$t17/lead-b" || true
if cmp -s "$t17/lead-a/yakos-lead.toml" "$t17/lead-b/yakos-lead.toml"; then
    ok "codex: leading line breaks of the prompt are dropped (bash composer keeps the blank line after the frontmatter, the Go composer drops it)"
else
    fail "codex: leading line breaks change the file"
fi

# ---- 18. agy emitter: skill directory layout, marker, cleanup (K-134) --------
echo
echo "Test 18: agy emitter writes <skills>/yakos-<id>/SKILL.md with marker and .gitignore"
# shellcheck source=../cli/lib/runtimes/agy.sh
. "$YAKOS_LIB/runtimes/agy.sh"
t18="$WORKDIR/t18/.agents/skills"
mkdir -p "$t18"
t18_json='{"description":"Probe.","prompt":"# Probe\n\nBody.\n","tools":["Read"]}'
t18_file="$(yk_rt_agy_emit_md probe "$t18_json" "$t18" 2>/dev/null)"
if [ "$t18_file" = "$t18/yakos-probe/SKILL.md" ] && [ -f "$t18_file" ] && [ ! -e "$t18/yakos-probe.md" ]; then
    ok "skill is a directory with SKILL.md (agy loads <dir>/<skill>/SKILL.md), no flat .md"
else
    fail "unexpected agy layout (path='$t18_file')"
fi
if grep -qx 'name: yakos-probe' "$t18_file" && grep -q 'yakos-generated:' "$t18_file"; then
    ok "SKILL.md name equals the directory name and carries the marker"
else
    fail "SKILL.md name/marker wrong"
fi
if [ "$(cat "$t18/yakos-probe/.gitignore")" = "*" ]; then
    ok "the skill directory carries a self-ignoring .gitignore"
else
    fail "missing or wrong .gitignore in the skill directory"
fi
if command -v git >/dev/null 2>&1; then
    git -C "$WORKDIR/t18" init -q 2>/dev/null
    if [ -z "$(git -C "$WORKDIR/t18" status --porcelain -uall 2>/dev/null)" ]; then
        ok "generated skill files are invisible to git status"
    else
        fail "generated skill files show up in git status"
    fi
fi
mkdir -p "$t18/yakos-own"
printf -- '---\nname: yakos-own\n---\nmine\n' > "$t18/yakos-own/SKILL.md"
cp "$t18/yakos-own/SKILL.md" "$WORKDIR/t18/own.before"
yk_rt_agy_emit_md own "$t18_json" "$t18" >/dev/null 2>&1
if cmp -s "$t18/yakos-own/SKILL.md" "$WORKDIR/t18/own.before" && [ ! -e "$t18/yakos-own/.gitignore" ]; then
    ok "an operator's SKILL.md without the marker is left alone"
else
    fail "an operator's SKILL.md was overwritten"
fi
printf -- '---\nname: old\n---\n' > "$t18/yakos-flat.md"
yk_rt_agy_cleanup_agents "$WORKDIR/t18"
if [ ! -e "$t18/yakos-probe" ] && [ ! -e "$t18/yakos-flat.md" ] && [ -f "$t18/yakos-own/SKILL.md" ]; then
    ok "cleanup removes generated skill dirs and legacy flat files, keeps the operator's"
else
    fail "cleanup removed the wrong things"
fi

# Escaping, refusal and normalisation, as for codex in Test 17.
t18_nasty='{"description":"d\u0001e \"q\" \\ \u001b","prompt":"\n\n# Probe\r\nlone\rcr\n","model":"opus","tools":["Read","a\u0002b"]}'
for mode in py jq; do
    t_emit "$mode" agy nasty "$t18_nasty" "$WORKDIR/t18n-$mode" || true
    f="$WORKDIR/t18n-$mode/yakos-nasty/SKILL.md"
    if [ -f "$f" ] && ! t_has_model_line "$f" \
       && grep -Fq 'description: "d\u0001e \"q\" \\ \u001B"' "$f" \
       && grep -Fq 'tools: ["Read", "a\u0002b"]' "$f" \
       && ! head -n 6 "$f" | LC_ALL=C grep -q "[$(printf '\001-\010\013\014\016-\037\177')]"; then
        ok "agy ($mode): frontmatter escapes control characters, lists the tools, omits the tier model"
    else
        fail "agy ($mode): frontmatter wrong for a description with control characters"
        head -n 8 "$f" 2>/dev/null | cat -v | sed 's/^/    /' >&2
    fi
done
if cmp -s "$WORKDIR/t18n-py/yakos-nasty/SKILL.md" "$WORKDIR/t18n-jq/yakos-nasty/SKILL.md"; then
    ok "agy: the jq fallback writes the same bytes as the python path"
else
    fail "agy: the jq fallback and the python path differ"
    diff "$WORKDIR/t18n-py/yakos-nasty/SKILL.md" "$WORKDIR/t18n-jq/yakos-nasty/SKILL.md" | sed 's/^/    /' >&2 || true
fi
for mode in py jq; do
    for field in prompt tool; do
        case "$field" in
            prompt) nul_json='{"description":"d","prompt":"a\u0000b"}' ;;
            tool)   nul_json='{"description":"d","prompt":"p","tools":["a\u0000b"]}' ;;
        esac
        rc=0
        t_emit "$mode" agy nul "$nul_json" "$WORKDIR/t18nul-$mode-$field" || rc=$?
        if [ "$rc" -eq 0 ] && [ ! -e "$WORKDIR/t18nul-$mode-$field/yakos-nul" ] \
           && grep -q 'NUL byte' "$WORKDIR/t18nul-$mode-$field/.last.err"; then
            ok "agy ($mode): a NUL byte in the $field is refused and leaves no directory behind"
        else
            fail "agy ($mode): NUL in the $field mishandled (status $rc)"
        fi
    done
done

# ---- 19. Go materializer parity under YAKOS_IMPL=go (K-133 / K-134) ----------
echo
echo "Test 19: Go dispatch materializes byte-identical agent files and runs sandboxed"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
if [ ! -x "$GO_BINARY" ] && [ -n "${YAKOS_REQUIRE_GO_BINARY:-}" ]; then
    # CI sets YAKOS_REQUIRE_GO_BINARY after building bin/yakos, so this check cannot
    # silently turn into a skip (it did once: the job never built the binary).
    fail "$GO_BINARY is not built and YAKOS_REQUIRE_GO_BINARY is set; run 'make build' first"
elif [ ! -x "$GO_BINARY" ]; then
    echo "  [skip] $GO_BINARY not built (run 'make build'); the Go half of the parity check is skipped"
else
    t19="$WORKDIR/t19"
    mkdir -p "$t19/home" "$t19/state" "$t19/shim" "$t19/rec" "$t19/proj/.claude/agents"
    # Shaped like a real agent on purpose: a blank line after the frontmatter (the bash
    # composer keeps it in the prompt, the Go composer drops it) and a model alias (the
    # bash composer resolves it to the tier sonnet). The emitters absorb both, so the
    # files still match; a probe that avoided them would hide exactly that.
    cat > "$t19/proj/.claude/agents/parity-probe.md" <<'AGENT_EOF'
---
id: parity-probe
role: specialist
domain: parity
mode: [feature]
tools: [Read, Edit]
model: balanced
references: []
---

# Parity probe

## Purpose

Probe agent used by the runtime fixtures to compare the bash and Go materializers.

## Rules

- Quote "carefully" and keep C:\paths intact.
AGENT_EOF
    for rt in codex agy; do
        cat > "$t19/shim/$rt" <<SHIM_EOF
#!/bin/sh
printf '%s\n' "\$@" > "$t19/rec/$rt.argv"
echo "fake $rt output"
SHIM_EOF
        chmod +x "$t19/shim/$rt"
    done
    t19_env() {
        env -u YAKOS_LIB HOME="$t19/home" YAKOS_DISPATCH_LOG="$t19/state" PATH="$t19/shim:$PATH" \
            YAKOS_IMPL=go YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" "$@"
    }
    # bash reference: the composed agent JSON through the bash emitters
    t19_composed="$(bash -c '
        set -eu
        export YAKOS_ROOT="'"$REPO_ROOT"'" YAKOS_LIB="'"$YAKOS_LIB"'"
        . "$YAKOS_LIB/compat.sh"
        . "$YAKOS_LIB/agents-compose.sh"
        yk_agents_compose "$YAKOS_ROOT" "'"$t19"'/proj" 2>/dev/null
    ')"
    # The probe plus real framework agents: general-codex is the one pinned to an alias
    # (composed as the tier sonnet by bash), the others carry tiers and the blank line
    # after the frontmatter that every framework agent file has.
    t19_agents="parity-probe general-codex general-agy architect backend security-reviewer planner"
    mkdir -p "$t19/ref/codex" "$t19/ref/agy"
    for aid in $t19_agents; do
        t19_agent_json="$(printf '%s' "$t19_composed" | jq -c --arg n "$aid" '.[$n]')"
        yk_rt_codex_emit_toml "$aid" "$t19_agent_json" "$t19/ref/codex" >/dev/null 2>&1
        yk_rt_agy_emit_md "$aid" "$t19_agent_json" "$t19/ref/agy" >/dev/null 2>&1
    done

    for aid in $t19_agents; do
        for rt in codex agy; do
            if ! t19_env dispatch "$aid" "hello world" --runtime "$rt" --project "$t19/proj" >"$t19/$aid.$rt.out" 2>"$t19/$aid.$rt.err"; then
                fail "$aid on $rt: yakos dispatch (YAKOS_IMPL=go) failed"
                sed 's/^/    /' "$t19/$aid.$rt.err" >&2
            fi
        done
        if cmp -s "$t19/proj/.codex/agents/yakos-$aid.toml" "$t19/ref/codex/yakos-$aid.toml"; then
            ok "$aid: Go-materialized codex TOML is byte-identical to the bash emitter's"
        else
            fail "$aid: Go and bash codex TOML differ"
            diff "$t19/proj/.codex/agents/yakos-$aid.toml" "$t19/ref/codex/yakos-$aid.toml" | sed 's/^/    /' >&2 || true
        fi
        if cmp -s "$t19/proj/.agents/skills/yakos-$aid/SKILL.md" "$t19/ref/agy/yakos-$aid/SKILL.md" \
           && cmp -s "$t19/proj/.agents/skills/yakos-$aid/.gitignore" "$t19/ref/agy/yakos-$aid/.gitignore"; then
            ok "$aid: Go-materialized agy SKILL.md and .gitignore are byte-identical to the bash emitter's"
        else
            fail "$aid: Go and bash agy skill files differ"
            diff "$t19/proj/.agents/skills/yakos-$aid/SKILL.md" "$t19/ref/agy/yakos-$aid/SKILL.md" | sed 's/^/    /' >&2 || true
        fi
        if t_has_model_line "$t19/proj/.codex/agents/yakos-$aid.toml" || t_has_model_line "$t19/proj/.agents/skills/yakos-$aid/SKILL.md"; then
            fail "$aid: a Go-materialized agent file names a model"
        fi
    done
    ok "no Go-materialized agent file names a model"
    if grep -qx -- '--sandbox' "$t19/rec/codex.argv" && grep -qx 'workspace-write' "$t19/rec/codex.argv" \
       && grep -qx 'approval_policy="never"' "$t19/rec/codex.argv" \
       && ! grep -q -- 'dangerously-bypass' "$t19/rec/codex.argv"; then
        ok "codex argv: --sandbox workspace-write with a non-prompting approval policy, no bypass flag"
    else
        fail "codex argv is not sandboxed by default:"; sed 's/^/    /' "$t19/rec/codex.argv" >&2
    fi
    if grep -qx -- '--sandbox' "$t19/rec/agy.argv" && grep -qx 'stream-json' "$t19/rec/agy.argv"; then
        ok "agy argv: --sandbox and --output-format stream-json"
    else
        fail "agy argv is missing --sandbox or stream-json:"; sed 's/^/    /' "$t19/rec/agy.argv" >&2
    fi

    # An operator-owned file at the generated path is left alone by the Go side too.
    mkdir -p "$t19/proj2/.claude/agents" "$t19/proj2/.codex/agents"
    cp "$t19/proj/.claude/agents/parity-probe.md" "$t19/proj2/.claude/agents/"
    printf 'name = "mine"\ndescription = "hand written"\n' > "$t19/proj2/.codex/agents/yakos-parity-probe.toml"
    cp "$t19/proj2/.codex/agents/yakos-parity-probe.toml" "$t19/own.before"
    t19_env dispatch parity-probe "hi" --runtime codex --project "$t19/proj2" >/dev/null 2>"$t19/own.err" || true
    if cmp -s "$t19/proj2/.codex/agents/yakos-parity-probe.toml" "$t19/own.before" && grep -q "no yakos-generated marker" "$t19/own.err"; then
        ok "Go dispatch leaves an operator's agent file alone and says so"
    else
        fail "Go dispatch overwrote an operator's agent file or did not explain the skip"
    fi

    # The bypass is only granted by the owner-only policy in the user's own
    # ~/.yakos-state; a project-controlled state directory is ignored (K-129).
    mkdir -p "$t19/home/.yakos-state" "$t19/planted"
    printf 'allow_unsandboxed_runtimes: [codex]\n' > "$t19/planted/router-policy.yml"
    chmod 600 "$t19/planted/router-policy.yml"
    env -u YAKOS_LIB HOME="$t19/home" YAKOS_DISPATCH_LOG="$t19/planted" PATH="$t19/shim:$PATH" YAKOS_IMPL=go \
        YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" dispatch parity-probe "x" --runtime codex --project "$t19/proj" >/dev/null 2>&1 || true
    if grep -qx -- '--sandbox' "$t19/rec/codex.argv"; then
        ok "a router-policy.yml in a relocated state directory does not unsandbox codex"
    else
        fail "a relocated state directory unsandboxed codex"
    fi
    printf 'allow_unsandboxed_runtimes: [codex]\n' > "$t19/home/.yakos-state/router-policy.yml"
    chmod 600 "$t19/home/.yakos-state/router-policy.yml"
    env -u YAKOS_LIB HOME="$t19/home" YAKOS_DISPATCH_LOG="$t19/state" PATH="$t19/shim:$PATH" YAKOS_IMPL=go \
        YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" dispatch parity-probe "x" --runtime codex --project "$t19/proj" >/dev/null 2>"$t19/bypass.err" || true
    if grep -qx -- '--dangerously-bypass-approvals-and-sandbox' "$t19/rec/codex.argv" && grep -q "WITHOUT its sandbox" "$t19/bypass.err"; then
        ok "the owner-only ~/.yakos-state/router-policy.yml re-enables the bypass and says so on stderr"
    else
        fail "the owner-only policy did not enable the bypass"
    fi
    chmod 666 "$t19/home/.yakos-state/router-policy.yml"
    env -u YAKOS_LIB HOME="$t19/home" YAKOS_DISPATCH_LOG="$t19/state" PATH="$t19/shim:$PATH" YAKOS_IMPL=go \
        YAKOS_ROOT="$REPO_ROOT" "$GO_BINARY" dispatch parity-probe "x" --runtime codex --project "$t19/proj" >/dev/null 2>/dev/null || true
    if grep -qx -- '--sandbox' "$t19/rec/codex.argv"; then
        ok "a world-writable policy file is ignored and codex stays sandboxed"
    else
        fail "a world-writable policy file unsandboxed codex"
    fi
fi

# ---- 20. claude-sdk hard gate: the bash twin of the Go sidecar gate (K-137) ----
echo
echo "Test 20: the claude-sdk runtime refuses to run without an Anthropic API key (K-137)"
# Anthropic's terms (2026-02-19) allow a subscription's OAuth only in Claude Code and
# claude.ai, not in the Agent SDK, and this runtime is the Python Agent SDK. The dispatch
# verb must refuse before it composes agents or starts python, with a one-line reason that
# carries no token material, and the python it does start must not inherit subscription
# OAuth variables. Twins: cli-go/internal/runtime/sdk_env.go and sidecar.mjs.
t20="$WORKDIR/t20"
mkdir -p "$t20/home" "$t20/py" "$t20/proj" "$t20/root/lib/agents"
t20_secret="T20SECRET0123456789"
# A one-agent roster as the framework root. The real roster is about 196 KB, which the
# adapter passes to python in one environment variable; Linux caps one string at 128 KiB
# (MAX_ARG_STRLEN) and the exec fails with E2BIG, so a test of the gate must not depend on it.
cat > "$t20/root/lib/agents/probe.md" <<'AGENT_EOF'
---
id: probe
role: specialist
domain: test
mode: [feature]
tools: [Read]
model: haiku
references: []
---

# Probe

## Purpose

A one-agent roster for the claude-sdk gate fixture.
AGENT_EOF
cat > "$t20/py/fakepython" <<'PY_EOF'
#!/bin/sh
# Stands in for the interpreter: records the NAMES of the variables it was started with.
env | sed 's/=.*//' | sort > "$T20_SEEN"
echo "fake sdk output"
PY_EOF
chmod +x "$t20/py/fakepython"

if bash -n "$YAKOS_LIB/runtimes/claude-sdk.sh"; then
    ok "claude-sdk.sh parses (bash -n)"
else
    fail "claude-sdk.sh does not parse"
fi

# t20_dispatch VAR=value...: the real dispatch verb, in an otherwise empty environment.
t20_dispatch() {
    rm -f "$t20/seen.env" "$t20/out" "$t20/err"
    env -i HOME="$t20/home" PATH="$PATH" YAKOS_ROOT="$t20/root" YAKOS_LIB="$YAKOS_LIB" \
        YAKOS_PYTHON="$t20/py/fakepython" T20_SEEN="$t20/seen.env" "$@" \
        bash -c '. "$YAKOS_LIB/runtimes/claude-sdk.sh"; yk_rt_claude_sdk_dispatch "$1" probe "hello"' _ "$t20/proj" \
        >"$t20/out" 2>"$t20/err"
}

# t20_expect_refusal <label> <text the reason must contain> [VAR=value...]
t20_expect_refusal() {
    local label="$1" want="$2" rc=0
    shift 2
    t20_dispatch "$@" || rc=$?
    if [ "$rc" -eq 0 ]; then
        fail "$label: the dispatch ran without a usable API key"
    elif [ -e "$t20/seen.env" ]; then
        fail "$label: python was started although the gate refused"
    elif ! grep -q 'ANTHROPIC_API_KEY' "$t20/err" || ! grep -q "$want" "$t20/err" || ! grep -q 'claude runtime' "$t20/err"; then
        fail "$label: the reason must name ANTHROPIC_API_KEY and the claude runtime: $(cat "$t20/err")"
    elif [ "$(grep -c . "$t20/err")" -ne 1 ]; then
        fail "$label: the reason must be one line: $(cat "$t20/err")"
    elif [ -s "$t20/out" ]; then
        fail "$label: stdout must stay empty"
    elif grep -q "$t20_secret" "$t20/err" "$t20/out"; then
        fail "$label: token material was echoed"
    else
        ok "$label"
    fi
}

t20_expect_refusal "no key: refused before python starts" "is not set"
t20_expect_refusal "empty key: refused" "is not set" ANTHROPIC_API_KEY=
t20_expect_refusal "blank key: refused" "is not set" "ANTHROPIC_API_KEY=   "
t20_expect_refusal "a subscription token alone is not a key" "is not set" "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-$t20_secret"
t20_expect_refusal "an OAuth token in the key slot is refused and not echoed" "OAuth token" "ANTHROPIC_API_KEY=sk-ant-oat01-$t20_secret"
t20_expect_refusal "an OAuth refresh token in the key slot is refused" "OAuth token" "ANTHROPIC_API_KEY=SK-ANT-ORT01-$t20_secret"
# The refusal comes before anything is composed. A framework root with no agents makes the
# compose step fail with a message of its own, so the key reason still being the only line
# shows the gate ran first. A gate placed after the compose step prints the compose error.
mkdir -p "$t20/emptyroot/lib/agents"
t20_expect_refusal "no key and nothing to compose: the refusal still comes first" "is not set" YAKOS_ROOT="$t20/emptyroot"

t20_rc=0
t20_dispatch "ANTHROPIC_API_KEY=sk-ant-api03-t20-fake-key" \
    "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-$t20_secret" "CLAUDE_CODE_OAUTH_REFRESH_TOKEN=sk-ant-ort01-$t20_secret" \
    "CLAUDE_CODE_OAUTH_SCOPES=user:inference" "claude_code_oauth_client_id=lowercase-name" \
    "ANTHROPIC_AUTH_TOKEN=sk-ant-oat01-$t20_secret" "T20_MISFILED=Bearer sk-ant-ort01-$t20_secret" \
    "T20.DOTTED=Bearer sk-ant-oat01-$t20_secret" "t20_lower_misfiled=SK-ANT-ORT01-$t20_secret" \
    "YAKOS_T20_PROSE=never paste sk-ant-oat or SK-ANT-ORT tokens" \
    "T20_YAKOS_MID=Bearer sk-ant-oat01-$t20_secret" "yakos_t20_lower=Bearer sk-ant-oat01-$t20_secret" \
    "T20_BENIGN=hello" || t20_rc=$?
if [ "$t20_rc" -ne 0 ] || ! grep -q 'fake sdk output' "$t20/out"; then
    fail "with an API key the dispatch must reach python (exit $t20_rc): $(cat "$t20/err")"
else
    ok "with an API key the dispatch reaches python"
    # claude_code_oauth_client_id is a lowercase name with a non-token value, T20.DOTTED a
    # name a shell cannot hold as a variable (bash 5 passes it to children, bash 3.2 drops
    # it, so the check is "never inherited" on both), t20_lower_misfiled a token in a
    # lowercase name and upper-case token marker. A name that starts with YAKOS_ (exact case) is
    # yakOS's own and is never judged by value (YAKOS_T20_PROSE mentions the prefixes in prose,
    # as a composed agent roster can); a name that merely contains YAKOS_ (T20_YAKOS_MID) or
    # spells it in lowercase (yakos_t20_lower) is an ordinary name and a token in it goes.
    for banned in CLAUDE_CODE_OAUTH_TOKEN CLAUDE_CODE_OAUTH_REFRESH_TOKEN CLAUDE_CODE_OAUTH_SCOPES claude_code_oauth_client_id \
                  ANTHROPIC_AUTH_TOKEN T20_MISFILED T20.DOTTED t20_lower_misfiled T20_YAKOS_MID yakos_t20_lower; do
        if grep -qx "$banned" "$t20/seen.env"; then
            fail "python inherited $banned (OAuth material must not reach the Agent SDK)"
        else
            ok "python does not inherit $banned"
        fi
    done
    for kept in ANTHROPIC_API_KEY T20_BENIGN YAKOS_T20_PROSE; do
        if grep -qx "$kept" "$t20/seen.env"; then
            ok "python still receives $kept"
        else
            fail "python lost $kept"
        fi
    done
fi

# Nothing to scrub: the empty scrub list must expand cleanly under `set -u` on bash 3.2.
t20_rc=0
t20_dispatch "ANTHROPIC_API_KEY=sk-ant-api03-t20-fake-key" || t20_rc=$?
if [ "$t20_rc" -eq 0 ] && grep -q 'fake sdk output' "$t20/out"; then
    ok "with an API key and no OAuth variables the dispatch reaches python"
else
    fail "with an API key and nothing to scrub the dispatch must reach python (exit $t20_rc): $(cat "$t20/err")"
fi

# The second anchor: claude-sdk-dispatch.py checks the key itself, before it reads its
# other inputs or imports the SDK, so a python started any other way cannot bypass the
# shell gate.
if command -v python3 >/dev/null 2>&1; then
    t20_py() {
        rm -f "$t20/pyout" "$t20/pyerr"
        env -i HOME="$t20/home" PATH="$PATH" "$@" python3 "$YAKOS_LIB/runtimes/claude-sdk-dispatch.py" \
            </dev/null >"$t20/pyout" 2>"$t20/pyerr"
    }
    t20_py_expect_refusal() {
        local label="$1" rc=0
        shift
        t20_py "$@" || rc=$?
        if [ "$rc" -ne 78 ]; then
            fail "claude-sdk-dispatch.py $label: exit $rc, want 78: $(cat "$t20/pyerr")"
        elif [ -s "$t20/pyout" ]; then
            fail "claude-sdk-dispatch.py $label: stdout must stay empty"
        elif [ "$(grep -c . "$t20/pyerr")" -ne 1 ] || ! grep -q 'ANTHROPIC_API_KEY' "$t20/pyerr" || ! grep -q 'refusing to run' "$t20/pyerr"; then
            fail "claude-sdk-dispatch.py $label: want one line naming ANTHROPIC_API_KEY: $(cat "$t20/pyerr")"
        elif grep -q "$t20_secret" "$t20/pyerr"; then
            fail "claude-sdk-dispatch.py $label: token material was echoed"
        else
            ok "claude-sdk-dispatch.py $label"
        fi
    }
    t20_py_expect_refusal "refuses with no key, before reading its other inputs"
    t20_py_expect_refusal "refuses a blank key" "ANTHROPIC_API_KEY=  "
    t20_py_expect_refusal "refuses an OAuth token as the key and does not echo it" "ANTHROPIC_API_KEY=sk-ant-oat01-$t20_secret"
    t20_rc=0
    t20_py "ANTHROPIC_API_KEY=sk-ant-api03-t20-fake-key" || t20_rc=$?
    if [ "$t20_rc" -eq 1 ] && grep -q 'YAKOS_AGENT_ID env var required' "$t20/pyerr"; then
        ok "claude-sdk-dispatch.py lets an API key through to its normal input checks"
    else
        fail "claude-sdk-dispatch.py with a key must reach its input checks (exit $t20_rc): $(cat "$t20/pyerr")"
    fi
    # -B: importing the script must not leave a __pycache__ next to it in the checkout.
    # End to end, offline: stand-in claude_agent_sdk and anyio modules (first on PYTHONPATH,
    # so they win over real ones) let main() run. The stand-in query() records the NAMES in
    # the environment the SDK would hand to Claude Code.
    mkdir -p "$t20/fakesdk"
    cat > "$t20/fakesdk/anyio.py" <<'PY_EOF'
import asyncio


def run(func, *args):
    return asyncio.run(func(*args))
PY_EOF
    cat > "$t20/fakesdk/claude_agent_sdk.py" <<'PY_EOF'
import os


class AgentDefinition:
    def __init__(self, **kw):
        self.kw = kw


class ClaudeAgentOptions:
    def __init__(self, **kw):
        self.kw = kw


class TextBlock:
    def __init__(self, text):
        self.text = text


class AssistantMessage:
    def __init__(self, content):
        self.content = content


class ResultMessage:
    total_cost_usd = 0.0


async def query(prompt, options):
    with open(os.environ["T20_SEEN"], "w") as f:
        f.write("\n".join(sorted(os.environ)) + "\n")
    yield AssistantMessage([TextBlock("fake sdk text")])
    yield ResultMessage()
PY_EOF
    t20_rc=0
    rm -f "$t20/seen.env"
    printf 'hello\n' | env -i HOME="$t20/home" PATH="$PATH" PYTHONPATH="$t20/fakesdk" T20_SEEN="$t20/seen.env" \
        YAKOS_AGENT_ID=probe YAKOS_PROJECT_DIR="$t20/proj" \
        YAKOS_AGENTS_JSON='{"probe":{"prompt":"p. Never paste a sk-ant-oat or sk-ant-ort token."}}' \
        ANTHROPIC_API_KEY=sk-ant-api03-t20-fake-key \
        "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-$t20_secret" "claude_code_oauth_scopes=user:inference" \
        "T20_MISFILED=Bearer sk-ant-ort01-$t20_secret" T20_BENIGN=hello \
        "T20_PROSE_NOTE=never paste sk-ant-oat tokens" "YAKOS_T20_NOTE=never paste sk-ant-oat tokens" \
        "T20_YAKOS_MID=Bearer sk-ant-oat01-$t20_secret" "yakos_t20_lower=Bearer sk-ant-oat01-$t20_secret" \
        python3 -B "$YAKOS_LIB/runtimes/claude-sdk-dispatch.py" >"$t20/pyout" 2>"$t20/pyerr" || t20_rc=$?
    if [ "$t20_rc" -ne 0 ] || ! grep -q 'fake sdk text' "$t20/pyout"; then
        fail "claude-sdk-dispatch.py with a key must run end to end against a stand-in SDK (exit $t20_rc): $(cat "$t20/pyerr")"
    else
        ok "claude-sdk-dispatch.py runs end to end with an API key"
        # The roster above mentions the token prefixes in prose and still reached the SDK: the
        # strip must not judge a YAKOS_ name by value. T20_PROSE_NOTE, which mentions them the
        # same way under an ordinary name, goes with the other OAuth-looking values.
        for banned in CLAUDE_CODE_OAUTH_TOKEN claude_code_oauth_scopes T20_MISFILED T20_PROSE_NOTE T20_YAKOS_MID yakos_t20_lower; do
            if grep -qx "$banned" "$t20/seen.env"; then
                fail "the SDK would inherit $banned from claude-sdk-dispatch.py"
            else
                ok "claude-sdk-dispatch.py hands the SDK no $banned"
            fi
        done
        if grep -qx ANTHROPIC_API_KEY "$t20/seen.env" && grep -qx T20_BENIGN "$t20/seen.env"; then
            ok "claude-sdk-dispatch.py still hands the SDK the key and the rest of the environment"
        else
            fail "claude-sdk-dispatch.py dropped the key or an unrelated variable"
        fi
        if grep -qx YAKOS_T20_NOTE "$t20/seen.env"; then
            ok "claude-sdk-dispatch.py keeps a YAKOS_ variable that mentions a token prefix in prose"
        else
            fail "claude-sdk-dispatch.py dropped YAKOS_T20_NOTE: yakOS's own variables are not judged by value"
        fi
    fi
    # The hand-off the real adapter uses: dispatch composes the roster from the framework root
    # and passes it to the real script in YAKOS_AGENTS_JSON. A roster whose agent text mentions
    # a token prefix in prose must reach the SDK; the strip once emptied it and the dispatch
    # died with "YAKOS_AGENTS_JSON env var required".
    mkdir -p "$t20/rootprose/lib/agents"
    sed 's/^A one-agent roster for the claude-sdk gate fixture\.$/Never paste a sk-ant-oat or sk-ant-ort token into a prompt./' \
        "$t20/root/lib/agents/probe.md" > "$t20/rootprose/lib/agents/probe.md"
    cat > "$t20/py/sdkpython" <<'PY_EOF'
#!/bin/sh
# Runs the real claude-sdk-dispatch.py against the stand-in SDK modules.
PYTHONPATH="$T20_FAKESDK" exec python3 -B "$@"
PY_EOF
    chmod +x "$t20/py/sdkpython"
    t20_rc=0
    t20_dispatch "ANTHROPIC_API_KEY=sk-ant-api03-t20-fake-key" YAKOS_ROOT="$t20/rootprose" \
        YAKOS_PYTHON="$t20/py/sdkpython" T20_FAKESDK="$t20/fakesdk" || t20_rc=$?
    if [ "$t20_rc" -eq 0 ] && grep -q 'fake sdk text' "$t20/out"; then
        ok "a roster that mentions a token prefix in prose reaches the SDK through the real dispatch"
    else
        fail "a roster that mentions a token prefix in prose must reach the SDK (exit $t20_rc): $(cat "$t20/err")"
    fi
    t20_scrubbed="$(python3 -B - "$YAKOS_LIB/runtimes/claude-sdk-dispatch.py" <<'PY_EOF'
import importlib.util, sys
spec = importlib.util.spec_from_file_location("claude_sdk_dispatch", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
env = {"ANTHROPIC_API_KEY": "sk-ant-api03-k", "CLAUDE_CODE_OAUTH_TOKEN": "x",
       "claude_code_oauth_scopes": "y", "MISFILED": "Bearer sk-ant-oat01-z", "PATH": "/usr/bin",
       "YAKOS_AGENTS_JSON": '{"a": "never paste sk-ant-oat tokens"}',
       "T_YAKOS_MID": "Bearer sk-ant-oat01-z", "yakos_lower": "Bearer sk-ant-oat01-z"}
mod.scrub_oauth_env(env)
print(",".join(sorted(env)))
PY_EOF
)" || t20_scrubbed="python failed: $t20_scrubbed"
    if [ "$t20_scrubbed" = "ANTHROPIC_API_KEY,PATH,YAKOS_AGENTS_JSON" ]; then
        ok "claude-sdk-dispatch.py scrub_oauth_env drops OAuth names and values, keeps the rest and YAKOS_ names"
    else
        fail "claude-sdk-dispatch.py scrub_oauth_env left: $t20_scrubbed"
    fi
else
    echo "  [skip] python3 not installed; the claude-sdk-dispatch.py second anchor is not exercised"
fi

# ---- summary -----------------------------------------------------------------
echo
echo "yakos runtime fixtures: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
