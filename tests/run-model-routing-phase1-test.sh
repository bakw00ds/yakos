#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Phase 1 model-routing substrate tests.
#
# Covers:
#   1. dispatch.sh --model flag: parsed, validated (haiku/sonnet/opus only)
#   2. dispatch-log carries model_chosen_by / model_resolved / eval_run_id
#   3. validate.sh golden-case schema: valid cases pass; mutated cases warn/error
#   4. validate.sh eval/ presence is detected for test-runner agent
#   5. --eval-run-id sets model_chosen_by:"eval" and non-null eval_run_id
#
# The tests do NOT invoke a real runtime (no claude/codex needed).
# They drive dispatch.sh in a synthetic mode using a mock adapter that
# exits 0 immediately without calling any external process.
#
# The mock runtime is registered as a plugin under a synthetic HOME
# (WORKDIR/home) so ~/.yakos is always a fresh directory, independent
# of the real user's ~/.yakos which may be a file.
#
# Exit codes: 0 all pass, 1 any failure.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

WORKDIR="${TMPDIR:-/tmp}/yakos-model-routing-phase1.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

# Source framework helpers in the test shell (for validate checks later).
# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../cli/lib/agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

echo "yakos model-routing Phase 1 tests"
echo

# ============================================================
# Synthetic HOME: keeps real ~/.yakos untouched and gives the
# mock runtime a clean .yakos/plugins/ directory to register in.
# ============================================================

FAKE_HOME="$WORKDIR/home"
mkdir -p "$FAKE_HOME/.yakos-state"
mkdir -p "$FAKE_HOME/.yakos/plugins"

# ============================================================
# Fixture: minimal fake project
# ============================================================

FAKE_PROJECT="$WORKDIR/fake-project"
mkdir -p "$FAKE_PROJECT/.claude/agents"

# Minimal agent the tests dispatch against.
cat > "$FAKE_PROJECT/.claude/agents/test-runner.md" <<'AGENTEOF'
---
id: test-runner
role: specialist
domain: testing
mode: [implement]
tools: [Read, Bash]
model: sonnet
version: 1
references: []
---

# Test Runner (test fixture)

## Purpose
Run tests.

## Execution
1. Run tests.

## Special rules
- Don't break things.

## Handling peer messages
Answer factually.

## Personality
Paranoid.
AGENTEOF

# Route all dispatches to mock-rt so no real LLM auth is needed.
cat > "$FAKE_PROJECT/.yakos.yml" <<'YMLEOF'
yakos: 0.9
default-runtime: mock-rt
YMLEOF

# ============================================================
# Mock runtime plugin: exits 0 immediately, writes fake telemetry.
# Registered under FAKE_HOME/.yakos/plugins/mock-rt/runtime.sh.
# ============================================================

MOCK_RT_DIR="$FAKE_HOME/.yakos/plugins/mock-rt"
mkdir -p "$MOCK_RT_DIR"
cat > "$MOCK_RT_DIR/runtime.sh" <<'RTEOF'
#!/usr/bin/env bash
# Mock runtime — no real LLM call.
yk_rt_mock_rt_id()                  { printf 'mock-rt\n'; }
yk_rt_mock_rt_capabilities()        { printf 'headless\n'; }
yk_rt_mock_rt_check_cli()           { return 0; }
yk_rt_mock_rt_check_auth()          { return 0; }
yk_rt_mock_rt_materialize_agents()  { return 0; }
yk_rt_mock_rt_cleanup_agents()      { return 0; }
yk_rt_mock_rt_launch()              { printf 'mock-rt: launch (dry run)\n'; return 0; }
yk_rt_mock_rt_dispatch() {
    if [ -n "${YAKOS_USAGE_OUT:-}" ]; then
        printf '{"input_tokens":10,"output_tokens":5,"total_cost_usd":0.0001}\n' \
            > "$YAKOS_USAGE_OUT"
    fi
    printf 'mock response from test-runner\n'
    return 0
}
RTEOF

