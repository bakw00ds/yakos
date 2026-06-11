#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: supervise.sh — manage the live shadow-agent supervisor.
#
# Subcommands:
#   enable [<project>]    Set supervisor.enabled: true in .yakos.yml
#   disable [<project>]   Set supervisor.enabled: false
#   status [<project>]    Show config + buffer + recent findings
#   tail [<project>] [--watch] [--n <N>]   Show recent findings
#   clear [<project>]     Wipe buffer + findings + counter (keeps config)

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos supervise'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

usage() {
    cat <<'EOF'
yakos supervise <subcommand> [args...]

Manage the live shadow-agent supervisor (v0.33+).

Subcommands:
  enable [<project>]          Flip .yakos.yml supervisor.enabled to true.
  disable [<project>]         Flip to false.
  set <key> <value> [<proj>]  Set a single supervisor config key in .yakos.yml.
                              Common keys: block_on_critical, score_every_n_calls,
                              runtime, agent, model, matcher. (supervisor-toggle skill.)
  status [<project>]          Config snapshot + buffer + recent findings count.
  tail [<project>] [--watch]  Print recent findings (--watch follows).
  clear [<project>]           Wipe buffer + findings + counter (keeps config).
  pending [<project>]         List unacknowledged surface_to_operator+ findings.
  ack <finding-id> [<proj>] [--note "..."]
                              Acknowledge a pending escalation finding.
  ack-all [<project>] [--note "..."]
                              Acknowledge all currently-pending escalation findings.

Project resolution: arg if given, else inferred from cwd (matches
'yakos start' / 'yakos peer status').

Emergency session bypass (no edit to .yakos.yml needed):
  export YAKOS_SUPERVISOR_DISABLE=1

Acknowledgement state:
  ~/.yakos-state/supervisor-acks.ndjson — append-only; one ack record per line.
  Finding IDs: f-<ts-safe>-<project>-<linenum>
  Format: {"ts":"<ISO>","finding_id":"<id>","project":"<name>","user":"<user>","note":"<...>"}

See docs/supervisor-mode.md for the full guide.
EOF
}

