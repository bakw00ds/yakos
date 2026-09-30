#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervisor-stream.sh — PostToolUse hook that streams mutation events
# to the live supervisor (v0.34+).
#
# Design (Option 2 supervisor redesign):
#   Matcher in settings.json limits invocation to Edit|Write|MultiEdit|Bash —
#   reads never reach this hook. Within those mutation calls, a local
#   shell-only pre-filter runs FIRST:
#
#     Sensitive path   → file matches a deny-glob in .claude/path-allowlist.json
#     Large diff        → edit produced > supervisor.pre_filter.min_diff_lines
#     Out-of-scope edit → touched file not referenced in decisions.md / plan.md
#     Risk regex        → content matches a known dangerous-command pattern
#
#   NONE trip  → append to buffer and return (no LLM dispatch, ~0 cost).
#   ANY trip   → log trigger reason + proceed to score-dispatch path.
#
#   Only ESCALATIONS count toward score_every_n_calls.
#   When dispatch fires, it runs nohup … & disown (async, does NOT block
#   PostToolUse return).
#
# Model tier: reads supervisor.model from .yakos.yml (default: haiku).
# TODO: supervisor.runtime: local-llm → Ollama integration (future PR).
#
# Never blocks. Always exits 0. This is telemetry, not policy.
# Policy enforcement happens in supervisor-gate.sh (PreToolUse).

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
. "$HOOK_DIR/lib/hook-input.sh"
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# Skip if supervisor mode disabled via env
if [ "${YAKOS_SUPERVISOR_DISABLE:-0}" = "1" ]; then
    exit 0
fi

# Skip if .yakos.yml says supervisor.enabled: false. Use awk to extract
# only the direct children of supervisor: (2-space indent), not nested
# keys. grep -A would match nested 'enabled: false' (e.g. pre_filter.enabled).
project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
yakos_yml="$project_dir/.yakos.yml"
_supervisor_enabled() {
    # Extracts only the direct-child keys of supervisor: (those with exactly
    # 2-space indent). Returns "false" if enabled: false, "true" otherwise.
    LC_ALL=C awk '
        /^supervisor:[[:space:]]*$/ { in_s=1; next }
        in_s && /^[^[:space:]#]/ { exit }
        in_s && /^  [a-z_][a-z_]*:/ { print; next }
        in_s { next }
    ' "$1" 2>/dev/null | grep -E '^  enabled:[[:space:]]*false[[:space:]]*$'
}
if [ -f "$yakos_yml" ]; then
    if _supervisor_enabled "$yakos_yml" | grep -q .; then
        exit 0
    fi
fi

command -v jq >/dev/null 2>&1 || exit 0
command -v yakos_current_dir >/dev/null 2>&1 || exit 0

current_dir="$(yakos_current_dir)"
[ -d "$current_dir" ] || exit 0

buffer="$current_dir/supervisor-buffer.ndjson"
counter="$current_dir/.supervisor-counter"
findings="$current_dir/supervisor-findings.ndjson"

# --- Read config from .yakos.yml -------------------------------------------

# supervisor.pre_filter.enabled (default: true)
pre_filter_enabled="true"
if [ -f "$yakos_yml" ]; then
    pf="$(grep -A 30 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -A 10 '^[[:space:]]*pre_filter:' \
        | grep -E '^[[:space:]]*enabled:[[:space:]]*(true|false)' \
        | head -1 | awk '{print $2}' | tr -d '[:space:]')"
    [ "$pf" = "false" ] && pre_filter_enabled="false"
fi

# supervisor.pre_filter.min_diff_lines (default: 20)
min_diff_lines=20
if [ -f "$yakos_yml" ]; then
    mdl="$(grep -A 30 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -A 10 '^[[:space:]]*pre_filter:' \
        | grep -E '^[[:space:]]*min_diff_lines:[[:space:]]*[0-9]+' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    case "$mdl" in
        ''|*[!0-9]*) : ;;
        *) min_diff_lines="$mdl" ;;
    esac
