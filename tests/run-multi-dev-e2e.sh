#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-multi-dev-e2e.sh — end-to-end test of Plan 1 multi-dev coordination.
#
# Unlike run-hook-fixtures.sh (which drives a single hook against a
# single fixture in a single process), this test spawns TWO concurrent
# bash subshells representing two developers and verifies the
# coordination protocol works under real concurrency.
#
# Scenarios covered:
#   1. Alice claims auth/login.ts → bob's edit attempt blocks
#   2. Bob adds a hook-bypass.md entry → next attempt warns+passes
#   3. Alice's session "ends" (team_deleted) → bob's claim succeeds clean
#   4. Mode-negotiation: alice proposes serialize; bob acks; alice unblocks
#   5. Mode-negotiation timeout: alice proposes; bob silent; alice falls
#      back to default-serialize
#
# All scenarios use a temp YAKOS_COORD_ROOT so the test never touches
# /var/lib/yakos. Identities are forged via USER + HOSTNAME +
# YAKOS_SESSION_PID env vars.
#
# Exit 0 if all scenarios pass; 1 on any failure.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
PEER_CLI="$REPO_ROOT/cli/lib/peer.sh"

pass=0
fail=0
fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }

# --- shared test fixtures ---------------------------------------------------

TMP_ROOT="$(mktemp -d -t yakos-multidev-XXXXXX)"
trap 'rm -rf "$TMP_ROOT"' EXIT INT TERM

export YAKOS_COORD_ROOT="$TMP_ROOT/coord-root"
export YAKOS_PROJECT_NAME="testproj"
COORD_DIR="$YAKOS_COORD_ROOT/$YAKOS_PROJECT_NAME/coord"

FAKE_REPO="$TMP_ROOT/fake-project"
export CLAUDE_PROJECT_DIR="$FAKE_REPO"
# Per paths.sh docs: YAKOS_INPLACE_WORK=1 keeps work/ inside the
# project repo. We use it here so bypass + log files land in
# $FAKE_REPO/work/current/ — predictable for the test.
export YAKOS_INPLACE_WORK=1

mkdir -p "$COORD_DIR/sessions" "$FAKE_REPO/work/current/logs"

# Create the per-project agent-control for peer.sh's resolve_project()
ac_root="$TMP_ROOT/fake-agent-control"
mkdir -p "$ac_root/$YAKOS_PROJECT_NAME"
echo "$FAKE_REPO" > "$ac_root/$YAKOS_PROJECT_NAME/.project-path"
# peer.sh defaults to $HOME/agent-control; we don't want to touch the real one.
# Override HOME for the test scope, but keep the real PATH/etc.
ORIG_HOME="$HOME"
export HOME="$TMP_ROOT/fake-home"
mkdir -p "$HOME"
ln -s "$ac_root" "$HOME/agent-control"

# --- helper: run a hook with forged identity --------------------------------
# Args: user host pid agent fixture-name expected-rc
run_hook_as() {
    local user="$1" host="$2" pid="$3" agent="$4" fixture="$5" expected_rc="$6"
    local label="${user}@${host}:${agent}"

    local fixture_file="$REPO_ROOT/tests/fixtures/hooks/$fixture"
    [ -f "$fixture_file" ] || { bad "$label: fixture $fixture missing"; return 1; }

    # Inject the forged identity. The agent_type in the fixture JSON
    # determines hi_sender_role; we override that via jq if needed.
    local actual_rc=0
    local payload
    payload="$(jq -c --arg a "$agent" '.agent_type = $a' "$fixture_file")"
    USER="$user" HOSTNAME="$host" YAKOS_SESSION_PID="$pid" \
        bash "$HOOKS/peer-claim.sh" <<<"$payload" >/dev/null 2>&1 || actual_rc=$?

    if [ "$actual_rc" -eq "$expected_rc" ]; then
        ok "$label: expected rc=$expected_rc; got $actual_rc"
    else
        bad "$label: expected rc=$expected_rc; got $actual_rc"
        return 1
    fi
}

run_confirm_as() {
    local user="$1" host="$2" pid="$3" agent="$4" fixture="$5"
    local payload
    payload="$(jq -c --arg a "$agent" '.agent_type = $a' "$REPO_ROOT/tests/fixtures/hooks/$fixture")"
    USER="$user" HOSTNAME="$host" YAKOS_SESSION_PID="$pid" \
        bash "$HOOKS/peer-claim-confirm.sh" <<<"$payload" >/dev/null 2>&1 || true
}

# === Scenario 1: alice claims → bob blocks ==================================
note ""
note "=== Scenario 1: alice claims login.ts, bob's edit blocks ==="

# Alice's PreToolUse + PostToolUse on auth/login.ts (the block fixture's path)
run_hook_as alice dev01 1001 frontend-pro pretooluse-peer-claim-block.json 0 \
    || true  # First time should be pass since claims file empty
run_confirm_as alice dev01 1001 frontend-pro posttooluse-peer-claim-confirm.json

# Now bob tries the same file — expect block (rc=2)
run_hook_as bob dev01 2002 backend-pro pretooluse-peer-claim-block.json 2

# === Scenario 2: bob bypasses ===============================================
note ""
note "=== Scenario 2: bob adds hook-bypass.md entry, retry warns+passes ==="

