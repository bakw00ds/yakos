#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: secret-scan.sh — PreToolUse hook on Edit|Write|MultiEdit|NotebookEdit.
#
# Refuses tool calls whose written content matches well-known secret
# patterns (AWS keys, GitHub tokens, Anthropic keys, Google API keys,
# generic API keys, private-key blocks).
# Exit 2 to BLOCK; surfaces a clear message to the agent.
#
# IMPORTANT: This hook is best-effort defense-in-depth for the write-path.
# It is NOT a security boundary. The authoritative secret-scanning gate is
# CI gitleaks (or trufflehog). These patterns catch the most common
# accidental writes; they do not guarantee full coverage.
#
# v0.1 patterns are deliberately conservative — false positives are worse
# than false negatives at this layer because hooks run on every Edit/Write.
# Add patterns only when the signal-to-noise ratio is clearly favorable.
#
# This hook can BLOCK (ho_block below), so it fails closed on a missing jq
# or malformed stdin rather than silently passing every write — see
# HOOK_FAIL_CLOSED in lib/hook-input.sh (security review C5).

set -eu

# Read by hi_init in hook-input.sh, which shellcheck cannot statically
# follow (HOOK_DIR is dynamic; excluded via -e SC1091 in CI).
# shellcheck disable=SC2034
HOOK_FAIL_CLOSED=1

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    Edit|Write|MultiEdit|NotebookEdit) ;;
    *) exit 0 ;;
esac

agent="$(hi_sender_role)"
file="$(hi_file_path)"

# Gather text-to-be-written. Union every content-bearing field this codebase
# knows about, rather than switching on $tool — a payload with a "swapped"
# shape (e.g. a Write carrying .new_string, or an Edit carrying .content)
# was previously invisible to this scan (security review M6), and
# NotebookEdit's .new_source was invisible outright (security review C4).
# MultiEdit's edits[] is iterated in full (verified: this hook has never
# had the "first-edit-only" bug some earlier notes suspected).
#
# Every value is filtered through `select(type == "string")` (security
# review M6 residue, round 2): a non-string at any of these paths — a
# number, an object, an array, a bool — used to make the whole jq
# expression error, `2>/dev/null || true` swallowed the error, and
# `write_text` came back empty with NO log record: the same fail-open
# shape C5 exists to close, just reached through a type mismatch instead
# of a missing jq. `.edits` is guarded to only iterate when it's actually
# an array, for the same reason. A jq value is silently DROPPED rather
# than stringified when it's the wrong type — this hook only scans literal
# string content, it doesn't walk into nested objects looking for secrets.
if ! write_text="$(jq -r '
    def as_str: if type == "string" then . else empty end;
    [
      (.tool_input.content // empty | as_str),
      (.tool_input.new_string // empty | as_str),
      (.tool_input.new_source // empty | as_str),
      ((.tool_input.edits // []) | (if type == "array" then . else [] end)[]?
        | (.new_string // empty | as_str))
    ]
    | map(select(. != null and . != ""))
    | join("\n")
' <<< "$(hi_raw)" 2>/dev/null)"; then
    # jq itself failed in some other unforeseen way — degraded input, same
    # class as a broken hi_init, same escape hatches (YAKOS_HOOKS_FAIL_OPEN
    # / hook-bypass.md).
    _hi_fail_or_warn "could not evaluate tool_input for secret patterns (jq error)"
    exit 0
fi

# If we don't have text, pass.
if [ -z "$write_text" ]; then
    exit 0
fi

# Patterns. Each entry: name|regex
PATTERNS=(
    'AWS Access Key|AKIA[0-9A-Z]{16}'
    'GitHub Token|ghp_[A-Za-z0-9]{36}'
    'GitHub Token (fine-grained)|github_pat_[A-Za-z0-9_]{82}'
    'PEM Private Key|-----BEGIN [A-Z0-9 ]*PRIVATE KEY'
    'Slack Token|xox[baprs]-[A-Za-z0-9-]{10,}'
    'Stripe Secret Key|sk_live_[A-Za-z0-9]{24,}'
    'Anthropic API Key|sk-ant-[A-Za-z0-9_-]{93}'
    'Google API Key|AIza[0-9A-Za-z_-]{35}'
)

matched_name=""
matched_pattern=""
for entry in "${PATTERNS[@]}"; do
    name="${entry%%|*}"
    pattern="${entry#*|}"
    # `-e "$pattern"` (not a bare "$pattern") so a pattern that starts with
    # "-" — the PEM rule above starts with "-----BEGIN" — is treated as the
    # regex it is, not parsed by grep as an option string (security review
    # H5a: this made the PEM rule dead code and printed
    # "grep: unrecognized option" to stderr on every non-matching write).
    if printf '%s' "$write_text" | grep -qE -e "$pattern"; then
        matched_name="$name"
        matched_pattern="$pattern"
        break
    fi
done

if [ -n "$matched_name" ]; then
    if ho_check_bypass "secret-scan" "$file"; then
        extra="$(jq -nc --arg agent "$agent" --arg file "$file" --arg name "$matched_name" \
            '{agent_type: $agent, file_path: $file, matched: $name, bypass: true}')"
        ho_log "secret-scan" "WARN" "pass" "match but bypass active" "$extra"
        exit 0
    fi
    extra="$(jq -nc --arg agent "$agent" --arg file "$file" --arg name "$matched_name" --arg pat "$matched_pattern" \
        '{agent_type: $agent, file_path: $file, matched: $name, pattern: $pat}')"
    ho_log "secret-scan" "BLOCK" "block" "secret pattern matched: $matched_name" "$extra"
    ho_block "secret-scan" "refused write to '$file': matches $matched_name pattern. Either remove the secret, or add a current bypass entry to work/current/hook-bypass.md if this is intentional."
fi

extra="$(jq -nc --arg agent "$agent" --arg file "$file" --arg tool "$tool" \
    '{agent_type: $agent, file_path: $file, tool: $tool}')"
ho_log "secret-scan" "REPORT" "pass" "no secret patterns matched" "$extra"
exit 0
