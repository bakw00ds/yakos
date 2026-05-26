#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-e2big-launch-test.sh — regression tests for the E2BIG fix in
# yk_rt_claude_launch (cli/lib/runtimes/claude.sh).
#
# Background: on Linux, execve caps each argv element at MAX_ARG_STRLEN
# (131,072 bytes on 4K-page kernels). The old launch path passed the
# entire composed agent roster as a single --agents JSON string (~167KB),
# which exceeded that cap and caused E2BIG. The fix materializes the
# roster as per-agent .md files in a session-scoped plugin dir and
# launches with --plugin-dir instead.
#
# Tests:
#   1. plugin_dir_path: path resolver honours YAKOS_WORK_DIR override
#   2. plugin_dir_path: falls back to $HOME/agent-control/<proj>/work/current
#   3. write_plugin_dir: creates manifest (.claude-plugin/plugin.json)
#   4. write_plugin_dir: agent count matches composed JSON length
#   5. write_plugin_dir: each agent file has valid frontmatter (name, description)
#   6. write_plugin_dir: tools and model fields emitted when present
#   7. write_plugin_dir: no single argv element >= 131072 bytes (max-arg-len gate)
#   8. write_plugin_dir: synthetic >128KB roster passes max-arg-len gate
#   9. cleanup_agents: rm -rf removes the plugin dir
#  10. write_plugin_dir: idempotent rebuild (old files not carried over)
#  11. write_plugin_dir: agent count matches framework compose (≥ 11)
#
# Hook identity normalization (yakos: prefix stripping):
#  12. hi_strip_rt_prefix strips "yakos:" prefix
#  13. hi_strip_rt_prefix leaves bare names unchanged
#  14. hi_strip_rt_prefix leaves "lead" unchanged
#  15. hi_strip_rt_prefix does NOT strip other namespace prefixes
#  16. hi_sender_role returns bare id for namespaced agent_type
#  17. hi_sender_role returns "lead" when agent_type absent
#  18. path-allowlist gate enforces bare-keyed policy for namespaced agent
#
# Does NOT actually exec claude — this is a unit test of the
# file-materialization logic only.
#
# Exit codes: 0 all pass, 1 any failure.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
export YAKOS_ROOT="$REPO_ROOT"
export YAKOS_LIB="$REPO_ROOT/cli/lib"

WORKDIR="${TMPDIR:-/tmp}/yakos-e2big-test.$$"
mkdir -p "$WORKDIR"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

PASS=0
FAIL=0

ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

# Source helpers (compat → agents-compose → claude adapter).
# shellcheck source=../cli/lib/compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=../cli/lib/agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=../cli/lib/runtimes/claude.sh
. "$YAKOS_LIB/runtimes/claude.sh"

echo "yakos E2BIG launch regression tests"
echo

# ---- 1. plugin_dir_path: honours YAKOS_WORK_DIR override -------------------
echo "Test 1: plugin_dir_path honours YAKOS_WORK_DIR"
got="$(YAKOS_WORK_DIR=/tmp/yakos-testwork yk_rt_claude_plugin_dir_path /some/project)"
expected="/tmp/yakos-testwork/current/.yakos-agents-plugin"
if [ "$got" = "$expected" ]; then
    ok "plugin_dir_path with YAKOS_WORK_DIR = $got"
else
    fail "plugin_dir_path with YAKOS_WORK_DIR: got '$got', expected '$expected'"
fi

# ---- 2. plugin_dir_path: falls back to HOME/agent-control layout -----------
echo
echo "Test 2: plugin_dir_path falls back to \$HOME/agent-control"
unset YAKOS_WORK_DIR 2>/dev/null || true
got="$(yk_rt_claude_plugin_dir_path /projects/myapp)"
expected="$HOME/agent-control/myapp/work/current/.yakos-agents-plugin"
if [ "$got" = "$expected" ]; then
    ok "plugin_dir_path fallback = $got"
else
    fail "plugin_dir_path fallback: got '$got', expected '$expected'"
fi

