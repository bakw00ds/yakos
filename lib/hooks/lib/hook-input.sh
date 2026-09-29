#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: hook-input.sh — stdin JSON helpers shared by every YakOS hook script.
#
# Usage in a hook:
#     . "$HOOK_DIR/lib/hook-input.sh"
#     hi_init                      # reads stdin once into $HI_INPUT
#     agent="$(hi_sender_role)"
#     file="$(hi_file_path)"
#
# The "sender" identity follows Phase 1.7's finding: read .agent_type from
# stdin JSON. Lead-originated events have .agent_type absent — we map that
# to the literal string "lead". This is the canonical lead/teammate
# discriminator across all hook events (Phase 0 Test 7 + Phase 1.7).
#
# --- HOOK_FAIL_CLOSED (security review C5) ----------------------------------
#
# hi_init validates that jq is on PATH and that stdin (when present) parses
# as JSON. Historically, a missing jq or malformed stdin left $HI_INPUT
# effectively empty, every hi_* accessor returned "", and every hook's
# `case "$tool" in ... *) exit 0 ;; esac` fell through to a silent PASS —
# i.e. the enforcement hooks (path-allowlist, secret-scan, supervisor-gate,
# supervisor-ack-gate, budget-guard, peer-claim, plan-quality-gate) degraded
# to no-ops exactly when the operator believed they were still running.
#
# A hook that BLOCKS (calls ho_block / can exit 2) must set
# `HOOK_FAIL_CLOSED=1` before sourcing this file:
#
#     HOOK_FAIL_CLOSED=1
#     . "$HOOK_DIR/lib/hook-input.sh"
#
# With that set, hi_init exits 2 (with a stderr explanation) instead of
# silently continuing with empty input. Purely observational hooks
# (path-log, mailbox-mirror, team-lifecycle, session-end-check, telemetry
# branches of output-injection-scan/task-dependency-gate/
# task-complete-dispatch, which never call ho_block) must NOT set
# HOOK_FAIL_CLOSED — they keep today's fail-open-with-a-warning behavior,
# per the README's "no-block policy for telemetry hooks".
#
# --- Emergency escape hatches (security review N2, round 2) -----------------
#
# A degraded jq/stdin lands BEFORE each hook's own tool-name case gate, so
# with HOOK_FAIL_CLOSED=1 the exit 2 above used to run unconditionally —
# including for budget-guard.sh, which matches every tool call ("*"). A
# missing jq therefore locked an operator out of Read/Edit/Bash entirely,
# with YAKOS_BUDGET_DISABLE=1, .yakos.yml, and hook-bypass.md all
# unreachable, because they're normally checked AFTER hi_init. Two
# independent overrides are honored here, BEFORE the exit 2:
#   1. YAKOS_HOOKS_FAIL_OPEN=1 — a single documented, session-wide,
#      emergency-only kill switch. Set it, fix jq, unset it.
#   2. A work/current/hook-bypass.md entry with `**Hook:** <hookname>` AND
#      `**Scope:** degraded-input` EXACTLY (checked via the awk-based
#      ho_check_bypass_exact, which needs no jq). The scope sentinel is
#      required (security review R2-3, round 3) — an empty probe scope
#      would otherwise match ANY entry for that hook, so a narrow bypass
#      an operator wrote for one unrelated file would silently disable
#      this hook's fail-closed behavior for every future degraded-input
#      event. The match is exact, not substring (security review R3-2,
#      round 4) — a Scope that merely contains the word (a filename like
#      `api/degraded-input.go`) does not count.
# Each hook's own `*_DISABLE` / `yakos_coord_enabled` check is ALSO moved
# above its `hi_init` call so it's reachable even when jq is broken,
# without needing either override above.
#
# See lib/hooks/README.md for the full writeup.

if [ "${HI_LOADED:-0}" = "1" ]; then
    return 0 2>/dev/null || exit 0
fi
# NOTE: HI_LOADED=1 is set on the LAST line of this file, not here. Setting it
# up front made a parse error later in the file invisible to a caller that
# checks the sentinel after `.` (K-101): bash 5 keeps going past a syntax error
# in a sourced file and returns 0, so "sourced OK" must mean "reached the end".

HI_INPUT=""
# HI_DEGRADED=1 once hi_init decided the payload could not be trusted (missing
# jq, empty / malformed / non-object stdin, or a jq that lies). HI_DEGRADED_REASON
# carries the human-readable cause. Blocking hooks never see HI_DEGRADED=1 unless
# an emergency override let them continue; non-blocking hooks exit 0 (see
# _hi_fail_or_warn).
HI_DEGRADED=0
HI_DEGRADED_REASON=""

