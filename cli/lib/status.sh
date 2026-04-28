#!/usr/bin/env bash
# status.sh — per-project dashboard.
#
# Reports session age (.session-started-history), scratchpad size,
# recent hook outcomes (logs/*.ndjson), bypass count, mailbox count,
# MEMORY.md presence. Soft warnings on session age >4h or scratchpad >100MB.

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos status'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

PROJECT=""
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos status <project>

Print a dashboard for a project under ~/agent-control/. Reports session
age, scratchpad size, recent hook outcomes, bypass count, mailbox
message count, and MEMORY.md state. Surfaces soft warnings on
session age >4h or scratchpad >100MB.

Usage: yakos status <project>
EOF
            exit 0
            ;;
        --*) ct_die "status: unknown flag '$arg'" ;;
        *)
            if [ -z "$PROJECT" ]; then PROJECT="$arg"
            else ct_die "status: too many positional args"
            fi
            ;;
    esac
done

[ -n "$PROJECT" ] || ct_die "status: missing <project> (try --help)"

CONTROL_DIR="$HOME/agent-control/$PROJECT"
[ -d "$CONTROL_DIR" ] || ct_die "status: project '$PROJECT' not found at $CONTROL_DIR"

WORK_DIR="$CONTROL_DIR/work"
CURRENT_DIR="$WORK_DIR/current"
ARCHIVE_DIR="$WORK_DIR/archive"

# ---- session age ------------------------------------------------------------

SESSION_HISTORY="$CURRENT_DIR/.session-started-history"
session_started=""
session_age_label="not started"
session_warning=""
if [ -f "$SESSION_HISTORY" ]; then
    # Take the last entry's "started_at" if any
    last_started="$(jq -r 'if type=="array" and length>0 then (.[length-1].started_at // empty) else empty end' "$SESSION_HISTORY" 2>/dev/null || true)"
    if [ -n "$last_started" ]; then
        session_started="$last_started"
        # Age in seconds via jq
        age_s="$(printf '%s' "$last_started" | jq -Rr 'try (now - fromdateiso8601 | floor | tostring) catch "?"')"
        case "$age_s" in
            ?|"") session_age_label="started $last_started (age unknown)" ;;
            *)
                if [ "$age_s" -lt 60 ]; then
                    session_age_label="started ${last_started} (${age_s}s ago)"
                elif [ "$age_s" -lt 3600 ]; then
                    session_age_label="started ${last_started} ($((age_s / 60))m ago)"
                else
                    h=$((age_s / 3600))
                    m=$(( (age_s % 3600) / 60 ))
                    session_age_label="started ${last_started} (${h}h ${m}m ago)"
                fi
                if [ "$age_s" -gt 14400 ]; then  # 4 hours
                    session_warning="session age >4h. consider 'yakos team restart $PROJECT' for context hygiene."
                fi
                ;;
        esac
    fi
fi

# ---- sizes ------------------------------------------------------------------

scratch_size="(empty)"
scratch_warning=""
if [ -d "$CURRENT_DIR" ]; then
    scratch_size="$(du -sh "$CURRENT_DIR" 2>/dev/null | awk '{print $1}')"
    # Check >100MB warning. du -sk gives KB.
    scratch_kb="$(du -sk "$CURRENT_DIR" 2>/dev/null | awk '{print $1}')"
    if [ "${scratch_kb:-0}" -gt 102400 ]; then
        scratch_warning="scratchpad >100MB. archive recommended."
    fi
fi

archive_size="(empty)"
if [ -d "$ARCHIVE_DIR" ] && [ -n "$(ls -A "$ARCHIVE_DIR" 2>/dev/null)" ]; then
    archive_size="$(du -sh "$ARCHIVE_DIR" 2>/dev/null | awk '{print $1}')"
fi

# ---- file ages helper -------------------------------------------------------

file_age_label() {
    local f="$1"
    [ -f "$f" ] || { printf 'absent'; return; }
    if command -v stat >/dev/null 2>&1; then
        # macOS uses -f, GNU stat uses -c
        local mtime
        mtime="$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f" 2>/dev/null || echo "")"
        if [ -n "$mtime" ]; then
            local now diff
            now="$(date +%s)"
            diff=$((now - mtime))
            if [ "$diff" -lt 60 ]; then
                printf '%ds ago' "$diff"
            elif [ "$diff" -lt 3600 ]; then
                printf '%dm ago' "$((diff / 60))"
            elif [ "$diff" -lt 86400 ]; then
                printf '%dh ago' "$((diff / 3600))"
            else
                printf '%dd ago' "$((diff / 86400))"
            fi
            return
        fi
    fi
    printf 'unknown'
}

