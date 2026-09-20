#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: path-allowlist.sh — PreToolUse hook on Edit|Write|MultiEdit|NotebookEdit.
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
#   - If the path lexically escapes the project root (residual ".." after
#     normalization) or resolves through a symlink to outside the project
#     root: BLOCK, before any allow/deny matching (security review C3/M7).
#   - If 'deny' matches: BLOCK.
#   - If 'allow' is set and no glob matches: BLOCK.
#   - Otherwise: PASS.
#
# Phase 0 Test 6a confirmed exit-2 from a PreToolUse script blocks the
# tool call and surfaces stderr to the calling agent.
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
. "$HOOK_DIR/lib/path-safety.sh"

hi_init

tool="$(hi_tool)"
case "$tool" in
    Edit|Write|MultiEdit|NotebookEdit) ;;
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

# ---- C3: lexical traversal guard --------------------------------------------
#
# Normalize BEFORE matching. Without this, "api/../../../../etc/cron.d/pwn"
# satisfies an allow glob of "api/**" (bash `case` treats '*' as spanning
# '/', and there was no normalization at all). Any path that still climbs
# above its starting point after normalization is rejected outright,
# regardless of what the allow/deny lists say — a traversal is never a
# legitimate edit target.
norm_rel_file="$(ps_lexical_normalize "$rel_file")"
if ps_escapes_root "$norm_rel_file"; then
    if ho_check_bypass "path-allowlist" "$rel_file"; then
        extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg norm "$norm_rel_file" \
            '{agent_type: $agent, file_path: $file, normalized: $norm, note: "traversal but bypass active", bypass: true}')"
        ho_log "path-allowlist" "WARN" "pass" "path traversal detected but bypass active" "$extra"
        exit 0
    fi
    extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg norm "$norm_rel_file" \
        '{agent_type: $agent, file_path: $file, normalized: $norm}')"
    ho_log "path-allowlist" "BLOCK" "block" "path lexically escapes project root" "$extra"
    ho_block "path-allowlist" "agent '$agent' path '$rel_file' normalizes to '$norm_rel_file', which escapes the project root — refused regardless of allow/deny policy"
fi
rel_file="$norm_rel_file"

# ---- M7: symlink-escape guard -----------------------------------------------
#
# A path can be lexically fine ("api/link.go") and still point, via a
# symlink somewhere in its chain, at a file outside the project entirely.
# Resolve the real, symlink-free path and confirm it is still inside the
# (also-resolved) project root before trusting any allow/deny match.
if [ -n "${CLAUDE_PROJECT_DIR:-}" ] && [ -d "$CLAUDE_PROJECT_DIR" ]; then
    project_real="$(ps_realpath "$CLAUDE_PROJECT_DIR")"
    target_real="$(ps_realpath "$CLAUDE_PROJECT_DIR/$rel_file")"
    if ! ps_is_within "$project_real" "$target_real"; then
        if ho_check_bypass "path-allowlist" "$rel_file"; then
            extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg real "$target_real" \
                '{agent_type: $agent, file_path: $file, resolved: $real, note: "symlink escape but bypass active", bypass: true}')"
            ho_log "path-allowlist" "WARN" "pass" "symlink escape detected but bypass active" "$extra"
            exit 0
        fi
        extra="$(jq -nc --arg agent "$agent" --arg file "$rel_file" --arg real "$target_real" --arg root "$project_real" \
            '{agent_type: $agent, file_path: $file, resolved: $real, project_root: $root}')"
        ho_log "path-allowlist" "BLOCK" "block" "path resolves outside project root via symlink" "$extra"
        ho_block "path-allowlist" "agent '$agent' path '$rel_file' resolves (following symlinks) to '$target_real', which is outside the project root — refused regardless of allow/deny policy"
    fi
fi

# ---- glob matching -----------------------------------------------------------
#
# Plain bash `case`-glob matching, not python3 fnmatch — there is no python3
# dependency here (an earlier version of this comment claimed otherwise;
# the code has always been pure bash). Matching is case-INSENSITIVE
# (security review H5b: macOS/Windows default filesystems are
# case-insensitive, so a case-sensitive deny of ".env" was bypassable by
# writing ".ENV" to the same inode) — both the glob and the candidate path
# are lowercased before comparison. Notes on `**`:
#   - bash `case` does not support `**` (double-star) natively; we collapse
#     `**` to `*` as a second attempt so `dir/**` patterns still work, but
#     `*` in bash glob-case DOES span `/` — so "api/*" already matches
#     "api/a/b/c". Prefer being explicit about depth in policy files.
#   - basename fallback for ALLOW decisions is explicitly prohibited: it can
#     cause a path like "malicious/api/foo.go" to match an allow glob of
#     "api/*.go" via basename, bypassing the prefix constraint entirely.
#
# For DENY patterns we retain the collapsed-`**` bash fallback AND a
# basename fallback, so deny stays conservative (may over-block, which is
# the safe direction).

_lc() {
    printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

# _fnmatch_exact <glob> <path>  → exit 0 if glob matches path exactly
# (case-insensitively).
_fnmatch_exact() {
    local g p g2
    g="$(_lc "$1")"
    p="$(_lc "$2")"
    # SC2254: unquoted $g is intentional — pattern for case, not literal.
    # shellcheck disable=SC2254
    case "$p" in
        $g) return 0 ;;
        *) ;;
    esac
    # Collapse `**` → `*` and try once more (handles common `dir/**` patterns).
    g2="${g//\*\*/*}"
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
    local g p
    g="$1" p="$2"
    _fnmatch_exact "$g" "$p" && return 0
    g="$(_lc "$g")"
    p="$(_lc "$(basename "$p")")"
    # shellcheck disable=SC2254
    case "$p" in
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
