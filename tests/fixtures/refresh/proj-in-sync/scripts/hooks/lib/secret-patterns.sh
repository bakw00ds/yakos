#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: secret-patterns.sh — the one secret-detection pattern table shared by
# secret-scan.sh (blocks writes that match) and supervisor-stream.sh (redacts
# matches from the previews it buffers for the supervisor LLM). Each entry is
# "name|grep -E regex", first match wins in secret-scan. Go twin:
# cli-go/internal/hooks/secretscan DefaultPatterns; keep the two identical.
#
# Usage:
#     . "$HOOK_DIR/lib/secret-patterns.sh"      # sets YAKOS_SECRET_PATTERNS
#
# The regexes must stay free of "#" (supervisor-stream feeds them to sed s###).
#
# YAKOS_REDACT_EXTRA_PATTERNS is REDACTION-ONLY (supervisor-stream previews):
# generic Bearer / KEY=VALUE shapes too loose to block a write on. secret-scan
# does not read it. Go twin: secretscan.redactExtra.
#
# YAKOS_REDACT_BLOCK_PATTERNS is REDACTION-ONLY and applied FIRST, over the
# whole (newline-slurped) preview: multi-line PEM private-key blocks, so the
# key BODY is redacted and not just the "-----BEGIN" header line the blocking
# table catches. Go twin: secretscan.redactBlockSources ((?s) dot-all).

if [ "${YAKOS_SECRET_PATTERNS_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi

# shellcheck disable=SC2034  # consumed by the sourcing hooks
YAKOS_SECRET_PATTERNS=(
    'AWS Access Key|AKIA[0-9A-Z]{16}'
    'GitHub Token|ghp_[A-Za-z0-9]{36}'
    'GitHub Token (fine-grained)|github_pat_[A-Za-z0-9_]{82}'
    'PEM Private Key|-----BEGIN [A-Z0-9 ]*PRIVATE KEY'
    'Slack Token|xox[baprs]-[A-Za-z0-9-]{10,}'
    'Stripe Secret Key|sk_live_[A-Za-z0-9]{24,}'
    'Anthropic API Key|sk-ant-[A-Za-z0-9_-]{93}'
    'Google API Key|AIza[0-9A-Za-z_-]{35}'
)

# shellcheck disable=SC2034  # consumed by supervisor-stream.sh
YAKOS_REDACT_BLOCK_PATTERNS=(
    'PEM block|-----BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----.*-----END [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----'
    'PEM block (truncated)|-----BEGIN [A-Z0-9 ]*PRIVATE KEY( BLOCK)?-----.*'
)

# YAKOS_REDACT_KEEP_PATTERNS is REDACTION-ONLY too, but each rule's group 1 is
# context to KEEP: only the rest of the match is replaced (sed \1[REDACTED]),
# so a preview still reads "curl -s -u [REDACTED] https://...". Go twin:
# secretscan.redactKeepSources.
# shellcheck disable=SC2034  # consumed by supervisor-stream.sh
YAKOS_REDACT_KEEP_PATTERNS=(
    "curl basic auth|((curl|wget|xh)[^|;&]*[[:space:]](-[A-Za-z]*[uU][[:space:]]*|--(proxy-)?user([[:space:]]+|=)))(\"[^\"]*:[^\"]*\"|'[^']*:[^']*'|[^[:space:]:\"']+:[^[:space:]]+)"
)

# shellcheck disable=SC2034  # consumed by supervisor-stream.sh
YAKOS_REDACT_EXTRA_PATTERNS=(
    'Bearer credential|[Bb][Ee][Aa][Rr][Ee][Rr][[:space:]]+[^[:space:]]{8,}'
    'KEY=VALUE credential|([Tt][Oo][Kk][Ee][Nn]|[Pp][Aa][Ss][Ss][Ww]([Oo][Rr])?[Dd]|[Ss][Ee][Cc][Rr][Ee][Tt]|[Aa][Pp][Ii][_-]?[Kk][Ee][Yy]).?[[:space:]]*[=:][[:space:]]*.?[^[:space:]]{8,}'
    'URL credentials|://[^[:space:]/@:]*:[^[:space:]@]+@'
)

# Must stay the last statement: reaching it proves the whole file parsed.
YAKOS_SECRET_PATTERNS_LOADED=1