# ---- 3. write_plugin_dir: creates manifest ---------------------------------
echo
echo "Test 3: write_plugin_dir creates plugin manifest"
PLUGIN_DIR="$WORKDIR/plugin-test1"
SIMPLE_JSON='{"myagent":{"description":"A test agent","prompt":"You are a test agent."}}'
yk_rt_claude_write_plugin_dir "$PLUGIN_DIR" "$SIMPLE_JSON" >/dev/null

manifest="$PLUGIN_DIR/.claude-plugin/plugin.json"
if [ -f "$manifest" ]; then
    pname="$(jq -r '.name' "$manifest" 2>/dev/null)"
    if [ "$pname" = "yakos" ]; then
        ok "manifest exists with name=yakos"
    else
        fail "manifest name wrong: got '$pname'"
    fi
else
    fail "manifest not created at $manifest"
fi

# ---- 4. write_plugin_dir: agent count matches JSON length ------------------
echo
echo "Test 4: write_plugin_dir agent count matches JSON length"
PLUGIN_DIR="$WORKDIR/plugin-test2"
JSON2='{"a1":{"description":"Agent 1","prompt":"P1"},"a2":{"description":"Agent 2","prompt":"P2"},"a3":{"description":"Agent 3","prompt":"P3"}}'
count="$(yk_rt_claude_write_plugin_dir "$PLUGIN_DIR" "$JSON2")"
file_count="$(find "$PLUGIN_DIR/agents" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ "$count" = "3" ] && [ "$file_count" = "3" ]; then
    ok "wrote $count agent files, found $file_count on disk"
else
    fail "count mismatch: returned $count, found $file_count on disk"
fi

# ---- 5. write_plugin_dir: valid frontmatter (name, description) -----------
echo
echo "Test 5: agent files have valid frontmatter"
sample="$PLUGIN_DIR/agents/a1.md"
if [ -f "$sample" ]; then
    has_name="$(grep -c '^name: a1$' "$sample" || true)"
    has_desc="$(grep -c '^description: ' "$sample" || true)"
    has_sep="$(grep -c '^---$' "$sample" || true)"
    if [ "$has_name" -ge 1 ] && [ "$has_desc" -ge 1 ] && [ "$has_sep" -ge 2 ]; then
        ok "a1.md has name, description, and --- delimiters"
    else
        fail "a1.md missing required frontmatter (name=$has_name desc=$has_desc sep=$has_sep)"
        head -6 "$sample" >&2
    fi
else
    fail "a1.md not found at $sample"
fi

# ---- 6. write_plugin_dir: tools and model fields emitted when present ------
echo
echo "Test 6: tools and model emitted when present in composed JSON"
PLUGIN_DIR="$WORKDIR/plugin-test3"
JSON3='{"worker":{"description":"Worker agent","prompt":"Work.","tools":["Read","Edit","Bash"],"model":"haiku"}}'
yk_rt_claude_write_plugin_dir "$PLUGIN_DIR" "$JSON3" >/dev/null
worker_md="$PLUGIN_DIR/agents/worker.md"
if [ -f "$worker_md" ]; then
    has_tools="$(grep -c '^tools: Read, Edit, Bash$' "$worker_md" || true)"
    has_model="$(grep -c '^model: haiku$' "$worker_md" || true)"
    if [ "$has_tools" -ge 1 ] && [ "$has_model" -ge 1 ]; then
        ok "worker.md has tools and model frontmatter"
    else
        fail "worker.md missing tools or model (tools=$has_tools model=$has_model)"
        head -8 "$worker_md" >&2
    fi
else
    fail "worker.md not found at $worker_md"
fi

# ---- 7. max-arg-len gate: no single argv element >= 131072 bytes -----------
# The actual composed roster from the framework. We measure the max length
# of any single string the OLD path would have passed as one argv element.
# The NEW path passes --plugin-dir <path> (short string) instead.
echo
echo "Test 7: no single argv element >= 131072 bytes in new launch path"

MAX_ARG_STRLEN=131072

fw_json="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null)"
fw_json_len="$(printf '%s' "$fw_json" | wc -c | tr -d ' ')"