fi

# supervisor.model (default: haiku)
sup_model="haiku"
if [ -f "$yakos_yml" ]; then
    m="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*model:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$m" ] && sup_model="$m"
fi

# --- 1. Append to buffer ----------------------------------------------------

agent="$(hi_sender_role)"
tool="$(hi_tool)"
file_path="$(hi_file_path)"
session_id="$(hi_session_id)"
ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Scan text vs stored preview (K-112):
#   - the RISK REGEXES see the unredacted text: 300 bytes of new_string/content
#     as before, and the FULL Bash command and description (a 2048-byte cap let
#     "<2100 B padding> rm -rf /" through);
#   - the BUFFER gets 300-byte previews with secret-table matches redacted
#     first (the supervisor LLM reads the buffer), then capped. Go twin:
#     supervisorstream.go.
# Edit/Write text is scanned in full too (bounded: head + tail beyond 64 KiB),
# so padding before a risky snippet cannot hide it; new_scan/content_scan (300
# bytes) still drive the large-diff check as before.
_ss_bound() {
    local n
    n="$(printf '%s' "$1" | wc -c | tr -d ' ')"
    if [ "${n:-0}" -le 65536 ]; then
        printf '%s' "$1"
    else
        printf '%s\n%s' "$(printf '%s' "$1" | head -c 32768)" "$(printf '%s' "$1" | tail -c 32768)"
    fi
}
new_full="$(hi_new_string 2>/dev/null || true)"
content_full="$(hi_content 2>/dev/null || true)"
new_scan="$(printf '%s' "$new_full" | head -c 300)"
content_scan="$(printf '%s' "$content_full" | head -c 300)"
new_risk="$(_ss_bound "$new_full")"
content_risk="$(_ss_bound "$content_full")"
command_scan="$(hi_field '.tool_input.command' 2>/dev/null || true)"
description_scan="$(hi_field '.tool_input.description' 2>/dev/null || true)"

# Redaction: one sed over the shared table lib/secret-patterns.sh. If the table
# cannot be loaded the previews are withheld rather than stored unredacted.
# Newline-slurp first (portable BSD/GNU loop) so multi-line PEM blocks match.
_ss_sed_args=(-e ':a' -e '$!{N;ba' -e '}')
_ss_redact_ok=0
if [ -r "$HOOK_DIR/lib/secret-patterns.sh" ] && ( . "$HOOK_DIR/lib/secret-patterns.sh" ) >/dev/null 2>&1 \
    && . "$HOOK_DIR/lib/secret-patterns.sh" && [ "${YAKOS_SECRET_PATTERNS_LOADED:-0}" = "1" ]; then
    # Multi-line blocks (PEM bodies) first: the blocking table would otherwise
    # eat the header and leave the key body behind.
    for _ss_entry in "${YAKOS_REDACT_BLOCK_PATTERNS[@]}"; do
        _ss_sed_args+=(-e "s#${_ss_entry#*|}#[REDACTED]#g")
    done
    for _ss_entry in "${YAKOS_SECRET_PATTERNS[@]}"; do
        _ss_sed_args+=(-e "s#${_ss_entry#*|}#[REDACTED]#g")
    done
    # Keep-context rules: group 1 (the command and flag) stays, the secret goes.
    # Each rule loops to a fixpoint (:label / s / t label) because the kept
    # prefix is greedy: one pass redacts only the last credential on a line.
    _ss_k=0
    for _ss_entry in "${YAKOS_REDACT_KEEP_PATTERNS[@]}"; do
        _ss_k=$((_ss_k + 1))
        _ss_sed_args+=(-e ":keep$_ss_k" -e "s#${_ss_entry#*|}#\\1[REDACTED]#" -e "tkeep$_ss_k")
    done
    # Redaction-only generic Bearer / KEY=VALUE rules (never used to block).
    for _ss_entry in "${YAKOS_REDACT_EXTRA_PATTERNS[@]}"; do
        _ss_sed_args+=(-e "s#${_ss_entry#*|}#[REDACTED]#g")
    done
    _ss_redact_ok=1