# hi_init's own degraded-input handler. Kept separate from hi_init so it can
# be unit-exercised and so the control flow in hi_init stays readable.
_hi_fail_or_warn() {
    local reason="$1"
    # $2 == "exit": the degradation is one where continuing with an empty
    # HI_INPUT is unsafe even for a non-blocking hook (a jq that lies), so the
    # non-blocking branch exits 0 (WARN, no stdout) instead of returning.
    local mode="${2:-}"
    local name
    # shellcheck disable=SC2034  # read by the sourcing hook (public state)
    HI_DEGRADED=1
    # shellcheck disable=SC2034
    HI_DEGRADED_REASON="$reason"
    name="$(basename -- "${0:-hook}" 2>/dev/null || echo hook)"
    name="${name%.sh}"

    if [ "${HOOK_FAIL_CLOSED:-0}" = "1" ]; then
        # Emergency escape hatches — checked BEFORE the exit 2. Both work
        # without jq: the env var is a plain string compare, and
        # ho_check_bypass (hook-output.sh) is awk-based.
        if [ "${YAKOS_HOOKS_FAIL_OPEN:-0}" = "1" ]; then
            if command -v ho_log >/dev/null 2>&1; then
                ho_log "$name" "WARN" "pass" "degraded input ($reason) but YAKOS_HOOKS_FAIL_OPEN=1 override active" "{}" 2>/dev/null || true
            fi
            echo "${name}: WARN — degraded input ($reason), but YAKOS_HOOKS_FAIL_OPEN=1 is set; passing through." >&2
            echo "${name}: this is an emergency override — unset it once jq/stdin are fixed." >&2
            HI_INPUT=""
            return 0
        fi
        # Security review R2-3 (round 3): the scope probe is the literal
        # sentinel "degraded-input", NOT an empty string. ho_check_bypass's
        # awk treats an empty scope as "matches any entry for this hook"
        # (`scope == "" || index(line, scope) > 0`), so an empty probe here
        # meant ANY hook-bypass.md entry for this hook name — including one
        # an operator scoped to a single unrelated file weeks ago — silently
        # disabled this hook's fail-closed behavior for every future
        # degraded-input event. Requiring the explicit sentinel means an
        # operator has to opt in to THIS specific override on purpose.
        # YAKOS_HOOKS_FAIL_OPEN=1 above already covers the genuine
        # emergency case, so this path can afford to be strict.
        #
        # Security review R3-2 (round 4): ho_check_bypass's own substring
        # matching (correct and load-bearing for its OTHER callers — see
        # its comment in hook-output.sh) meant a Scope that merely
        # CONTAINED "degraded-input" also satisfied this probe — an
        # ordinary filename like `api/degraded-input.go`, or even the
        # literal negation `not-degraded-input`. Use
        # ho_check_bypass_exact instead, which requires the Scope value to
        # equal the sentinel exactly, matching what
        # hook-bypass.template.md has always documented.
        if command -v ho_check_bypass_exact >/dev/null 2>&1 && ho_check_bypass_exact "$name" "degraded-input"; then
            if command -v ho_log >/dev/null 2>&1; then
                ho_log "$name" "WARN" "pass" "degraded input ($reason) but hook-bypass.md override active (scope: degraded-input)" "{}" 2>/dev/null || true
            fi
            echo "${name}: WARN — degraded input ($reason), but a hook-bypass.md entry for '$name' scoped to 'degraded-input' is active; passing through." >&2
            HI_INPUT=""
            return 0
        fi

        # Best-effort log record before we exit — ho_log degrades gracefully
        # when jq itself is the thing that's missing (see hook-output.sh).
        if command -v ho_log >/dev/null 2>&1; then
            ho_log "$name" "BLOCK" "block" "degraded input, failing closed: $reason" "{}" 2>/dev/null || true
        fi
        echo "${name}: BLOCKED — cannot safely evaluate this tool call ($reason)." >&2
        echo "${name}: this hook enforces a security control and refuses to fail open." >&2
        echo "${name}: fix jq on PATH / the caller's JSON payload, then retry." >&2
        echo "${name}: emergency overrides: export YAKOS_HOOKS_FAIL_OPEN=1, or add a" >&2
        echo "${name}: work/current/hook-bypass.md entry with **Hook:** $name and" >&2
        echo "${name}: **Scope:** degraded-input." >&2
        exit 2
    fi

    if [ "$mode" = "exit" ]; then
        if command -v ho_log >/dev/null 2>&1; then
            ho_log "$name" "WARN" "pass" "degraded input ($reason); non-blocking hook skipped" "{}" 2>/dev/null || true
        fi
        echo "${name}: WARN — $reason. Skipping this non-blocking hook (tool call not blocked)." >&2
        exit 0
    fi
    echo "${name}: WARN — $reason. This hook is degraded for this event (jq unavailable or stdin unparseable); treating input as empty." >&2
}

