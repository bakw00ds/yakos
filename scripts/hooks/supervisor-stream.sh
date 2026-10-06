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
# Aliases resolve like `yakos dispatch` (_resolve_model_alias); anything that is
# still not a tier would make dispatch die (exit 1), so it falls back to haiku
# and is logged at launch (K-117). Go twin: resolveModel.
sup_model_bad=""
case "$sup_model" in
    cheap) sup_model=haiku ;;
    balanced) sup_model=sonnet ;;
    best|reasoning) sup_model=opus ;;
    frontier) sup_model=fable ;;
esac
case "$sup_model" in
    haiku|sonnet|opus|fable) : ;;
    *) sup_model_bad="$sup_model"; sup_model=haiku ;;
esac

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
# Provider resolution matches `yakos decide` (decision.ResolveProvider):
# YAKOS_DECISION_DISABLE=1, then $YAKOS_DECISION_PROVIDER, then the top-level
# `provider:` in the USER-level ~/.yakos-state/decision-policy.yml. A project
# .yakos.yml can never enable a provider; an explicit `decisions.provider: none`
# there (a direct child of the top-level decisions: block) vetoes the policy
# switch. The env var still wins over that veto.
# The policy file can enable egress, so it is trusted only when it is a regular
# file (not a symlink), ours, and not group/world writable. GNU stat first:
# on Linux `stat -f` is the filesystem form and "succeeds" with garbage; BSD
# `stat -c` fails, so the fallback runs. Go twin: decision.LoadPolicy.
_ss_policy_trusted() {
    local f="$1" mode uid g o
    [ -L "$f" ] && return 1
    mode="$(stat -c '%a' "$f" 2>/dev/null || stat -f '%Lp' "$f" 2>/dev/null || true)"
    uid="$(stat -c '%u' "$f" 2>/dev/null || stat -f '%u' "$f" 2>/dev/null || true)"
    case "$mode" in ''|*[!0-7]*) return 1 ;; esac
    case "$uid" in ''|*[!0-9]*) return 1 ;; esac
    [ "$uid" = "$(id -u)" ] || return 1
    mode="000$mode"; mode="${mode: -3}"
    g="${mode:1:1}"; o="${mode:2:1}"
    [ $(( (g & 2) | (o & 2) )) -eq 0 ] || return 1
    return 0
}

_ss_provider() {
    [ "${YAKOS_DECISION_DISABLE:-0}" = "1" ] && return 0
    local p="${YAKOS_DECISION_PROVIDER:-}" pol line content="" proj=""
    if [ -z "$p" ]; then
        pol="${YAKOS_DISPATCH_LOG:-${HOME:-}/.yakos-state}/decision-policy.yml"
        if [ -f "$pol" ] && _ss_policy_trusted "$pol"; then
            while IFS= read -r line || [ -n "$line" ]; do
                case "$line" in
                    provider:*)
                        p="${line#provider:}"
                        p="${p%%#*}"
                        p="$(printf '%s' "$p" | tr -d " \t\r\"'")"
                        break
                        ;;
                esac
            done < "$pol"
        fi
        if [ -n "$p" ] && [ -f "$yakos_yml" ]; then
            # $(<file) is a builtin read (no exec).
            content="$(<"$yakos_yml")" 2>/dev/null || content=""
            case "$content" in
                *decisions:*)
                    proj="$(awk '
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
            [ "$proj" = "none" ] && p=""
        fi
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
    # The raw preview and intent go to jq through its ENVIRONMENT (owner-only),
    # never argv (world-readable in /proc/*/cmdline on Linux); only the tool
    # name, the path and the plan flag ride on argv, as before this hook
    # shipped state.
    state="$(_SS_PREV="$preview" _SS_INTENT="$intent" jq -nc --arg tool "$tool" --arg fp "$file_path" --arg pm "$pm" \
        '($ENV._SS_PREV // "") as $prev | ($ENV._SS_INTENT // "") as $intent
         | {tool: $tool}
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

# Pre-filter probes (also used to classify a trigger as high-risk, K-117).
_ss_sens_reason() {
    local escalate_reason=""
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
    printf '%s' "$escalate_reason"
    return 0
}
_ss_risk_reason() {
    local escalate_reason=""
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
            # K-117: the bare 'force.*push' matched prose ("enforce push notification") and
            # could burn the high-risk ceiling; the git-command shapes below cover real pushes.
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
    printf '%s' "$escalate_reason"
    return 0
}

# --- K-117: supervisor launch gate (coalesce, cap, interval, deadline) ------
# The supervisor run takes 1-4 minutes, so triggers routinely arrive while one
# is in flight. Per session key, under the counter's lock, the gate:
#   - IN FLIGHT  records the trigger (redacted preview appended to a per-session
#                pending file) and launches nothing; the running wrapper starts
#                exactly ONE follow-up when it ends and hands it that file;
#   - CAP        only ROUTINE launches count. High-risk triggers (a sensitive
#                path or a risk-regex hit) bypass the cap and the interval, up
#                to a ceiling of 3x the cap, so benign escalations can never
#                exhaust supervision; hitting either limit logs a WARN (and one
#                stderr line) once per session;
#   - INTERVAL   a routine trigger inside min_launch_interval_s starts a
#                DEFERRED wrapper that sleeps the rest of the interval, so no
#                trigger is stranded;
#   - BACKOFF    after an account session-limit failure launches pause.
# Everything is fail-open: this hook still exits 0. Limits come from the
# trusted user-level policy (~/.yakos-state/supervisor-policy.yml) or the
# built-in defaults; a project .yakos.yml may only make them STRICTER.
# State (key=value): start launches hlaunches last pending high caplog ceillog
# (the count ceiling's report flag) backoff budgetlog (the dollar ceiling's own flag)
# backoff. Go twin: supervisorstream.go (launchGate, allowLaunch) and wrap.go.
_ss_cfg_raw() { # <key>: raw text after "key:" of the first match under supervisor:
    [ -f "$yakos_yml" ] || return 0
    grep -A 30 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -E "^[[:space:]]*$1:" | head -1 | sed -E "s/^[[:space:]]*$1:[[:space:]]*//" || true
}
# _ss_int <raw>: decimal digits only (no sign, hex, quotes); a YAML inline
# comment is stripped; more than 9 digits clamps. Prints nothing (rc 1) otherwise.
_ss_int() {
    local v="$1"
    v="$(printf '%s' "$v" | sed -E 's/[[:space:]]+#.*$//' | tr -d '[:space:]')"
    case "$v" in ''|*[!0-9]*) return 1 ;; esac
    [ "${#v}" -le 9 ] || v=999999999
    printf '%s' "$((10#$v))"
}
_ss_clamp() { # <value> <min> <max>
    local v="$1"
    [ "$v" -ge "$2" ] || v="$2"
    [ "$v" -le "$3" ] || v="$3"
    printf '%s' "$v"
}
_ss_pol="${YAKOS_DISPATCH_LOG:-${HOME:-}/.yakos-state}/supervisor-policy.yml"
_ss_pol_ok=0
_ss_ignored=""
_ss_invalid=""
# Per-key limits (one table, mirrored in limits.go limitTable). "Stricter"
# means MORE supervision; a project .yakos.yml may only move a limit that way,
# the user-level policy may move it either way:
#
#   key                        default            range       project may
#   max_launches_per_session   30                 0..10000    RAISE it, or 0 (unlimited)
#   min_launch_interval_s      120                0..86400    LOWER it
#   run_deadline_s             240/480/600 by     floor..3600 LENGTHEN it (a shorter
#                              tier (haiku/                   deadline kills runs); values
#                              sonnet/opus)                   below the floor (30 s, env
#                                                             YAKOS_SUPERVISOR_MIN_DEADLINE_S)
#                                                             are invalid -> tier default
#   session_limit_backoff_min  30                 0..1440     LOWER it
#
# The high-risk ceiling is 3x the TRUSTED cap (policy or default), so a project
# can never shrink it.
# _ss_limit <key> <default> <min> <max> <up|down|up0> [floor]: sets _ss_lim.
_ss_limit() {
    local key="$1" base="$2" lo="$3" hi="$4" mode="$5" floor="${6:-0}" v pv better=0
    if [ "$_ss_pol_ok" = 1 ]; then
        if v="$(_ss_int "$(sed -n "s/^$key:[[:space:]]*//p" "$_ss_pol" 2>/dev/null | head -1)")"; then
            if [ "$v" -ge "$floor" ]; then
                base="$(_ss_clamp "$v" "$lo" "$hi")"
            else
                _ss_invalid="${_ss_invalid:+$_ss_invalid,}$key"
            fi
        fi
    fi
    _ss_base="$base"
    _ss_lim="$base"
    if pv="$(_ss_int "$(_ss_cfg_raw "$key")")"; then
        if [ "$pv" -lt "$floor" ]; then
            _ss_invalid="${_ss_invalid:+$_ss_invalid,}$key"
            return 0
        fi
        pv="$(_ss_clamp "$pv" "$lo" "$hi")"
        case "$mode" in
            up) [ "$pv" -gt "$base" ] && better=1 ;;
            down) [ "$pv" -lt "$base" ] && better=1 ;;
            up0) if [ "$base" -gt 0 ] && { [ "$pv" -eq 0 ] || [ "$pv" -gt "$base" ]; }; then better=1; fi ;;
        esac
        if [ "$better" = 1 ]; then
            _ss_lim="$pv"
        elif [ "$pv" != "$base" ]; then
            _ss_ignored="${_ss_ignored:+$_ss_ignored,}$key"
        fi
    fi
    return 0
}
_ss_limits() {
    local def_deadline=240 floor="${YAKOS_SUPERVISOR_MIN_DEADLINE_S:-30}"
    case "$floor" in ''|*[!0-9]*) floor=30 ;; esac
    case "$sup_model" in sonnet) def_deadline=480 ;; opus|fable) def_deadline=600 ;; esac
    if [ -f "$_ss_pol" ] && _ss_policy_trusted "$_ss_pol"; then _ss_pol_ok=1; fi
    _ss_ignored=""; _ss_invalid=""
    _ss_limit max_launches_per_session 30 0 10000 up0; sup_cap="$_ss_lim"
    sup_ceil=$((_ss_base * 3))
    _ss_limit min_launch_interval_s 120 0 86400 down; sup_interval="$_ss_lim"
    _ss_limit run_deadline_s "$def_deadline" "$floor" 3600 up "$floor"; sup_deadline="$_ss_lim"
    _ss_limit session_limit_backoff_min 30 0 1440 down; sup_backoff="$_ss_lim"
    return 0
}

