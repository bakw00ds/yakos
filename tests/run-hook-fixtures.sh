#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-fixtures.sh — drive every hook against every relevant fixture.
#
# For each (hook, fixture, expected-outcome) tuple:
#   1. Set up a temp $CLAUDE_PROJECT_DIR with the right .claude/ files.
#   2. Run the hook with the fixture piped to stdin.
#   3. Verify exit code matches expected.
#   4. Verify a structured log entry was appended to the expected ndjson.
#
# Exits 0 if all cases match, 1 otherwise. Prints a per-case status line.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
FIXT="$REPO_ROOT/tests/fixtures/hooks"

pass=0
fail=0
fail_log=""

case_check() {
    # Args: hook-script-relpath, fixture-relpath, expected-rc, expected-log-name, [setup-fn]
    local hook="$1" fixture="$2" expected_rc="$3" log_name="$4" setup_fn="${5:-}"

    local tmp
    tmp="$(mktemp -d -t yakos-hookfix-XXXXXX)"
    mkdir -p "$tmp/.claude" "$tmp/work/current/logs"

    if [ -n "$setup_fn" ]; then
        "$setup_fn" "$tmp"
    fi

    # Pin work-directory resolution to the temp dir so hooks write here,
    # not into ~/agent-control/.
    local actual_rc=0
    local stdout_capture
    stdout_capture="$(YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$tmp" bash "$HOOKS/$hook" < "$FIXT/$fixture" 2>/dev/null)" || actual_rc=$?

    # Verify rc
    local rc_ok=0
    [ "$actual_rc" = "$expected_rc" ] && rc_ok=1

    # Verify log was written (when expected)
    local log_ok=1
    if [ -n "$log_name" ]; then
        if [ ! -f "$tmp/work/current/logs/${log_name}.ndjson" ]; then
            log_ok=0
        else
            # Confirm the last line is parseable JSON
            tail -n 1 "$tmp/work/current/logs/${log_name}.ndjson" | jq empty 2>/dev/null || log_ok=0
        fi
    fi

    if [ "$rc_ok" = "1" ] && [ "$log_ok" = "1" ]; then
        printf 'PASS  %-30s | %-30s | rc=%s log=%s\n' "$(basename "$hook")" "$(basename "$fixture")" "$actual_rc" "$log_name"
        pass=$((pass + 1))
    else
        printf 'FAIL  %-30s | %-30s | rc=%s (want %s) log=%s rc_ok=%s log_ok=%s\n' \
            "$(basename "$hook")" "$(basename "$fixture")" "$actual_rc" "$expected_rc" "$log_name" "$rc_ok" "$log_ok"
        fail=$((fail + 1))
        fail_log="${fail_log}---- $hook on $fixture ----\nstdout: $stdout_capture\nlog file: $tmp/work/current/logs/${log_name}.ndjson\n$(ls -la "$tmp/work/current/logs/" 2>&1)\n"
        # Also stash the temp dir for inspection
        echo "    (state preserved at $tmp)"
        return 0  # don't abort the suite on a single failure
    fi
    rm -rf "$tmp"
}

# ---- setup helpers ----------------------------------------------------------

setup_allowlist_strict() {
    # go-api can edit api/** and internal/**, but not web/**.
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "lead": {"allow": ["**"], "deny": [".env", ".env.*"]},
  "go-api": {
    "allow": ["api/**", "internal/**"],
    "deny": ["api/migrations/**"]
  }
}
EOF
}

setup_no_allowlist() {
    : # no path-allowlist.json
}

setup_with_bypass() {
    # Bypass file with one active entry covering path-allowlist on web/index.js
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:web-test-fixture

**Hook:** path-allowlist
**Reason:** test fixture for bypass mechanism
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** web/index.js
**Follow-up:** none — fixture only
EOF
}

setup_with_decisions_stale() {
    mkdir -p "$1/work/current"
    # Touch decisions.md as 3h old
    touch -t "$(date -u -v-3H +%Y%m%d%H%M 2>/dev/null || date -u -d '-3 hours' +%Y%m%d%H%M)" "$1/work/current/decisions.md" 2>/dev/null
    : > "$1/work/current/decisions.md"
}

# ---- cases ------------------------------------------------------------------

echo "Running hook fixtures..."
echo

# --- path-allowlist ---
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_with_bypass     # bypass dir + allowlist absent → permissive
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_no_allowlist    # no allowlist → permissive WARN

# --- path-log ---
case_check path-log.sh         pretooluse-edit-api.json          0 path-log
case_check path-log.sh         pretooluse-edit-web-blocked.json  0 path-log
case_check path-log.sh         pretooluse-write-secret.json      0 path-log

# --- secret-scan ---
case_check secret-scan.sh      pretooluse-edit-api.json          0 secret-scan
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan

# --- mailbox-mirror ---
case_check mailbox-mirror.sh   sendmessage-peer.json             0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-from-lead.json        0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-to-lead.json          0 mailbox-mirror

# --- team-lifecycle ---
case_check team-lifecycle.sh   teamcreate.json                   0 team-lifecycle
case_check team-lifecycle.sh   agent-spawn.json                  0 team-lifecycle

# --- session-end-check ---
case_check session-end-check.sh sessionend-clean.json            0 session-end-check
case_check session-end-check.sh sessionend-stuck.json            0 session-end-check setup_with_decisions_stale

# --- task-* (REPORT-only in v0.1) ---
case_check task-dependency-gate.sh    taskcompleted-blocked.json   0 task-dependency-gate
case_check task-dependency-gate.sh    taskcompleted-unblocked.json 0 task-dependency-gate
case_check task-complete-dispatch.sh  taskcompleted-backend.json   0 task-complete-dispatch
case_check task-complete-dispatch.sh  taskcompleted-frontend.json  0 task-complete-dispatch

# ---- summary ----------------------------------------------------------------

echo
echo "Hook fixture results: $pass passed, $fail failed"
if [ "$fail" -gt 0 ]; then
    printf '\nFailure detail:\n%s\n' "$fail_log"
    exit 1
fi
exit 0
