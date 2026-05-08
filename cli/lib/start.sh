#!/usr/bin/env bash
# start.sh — launch a Claude Code session for a yakos-bootstrapped project.
#
# Purpose: end the manual `cd ~/agent-control/<name> && claude --add-dir ...`
# pattern, default to bypassPermissions, and inject project + framework
# agents into --agents JSON so subagent_type binds at runtime
# (workaround for incident:v0.2.0-project-agent-runtime-non-discovery).
#
# Replaces the v0.2.x manual flow:
#     cd ~/agent-control/<name>
#     claude --add-dir <project> --permission-mode bypassPermissions
#
# What this command does:
#   1. Resolve <name> → control dir → project repo (.project-path).
#   2. Compose --agents JSON via cli/lib/agents-compose.sh (framework
#      + project agents). Project agents override framework on id collision.
#   3. Auto-detect <project>/.mcp.json and pass --mcp-config.
#   4. exec claude with --add-dir, --permission-mode, --agents, and any
#      pass-through flags the operator added.
#   5. Append a session_launched event to:
#        ~/agent-control/<name>/work/current/.session-started-history.ndjson
#        ~/.yakos-state/launch-log.ndjson
#
# Defaults match the operator's stated preference (bypassPermissions);
# --safe restores prompts (default permission mode). Pass-through flags
# allow --continue / --resume / --fork-session / --ide / --bare.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos start'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos start'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"

usage() {
    cat <<'EOF'
yakos start [<name>] [flags] — launch a Claude Code session for a project.

Resolves the project repo from ~/agent-control/<name>/.project-path, composes
the --agents JSON (framework + project agents), and exec's claude with
--add-dir <repo> --permission-mode bypassPermissions --agents <json>.

If <name> is omitted, infers it from the current directory:
    cwd ~ ~/agent-control/<name>/  → use <name>
    cwd is a yakos-bootstrapped project repo  → use the matching control dir

Permission mode:
    --safe                Run with --permission-mode default (prompts on).
    (default)             Run with --permission-mode bypassPermissions.

Agent injection:
    --no-agents           Skip --agents composition (debug; agents won't bind).

Session passthroughs (forwarded to `claude`):
    --continue, -c        Continue most recent conversation in the cwd.
    --resume <id>         Resume a specific session id.
    --fork-session        With --resume / --continue, fork to a new id.
    --ide                 Auto-attach to a single available IDE.
    --bare                Minimal mode (skip hooks/LSP/auto-memory etc.).
    --strict-mcp          Pass --strict-mcp-config to claude.
    --model <alias>       Override default model for the session (sonnet|opus|haiku|<full>).

Inspection:
    --dry-run             Print the resolved claude command + agent count
                          and exit 0 without launching.
    --print-agents        Print the composed --agents JSON to stdout
                          and exit 0 (useful for debugging).
    --                    End of yakos flags; everything after is forwarded
                          to claude verbatim.

Examples:
    yakos start                 # in ~/agent-control/myapp/
    yakos start myapp           # from anywhere
    yakos start myapp --safe
    yakos start myapp --continue
    yakos start myapp --dry-run
EOF
}

NAME=""
SAFE=0
NO_AGENTS=0
DRY_RUN=0
PRINT_AGENTS=0
CONTINUE=0
RESUME=""
FORK=0
IDE=0
BARE=0
STRICT_MCP=0
MODEL=""
PASSTHROUGH=()

# ---- argument parsing -------------------------------------------------------

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --safe) SAFE=1 ;;
        --no-agents) NO_AGENTS=1 ;;
        --dry-run) DRY_RUN=1 ;;
        --print-agents) PRINT_AGENTS=1 ;;
        -c|--continue) CONTINUE=1 ;;
        --resume)
            shift
            [ "$#" -gt 0 ] || ct_die "start: --resume requires a session id"
            RESUME="$1"
            ;;
        --resume=*) RESUME="${1#--resume=}" ;;
        --fork-session) FORK=1 ;;
        --ide) IDE=1 ;;
        --bare) BARE=1 ;;
        --strict-mcp) STRICT_MCP=1 ;;
        --model)
            shift
            [ "$#" -gt 0 ] || ct_die "start: --model requires an alias"
            MODEL="$1"
            ;;
        --model=*) MODEL="${1#--model=}" ;;
        --) shift; while [ "$#" -gt 0 ]; do PASSTHROUGH+=("$1"); shift; done; break ;;
        --*) ct_die "start: unknown flag '$1' (try --help)" ;;
        *)
            if [ -z "$NAME" ]; then
                NAME="$1"
            else
                ct_die "start: unexpected positional argument '$1'"
            fi
            ;;
    esac
    shift