# BEGIN supervisor-wrap
IFS= read -r -d '' _ss_wrap <<'SSWRAP_EOF' || true
# supervisor-wrap: run the dispatch under a wall-clock deadline, then drain
# coalesced triggers with at most one follow-up run per completion (K-117).
# argv: <cli> dispatch <agent> <task> [flags...]
# env: _SSW_STATE _SSW_LOCK _SSW_LOG _SSW_PENDING _SSW_FINDINGS _SSW_DEADLINE _SSW_CAP _SSW_CEIL
#      _SSW_INTERVAL _SSW_BACKOFF (minutes) _SSW_DELAY (seconds, deferred start)
set +e
set -m 2>/dev/null
_ssw_locked=0
_ssw_now() { date +%s; }
_ssw_num() { case "$1" in ''|*[!0-9]*) printf '%s' "$2" ;; *) printf '%s' "$((10#$1))" ;; esac; }
_ssw_log() {
    printf '{"ts":"%s","hook":"supervisor-stream","severity":"%s","decision":"pass","reason":"%s"%s}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "${3:+,$3}" >> "$_SSW_LOG" 2>/dev/null
    return 0
}
# K-128: the same lock protocol as the hook (_ss_lock_take): an exclusive create
# done by the shell (noclobber, no fork), a builtin [ -e ] probe while it is held,
# exponential backoff with jitter (5..160 ms), age check on the fifth miss and
# every 8th after. Each wait step is one sleep fork.
# The file is created owner-only (0600) whatever the caller's umask, the way the hook does it
# (K-128, S5): the mask is read once, by the first create, then set and put back with builtins.
_ssw_um=""
_ssw_try_lock() {
    local rc=0
    if [ -z "$_ssw_um" ]; then _ssw_um="$(umask 2>/dev/null)" || _ssw_um=""; fi
    if [ -n "$_ssw_um" ]; then umask 077; fi
    set -C
    { true > "$_SSW_LOCK"; } 2>/dev/null || rc=1
    set +C
    if [ -n "$_ssw_um" ]; then umask "$_ssw_um"; fi
    return "$rc"
}
_ssw_nap() {
    local d
    printf -v d '%d.%03d' "$(($1 / 1000))" "$(($1 % 1000))"
    sleep "$d" 2>/dev/null || sleep 1
    return 0
}
_ssw_lock_step() {
    local reap
    if [ $((n % 8)) -eq 4 ] && [ -n "$(find "$_SSW_LOCK" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
        reap="$_SSW_LOCK.reap.$$"
        if mv "$_SSW_LOCK" "$reap" 2>/dev/null; then
            if [ -n "$(find "$reap" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
                rm -rf "$reap" 2>/dev/null
            else
                mv "$reap" "$_SSW_LOCK" 2>/dev/null || rm -rf "$reap" 2>/dev/null
            fi
        fi
    fi
    n=$((n + 1))
    _ssw_nap $((bo / 2 + RANDOM % (bo + 1)))
    bo=$((bo * 2))
    [ "$bo" -le 160 ] || bo=160
    return 0
}
_ssw_lock_once() {
    local n=0 bo=5 dl=$((SECONDS + 3))
    while [ "$n" -lt 500 ] && [ "$SECONDS" -lt "$dl" ]; do
        if [ -e "$_SSW_LOCK" ]; then _ssw_lock_step; continue; fi
        if _ssw_try_lock; then _ssw_locked=1; return 0; fi
        _ssw_lock_step
    done
    return 1
}
# Three 3 s budgets: this process is detached, so waiting costs no latency.
# State is never written without the lock.
_ssw_lock() {
    local i=0
    _ssw_locked=0
    while [ "$i" -lt 3 ]; do
        _ssw_lock_once && return 0
        i=$((i + 1))
    done
    return 1
}
_ssw_unlock() { if [ "$_ssw_locked" = 1 ]; then rm -f "$_SSW_LOCK" 2>/dev/null; fi; _ssw_locked=0; return 0; }
_ssw_load() {
    st_start=""; st_launches=0; st_hlaunches=0; st_last=0; st_pending=0; st_high=0
    st_caplog=0; st_ceillog=0; st_backoff=0; st_budgetlog=0
    [ -f "$_SSW_STATE" ] || return 0
    local k v
    while IFS='=' read -r k v || [ -n "$k" ]; do
        case "$k" in
            start) st_start="$(_ssw_num "$v" "")" ;;
            launches) st_launches="$(_ssw_num "$v" 0)" ;;
            hlaunches) st_hlaunches="$(_ssw_num "$v" 0)" ;;
            last) st_last="$(_ssw_num "$v" 0)" ;;
            pending) st_pending="$(_ssw_num "$v" 0)" ;;
            high) st_high="$(_ssw_num "$v" 0)" ;;
            caplog) st_caplog="$(_ssw_num "$v" 0)" ;;
            ceillog) st_ceillog="$(_ssw_num "$v" 0)" ;;
            backoff) st_backoff="$(_ssw_num "$v" 0)" ;;
            budgetlog) st_budgetlog="$(_ssw_num "$v" 0)" ;;
        esac
    done < "$_SSW_STATE"
    return 0
}
_ssw_save() {
    local tmp="$_SSW_STATE.tmp.$$"
    if printf 'start=%s\nlaunches=%s\nhlaunches=%s\nlast=%s\npending=%s\nhigh=%s\ncaplog=%s\nceillog=%s\nbackoff=%s\nbudgetlog=%s\n' \
        "$st_start" "$st_launches" "$st_hlaunches" "$st_last" "$st_pending" "$st_high" \
        "$st_caplog" "$st_ceillog" "$st_backoff" "$st_budgetlog" > "$tmp" 2>/dev/null \
        && mv "$tmp" "$_SSW_STATE" 2>/dev/null; then
        return 0
    fi
    rm -f "$tmp" 2>/dev/null
    return 1
}
# K-128: trigger records that hooks left because they could not take the lock
# (<state>.add.*, same format as the hook's _ss_fold_gate): fold them into the
# state the wrapper has just loaded, so a trigger that arrived while this run was
# in flight still gets its follow-up. Called under the lock; the records are removed
# once the state is saved (_ssw_fold_done). At most 32 per fold.
_ssw_gfold=()
_ssw_gfold_n=0
_ssw_fold_gate() {
    local f h ev
    _ssw_gfold=(); _ssw_gfold_n=0
    for f in "$_SSW_STATE".add.*; do
        if [ -f "$f" ] && [ ! -L "$f" ]; then
            h=""; ev=""
            { { read -r -n 8 h; read -r -n 16384 ev; } < "$f"; } 2>/dev/null || true
            case "$h" in high=0|high=1) ;; *) continue ;; esac
            st_pending=$((st_pending + 1))
            if [ "$h" = high=1 ]; then st_high=$((st_high + 1)); fi
            if [ -n "$ev" ]; then
                if [ ! -f "$_SSW_PENDING" ]; then ( umask 077; true > "$_SSW_PENDING" ) 2>/dev/null || true; fi
                { printf '%s\n' "$ev" >> "$_SSW_PENDING"; } 2>/dev/null || true
            fi
            _ssw_gfold[_ssw_gfold_n]="$f"; _ssw_gfold_n=$((_ssw_gfold_n + 1))
            [ "$_ssw_gfold_n" -lt 32 ] || break
        fi
    done
    return 0
}
_ssw_fold_done() {
    if [ "$_ssw_gfold_n" -gt 0 ]; then rm -f -- "${_ssw_gfold[@]}" 2>/dev/null; fi
    _ssw_gfold_n=0; _ssw_gfold=()
    return 0
}
_ssw_hit=0
_ssw_slimit=0
_ssw_run() {
    local pid wd rc=0 flag="$_SSW_STATE.deadline.$$" out="$_SSW_STATE.out.$$"
    _ssw_hit=0; _ssw_slimit=0
    rm -f "$flag" 2>/dev/null
    "$@" > "$out" &
    pid=$!
    (
        sp=""
        trap 'kill "$sp" 2>/dev/null; exit 0' TERM
        sleep "$_SSW_DEADLINE" & sp=$!
        wait "$sp"
        : > "$flag"
        kill -TERM -- "-$pid" 2>/dev/null; kill -TERM "$pid" 2>/dev/null
        sleep 2 & sp=$!
        wait "$sp"
        kill -KILL -- "-$pid" 2>/dev/null; kill -KILL "$pid" 2>/dev/null
    ) >/dev/null 2>&1 &
    wd=$!
    { wait "$pid"; } 2>/dev/null || rc=$?
    kill -TERM "$wd" 2>/dev/null
    { wait "$wd"; } 2>/dev/null
    if [ -e "$flag" ]; then rm -f "$flag" 2>/dev/null; _ssw_hit=1; fi
    cat "$out" 2>/dev/null
    # An account session limit ends the run with a short message and exit 1:
    # launching more runs into it only burns time.
    if [ "$rc" != 0 ] && head -c 4096 "$out" 2>/dev/null | grep -qi 'session limit'; then _ssw_slimit=1; fi
    rm -f "$out" 2>/dev/null
    return "$rc"
}
if [ "$(_ssw_num "${_SSW_DELAY:-0}" 0)" -gt 0 ]; then sleep "$(_ssw_num "$_SSW_DELAY" 0)"; fi
_ssw_cli="$1"; _ssw_sub="$2"; _ssw_agent="$3"; _ssw_task="$4"
shift 4
while :; do
    n=0
    run_task="$_ssw_task"
    if _ssw_lock; then
        _ssw_load
        _ssw_fold_gate
        n="$st_pending"
        if [ "$n" -gt 0 ]; then
            if [ -f "$_SSW_PENDING" ]; then mv "$_SSW_PENDING" "$_SSW_PENDING.run" 2>/dev/null; fi
            st_pending=0; st_high=0
            if _ssw_save; then _ssw_fold_done; fi
        fi
        _ssw_unlock
    else
        _ssw_log WARN "state lock busy; running without claiming coalesced events" ""
    fi
    if [ "$n" -gt 0 ]; then
        run_task="$_ssw_task"$'\n\n'"Coalesced events: $n more escalated tool calls arrived while the previous supervisor run was in flight or throttled. Their redacted previews (one JSON object per line) are in $_SSW_PENDING.run. Read that file in addition to the buffer and judge them too."
    fi
    t0="$(_ssw_now)"
    rc=0
    _ssw_run "$_ssw_cli" "$_ssw_sub" "$_ssw_agent" "$run_task" "$@" || rc=$?
    dur=$(( $(_ssw_now) - t0 ))
    if [ "$_ssw_hit" = 1 ]; then
        _ssw_log WARN "supervisor run exceeded its wall-clock deadline and was killed" "\"rc\":$rc,\"deadline_s\":$_SSW_DEADLINE,\"duration_s\":$dur"
    elif [ "$rc" != 0 ]; then
        _ssw_log WARN "supervisor run exited non-zero" "\"rc\":$rc,\"duration_s\":$dur,\"session_limit\":$_ssw_slimit"
    else
        _ssw_log REPORT "supervisor run finished" "\"rc\":0,\"duration_s\":$dur"
    fi
    _ssw_load
    if [ "$_ssw_slimit" = 0 ] && [ "$st_pending" -gt 0 ] && [ "$st_high" -eq 0 ]; then
        rem=$(( st_last + _SSW_INTERVAL - $(_ssw_now) ))
        if [ "$rem" -gt 0 ]; then sleep "$rem" 2>/dev/null; fi
    fi
    if ! _ssw_lock; then
        _ssw_log WARN "state lock busy; leaving the in-flight marker to expire" ""
        break
    fi
    _ssw_load
    _ssw_fold_gate
    now="$(_ssw_now)"
    if [ "$_ssw_slimit" = 1 ] && [ "$st_backoff" -le "$now" ]; then
        st_backoff=$((now + _SSW_BACKOFF * 60))
        _ssw_log WARN "account session limit reached; skipping supervisor launches for a while" "\"backoff_min\":$_SSW_BACKOFF"
    fi
    follow=""
    if [ "$_ssw_slimit" = 0 ] && [ "$st_pending" -gt 0 ] && [ "$st_backoff" -le "$now" ]; then
        if [ "$st_high" -gt 0 ]; then
            if [ "$_SSW_CEIL" -eq 0 ] || [ "$st_hlaunches" -lt "$_SSW_CEIL" ]; then
                follow=high
            elif [ "$st_ceillog" != 1 ]; then
                st_ceillog=1
                _ssw_log WARN "high-risk supervisor launch ceiling reached for this session" "\"ceiling\":$_SSW_CEIL"
                echo "supervisor-stream: high-risk launch ceiling reached for this session; skipping further supervisor runs" >&2
                ( umask 077; printf '{"ts":"%s","batch_size":0,"scores":{},"overall":"CRITICAL","synthetic":true,"rationale":"High-risk supervisor launch ceiling (%s) reached for this session: further high-risk events are recorded but no longer supervised. Review the session and the pending events file.","recommended_action":"surface_to_operator"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$_SSW_CEIL" >> "$_SSW_FINDINGS" ) 2>/dev/null
            fi
        elif [ "$_SSW_CAP" -eq 0 ] || [ "$st_launches" -lt "$_SSW_CAP" ]; then
            follow=routine
        elif [ "$st_caplog" != 1 ]; then
            st_caplog=1
            _ssw_log WARN "supervisor launch cap reached for this session; skipping further launches" "\"cap\":$_SSW_CAP"
            echo "supervisor-stream: launch cap reached for this session; skipping further routine supervisor runs" >&2
        fi
    fi
    if [ -n "$follow" ]; then
        _ssw_log REPORT "coalesced triggers: launching one follow-up supervisor run" "\"coalesced\":$st_pending,\"kind\":\"$follow\""
        if [ "$follow" = high ]; then st_hlaunches=$((st_hlaunches + 1)); else st_launches=$((st_launches + 1)); fi
        st_last="$now"; st_start="$now"
        if _ssw_save; then _ssw_fold_done; fi
        _ssw_unlock
        continue
    fi
    st_start=""
    if _ssw_save; then _ssw_fold_done; fi
    _ssw_unlock
    break
done
exit 0
SSWRAP_EOF
# END supervisor-wrap

# Session key: the session id with everything outside [A-Za-z0-9_-] replaced by
# "_", capped at 64 bytes; "nosession" when absent. Go twin: sessionKey.
# Computed lazily by _ss_gate: the hot path (every mutation call) pays nothing.
_ss_key=""; _ss_state=""; _ss_pending=""
_ss_lock="$counter.lock"
_ss_locked=0
_ss_lock_wait=3     # seconds: the hard ceiling on how long a hook waits for the lock (K-128)
_ss_budget_wait=2   # seconds of wall clock the budget CLI gets (K-119, K-128)
_ss_wlog=""         # the wrapper's log path, resolved before the lock is taken
# Test seam (K-128): with YAKOS_TEST_SEAMS=1 and YAKOS_TEST_LOCK_STATS=1, every lock
# take appends one record (label, wait, tries, hold; microseconds) to
# work/current/.supervisor-lock-stats: a fixed file in the directory this hook
# already writes, never a path taken from the environment. It reads the process
# environment only (a project .yakos.yml cannot set it) and is a no-op in
# production. The clock is $EPOCHREALTIME (bash 5); an older shell forks perl per
# probe, which inflates the holds it reports. Go twin: lockStat.
_ss_stats=""
if [ "${YAKOS_TEST_SEAMS:-}" = 1 ] && [ "${YAKOS_TEST_LOCK_STATS:-}" = 1 ] && [ ! -L "$current_dir/.supervisor-lock-stats" ]; then _ss_stats="$current_dir/.supervisor-lock-stats"; fi
_ss_us=0; _ss_t0=0; _ss_t1=0; _ss_tries=0; _ss_label=lock
_ss_now_us() { # sets _ss_us
    if [ -n "${EPOCHREALTIME:-}" ]; then _ss_us="${EPOCHREALTIME//[.,]/}"
    else _ss_us="$(perl -MTime::HiRes=time -e 'printf "%d", time()*1e6' 2>/dev/null || echo 0)"; fi
    return 0
}

_ss_load_state() {
    st_start=""; st_launches=0; st_hlaunches=0; st_last=0; st_pending=0; st_high=0
    st_caplog=0; st_ceillog=0; st_backoff=0; st_budgetlog=0
    [ -f "$_ss_state" ] || return 0
    local k v
    while IFS='=' read -r k v || [ -n "$k" ]; do
        case "$v" in *[!0-9]*) v="" ;; esac
        [ "${#v}" -le 15 ] || v=""
        case "$k" in
            start) st_start="$v" ;;
            launches) st_launches="${v:-0}" ;;
            hlaunches) st_hlaunches="${v:-0}" ;;
            last) st_last="${v:-0}" ;;
            pending) st_pending="${v:-0}" ;;
            high) st_high="${v:-0}" ;;
            caplog) st_caplog="${v:-0}" ;;
            ceillog) st_ceillog="${v:-0}" ;;
            backoff) st_backoff="${v:-0}" ;;
            budgetlog) st_budgetlog="${v:-0}" ;;
        esac
    done < "$_ss_state"
    st_launches=$((10#$st_launches)); st_hlaunches=$((10#$st_hlaunches)); st_last=$((10#$st_last))
    st_pending=$((10#$st_pending)); st_high=$((10#$st_high)); st_caplog=$((10#$st_caplog))
    st_ceillog=$((10#$st_ceillog)); st_backoff=$((10#$st_backoff)); st_budgetlog=$((10#$st_budgetlog))
    [ -z "$st_start" ] || st_start=$((10#$st_start))
    return 0
}
_ss_save_state() {
    local tmp="$_ss_state.tmp.$$"
    if printf 'start=%s\nlaunches=%s\nhlaunches=%s\nlast=%s\npending=%s\nhigh=%s\ncaplog=%s\nceillog=%s\nbackoff=%s\nbudgetlog=%s\n' \
        "$st_start" "$st_launches" "$st_hlaunches" "$st_last" "$st_pending" "$st_high" \
        "$st_caplog" "$st_ceillog" "$st_backoff" "$st_budgetlog" > "$tmp" 2>/dev/null \
        && mv "$tmp" "$_ss_state" 2>/dev/null; then
        return 0
    fi
    rm -f "$tmp" 2>/dev/null || true
    return 1
}
# Per-session pending file: redacted event previews (owner-only), last 100.
# This runs under the lock, so it is builtins only: the file is created
# owner-only (umask 077 in a subshell, first record only) and then plainly
# appended to. The trim needs a count and a tail, so it only runs once more than
# 100 triggers are pending (the file has about one line per pending trigger), and
# the count stops at 151 lines. K-128. Go twin: appendPending.
_ss_pend_put() { # <line>: append one preview line
    [ -n "$1" ] || return 0
    if [ ! -f "$_ss_pending" ]; then ( umask 077; true > "$_ss_pending" ) 2>/dev/null || return 0; fi
    { printf '%s\n' "$1" >> "$_ss_pending"; } 2>/dev/null || return 0
    return 0
}
_ss_pend_append() { # append $event (the trigger being recorded) to the pending file
    _ss_pend_put "$event"
    if [ "$st_pending" -gt 100 ]; then _ss_pend_trim; fi
    return 0
}
_ss_pend_trim() { # more than 150 lines: keep the last 100
    local n=0 l tmp="$_ss_pending.tmp.$$"
    [ -f "$_ss_pending" ] || return 0
    while [ "$n" -le 150 ] && { IFS= read -r l || [ -n "$l" ]; }; do n=$((n + 1)); done < "$_ss_pending"
    [ "$n" -gt 150 ] || return 0
    ( umask 077; tail -n 100 "$_ss_pending" > "$tmp" ) 2>/dev/null && mv "$tmp" "$_ss_pending" 2>/dev/null || rm -f "$tmp" 2>/dev/null
    return 0
}

# --- K-128: the lock ---------------------------------------------------------
# One lock (<counter>.lock) serialises the escalation counter, the per-session
# run state and the detached wrapper. Holding it means the path exists: it is
# created exclusively (an O_EXCL file create here, mkdir in older hooks; both are
# atomic and exclude each other) and removed by whoever created it. It protects
# ONLY the read-modify-write of those files: everything that forks and is not
# part of that (jq, ho_log, the budget CLI, the synthetic findings) runs before
# the lock is taken or after it is dropped, so a hold is a few builtins, one
# rename and the release.
#
# Waiting: a waiter backs off exponentially (5 ms doubling to 160 ms, +-50 %
# jitter) so ten hooks neither poll in lockstep nor starve the holder of CPU, and
# while the lock plainly exists it polls with the builtin [ -e ] instead of
# forking a doomed create. Taking it costs no fork at all, so a hand-off is only
# the waiter's wake-up. Each wait step costs one fork (sleep): bash 3.2 has no
# sub-second sleep builtin. The age check (one find) runs on the fifth miss
# (~75 ms in) and every 8th after, not on every retry: a lock created a moment
# ago is essentially never stale, and a waiter's first move should not be two
# forks that compete with the holder for CPU. A lock older than 1 min is reaped: rename first
# (atomic, one winner), then re-check the age of what was moved, so a waiter
# cannot delete a lock another hook has just created. The wait ends after
# _ss_lock_wait seconds ($SECONDS ticks in whole seconds, so 2..3 s) or 500
# tries, whichever comes first; the callers then journal their tick (below)
# instead of dropping it. Go twin: acquireLock.
_ss_nap() { # <ms>
    local d
    printf -v d '%d.%03d' "$(($1 / 1000))" "$(($1 % 1000))"
    sleep "$d" 2>/dev/null || sleep 1
    return 0
}
_ss_lock_step() { # one wait step; n and bo are _ss_lock_take's locals
    local reap
    if [ $((n % 8)) -eq 4 ] && [ -n "$(find "$_ss_lock" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
        reap="$_ss_lock.reap.$$"
        if mv "$_ss_lock" "$reap" 2>/dev/null; then
            if [ -n "$(find "$reap" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
                rm -rf "$reap" 2>/dev/null || true
            else
                mv "$reap" "$_ss_lock" 2>/dev/null || rm -rf "$reap" 2>/dev/null || true
            fi
        fi
    fi
    n=$((n + 1))
    _ss_nap $((bo / 2 + RANDOM % (bo + 1)))
    bo=$((bo * 2))
    [ "$bo" -le 160 ] || bo=160
    return 0
}
# _ss_try_lock: create the lock exclusively. With noclobber the redirection is an
# O_EXCL create done by the shell itself: no fork, unlike mkdir. A directory (what
# an older hook creates with mkdir) or any other existing path makes it fail, so
# old and new hooks still exclude each other. The command is `true`, not `:`: a
# failed redirection on a POSIX SPECIAL builtin ends a non-interactive shell in
# POSIX mode (bash --posix, POSIXLY_CORRECT, run as sh), and losing the create
# race is exactly when this fails; `true` is a regular builtin, so it just returns 1.
# The file is created owner-only (0600, as the Go twin's is) whatever the caller's umask
# (K-128, S5): the mask is read once, by the first create (one fork, before the lock is
# held), and then set and put back with builtins, so taking the lock still forks nothing.
# If the mask cannot be read the create keeps the caller's, as it always did.
_ss_um=""
_ss_try_lock() {
    local rc=0
    if [ -z "$_ss_um" ]; then _ss_um="$(umask 2>/dev/null)" || _ss_um=""; fi
    if [ -n "$_ss_um" ]; then umask 077; fi
    set -C
    { true > "$_ss_lock"; } 2>/dev/null || rc=1
    set +C
    if [ -n "$_ss_um" ]; then umask "$_ss_um"; fi
    return "$rc"
}
_ss_lock_loop() { # 0 when the lock is taken, 1 when the ceiling expired
    local n=0 bo=5 dl=$((SECONDS + _ss_lock_wait))
    while [ "$n" -lt 500 ] && [ "$SECONDS" -lt "$dl" ]; do
        _ss_tries=$n
        if [ -e "$_ss_lock" ]; then _ss_lock_step; continue; fi
        if _ss_try_lock; then _ss_locked=1; return 0; fi
        _ss_lock_step
    done
    return 1
}
_ss_lock_take() { # [label for the stats seam]
    local rc=0
    if [ -n "$_ss_stats" ]; then _ss_label="${1:-lock}"; _ss_now_us; _ss_t0=$_ss_us; fi
    _ss_lock_loop || rc=1
    if [ -n "$_ss_stats" ]; then
        _ss_now_us; _ss_t1=$_ss_us
        if [ "$rc" = 1 ]; then ( umask 077; printf 'H %s %s FAIL wait_us=%s tries=%s\n' "$$" "$_ss_label" "$((_ss_t1 - _ss_t0))" "$_ss_tries" >> "$_ss_stats" ) 2>/dev/null || true; fi
    fi
    return "$rc"
}
_ss_lock_drop() {
    if [ "$_ss_locked" = 1 ]; then
        rm -f "$_ss_lock" 2>/dev/null || true
        if [ -n "$_ss_stats" ]; then
            _ss_now_us
            ( umask 077; printf 'H %s %s OK wait_us=%s tries=%s hold_us=%s got_us=%s\n' "$$" "$_ss_label" "$((_ss_t1 - _ss_t0))" "$_ss_tries" "$((_ss_us - _ss_t1))" "$_ss_t1" >> "$_ss_stats" ) 2>/dev/null || true
        fi
    fi
    _ss_locked=0
    trap - EXIT
}
# The session's run-state and pending files. Go twin: sessionKey.
_ss_paths() {
    _ss_key="$(printf '%s' "$session_id" | LC_ALL=C tr -c 'A-Za-z0-9_-' '_' | head -c 64)"
    [ -n "$_ss_key" ] || _ss_key="nosession"
    _ss_state="$current_dir/.supervisor-run.$_ss_key"
    _ss_pending="$current_dir/.supervisor-pending.$_ss_key"
    return 0
}
# --- K-128: records left by a hook that could not take the lock ---------------
# Dropping a tick under load is fail-open: a lost increment shifts every later
# threshold and a lost high-risk trigger is never recorded. A hook whose wait
# ceiling expired therefore leaves a small owner-only record that the next lock
# holder folds in (docs/supervisor-mode.md, "Lock protocol"):
#   <counter>.add.<pid>.<id>  "1": one increment owed to the counter. The holder
#                             that folds it counts it and, if that moves the
#                             counter past a score-every multiple, covers the
#                             crossing (the hook that owed it is long gone).
#   <state>.add.<pid>.<id>    "high=<0|1>", then the event preview: one trigger
#                             owed to the session's run state (pending +1, high
#                             +1 when flagged, preview appended to the pending
#                             file). Folded by the next gate holder of the
#                             session and by the session's wrapper. A launch the
#                             crossing owed is NOT promised: the next run (the next
#                             crossing) covers the recorded trigger.
# A record is created exclusively (noclobber: a planted symlink is never
# followed), read with bounded builtin reads, folded only when complete and well
# formed (a half-written one waits for the next fold), at most 256 counter or 32
# trigger records per fold (a fold reads each record under the lock; the rest wait
# for the next holder), and removed only after the file it was folded into has been
# written. At most 512 records of a kind wait at once: past that a hook drops its
# tick, as before K-128, and says so. Go twin: journalTick, journalGate,
# foldTicks, foldGate.
_ss_journal_full() { # <base>: 0 when 512 records or more already wait (the storage is bounded)
    local f n=0
    for f in "$1".add.*; do
        { [ -f "$f" ] && [ ! -L "$f" ]; } || continue
        n=$((n + 1))
        [ "$n" -lt 512 ] || return 0
    done
    return 1
}
_ss_journal_write() { # <base> <body>: a new record <base>.add.<pid>.<id>; 0 when it was written. A name taken (a recycled pid) gets a new one.
    local i=0
    if _ss_journal_full "$1"; then return 1; fi
    while [ "$i" -lt 5 ]; do
        if ( umask 077; set -C; printf '%s' "$2" > "$1.add.$$.$RANDOM$RANDOM" ) 2>/dev/null; then return 0; fi
        i=$((i + 1))
    done
    return 1
}
_ss_journal_tick() {
    _ss_journal_write "$counter" $'1\n'
}
_ss_journal_gate() {
    local body
    printf -v body 'high=%s\n%s\n' "$trigger_high" "$event"
    _ss_journal_write "$_ss_state" "$body"
}
_ss_fold_n=0; _ss_fold=()
_ss_fold_adds() { # under the lock: count the complete "+1" records
    local f v
    _ss_fold_n=0; _ss_fold=()
    for f in "$counter".add.*; do
        if [ -f "$f" ] && [ ! -L "$f" ]; then
            v=""
            { { read -r -n 8 v; } < "$f"; } 2>/dev/null || true
            if [ "$v" = 1 ]; then
                _ss_fold[_ss_fold_n]="$f"; _ss_fold_n=$((_ss_fold_n + 1))
                [ "$_ss_fold_n" -lt 256 ] || break
            fi
        fi
    done
    return 0
}
_ss_fold_done() { # after the counter has been written
    if [ "$_ss_fold_n" -gt 0 ]; then rm -f -- "${_ss_fold[@]}" 2>/dev/null || true; fi
    _ss_fold_n=0; _ss_fold=()
    return 0
}
_ss_gfold_n=0; _ss_gfold=()
_ss_fold_gate() { # under the lock, after _ss_load_state: fold this session's trigger records
    local f h ev
    _ss_gfold_n=0; _ss_gfold=()
    for f in "$_ss_state".add.*; do
        if [ -f "$f" ] && [ ! -L "$f" ]; then
            h=""; ev=""
            { { read -r -n 8 h; read -r -n 16384 ev; } < "$f"; } 2>/dev/null || true
            case "$h" in high=0|high=1) ;; *) continue ;; esac
            st_pending=$((st_pending + 1))
            if [ "$h" = high=1 ]; then st_high=$((st_high + 1)); fi
            _ss_pend_put "$ev"
            _ss_gfold[_ss_gfold_n]="$f"; _ss_gfold_n=$((_ss_gfold_n + 1))
            [ "$_ss_gfold_n" -lt 32 ] || break
        fi
    done
    if [ "$_ss_gfold_n" -gt 0 ] && [ "$st_pending" -gt 100 ]; then _ss_pend_trim; fi
    return 0
}
_ss_gfold_done() { # after the run state has been written
    if [ "$_ss_gfold_n" -gt 0 ]; then rm -f -- "${_ss_gfold[@]}" 2>/dev/null || true; fi
    _ss_gfold_n=0; _ss_gfold=()
    return 0
}
# _ss_budget: the supervisor's dollar budget (K-119), read once per launch
# decision (never per event) through `yakos budget check --json`. The Go twin
# evaluates in-process; a bash hook cannot, so it forks the CLI here. Sets
# _ss_bud_state (off|ok|warning|hard_stop), _ss_bud_hard / _ss_bud_over (0|1:
# spent >= limit / spent >= the 2x dispatch stop), and the amounts. ANY failure
# (no CLI, an old CLI without `budget`, bad JSON) leaves everything off: fail
# open. The project's agent_budgets can only lower the limit; the CLI applies
# that rule. Go twin: budgetGate / evalBudget.
# K-128 (S3): a failed read still fails open, but no longer silently. _ss_bud_cause says
# why: timeout (the CLI outlived its wall-clock bound), no_output (it printed nothing and
# exited non-zero: too old to have `budget`, crashed, or could not be run), parse (what it
# printed is not a budget) or read_error (its JSON says read_failed: it could not read the
# spend log, or failed inside, so its "ok, nothing spent" is not a measurement).
# _ss_gate_report writes one WARN record naming the cause, after the lock is dropped. Not
# failures, so silent: a budget that is merely off (a limit of 0), and a CLI that prints
# nothing and exits 0 (it has no budget to report, as the test stubs do). Nothing is ever
# inferred from the words the CLI writes: its stderr and the warnings in its JSON carry
# text a project controls (a repeated agent_budgets key in .yakos.yml is echoed back in the
# YAML error), so the decision rests on the exit status, the watchdog and the structured
# fields of the JSON only (K-128, S12). Go twin: the cause of evalBudget.
# _ss_budget_raw: run the CLI in the background and give it _ss_budget_wait
# seconds of WALL-CLOCK time, so a hung or slow CLI can never stall the hook (no
# GNU `timeout`: see the K-117 rule). A watchdog subshell (the wrapper's pattern)
# flags and kills it while the hook simply waits. The old bound was 40 polls of
# `sleep 0.05`: it counted iterations, not time, so on a loaded runner each poll
# cost its 50 ms plus a fork and the "2 s" read took 4 s or more (K-128). The CLI
# exits non-zero at the hard stop AFTER printing its JSON, so the exit status is
# never used: only the watchdog's flag file marks a timeout.
# Sets _ss_bud_raw (the CLI's stdout; empty on a timeout), _ss_bud_rc (its exit status,
# used only when it printed nothing) and _ss_bud_cause=timeout when the watchdog fired.
# The CLI's stderr is discarded: nothing is read from it (see above).
_ss_budget_raw() {
    local tmp pid wd flag rc=0
    _ss_bud_raw=""; _ss_bud_cause=""; _ss_bud_rc=0
    tmp="$(mktemp 2>/dev/null)" || { _ss_bud_cause=no_output; return 0; }
    flag="$tmp.timeout"
    "$yakos_cli" budget check "$sup_agent" --project "$project_dir" --json >"$tmp" 2>/dev/null &
    pid=$!
    (
        sp=""
        trap 'kill "$sp" 2>/dev/null; exit 0' TERM
        sleep "$_ss_budget_wait" & sp=$!
        wait "$sp"
        : > "$flag"
        kill -TERM "$pid" 2>/dev/null
        sleep 1 & sp=$!
        wait "$sp"
        kill -KILL "$pid" 2>/dev/null
    ) >/dev/null 2>&1 &
    wd=$!
    { wait "$pid"; } 2>/dev/null || rc=$?
    _ss_bud_rc=$rc
    kill -TERM "$wd" 2>/dev/null || true
    { wait "$wd"; } 2>/dev/null || true
    if [ -e "$flag" ]; then
        _ss_bud_cause=timeout
    else
        _ss_bud_raw="$(cat "$tmp" 2>/dev/null)" || true
    fi
    rm -f "$tmp" "$flag"
    return 0
}
_ss_budget() {
    _ss_bud_state=off; _ss_bud_hard=0; _ss_bud_over=0
    _ss_bud_spent=0; _ss_bud_limit=0; _ss_bud_stop=0
    _ss_bud_cause=""
    local out
    [ -n "${yakos_cli:-}" ] || return 0
    _ss_budget_raw
    [ -z "$_ss_bud_cause" ] || return 0
    if [ -z "$_ss_bud_raw" ]; then
        # Nothing printed. A CLI that exits 0 has nothing to report (a stub without a budget): not a
        # failure. One that failed (too old to have `budget`, crashed, could not be run) is.
        if [ "$_ss_bud_rc" != 0 ]; then _ss_bud_cause=no_output; fi
        return 0
    fi
    # The CLI's own word that it could not read the spend (read_failed: set from the error and never
    # from text, also on its internal-error path) comes first, because its numbers are then "ok, nothing
    # spent", not a measurement. Otherwise not a budget at all (no numeric limit_usd, or not JSON) is a
    # parse failure, and a limit of 0 is the budget switched off, which is not. The seventh field is
    # read_failed as 0 or 1.
    out="$(printf '%s' "$_ss_bud_raw" | jq -r 'if .read_failed == true then ["off", 0, 0, 0, 0, 0, 1] elif (.limit_usd | type) != "number" then error("no limit_usd") else select(.limit_usd > 0) | [.state, .spent_usd, .limit_usd, .stop_usd, (if .state == "hard_stop" then 1 else 0 end), (if .state == "hard_stop" and (.spent_usd + 0.000000001) >= .stop_usd then 1 else 0 end), 0] end | @tsv' 2>/dev/null)" || { _ss_bud_cause=parse; return 0; }
    [ -n "$out" ] || return 0
    IFS="$(printf '\t')" read -r _ss_bud_state _ss_bud_spent _ss_bud_limit _ss_bud_stop _ss_bud_hard _ss_bud_over _ss_bud_rf <<EOF_BUD
$out
EOF_BUD
    case "$_ss_bud_hard$_ss_bud_over$_ss_bud_rf" in
        [01][01][01]) : ;;
        *) _ss_bud_state=off; _ss_bud_hard=0; _ss_bud_over=0; _ss_bud_cause=parse; return 0 ;;
    esac
    if [ "$_ss_bud_rf" = 1 ]; then
        _ss_bud_state=off; _ss_bud_hard=0; _ss_bud_over=0
        _ss_bud_spent=0; _ss_bud_limit=0; _ss_bud_stop=0
        _ss_bud_cause=read_error
    fi
    return 0
}
# _ss_ledger_stamp: a freshness token for the spend ledger, the dispatch log the budget is
# computed from: its size in bytes ("none" while it does not exist). Every dispatch event,
# spend records included, is appended to it, so the stamp changes whenever spend may have been
# recorded. The directory is the CLI's own ($YAKOS_DISPATCH_LOG, else ~/.yakos-state). One `wc`
# fork; skipped when there is no CLI to read a budget from. Go twin: ledgerStamp.
_ss_ledger_stamp() {
    _ss_stamp_v=""
    [ -n "${yakos_cli:-}" ] || return 0
    local d="${YAKOS_DISPATCH_LOG:-}"
    if [ -z "$d" ]; then
        if [ -n "${HOME:-}" ]; then d="$HOME/.yakos-state"; else d="${TMPDIR:-/tmp}/.yakos-state"; fi
    fi
    # A regular file only: wc would block on a FIFO planted in its place (and the Go twin's stat never does).
    if [ -f "$d/dispatch-log.ndjson" ]; then _ss_stamp_v="$({ wc -c < "$d/dispatch-log.ndjson"; } 2>/dev/null)" || true; fi
    _ss_stamp_v="${_ss_stamp_v//[[:space:]]/}"
    [ -n "$_ss_stamp_v" ] || _ss_stamp_v=none
    return 0
}
# _ss_budget_read: read the budget, stamping the ledger BEFORE the read, so that anything
# recorded after the stamp is noticed by the comparison under the lock (see _ss_gate).
_ss_budget_read() {
    _ss_ledger_stamp
    _ss_bud_stamp="$_ss_stamp_v"
    _ss_budget
    _ss_bud_ready=1
    return 0
}
# Test seam (K-128): with YAKOS_TEST_SEAMS=1 and a file .supervisor-test-pause in the work
# directory, a hook about to take the gate lock creates .supervisor-test-reached there and waits
# (20 s at most) for the pause file to be removed, so a test can change the world between the
# budget read and the lock without racing the hook. Fixed names in the directory the hook already
# writes, never a path from the environment; a no-op without the toggle. The marker is created
# exclusively (noclobber), so a planted link is never followed. Go twin: gatePause.
_ss_test_pause() {
    local i=0
    [ "${YAKOS_TEST_SEAMS:-}" = 1 ] && [ -e "$current_dir/.supervisor-test-pause" ] || return 0
    ( set -C; true > "$current_dir/.supervisor-test-reached" ) 2>/dev/null || true
    while [ -e "$current_dir/.supervisor-test-pause" ] && [ "$i" -lt 400 ]; do sleep 0.05; i=$((i + 1)); done
    return 0
}
# _ss_allow <routine|high> <now>: sets _ss_deny to "" (allow) or the reason:
# backoff, ceiling, budgetceil, budget, cap, interval. The one gate decision.
# The dollar budget exempts high-risk launches the same way the cap does:
# routine launches stop at the supervisor's hard_stop, high-risk ones run on to
# 2x the limit, decided here in-process (no env var or flag carries it).
# Go twin: allowLaunch.
_ss_allow() {
    _ss_deny=""
    if [ "$st_backoff" -gt "$2" ]; then _ss_deny=backoff; return 0; fi
    if [ "$1" = high ]; then
        if [ "$sup_ceil" -gt 0 ] && [ "$st_hlaunches" -ge "$sup_ceil" ]; then _ss_deny=ceiling; return 0; fi
        if [ "$_ss_bud_over" = 1 ]; then _ss_deny=budgetceil; fi
        return 0
    fi
    if [ "$_ss_bud_hard" = 1 ]; then _ss_deny=budget; return 0; fi
    if [ "$sup_cap" -gt 0 ] && [ "$st_launches" -ge "$sup_cap" ]; then _ss_deny=cap; return 0; fi
    if [ "$sup_interval" -gt 0 ] && [ $(($2 - st_last)) -lt "$sup_interval" ]; then _ss_deny=interval; fi
    return 0
}
# _ss_spawn <delay-seconds>: detached wrapper around the dispatch.
_ss_spawn() {
    _SSW_STATE="$_ss_state" _SSW_LOCK="$_ss_lock" _SSW_LOG="$_ss_wlog" \
    _SSW_PENDING="$_ss_pending" _SSW_FINDINGS="$findings" _SSW_DEADLINE="$sup_deadline" _SSW_CAP="$sup_cap" _SSW_CEIL="$sup_ceil" \
    _SSW_INTERVAL="$sup_interval" _SSW_BACKOFF="$sup_backoff" _SSW_DELAY="$1" \
    nohup "${BASH:-bash}" -c "$_ss_wrap" supervisor-wrap \
        "$yakos_cli" dispatch "$sup_agent" "$task" \
        --runtime "$sup_runtime" \
        --model "$sup_model" \
        >> "$current_dir/.supervisor-stdout.log" 2>>"$current_dir/.supervisor-stderr.log" &
    disown 2>/dev/null || true
}
# Hitting the high-risk ceiling means real dangerous events are no longer
# supervised: write a synthetic CRITICAL finding so block_on_critical
# operators are stopped and told instead of getting a silent WARN.
_ss_synth_finding() {
    local line
    line="$(jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --argjson c "$sup_ceil" \
        '{ts: $ts, batch_size: 0, scores: {}, overall: "CRITICAL", synthetic: true,
          rationale: ("High-risk supervisor launch ceiling (" + ($c|tostring) + ") reached for this session: further high-risk events are recorded but no longer supervised. Review the session and the pending events file."),
          recommended_action: "surface_to_operator"}' 2>/dev/null)" || return 0
    [ -n "$line" ] || return 0
    ( umask 077; printf '%s\n' "$line" >> "$findings" ) 2>/dev/null || true
    return 0
}
# Same CRITICAL alert for the dollar-budget ceiling. Go twin: writeSynthBudgetFinding.
_ss_synth_budget_finding() {
    local line
    line="$(jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg c "$(printf '%.2f' "$_ss_bud_stop")" \
        '{ts: $ts, batch_size: 0, scores: {}, overall: "CRITICAL", synthetic: true,
          rationale: ("Supervisor dollar-budget ceiling ($" + $c + ") reached: further high-risk events are recorded but no longer supervised. Raise or reset the budget (yakos budget status) and review the session and the pending events file."),
          recommended_action: "surface_to_operator"}' 2>/dev/null)" || return 0
    [ -n "$line" ] || return 0
    ( umask 077; printf '%s\n' "$line" >> "$findings" ) 2>/dev/null || true
    return 0
}
# _ss_gate <crossed 0|1>: called for every threshold crossing and for every
# high-risk escalation ($trigger_high=1). Always returns 0.
#
# K-128: the critical section is the run-state read-modify-write. The decision is
# made under the lock and recorded in _ss_act; everything that forks (the budget
# CLI, jq, ho_log, the synthetic findings) runs before the lock is taken or in
# _ss_gate_report after it is dropped, with the same records in the same order as
# when it all ran under the lock. Only the wrapper spawn stays inside, so a launch
# is claimed and started together (the Go twin rolls a failed spawn back there).
# The budget read taken before the lock is stamped with the size of the dispatch log and
# compared under the lock: a run that started, spent and ended while this hook waited
# leaves nothing in flight, so only the grown log says the read is stale, and the budget is
# then read again under the lock. Go twin: launchGate.
_ss_gate() {
    local crossed="$1" now stale kind delay nsec
    _ss_paths
    _ss_limits
    if [ -n "$_ss_ignored" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "project supervisor limit would reduce supervision; ignored (only a stricter value is accepted; loosen in ~/.yakos-state/supervisor-policy.yml)" \
            "$(jq -nc --arg k "$_ss_ignored" '{ignored_keys: $k}')"
    fi
    if [ -n "$_ss_invalid" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "supervisor limit below its minimum is invalid; using the default" \
            "$(jq -nc --arg k "$_ss_invalid" '{invalid_keys: $k}')"
    fi
    if [ "$crossed" = 1 ]; then _ss_wlog="$(ho_logdir)/supervisor-stream.ndjson"; fi
    now="$(date +%s)"; nsec=$SECONDS
    stale=$((sup_deadline + sup_interval + 60))
    _ss_bud_state=off; _ss_bud_hard=0; _ss_bud_over=0
    _ss_bud_spent=0; _ss_bud_limit=0; _ss_bud_stop=0
    _ss_bud_ready=0; _ss_bud_stamp=""; _ss_bud_cause=""
    while :; do
        # The budget read forks the CLI (up to ~2 s) and only a launch decision
        # needs it: a crossing with no live run in flight. Peek at the state
        # without the lock (it is replaced by rename, never torn) to find out ...
        if [ "$crossed" = 1 ] && [ "$_ss_bud_ready" = 0 ]; then
            _ss_load_state
            if [ -z "$st_start" ] || [ $((now - st_start)) -gt "$stale" ]; then _ss_budget_read; fi
        fi
        _ss_test_pause
        if ! _ss_lock_take gate; then
            if _ss_journal_gate; then _ss_note="trigger journaled for the next lock holder"; else _ss_note="could not journal the trigger"; fi
            ho_log "supervisor-stream" "WARN" "pass" \
                "launch-state lock busy or unremovable; $_ss_note" \
                "$(jq -nc --arg p "$_ss_lock" '{lock: $p}')"
            return 0
        fi
        trap 'rm -f "$_ss_lock" 2>/dev/null || true' EXIT
        # The budget read and the lock wait can take seconds: bring the clock up to
        # date (no fork: $SECONDS ticks in whole seconds, so it is within a second)
        # so the interval and the in-flight start are recorded against the time of
        # the decision, not of the hook's start.
        now=$((now + SECONDS - nsec)); nsec=$SECONDS
        _ss_load_state
        _ss_stale_age=""
        if [ -n "$st_start" ] && [ $((now - st_start)) -gt "$stale" ]; then
            _ss_stale_age=$((now - st_start)); st_start=""
        fi
        if [ "$crossed" = 1 ] && [ -z "$st_start" ]; then
            # ... and re-check under it. If the run ended in between, a launch is on
            # the table after all, so read the budget (outside the lock) and retry.
            if [ "$_ss_bud_ready" = 0 ]; then
                _ss_lock_drop
                _ss_budget_read
                continue
            fi
            # The read was taken before the lock. If the dispatch log has grown since (a
            # run started, spent and ended while this hook read and waited; of this session
            # or another) the read may be stale, and the state shows nothing in flight: read
            # again, here, under the lock, as before K-128 (the one case the CLI runs under it).
            _ss_ledger_stamp
            if [ "$_ss_stamp_v" != "$_ss_bud_stamp" ]; then
                _ss_bud_stamp="$_ss_stamp_v"
                _ss_budget
            fi
        fi
        break
    done
    # Test seam (K-117): widen the load-then-save window so a missing gate lock
    # is deterministic. Read from the process environment only (a project
    # .yakos.yml cannot set it) and only with YAKOS_TEST_SEAMS=1; a no-op in
    # production. Go twin: gateHold.
    if [ "${YAKOS_TEST_SEAMS:-}" = 1 ]; then
        case "${YAKOS_TEST_GATE_HOLD_MS:-}" in
            ''|*[!0-9]*) : ;;
            *) sleep "$((YAKOS_TEST_GATE_HOLD_MS / 1000)).$(printf '%03d' "$((YAKOS_TEST_GATE_HOLD_MS % 1000))")" ;;
        esac
    fi
    _ss_fold_gate
    _ss_act=""; _ss_first=0; _ss_deny=""; kind=""; delay=0
    if [ "$trigger_high" = 1 ]; then st_high=$((st_high + 1)); fi
    if [ -n "$st_start" ]; then
        st_pending=$((st_pending + 1)); _ss_pend_append
        _ss_act=coalesced
    elif [ "$crossed" != 1 ]; then
        st_pending=$((st_pending + 1)); _ss_pend_append
        _ss_act=recorded
    else
        kind=routine
        if [ "$st_high" -gt 0 ]; then kind=high; fi
        _ss_allow "$kind" "$now"
        case "$_ss_deny" in
            budget|backoff)
                st_pending=$((st_pending + 1)); _ss_pend_append
                _ss_act="$_ss_deny" ;;
            budgetceil)
                st_pending=$((st_pending + 1)); _ss_pend_append
                if [ "$st_budgetlog" != 1 ]; then st_budgetlog=1; _ss_first=1; fi
                _ss_act=budgetceil ;;
            cap)
                st_pending=$((st_pending + 1)); _ss_pend_append
                if [ "$st_caplog" != 1 ]; then st_caplog=1; _ss_first=1; fi
                _ss_act=cap ;;
            ceiling)
                st_pending=$((st_pending + 1)); _ss_pend_append
                if [ "$st_ceillog" != 1 ]; then st_ceillog=1; _ss_first=1; fi
                _ss_act=ceiling ;;
            interval)
                delay=$((sup_interval - (now - st_last)))
                st_launches=$((st_launches + 1)); st_pending=$((st_pending + 1)); _ss_pend_append
                st_last=$((now + delay)); st_start="$now"
                _ss_act=defer ;;
            *)
                if [ "$kind" = high ]; then st_hlaunches=$((st_hlaunches + 1)); else st_launches=$((st_launches + 1)); fi
                st_last="$now"; st_start="$now"
                _ss_act=launch ;;
        esac
    fi
    if _ss_save_state; then _ss_gfold_done; fi
    case "$_ss_act" in
        defer) _ss_spawn "$delay" ;;
        launch) _ss_spawn 0 ;;
    esac
    _ss_lock_drop
    _ss_gate_report "$kind" "$delay"
    return 0
}
# What the gate says, after the lock is dropped (K-117 records, K-119 notes).
_ss_gate_report() { # <kind> <delay>
    local kind="$1" delay="$2"
    if [ -n "$_ss_stale_age" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "in-flight supervisor run is older than its deadline; treating it as dead" \
            "$(jq -nc --argjson age "$_ss_stale_age" '{age_s: $age}')"
    fi
    case "$_ss_act" in
        coalesced)
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor run already in flight for this session; trigger coalesced into one follow-up" \
                "$(jq -nc --argjson p "$st_pending" --arg k "$_ss_key" '{coalesced: true, pending: $p, session_key: $k}')"
            return 0 ;;
        recorded)
            ho_log "supervisor-stream" "REPORT" "pass" \
                "high-risk event recorded; the next supervisor run will cover it" \
                "$(jq -nc --argjson p "$st_pending" '{high_risk: true, pending: $p}')"
            return 0 ;;
    esac
    # K-128 (S3): a budget that could not be read was treated as off (fail open, as
    # documented). Say so, with the cause, ahead of the decision's own records. A read
    # that did not fail leaves the cause empty. Go twin: reportGate.
    if [ -n "$_ss_bud_cause" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "supervisor budget unavailable (cause: $_ss_bud_cause); failing open: this launch decision is not checked against the dollar budget" \
            "$(jq -nc --arg a "$sup_agent" --arg c "$_ss_bud_cause" '{agent: $a, budget_reason: "budget_unavailable", cause: $c}')"
    fi
    # Budget warning: every launch decision at warning level says so. At
    # hard_stop the deny cases below say it (or, for a high-risk launch under
    # the ceiling, the exempt note here). Go twin: launchGate.
    if [ "$_ss_bud_state" = warning ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "supervisor budget at warning level" \
            "$(jq -nc --arg a "$sup_agent" --argjson s "$_ss_bud_spent" --argjson l "$_ss_bud_limit" '{agent: $a, spent_usd: $s, limit_usd: $l, budget_reason: "budget_warning"}')"
        echo "supervisor-stream: supervisor budget at $(awk -v s="$_ss_bud_spent" -v l="$_ss_bud_limit" 'BEGIN{printf "%.0f", s/l*100}')% ($(printf '$%.2f of $%.2f' "$_ss_bud_spent" "$_ss_bud_limit")); at 100% routine supervisor runs stop" >&2
    elif [ "$_ss_bud_hard" = 1 ] && [ -z "$_ss_deny" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "supervisor budget exhausted; high-risk launch allowed under the ceiling" \
            "$(jq -nc --arg a "$sup_agent" --argjson s "$_ss_bud_spent" --argjson l "$_ss_bud_limit" --argjson c "$_ss_bud_stop" '{agent: $a, spent_usd: $s, limit_usd: $l, ceiling_usd: $c, budget_reason: "budget_exhausted"}')"
        echo "supervisor-stream: supervisor budget exhausted ($(printf '$%.2f of $%.2f' "$_ss_bud_spent" "$_ss_bud_limit")); launching high-risk supervision under the $(printf '$%.2f' "$_ss_bud_stop") ceiling" >&2
    fi
    case "$_ss_act" in
        budget)
            ho_log "supervisor-stream" "WARN" "pass" \
                "supervisor budget exhausted; skipping this routine supervisor launch (high-risk events still launch)" \
                "$(jq -nc --arg a "$sup_agent" --argjson s "$_ss_bud_spent" --argjson l "$_ss_bud_limit" --arg k "$kind" '{agent: $a, spent_usd: $s, limit_usd: $l, budget_reason: "budget_exhausted", kind: $k}')"
            echo "supervisor-stream: supervisor budget exhausted ($(printf '$%.2f of $%.2f' "$_ss_bud_spent" "$_ss_bud_limit")); routine supervisor runs are skipped until it is raised, reset, or the month rolls over (yakos budget status)" >&2 ;;
        budgetceil)
            if [ "$_ss_first" = 1 ]; then
                ho_log "supervisor-stream" "WARN" "pass" \
                    "supervisor budget ceiling reached; high-risk launches are no longer supervised" \
                    "$(jq -nc --arg a "$sup_agent" --argjson s "$_ss_bud_spent" --argjson c "$_ss_bud_stop" '{agent: $a, spent_usd: $s, ceiling_usd: $c, budget_reason: "budget_exhausted"}')"
                echo "supervisor-stream: supervisor budget ceiling ($(printf '$%.2f' "$_ss_bud_stop")) reached; high-risk supervisor runs are skipped" >&2
                _ss_synth_budget_finding
            fi ;;
        backoff)
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor launch paused after an account session limit" \
                "$(jq -nc --argjson until "$st_backoff" '{backoff_until: $until}')" ;;
        cap)
            if [ "$_ss_first" = 1 ]; then
                ho_log "supervisor-stream" "WARN" "pass" \
                    "supervisor launch cap reached for this session; skipping further routine launches" \
                    "$(jq -nc --argjson cap "$sup_cap" --arg k "$_ss_key" '{capped: true, cap: $cap, session_key: $k}')"
                echo "supervisor-stream: launch cap ($sup_cap) reached for this session; routine supervisor runs are skipped (high-risk events still launch)" >&2
            fi ;;
        ceiling)
            if [ "$_ss_first" = 1 ]; then
                ho_log "supervisor-stream" "WARN" "pass" \
                    "high-risk supervisor launch ceiling reached for this session" \
                    "$(jq -nc --argjson c "$sup_ceil" '{ceiling: $c}')"
                echo "supervisor-stream: high-risk launch ceiling ($sup_ceil) reached for this session; supervisor runs are skipped" >&2
                _ss_synth_finding
            fi ;;
        defer)
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor launch deferred to the end of the minimum interval" \
                "$(jq -nc --argjson d "$delay" --argjson p "$st_pending" '{throttled: true, deferred_s: $d, pending: $p}')" ;;
        launch)
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor dispatch forked async (model=$sup_model runtime=$sup_runtime)" \
                "$(jq -nc --arg model "$sup_model" --arg runtime "$sup_runtime" --argjson deadline "$sup_deadline" --arg kind "$kind" \
                    '{dispatch: "async", model: $model, runtime: $runtime, deadline_s: $deadline, kind: $kind}')" ;;
    esac
    return 0
}