infer_project() {
    local cwd_real ac_root
    cwd_real="$(ct_realpath "$PWD")"
    ac_root="$HOME/agent-control"
    case "$cwd_real" in
        "$ac_root"/*)
            local rest="${cwd_real#$ac_root/}"
            printf '%s\n' "${rest%%/*}"
            return 0
            ;;
    esac
    if [ -d "$ac_root" ]; then
        for cd_path in "$ac_root"/*/.project-path; do
            [ -f "$cd_path" ] || continue
            local p pr
            p="$(head -1 "$cd_path")"
            pr="$(ct_realpath "$p" 2>/dev/null || true)"
            case "$cwd_real" in
                "$pr"|"$pr"/*)
                    basename -- "$(dirname -- "$cd_path")"
                    return 0
                    ;;
            esac
        done
    fi
    return 1
}

resolve_project() {
    local arg="${1:-}"
    if [ -n "$arg" ]; then
        printf '%s\n' "$arg"
        return 0
    fi
    local p
    p="$(infer_project || true)"
    [ -n "$p" ] || ct_die "supervise: could not infer project from cwd; pass <name> explicitly"
    printf '%s\n' "$p"
}

resolve_project_paths() {
    local proj="$1"
    cd_path="$HOME/agent-control/$proj/.project-path"
    [ -f "$cd_path" ] || ct_die "supervise: $cd_path missing; run 'yakos init $proj --project <repo>' first"
    project_repo="$(head -1 "$cd_path")"
    [ -d "$project_repo" ] || ct_die "supervise: project repo $project_repo not present on this host"
    yakos_yml="$project_repo/.yakos.yml"
    work_current="$HOME/agent-control/$proj/work/current"
    buffer="$work_current/supervisor-buffer.ndjson"
    findings="$work_current/supervisor-findings.ndjson"
    counter="$work_current/.supervisor-counter"
}

# Awk helper to flip supervisor.enabled. If the supervisor: block is
# present but commented, we uncomment + set enabled. If the block is
# absent, we append a fresh block.
flip_supervisor_enabled() {
    local target_state="$1"   # true | false
    [ -f "$yakos_yml" ] || ct_die "supervise: $yakos_yml missing; run 'yakos init' first"

    # If already in the desired state, no-op
    if grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -q "^[[:space:]]*enabled:[[:space:]]*${target_state}[[:space:]]*$"; then
        echo "supervisor.enabled is already '$target_state' in $yakos_yml — no change"
        return 0
    fi

    local tmp="$yakos_yml.tmp.$$"
    if grep -q '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null; then
        # Existing block — toggle the enabled line
        awk -v st="$target_state" '
            BEGIN { in_super=0; replaced=0 }
            /^[[:space:]]*supervisor:[[:space:]]*$/ { in_super=1; print; next }
            in_super && /^[[:space:]]*enabled:/ {
                sub(/enabled:.*/, "enabled: " st)
                replaced=1
                print
                in_super=0
                next
            }
            in_super && /^[^[:space:]#]/ { in_super=0 }
            { print }
            END { if (!replaced) exit 2 }
        ' "$yakos_yml" > "$tmp"
        local rc=$?
        if [ "$rc" -eq 2 ]; then
            # Block exists but no enabled line — append one inside the block
            rm -f "$tmp"
            awk -v st="$target_state" '
                /^[[:space:]]*supervisor:[[:space:]]*$/ {
                    print
                    print "  enabled: " st
                    next
                }
                { print }
            ' "$yakos_yml" > "$tmp"
        fi
        mv "$tmp" "$yakos_yml"
    else
        # No supervisor block — append a fresh one
        {
            cat "$yakos_yml"
            printf '\n# Added by `yakos supervise %s` on %s\n' \
                "$([ "$target_state" = "true" ] && echo enable || echo disable)" \
                "$(ct_iso_now_z)"
            printf 'supervisor:\n'
            printf '  enabled: %s\n' "$target_state"
            printf '  runtime: claude\n'
            printf '  agent: supervisor\n'
            printf '  score_every_n_calls: 10\n'
            printf '  block_on_critical: true\n'
        } > "$tmp"
        mv "$tmp" "$yakos_yml"
    fi

    echo "set supervisor.enabled: $target_state in $yakos_yml"
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    enable|disable|status|tail|clear|set|pending|ack|ack-all) ;;
    *) ct_die "supervise: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- enable / disable -----------------------------------------------------