fi
# _ss_preview <text>: redact (on up to 4096 bytes, so a token cannot straddle
# the cut), then cap at 300 bytes.
_ss_preview() {
    [ -n "$1" ] || return 0
    [ "$_ss_redact_ok" = "1" ] || return 0
    printf '%s' "$1" | head -c 4096 | LC_ALL=C sed -E "${_ss_sed_args[@]}" | head -c 300 || true
}
new_preview="$(_ss_preview "$new_full")"
content_preview="$(_ss_preview "$content_full")"
command_preview="$(_ss_preview "$command_scan")"
description_preview="$(_ss_preview "$description_scan")"

event="$(jq -nc \
    --arg ts "$ts" \
    --arg agent "$agent" \
    --arg tool "$tool" \
    --arg file "$file_path" \
    --arg new "$new_preview" \
    --arg content "$content_preview" \
    --arg cmd "$command_preview" \
    --arg desc "$description_preview" \
    --arg sid "$session_id" \
    '{ts: $ts, agent: $agent, tool: $tool,
      input: ({file_path: $file,
              new_preview: (if $new == "" then null else $new end),
              content_preview: (if $content == "" then null else $content end)}
              + (if $cmd == "" then {} else {command_preview: $cmd} end)
              + (if $desc == "" then {} else {description_preview: $desc} end)),
      session_id: $sid}' 2>/dev/null)"

[ -n "$event" ] || exit 0
# The buffer feeds an LLM and holds command lines: owner-only (K-112).
( umask 077; printf '%s\n' "$event" >> "$buffer" ) 2>/dev/null || exit 0
chmod 600 "$buffer" 2>/dev/null || true

# Trim buffer to last 50 lines (rolling window)
if [ -f "$buffer" ]; then
    buf_lines="$(wc -l < "$buffer" 2>/dev/null | tr -d ' ')"
    if [ "${buf_lines:-0}" -gt 50 ]; then
        tmp="$buffer.tmp.$$"
        ( umask 077; tail -n 50 "$buffer" > "$tmp" ) 2>/dev/null && mv "$tmp" "$buffer" 2>/dev/null
        chmod 600 "$buffer" 2>/dev/null || true
    fi
fi

