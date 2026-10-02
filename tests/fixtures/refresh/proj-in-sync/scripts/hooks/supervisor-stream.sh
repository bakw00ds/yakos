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
# is in flight. Per session key, under the counter's lock dir, the gate:
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
_ssw_lock_once() {
    local try=0 dl=$((SECONDS + 3)) reap
    while [ "$try" -lt 150 ] && [ "$SECONDS" -lt "$dl" ]; do
        if mkdir "$_SSW_LOCK" 2>/dev/null; then _ssw_locked=1; return 0; fi
        try=$((try + 1))
        if [ -n "$(find "$_SSW_LOCK" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
            reap="$_SSW_LOCK.reap.$$"
            if mv "$_SSW_LOCK" "$reap" 2>/dev/null; then
                if [ -n "$(find "$reap" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
                    rm -rf "$reap" 2>/dev/null
                else
                    mv "$reap" "$_SSW_LOCK" 2>/dev/null || rm -rf "$reap" 2>/dev/null
                fi
            fi
        fi
        sleep 0.02 2>/dev/null || sleep 1
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
_ssw_unlock() { if [ "$_ssw_locked" = 1 ]; then rmdir "$_SSW_LOCK" 2>/dev/null; fi; _ssw_locked=0; return 0; }
_ssw_load() {
    st_start=""; st_launches=0; st_hlaunches=0; st_last=0; st_pending=0; st_high=0
    st_caplog=0; st_ceillog=0; st_backoff=0
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
        esac
    done < "$_SSW_STATE"
    return 0
}
_ssw_save() {
    local tmp="$_SSW_STATE.tmp.$$"
    if printf 'start=%s\nlaunches=%s\nhlaunches=%s\nlast=%s\npending=%s\nhigh=%s\ncaplog=%s\nceillog=%s\nbackoff=%s\n' \
        "$st_start" "$st_launches" "$st_hlaunches" "$st_last" "$st_pending" "$st_high" \
        "$st_caplog" "$st_ceillog" "$st_backoff" > "$tmp" 2>/dev/null \
        && mv "$tmp" "$_SSW_STATE" 2>/dev/null; then
        return 0
    fi
    rm -f "$tmp" 2>/dev/null
    return 1
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
        n="$st_pending"
        if [ "$n" -gt 0 ]; then
            if [ -f "$_SSW_PENDING" ]; then mv "$_SSW_PENDING" "$_SSW_PENDING.run" 2>/dev/null; fi
            st_pending=0; st_high=0
            _ssw_save
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
        _ssw_save; _ssw_unlock
        continue
    fi
    st_start=""
    _ssw_save; _ssw_unlock
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

_ss_load_state() {
    st_start=""; st_launches=0; st_hlaunches=0; st_last=0; st_pending=0; st_high=0
    st_caplog=0; st_ceillog=0; st_backoff=0
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
        esac
    done < "$_ss_state"
    st_launches=$((10#$st_launches)); st_hlaunches=$((10#$st_hlaunches)); st_last=$((10#$st_last))
    st_pending=$((10#$st_pending)); st_high=$((10#$st_high)); st_caplog=$((10#$st_caplog))
    st_ceillog=$((10#$st_ceillog)); st_backoff=$((10#$st_backoff))
    [ -z "$st_start" ] || st_start=$((10#$st_start))
    return 0
}
_ss_save_state() {
    local tmp="$_ss_state.tmp.$$"
    if printf 'start=%s\nlaunches=%s\nhlaunches=%s\nlast=%s\npending=%s\nhigh=%s\ncaplog=%s\nceillog=%s\nbackoff=%s\n' \
        "$st_start" "$st_launches" "$st_hlaunches" "$st_last" "$st_pending" "$st_high" \
        "$st_caplog" "$st_ceillog" "$st_backoff" > "$tmp" 2>/dev/null \
        && mv "$tmp" "$_ss_state" 2>/dev/null; then
        return 0
    fi
    rm -f "$tmp" 2>/dev/null || true
    return 1
}
# Per-session pending file: redacted event previews (owner-only), last 100.
_ss_pend_append() {
    ( umask 077; printf '%s\n' "$event" >> "$_ss_pending" ) 2>/dev/null || return 0
    chmod 600 "$_ss_pending" 2>/dev/null || true
    local n tmp
    n="$(wc -l < "$_ss_pending" 2>/dev/null | tr -d ' ')"
    if [ "${n:-0}" -gt 150 ]; then
        tmp="$_ss_pending.tmp.$$"
        ( umask 077; tail -n 100 "$_ss_pending" > "$tmp" ) 2>/dev/null && mv "$tmp" "$_ss_pending" 2>/dev/null
        chmod 600 "$_ss_pending" 2>/dev/null || true
    fi
    return 0
}
# Same mkdir lock as the counter (rename-then-recheck reap, 3 s budget).
_ss_lock_take() {
    local try=0 dl=$((SECONDS + 3)) reap
    while [ "$try" -lt 150 ] && [ "$SECONDS" -lt "$dl" ]; do
        if mkdir "$_ss_lock" 2>/dev/null; then _ss_locked=1; return 0; fi
        try=$((try + 1))
        if [ -n "$(find "$_ss_lock" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
            reap="$_ss_lock.reap.$$"
            if mv "$_ss_lock" "$reap" 2>/dev/null; then
                if [ -n "$(find "$reap" -maxdepth 0 -mmin +1 2>/dev/null)" ]; then
                    rm -rf "$reap" 2>/dev/null || true
                else
                    mv "$reap" "$_ss_lock" 2>/dev/null || rm -rf "$reap" 2>/dev/null || true
                fi
            fi
        fi
        sleep 0.02 2>/dev/null || sleep 1
    done
    return 1
}
_ss_lock_drop() {
    if [ "$_ss_locked" = 1 ]; then rmdir "$_ss_lock" 2>/dev/null || true; fi
    _ss_locked=0
    trap - EXIT
}
# _ss_allow <routine|high> <now>: sets _ss_deny to "" (allow) or the reason:
# backoff, ceiling, cap, interval. The one gate decision; the budget check
# (#316) plugs in here and exempts high-risk launches the same way.
_ss_allow() {
    _ss_deny=""
    if [ "$st_backoff" -gt "$2" ]; then _ss_deny=backoff; return 0; fi
    if [ "$1" = high ]; then
        if [ "$sup_ceil" -gt 0 ] && [ "$st_hlaunches" -ge "$sup_ceil" ]; then _ss_deny=ceiling; fi
        return 0
    fi
    if [ "$sup_cap" -gt 0 ] && [ "$st_launches" -ge "$sup_cap" ]; then _ss_deny=cap; return 0; fi
    if [ "$sup_interval" -gt 0 ] && [ $(($2 - st_last)) -lt "$sup_interval" ]; then _ss_deny=interval; fi
    return 0
}
# _ss_spawn <delay-seconds>: detached wrapper around the dispatch.
_ss_spawn() {
    _SSW_STATE="$_ss_state" _SSW_LOCK="$_ss_lock" _SSW_LOG="$(ho_logdir)/supervisor-stream.ndjson" \
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
# _ss_gate <crossed 0|1>: called for every threshold crossing and for every
# high-risk escalation ($trigger_high=1). Always returns 0.
_ss_gate() {
    local crossed="$1" now stale kind delay
    _ss_key="$(printf '%s' "$session_id" | LC_ALL=C tr -c 'A-Za-z0-9_-' '_' | head -c 64)"
    [ -n "$_ss_key" ] || _ss_key="nosession"
    _ss_state="$current_dir/.supervisor-run.$_ss_key"
    _ss_pending="$current_dir/.supervisor-pending.$_ss_key"
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
    if ! _ss_lock_take; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "launch-state lock busy or unremovable; skipping this supervisor launch" \
            "$(jq -nc --arg p "$_ss_lock" '{lock: $p}')"
        return 0
    fi
    trap 'rmdir "$_ss_lock" 2>/dev/null || true' EXIT
    _ss_load_state
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
    now="$(date +%s)"
    stale=$((sup_deadline + sup_interval + 60))
    if [ -n "$st_start" ] && [ $((now - st_start)) -gt "$stale" ]; then
        ho_log "supervisor-stream" "WARN" "pass" \
            "in-flight supervisor run is older than its deadline; treating it as dead" \
            "$(jq -nc --argjson age "$((now - st_start))" '{age_s: $age}')"
        st_start=""
    fi
    if [ "$trigger_high" = 1 ]; then st_high=$((st_high + 1)); fi
    if [ -n "$st_start" ]; then
        st_pending=$((st_pending + 1)); _ss_pend_append; _ss_save_state || true
        ho_log "supervisor-stream" "REPORT" "pass" \
            "supervisor run already in flight for this session; trigger coalesced into one follow-up" \
            "$(jq -nc --argjson p "$st_pending" --arg k "$_ss_key" '{coalesced: true, pending: $p, session_key: $k}')"
        _ss_lock_drop; return 0
    fi
    if [ "$crossed" != 1 ]; then
        st_pending=$((st_pending + 1)); _ss_pend_append; _ss_save_state || true
        ho_log "supervisor-stream" "REPORT" "pass" \
            "high-risk event recorded; the next supervisor run will cover it" \
            "$(jq -nc --argjson p "$st_pending" '{high_risk: true, pending: $p}')"
        _ss_lock_drop; return 0
    fi
    kind=routine
    if [ "$st_high" -gt 0 ]; then kind=high; fi
    _ss_allow "$kind" "$now"
    case "$_ss_deny" in
        backoff)
            st_pending=$((st_pending + 1)); _ss_pend_append; _ss_save_state || true
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor launch paused after an account session limit" \
                "$(jq -nc --argjson until "$st_backoff" '{backoff_until: $until}')"
            _ss_lock_drop; return 0 ;;
        cap)
            st_pending=$((st_pending + 1)); _ss_pend_append
            if [ "$st_caplog" != 1 ]; then
                st_caplog=1
                ho_log "supervisor-stream" "WARN" "pass" \
                    "supervisor launch cap reached for this session; skipping further routine launches" \
                    "$(jq -nc --argjson cap "$sup_cap" --arg k "$_ss_key" '{capped: true, cap: $cap, session_key: $k}')"
                echo "supervisor-stream: launch cap ($sup_cap) reached for this session; routine supervisor runs are skipped (high-risk events still launch)" >&2
            fi
            _ss_save_state || true; _ss_lock_drop; return 0 ;;
        ceiling)
            st_pending=$((st_pending + 1)); _ss_pend_append
            if [ "$st_ceillog" != 1 ]; then
                st_ceillog=1
                ho_log "supervisor-stream" "WARN" "pass" \
                    "high-risk supervisor launch ceiling reached for this session" \
                    "$(jq -nc --argjson c "$sup_ceil" '{ceiling: $c}')"
                echo "supervisor-stream: high-risk launch ceiling ($sup_ceil) reached for this session; supervisor runs are skipped" >&2
                _ss_synth_finding
            fi
            _ss_save_state || true; _ss_lock_drop; return 0 ;;
        interval)
            delay=$((sup_interval - (now - st_last)))
            st_launches=$((st_launches + 1)); st_pending=$((st_pending + 1)); _ss_pend_append
            st_last=$((now + delay)); st_start="$now"
            _ss_save_state || true
            _ss_spawn "$delay"
            ho_log "supervisor-stream" "REPORT" "pass" \
                "supervisor launch deferred to the end of the minimum interval" \
                "$(jq -nc --argjson d "$delay" --argjson p "$st_pending" '{throttled: true, deferred_s: $d, pending: $p}')"
            _ss_lock_drop; return 0 ;;
    esac
    if [ "$kind" = high ]; then st_hlaunches=$((st_hlaunches + 1)); else st_launches=$((st_launches + 1)); fi
    st_last="$now"; st_start="$now"
    _ss_save_state || true
    _ss_spawn 0
    ho_log "supervisor-stream" "REPORT" "pass" \
        "supervisor dispatch forked async (model=$sup_model runtime=$sup_runtime)" \
        "$(jq -nc --arg model "$sup_model" --arg runtime "$sup_runtime" --argjson deadline "$sup_deadline" --arg kind "$kind" \
            '{dispatch: "async", model: $model, runtime: $runtime, deadline_s: $deadline, kind: $kind}')"
    _ss_lock_drop
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

# Read score_every_n_calls from .yakos.yml; default 10. A value that is not a
# plain decimal (quoted, hex, signed) falls back to the default on its own.
score_every=10
if n="$(_ss_int "$(_ss_cfg_raw score_every_n_calls)")"; then
    [ "$n" -gt 0 ] && score_every="$n"
fi

if [ "$((cur % score_every))" -ne 0 ]; then
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