if [ "$SUB" = "enable" ] || [ "$SUB" = "disable" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    resolve_project_paths "$PROJECT"
    target="true"
    [ "$SUB" = "disable" ] && target="false"
    flip_supervisor_enabled "$target"

    # Touch the .yakos.yml for the lead — it's read at session start
    if [ "$target" = "true" ]; then
        cat <<EOF

Supervisor enabled. Next session in $PROJECT will:
  - Stream every tool call to $buffer
  - Every 10 lead tool calls, fork a supervisor dispatch (on 'claude'
    runtime by default — edit .yakos.yml to change)
  - The supervisor scores and writes findings to:
      $findings
  - supervisor-gate.sh reads findings and blocks lead on CRITICAL
    (block_on_critical: true; set to false for surface-only mode)

Emergency disable for one session: export YAKOS_SUPERVISOR_DISABLE=1
See: docs/supervisor-mode.md
EOF
    fi
    exit 0
fi

# ---- status ----------------------------------------------------------------

if [ "$SUB" = "status" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    resolve_project_paths "$PROJECT"

    echo "yakos supervise status — project: $PROJECT"
    echo "  .yakos.yml:  $yakos_yml"

    if [ ! -f "$yakos_yml" ]; then
        echo "  config:      (missing; run 'yakos init')"
    else
        if grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
            | grep -q '^[[:space:]]*enabled:[[:space:]]*true[[:space:]]*$'; then
            echo "  enabled:     YES"
        else
            echo "  enabled:     no"
        fi
        # Surface the runtime + block setting
        for key in runtime agent score_every_n_calls block_on_critical model matcher; do
            v="$(grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
                | grep -E "^[[:space:]]*$key:[[:space:]]*" \
                | head -1 | awk -F: '{print $2}' | xargs)"
            [ -n "$v" ] && printf '  %-20s %s\n' "$key:" "$v"
        done
    fi

    if [ "${YAKOS_SUPERVISOR_DISABLE:-0}" = "1" ]; then
        echo "  env override:  YAKOS_SUPERVISOR_DISABLE=1 (currently DISABLED in this shell)"
    fi

    echo ""
    echo "  buffer:      $buffer"
    if [ -f "$buffer" ]; then
        echo "    entries:   $(wc -l < "$buffer" | tr -d ' ')"
        echo "    last ts:   $(tail -n 1 "$buffer" | jq -r '.ts // "-"' 2>/dev/null)"
    else
        echo "    (no entries yet — start a session)"
    fi

    echo ""
    echo "  counter:     $counter"
    if [ -f "$counter" ]; then
        echo "    value:     $(cat "$counter" 2>/dev/null)"
    else
        echo "    (not initialized — supervisor hasn't fired yet)"
    fi

    echo ""
    echo "  findings:    $findings"
    if [ -f "$findings" ]; then
        n="$(wc -l < "$findings" | tr -d ' ')"
        echo "    count:     $n"
        if [ "$n" -gt 0 ]; then
            echo "    most recent:"
            tail -n 1 "$findings" | jq -C . 2>/dev/null | sed 's/^/      /' || tail -n 1 "$findings" | sed 's/^/      /'
        fi
    else
        echo "    (no findings yet)"
    fi
    exit 0
fi

# ---- tail ------------------------------------------------------------------

if [ "$SUB" = "tail" ]; then
    WATCH=0
    N=10
    PROJECT=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --watch|-w) WATCH=1 ;;
            --n) shift; N="${1:-10}" ;;
            --n=*) N="${1#--n=}" ;;
            -*) ct_die "supervise tail: unknown flag '$1'" ;;
            *)
                [ -z "$PROJECT" ] && PROJECT="$1" || ct_die "supervise tail: too many positional args"
                ;;
        esac
        shift
    done
    PROJECT="$(resolve_project "$PROJECT")"
    resolve_project_paths "$PROJECT"

    if [ ! -f "$findings" ]; then
        echo "supervise tail: no findings yet at $findings"
        exit 0
    fi

    if [ "$WATCH" = "1" ]; then
        echo "yakos supervise tail --watch — Ctrl+C to exit"
        tail -n "$N" -f "$findings" | while IFS= read -r line; do
            printf '%s\n' "$line" | jq -C 'select(. != null)' 2>/dev/null || printf '%s\n' "$line"
            echo "---"
        done
    else
        tail -n "$N" "$findings" | while IFS= read -r line; do
            printf '%s\n' "$line" | jq -C 'select(. != null)' 2>/dev/null || printf '%s\n' "$line"
            echo "---"
        done
    fi
    exit 0
fi

# ---- set <key> <value> -----------------------------------------------------