# --- K-111 P2b: shadow decision provider (async, never blocks) --------------
# When a decision provider is configured, ask it the supervisor-prefilter
# question set about this event in SHADOW mode and log its verdict next to the
# local pre-filter verdict (`yakos decide compare` reads them back). The call
# is detached: this hook never waits for it, never reads its result and never
# changes its own exit code, buffer, counter or escalation path. With provider
# none (the default) _ss_provider prints nothing and _ss_shadow returns before
# doing any work. Go twin: internal/hooks/supervisorstream/shadow.go.
#
# Provider resolution matches `yakos decide`: YAKOS_DECISION_DISABLE=1, then
# $YAKOS_DECISION_PROVIDER, then decisions.provider (a direct child of the
# top-level decisions: block) in .yakos.yml.
_ss_provider() {
    [ "${YAKOS_DECISION_DISABLE:-0}" = "1" ] && return 0
    local p="${YAKOS_DECISION_PROVIDER:-}" content=""
    if [ -z "$p" ] && [ -f "$yakos_yml" ]; then
        # $(<file) is a builtin read (no exec): the common no-decisions: case
        # costs no extra process.
        content="$(<"$yakos_yml")" 2>/dev/null || content=""
        case "$content" in
            *decisions:*)
                p="$(awk '
                    /^decisions:[[:space:]]*(#.*)?$/ { in_d = 1; next }
                    in_d && /^[^[:space:]#]/ { exit }
                    in_d && /^[[:space:]]+[A-Za-z_]/ {
                        match($0, /^[[:space:]]+/); ind = RLENGTH
                        if (base == 0) base = ind
                        if (ind == base && $0 ~ /^[[:space:]]+provider:/) {
                            v = $0
                            sub(/^[[:space:]]+provider:[[:space:]]*/, "", v)
                            sub(/[[:space:]]+#.*$/, "", v)
                            sub(/[[:space:]]+$/, "", v)
                            print v
                            exit
                        }
                    }
                ' "$yakos_yml" 2>/dev/null | tr -d "\"'" || true)"
                ;;
        esac
    fi
    printf '%s' "$p"
}

# _ss_shadow <pass|escalate> <escalate_reason>
_ss_shadow() {
    local verdict="$1" reason="${2:-}" prov cli state kind preview intent pm
    local -a dargs
    prov="$(_ss_provider)"
    case "$prov" in
        jev) [ -n "${TYPESAFE_API_KEY:-}" ] || return 0 ;;
        mock) : ;;
        *) return 0 ;;
    esac

    if [ -n "${YAKOS_CLI:-}" ]; then
        cli="$YAKOS_CLI"
    elif [ -n "${YAKOS_ROOT:-}" ] && [ -f "$YAKOS_ROOT/cli/yakos" ]; then
        cli="$YAKOS_ROOT/cli/yakos"
    else
        cli="$(command -v yakos 2>/dev/null || true)"
    fi
    [ -n "$cli" ] || return 0

    # State: only the fields lib/decisions/supervisor-prefilter.yaml allows.
    # The child redacts every string and cuts it to a 2 KiB preview.
    preview="$command_scan"
    [ -n "$preview" ] || preview="$new_full"
    [ -n "$preview" ] || preview="$content_full"
    preview="$(printf '%s' "$preview" | head -c 4096)"
    intent=""
    if [ -f "$current_dir/decisions.md" ]; then
        intent="$(head -c 1500 "$current_dir/decisions.md" 2>/dev/null || true)"
    fi
    pm=""
    if [ -n "$file_path" ] && { [ -f "$current_dir/decisions.md" ] || [ -f "$current_dir/plan.md" ]; }; then
        pm="false"
        local ref bn rel
        bn="$(basename -- "$file_path")"
        rel="${file_path#$project_dir/}"
        for ref in "$current_dir/decisions.md" "$current_dir/plan.md"; do
            [ -f "$ref" ] || continue
            if grep -qF "$bn" "$ref" 2>/dev/null || grep -qF "$rel" "$ref" 2>/dev/null; then
                pm="true"
                break
            fi
        done
    fi
    state="$(jq -nc --arg tool "$tool" --arg fp "$file_path" --arg prev "$preview" \
        --arg intent "$intent" --arg pm "$pm" \
        '{tool: $tool}
         + (if $prev == "" then {} else {command_or_diff_preview: $prev} end)
         + (if $fp == "" then {} else {file_path: $fp} end)
         + (if $intent == "" then {} else {stated_intent: $intent} end)
         + (if $pm == "" then {} else {plan_mentions_path: ($pm == "true")} end)' 2>/dev/null)" || return 0
    [ -n "$state" ] || return 0

    dargs=(decide supervisor-prefilter --shadow --local "$verdict")
    if [ "$verdict" = "escalate" ]; then
        kind="${reason%%:*}"
        [ -z "$kind" ] || dargs+=(--local-trigger "$kind")
    fi
    [ -z "$session_id" ] || dargs+=(--session "$session_id")
    dargs+=(--config "$yakos_yml")

    # Detached like the supervisor dispatch below; all fds closed to the hook.
    printf '%s' "$state" | nohup "$cli" "${dargs[@]}" >/dev/null 2>&1 &
    disown 2>/dev/null || true
    return 0
}

# --- 2. Local pre-filter (shell-only, zero LLM cost) -----------------------