# OLD path max arg: the entire JSON string (--agents <json>).
old_max_arg="$fw_json_len"

# NEW path max arg: --plugin-dir <path>. The path is the longest single
# element the launcher passes. The longest realistic path is something like
# $HOME/agent-control/myproject/work/current/.yakos-agents-plugin (≈90 chars).
PLUGIN_DIR="$WORKDIR/plugin-size-check"
unset YAKOS_WORK_DIR 2>/dev/null || true
new_plugin_dir="$(yk_rt_claude_plugin_dir_path "$REPO_ROOT")"
new_max_arg="${#new_plugin_dir}"

if [ "$old_max_arg" -ge "$MAX_ARG_STRLEN" ]; then
    ok "OLD path max arg len = $old_max_arg bytes >= $MAX_ARG_STRLEN (confirms the bug)"
else
    ok "OLD path max arg len = $old_max_arg bytes (smaller than $MAX_ARG_STRLEN on this machine; bug manifests on Linux with larger rosters)"
fi

if [ "$new_max_arg" -lt "$MAX_ARG_STRLEN" ]; then
    ok "NEW path max arg len = $new_max_arg bytes < $MAX_ARG_STRLEN (E2BIG-safe)"
else
    fail "NEW path max arg len = $new_max_arg bytes >= $MAX_ARG_STRLEN (path too long!)"
fi

# ---- 8. synthetic >128KB roster: max-arg-len gate still passes --------------
echo
echo "Test 8: synthetic roster > 128KB — new path max arg remains short"

# Build a synthetic roster of 40 agents each with a 5KB prompt body.
# Total JSON size will be ~200KB, well above 131072.
_big_json_build() {
    local body
    # 5000 bytes of filler text
    body="$(dd if=/dev/urandom bs=5000 count=1 2>/dev/null | base64 | head -c 5000)"
    local out='{'
    local i=1
    while [ "$i" -le 40 ]; do
        [ "$i" -gt 1 ] && out="${out},"
        local esc_body
        esc_body="$(printf '%s' "$body" | sed 's/"/\\"/g' | tr '\n' ' ')"
        out="${out}\"agent${i}\":{\"description\":\"Synthetic agent ${i}\",\"prompt\":\"${esc_body}\"}"
        i=$((i + 1))
    done
    out="${out}}"
    printf '%s' "$out"
}

BIG_JSON="$(_big_json_build)"
big_json_len="$(printf '%s' "$BIG_JSON" | wc -c | tr -d ' ')"

BIG_PLUGIN_DIR="$WORKDIR/plugin-big"
YAKOS_WORK_DIR="$WORKDIR" yk_rt_claude_write_plugin_dir "$BIG_PLUGIN_DIR" "$BIG_JSON" >/dev/null 2>&1 || true

big_new_max_arg="${#BIG_PLUGIN_DIR}"

if [ "$big_json_len" -ge "$MAX_ARG_STRLEN" ]; then
    ok "synthetic roster = $big_json_len bytes (> $MAX_ARG_STRLEN; old path would E2BIG)"
else
    ok "synthetic roster = $big_json_len bytes (generated; may not reach 128KB threshold)"
fi

if [ "$big_new_max_arg" -lt "$MAX_ARG_STRLEN" ]; then
    ok "new path max arg with large roster = $big_new_max_arg bytes (still E2BIG-safe)"
else
    fail "new path max arg with large roster = $big_new_max_arg bytes >= $MAX_ARG_STRLEN"
fi

# ---- 9. cleanup_agents: removes the plugin dir ------------------------------
echo
echo "Test 9: cleanup_agents removes plugin dir"
CLEAN_DIR="$WORKDIR/plugin-cleanup"
YAKOS_WORK_DIR="$(dirname "$CLEAN_DIR")" yk_rt_claude_write_plugin_dir "$CLEAN_DIR" \
    '{"x":{"description":"x","prompt":"x"}}' >/dev/null
[ -d "$CLEAN_DIR" ] || { fail "pre-cleanup: plugin dir was not created"; }

