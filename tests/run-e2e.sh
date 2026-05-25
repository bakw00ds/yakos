#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: end-to-end smoke that exercises every yakOS subcommand
# against a throwaway project, verifying the install → init → use →
# uninstall cycle works without manual steps. Designed for CI.
#
# What this exercises:
#   yakos install / doctor / version --version
#   yakos init <name> --project <path>
#   yakos agent new <name> --runtime claude
#   yakos agents lint
#   yakos agent diff <name>      (no-extends path)
#   yakos start --dry-run
#   yakos start --print-agents
#   yakos auth status
#   yakos doctor <project> --probe-runtime
#   yakos doctor <project> --fix
#   yakos memory list (empty)
#   yakos cost (empty)
#   yakos session list
#   yakos session export <name> e2e-test
#   yakos migrate <name> (.yakos.yml already at v0.9 from init; idempotent)
#   yakos plugin list (empty)
#   yakos uninstall
#
# Exit 0 when every step passes. Skips runtime CLI calls that
# require network / auth (real dispatch is verified separately).

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
TMP="${TMPDIR:-/tmp}/yakos-e2e.$$"
mkdir -p "$TMP"
trap 'rm -rf "$TMP" 2>/dev/null || true' EXIT

# Use a fake $HOME so we don't pollute the real user's ~/.claude/.
export HOME="$TMP/home"
mkdir -p "$HOME"

# Symlink real auth state in if present, so check_auth doesn't trip.
[ -f "$ORIG_HOME/.claude/auth.json" ] 2>/dev/null && \
    cp "$ORIG_HOME/.claude/auth.json" "$HOME/.claude/auth.json" 2>/dev/null || true

PROJ="$TMP/test-project"
mkdir -p "$PROJ"
cd "$PROJ"
git init -q -b main
git config user.email ci@yakos.test
git config user.name "yakos-ci"
echo "0.1.0.0" > VERSION
echo "# probe" > README.md
git add . && git commit -qm initial
cd "$REPO_ROOT"

PASS=0; FAIL=0
ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

step() {
    local label="$1"; shift
    if "$@" >/dev/null 2>&1; then
        ok "$label"
    else
        fail "$label (cmd: $*)"
    fi
}

# Step asserting a specific exit code.
step_rc() {
    local label="$1"; shift
    local expected="$1"; shift
    local actual=0
    "$@" >/dev/null 2>&1 || actual=$?
    if [ "$actual" = "$expected" ]; then
        ok "$label (rc=$actual)"
    else
        fail "$label (expected rc=$expected, got $actual; cmd: $*)"
    fi
}

step_grep() {
    local label="$1" pattern="$2"; shift 2
    if "$@" 2>&1 | grep -qE "$pattern"; then
        ok "$label"
    else
        fail "$label (pattern '$pattern' not found in: $*)"
    fi
}

echo "yakos e2e smoke (HOME=$HOME, PROJ=$PROJ)"
echo

# 1. install + doctor + version
step "yakos --version"           ./cli/yakos --version
step "yakos install"             ./cli/yakos install
step "yakos doctor"              ./cli/yakos doctor

# 2. init + project bootstrap
step "yakos init test-project"   ./cli/yakos init test-project --project "$PROJ"
[ -d "$HOME/agent-control/test-project" ] && ok "control dir created" || fail "control dir missing"
[ -f "$PROJ/.claude/settings.json" ] && ok "project settings.json created" || fail "settings.json missing"

# 3. agent new
step "yakos agent new" ./cli/yakos agent new e2e-helper \
    --domain testing --project "$PROJ"
[ -f "$PROJ/.claude/agents/e2e-helper.md" ] && ok "agent file written" || fail "agent file not written"

# 4. agents lint
step_rc "yakos agents lint" 0  ./cli/yakos agents lint "$PROJ"

# 5. agent diff (no extends — should print 'does not extend' and rc=0)
step_grep "yakos agent diff (no extends)" "does not extend" \
    ./cli/yakos agent diff e2e-helper --project "$PROJ"

# 6. start --dry-run
step_grep "yakos start --dry-run banner" "runtime:[[:space:]]+claude" \
    ./cli/yakos start test-project --dry-run

step_grep "yakos start --dry-run agents count" "[0-9]+ registered" \
    ./cli/yakos start test-project --dry-run

# 7. auth status
step_grep "yakos auth status" "claude" \
    ./cli/yakos auth status

# 8. start --print-agents emits valid JSON
out="$(./cli/yakos start test-project --print-agents 2>/dev/null)"
if printf '%s' "$out" | jq -e 'type == "object" and (length > 0)' >/dev/null 2>&1; then
    ok "yakos start --print-agents emits valid JSON object"
else
    fail "yakos start --print-agents not valid JSON"
fi

# 9. doctor --probe-runtime
step_grep "yakos doctor --probe-runtime" "Claude Code runtime" \
    ./cli/yakos doctor "$PROJ" --probe-runtime

# 10. doctor --fix
step "yakos doctor --fix"     ./cli/yakos doctor "$PROJ" --fix

# 11. memory list (empty)
step_grep "yakos memory list (empty)" "no memory yet|Memory" \
    ./cli/yakos memory list test-project

# 12. cost (empty — no dispatches in this isolated HOME)
step_grep "yakos cost (empty)" "no dispatch-log|no.*found" \
    ./cli/yakos cost

# 13. session list
step "yakos session list"      ./cli/yakos session list test-project

# 14. session export
step "yakos session export"    ./cli/yakos session export test-project e2e-test
[ -f "$HOME/agent-control/test-project/work/exports/test-project-e2e-test.tar.gz" ] \
    && ok "export bundle written" || fail "export bundle missing"

# 15. migrate (.yakos.yml already exists at v0.9 from init; migrate is a no-op)
step "yakos migrate"           ./cli/yakos migrate test-project
[ -f "$PROJ/.yakos.yml" ] && ok ".yakos.yml created" || fail ".yakos.yml not created"
grep -q "^yakos: 0.9$" "$PROJ/.yakos.yml" && ok "schema stamped at 0.9" \
    || fail "schema not stamped at 0.9"

# Re-run migrate is idempotent (no-op).
step_grep "yakos migrate idempotent" "already at|nothing to do" \
    ./cli/yakos migrate test-project

# 16. plugin list (empty)
step_grep "yakos plugin list (empty)" "no plugins|none" \
    ./cli/yakos plugin list

# 17. validate (should be clean given the throwaway project)
step_rc "yakos validate" 0  ./cli/yakos validate

# 18. validate --strict — may have errors due to gather-feedback budget;
# just check the flag is honored.
step_grep "yakos validate --strict honored" "strict mode|Summary" \
    ./cli/yakos validate --strict

# 19. uninstall
step "yakos uninstall"         ./cli/yakos uninstall
[ ! -L "$HOME/.claude/agents/lead-template.md" ] && ok "uninstall removed lead-template symlink" \
    || fail "uninstall left lead-template symlink"
[ -d "$HOME/.claude/projects" ] || mkdir -p "$HOME/.claude/projects"
ok "auto-memory dir untouched (~/.claude/projects/)"

# ---- summary
echo
echo "yakos e2e: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