# When pre_filter is disabled, every mutation counts toward the score counter
# and behaves like pre-redesign (every Nth call dispatches regardless).
if [ "$pre_filter_enabled" = "false" ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "pre-filter disabled; counting toward score threshold" \
        "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
            '{pre_filter: "disabled", tool: $tool, file: $file}')"
    # Fall through to counter/dispatch logic below
else
    # Pre-filter is enabled — run deterministic triage.
    # ESCALATE accumulates the first trigger reason found.
    escalate_reason=""

    # 2a. Sensitive-path check: file matches a deny-glob in path-allowlist.json
    if [ -z "$escalate_reason" ] && [ -n "$file_path" ]; then
        allowlist_file="$project_dir/.claude/path-allowlist.json"
        if [ -f "$allowlist_file" ] && command -v jq >/dev/null 2>&1; then
            # Extract deny globs for the lead (the agent initiating the edit)
            deny_globs="$(jq -r '(.lead.deny // []) | .[]' "$allowlist_file" 2>/dev/null || true)"
            if [ -n "$deny_globs" ]; then
                while IFS= read -r glob; do
                    [ -n "$glob" ] || continue
                    # Shell glob match against the file path
                    # Use case for portability (no extglob needed for simple globs)
                    # shellcheck disable=SC2254  # intentional: glob patterns from policy config
                    case "$file_path" in
                        $glob) escalate_reason="sensitive-path:$glob"; break ;;
                    esac
                    # Also strip leading **/ for partial path matching
                    bare_glob="${glob#\*\*/}"
                    # shellcheck disable=SC2254  # intentional: glob patterns from policy config
                    case "$file_path" in
                        */$bare_glob|$bare_glob) escalate_reason="sensitive-path:$glob"; break ;;
                    esac
                done <<< "$deny_globs"
            fi
        fi
    fi

    # 2b. Diff-size check: new_string or content line count > min_diff_lines
    if [ -z "$escalate_reason" ]; then
        diff_text="${new_scan}${content_scan}"
        if [ -n "$diff_text" ]; then
            diff_lines="$(printf '%s' "$diff_text" | wc -l | tr -d ' ')"
            if [ "${diff_lines:-0}" -gt "$min_diff_lines" ]; then
                escalate_reason="large-diff:${diff_lines}-lines"
            fi
        fi
    fi

    # 2c. Out-of-scope edit: touched file not referenced in decisions.md or plan.md
    if [ -z "$escalate_reason" ] && [ -n "$file_path" ]; then
        decisions="$current_dir/decisions.md"
        plan="$current_dir/plan.md"
        # Only apply this check when the reference files actually exist
        if [ -f "$decisions" ] || [ -f "$plan" ]; then
            found_in_scope=0
            # Strip the project dir prefix for the lookup — the reference files
            # typically mention relative paths or basenames.
            file_basename="$(basename -- "$file_path")"
            file_rel="${file_path#$project_dir/}"
            for ref_file in "$decisions" "$plan"; do
                [ -f "$ref_file" ] || continue
                if grep -qF "$file_basename" "$ref_file" 2>/dev/null || \
                   grep -qF "$file_rel" "$ref_file" 2>/dev/null; then
                    found_in_scope=1
                    break
                fi
            done
            if [ "$found_in_scope" -eq 0 ]; then
                escalate_reason="out-of-scope:$file_basename"
            fi
        fi
    fi

    # 2d. Risk-regex check: content matches a dangerous-command pattern.
    # Default patterns; extendable via .yakos.yml supervisor.pre_filter.risk_regex list.
    if [ -z "$escalate_reason" ] && [ -n "${new_risk}${content_risk}${command_scan}${description_scan}" ]; then
        # Newlines join to spaces so a line-continued "curl x \<nl>| sh" matches the
        # same on both sides (Go matches the whole string); a backslash before a space
        # (what a continuation leaves behind) is dropped. K-112.
        combined="$(printf '%s\n%s\n%s\n%s' "$new_risk" "$content_risk" "$command_scan" "$description_scan" | tr '\n' ' ' | sed 's/\\ /  /g')"
        # Built-in default patterns (POSIX ERE for grep -E)
        _ss_bt='`'
        default_patterns=(
            'drop[[:space:]]+table'
            'force.*push'
            'rm[[:space:]]+-rf'
            'chmod[[:space:]]+777'
            '(password|secret|api_key|token)[[:space:]]*=[[:space:]]*[^$({][^[:space:]]{8,}'
            # K-112: Bash-command shapes the patterns above miss. Go twin: defaultRiskPatterns.
            'git[[:space:]]+push[[:space:]]([^;&|]*[[:space:]])?(--force[a-z-]*|-f)([[:space:]]|$)'
            'git[[:space:]]+push[[:space:]]([^;&|]*[[:space:]])?[+][^[:space:]]'
            '(curl|wget)[^|]*[|][[:space:]]*(sudo[[:space:]]+)?((ba|z|da)?sh|python[0-9.]*|perl|ruby|node|php)([[:space:]]|$)'
            '(sh|source)[[:space:]]+<[(][^)]*(curl|wget)'
            'base64[^|]*[|][[:space:]]*(sudo[[:space:]]+)?(ba|z|da)?sh([[:space:]]|$)'
            '>[|>]?[[:space:]]*[^[:space:]]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)'
            'tee[[:space:]]+([^;&|]*[[:space:]])?[^[:space:]]*(\.env|\.ssh/|\.pem|credentials|\.claude/settings|hook-bypass|/etc/)'
            'rm[[:space:]]+-[a-z]*(fr|rf)'
            'rm[[:space:]]+-[a-z]*r[a-z]*[[:space:]]+-[a-z]*f'
            'rm[[:space:]]+-[a-z]*f[a-z]*[[:space:]]+-[a-z]*r'
            'chmod[[:space:]]+-[a-z]+[[:space:]]+777'
            # K-110: long-flag rm, sh -c "$(curl ...)", cp of .env. sudo/env
            # prefixes need no stripping: every pattern is an unanchored search.
            'rm[[:space:]]+([^;&|]*[[:space:]])?(-[a-z]*r[a-z]*|--recursive)[[:space:]]([^;&|]*[[:space:]])?(-[a-z]*f[a-z]*|--force)([[:space:]]|$)'
            'rm[[:space:]]+([^;&|]*[[:space:]])?(-[a-z]*f[a-z]*|--force)[[:space:]]([^;&|]*[[:space:]])?(-[a-z]*r[a-z]*|--recursive)([[:space:]]|$)'
            "(ba|z|da)?sh[[:space:]]+-[a-z]*c[[:space:]]+[^[:space:]]?([\$][(]|${_ss_bt})[[:space:]]*([^[:space:])]*/)?(curl|wget)"
            'cp[[:space:]]+([^;&|]*[[:space:]])?[^[:space:]]*\.env(\.[^[:space:]]*)?[^[:alnum:][:space:]._/-]?([[:space:]]|$)'
            "eval[[:space:]]+[^[:space:]]?([\$][(]|${_ss_bt})[[:space:]]*([^[:space:])]*/)?(curl|wget)"
            "find[[:space:]]+(([^;&|'\"]|\"[^\"]*\"|'[^']*')*[[:space:]])?-delete([[:space:];&|]|\$)"
        )
        for pat in "${default_patterns[@]}"; do
            if printf '%s' "$combined" | grep -qiE "$pat" 2>/dev/null; then
                escalate_reason="risk-regex:$pat"
                break
            fi
        done

        # Additional patterns from .yakos.yml supervisor.pre_filter.risk_regex
        if [ -z "$escalate_reason" ] && [ -f "$yakos_yml" ]; then
            # Extract YAML list items under pre_filter.risk_regex
            extra_patterns="$(grep -A 50 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
                | grep -A 30 '^[[:space:]]*pre_filter:' \
                | grep -A 20 '^[[:space:]]*risk_regex:' \
                | grep '^[[:space:]]*-[[:space:]]*' \
                | sed 's/^[[:space:]]*-[[:space:]]*//' | tr -d '"' || true)"
            if [ -n "$extra_patterns" ]; then
                while IFS= read -r xpat; do
                    [ -n "$xpat" ] || continue
                    if printf '%s' "$combined" | grep -qiE "$xpat" 2>/dev/null; then
                        escalate_reason="risk-regex:$xpat"
                        break
                    fi
                done <<< "$extra_patterns"
            fi
        fi
    fi

    # If no trigger fired, buffer-only return — no dispatch, no counter tick.
    if [ -z "$escalate_reason" ]; then
        ho_log "supervisor-stream" "REPORT" "pass" \
            "pre-filter: no trigger; buffered without dispatch" \
            "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
                '{pre_filter: "pass", tool: $tool, file: $file}')"
        _ss_shadow pass "" || true
        exit 0
    fi

    # A trigger fired — log it and fall through to the score-dispatch path.
    ho_log "supervisor-stream" "REPORT" "pass" \
        "pre-filter: ESCALATE ($escalate_reason); counting toward score threshold" \
        "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
            --arg reason "$escalate_reason" \
            '{pre_filter: "escalate", trigger: $reason, tool: $tool, file: $file}')"
    _ss_shadow escalate "$escalate_reason" || true