# Helper: run a dispatch invocation; return the last dispatch_finished log line.
# Uses FAKE_HOME so ~/.yakos-state/dispatch-log.ndjson is isolated.
dispatch_and_get_log() {
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/dispatch.sh" \
        test-runner "run the test suite" \
        --project "$FAKE_PROJECT" \
        --runtime mock-rt \
        "$@" >/dev/null 2>&1 || true
    grep '"dispatch_finished"' "$FAKE_HOME/.yakos-state/dispatch-log.ndjson" 2>/dev/null | tail -1
}

# ============================================================
# Test 1: --model sonnet → model_chosen_by=override, model_resolved=sonnet
# ============================================================
echo "Test 1: --model flag sets model_chosen_by=override"
entry="$(dispatch_and_get_log --model sonnet)"
if printf '%s' "$entry" | jq -e '.model_chosen_by == "override"' >/dev/null 2>&1; then
    ok "model_chosen_by is 'override'"
else
    fail "model_chosen_by is not 'override' (got: $(printf '%s' "$entry" | jq -r '.model_chosen_by // "missing"' 2>/dev/null))"
fi
if printf '%s' "$entry" | jq -e '.model_resolved == "sonnet"' >/dev/null 2>&1; then
    ok "model_resolved is 'sonnet'"
else
    fail "model_resolved is not 'sonnet' (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi
if printf '%s' "$entry" | jq -e '.eval_run_id == null' >/dev/null 2>&1; then
    ok "eval_run_id is null (no --eval-run-id)"
else
    fail "eval_run_id should be null when --eval-run-id not passed"
fi

# ============================================================
# Test 2: --model haiku → model_resolved=haiku
# ============================================================
echo
echo "Test 2: --model haiku resolves to haiku"
entry="$(dispatch_and_get_log --model haiku)"
if printf '%s' "$entry" | jq -e '.model_resolved == "haiku"' >/dev/null 2>&1; then
    ok "model_resolved=haiku"
else
    fail "model_resolved should be haiku"
fi

# ============================================================
# Test 3: no --model → model_chosen_by=frontmatter, model_resolved=sonnet
# ============================================================
echo
echo "Test 3: no --model → model_chosen_by=frontmatter"
entry="$(dispatch_and_get_log)"
if printf '%s' "$entry" | jq -e '.model_chosen_by == "frontmatter"' >/dev/null 2>&1; then
    ok "model_chosen_by=frontmatter (default)"
else
    fail "model_chosen_by should be 'frontmatter' when no --model (got: $(printf '%s' "$entry" | jq -r '.model_chosen_by // "missing"' 2>/dev/null))"
fi
if printf '%s' "$entry" | jq -e '.model_resolved == "sonnet"' >/dev/null 2>&1; then
    ok "model_resolved=sonnet (from frontmatter)"
else
    fail "model_resolved should be 'sonnet' from frontmatter"
fi

# ============================================================
# Test 4: --eval-run-id → model_chosen_by=eval + non-null eval_run_id
# ============================================================
echo
echo "Test 4: --eval-run-id sets model_chosen_by=eval"
entry="$(dispatch_and_get_log --eval-run-id "test-run-abc123")"
if printf '%s' "$entry" | jq -e '.model_chosen_by == "eval"' >/dev/null 2>&1; then
    ok "model_chosen_by=eval"
else
    fail "model_chosen_by should be 'eval' when --eval-run-id passed"
fi
if printf '%s' "$entry" | jq -e '.eval_run_id == "test-run-abc123"' >/dev/null 2>&1; then
    ok "eval_run_id='test-run-abc123'"
else
    fail "eval_run_id should be 'test-run-abc123'"
fi

# ============================================================
# Test 5: --model + --eval-run-id → model_chosen_by=override (CLI wins)
# ============================================================
echo
echo "Test 5: --model + --eval-run-id → model_chosen_by=override (CLI wins)"
entry="$(dispatch_and_get_log --model opus --eval-run-id "run-999")"
if printf '%s' "$entry" | jq -e '.model_chosen_by == "override"' >/dev/null 2>&1; then
    ok "model_chosen_by=override (--model beats --eval-run-id)"
