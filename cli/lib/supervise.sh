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
                              runtime, agent. (Used by the supervisor-toggle skill.)
  status [<project>]          Config snapshot + buffer + recent findings count.
  tail [<project>] [--watch]  Print recent findings (--watch follows).
  clear [<project>]           Wipe buffer + findings + counter (keeps config).

Project resolution: arg if given, else inferred from cwd (matches
'yakos start' / 'yakos peer status').

Emergency session bypass (no edit to .yakos.yml needed):
  export YAKOS_SUPERVISOR_DISABLE=1

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
        | grep -q "^[[:space:]]*enabled:[[:space:]]*$target_state[[:space:]]*$"; then
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
    enable|disable|status|tail|clear|set) ;;
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
        for key in runtime agent score_every_n_calls block_on_critical; do
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
        enabled|runtime|agent|score_every_n_calls|block_on_critical) ;;
        *) ct_die "supervise set: unknown key '$KEY' (allowed: enabled / runtime / agent / score_every_n_calls / block_on_critical)" ;;
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
    esac

    [ -f "$yakos_yml" ] || ct_die "supervise set: $yakos_yml missing; run 'yakos init' first"

    # No-op if already at target
    if grep -A 10 '^[[:space:]]*supervisor:' "$yakos_yml" 2>/dev/null \
        | grep -q "^[[:space:]]*$KEY:[[:space:]]*$VAL[[:space:]]*$"; then
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

ct_die "supervise: internal — fell through dispatch (subcommand '$SUB')"