if [ "$SUB" = "set" ]; then
    [ "$#" -ge 2 ] || ct_die "supervise set: <key> <value> required (e.g. block_on_critical false)"
    KEY="$1"
    VAL="$2"
    PROJECT="$(resolve_project "${3:-}")"
    resolve_project_paths "$PROJECT"

    # Validate the key is one we know about
    case "$KEY" in
        enabled|runtime|agent|score_every_n_calls|block_on_critical|model|matcher) ;;
        *) ct_die "supervise set: unknown key '$KEY' (allowed: enabled / runtime / agent / score_every_n_calls / block_on_critical / model / matcher)" ;;
    esac

    # Validate boolean values for bool keys
    case "$KEY" in
        enabled|block_on_critical)
            case "$VAL" in
                true|false) ;;
                *) ct_die "supervise set $KEY: value must be 'true' or 'false' (got '$VAL')" ;;
            esac
            ;;
        score_every_n_calls)
            case "$VAL" in
                ''|*[!0-9]*) ct_die "supervise set $KEY: value must be a positive integer (got '$VAL')" ;;
            esac
            ;;
        model)
            case "$VAL" in
                haiku|sonnet|opus|fable) ;;
                *) ct_die "supervise set model: value must be haiku, sonnet, opus, or fable (got '$VAL')" ;;
            esac
            ;;
    esac

    [ -f "$yakos_yml" ] || ct_die "supervise set: $yakos_yml missing; run 'yakos init' first"

    # No-op if already at target
    if grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -q "^[[:space:]]*$KEY:[[:space:]]*${VAL}[[:space:]]*$"; then
        echo "supervisor.$KEY is already '$VAL' in $yakos_yml — no change"
        exit 0
    fi

    tmp="$yakos_yml.tmp.$$"
    if grep -q '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null; then
        # Block exists — replace or add the specific key
        awk -v key="$KEY" -v val="$VAL" '
            BEGIN { in_super=0; replaced=0 }
            /^[[:space:]]*supervisor:[[:space:]]*$/ { in_super=1; print; next }
            in_super && $0 ~ "^[[:space:]]*"key":" {
                sub(key":.*", key": " val)
                replaced=1
                print
                next
            }
            in_super && /^[^[:space:]#]/ {
                if (!replaced) { print "  " key ": " val; replaced=1 }
                in_super=0
            }
            { print }
            END { if (in_super && !replaced) { print "  " key ": " val } }
        ' "$yakos_yml" > "$tmp"
        mv "$tmp" "$yakos_yml"
    else
        # No supervisor block — append a fresh one with just this key
        {
            cat "$yakos_yml"
            printf '\n# Added by `yakos supervise set` on %s\nsupervisor:\n  %s: %s\n' \
                "$(ct_iso_now_z)" "$KEY" "$VAL"
        } > "$tmp"
        mv "$tmp" "$yakos_yml"
    fi

    echo "set supervisor.$KEY: $VAL in $yakos_yml"
    exit 0
fi

# ---- clear -----------------------------------------------------------------

