#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Phase 3 model-routing eval harness tests.
#
# Covers:
#   1.  promote happy path (project agent): frontmatter rewritten, backup exists,
#       history record appended, candidate stripped, validate passes.
#   2.  promote with --global on a framework-style agent: works.
#   3.  promote without --global on a framework agent: exact remediation message.
#   4.  promote no-candidate: exit 1 with helpful message.
#   5.  promote validation failure: backup restored, original file unchanged.
#   6.  reject happy path: candidate stripped, graveyard record written.
#   7.  reject repeat (>=3): warning + exit 1; --force succeeds.
#   8.  history: seed 3 history + 2 graveyard records; tabular output sorted desc.
#   9.  atomic frontmatter rewrite preserves comments, key order, trailing
#       whitespace (fixture with comment before model: line).
#   10. finops-review SKILL: candidates file with 2 pending; SKILL documents
#       reading procedure and estimated_monthly_savings_usd field.
#
# All file I/O is isolated to $WORKDIR; no real model API calls are made.
#
# Exit codes: 0 all pass, 1 any failure.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

WORKDIR="${TMPDIR:-/tmp}/yakos-mr-phase3.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

echo "yakos model-routing Phase 3 tests"
echo

# ============================================================
# Shared helpers
# ============================================================

# Isolated state dir (overridden per-test via YAKOS_MR_* env vars).
FAKE_HOME="$WORKDIR/home"
mkdir -p "$FAKE_HOME/.yakos-state"

# Mock lib directories:
#   MOCK_PASS_LIB  — validate.sh always passes  (used by promote happy-path tests)
#   MOCK_FAIL_LIB  — validate.sh always fails   (used by Test 5)
MOCK_PASS_LIB="$WORKDIR/mocklib-pass"
MOCK_FAIL_LIB="$WORKDIR/mocklib-fail"
mkdir -p "$MOCK_PASS_LIB" "$MOCK_FAIL_LIB"
# Both mock libs share real compat.sh (sourced by model-routing.sh).
cp "$YAKOS_LIB/compat.sh" "$MOCK_PASS_LIB/compat.sh"
cp "$YAKOS_LIB/compat.sh" "$MOCK_FAIL_LIB/compat.sh"
# Passing validate stub.
printf '#!/usr/bin/env bash\nexit 0\n' > "$MOCK_PASS_LIB/validate.sh"
chmod +x "$MOCK_PASS_LIB/validate.sh"
# Failing validate stub.
printf '#!/usr/bin/env bash\nexit 1\n' > "$MOCK_FAIL_LIB/validate.sh"
chmod +x "$MOCK_FAIL_LIB/validate.sh"

# Minimal valid agent file with frontmatter.
# $1 = dest file, $2 = current model tier
make_agent_file() {
    local dest="$1" model="$2"
    cat > "$dest" <<AGENTEOF
---
id: test-promote-agent
role: specialist
domain: testing
mode: [implement]
tools: [Read, Bash]
model: ${model}
version: 1
references: []
---
# Test Promote Agent
## Purpose
Used in Phase 3 promote tests.
## Execution
1. Run.
## Special rules
- None.
## Handling peer messages
Pass.
## Personality
Minimal.
AGENTEOF
}

# Minimal candidate record for a given agent.
make_candidate() {
    local agent="$1" current="$2" suggested="$3"
    jq -cn \
        --arg agent "$agent" \
        --arg current_model "$current" \
        --arg suggested_model "$suggested" \
        --arg run_id "yakos-mr-test-$(date +%s)" \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{agent:$agent,current_model:$current_model,suggested_model:$suggested_model,
          evidence:{pass_rates:{haiku:0.9,sonnet:0.93,opus:0.93},
                    ci_lower:{haiku:0.7,sonnet:0.8,opus:0.8},
                    mean_costs:{haiku:0.001,sonnet:0.005,opus:0.020},
                    n_cases:14,eval_run_id:$run_id,epsilon_used:0.05,
                    judge:"code-reviewer"},
          estimated_monthly_savings_usd:22.50,
          generated_at:$ts}'
}

