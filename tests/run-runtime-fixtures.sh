#!/usr/bin/env bash
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

# ---- summary -----------------------------------------------------------------
echo
echo "yakos runtime fixtures: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
