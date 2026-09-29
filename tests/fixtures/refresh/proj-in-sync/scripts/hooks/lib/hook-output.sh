#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: hook-output.sh — structured NDJSON logging + bypass + block helpers.
#
# Severity tiers (from Phase 1.5 §12):
#   BLOCK  — exit 2; tool call refused
#   WARN   — exit 0; non-empty warning field; agent sees it but action proceeds
#   PASS   — exit 0; clean
#   REPORT — exit 0; pure telemetry
#
# Usage:
#     . "$HOOK_DIR/lib/hook-output.sh"
#     ho_log <hook> <severity> <decision> <reason> [extra-jq-object]
#     ho_block <hook> <message>                # writes BLOCK record + exits 2
#     ho_check_bypass <hook> <scope> && pass   # returns 0 if bypass active

if [ "${HO_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
# HO_LOADED=1 is set on the LAST line of this file (K-101): a checked `.` must mean
# "parsed through to the end", not merely "started".

# paths.sh provides yakos_work_dir / yakos_current_dir / yakos_logs_dir /
# yakos_bypass_file etc. — the canonical resolver shared with the CLI.
# It lives at $HOOK_DIR/lib/paths.sh in both the framework tree and any
# project that ran 'yakos init' (init copies it).
__ho_self_dir="$(cd "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
if [ -f "$__ho_self_dir/paths.sh" ]; then
    # shellcheck source=./paths.sh
    . "$__ho_self_dir/paths.sh"
fi

ho_now() {
    date -u +%Y-%m-%dT%H:%M:%SZ
}

ho_logdir() {
    # Use the canonical resolver if available, otherwise fall back to the
    # in-repo path. Tests can override via $YAKOS_WORK_DIR or
    # $YAKOS_INPLACE_WORK=1 (see paths.sh).
    if command -v yakos_logs_dir >/dev/null 2>&1; then
        yakos_logs_dir
    else
        printf '%s' "${CLAUDE_PROJECT_DIR:-.}/work/current/logs"
    fi
}

ho_log() {
    # ho_log <hook-name> <severity> <decision> <reason> [extra-jq-object]
    # extra-jq-object defaults to {}; merged into the record.
    local hook="$1" severity="$2" decision="$3" reason="$4" extra="${5:-}"
    [ -z "$extra" ] && extra='{}'

    local logdir logfile
    logdir="$(ho_logdir)"
    mkdir -p "$logdir" 2>/dev/null || return 0
    logfile="$logdir/${hook}.ndjson"

    # If hi_init wasn't called or jq isn't available, write a minimal record.
    if ! command -v jq >/dev/null 2>&1; then
        printf '{"ts":"%s","hook":"%s","severity":"%s","decision":"%s","reason":%s}\n' \
            "$(ho_now)" "$hook" "$severity" "$decision" \
            "$(printf '%s' "$reason" | sed 's/"/\\"/g; s/^/"/; s/$/"/')" \
            >> "$logfile"
        return 0
    fi

    jq -nc \
        --arg ts "$(ho_now)" \
        --arg hook "$hook" \
        --arg severity "$severity" \
        --arg decision "$decision" \
        --arg reason "$reason" \
        --arg agent "$(hi_sender_role 2>/dev/null || echo lead)" \
        --arg session "$(hi_session_id 2>/dev/null || echo '')" \
        --arg event "$(hi_event 2>/dev/null || echo '')" \
        --argjson extra "$extra" \
        '{ts: $ts, hook: $hook, severity: $severity, decision: $decision, reason: $reason, agent: $agent, session_id: $session, event: $event} + $extra' \
        >> "$logfile"
}

ho_block() {
    # Print message to stderr (surfaced to model per Phase 0 Test 6a) and
    # exit 2. The hook should already have written its log record before
    # calling this.
    local hook="$1" reason="$2"
    # `|| true` (K-101): with stderr closed the echo fails, and under a caller's
    # `set -e` that would exit 1 (non-blocking) before the `exit 2` below.
    echo "${hook}: ${reason}" >&2 || true
    exit 2
}

ho_check_bypass() {
    # Returns 0 (success) if the active-bypass file has an entry whose:
    #   **Hook:** value contains <hook-name>, AND
    #   **Scope:** value matches <scope-string> EXACTLY or as an explicit glob
    # Otherwise returns 1.
    #
    # Scope matching (K-99). This used to be a substring test, so a scope of
    # `web/secret.env-rotation` also bypassed `web/secret.env`, and an empty
    # probe scope matched every entry for the hook. Now:
    #   - the probe is compared as given (callers pass slash paths; a POSIX
    #     file may legitimately contain a backslash);
    #   - an entry scope matches when it equals the probe (case-sensitive);
    #   - otherwise an entry scope containing `*` is a glob with bash `case`
    #     semantics (the matcher path-allowlist uses; `*` crosses `/`), so
    #     `web/**` covers everything under web/;
    #   - a blank entry scope matches nothing and prints a WARN to stderr;
    #   - an empty probe matches nothing.
    # Migration: a bare-prefix entry keeps working only as `prefix/**`.
    # Go twin: cli-go/internal/hooks/hookbypass (Check).
    #
    # Bypass entries must appear after the literal `## Active entries`
    # heading in work/current/hook-bypass.md. The format-example block
    # above that header is ignored.
    local hook="$1" scope="${2:-}"
    local bypass_file
    if command -v yakos_bypass_file >/dev/null 2>&1; then
        bypass_file="$(yakos_bypass_file)"
    else
        bypass_file="${CLAUDE_PROJECT_DIR:-.}/work/current/hook-bypass.md"
    fi
    [ -f "$bypass_file" ] || return 1

    local probe="$scope"
    [ -n "$probe" ] || return 1

    # awk owns the entry state machine (identical to ho_check_bypass_exact)
    # and emits one "S<scope>" line per Scope field of every entry whose
    # Hook matches, judged once the entry is complete so field order is
    # free. The glob/equality decision is made in bash below because awk
    # has no `case`-style matcher.
    local scopes
    scopes="$(awk -v hook="$hook" '
        BEGIN { active=0; in_entry=0; ok_hook=0; n=0 }
        function flush(   i) {
            if (in_entry && ok_hook) for (i = 1; i <= n; i++) print "S" sc[i]
        }
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active=1; next }
        active && /^##[[:space:]]+bypass:/ {
            flush()
            in_entry=1; ok_hook=0; n=0; next
        }
        in_entry && /^\*\*Hook:\*\*/ {
            line=$0; sub(/^\*\*Hook:\*\*[[:space:]]*/, "", line)
            if (index(line, hook) > 0) ok_hook=1
        }
        in_entry && /^\*\*Scope:\*\*/ {
            line=$0; sub(/^\*\*Scope:\*\*[[:space:]]*/, "", line)
            gsub(/[[:space:]]+$/, "", line)
            sc[++n]=line
        }
        END { flush() }
    ' "$bypass_file")"

    local line entry
    while IFS= read -r line; do
        case "$line" in S*) ;; *) continue ;; esac
        entry="${line#S}"
        if [ -z "$entry" ]; then
            echo "WARN: bypass entry has empty scope, ignored" >&2
            continue
        fi
        if [ "$entry" = "$probe" ]; then
            return 0
        fi
        case "$entry" in
            *'*'*)
                # shellcheck disable=SC2254  # intentional: entry is a glob
                case "$probe" in
                    $entry) return 0 ;;
                esac
                ;;
        esac
    done <<EOF
