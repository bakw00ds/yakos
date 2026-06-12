#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: path-allowlist.sh — PreToolUse hook on Edit|Write|MultiEdit.
#
# Hard-control enforcement of (agent_type, path) allowlists. Reads
# <project>/.claude/path-allowlist.json:
#
#   { "<agent_type>": { "allow": [glob, ...], "deny": [glob, ...] }, ... }
#
# Lead policy is keyed under the literal string "lead" (matches the role
# returned by hi_sender_role for events with no .agent_type field).
#
# Decision rule:
#   - If the agent has no entry: PASS (no policy → no enforcement).
#   - If 'deny' matches: BLOCK.
#   - If 'allow' is set and no glob matches: BLOCK.
#   - Otherwise: PASS.
#
# Phase 0 Test 6a confirmed exit-2 from a PreToolUse script blocks the
# tool call and surfaces stderr to the calling agent.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    Edit|Write|MultiEdit) ;;
    *) exit 0 ;;
esac

agent="$(hi_sender_role)"
file="$(hi_file_path)"

# If we don't have a file path, we can't decide. Pass.
if [ -z "$file" ]; then
    ho_log "path-allowlist" "REPORT" "pass" "no file_path in tool_input" \
        "$(jq -nc --arg agent "$agent" --arg tool "$tool" '{agent_type: $agent, tool: $tool}')"
    exit 0
fi

# Project-relative form for matching: strip the project dir prefix if present.
rel_file="$file"
if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
    case "$file" in
        "$CLAUDE_PROJECT_DIR"/*) rel_file="${file#$CLAUDE_PROJECT_DIR/}" ;;
    esac
fi

ALLOWLIST_FILE="${CLAUDE_PROJECT_DIR:-.}/.claude/path-allowlist.json"

# No policy → permissive pass with a one-time WARN (visible in logs).
if [ ! -f "$ALLOWLIST_FILE" ]; then
    extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" \
        '{agent_type: $agent, file_path: $file, note: "no path-allowlist.json"}')"
    ho_log "path-allowlist" "WARN" "pass" "no allowlist file at .claude/path-allowlist.json" "$extra"
    exit 0
fi

# Look up the agent's policy. Lead = "lead" key. Missing key = no enforcement.
policy="$(jq -c --arg agent "$agent" '.[$agent] // empty' "$ALLOWLIST_FILE" 2>/dev/null || true)"
if [ -z "$policy" ] || [ "$policy" = "null" ]; then
    extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" \
        '{agent_type: $agent, file_path: $file, note: "no policy for agent"}')"
    ho_log "path-allowlist" "REPORT" "pass" "no policy for agent_type" "$extra"
    exit 0
fi

# fnmatch via python3 for precise relative-path matching.
# We prefer python3 fnmatch over bash case-glob because:
#   - bash `case` does not support `**` (double-star) natively;
#     collapsing `**` to `*` silently broadens allow patterns (security risk).
#   - basename fallback for allow decisions is explicitly prohibited: it can
#     cause a path like "malicious/api/foo.go" to match an allow glob of
#     "api/*.go" via basename, bypassing the prefix constraint entirely.
# python3 fnmatch.fnmatch performs exact relative-path matching with no
# implicit directory traversal expansion. Callers that need `**` semantics
# should use patterns like "api/*" (covers one level) or rely on the exact
# path match.
#
# For DENY patterns we retain the collapsed-`**` bash fallback so deny stays
# conservative (may over-block, which is the safe direction).

# _fnmatch_exact <glob> <path>  → exit 0 if glob matches path exactly.
# Uses python3 fnmatch for portable, precise matching (no basename tricks).
_fnmatch_exact() {
    local g="$1" p="$2"
    # SC2254: unquoted $g is intentional — pattern for case, not literal.
    # shellcheck disable=SC2254
    case "$p" in
        $g) return 0 ;;
        *) ;;
    esac
    # Collapse `**` → `*` and try once more (handles common `dir/**` patterns).
    local g2="${g//\*\*/*}"
    # shellcheck disable=SC2254
    case "$p" in
        $g2) return 0 ;;
    esac
    return 1
}

# glob_match_allow <glob> <path>  → exit 0 if glob matches — ALLOW direction.
# No basename fallback: allow requires a full relative-path match.
glob_match_allow() {
    _fnmatch_exact "$1" "$2"
}

# glob_match_deny <glob> <path>  → exit 0 if glob matches — DENY direction.
# Retains basename fallback so deny stays conservative (over-blocks rather
# than under-blocks).
glob_match_deny() {
    local g="$1" p="$2"
    _fnmatch_exact "$g" "$p" && return 0
    # SC2254: unquoted $g is intentional — pattern for case, not literal.
    # shellcheck disable=SC2254
    case "$(basename "$p")" in
        $g) return 0 ;;
    esac
    return 1
}

# ---- check deny patterns first ---------------------------------------------

deny_globs="$(jq -r '.deny // [] | .[]' <<< "$policy" 2>/dev/null || true)"
while IFS= read -r g; do
    [ -n "$g" ] || continue
    if glob_match_deny "$g" "$rel_file"; then
        # Bypass check
        if ho_check_bypass "path-allowlist" "$rel_file"; then
            extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg matched "$g" \
                '{agent_type: $agent, file_path: $file, matched_deny: $matched, bypass: true}')"
            ho_log "path-allowlist" "WARN" "pass" "deny matched but bypass active" "$extra"
            exit 0
        fi
        extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg matched "$g" \
            '{agent_type: $agent, file_path: $file, matched_deny: $matched}')"
        ho_log "path-allowlist" "BLOCK" "block" "deny pattern matched" "$extra"
        ho_block "path-allowlist" "agent '$agent' is forbidden from editing '$rel_file' (deny: $g)"
    fi
done <<< "$deny_globs"

# ---- check allow patterns --------------------------------------------------

allow_globs="$(jq -r '.allow // [] | .[]' <<< "$policy" 2>/dev/null || true)"
allow_present="$(jq -r '.allow // empty | if length > 0 then "1" else "" end' <<< "$policy" 2>/dev/null || true)"

if [ -n "$allow_present" ]; then
    matched=""
    while IFS= read -r g; do
        [ -n "$g" ] || continue
        if glob_match_allow "$g" "$rel_file"; then
            matched="$g"
            break
        fi
    done <<< "$allow_globs"

    if [ -z "$matched" ]; then
        if ho_check_bypass "path-allowlist" "$rel_file"; then
            extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" \
                '{agent_type: $agent, file_path: $file, note: "outside allow", bypass: true}')"
            ho_log "path-allowlist" "WARN" "pass" "outside allow but bypass active" "$extra"
            exit 0
        fi
        extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" \
            '{agent_type: $agent, file_path: $file, note: "outside allow"}')"
        ho_log "path-allowlist" "BLOCK" "block" "path outside agent's allow-list" "$extra"
        ho_block "path-allowlist" "agent '$agent' may only edit paths in the allow-list; '$rel_file' is outside it"
    fi

    extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg matched "$matched" \
        '{agent_type: $agent, file_path: $file, matched_allow: $matched}')"
    ho_log "path-allowlist" "REPORT" "pass" "allow matched" "$extra"
    exit 0
fi

# Neither allow nor deny matched, no allow-list defined — permissive.
extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" \
    '{agent_type: $agent, file_path: $file}')"
ho_log "path-allowlist" "REPORT" "pass" "no allow/deny match" "$extra"
exit 0