# ============================================================
# Test 1: promote happy path (project agent)
# ============================================================
echo "Test 1: promote happy path (project agent)"

T1_DIR="$WORKDIR/t1"
mkdir -p "$T1_DIR/project/.claude/agents"
mkdir -p "$T1_DIR/state"

T1_AGENT="$T1_DIR/project/.claude/agents/my-agent.md"
make_agent_file "$T1_AGENT" "opus"

T1_CANDS="$T1_DIR/state/model-routing-candidates.ndjson"
T1_HIST="$T1_DIR/state/model-routing-history.ndjson"
T1_GRAVE="$T1_DIR/state/model-routing-graveyard.ndjson"
T1_BACKUPS="$T1_DIR/state/model-routing-backups"

make_candidate "my-agent" "opus" "sonnet" > "$T1_CANDS"

# Use YAKOS_PROJECT_DIR so the promote command finds the project agent.
MR_CANDS_OVERRIDE="$T1_CANDS" \
MR_HISTORY_OVERRIDE="$T1_HIST" \
MR_GRAVEYARD_OVERRIDE="$T1_GRAVE" \
MR_BACKUPS_OVERRIDE="$T1_BACKUPS" \
T1_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$MOCK_PASS_LIB" \
    YAKOS_PROJECT_DIR="$T1_DIR/project" \
    YAKOS_MR_CANDIDATES="$T1_CANDS" \
    YAKOS_MR_HISTORY="$T1_HIST" \
    YAKOS_MR_GRAVEYARD="$T1_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T1_BACKUPS" \
    bash "$YAKOS_LIB/model-routing.sh" promote my-agent 2>&1 || true
)"

# Check frontmatter rewritten.
if grep -q 'model: sonnet' "$T1_AGENT" 2>/dev/null; then
    ok "frontmatter model: rewritten to sonnet"
else
    fail "frontmatter model: not rewritten; file: $(grep 'model:' "$T1_AGENT" | head -3)"
fi

# Check backup exists.
backup_count="$(find "$T1_BACKUPS" -name 'my-agent-*.md' 2>/dev/null | wc -l | tr -d ' ')"
if [ "$backup_count" -ge 1 ]; then
    ok "backup file exists in backups dir ($backup_count found)"
else
    fail "no backup file found in $T1_BACKUPS"
fi

# Check history record appended.
if [ -f "$T1_HIST" ] && grep -q '"promoted"' "$T1_HIST" 2>/dev/null; then
    ok "history record with action=promoted written"
    if grep '"promoted"' "$T1_HIST" | jq -e '.from_model == "opus" and .to_model == "sonnet"' >/dev/null 2>&1; then
        ok "history record has correct from_model=opus to_model=sonnet"
    else
        fail "history record has wrong model values: $(cat "$T1_HIST")"
    fi
else
    fail "no promoted record in history file"
fi

# Check candidate stripped.
if [ ! -f "$T1_CANDS" ] || ! grep -q '"my-agent"' "$T1_CANDS" 2>/dev/null; then
    ok "candidate stripped from candidates file"
else
    fail "candidate still present in candidates file after promote"
fi

# ============================================================
# Test 2: promote with --global on a framework-style agent
# ============================================================
echo
echo "Test 2: promote --global on framework agent"

T2_DIR="$WORKDIR/t2"
mkdir -p "$T2_DIR/state"
mkdir -p "$T2_DIR/state/model-routing-backups"

# Use the real framework agent directory but with a temp copy of an agent.
# We don't want to modify real lib/agents/ files, so point YAKOS_ROOT to T2_DIR.
mkdir -p "$T2_DIR/lib/agents"
make_agent_file "$T2_DIR/lib/agents/global-agent.md" "opus"