# --- 2. Local pre-filter (shell-only, zero LLM cost) -----------------------
trigger_high=0

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


    if [ -z "$escalate_reason" ]; then escalate_reason="$(_ss_sens_reason)"; fi

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


    if [ -z "$escalate_reason" ]; then escalate_reason="$(_ss_risk_reason)"; fi

    # If no trigger fired, buffer-only return — no dispatch, no counter tick.
    if [ -z "$escalate_reason" ]; then
        ho_log "supervisor-stream" "REPORT" "pass" \
            "pre-filter: no trigger; buffered without dispatch" \
            "$(jq -nc --arg tool "$tool" --arg file "$file_path" \
                '{pre_filter: "pass", tool: $tool, file: $file}')"
        _ss_shadow pass "" || true
        exit 0
    fi

    # K-117: a trigger is HIGH-risk when a sensitive-path or risk-regex match
    # exists, whichever check fired first (a curl|sh inside a large diff is
    # still high-risk). High-risk triggers bypass the launch cap.
    case "$escalate_reason" in
        risk-regex:*|sensitive-path:*) trigger_high=1 ;;
        *)
            if [ -n "$(_ss_sens_reason)" ] || [ -n "$(_ss_risk_reason)" ]; then trigger_high=1; fi
            ;;
    esac

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

