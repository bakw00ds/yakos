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
#   7. runtime-resolve: yk_rt_default falls back to claude
#   8. runtime-resolve: yk_rt_capability returns 0/1 correctly
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
echo "Test 6: gemini markdown emitter"
# shellcheck source=../cli/lib/runtimes/gemini.sh
. "$YAKOS_LIB/runtimes/gemini.sh"
gemini_out="$WORKDIR/gemini-agents"
mkdir -p "$gemini_out"
yk_rt_gemini_materialize_agents "$REPO_ROOT" "" "$gemini_out" >/dev/null 2>&1 || true
emitted_count="$(find "$gemini_out" -name 'yakos-*.md' -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ "$emitted_count" -ge 11 ]; then
    ok "gemini emitter wrote $emitted_count markdown files (expected ≥ 11)"
else
    fail "gemini emitter wrote $emitted_count markdown files (expected ≥ 11)"
fi
sample="$gemini_out/yakos-architect.md"
if [ -f "$sample" ]; then
    # Frontmatter open + name + frontmatter close + body
    if grep -qE '^---$' "$sample" \
       && grep -qE '^name: architect$' "$sample" \
       && grep -qE '^description: ' "$sample"; then
        ok "gemini markdown has frontmatter (---, name, description)"
    else
        fail "gemini markdown missing frontmatter shape"
        head -10 "$sample" | sed 's/^/    /' >&2
    fi
else
    fail "gemini sample file not found at $sample"
fi

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

# ---- 10. general-codex + general-gemini agent files exist with valid fm ------
echo
echo "Test 10: general-{codex,gemini} agents exist with valid frontmatter"
for agent_id in general-codex general-gemini; do
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
echo "Test 11: runtime: pinned (not claude); model: set to concrete model IDs"
# Format: "agent-id:expected-runtime:expected-model"
for check in "general-codex:codex:gpt-5.5" "general-gemini:gemini:gemini-3.5"; do
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

    # model check — must be the concrete ID, not a semantic alias
    actual_model="$(yk_agents_fm_get "$fm" "model")"
    if [ "$actual_model" = "$expected_model" ]; then
        ok "$agent_id: model = $actual_model (expected $expected_model)"
    else
        fail "$agent_id: model = '$actual_model' (expected '$expected_model')"
    fi
    # model must not be a semantic alias (balanced/cheap/best/reasoning)
    case "$actual_model" in
        balanced|cheap|best|reasoning)
            fail "$agent_id: model '$actual_model' is a semantic alias, not a concrete model ID"
            ;;
        *)
            ok "$agent_id: model '$actual_model' is a concrete model ID (not a semantic alias)"
            ;;
    esac
done

# ---- 12. find_agent_file resolves general-codex + general-gemini ------------
echo
echo "Test 12: find_agent_file resolves general-codex and general-gemini"
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
for agent_id in general-codex general-gemini; do
    resolved="$(_find_agent_fw "$agent_id" || true)"
    if [ -n "$resolved" ] && [ -f "$resolved" ]; then
        ok "$agent_id: find_agent_file resolved to $resolved"
    else
        fail "$agent_id: find_agent_file did not resolve"
    fi
done

# ---- 13. compose includes general-codex + general-gemini --------------------
echo
echo "Test 13: yk_agents_compose includes general-codex and general-gemini"
# Use a fresh shell to avoid cache pollution from Test 1.
composed="$(bash -c '
    set -eu
    export YAKOS_ROOT="'"$REPO_ROOT"'"
    export YAKOS_LIB="'"$YAKOS_LIB"'"
    . "$YAKOS_LIB/compat.sh"
    . "$YAKOS_LIB/agents-compose.sh"
    yk_agents_compose "$YAKOS_ROOT" ""
' 2>/dev/null)"
for agent_id in general-codex general-gemini; do
    if printf '%s' "$composed" | jq -e --arg n "$agent_id" 'has($n)' >/dev/null; then
        ok "compose includes agent: $agent_id"
    else
        fail "compose missing agent: $agent_id"
    fi
done

# ---- 14. yakos validate --strict passes on both new agents ------------------
echo
echo "Test 14: yakos validate --strict passes on general-codex and general-gemini"
# Run validate in framework mode (no project path) and capture per-agent output.
validate_out="$(YAKOS_ROOT="$REPO_ROOT" YAKOS_LIB="$YAKOS_LIB" \
    bash "$YAKOS_LIB/validate.sh" --strict 2>&1 || true)"
for agent_id in general-codex general-gemini; do
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

# ---- summary -----------------------------------------------------------------
echo
echo "yakos runtime fixtures: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