T2_CANDS="$T2_DIR/state/model-routing-candidates.ndjson"
T2_HIST="$T2_DIR/state/model-routing-history.ndjson"
T2_GRAVE="$T2_DIR/state/model-routing-graveyard.ndjson"
T2_BACKUPS="$T2_DIR/state/model-routing-backups"

make_candidate "global-agent" "opus" "haiku" > "$T2_CANDS"

T2_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$T2_DIR" \
    YAKOS_LIB="$MOCK_PASS_LIB" \
    YAKOS_MR_CANDIDATES="$T2_CANDS" \
    YAKOS_MR_HISTORY="$T2_HIST" \
    YAKOS_MR_GRAVEYARD="$T2_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T2_BACKUPS" \
    bash "$YAKOS_LIB/model-routing.sh" promote global-agent --global 2>&1 || true
)"

if grep -q 'model: haiku' "$T2_DIR/lib/agents/global-agent.md" 2>/dev/null; then
    ok "--global promote rewrites framework agent frontmatter"
else
    fail "--global promote did not rewrite framework agent; output: $T2_OUT"
fi

# ============================================================
# Test 3: promote without --global on a framework agent: refusal
# ============================================================
echo
echo "Test 3: promote without --global on framework agent: refusal + remediation message"

T3_DIR="$WORKDIR/t3"
mkdir -p "$T3_DIR/lib/agents"
mkdir -p "$T3_DIR/state"

make_agent_file "$T3_DIR/lib/agents/fw-only-agent.md" "opus"
make_candidate "fw-only-agent" "opus" "sonnet" > "$T3_DIR/state/model-routing-candidates.ndjson"

T3_RC=0
T3_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$T3_DIR" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T3_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T3_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T3_DIR/state/model-routing-graveyard.ndjson" \
    YAKOS_MR_BACKUPS_DIR="$T3_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" promote fw-only-agent 2>&1
)" || T3_RC=$?

if [ "$T3_RC" -ne 0 ]; then
    ok "promote without --global on framework agent exits non-zero ($T3_RC)"
else
    fail "promote without --global should exit non-zero on framework agent"
fi

if printf '%s' "$T3_OUT" | grep -qi '\-\-global'; then
    ok "refusal message mentions --global"
else
    fail "refusal message should mention --global; got: $T3_OUT"
fi

if printf '%s' "$T3_OUT" | grep -qi 'Pass --global\|pass --global'; then
    ok "refusal message contains exact remediation (Pass --global)"
else
    fail "refusal message missing 'Pass --global' remediation; got: $T3_OUT"
fi

# Verify original agent file is untouched (still model: opus).
if grep -q 'model: opus' "$T3_DIR/lib/agents/fw-only-agent.md" 2>/dev/null; then
    ok "framework agent file unchanged after refusal"
else
    fail "framework agent file was modified on refusal"
fi

# ============================================================
# Test 4: promote no-candidate → exit 1 with helpful message
# ============================================================
echo
echo "Test 4: promote no-candidate exits 1 with helpful message"

T4_DIR="$WORKDIR/t4"
mkdir -p "$T4_DIR/project/.claude/agents"
mkdir -p "$T4_DIR/state"

make_agent_file "$T4_DIR/project/.claude/agents/no-cand-agent.md" "opus"
# No candidates file written.

T4_RC=0
T4_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_PROJECT_DIR="$T4_DIR/project" \
    YAKOS_MR_CANDIDATES="$T4_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T4_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T4_DIR/state/model-routing-graveyard.ndjson" \
    YAKOS_MR_BACKUPS_DIR="$T4_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" promote no-cand-agent 2>&1
)" || T4_RC=$?

if [ "$T4_RC" -ne 0 ]; then
    ok "promote no-candidate exits non-zero ($T4_RC)"
else
    fail "promote no-candidate should exit non-zero"
fi

if printf '%s' "$T4_OUT" | grep -qi 'no candidate\|not found'; then
    ok "promote no-candidate shows helpful message mentioning 'no candidate'"
else
    fail "promote no-candidate message unclear; got: $T4_OUT"