done

# ---- name inference ---------------------------------------------------------

if [ -z "$NAME" ]; then
    cwd_real="$(ct_realpath "$PWD")"
    ac_real="$(ct_realpath "$HOME/agent-control")"
    case "$cwd_real" in
        "$ac_real"/*)
            # cwd is ~/agent-control/<name>/...; take the first path segment.
            rest="${cwd_real#$ac_real/}"
            NAME="${rest%%/*}"
            ;;
        *)
            # Try matching cwd against any control dir's .project-path.
            if [ -d "$HOME/agent-control" ]; then
                for cd_path in "$HOME/agent-control"/*/.project-path; do
                    [ -f "$cd_path" ] || continue
                    proj="$(cat "$cd_path" 2>/dev/null | head -1)"
                    proj_real="$(ct_realpath "$proj" 2>/dev/null || true)"
                    case "$cwd_real" in
                        "$proj_real"|"$proj_real"/*)
                            NAME="$(basename -- "$(dirname -- "$cd_path")")"
                            break
                            ;;
                    esac
                done
            fi
            ;;
    esac
fi

[ -n "$NAME" ] || ct_die "start: cannot infer project name from cwd; pass <name> explicitly"

case "$NAME" in
    *[!A-Za-z0-9_-]*) ct_die "start: <name> must be alphanumeric (with - or _ allowed): got '$NAME'" ;;
esac

CONTROL_DIR="$HOME/agent-control/$NAME"
[ -d "$CONTROL_DIR" ] || ct_die "start: project '$NAME' not bootstrapped — run 'yakos init $NAME --project <path>' first"

PROJECT_PATH_FILE="$CONTROL_DIR/.project-path"
if [ ! -f "$PROJECT_PATH_FILE" ]; then
    ct_die "start: $PROJECT_PATH_FILE missing — re-run 'yakos init' to repair"
fi
PROJECT_REPO="$(head -1 "$PROJECT_PATH_FILE")"
[ -d "$PROJECT_REPO" ] || ct_die "start: project repo not found at $PROJECT_REPO (per .project-path)"

# ---- permission mode --------------------------------------------------------

if [ "$SAFE" = "1" ]; then
    PERM_MODE="default"
else
    PERM_MODE="bypassPermissions"
fi

# ---- compose --agents JSON --------------------------------------------------

AGENTS_JSON=""
AGENT_COUNT=0
if [ "$NO_AGENTS" != "1" ]; then
    AGENTS_JSON="$(yk_agents_compose "$YAKOS_ROOT" "$PROJECT_REPO")"
    AGENT_COUNT="$(printf '%s' "$AGENTS_JSON" | jq 'length')"
fi

if [ "$PRINT_AGENTS" = "1" ]; then
    if [ -n "$AGENTS_JSON" ]; then
        printf '%s\n' "$AGENTS_JSON"
    else
        echo "{}"
    fi
    exit 0
fi

# ---- assemble claude command -----------------------------------------------

CLAUDE_ARGS=( "--add-dir" "$PROJECT_REPO" "--permission-mode" "$PERM_MODE" )

if [ -n "$AGENTS_JSON" ] && [ "$AGENT_COUNT" -gt 0 ]; then
    CLAUDE_ARGS+=( "--agents" "$AGENTS_JSON" )
fi

# MCP auto-detect: if <project>/.mcp.json exists, pass it.
PROJECT_MCP="$PROJECT_REPO/.mcp.json"
if [ -f "$PROJECT_MCP" ]; then
    CLAUDE_ARGS+=( "--mcp-config" "$PROJECT_MCP" )
    if [ "$STRICT_MCP" = "1" ]; then
        CLAUDE_ARGS+=( "--strict-mcp-config" )
    fi
fi

if [ -n "$MODEL" ]; then CLAUDE_ARGS+=( "--model" "$MODEL" ); fi
if [ "$BARE" = "1" ]; then CLAUDE_ARGS+=( "--bare" ); fi
if [ "$IDE" = "1" ]; then CLAUDE_ARGS+=( "--ide" ); fi
if [ "$CONTINUE" = "1" ]; then CLAUDE_ARGS+=( "--continue" ); fi
if [ -n "$RESUME" ]; then CLAUDE_ARGS+=( "--resume" "$RESUME" ); fi
if [ "$FORK" = "1" ]; then CLAUDE_ARGS+=( "--fork-session" ); fi

# Pass-through after `--`.
if [ "${#PASSTHROUGH[@]}" -gt 0 ]; then
    CLAUDE_ARGS+=( "${PASSTHROUGH[@]}" )
fi

# ---- preflight banner -------------------------------------------------------

print_banner() {
    cat <<EOF
yakos start — preflight
  project:        $NAME
  repo:           $PROJECT_REPO
  control dir:    $CONTROL_DIR
  permission:     $PERM_MODE
  agents:         $AGENT_COUNT registered$( [ "$NO_AGENTS" = "1" ] && printf ' (--no-agents: suppressed)' || true )
  mcp:            $( [ -f "$PROJECT_MCP" ] && printf '%s\n' "$PROJECT_MCP$( [ "$STRICT_MCP" = "1" ] && printf ' (strict)' || true )" || printf '(none — no .mcp.json)\n' )
  model:          ${MODEL:-(default)}
  mode flags:     $( [ "$BARE" = "1" ] && printf 'bare ' || true )$( [ "$IDE" = "1" ] && printf 'ide ' || true )$( [ "$CONTINUE" = "1" ] && printf 'continue ' || true )$( [ -n "$RESUME" ] && printf 'resume=%s ' "$RESUME" || true )$( [ "$FORK" = "1" ] && printf 'fork ' || true )
EOF
}
print_banner

# ---- dry-run exit -----------------------------------------------------------

if [ "$DRY_RUN" = "1" ]; then
    echo
    echo "Dry run — would exec:"
    printf '  claude'
    for a in "${CLAUDE_ARGS[@]}"; do
        case "$a" in
            *--agents) printf ' %s' "$a" ;;
            *)
                # Truncate the JSON arg for display.
                if [ "${#a}" -gt 80 ] && case "$a" in '{'*) true ;; *) false ;; esac; then
                    printf " '<%d-byte agents JSON>'" "${#a}"
                else
                    printf ' %q' "$a"
                fi
                ;;
        esac
    done
    printf '\n'
    exit 0
fi

# ---- audit-trail events ----------------------------------------------------

ts="$(ct_iso_now_z)"
session_event="$(jq -cn \
    --arg t "$ts" \
    --arg name "$NAME" \
    --arg repo "$PROJECT_REPO" \
    --arg perm "$PERM_MODE" \
    --argjson agents "$AGENT_COUNT" \
    '{type:"session_launched", ts:$t, project:$name, repo:$repo, permission_mode:$perm, agent_count:$agents}')"

SESSION_HISTORY="$CONTROL_DIR/work/current/.session-started-history.ndjson"
if [ -d "$(dirname -- "$SESSION_HISTORY")" ]; then
    printf '%s\n' "$session_event" >> "$SESSION_HISTORY" 2>/dev/null || \
        ct_log "WARN: could not append to $SESSION_HISTORY"
fi

LAUNCH_LOG_DIR="$HOME/.yakos-state"
mkdir -p "$LAUNCH_LOG_DIR" 2>/dev/null || true
LAUNCH_LOG="$LAUNCH_LOG_DIR/launch-log.ndjson"
printf '%s\n' "$session_event" >> "$LAUNCH_LOG" 2>/dev/null || \
    ct_log "WARN: could not append to $LAUNCH_LOG"

# ---- exec claude -----------------------------------------------------------

if ! command -v claude >/dev/null 2>&1; then
    ct_die "start: 'claude' CLI not on PATH (https://docs.claude.com/en/docs/claude-code)"
fi

# cd to control dir so claude treats it as cwd (enables auto-memory at the
# right path and keeps work/current/ accessible from inside the session).
cd "$CONTROL_DIR" || ct_die "start: failed to cd $CONTROL_DIR"

echo
exec claude "${CLAUDE_ARGS[@]}"