if [ "$SUB" = "clear" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    resolve_project_paths "$PROJECT"
    removed=0
    for f in "$buffer" "$findings" "$counter" "$work_current/.supervisor-gate-last-surfaced"; do
        [ -f "$f" ] && rm -f "$f" && removed=$((removed + 1))
    done
    echo "supervise clear: removed $removed file(s) for $PROJECT (config in .yakos.yml preserved)"
    exit 0
fi

# ---- shared ack helpers ---------------------------------------------------

YAKOS_STATE_DIR="${HOME}/.yakos-state"
ACK_FILE="${YAKOS_STATE_DIR}/supervisor-acks.ndjson"

# Finding ID derivation — must match supervisor-ack-gate.sh exactly:
#   f-<ts-colons-as-hyphens>-<project>-<linenum>
derive_finding_id() {
    local raw_ts="$1" proj="$2" linenum="$3"
    local safe_ts="${raw_ts//:/-}"
    printf 'f-%s-%s-%s' "$safe_ts" "$proj" "$linenum"
}

# List all pending findings for the given project (findings file path, project name).
# Output: tab-separated lines: <finding_id>\t<recommended_action>\t<rationale>
list_pending_findings() {
    local findings_file="$1" proj="$2"

    [ -f "$findings_file" ] || return 0

    # Build acknowledged set for this project
    local acks_set=""
    if [ -f "$ACK_FILE" ] && [ -s "$ACK_FILE" ]; then
        acks_set="$(jq -r --arg p "$proj" \
            'select(.project == $p) | .finding_id // empty' \
            "$ACK_FILE" 2>/dev/null || true)"
    fi

    local linenum=0
    while IFS= read -r line; do
        linenum=$((linenum + 1))
        [ -n "$line" ] || continue
        if ! printf '%s' "$line" | jq empty 2>/dev/null; then
            continue
        fi

        local recommended
        recommended="$(printf '%s' "$line" | jq -r '.recommended_action // "continue"' 2>/dev/null || true)"
        case "$recommended" in
            surface_to_operator|block_next_tool|halt) ;;
            *) continue ;;
        esac

        local raw_ts
        raw_ts="$(printf '%s' "$line" | jq -r '.ts // ""' 2>/dev/null || true)"
        local fid
        fid="$(derive_finding_id "$raw_ts" "$proj" "$linenum")"

        # Check if acked
        case "$acks_set" in
            "$fid"|"$fid"$'\n'*|*$'\n'"$fid"|*$'\n'"$fid"$'\n'*) continue ;;
        esac

        local rationale
        rationale="$(printf '%s' "$line" | jq -r '.rationale // "(no rationale)"' 2>/dev/null || true)"
        printf '%s\t%s\t%s\n' "$fid" "$recommended" "$rationale"
    done < "$findings_file"
}

write_ack() {
    local fid="$1" proj="$2" note="$3"
    mkdir -p "$YAKOS_STATE_DIR"
    local user="${USER:-$(id -un 2>/dev/null || echo unknown)}"
    local ts
    ts="$(ct_iso_now_z)"
    jq -nc \
        --arg ts "$ts" \
        --arg finding_id "$fid" \
        --arg project "$proj" \
        --arg user "$user" \
        --arg note "$note" \
        '{ts: $ts, finding_id: $finding_id, project: $project, user: $user, note: $note}' \
        >> "$ACK_FILE"
}

# ---- pending ---------------------------------------------------------------

if [ "$SUB" = "pending" ]; then
    PROJECT="$(resolve_project "${1:-}")"
    resolve_project_paths "$PROJECT"

    command -v jq >/dev/null 2>&1 || ct_die "supervise pending: jq is required"

    if [ ! -f "$findings" ]; then
        echo "supervise pending: no findings file at $findings"
        exit 0
    fi

    echo "yakos supervise pending — project: $PROJECT"
    echo ""

    pending_output="$(list_pending_findings "$findings" "$PROJECT")"
    if [ -z "$pending_output" ]; then
        echo "  No unacknowledged escalation findings."
        exit 0
    fi

    echo "  Unacknowledged escalation findings (surface_to_operator+):"
    echo ""
    while IFS=$'\t' read -r fid action rationale; do
        printf '  [%s]\n' "$fid"
        printf '    action:   %s\n' "$action"
        printf '    rationale: %s\n' "$rationale"
        echo ""
    done <<< "$pending_output"

    echo "Acknowledge with:"
    first_id="$(printf '%s' "$pending_output" | head -n 1 | cut -f1)"
    echo "  yakos supervise ack ${first_id} [--note \"...\"]"
    echo "  yakos supervise ack-all [--note \"...\"]"
    exit 0
fi

# ---- ack -------------------------------------------------------------------