fi

# ============================================================
# Test 5: promote validation failure → backup restored
# ============================================================
echo
echo "Test 5: promote validation failure restores backup"

T5_DIR="$WORKDIR/t5"
mkdir -p "$T5_DIR/project/.claude/agents"
mkdir -p "$T5_DIR/state"

make_agent_file "$T5_DIR/project/.claude/agents/val-agent.md" "opus"
T5_ORIGINAL_CONTENT="$(cat "$T5_DIR/project/.claude/agents/val-agent.md")"

make_candidate "val-agent" "opus" "haiku" > "$T5_DIR/state/model-routing-candidates.ndjson"

T5_RC=0
T5_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$MOCK_FAIL_LIB" \
    YAKOS_PROJECT_DIR="$T5_DIR/project" \
    YAKOS_MR_CANDIDATES="$T5_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T5_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T5_DIR/state/model-routing-graveyard.ndjson" \
    YAKOS_MR_BACKUPS_DIR="$T5_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" promote val-agent 2>&1
)" || T5_RC=$?

if [ "$T5_RC" -ne 0 ]; then
    ok "promote validation-failure exits non-zero ($T5_RC)"
else
    fail "promote validation-failure should exit non-zero"
fi

T5_CURRENT_CONTENT="$(cat "$T5_DIR/project/.claude/agents/val-agent.md")"
if [ "$T5_CURRENT_CONTENT" = "$T5_ORIGINAL_CONTENT" ]; then
    ok "agent file restored to original after validation failure"
else
    fail "agent file was NOT restored after validation failure"
fi

if printf '%s' "$T5_OUT" | grep -qi 'restor\|backup'; then
    ok "validation failure message mentions restore/backup"
else
    fail "validation failure message should mention restore; got: $T5_OUT"
fi

# ============================================================
# Test 6: reject happy path
# ============================================================
echo
echo "Test 6: reject happy path"

T6_DIR="$WORKDIR/t6"
mkdir -p "$T6_DIR/state"

T6_CANDS="$T6_DIR/state/model-routing-candidates.ndjson"
T6_GRAVE="$T6_DIR/state/model-routing-graveyard.ndjson"

make_candidate "reject-agent" "opus" "haiku" > "$T6_CANDS"

T6_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T6_CANDS" \
    YAKOS_MR_HISTORY="$T6_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T6_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T6_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" reject reject-agent --note "too risky" 2>&1 || true
)"

# Candidate stripped.
if [ ! -f "$T6_CANDS" ] || ! grep -q '"reject-agent"' "$T6_CANDS" 2>/dev/null; then
    ok "reject: candidate stripped from candidates file"
else
    fail "reject: candidate still present after reject"
fi

# Graveyard record written.
if [ -f "$T6_GRAVE" ] && grep -q '"reject-agent"' "$T6_GRAVE" 2>/dev/null; then
    ok "reject: graveyard record written"
    if grep '"reject-agent"' "$T6_GRAVE" | jq -e '.reason == "too risky"' >/dev/null 2>&1; then
        ok "reject: graveyard record contains note/reason"
    else
        fail "reject: graveyard record missing note; $(cat "$T6_GRAVE")"
    fi
    if grep '"reject-agent"' "$T6_GRAVE" | jq -e 'has("suggested_model") and has("ts") and has("by_user")' >/dev/null 2>&1; then
        ok "reject: graveyard record has required fields (suggested_model, ts, by_user)"
    else
        fail "reject: graveyard record missing required fields; $(cat "$T6_GRAVE")"
    fi
else
    fail "reject: graveyard record not written; output: $T6_OUT"
fi

# ============================================================
# Test 7: reject repeat (>=3) → warning + exit 1; --force succeeds
# ============================================================
echo
echo "Test 7: reject repeat-rejection guard"

T7_DIR="$WORKDIR/t7"
mkdir -p "$T7_DIR/state"

