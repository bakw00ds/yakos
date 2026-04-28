#!/usr/bin/env bash
# secret-scan.sh — PreToolUse hook on Edit|Write|MultiEdit.
#
# Refuses tool calls whose written content matches well-known secret
# patterns (AWS keys, GitHub tokens, generic API keys, private-key blocks).
# Exit 2 to BLOCK; surfaces a clear message to the agent.
#
# v0.1 patterns are deliberately conservative — false positives are worse
# than false negatives at this layer because hooks run on every Edit/Write.
# Specialized scanners (gitleaks, trufflehog) belong in CI, not here.

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

# Gather text-to-be-written. For Write it's tool_input.content; for Edit
# it's tool_input.new_string; for MultiEdit it's an array of new_strings.
write_text=""
case "$tool" in
    Write)      write_text="$(hi_content)" ;;
    Edit)       write_text="$(hi_new_string)" ;;
    MultiEdit)  write_text="$(jq -r '[.tool_input.edits[]?.new_string] | join("\n")' <<< "$(hi_raw)" 2>/dev/null || true)" ;;
esac

# If we don't have text, pass.
if [ -z "$write_text" ]; then
    exit 0
fi

# Patterns. Each entry: name|regex
PATTERNS=(
    'AWS Access Key|AKIA[0-9A-Z]{16}'
    'GitHub Token|ghp_[A-Za-z0-9]{36}'
    'GitHub Token (fine-grained)|github_pat_[A-Za-z0-9_]{82}'
    'PEM Private Key|-----BEGIN (RSA |EC |OPENSSH |DSA |)PRIVATE KEY-----'
    'Slack Token|xox[baprs]-[A-Za-z0-9-]{10,}'
    'Stripe Secret Key|sk_live_[A-Za-z0-9]{24,}'
)

matched_name=""
matched_pattern=""
for entry in "${PATTERNS[@]}"; do
    name="${entry%%|*}"
    pattern="${entry#*|}"
    if printf '%s' "$write_text" | grep -qE "$pattern"; then
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