if [ "$SUB" = "ack" ]; then
    command -v jq >/dev/null 2>&1 || ct_die "supervise ack: jq is required"

    FINDING_ID="${1:-}"
    [ -n "$FINDING_ID" ] || ct_die "supervise ack: <finding-id> required (try 'yakos supervise pending')"
    shift

    NOTE=""
    PROJECT_ARG=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --note)
                shift
                NOTE="${1:-}"
                shift
                ;;
            --note=*)
                NOTE="${1#--note=}"
                shift
                ;;
            -*)
                ct_die "supervise ack: unknown flag '$1'"
                ;;
            *)
                [ -z "$PROJECT_ARG" ] && PROJECT_ARG="$1" || ct_die "supervise ack: too many positional args"
                shift
                ;;
        esac
    done

    PROJECT="$(resolve_project "$PROJECT_ARG")"
    resolve_project_paths "$PROJECT"

    if [ ! -f "$findings" ]; then
        ct_die "supervise ack: no findings file at $findings"
    fi

    # Verify the finding_id refers to an actual escalation finding in this project
    pending_output="$(list_pending_findings "$findings" "$PROJECT")"
    if [ -z "$pending_output" ]; then
        echo "supervise ack: no pending escalation findings for $PROJECT — nothing to acknowledge"
        exit 0
    fi

    # Check that the given id is in the pending list OR already acked
    found_in_pending=0
    while IFS=$'\t' read -r fid _action _rationale; do
        if [ "$fid" = "$FINDING_ID" ]; then
            found_in_pending=1
            break
        fi
    done <<< "$pending_output"

    if [ "$found_in_pending" -eq 0 ]; then
        # Check if it's already acked
        already_acked=0
        if [ -f "$ACK_FILE" ]; then
            if jq -e --arg fid "$FINDING_ID" --arg proj "$PROJECT" \
                'select(.finding_id == $fid and .project == $proj)' \
                "$ACK_FILE" 2>/dev/null | grep -q .; then
                already_acked=1
            fi
        fi
        if [ "$already_acked" -eq 1 ]; then
            echo "supervise ack: $FINDING_ID is already acknowledged for $PROJECT"
            exit 0
        fi
        ct_die "supervise ack: finding '$FINDING_ID' not found in pending escalations for $PROJECT (run 'yakos supervise pending' to list)"
    fi

    write_ack "$FINDING_ID" "$PROJECT" "$NOTE"
    echo "Acknowledged: $FINDING_ID (project: $PROJECT)"
    [ -n "$NOTE" ] && echo "  note: $NOTE"
    echo "  ack record appended to: $ACK_FILE"
    exit 0
fi

# ---- ack-all ---------------------------------------------------------------

if [ "$SUB" = "ack-all" ]; then
    command -v jq >/dev/null 2>&1 || ct_die "supervise ack-all: jq is required"

    NOTE=""
    PROJECT_ARG=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --note)
                shift
                NOTE="${1:-}"
                shift
                ;;
            --note=*)
                NOTE="${1#--note=}"
                shift
                ;;
            -*)
                ct_die "supervise ack-all: unknown flag '$1'"
                ;;
            *)
                [ -z "$PROJECT_ARG" ] && PROJECT_ARG="$1" || ct_die "supervise ack-all: too many positional args"
                shift
                ;;
        esac
    done

    PROJECT="$(resolve_project "$PROJECT_ARG")"
    resolve_project_paths "$PROJECT"

    if [ ! -f "$findings" ]; then
        echo "supervise ack-all: no findings file at $findings — nothing to acknowledge"
        exit 0
    fi

    pending_output="$(list_pending_findings "$findings" "$PROJECT")"
    if [ -z "$pending_output" ]; then
        echo "supervise ack-all: no pending escalation findings for $PROJECT"
        exit 0
    fi

    acked_count=0
    while IFS=$'\t' read -r fid _action _rationale; do
        [ -n "$fid" ] || continue
        write_ack "$fid" "$PROJECT" "$NOTE"
        echo "  Acknowledged: $fid"
        acked_count=$((acked_count + 1))
    done <<< "$pending_output"

    echo ""
    echo "ack-all: acknowledged $acked_count finding(s) for $PROJECT"
    [ -n "$NOTE" ] && echo "  note: $NOTE"
    echo "  ack records appended to: $ACK_FILE"
    exit 0
fi

ct_die "supervise: internal — fell through dispatch (subcommand '$SUB')"