# _hi_decoder_sane — 0 when jq actually evaluates programs. A jq that prints
# garbage, prints a constant ("object", `[1]`, ...) for every query, or is a
# shim that ignores its program cannot compute a fresh arithmetic result, so
# the canary below fails. The value is derived at call time from $$/$RANDOM so a
# stub cannot hard-code the answer. Without this, `jq -r type` answering
# "object" to everything sailed through every type check and each accessor then
# returned the same constant, so the hook's tool-name gate fell to `*) exit 0`.
_hi_decoder_sane() {
    local a b want got
    a=$(( ($$ % 89) + 11 ))
    b=$(( (${RANDOM:-7} % 89) + 11 ))
    want=$(( a * b + a ))
    got="$(jq -nr --argjson a "$a" --argjson b "$b" '$a * $b + $a' 2>/dev/null)" || return 1
    [ "$got" = "$want" ]
}

# hi_skip_if_no_jq — for NON-BLOCKING (telemetry / report-only) hooks.
#
# Call before hi_init. When jq is missing, log a one-line warning to stderr
# and exit 0 so the tool call proceeds, instead of dying with exit 127 from
# an unguarded `$(jq ...)` under `set -e` (K-81). Blocking / security hooks
# (path-allowlist, secret-scan, budget-guard, ...) must NOT call this: they
# set HOOK_FAIL_CLOSED and rely on hi_init's fail-closed exit 2.
hi_skip_if_no_jq() {
    command -v jq >/dev/null 2>&1 && return 0
    local name
    name="$(basename -- "${0:-hook}" 2>/dev/null || echo hook)"
    name="${name%.sh}"
    echo "${name}: WARN — jq is not installed or not on PATH; skipping this non-blocking hook (tool call not blocked)." >&2
    if command -v ho_log >/dev/null 2>&1; then
        ho_log "$name" "WARN" "pass" "jq missing; non-blocking hook skipped" "{}" 2>/dev/null || true
    fi
    exit 0
}

hi_init() {
    # Slurp stdin once. Subsequent hi_* calls query $HI_INPUT via jq.
    if [ -t 0 ]; then
        # No stdin provided — leave HI_INPUT empty so callers can decide
        # what to do. Most hooks should treat this as a no-op pass. This
        # is the deliberate "run me by hand with no input" case, distinct
        # from "stdin was provided but is broken" below, so it is not
        # subject to HOOK_FAIL_CLOSED.
        HI_INPUT=""
        return 0
    fi

    HI_INPUT="$(cat)"

    if ! command -v jq >/dev/null 2>&1; then
        HI_INPUT=""
        _hi_fail_or_warn "jq is not installed or not on PATH"
        return 0
    fi

    # Security review N4 / C5 residue (round 2): stdin WAS provided (not a
    # tty — the exemption above) but read zero bytes. The previous
    # `[ -n "$HI_INPUT" ] &&` guard skipped validation entirely on an empty
    # read, so an empty pipe fell through to hi_tool -> "" ->
    # `case ... *) exit 0` — the exact silent PASS this whole mechanism
    # exists to close, just triggered by an empty payload instead of a
    # malformed one. Note this also revises the empty-`/dev/null`-redirect
    # case from a tolerated no-op to a normal degraded-input event; that's
    # intentional (see YAKOS_HOOKS_FAIL_OPEN / hook-bypass.md if a manual
    # `hook.sh < /dev/null` invocation needs to pass under
    # HOOK_FAIL_CLOSED=1).
    if [ -z "$HI_INPUT" ]; then
        _hi_fail_or_warn "stdin was provided but empty (0 bytes) — expected a JSON hook payload"
        return 0
    fi

    if ! jq empty <<< "$HI_INPUT" >/dev/null 2>&1; then
        HI_INPUT=""
        _hi_fail_or_warn "stdin did not parse as valid JSON"
        return 0
    fi

    # Security review N4 / C5 residue (round 2): `jq empty` accepts ANY
    # valid JSON — an array, a bare string, a number, `null` — not just an
    # object. Every hi_* accessor assumes an object (`.tool_name`,
    # `.tool_input.file_path`, ...), so a non-object payload silently
    # yielded empty fields everywhere, which is the same fail-open shape
    # as malformed JSON.
    if ! jq -e 'type == "object"' <<< "$HI_INPUT" >/dev/null 2>&1; then
        HI_INPUT=""
        _hi_fail_or_warn "stdin parsed as JSON but is not a JSON object (hook payloads are always an object)"
        return 0
    fi

    # K-101 (#289 review): every check above trusts jq's answers. A jq that is
    # present but lying (prints "garbage", a wrong-typed value, or "object" for
    # every query) passes them all. Verify the decoder with a canary, then
    # verify the payload decodes to an object carrying a string
    # hook_event_name that is literally present in the raw input. Non-blocking
    # hooks exit 0 here rather than continue on garbage accessors.
    if ! _hi_decoder_sane; then
        HI_INPUT=""
        _hi_fail_or_warn "jq is present but returned a wrong answer for a known query (broken or shadowed jq)" exit
        return 0
    fi
    local _hi_ev
    _hi_ev="$(jq -r 'if type == "object" and (.hook_event_name | type) == "string" then .hook_event_name else "" end' <<< "$HI_INPUT" 2>/dev/null)" || _hi_ev=""
    if [ -z "$_hi_ev" ]; then
        HI_INPUT=""
        _hi_fail_or_warn "stdin is a JSON object but has no string hook_event_name (not a Claude Code hook payload)" exit
        return 0
    fi
    case "$HI_INPUT" in
        *"$_hi_ev"*) ;;
        *)
            HI_INPUT=""
            _hi_fail_or_warn "jq decoded a hook_event_name that is not present in the payload (broken or shadowed jq)" exit
            return 0
            ;;
    esac
}