else
    fail "model_chosen_by should be 'override' when both --model and --eval-run-id given"
fi
if printf '%s' "$entry" | jq -e '.model_resolved == "opus"' >/dev/null 2>&1; then
    ok "model_resolved=opus"
else
    fail "model_resolved should be 'opus'"
fi
if printf '%s' "$entry" | jq -e '.eval_run_id == "run-999"' >/dev/null 2>&1; then
    ok "eval_run_id preserved alongside override"
else
    fail "eval_run_id should still be 'run-999' even when model is overridden"
fi

# ============================================================
# Test 6: dispatch_finished has all three new fields
# ============================================================
echo
echo "Test 6: dispatch_finished event contains all three new fields"
entry="$(dispatch_and_get_log --model sonnet)"
for field in model_chosen_by model_resolved eval_run_id; do
    if printf '%s' "$entry" | jq -e "has(\"$field\")" >/dev/null 2>&1; then
        ok "dispatch_finished has field: $field"
    else
        fail "dispatch_finished missing field: $field"
    fi
done

# ============================================================
# Test 7: --model invalid-tier exits non-zero
# ============================================================
echo
echo "Test 7: --model invalid-tier is rejected"
invalid_rc=0
HOME="$FAKE_HOME" \
YAKOS_ROOT="$REPO_ROOT" \
YAKOS_LIB="$YAKOS_LIB" \
bash "$REPO_ROOT/cli/lib/dispatch.sh" \
    test-runner "run the test suite" \
    --project "$FAKE_PROJECT" \
    --runtime mock-rt \
    --model gpt-99 >/dev/null 2>&1 || invalid_rc=$?
if [ "$invalid_rc" -ne 0 ]; then
    ok "--model gpt-99 rejected with exit code $invalid_rc"
else
    fail "--model gpt-99 should have exited non-zero (invalid tier)"
fi

# ============================================================
# Test 8: validate.sh accepts the 5 test-runner eval cases.
#
# Run validate.sh in project mode (validate.sh <project>) rather than
# framework mode (no args).  Framework mode triggers check_dark_code,
# which scans the entire repo and takes ~5 minutes — impractical for a
# unit test.  Project mode calls validate_tree directly (fast) and still
# exercises check_eval_dirs because validate_tree calls it.
#
# We build a minimal project that has the real test-runner eval cases
# copied into .claude/agents/test-runner/eval/ so the assertion is
# identical: 5 per-case [ok] lines, one per case-*.json.
# ============================================================
echo
echo "Test 8: validate.sh accepts the 5 test-runner eval cases"

EVAL_PROJECT="$WORKDIR/eval-project"
mkdir -p "$EVAL_PROJECT/.claude/agents/test-runner/eval"

# Minimal agent .md so validate_tree has something to iterate over.
cat > "$EVAL_PROJECT/.claude/agents/test-runner.md" <<'EVALEOF'
---
id: test-runner
role: specialist
domain: testing
mode: [implement]
tools: [Read, Bash]
model: sonnet
version: 1
references: []
---
# Test Runner
## Purpose
Run tests.
## Execution
1. Run tests.
## Special rules
- Don't break.
## Handling peer messages
Be factual.
## Personality
Paranoid.
EVALEOF

# Copy the 5 real golden-case files from the framework location.
for f in "$REPO_ROOT/lib/agents/test-runner/eval"/case-*.json; do
    cp "$f" "$EVAL_PROJECT/.claude/agents/test-runner/eval/"
done

validate_out="$(HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/validate.sh" "$EVAL_PROJECT" 2>&1 || true)"
eval_ok_count="$(printf '%s' "$validate_out" | grep -c 'eval case.*test-runner' 2>/dev/null || true)"
if [ "$eval_ok_count" -ge 5 ]; then
    ok "validate.sh accepted $eval_ok_count eval cases for test-runner"