fi

# --- 3. Increment escalation counter ----------------------------------------
# Only escalations (pre-filter triggers OR pre-filter disabled) reach here.

# K-110: read-increment-write under an atomic mkdir lock. Without it two
# concurrent hooks both read N, both write N+1, and both cross the same
# score-every threshold (double supervisor launch). The increment is the
# whole decision: each hook gets a unique value, so at most one sees a
# multiple of score_every. Go twin: incrementCounter (same lock dir).
_ss_lock="$counter.lock"
_ss_locked=0
_ss_try=0
_ss_deadline=$((SECONDS + 3))
while [ "$_ss_try" -lt 150 ] && [ "$SECONDS" -lt "$_ss_deadline" ]; do
    if mkdir "$_ss_lock" 2>/dev/null; then
        _ss_locked=1
        break
    fi
    # Every retry counts against the budget, reaping included, so a stale lock
    # that cannot be removed can never spin this loop forever.
    _ss_try=$((_ss_try + 1))
    # A holder that crashed leaves the lock behind: reap one older than 1 min.
    # Rename first (atomic, one winner), then re-check the age of what we moved,
    # so a waiter cannot delete a lock another hook just created.
    if [ -n "$(find "$_ss_lock" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
        _ss_reap="$_ss_lock.reap.$$"
        if mv "$_ss_lock" "$_ss_reap" 2>/dev/null; then
            if [ -n "$(find "$_ss_reap" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
                rm -rf "$_ss_reap" 2>/dev/null || true
            else
                mv "$_ss_reap" "$_ss_lock" 2>/dev/null || rm -rf "$_ss_reap" 2>/dev/null || true
            fi
        fi
    fi
    sleep 0.02 2>/dev/null || sleep 1
done
# Never block or double-count: no lock within ~3 s means skip this tick.
if [ "$_ss_locked" != "1" ]; then
    ho_log "supervisor-stream" "WARN" "pass" \
        "counter lock busy or unremovable; skipping this escalation tick" \
        "$(jq -nc --arg p "$_ss_lock" '{lock: $p}')"
    exit 0
fi
# Release the lock on every exit path (kill, error) after this point.
trap 'rmdir "$_ss_lock" 2>/dev/null || true' EXIT
cur=0
[ -f "$counter" ] && cur="$(cat "$counter" 2>/dev/null || echo 0)"
case "$cur" in ''|*[!0-9]*) cur=0 ;; esac
cur=$((10#$cur + 1))
if ! printf '%d\n' "$cur" > "$counter" 2>/dev/null; then
    exit 0
fi
rmdir "$_ss_lock" 2>/dev/null || true
trap - EXIT

# --- 4. Every N escalations, fork supervisor dispatch ----------------------

# Read score_every_n_calls from .yakos.yml; default 10
score_every=10
if [ -f "$yakos_yml" ]; then
    n="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*score_every_n_calls:[[:space:]]*[0-9]+' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    case "$n" in
        ''|*[!0-9]*) : ;;
        *) score_every="$n" ;;
    esac
fi

if [ "$((cur % score_every))" -ne 0 ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "escalation buffered; not yet at score-every threshold" \
        "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
            '{counter: $cur, score_every: $every, will_score: false}')"
    exit 0
fi

# We're at the threshold — fork the supervisor.
ho_log "supervisor-stream" "REPORT" "pass" \
    "escalation score threshold hit; forking supervisor dispatch (async)" \
    "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
        '{counter: $cur, score_every: $every, will_score: true}')"