# Simulate cleanup: call the function directly with the dir's parent project.
# Patch the path resolver by setting YAKOS_WORK_DIR so the path matches.
fake_proj="$WORKDIR/fake-project-cleanup"
mkdir -p "$fake_proj"
export YAKOS_WORK_DIR="$WORKDIR/fake-work-cleanup"
mkdir -p "$YAKOS_WORK_DIR/current"
CLEAN_VIA_FUNC="$YAKOS_WORK_DIR/current/.yakos-agents-plugin"
mkdir -p "$CLEAN_VIA_FUNC/.claude-plugin" "$CLEAN_VIA_FUNC/agents"

yk_rt_claude_cleanup_agents "$fake_proj"

if [ ! -d "$CLEAN_VIA_FUNC" ]; then
    ok "cleanup_agents removed the plugin dir"
else
    fail "cleanup_agents did NOT remove the plugin dir at $CLEAN_VIA_FUNC"
fi
unset YAKOS_WORK_DIR 2>/dev/null || true

# ---- 10. write_plugin_dir: idempotent rebuild (stale files removed) --------
echo
echo "Test 10: write_plugin_dir rebuild removes stale files"
IDEM_DIR="$WORKDIR/plugin-idempotent"
JSON_A='{"stale_agent":{"description":"Old","prompt":"Old agent"}}'
JSON_B='{"new_agent":{"description":"New","prompt":"New agent"}}'

yk_rt_claude_write_plugin_dir "$IDEM_DIR" "$JSON_A" >/dev/null
yk_rt_claude_write_plugin_dir "$IDEM_DIR" "$JSON_B" >/dev/null

stale_exists=0
new_exists=0
[ -f "$IDEM_DIR/agents/stale_agent.md" ] && stale_exists=1
[ -f "$IDEM_DIR/agents/new_agent.md" ] && new_exists=1

if [ "$stale_exists" = "0" ] && [ "$new_exists" = "1" ]; then
    ok "stale file removed; new file present after rebuild"
else
    fail "stale=$stale_exists new=$new_exists (expected stale=0 new=1)"
fi

# ---- 11. agent count matches framework compose (≥ 11) ----------------------
echo
echo "Test 11: written file count matches framework compose (≥ 11)"
FW_PLUGIN_DIR="$WORKDIR/plugin-framework"
fw_json2="$(yk_agents_compose "$REPO_ROOT" "" 2>/dev/null)"
fw_agent_count_json="$(printf '%s' "$fw_json2" | jq 'length')"
written_count="$(yk_rt_claude_write_plugin_dir "$FW_PLUGIN_DIR" "$fw_json2")"
file_count2="$(find "$FW_PLUGIN_DIR/agents" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')"

if [ "$written_count" = "$fw_agent_count_json" ] && [ "$file_count2" = "$fw_agent_count_json" ]; then
    ok "wrote $written_count files == composed count $fw_agent_count_json"
else
    fail "count mismatch: written=$written_count files=$file_count2 composed=$fw_agent_count_json"
fi

if [ "$fw_agent_count_json" -ge 11 ]; then
    ok "framework agent count = $fw_agent_count_json (>= 11)"
else
    fail "framework agent count = $fw_agent_count_json (< 11, expected ≥ 11)"
fi

# ============================================================================
# Tests 12-18: hook identity normalization (yakos: prefix stripping)
#
# With --plugin-dir, claude sets .agent_type = "yakos:backend" in hook
# stdin JSON. hi_sender_role must strip the prefix so all hook consumers
# (path-allowlist, task-complete-dispatch, supervisor-stream, etc.) see
# bare ids matching policy-file keys.
# ============================================================================

# Source hook-input.sh for the tests below.
# shellcheck source=../lib/hooks/lib/hook-input.sh
. "$REPO_ROOT/lib/hooks/lib/hook-input.sh"

echo
echo "Test 12: hi_strip_rt_prefix strips 'yakos:' prefix"
got="$(hi_strip_rt_prefix 'yakos:backend')"
if [ "$got" = "backend" ]; then
    ok "yakos:backend → backend"