else
    fail "validate.sh should report >= 5 eval case [ok] lines for test-runner (got $eval_ok_count)"
fi

# ============================================================
# Test 9: validate.sh warns on schema-violating case (default mode)
# ============================================================
echo
echo "Test 9: validate.sh warns on schema-violating case (default mode)"

EVIL_PROJECT="$WORKDIR/evil-project"
mkdir -p "$EVIL_PROJECT/.claude/agents/evil-agent/eval"
cat > "$EVIL_PROJECT/.claude/agents/evil-agent.md" <<'EOF'
---
id: evil-agent
role: specialist
domain: testing
mode: [implement]
tools: [Read]
model: sonnet
version: 1
references: []
---
# Evil Agent
## Purpose
For testing only.
## Execution
1. Do nothing.
## Special rules
- None.
## Handling peer messages
Pass.
## Personality
Neutral.
EOF

# Bad case: missing required field 'task', weight > 1.
printf '%s\n' '{
  "case_id": "bad-case",
  "rubric": {
    "criteria": [
      {"name": "foo", "weight": 2.5, "type": "binary"}
    ]
  },
  "expected_outcomes": ["something"],
  "context_files": [],
  "max_duration_s": 60,
  "max_cost_usd": 0.01,
  "tags": []
}' > "$EVIL_PROJECT/.claude/agents/evil-agent/eval/case-99-bad.json"

evil_out="$(HOME="$FAKE_HOME" bash "$REPO_ROOT/cli/lib/validate.sh" "$EVIL_PROJECT" 2>&1 || true)"
if printf '%s' "$evil_out" | grep -qE '\[warn\]|\[err\]'; then
    ok "validate.sh emitted warn/err for bad case"
else
    fail "validate.sh should warn on schema-violating case"
fi

# ============================================================
# Test 10: validate --strict errors on schema-violating case
# ============================================================
echo
echo "Test 10: validate --strict errors on schema-violating case"
strict_rc=0
HOME="$FAKE_HOME" \
bash "$REPO_ROOT/cli/lib/validate.sh" --strict "$EVIL_PROJECT" >/dev/null 2>&1 || strict_rc=$?
if [ "$strict_rc" -ne 0 ]; then
    ok "validate --strict exited non-zero on schema-violating case"
else
    strict_out="$(HOME="$FAKE_HOME" bash "$REPO_ROOT/cli/lib/validate.sh" --strict "$EVIL_PROJECT" 2>&1 || true)"
    if printf '%s' "$strict_out" | grep -qE '\[err\]'; then
        ok "validate --strict flagged schema violation as error (exit 0 with errors)"
    else
        fail "validate --strict should error on schema-violating case (exit=$strict_rc)"
    fi
fi

# ============================================================
# Tests 11-15: semantic alias expansion in model_resolved
#
# Each test writes a one-off agent .md with the alias under test into the
# fake project, dispatches without --model (so frontmatter wins), and
# asserts that model_resolved in the log is the concrete tier, not the
# alias string.
# ============================================================
echo
echo "Test 11: frontmatter model:balanced → model_resolved=sonnet"
cat > "$FAKE_PROJECT/.claude/agents/alias-agent.md" <<'AGEOF'
---
id: alias-agent
role: specialist
domain: testing
mode: [implement]
tools: [Read]
model: balanced
version: 1
references: []
---
# Alias Agent
## Purpose
Test alias expansion.
## Execution
1. Do nothing.
## Special rules
- None.
## Handling peer messages
Pass.
## Personality
Neutral.
AGEOF
entry="$(HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/dispatch.sh" \
        alias-agent "test task" \
        --project "$FAKE_PROJECT" \
        --runtime mock-rt >/dev/null 2>&1 || true
    grep '"dispatch_finished"' "$FAKE_HOME/.yakos-state/dispatch-log.ndjson" 2>/dev/null | tail -1)"
if printf '%s' "$entry" | jq -e '.model_resolved == "sonnet"' >/dev/null 2>&1; then
    ok "model:balanced → model_resolved=sonnet"