# Read supervisor runtime + agent from .yakos.yml (defaults: claude, supervisor)
# TODO: supervisor.runtime: local-llm → Ollama integration (future PR)
sup_runtime="claude"
sup_agent="supervisor"
if [ -f "$yakos_yml" ]; then
    r="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*runtime:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$r" ] && sup_runtime="$r"
    a="$(grep -A 20 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E '^[[:space:]]*agent:[[:space:]]*' \
        | head -1 | awk -F: '{print $2}' | tr -d '[:space:]')"
    [ -n "$a" ] && sup_agent="$a"
fi

# Find the yakos CLI
yakos_cli=""
if [ -n "${YAKOS_CLI:-}" ]; then
    yakos_cli="$YAKOS_CLI"
elif [ -n "${YAKOS_ROOT:-}" ] && [ -f "$YAKOS_ROOT/cli/yakos" ]; then
    yakos_cli="$YAKOS_ROOT/cli/yakos"
elif command -v yakos >/dev/null 2>&1; then
    yakos_cli="$(command -v yakos)"
else
    # No CLI — log + abort the fork
    ho_log "supervisor-stream" "WARN" "pass" \
        "could not locate yakos CLI to fork supervisor" "{}"
    exit 0
fi

# Build the supervisor task. The agent itself knows the rubric (from
# its persona); we just point it at the buffer + tell it where to
# write findings.
task="Read $buffer (the last 50 tool calls; focus on the most recent $score_every).
Apply the rubric in your persona. Write your finding as a single
JSON line appended to $findings.

Stated intent of the active session: $(head -c 1500 \
    "$current_dir/decisions.md" 2>/dev/null \
    || echo '(decisions.md not found; use the most recent user prompt as intent)')"

# Fork in background; daemonize via nohup + redirect. The supervisor's
# dispatch may take 5-30 seconds; we MUST NOT block this PostToolUse hook.
# Pattern mirrors retro-dispatch.sh (PR #20): nohup + & + disown.
# Model flag (--model haiku by default) keeps routine scoring on the cheap
# tier; override via supervisor.model in .yakos.yml (haiku|sonnet|opus).
nohup "$yakos_cli" dispatch "$sup_agent" "$task" \
    --runtime "$sup_runtime" \
    --model "$sup_model" \
    >> "$current_dir/.supervisor-stdout.log" 2>>"$current_dir/.supervisor-stderr.log" &
disown 2>/dev/null || true

ho_log "supervisor-stream" "REPORT" "pass" \
    "supervisor dispatch forked async (model=$sup_model runtime=$sup_runtime)" \
    "$(jq -nc --arg model "$sup_model" --arg runtime "$sup_runtime" \
        '{dispatch: "async", model: $model, runtime: $runtime}')"

exit 0
