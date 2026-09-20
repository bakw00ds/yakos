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
# `.. | strings` recurses into each of content/new_string/new_source/the
# WRITTEN half of edits and collects every string leaf, regardless of
# what shape the field itself turns out to be (security review R2-4,
# round 3, superseding the round-2 `select(type == "string")` per-field
# filter): a per-field filter DROPPED a secret sitting inside an array of
# strings or a nested object (round 2's own fix left this gap — M6
# residue PARTIAL) — e.g. `content: ["...", "AKIA..."]` or
# `new_source: ["line1", "AKIA..."]` (a Jupyter cell's `source` is
# canonically an array of strings, so this is a plausible real shape, not
# just a theoretical one). `.. | strings` cannot error regardless of the
# value's shape (number, object, array, null all just contribute zero
# string leaves), so the `jq` call itself can now only fail for a reason
# unrelated to field shape.
#
# `.edits` is mapped to `.new_string` BEFORE the walk (security review
# R3-3, round 4 — a regression the R2-4 fix introduced): walking `.edits`
# whole also scanned each edit's `old_string` — the text being REPLACED,
# not written — while a top-level Edit's `old_string` was never scanned.
# That asymmetry blocked the natural incident-response move (MultiEdit
# redacting several leaked-secret occurrences at once) while the
# single-edit form of the identical remediation passed. Decision: scan
# only what is actually being WRITTEN, consistently, for both tool
# shapes — matching origin/main's original behavior (which never scanned
# old_string at all) rather than the round-2 asymmetry. A secret an agent
# is actively deleting is not a new leak; a secret it is introducing or
# leaving in place is.
if ! write_text="$(jq -r '
    [
      .tool_input
      | (.content, .new_string, .new_source,
         ((.edits // []) | if type == "array" then map(.new_string) else [] end))
      | .. | strings
    ]
    | join("\n")
' <<< "$(hi_raw)" 2>/dev/null)"; then
    # jq itself failed in some other unforeseen way — degraded input, same
    # class as a broken hi_init, same escape hatches (YAKOS_HOOKS_FAIL_OPEN
    # / hook-bypass.md).
    _hi_fail_or_warn "could not evaluate tool_input for secret patterns (jq error)"
    exit 0
fi

# If we don't have text, pass — but leave a REPORT record (security review
# R2-4, round 3): this used to be a bare `exit 0` with no log record at
# all, so a write with no scannable content and a genuinely-skipped scan
# were indistinguishable from each other in secret-scan.ndjson.
if [ -z "$write_text" ]; then
    extra="$(jq -nc --arg agent "$agent" --arg file "$file" --arg tool "$tool" \
        '{agent_type: $agent, file_path: $file, tool: $tool}' 2>/dev/null \
        || printf '{"agent_type":"%s","file_path":"%s","tool":"%s"}' "$agent" "$file" "$tool")"
    ho_log "secret-scan" "REPORT" "pass" "no content-bearing string fields to scan" "$extra"
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