# K-110/K-128: read-increment-write under the lock. Without it two
# concurrent hooks both read N, both write N+1, and both cross the same
# score-every threshold (double supervisor launch). The increment is the whole
# decision: each hook gets a unique value, so at most one sees a multiple of
# score_every. The critical section is the counter read, the write (a temp file,
# then a rename: a reader never sees a torn counter) and the release; the wait
# for the lock is the ~3 s ceiling of _ss_lock_take. If it expires the increment
# is NOT dropped: the hook leaves a "+1" record that the next lock holder folds
# in (_ss_journal_tick, docs/supervisor-mode.md "Lock protocol"). Go twin:
# incrementCounter (same lock).
if ! _ss_lock_take counter; then
    if _ss_journal_tick; then _ss_note="increment journaled for the next lock holder"; else _ss_note="could not journal the increment"; fi
    # A high-risk trigger also owes the session's run state a record.
    if [ "$trigger_high" = 1 ]; then _ss_paths; _ss_journal_gate || true; fi
    ho_log "supervisor-stream" "WARN" "pass" \
        "counter lock busy or unremovable; $_ss_note, skipping this escalation tick" \
        "$(jq -nc --arg p "$_ss_lock" '{lock: $p}')"
    exit 0
fi
# Release the lock on every exit path (kill, error) after this point.
trap 'rm -f "$_ss_lock" 2>/dev/null || true' EXIT
# The counter cannot be written (a directory in its place, say), so this tick cannot be counted.
# Say so (it used to be dropped without a word) and still record a high-risk trigger in the
# session's run state. Never returns: it ends the hook. Go twin: the counterWriteFailed case.
_ss_counter_unwritable() {
    _ss_lock_drop
    ho_log "supervisor-stream" "WARN" "pass" \
        "counter not writable; skipping this escalation tick" \
        "$(jq -nc --arg p "$counter" '{counter: $p}')"
    if [ "$trigger_high" = 1 ]; then _ss_gate 0 || true; fi
    exit 0
}
cur=0
if [ -f "$counter" ]; then { read -r cur < "$counter"; } 2>/dev/null || true; fi
case "$cur" in ''|*[!0-9]*) cur=0 ;; esac
_ss_fold_adds
cur=$((10#$cur + _ss_fold_n + 1))
_ss_ctmp="$counter.tmp.$$"
if [ -d "$counter" ]; then _ss_counter_unwritable; fi
if ! { printf '%d\n' "$cur" > "$_ss_ctmp" && mv -f "$_ss_ctmp" "$counter"; } 2>/dev/null; then
    rm -f "$_ss_ctmp" 2>/dev/null || true
    # No rename (a failed fork, say): a direct write still beats losing the tick.
    if ! { printf '%d\n' "$cur" > "$counter"; } 2>/dev/null; then _ss_counter_unwritable; fi
fi
_ss_folded=$_ss_fold_n
_ss_fold_done
_ss_lock_drop

# --- 4. Every N escalations, fork supervisor dispatch ----------------------

# Read score_every_n_calls from .yakos.yml; default 10. A value that is not a
# plain decimal (quoted, hex, signed) falls back to the default on its own.
score_every=10
if n="$(_ss_int "$(_ss_cfg_raw score_every_n_calls)")"; then
    [ "$n" -gt 0 ] && score_every="$n"
fi

# A threshold is crossed when a multiple of score_every lies in (cur - 1 - folded, cur]:
# with no journaled records that is cur % score_every == 0, as before.
if [ "$((cur / score_every))" -le "$(((cur - 1 - _ss_folded) / score_every))" ]; then
    ho_log "supervisor-stream" "REPORT" "pass" \
        "escalation buffered; not yet at score-every threshold" \
        "$(jq -nc --argjson cur "$cur" --argjson every "$score_every" \
            '{counter: $cur, score_every: $every, will_score: false}')"
    # A high-risk event is still recorded (and bumps the high-risk flag) so the
    # next run covers it even if the cap has been reached by then.
    if [ "$trigger_high" = 1 ]; then _ss_gate 0 || true; fi
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

if [ -n "$sup_model_bad" ]; then
    ho_log "supervisor-stream" "WARN" "pass" \
        "supervisor.model is not a known tier or alias; using haiku" \
        "$(jq -nc --arg m "$sup_model_bad" '{model: $m}')"
fi

# Fork in background (see _ss_spawn); the gate decides whether to launch,
# coalesce, defer or skip, and always returns.
_ss_gate 1 || true

exit 0
