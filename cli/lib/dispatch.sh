#!/usr/bin/env bash
# dispatch.sh — one-shot cross-runtime agent dispatch.
#
# Purpose: spawn a yakOS agent on the runtime its frontmatter declares
# (or a runtime override), pass the task as a prompt, capture and
# return the agent's output. Designed to be called from a session
# lead via the Bash tool — the lead doesn't care which runtime the
# specialist runs on.
#
# Usage:
#     yakos dispatch <agent-name> "<task-prompt>" [--runtime <id>]
#                                                  [--project <path>]
#                                                  [--timeout <seconds>]
#
# The agent's frontmatter `runtime:` field selects the runtime. CLI
# flag overrides the frontmatter. Default if neither is set: claude.
#
# Examples (called from a lead's Bash tool):
#     yakos dispatch backend "implement /v1/users GET endpoint"
#     yakos dispatch security-reviewer "review the auth middleware"
#     yakos dispatch frontend "..." --runtime gemini

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos dispatch'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos dispatch'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

usage() {
    cat <<'EOF'
yakos dispatch <agent-name> "<task-prompt>" [flags]

Spawn a yakOS agent on the runtime its frontmatter declares (or a
runtime override). One-shot, non-interactive — captures stdout and
returns the runtime's exit code.

Arguments:
  <agent-name>      The agent's id (e.g. backend, security-reviewer,
                    pandaos-database). Must exist in the composed agent
                    set for the project.
  <task-prompt>     The work to do. Quoted so the lead can pass a
                    multi-line description.

Flags:
  --runtime <id>    Override the agent's frontmatter `runtime:` field.
  --project <path>  Project repo path. Defaults to inferring from cwd
                    (matches `yakos start`'s inference).
  --timeout <secs>  Max time to wait. Default 600s.

Audit trail at ~/.yakos-state/dispatch-log.ndjson.

Examples:
  yakos dispatch backend "implement the /v1/meal-plans GET handler"
  yakos dispatch troubleshooter "diagnose why login_test fails on CI" --runtime codex
EOF
}

AGENT_NAME=""
TASK=""
RUNTIME_OVERRIDE=""
PROJECT=""
TIMEOUT=600

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --runtime)
            shift
            [ "$#" -gt 0 ] || ct_die "dispatch: --runtime requires an id"
            RUNTIME_OVERRIDE="$1"
            ;;
        --runtime=*) RUNTIME_OVERRIDE="${1#--runtime=}" ;;
        --project)
            shift
            [ "$#" -gt 0 ] || ct_die "dispatch: --project requires a path"
            PROJECT="$1"
            ;;
        --project=*) PROJECT="${1#--project=}" ;;
        --timeout)
            shift
            [ "$#" -gt 0 ] || ct_die "dispatch: --timeout requires a number"
            TIMEOUT="$1"
            ;;
        --timeout=*) TIMEOUT="${1#--timeout=}" ;;
        --*) ct_die "dispatch: unknown flag '$1'" ;;
        *)
            if [ -z "$AGENT_NAME" ]; then
                AGENT_NAME="$1"
            elif [ -z "$TASK" ]; then
                TASK="$1"
            else
                ct_die "dispatch: too many positional args (use --help)"
            fi
            ;;
    esac
    shift
done

[ -n "$AGENT_NAME" ] || { usage >&2; ct_die "dispatch: missing <agent-name>"; }
[ -n "$TASK" ] || { usage >&2; ct_die "dispatch: missing <task-prompt>"; }

# ---- resolve project --------------------------------------------------------