# Write a bypass file that matches Hook=peer-claim AND Scope contains
# both file=src/auth/login.ts AND peer=alice@dev01
bypass_file="$FAKE_REPO/work/current/hook-bypass.md"
cat > "$bypass_file" <<'EOF'
# Active hook bypasses

## Active entries

## bypass:test-override

**Hook:** peer-claim
**Reason:** test scenario 2
**Approved by:** test
**Created:** 2026-05-22T17:00:00Z
**Expires:** 2026-05-22T18:00:00Z
**Scope:** file=src/auth/login.ts peer=alice@dev01
**Follow-up:** delete after test
EOF

# Bob retries — bypass should kick in, rc=0
run_hook_as bob dev01 2002 backend-pro pretooluse-peer-claim-block.json 0

# === Scenario 3: alice "ends session" → bob clean ===========================
note ""
note "=== Scenario 3: alice team_deleted, bob retries clean ==="

# Remove the bypass so we're testing actual claim release.
rm -f "$bypass_file"

# Forge a team_deleted event from alice
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -nc --arg ts "$ts" \
    '{ts: $ts, kind: "team_deleted",
      actor: {user: "alice", host: "dev01", pid: 1001,
              session_id: "alice-sess", agent: "lead"},
      detail: {team_name: "test-team"}}' \
    >> "$COORD_DIR/activity.ndjson"

# Force rebuild of active-claims.json by deleting it
rm -f "$COORD_DIR/active-claims.json"

# Now bob's edit attempt should pass (rc=0) because alice's claim was
# released by the team_deleted event.
run_hook_as bob dev01 2002 backend-pro pretooluse-peer-claim-block.json 0

# === Scenario 4: mode-negotiation ack =======================================
note ""
note "=== Scenario 4: alice proposes serialize, bob acks ==="

proposal_log="$TMP_ROOT/proposal-output.log"

# Start alice's propose-mode in the background with a 5s timeout
(
    USER=alice HOSTNAME=dev01 YAKOS_SESSION_PID=1001 \
        bash "$PEER_CLI" propose-mode \
            --mode serialize \
            --targets 'src/auth/**' \
            --reason 'test scenario 4' \
            --timeout 5 \
            testproj > "$proposal_log" 2>&1
    echo "exit=$?" >> "$proposal_log"
) &
alice_pid=$!

# Give alice a moment to emit her proposal
sleep 1

# Extract the proposal_id from the log
proposal_id="$(jq -r 'select(.kind == "mode_proposal") | .detail.proposal_id' \
    "$COORD_DIR/activity.ndjson" 2>/dev/null | tail -n 1)"

if [ -z "$proposal_id" ]; then
    bad "scenario 4: no mode_proposal event found in activity log"
    kill $alice_pid 2>/dev/null || true
    wait $alice_pid 2>/dev/null || true
else
    ok "scenario 4: alice emitted mode_proposal with id=$proposal_id"

    # Bob acks
    USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002 \
        bash "$PEER_CLI" respond-mode \
            --to "$proposal_id" --ack \
            --reason 'sounds good' \
            testproj > /dev/null 2>&1

    # Wait for alice to see the ack and exit
    wait $alice_pid
    alice_rc=$?

    if [ "$alice_rc" -eq 0 ]; then
        ok "scenario 4: alice's propose-mode returned rc=0 after bob's ack"
    else
        bad "scenario 4: alice's propose-mode returned rc=$alice_rc (expected 0)"
    fi

    if grep -q 'RESPONSE.*ack' "$proposal_log"; then
        ok "scenario 4: alice's output shows ack from bob"
    else
        bad "scenario 4: alice's output missing ack confirmation"
        echo "    alice output was:"; sed 's/^/      /' "$proposal_log"
    fi
fi

# === Scenario 5: mode-negotiation timeout ===================================
note ""
note "=== Scenario 5: alice proposes, bob silent, timeout → default_serialize ==="

timeout_log="$TMP_ROOT/timeout-output.log"

USER=alice HOSTNAME=dev01 YAKOS_SESSION_PID=1001 \
    bash "$PEER_CLI" propose-mode \
        --mode parallel \
        --targets 'src/api/**' \
        --reason 'test scenario 5 timeout' \
        --timeout 3 \
        testproj > "$timeout_log" 2>&1
alice_rc=$?

if [ "$alice_rc" -eq 0 ]; then
    ok "scenario 5: alice's propose-mode returned rc=0 on timeout"
else
    bad "scenario 5: alice's propose-mode returned rc=$alice_rc (expected 0 even on timeout)"
fi

if grep -q 'TIMEOUT' "$timeout_log"; then
    ok "scenario 5: alice's output shows TIMEOUT message"
else
    bad "scenario 5: alice's output missing TIMEOUT message"
    echo "    output was:"; sed 's/^/      /' "$timeout_log"
fi

if jq -r 'select(.kind == "mode_response" and .detail.timeout == true) | .detail.response' \
    "$COORD_DIR/activity.ndjson" 2>/dev/null | grep -q default_serialize; then
    ok "scenario 5: synthetic timeout mode_response written with default_serialize"
else
    bad "scenario 5: no synthetic timeout mode_response found"
fi

# === Summary ================================================================
note ""
note "=== Summary ==="
printf '  %d passed, %d failed\n' "$pass" "$fail"

if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    printf '%b' "$fail_log"
    exit 1
fi

# Clean up env we mutated
export HOME="$ORIG_HOME"
exit 0
