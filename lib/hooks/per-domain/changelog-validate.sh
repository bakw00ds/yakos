#!/usr/bin/env bash
# changelog-validate.sh — per-domain validator for changelog citations.
#
# Enforces the convention: every BE-only change shipped under a combined
# release MUST include a "Feedback #<8hex>" citation in the changelog
# entry it added (see PandaOS rule:changelog).
#
# Inputs:
#   $1   path to the changelog file or directory; defaults to scanning
#        common locations.
#
# Heuristic: in the lines added in the last commit, if any new
# changelog entry exists, at least one must include the Feedback citation
# OR a "[no-feedback]" tag for explicit opt-out (security or refactor
# changes that genuinely have no feedback to cite).

set -eu

TARGET="${1:-}"
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"

emit_json() {
    local decision="$1" reason="$2" extra="${3:-}"
    [ -z "$extra" ] && extra='{}'
    jq -nc \
        --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        --arg validator "changelog" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg target "$TARGET" \
        --argjson extra "$extra" \
        '{ts: $ts, validator: $validator, decision: $decision, reason: $reason, target: $target} + $extra'
}

cd "$PROJECT_DIR" 2>/dev/null || { emit_json "pass" "no project dir"; exit 0; }

# Identify candidate changelog files
candidates=""
[ -n "$TARGET" ] && [ -f "$TARGET" ] && candidates="$TARGET"
if [ -z "$candidates" ]; then
    for f in \
        "web/src/lib/changelog.ts" \
        "src/lib/changelog.ts" \
        "CHANGELOG.md" \
        "docs/CHANGELOG.md"; do
        [ -f "$f" ] && candidates="${candidates}${f}\n"
    done
fi

if [ -z "$candidates" ]; then
    emit_json "pass" "no changelog file found in known locations"
    exit 0
fi

# Pull added lines from the last commit, restricted to changelog files.
if ! command -v git >/dev/null 2>&1 || ! git rev-parse --git-dir >/dev/null 2>&1; then
    emit_json "warn" "git not available; cannot inspect last commit"
    exit 0
fi

added_lines="$(printf '%s' "$candidates" | tr ' ' '\n' | while read -r f; do
    [ -n "$f" ] || continue
    git diff --cached --no-color -- "$f" 2>/dev/null | grep -E '^\+' | grep -vE '^\+\+\+ ' || true
    git log -1 --no-color -p -- "$f" 2>/dev/null | grep -E '^\+' | grep -vE '^\+\+\+ ' || true
done)"

if [ -z "$added_lines" ]; then
    emit_json "pass" "no changelog additions in recent diff"
    exit 0
fi

# Look for citation or opt-out
if printf '%s' "$added_lines" | grep -qE 'Feedback[[:space:]]*#[0-9a-fA-F]{8}|\[no-feedback\]'; then
    emit_json "pass" "changelog entry includes Feedback # citation or [no-feedback] opt-out"
    exit 0
fi

# Only fire if the additions look like changelog entries (heuristic:
# multi-character non-comment lines).
substantive_lines="$(printf '%s' "$added_lines" | grep -vE '^\+[[:space:]]*$|^\+[[:space:]]*//|^\+[[:space:]]*\*' || true)"
if [ -z "$substantive_lines" ]; then
    emit_json "pass" "no substantive changelog additions"
    exit 0
fi

extra="$(jq -nc --arg lines "$(printf '%s' "$substantive_lines" | head -n 5)" \
    '{sample_added_lines: $lines}')"
emit_json "block" "changelog additions missing 'Feedback #<8hex>' citation (or [no-feedback] opt-out)" "$extra"
exit 2