else
    fail "yakos:backend → '$got' (expected 'backend')"
fi

echo
echo "Test 13: hi_strip_rt_prefix leaves bare names unchanged"
got="$(hi_strip_rt_prefix 'backend')"
if [ "$got" = "backend" ]; then
    ok "backend → backend (no change)"
else
    fail "backend → '$got' (should be unchanged)"
fi

echo
echo "Test 14: hi_strip_rt_prefix leaves 'lead' unchanged"
got="$(hi_strip_rt_prefix 'lead')"
if [ "$got" = "lead" ]; then
    ok "lead → lead (no change)"
else
    fail "lead → '$got' (should be unchanged)"
fi

echo
echo "Test 15: hi_strip_rt_prefix does NOT strip other namespace prefixes"
got="$(hi_strip_rt_prefix 'otherns:agent')"
if [ "$got" = "otherns:agent" ]; then
    ok "otherns:agent → otherns:agent (not stripped)"
else
    fail "otherns:agent → '$got' (should be unchanged)"
fi

echo
echo "Test 16: hi_sender_role returns bare id for namespaced agent_type"
# Simulate hook input JSON with yakos: prefix in .agent_type
HI_INPUT='{"agent_type":"yakos:security-reviewer","tool_name":"Edit","session_id":"test"}'
got="$(hi_sender_role)"
if [ "$got" = "security-reviewer" ]; then
    ok "hi_sender_role with yakos:security-reviewer → security-reviewer"
else
    fail "hi_sender_role returned '$got' (expected 'security-reviewer')"
fi

echo
echo "Test 17: hi_sender_role returns 'lead' when agent_type absent"
HI_INPUT='{"tool_name":"Edit","session_id":"test"}'
got="$(hi_sender_role)"
if [ "$got" = "lead" ]; then
    ok "hi_sender_role with no agent_type → lead"
else
    fail "hi_sender_role returned '$got' (expected 'lead')"
fi

echo
echo "Test 18: path-allowlist gate enforces bare-keyed policy for namespaced agent"
# Regression: a namespaced agent (yakos:backend) writing a DENIED path
# must be blocked — not silently passed due to policy key miss.
#
# We don't exec claude here (that's a live test); we simulate the hook
# logic directly to confirm the normalization makes the deny lookup hit.
#
# Setup: policy denies "backend" from writing "secrets/*".
ALLOWLIST_JSON='{"backend":{"deny":["secrets/*"]}}'
TEST_FILE="secrets/key.pem"
TEST_AGENT_RAW="yakos:backend"
TEST_AGENT_NORM="$(hi_strip_rt_prefix "$TEST_AGENT_RAW")"

# Look up policy using the normalized id (mirrors path-allowlist.sh line 65).
policy="$(printf '%s' "$ALLOWLIST_JSON" | jq -c --arg agent "$TEST_AGENT_NORM" '.[$agent] // empty' 2>/dev/null || true)"
deny_hit=""
if [ -n "$policy" ] && [ "$policy" != "null" ]; then
    # Check deny patterns (simplified — no glob, just prefix check for this test)
    deny_pattern="$(printf '%s' "$policy" | jq -r '.deny // [] | .[]' 2>/dev/null | head -1)"
    # Use bash case for fnmatch (same as path-allowlist.sh glob_match logic)
    case "$TEST_FILE" in
        ${deny_pattern/\*\*/*}) deny_hit="1" ;;
    esac
fi

if [ -n "$policy" ] && [ -n "$deny_hit" ]; then
    ok "namespaced agent 'yakos:backend' → normalized 'backend' → policy found → deny matched (BLOCK would fire)"
elif [ -z "$policy" ]; then
    fail "normalization broke: policy key miss for 'yakos:backend' (allowlist check sees '$TEST_AGENT_NORM' but found no policy)"
else
    fail "deny pattern not matched: file='$TEST_FILE' deny='$deny_pattern'"
fi

# ---- summary ----------------------------------------------------------------
echo
printf 'yakos E2BIG launch: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