if [ -z "$PROJECT" ]; then
    cwd_real="$(ct_realpath "$PWD")"
    ac_root="$HOME/agent-control"
    case "$cwd_real" in
        "$ac_root"/*)
            rest="${cwd_real#$ac_root/}"
            name="${rest%%/*}"
            project_path_file="$ac_root/$name/.project-path"
            [ -f "$project_path_file" ] && PROJECT="$(head -1 "$project_path_file")"
            ;;
        *)
            if [ -d "$ac_root" ]; then
                for cd_path in "$ac_root"/*/.project-path; do
                    [ -f "$cd_path" ] || continue
                    p="$(head -1 "$cd_path" 2>/dev/null)"
                    p_real="$(ct_realpath "$p" 2>/dev/null || true)"
                    case "$cwd_real" in
                        "$p_real"|"$p_real"/*) PROJECT="$p_real"; break ;;
                    esac
                done
            fi
            ;;
    esac
fi

[ -n "$PROJECT" ] || ct_die "dispatch: cannot infer project; pass --project <path>"
[ -d "$PROJECT" ] || ct_die "dispatch: project path not found: $PROJECT"

# ---- find the agent + read its runtime: frontmatter ------------------------

# Search both lib/agents/ and <project>/.claude/agents/. Project agents
# override framework agents on id collision (matches agents-compose).
find_agent_file() {
    local id="$1"
    local proj_file="$PROJECT/.claude/agents/${id}.md"
    [ -f "$proj_file" ] && { printf '%s\n' "$proj_file"; return 0; }
    # Also search by frontmatter id (filename ≠ id case).
    local d f fm fm_id
    for d in "$PROJECT/.claude/agents" "$YAKOS_ROOT/lib/agents"; do
        [ -d "$d" ] || continue
        for f in "$d"/*.md; do
            [ -f "$f" ] || continue
            case "$(basename -- "$f")" in README.md|lead-template.md) continue ;; esac
            fm="$(yk_agents_extract_frontmatter "$f")"
            fm_id="$(yk_agents_fm_get "$fm" "id")"
            if [ "$fm_id" = "$id" ] || [ "${f%.md}" = "$d/$id" ]; then
                printf '%s\n' "$f"
                return 0
            fi
        done
    done
    return 1
}

AGENT_FILE="$(find_agent_file "$AGENT_NAME" || true)"
[ -n "$AGENT_FILE" ] || ct_die "dispatch: agent '$AGENT_NAME' not found in $PROJECT/.claude/agents/ or $YAKOS_ROOT/lib/agents/"

FM="$(yk_agents_extract_frontmatter "$AGENT_FILE")"
AGENT_RUNTIME="$(yk_agents_fm_get "$FM" "runtime")"
AGENT_FALLBACK="$(yk_agents_fm_list "$FM" "runtime-fallback" || true)"

# Build the runtime preference chain.
# Override > frontmatter `runtime:` > yakos default → then frontmatter
# `runtime-fallback:` list, in order.
RUNTIME_CHAIN=""
if [ -n "$RUNTIME_OVERRIDE" ]; then
    RUNTIME_CHAIN="$RUNTIME_OVERRIDE"
elif [ -n "$AGENT_RUNTIME" ]; then
    RUNTIME_CHAIN="$AGENT_RUNTIME"
else
    RUNTIME_CHAIN="$(yk_rt_default)"
fi
if [ -n "$AGENT_FALLBACK" ]; then
    RUNTIME_CHAIN="$RUNTIME_CHAIN
$AGENT_FALLBACK"
fi

# Resolve the chain: pick the first runtime where check_cli + check_auth
# both succeed. Surface a soft warning on each fallback step so the
# operator knows why a non-preferred runtime was used.
RUNTIME=""
TRIED_LIST=""
while IFS= read -r candidate; do
    [ -n "$candidate" ] || continue
    yk_rt_is_known "$candidate" || { ct_log "dispatch: unknown runtime in chain: '$candidate'; skipping"; continue; }
    yk_rt_load "$candidate"
    if yk_rt_check_cli 2>/dev/null && yk_rt_check_auth 2>/dev/null; then
        RUNTIME="$candidate"
        break
    fi
    TRIED_LIST="${TRIED_LIST}${TRIED_LIST:+, }$candidate"
done <<< "$RUNTIME_CHAIN"

if [ -z "$RUNTIME" ]; then
    ct_die "dispatch: no runtime in chain is available + authed (tried: $TRIED_LIST)"
fi

if [ -n "$TRIED_LIST" ]; then
    ct_log "dispatch: preferred runtime(s) unavailable [$TRIED_LIST]; falling back to '$RUNTIME'"
fi

ts_start="$(ct_iso_now_z)"

# ---- audit log -------------------------------------------------------------

mkdir -p "$HOME/.yakos-state" 2>/dev/null || true
DISPATCH_LOG="$HOME/.yakos-state/dispatch-log.ndjson"
event_start="$(jq -cn \
    --arg t "$ts_start" \
    --arg agent "$AGENT_NAME" \
    --arg runtime "$RUNTIME" \
    --arg project "$PROJECT" \
    --arg task "$(printf '%s' "$TASK" | head -c 200)" \
    '{type:"dispatch_started", ts:$t, agent:$agent, runtime:$runtime, project:$project, task_preview:$task}')"
printf '%s\n' "$event_start" >> "$DISPATCH_LOG" 2>/dev/null || true

# ---- run the dispatch ------------------------------------------------------

echo "yakos dispatch: agent=$AGENT_NAME runtime=$RUNTIME project=$PROJECT" >&2

# Capture stdout to a tempfile, time the call. Wall-clock + output-byte
# size give a rough proxy for cost without runtime-specific stream-json
# parsing (proper token telemetry is v0.6+ per docs/telemetry.md).
out_tmp="$(mktemp -t yakos-dispatch.XXXXXX)"
epoch_start="$(ct_iso_to_epoch "$ts_start" 2>/dev/null || date +%s)"

set +e
ct_timeout "$TIMEOUT" yk_rt_dispatch "$PROJECT" "$AGENT_NAME" "$TASK" >"$out_tmp" 2>&1
rc=$?
set -e

epoch_end="$(date +%s)"
duration_s=$(( epoch_end - epoch_start ))
out_bytes="$(wc -c < "$out_tmp" 2>/dev/null | tr -d ' ' || echo 0)"
task_bytes="$(printf '%s' "$TASK" | wc -c | tr -d ' ')"
# Rough token estimate: ~4 chars/token for English. Used in cost reports.
est_input_tokens=$(( task_bytes / 4 ))
est_output_tokens=$(( out_bytes / 4 ))

cat "$out_tmp"
rm -f "$out_tmp" 2>/dev/null || true

ts_end="$(ct_iso_now_z)"
event_end="$(jq -cn \
    --arg t "$ts_end" \
    --arg agent "$AGENT_NAME" \
    --arg runtime "$RUNTIME" \
    --argjson rc "$rc" \
    --argjson dur "$duration_s" \
    --argjson out_b "$out_bytes" \
    --argjson task_b "$task_bytes" \
    --argjson in_tok "$est_input_tokens" \
    --argjson out_tok "$est_output_tokens" \
    '{type:"dispatch_finished", ts:$t, agent:$agent, runtime:$runtime,
      exit_code:$rc, duration_s:$dur,
      output_bytes:$out_b, task_bytes:$task_b,
      est_input_tokens:$in_tok, est_output_tokens:$out_tok}')"
printf '%s\n' "$event_end" >> "$DISPATCH_LOG" 2>/dev/null || true

exit "$rc"