hi_field() {
    # Print the (string) value at jq path $1, or empty if missing/null.
    [ -n "$HI_INPUT" ] || { printf ''; return; }
    jq -r "$1 // empty" <<< "$HI_INPUT" 2>/dev/null || true
}

hi_field_or() {
    # Print field $1's value, or fallback $2 if missing/empty.
    local v
    v="$(hi_field "$1")"
    if [ -n "$v" ]; then
        printf '%s' "$v"
    else
        printf '%s' "$2"
    fi
}

hi_raw() {
    # Echo the whole input JSON (for hooks that pipe it onward).
    printf '%s' "$HI_INPUT"
}

# ---- common field shortcuts ------------------------------------------------

hi_event()        { hi_field '.hook_event_name'; }
hi_tool()         { hi_field '.tool_name'; }
hi_session_id()   { hi_field '.session_id'; }
hi_transcript()   { hi_field '.transcript_path'; }
hi_cwd()          { hi_field '.cwd'; }

# hi_strip_rt_prefix <value>
#   Strip the leading "yakos:" namespace prefix that claude attaches when
#   agents are registered via --plugin-dir with the "yakos" plugin name
#   (E2BIG fix, runtimes/claude.sh). Returns the bare agent id.
#   Example: "yakos:backend" → "backend", "lead" → "lead".
#   Only strips the exact "yakos:" prefix — does not touch other colons
#   (e.g. a project agent legitimately named "myns:helper" is left alone).
hi_strip_rt_prefix() {
    local v="$1"
    case "$v" in
        yakos:*) printf '%s' "${v#yakos:}" ;;
        *)        printf '%s' "$v" ;;
    esac
}

# Sender role: "lead" if .agent_type absent, otherwise the agent_type string
# with any "yakos:" namespace prefix stripped (see hi_strip_rt_prefix).
# Reading from stdin JSON, NOT $CLAUDE_CODE_AGENT (per Phase 1.7: env var
# is missing in team SendMessage hook fires).
#
# Leading/trailing whitespace is trimmed (security review N4.3: a runtime
# that happens to send "go-api " with a trailing space would otherwise miss
# its own policy key and fall through to "no policy" PASS — a robustness
# fix, not a security one, since .agent_type comes from the runtime, not
# an attacker). Case is deliberately left alone.
hi_sender_role() {
    local raw
    raw="$(hi_field_or '.agent_type' 'lead')"
    raw="${raw#"${raw%%[![:space:]]*}"}"
    raw="${raw%"${raw##*[![:space:]]}"}"
    hi_strip_rt_prefix "$raw"
}

# Tool-specific shortcuts
#
# hi_file_path falls back to .tool_input.notebook_path (C4: NotebookEdit
# carries its target under a different key than Edit/Write/MultiEdit) so
# every path-based hook gets a usable path without special-casing the tool.
hi_file_path()    { hi_field '.tool_input.file_path // .tool_input.notebook_path'; }
hi_content()      { hi_field '.tool_input.content'; }        # Write
hi_new_string()   { hi_field '.tool_input.new_string'; }     # Edit
hi_new_source()   { hi_field '.tool_input.new_source'; }     # NotebookEdit
hi_notebook_path() { hi_field '.tool_input.notebook_path'; } # NotebookEdit

# SendMessage shortcuts (canonical names per Phase 1.7)
hi_msg_to()       { hi_field '.tool_input.to'; }
hi_msg_summary()  { hi_field '.tool_input.summary'; }
hi_msg_body()     { hi_field '.tool_input.message'; }

# Must stay the last statement: reaching it proves the whole file parsed.
HI_LOADED=1
