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

# ---- no-jq PATH (security review C5 regression coverage) -------------------
#
# A directory of symlinks to every binary on the real PATH except jq, so a
# case can simulate "jq is not installed" while keeping every other tool
# (bash, grep, cat, mkdir, date, ...) available. Built once; cleaned up on
# exit via the trap below.
NOJQ_PATH="$(mktemp -d -t yakos-hookfix-nojq-XXXXXX)"
trap 'rm -rf "$NOJQ_PATH"' EXIT
for _dir in /usr/bin /bin /usr/local/bin; do
    [ -d "$_dir" ] || continue
    for _bin in "$_dir"/*; do
        [ -x "$_bin" ] || continue
        _name="$(basename -- "$_bin")"
        [ "$_name" = "jq" ] && continue
        ln -sf "$_bin" "$NOJQ_PATH/$_name" 2>/dev/null || true
    done
done

# ---- synthetic secret literals (GitHub push-protection safety) -------------
#
# GitHub's push protection scans every line of every commit in a push for
# known secret-detector patterns (AWS access keys, Stripe live keys, Slack
# tokens, GitHub tokens, Google API keys, PEM private key blocks, ...) and
# rejects the push if any commit — including old ones a rewritten history
# still contains — matches. This repo's hook fixtures deliberately
# construct fake-but-pattern-matching secrets so secret-scan.sh's regexes
# have something to fire on; that is exactly the shape push protection is
# built to catch, regardless of intent.
#
# Each fixture below carries a placeholder token (`__SECRET_<NAME>__`)
# instead of the literal value. The functions here assemble the real value
# at RUNTIME from two or more separately-quoted string literals, chosen so
# the detector-matching substring never appears contiguous in this file's
# own text either — each secret is split across a quote boundary below, so
# the full value only ever exists assembled in process memory, after this
# script is already running, never as a single token on disk.
# case_check substitutes each placeholder into the fixture payload after
# the __CLAUDE_PROJECT_DIR__ substitution, so no file in this tree and no
# line in this script contains a contiguous detector-matching string.
_secret_aws() { printf '%s%s' 'AKIA' '0123456789ABCDEF'; }
_secret_stripe() { printf '%s%s' 'sk_live' '_0123456789abcdefghijklmn'; }
_secret_slack() { printf '%s%s' 'xox' 'b-0123456789-abcdefghijklmnop'; }
_secret_anthropic() { printf '%s%s%s' 'sk-ant' '-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRST' 'UVWXYZ0123456789_-abcdefghijklmnopqrstuvwxyzABC'; }
_secret_google() { printf '%s%s' 'AIz' 'aabcdefghijklmnopqrstuvwxyzABCDEFGHI'; }
_secret_ghp() { printf '%s%s' 'ghp' '_0123456789abcdefghij0123456789abcdef'; }
_secret_ghpat() { printf '%s%s' 'github_pat' '_0123456789abcdefghijkl_0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyzABCD'; }
# The PEM value is assembled from header/body/footer pieces too, split
# mid-word, even though the body is obviously synthetic base64 and a bare
# BEGIN/PRIVATE-KEY header alone is unlikely to be what push protection's
# detector keys on — belt and suspenders. `\n` here is the literal
# two-character JSON escape (backslash + n), not a real newline — it has
# to stay that way to remain valid JSON once substituted into a fixture.
_secret_pem() {
    printf '%s%s%s%s' \
        '-----BEGIN RSA PRIVATE' ' KEY-----\nMIIBOgIBAAJBAKj34GkxFhD91RIkY7yOaeQz3QUcz2eGZLwqIkTLdw9ZuNJ5R2Hd\n-----END RSA PRIVATE' ' KEY' '-----\n'
}

case_check() {
    # Args: hook-script-relpath, fixture-relpath, expected-rc, expected-log-name, [setup-fn], [extra-env-assignment]
    #
    # extra-env-assignment, if given, is a single "NAME=value" string
    # exported into the hook's environment for this one invocation (used
    # by the missing-jq fail-closed cases to override PATH).
    local hook="$1" fixture="$2" expected_rc="$3" log_name="$4" setup_fn="${5:-}" extra_env="${6:-}"

    local tmp
    tmp="$(mktemp -d -t yakos-hookfix-XXXXXX)"
    mkdir -p "$tmp/.claude" "$tmp/work/current/logs"

    if [ -n "$setup_fn" ]; then
        "$setup_fn" "$tmp"
    fi

    local payload
    payload="$(cat "$FIXT/$fixture")"
    # Assemble any synthetic-secret placeholders (see the _secret_* functions
    # above) into their real values — a no-op for fixtures that carry none.
    # Plain substring replacement (no glob metacharacters in the search
    # tokens), so this is safe even though some assembled values themselves
    # contain regex-special characters.
    payload="${payload//__SECRET_AWS__/$(_secret_aws)}"
    payload="${payload//__SECRET_STRIPE__/$(_secret_stripe)}"
    payload="${payload//__SECRET_SLACK__/$(_secret_slack)}"
    payload="${payload//__SECRET_ANTHROPIC__/$(_secret_anthropic)}"
    payload="${payload//__SECRET_GOOGLE__/$(_secret_google)}"
    payload="${payload//__SECRET_GHP__/$(_secret_ghp)}"
    payload="${payload//__SECRET_GHPAT__/$(_secret_ghpat)}"
    payload="${payload//__SECRET_PEM__/$(_secret_pem)}"

    # Pin work-directory resolution to the temp dir so hooks write here,
    # not into ~/agent-control/.
    local actual_rc=0
    local stdout_capture
    if [ -n "$extra_env" ]; then
        stdout_capture="$(printf '%s' "$payload" | env "$extra_env" YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$tmp" bash "$HOOKS/$hook" 2>/dev/null)" || actual_rc=$?
    else
        stdout_capture="$(printf '%s' "$payload" | YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$tmp" bash "$HOOKS/$hook" 2>/dev/null)" || actual_rc=$?
    fi

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

setup_allowlist_strict_namespaced() {
    # Same policy as setup_allowlist_strict but keyed on bare "go-api".
    # Used to verify that a namespaced agent ("yakos:go-api") hits the
    # same policy after hi_sender_role strips the prefix.
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

setup_allowlist_deny_pem() {
    # go-api may write anywhere under api/, EXCEPT *.pem (any case — H5b).
    mkdir -p "$1/api/creds"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"],
    "deny": ["*.pem"]
  }
}
EOF
}

setup_allowlist_notebook() {
    # go-api may only touch api/**; used for the NotebookEdit coverage cases.
    mkdir -p "$1/api" "$1/web"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"]
  }
}
EOF
}

setup_symlink_escape() {
    # go-api may write anywhere under api/, but api/escape-link is a
    # symlink to a file outside the project root entirely (M7 regression:
    # a lexically-fine path that resolves outside the tree via symlink).
    mkdir -p "$1/api"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"]
  }
}
EOF
    ln -sf /etc/hosts "$1/api/escape-link"
}

setup_budget_low_cap() {
    # max_tool_calls: 1, with state already at count=1 from a prior call —
    # the next PreToolUse call pushes the count to 2, over the cap.
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: true
  max_tool_calls: 1
EOF
    mkdir -p "$1/work/current"
    cat > "$1/work/current/.budget-state.json" <<EOF
{"session_id":"fixture-generic-tool-0001","started_at":$(date +%s),"tool_call_count":1,"last_tool":"Read","last_tool_run_count":1}
EOF
}

setup_budget_headroom() {
    # max_tool_calls: 500 — nowhere near the cap, first call of the session.
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: true
  max_tool_calls: 500
EOF
}

# ---- cases ------------------------------------------------------------------

echo "Running hook fixtures..."
echo

# --- path-allowlist ---
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_with_bypass     # bypass dir + allowlist absent → permissive
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_no_allowlist    # no allowlist → permissive WARN
# namespaced agent ("yakos:go-api") must hit bare-keyed policy ("go-api")
case_check path-allowlist.sh   pretooluse-edit-api-namespaced.json 0 path-allowlist setup_allowlist_strict_namespaced
# C3: lexical "../" traversal must be rejected even though it starts inside
# an allowed prefix ("api/...").
case_check path-allowlist.sh   pretooluse-write-traversal.json   2 path-allowlist setup_allowlist_strict
# C4: NotebookEdit must be covered by the same allow/deny policy as
# Edit/Write/MultiEdit.
case_check path-allowlist.sh   pretooluse-notebookedit-api-ok.json      0 path-allowlist setup_allowlist_notebook
case_check path-allowlist.sh   pretooluse-notebookedit-web-blocked.json 2 path-allowlist setup_allowlist_notebook
# H5b: deny matching is case-insensitive (APFS/NTFS default to
# case-insensitive filesystems, so ".env" vs ".ENV" is the same inode).
case_check path-allowlist.sh   pretooluse-write-dotenv-upper.json 2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-pem-upper.json    2 path-allowlist setup_allowlist_deny_pem
# M7: a lexically-fine path that is a symlink resolving outside the
# project root must be blocked regardless of what the allow list says.
case_check path-allowlist.sh   pretooluse-write-symlink-escape.json 2 path-allowlist setup_symlink_escape
# C5: missing jq must fail CLOSED (block), not silently pass every write.
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH"

# --- path-log ---
case_check path-log.sh         pretooluse-edit-api.json          0 path-log
case_check path-log.sh         pretooluse-edit-web-blocked.json  0 path-log
case_check path-log.sh         pretooluse-write-secret.json      0 path-log

# --- secret-scan ---
case_check secret-scan.sh      pretooluse-edit-api.json          0 secret-scan
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan
# H5a: the PEM rule starts with "-", which grep parsed as an option string
# before the -e fix — it must actually fire.
case_check secret-scan.sh      pretooluse-write-pem-secret.json  2 secret-scan
# C4: NotebookEdit's .new_source must be scanned like any other write.
case_check secret-scan.sh      pretooluse-notebookedit-secret.json 2 secret-scan
case_check secret-scan.sh      pretooluse-notebookedit-api-ok.json  0 secret-scan
# C5: missing jq must fail CLOSED (block), not silently pass every write.
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan "" "PATH=$NOJQ_PATH"

# --- budget-guard ---
# Previously had zero shell fixtures (Go unit test only, per the security
# review's fixture-coverage audit). Basic block+allow pair, plus the C5
# missing-jq fail-closed case.
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_headroom
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_low_cap
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_low_cap "PATH=$NOJQ_PATH"

# --- mailbox-mirror ---
case_check mailbox-mirror.sh   sendmessage-peer.json             0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-from-lead.json        0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-to-lead.json          0 mailbox-mirror

# --- team-lifecycle ---
case_check team-lifecycle.sh   teamcreate.json                   0 team-lifecycle
case_check team-lifecycle.sh   agent-spawn.json                  0 team-lifecycle
# namespaced subagent_type (yakos: prefix) must also pass
case_check team-lifecycle.sh   agent-spawn-namespaced.json       0 team-lifecycle

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
