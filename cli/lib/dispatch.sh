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

# Override > frontmatter > default
if [ -n "$RUNTIME_OVERRIDE" ]; then
    RUNTIME="$RUNTIME_OVERRIDE"
elif [ -n "$AGENT_RUNTIME" ]; then
    RUNTIME="$AGENT_RUNTIME"
else
    RUNTIME="$(yk_rt_default)"
fi

yk_rt_is_known "$RUNTIME" || ct_die "dispatch: unknown runtime '$RUNTIME' (known: $(yk_rt_known | tr '\n' ' '))"
yk_rt_load "$RUNTIME"

# ---- preflight + audit ------------------------------------------------------

yk_rt_check_cli || ct_die "dispatch: '$RUNTIME' CLI not on PATH; install or pick a different --runtime"
yk_rt_check_auth || ct_log "WARN: '$RUNTIME' auth not detected; the call may fail"

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

# Print a one-line preflight so callers know what's running.
echo "yakos dispatch: agent=$AGENT_NAME runtime=$RUNTIME project=$PROJECT" >&2

set +e
out_tmp="$(mktemp -t yakos-dispatch.XXXXXX)"
ct_timeout "$TIMEOUT" yk_rt_dispatch "$PROJECT" "$AGENT_NAME" "$TASK" >"$out_tmp" 2>&1
rc=$?
set -e

cat "$out_tmp"
rm -f "$out_tmp" 2>/dev/null || true

ts_end="$(ct_iso_now_z)"
event_end="$(jq -cn \
    --arg t "$ts_end" \
    --arg agent "$AGENT_NAME" \
    --arg runtime "$RUNTIME" \
    --argjson rc "$rc" \
    '{type:"dispatch_finished", ts:$t, agent:$agent, runtime:$runtime, exit_code:$rc}')"
printf '%s\n' "$event_end" >> "$DISPATCH_LOG" 2>/dev/null || true

exit "$rc"