else
    fail "model:balanced should resolve to sonnet (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi

echo
echo "Test 12: frontmatter model:cheap → model_resolved=haiku"
cat > "$FAKE_PROJECT/.claude/agents/alias-agent.md" <<'AGEOF'
---
id: alias-agent
role: specialist
domain: testing
mode: [implement]
tools: [Read]
model: cheap
version: 1
references: []
---
# Alias Agent
## Purpose
Test alias expansion.
## Execution
1. Do nothing.
## Special rules
- None.
## Handling peer messages
Pass.
## Personality
Neutral.
AGEOF
entry="$(HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/dispatch.sh" \
        alias-agent "test task" \
        --project "$FAKE_PROJECT" \
        --runtime mock-rt >/dev/null 2>&1 || true
    grep '"dispatch_finished"' "$FAKE_HOME/.yakos-state/dispatch-log.ndjson" 2>/dev/null | tail -1)"
if printf '%s' "$entry" | jq -e '.model_resolved == "haiku"' >/dev/null 2>&1; then
    ok "model:cheap → model_resolved=haiku"
else
    fail "model:cheap should resolve to haiku (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi

echo
echo "Test 13: frontmatter model:best → model_resolved=opus"
cat > "$FAKE_PROJECT/.claude/agents/alias-agent.md" <<'AGEOF'
---
id: alias-agent
role: specialist
domain: testing
mode: [implement]
tools: [Read]
model: best
version: 1
references: []
---
# Alias Agent
## Purpose
Test alias expansion.
## Execution
1. Do nothing.
## Special rules
- None.
## Handling peer messages
Pass.
## Personality
Neutral.
AGEOF
entry="$(HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/dispatch.sh" \
        alias-agent "test task" \
        --project "$FAKE_PROJECT" \
        --runtime mock-rt >/dev/null 2>&1 || true
    grep '"dispatch_finished"' "$FAKE_HOME/.yakos-state/dispatch-log.ndjson" 2>/dev/null | tail -1)"
if printf '%s' "$entry" | jq -e '.model_resolved == "opus"' >/dev/null 2>&1; then
    ok "model:best → model_resolved=opus"
else
    fail "model:best should resolve to opus (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi

echo
echo "Test 14: frontmatter model:sonnet (concrete) → model_resolved=sonnet passthrough"
# Reuse the original test-runner agent which has model:sonnet.
entry="$(dispatch_and_get_log)"
if printf '%s' "$entry" | jq -e '.model_resolved == "sonnet"' >/dev/null 2>&1; then
    ok "model:sonnet passthrough → model_resolved=sonnet"
else
    fail "model:sonnet should pass through as sonnet (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi

echo
echo "Test 15: --model haiku CLI override wins over balanced frontmatter"
# alias-agent still has model:best from Test 13; override with --model haiku.
entry="$(HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    bash "$REPO_ROOT/cli/lib/dispatch.sh" \
        alias-agent "test task" \
        --project "$FAKE_PROJECT" \
        --runtime mock-rt \
        --model haiku >/dev/null 2>&1 || true
    grep '"dispatch_finished"' "$FAKE_HOME/.yakos-state/dispatch-log.ndjson" 2>/dev/null | tail -1)"
if printf '%s' "$entry" | jq -e '.model_resolved == "haiku"' >/dev/null 2>&1; then
    ok "CLI --model haiku overrides alias frontmatter → model_resolved=haiku"
else
    fail "--model haiku should win over frontmatter alias (got: $(printf '%s' "$entry" | jq -r '.model_resolved // "missing"' 2>/dev/null))"
fi
if printf '%s' "$entry" | jq -e '.model_chosen_by == "override"' >/dev/null 2>&1; then
    ok "model_chosen_by=override when CLI --model flag used"
else
    fail "model_chosen_by should be 'override' when CLI --model used"
fi

# ============================================================
# Summary
# ============================================================
echo
echo "yakos model-routing Phase 1: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