$scopes
EOF
    return 1
}

ho_check_bypass_exact() {
    # Like ho_check_bypass, but the **Scope:** value must match <scope>
    # EXACTLY (after trimming surrounding whitespace), not merely contain
    # it as a substring.
    #
    # Since K-99 ho_check_bypass is itself exact-or-glob, but a glob is the
    # wrong tool for an opt-in SENTINEL (and for the path-allowlist escape
    # guards): a literal `*` scope would satisfy it. That call needs a
    # strict literal comparison, so this helper stays separate: a literal-string
    # sentinel like "degraded-input" is meant to mean "the operator wrote
    # this exact word on purpose," and under substring matching an
    # unrelated Scope that happens to CONTAIN the sentinel — a real
    # filename `api/degraded-input.go`, or the literal negation
    # `not-degraded-input` — satisfied it too (security review R3-2,
    # round 4), silently widening the opt-in exactly the way R2-3 already
    # had to close once for the probe-scope side of this same check.
    local hook="$1" scope="$2"
    local bypass_file
    if command -v yakos_bypass_file >/dev/null 2>&1; then
        bypass_file="$(yakos_bypass_file)"
    else
        bypass_file="${CLAUDE_PROJECT_DIR:-.}/work/current/hook-bypass.md"
    fi
    [ -f "$bypass_file" ] || return 1

    awk -v hook="$hook" -v scope="$scope" '
        BEGIN { active=0; in_entry=0; ok_hook=0; ok_scope=0; found=0 }
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active=1; next }
        active && /^##[[:space:]]+bypass:/ {
            if (in_entry && ok_hook && ok_scope) found=1
            in_entry=1; ok_hook=0; ok_scope=0; next
        }
        in_entry && /^\*\*Hook:\*\*/ {
            line=$0; sub(/^\*\*Hook:\*\*[[:space:]]*/, "", line)
            if (index(line, hook) > 0) ok_hook=1
        }
        in_entry && /^\*\*Scope:\*\*/ {
            line=$0; sub(/^\*\*Scope:\*\*[[:space:]]*/, "", line)
            gsub(/^[[:space:]]+/, "", line)
            gsub(/[[:space:]]+$/, "", line)
            if (line == scope) ok_scope=1
        }
        END {
            if (in_entry && ok_hook && ok_scope) found=1
            exit(found ? 0 : 1)
        }
    ' "$bypass_file"
}

# Must stay the last statement: reaching it proves the whole file parsed.
HO_LOADED=1