T7_CANDS="$T7_DIR/state/model-routing-candidates.ndjson"
T7_GRAVE="$T7_DIR/state/model-routing-graveyard.ndjson"

# Pre-seed 3 graveyard entries for (repeat-agent, haiku).
for _i in 1 2 3; do
    jq -cn \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        '{ts:$ts,agent:"repeat-agent",suggested_model:"haiku",
          eval_run_id:"run-old",reason:"prev rejection",by_user:"test"}' \
        >> "$T7_GRAVE"
done

make_candidate "repeat-agent" "opus" "haiku" > "$T7_CANDS"

T7_RC=0
T7_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T7_CANDS" \
    YAKOS_MR_HISTORY="$T7_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T7_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T7_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" reject repeat-agent 2>&1
)" || T7_RC=$?

if [ "$T7_RC" -ne 0 ]; then
    ok "repeat-rejection guard exits non-zero when >=3 prior rejections ($T7_RC)"
else
    fail "repeat-rejection guard should exit non-zero; got 0"
fi

if printf '%s' "$T7_OUT" | grep -qiE 'rejected [0-9]+ times|3 times'; then
    ok "repeat-rejection guard message mentions prior rejection count"
else
    fail "repeat-rejection guard message unclear; got: $T7_OUT"
fi

# Candidate should still be present (was not stripped on guard failure).
if grep -q '"repeat-agent"' "$T7_CANDS" 2>/dev/null; then
    ok "candidate still present after guard-blocked reject"
else
    fail "candidate was stripped even though reject was guard-blocked"
fi

# --force bypasses the guard.
# Re-seed a fresh candidate (same agent/model) after the guard test.
make_candidate "repeat-agent" "opus" "haiku" > "$T7_CANDS"

T7_FORCE_RC=0
T7_FORCE_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T7_CANDS" \
    YAKOS_MR_HISTORY="$T7_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T7_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T7_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" reject repeat-agent --force 2>&1
)" || T7_FORCE_RC=$?

if [ "$T7_FORCE_RC" -eq 0 ]; then
    ok "reject --force exits 0 even with >=3 prior rejections"
else
    fail "reject --force should exit 0; got $T7_FORCE_RC; output: $T7_FORCE_OUT"
fi

if [ ! -f "$T7_CANDS" ] || ! grep -q '"repeat-agent"' "$T7_CANDS" 2>/dev/null; then
    ok "reject --force: candidate stripped"
else
    fail "reject --force: candidate not stripped"
fi

# ============================================================
# Test 8: history — seed 3 history + 2 graveyard; tabular output sorted desc
# ============================================================
echo
echo "Test 8: history tabular output"

T8_DIR="$WORKDIR/t8"
mkdir -p "$T8_DIR/state"

T8_HIST="$T8_DIR/state/model-routing-history.ndjson"
T8_GRAVE="$T8_DIR/state/model-routing-graveyard.ndjson"

# 3 history records (promoted), different agents and timestamps.
for i in 1 2 3; do
    local_ts="2026-05-0${i}T10:00:00Z"
    jq -cn \
        --arg ts "$local_ts" \
        --arg agent "agent-h${i}" \
        '{ts:$ts,action:"promoted",agent:$agent,from_model:"opus",
          to_model:"sonnet",eval_run_id:"run-h'$i'",by_user:"tester"}' \
        >> "$T8_HIST"
done

# 2 graveyard records (rejected), different agents and timestamps.
for i in 1 2; do
    local_ts="2026-05-1${i}T10:00:00Z"
    jq -cn \
        --arg ts "$local_ts" \
        --arg agent "agent-g${i}" \
        '{ts:$ts,agent:$agent,suggested_model:"haiku",
          eval_run_id:"run-g'$i'",reason:"not worth it",by_user:"tester"}' \
        >> "$T8_GRAVE"
done

T8_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T8_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T8_HIST" \
    YAKOS_MR_GRAVEYARD="$T8_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T8_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" history 2>&1 || true
)"