plan_age="$(file_age_label "$CURRENT_DIR/plan.md")"
contracts_age="$(file_age_label "$CURRENT_DIR/contracts.md")"
decisions_age="$(file_age_label "$CURRENT_DIR/decisions.md")"

# Decisions stale flag — if present and >2h old
decisions_warn=""
if [ -f "$CURRENT_DIR/decisions.md" ]; then
    mtime="$(stat -f '%m' "$CURRENT_DIR/decisions.md" 2>/dev/null || stat -c '%Y' "$CURRENT_DIR/decisions.md" 2>/dev/null || echo 0)"
    now_s="$(date +%s)"
    if [ "$mtime" -gt 0 ] && [ "$((now_s - mtime))" -gt 7200 ]; then
        decisions_warn=" ⚠"
    fi
fi

# ---- hook outcomes ----------------------------------------------------------

hook_lines=""
if [ -d "$CURRENT_DIR/logs" ]; then
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        name="$(basename "$f" .ndjson)"
        # Count records by decision: pass / block / warn (or "report")
        counts="$(jq -r '.decision // "report"' "$f" 2>/dev/null | sort | uniq -c | awk '{printf("%s %s, ", $1, $2)}' | sed 's/, $//')"
        last_ts="$(tail -n 1 "$f" 2>/dev/null | jq -r '.ts // empty' 2>/dev/null || true)"
        if [ -n "$counts" ]; then
            hook_lines="${hook_lines}    ${name}: ${counts}"
            [ -n "$last_ts" ] && hook_lines="${hook_lines} (last ${last_ts})"
            hook_lines="${hook_lines}"$'\n'
        else
            hook_lines="${hook_lines}    ${name}: (empty log)"$'\n'
        fi
    done < <(find "$CURRENT_DIR/logs" -maxdepth 1 -type f -name '*.ndjson' 2>/dev/null | sort)
fi
[ -n "$hook_lines" ] || hook_lines="    (no hook logs yet)"$'\n'

# ---- bypasses ---------------------------------------------------------------

bypass_count=0
if [ -f "$CURRENT_DIR/hook-bypass.md" ]; then
    # Section-aware: only count `## bypass:` headings after `## Active entries`.
    # Skips the format example in the template's documentation block.
    bypass_count="$(awk '
        /^##[[:space:]]+Active entries[[:space:]]*$/ { active = 1; next }
        active && /^##[[:space:]]+bypass:/ { n++ }
        END { print n + 0 }
    ' "$CURRENT_DIR/hook-bypass.md" 2>/dev/null || echo 0)"
fi

# ---- mailbox messages this session ------------------------------------------

mailbox_count=0
if [ -f "$CURRENT_DIR/messages.ndjson" ]; then
    mailbox_count="$(wc -l < "$CURRENT_DIR/messages.ndjson" | tr -d ' ')"
fi

# ---- memory state -----------------------------------------------------------

memory_label="not configured"
# Look for any project memory dir whose path-decoded form starts with HOME
# The init script wrote one for this project; we don't know the project path
# from here without re-reading state — fall through cheaply by looking for
# ~/.claude/projects/* containing a MEMORY.md.
# We avoid making strong claims if we can't be sure.
mem_root="$HOME/.claude/projects"
if [ -d "$mem_root" ]; then
    n_mem="$(find "$mem_root" -mindepth 2 -maxdepth 2 -name MEMORY.md 2>/dev/null | wc -l | tr -d ' ')"
    if [ "$n_mem" -gt 0 ]; then
        memory_label="$n_mem MEMORY.md file(s) under ~/.claude/projects/"
    fi
fi

# ---- output -----------------------------------------------------------------

cat <<EOF

Project: $PROJECT
  Control:     $CONTROL_DIR
  Last team:   $session_age_label
  Scratchpad:  ${scratch_size} current/  |  ${archive_size} archive/
  Plan:        ${plan_age}
  Contracts:   ${contracts_age}
  Decisions:   ${decisions_age}${decisions_warn}
  Hooks (current session):
${hook_lines%$'\n'}
  Bypasses:    ${bypass_count} active
  Messages:    ${mailbox_count} this session
  Memory:      ${memory_label}
EOF

if [ -n "$session_warning" ]; then
    echo "  ⚠ $session_warning"
fi
if [ -n "$scratch_warning" ]; then
    echo "  ⚠ $scratch_warning"
fi
echo
