#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-runtime-live.sh — execute SDK adapters against real installed SDKs.
#
# Most yakOS tests are hermetic (fixtures + tempdirs). This one is
# different: it actually invokes the runtime if and only if the
# corresponding SDK is pip-installed AND its credentials are present.
# If a runtime isn't available, that runtime's tests are SKIPPED with
# a clear message — they don't fail.
#
# Currently covered:
#   - claude-sdk     (pip install claude-agent-sdk; ANTHROPIC_API_KEY
#                     OR claude CLI auth; ~10s + ~$0.01 per run)
#   - antigravity-sdk (pip install google-antigravity; GEMINI_API_KEY;
#                     ~10s + Gemini API cost per run)
#
# Each test dispatches a trivial agent ("respond with the literal
# string PASS") and verifies (a) the response text contains PASS and
# (b) usage telemetry was captured (non-empty YAKOS_USAGE_OUT file).
#
# Run manually before tagging a release — not in CI by default
# because it costs real money on real API endpoints. To opt in to CI,
# set YAKOS_RUNTIME_LIVE=1 in workflow env.
#
# Exit 0 if all runnable tests passed; 1 on any failure. SKIPPED
# tests do not affect the exit code.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
RT_DIR="$REPO_ROOT/cli/lib/runtimes"

pass=0
fail=0
skip=0
fail_log=""

note() { printf '  %s\n' "$1"; }
ok()   { printf '  \033[32mOK\033[0m    %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; fail=$((fail + 1)); fail_log="${fail_log}    - $1\n"; }
skp()  { printf '  \033[33mSKIP\033[0m  %s\n' "$1"; skip=$((skip + 1)); }

# --- shared setup -----------------------------------------------------------

TMP_ROOT="$(mktemp -d -t yakos-rtlive-XXXXXX)"
trap 'rm -rf "$TMP_ROOT"' EXIT INT TERM

FAKE_REPO="$TMP_ROOT/project"
mkdir -p "$FAKE_REPO"
echo "# Test repo" > "$FAKE_REPO/README.md"
( cd "$FAKE_REPO" && git init -q && git add . && \
  git -c user.email=test@yakos -c user.name=test commit -q -m init >/dev/null 2>&1 )

USAGE_OUT="$TMP_ROOT/usage.json"

# Trivial agent: respond with literal "PASS" and stop.
TRIVIAL_AGENT_JSON='{"trivial-test-agent":{"description":"Live test agent",
"prompt":"You are a test agent. When asked, respond with exactly the four characters PASS and nothing else. Do not call any tools. Do not explain.",
"tools":[]}}'

# Build the per-test invocation helper. Args:
#   runtime-id    (claude-sdk | antigravity-sdk)
#   import-probe  (python -c snippet that succeeds iff the SDK is installed)
run_one() {
    local rt_id="$1"
    local import_probe="$2"

    note ""
    note "=== $rt_id ==="

    if ! command -v python3 >/dev/null 2>&1; then
        skp "$rt_id: python3 not on PATH"
        return 0
    fi

    if ! python3 -c "$import_probe" 2>/dev/null; then
        skp "$rt_id: SDK package not pip-installed (skipping)"
        return 0
    fi

    # Auth check: claude-sdk uses ANTHROPIC_API_KEY or ~/.claude/auth.json;
    # antigravity-sdk uses GEMINI_API_KEY.
    case "$rt_id" in
        claude-sdk)
            if [ -z "${ANTHROPIC_API_KEY:-}" ] && [ ! -f "$HOME/.claude/auth.json" ]; then
                skp "$rt_id: no ANTHROPIC_API_KEY env AND no ~/.claude/auth.json (skipping)"
                return 0
            fi
            ;;
        antigravity-sdk)
            if [ -z "${GEMINI_API_KEY:-}" ]; then
                skp "$rt_id: no GEMINI_API_KEY env (skipping)"
                return 0
            fi
            ;;
    esac

    local dispatch_script="$RT_DIR/${rt_id}-dispatch.py"
    [ -f "$dispatch_script" ] || { bad "$rt_id: dispatch script missing at $dispatch_script"; return 1; }

    local out_tmp="$TMP_ROOT/${rt_id}-out.txt"
    : > "$USAGE_OUT"

    note "invoking trivial agent (this will cost a small amount on the real API)..."

    set +e
    YAKOS_AGENT_ID=trivial-test-agent \
    YAKOS_PROJECT_DIR="$FAKE_REPO" \
    YAKOS_AGENTS_JSON="$TRIVIAL_AGENT_JSON" \
    YAKOS_USAGE_OUT="$USAGE_OUT" \
        timeout 60 python3 "$dispatch_script" \
        <<<"Please respond with exactly the four characters PASS." \
        > "$out_tmp" 2>&1
    local rc=$?
    set -e

    if [ "$rc" -ne 0 ]; then
        bad "$rt_id: dispatch exited rc=$rc"
        echo "    output:"
        sed 's/^/      /' "$out_tmp" | head -20
        return 1
    fi

    if grep -q PASS "$out_tmp"; then
        ok "$rt_id: response contains 'PASS'"
    else
        bad "$rt_id: response did not contain 'PASS'"
        echo "    output was:"
        sed 's/^/      /' "$out_tmp" | head -10
    fi

    if [ -s "$USAGE_OUT" ]; then
        if jq empty "$USAGE_OUT" >/dev/null 2>&1; then
            ok "$rt_id: usage telemetry written (valid JSON)"
            # Pretty preview
            echo "    usage preview:"
            jq -c 'with_entries(select(.value != null) | .value |= (if type == "object" or type == "array" then (. | tostring | .[0:60]) else . end))' "$USAGE_OUT" 2>/dev/null \
                | sed 's/^/      /' | head -3
        else
            bad "$rt_id: usage file written but not valid JSON"
        fi
    else
        bad "$rt_id: no usage telemetry written to YAKOS_USAGE_OUT"
    fi
}

# --- claude-sdk -------------------------------------------------------------
run_one claude-sdk 'import claude_agent_sdk'

# --- antigravity-sdk --------------------------------------------------------
run_one antigravity-sdk 'from google.antigravity import Agent'

# --- summary ----------------------------------------------------------------
note ""
note "=== Summary ==="
printf '  %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skip"

if [ "$fail" -gt 0 ]; then
    printf '\nFailures:\n'
    printf '%b' "$fail_log"
    exit 1
fi

if [ "$pass" -eq 0 ] && [ "$skip" -gt 0 ]; then
    echo
    echo "All tests skipped — install the SDKs + set credentials to actually exercise:"
    echo "  pip install claude-agent-sdk            # then export ANTHROPIC_API_KEY=..."
    echo "  pip install google-antigravity          # then export GEMINI_API_KEY=..."
fi

exit 0