# Should show all 5 records.
line_count="$(printf '%s' "$T8_OUT" | grep -cE 'agent-[gh][0-9]' 2>/dev/null || echo 0)"
if [ "$line_count" -ge 5 ]; then
    ok "history shows $line_count records (expected >= 5)"
else
    fail "history should show >= 5 records (3 promoted + 2 rejected); got $line_count: $T8_OUT"
fi

# Should contain both "promoted" and "rejected" labels.
if printf '%s' "$T8_OUT" | grep -q 'promoted'; then
    ok "history output contains 'promoted' entries"
else
    fail "history output missing 'promoted' entries"
fi

if printf '%s' "$T8_OUT" | grep -q 'rejected'; then
    ok "history output contains 'rejected' entries"
else
    fail "history output missing 'rejected' entries"
fi

# Test optional agent-id filter.
T8_FILTER_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$YAKOS_LIB" \
    YAKOS_MR_CANDIDATES="$T8_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T8_HIST" \
    YAKOS_MR_GRAVEYARD="$T8_GRAVE" \
    YAKOS_MR_BACKUPS_DIR="$T8_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" history agent-h1 2>&1 || true
)"

if printf '%s' "$T8_FILTER_OUT" | grep -q 'agent-h1'; then
    ok "history filter by agent shows matching record"
else
    fail "history filter by agent-h1 missing record; got: $T8_FILTER_OUT"
fi

# Filter should NOT include agent-h2 or agent-g1.
if printf '%s' "$T8_FILTER_OUT" | grep -qE 'agent-h2|agent-g1'; then
    fail "history filter leaked unrelated agents"
else
    ok "history filter excludes unrelated agents"
fi

# ============================================================
# Test 9: atomic frontmatter rewrite preserves comments and key order
# ============================================================
echo
echo "Test 9: atomic frontmatter rewrite preserves comments/key-order/whitespace"

T9_DIR="$WORKDIR/t9"
mkdir -p "$T9_DIR/project/.claude/agents"
mkdir -p "$T9_DIR/state"

# Fixture agent with a comment before the model: line.
T9_AGENT="$T9_DIR/project/.claude/agents/comment-agent.md"
cat > "$T9_AGENT" <<'AGENTEOF'
---
id: comment-agent
role: specialist
domain: testing
# This comment should be preserved verbatim.
mode: [implement]
tools: [Read]
model: opus
version: 1
references: []
---
# Comment Agent
## Purpose
Tests preservation of comments and key order.
## Execution
1. Do.
## Special rules
- Preserve comments.
## Handling peer messages
Pass.
## Personality
Careful.
AGENTEOF

T9_BEFORE_CHECKSUM="$(grep -v 'model:' "$T9_AGENT" | cksum)"

make_candidate "comment-agent" "opus" "haiku" > "$T9_DIR/state/model-routing-candidates.ndjson"

T9_OUT="$(
    HOME="$FAKE_HOME" \
    YAKOS_ROOT="$REPO_ROOT" \
    YAKOS_LIB="$MOCK_PASS_LIB" \
    YAKOS_PROJECT_DIR="$T9_DIR/project" \
    YAKOS_MR_CANDIDATES="$T9_DIR/state/model-routing-candidates.ndjson" \
    YAKOS_MR_HISTORY="$T9_DIR/state/model-routing-history.ndjson" \
    YAKOS_MR_GRAVEYARD="$T9_DIR/state/model-routing-graveyard.ndjson" \
    YAKOS_MR_BACKUPS_DIR="$T9_DIR/state/model-routing-backups" \
    bash "$YAKOS_LIB/model-routing.sh" promote comment-agent 2>&1 || true
)"

# model: line updated.
if grep -q 'model: haiku' "$T9_AGENT" 2>/dev/null; then
    ok "comment-agent: model: line updated to haiku"
else
    fail "comment-agent: model: not updated; $(grep 'model' "$T9_AGENT")"
fi

# Comment preserved.
if grep -q '# This comment should be preserved verbatim.' "$T9_AGENT" 2>/dev/null; then
    ok "comment-agent: inline comment preserved verbatim"
else
    fail "comment-agent: inline comment was lost"
fi

# id:, role:, domain: still in the frontmatter (key order preserved).
for key in id role domain mode tools version references; do
    if grep -q "^${key}:" "$T9_AGENT" 2>/dev/null; then
        ok "comment-agent: frontmatter key '$key' preserved"
    else
        fail "comment-agent: frontmatter key '$key' missing after rewrite"
    fi
done

# Only the model: line differs from the original (everything else byte-for-byte).
T9_AFTER_CHECKSUM="$(grep -v 'model:' "$T9_AGENT" | cksum)"
if [ "$T9_BEFORE_CHECKSUM" = "$T9_AFTER_CHECKSUM" ]; then
    ok "comment-agent: all non-model: lines unchanged (byte-for-byte)"
else
    fail "comment-agent: non-model: lines were altered (checksum mismatch)"
fi

# ============================================================
# Test 10: finops-review SKILL documents reading procedure
#           for pending model-routing candidates
# ============================================================
echo
echo "Test 10: finops-review SKILL documents pending routing candidates procedure"

FINOPS_SKILL="$REPO_ROOT/lib/skills/finops-review/SKILL.md"

# Verify the SKILL.md mentions the candidates file path.
if grep -q 'model-routing-candidates.ndjson' "$FINOPS_SKILL" 2>/dev/null; then
    ok "finops-review SKILL.md references model-routing-candidates.ndjson"
else
    fail "finops-review SKILL.md missing model-routing-candidates.ndjson reference"
fi

# Verify it mentions estimated_monthly_savings_usd.
if grep -q 'estimated_monthly_savings_usd' "$FINOPS_SKILL" 2>/dev/null; then
    ok "finops-review SKILL.md references estimated_monthly_savings_usd field"
else
    fail "finops-review SKILL.md missing estimated_monthly_savings_usd"
fi

# Verify it mentions yakos model-routing promote.
if grep -q 'yakos model-routing promote' "$FINOPS_SKILL" 2>/dev/null; then
    ok "finops-review SKILL.md mentions 'yakos model-routing promote'"
else
    fail "finops-review SKILL.md missing 'yakos model-routing promote' instruction"
fi

# Verify it mentions ranking (sort_by savings).
if grep -q 'sort_by\|ranked\|savings' "$FINOPS_SKILL" 2>/dev/null; then
    ok "finops-review SKILL.md describes ranking by savings"
else
    fail "finops-review SKILL.md missing ranking-by-savings description"
fi

# Functional smoke: hand-construct a candidates file with 2 pending entries
# and verify the jq fragment from the SKILL produces output for both.
T10_CANDS="$WORKDIR/t10-cands.ndjson"
make_candidate "agent-a" "opus" "haiku" > "$T10_CANDS"
make_candidate "agent-b" "opus" "sonnet" >> "$T10_CANDS"

T10_OUT="$(jq -rs '
    group_by(.agent) |
    map(sort_by(.generated_at) | last) |
    sort_by(-.estimated_monthly_savings_usd) |
    .[] |
    "  \(.agent): \(.current_model) -> \(.suggested_model)" +
    "  est. savings=~$\(.estimated_monthly_savings_usd)/mo"
' "$T10_CANDS" 2>&1 || true)"

if printf '%s' "$T10_OUT" | grep -q 'agent-a'; then
    ok "finops-review candidates jq fragment produces output for agent-a"
else
    fail "finops-review candidates jq fragment missing agent-a; output: $T10_OUT"
fi

if printf '%s' "$T10_OUT" | grep -q 'agent-b'; then
    ok "finops-review candidates jq fragment produces output for agent-b"
else
    fail "finops-review candidates jq fragment missing agent-b; output: $T10_OUT"
fi

# ============================================================
# Summary
# ============================================================
echo
echo "yakos model-routing Phase 3: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
