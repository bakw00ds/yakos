#!/usr/bin/env bash
# start.sh — launch a Claude/codex/gemini session for a yakos-bootstrapped project.
#
# Purpose: end the manual `cd ~/agent-control/<name> && claude --add-dir ...`
# pattern, default to bypassPermissions, and inject project + framework
# agents via the right path for the chosen runtime (Claude: --agents JSON;
# codex: <project>/.codex/agents/yakos-*.toml; gemini: similar).
#
# v0.4.0+ supports multiple runtimes. The runtime is selected via:
#   1. --runtime <id> flag on the command line
#   2. YAKOS_RUNTIME environment variable
#   3. ~/.yakos-state/default-runtime file (set by `yakos auth login --as-default`)
#   4. Fallback: claude
#
# Audit trail is appended at:
#   <control>/work/current/.session-started-history.ndjson
#   ~/.yakos-state/launch-log.ndjson

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos start'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos start'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

usage() {
    cat <<'EOF'
yakos start [<name>] [flags] — launch a session for a yakos project.

Resolves the project repo from ~/agent-control/<name>/.project-path,
loads the chosen runtime adapter, materializes agents in the right
format, and exec's the session. <name> is inferred from the cwd if
not supplied.

Runtime selection:
    --runtime <id>        claude (default) | codex | gemini
                          Falls back to YAKOS_RUNTIME env or
                          ~/.yakos-state/default-runtime.

Permission mode:
    --safe                Prompts on (claude: --permission-mode default;
                          codex: default sandbox; gemini: default).
    (default)             bypass — claude bypassPermissions / codex
                          --dangerously-bypass-approvals-and-sandbox /
                          gemini --approval-mode=yolo.

Agent injection:
    --no-agents           Skip materialization (debug; agents won't bind).

Session passthroughs (forwarded to the runtime CLI when supported):
    --continue, -c        claude only — continue most recent.
    --resume <id>         claude / codex (codex resume <id>).
    --fork-session        claude / codex (codex fork).
    --ide                 claude only — auto-attach to IDE.
    --bare                claude only — minimal mode.
    --strict-mcp          claude only — pass --strict-mcp-config.
    --model <alias>       Forward to runtime if it supports a model flag.

Inspection:
    --dry-run             Print what would be exec'd; exit 0.
    --print-agents        Print the composed agent JSON; exit 0.
    --                    End of yakos flags; rest passed to runtime CLI.

Examples:
    yakos start                       # auto-detect, claude (default)
    yakos start myapp
    yakos start myapp --runtime codex
    yakos start myapp --runtime gemini --safe
    yakos start myapp --dry-run
EOF
}

NAME=""
RUNTIME=""
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

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --runtime)
            shift
            [ "$#" -gt 0 ] || ct_die "start: --runtime requires an id"
            RUNTIME="$1"
            ;;
        --runtime=*) RUNTIME="${1#--runtime=}" ;;
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
            rest="${cwd_real#$ac_real/}"
            NAME="${rest%%/*}"
            ;;
        *)
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
[ -f "$PROJECT_PATH_FILE" ] || ct_die "start: $PROJECT_PATH_FILE missing — re-run 'yakos init' to repair"
PROJECT_REPO="$(head -1 "$PROJECT_PATH_FILE")"
[ -d "$PROJECT_REPO" ] || ct_die "start: project repo not found at $PROJECT_REPO"

# ---- runtime selection ------------------------------------------------------

if [ -z "$RUNTIME" ]; then
    RUNTIME="$(yk_rt_default)"
fi
yk_rt_is_known "$RUNTIME" || ct_die "start: unknown runtime '$RUNTIME' (known: $(yk_rt_known | tr '\n' ' '))"
yk_rt_load "$RUNTIME"

# ---- preflight checks -------------------------------------------------------

CLI_OK=1; AUTH_OK=1
yk_rt_check_cli 2>/dev/null || CLI_OK=0
if [ "$CLI_OK" = "1" ]; then
    yk_rt_check_auth 2>/dev/null || AUTH_OK=0
fi

if [ "$DRY_RUN" != "1" ] && [ "$PRINT_AGENTS" != "1" ]; then
    if [ "$CLI_OK" != "1" ]; then
        ct_die "start: '$RUNTIME' CLI not on PATH. Install it, then retry. (--dry-run works without the CLI installed.)"
    fi
    if [ "$AUTH_OK" != "1" ]; then
        ct_log "WARN: '$RUNTIME' auth not detected; the runtime may prompt or fail. Run 'yakos auth login $RUNTIME' to fix."
    fi
fi

# Translate perm mode to the abstract bypass|safe used by adapters.
PERM_MODE="bypass"; [ "$SAFE" = "1" ] && PERM_MODE="safe"

# ---- agent count for banner -------------------------------------------------

AGENT_COUNT=0
if [ "$NO_AGENTS" != "1" ] && command -v jq >/dev/null 2>&1; then
    composed="$(yk_agents_compose "$YAKOS_ROOT" "$PROJECT_REPO" 2>/dev/null || echo '{}')"
    AGENT_COUNT="$(printf '%s' "$composed" | jq 'length' 2>/dev/null || echo 0)"
fi

if [ "$PRINT_AGENTS" = "1" ]; then
    yk_agents_compose "$YAKOS_ROOT" "$PROJECT_REPO"
    exit 0
fi

# ---- preflight banner -------------------------------------------------------

CAPS="$(yk_rt_capabilities)"
print_banner() {
    cat <<EOF
yakos start — preflight
  project:        $NAME
  repo:           $PROJECT_REPO
  control dir:    $CONTROL_DIR
  runtime:        $RUNTIME ($CAPS)
  cli:            $( [ "$CLI_OK" = "1" ] && printf 'OK' || printf 'NOT FOUND (--dry-run only)' )
  auth:           $( [ "$AUTH_OK" = "1" ] && printf 'OK' || printf 'NOT CONFIGURED (run: yakos auth login %s)' "$RUNTIME" )
  permission:     $PERM_MODE
  agents:         $AGENT_COUNT registered$( [ "$NO_AGENTS" = "1" ] && printf ' (--no-agents: suppressed)' || true )
  mode flags:     $( [ "$BARE" = "1" ] && printf 'bare ' || true )$( [ "$IDE" = "1" ] && printf 'ide ' || true )$( [ "$CONTINUE" = "1" ] && printf 'continue ' || true )$( [ -n "$RESUME" ] && printf 'resume=%s ' "$RESUME" || true )$( [ "$FORK" = "1" ] && printf 'fork ' || true )$( [ -n "$MODEL" ] && printf 'model=%s ' "$MODEL" || true )

  Lead discipline (rule:lead-dispatch-discipline):
    lead = decompose / integrate / supervise. specialists = parallel.
    sequential only when the next task depends on the previous.
EOF
}
print_banner

# ---- soft-degrade warnings --------------------------------------------------

# Warn when a feature is requested but the runtime can't honor it.
warn_unsupported() {
    local cap="$1"
    local feature="$2"
    case ",$CAPS," in
        *",$cap,"*) return 0 ;;
        *) ct_log "NOTE: '$RUNTIME' does not support $feature ($cap); flag will be ignored." ;;
    esac
}

if [ "$CONTINUE" = "1" ]; then
    case "$RUNTIME" in
        claude) : ;;
        *) warn_unsupported "fork-headless" "--continue (claude-specific)" ;;
    esac
fi
if [ "$IDE" = "1" ] && [ "$RUNTIME" != "claude" ]; then
    ct_log "NOTE: --ide is claude-specific; ignored for $RUNTIME."
fi
if [ "$BARE" = "1" ] && [ "$RUNTIME" != "claude" ]; then
    ct_log "NOTE: --bare is claude-specific; ignored for $RUNTIME."
fi
if [ "$STRICT_MCP" = "1" ] && [ "$RUNTIME" != "claude" ]; then
    ct_log "NOTE: --strict-mcp is claude-specific; ignored for $RUNTIME."
fi

# ---- assemble runtime-specific extra flags ----------------------------------

EXTRA_FLAGS=()
case "$RUNTIME" in
    claude)
        [ -n "$MODEL" ]      && EXTRA_FLAGS+=( --model "$MODEL" )
        [ "$BARE" = "1" ]    && EXTRA_FLAGS+=( --bare )
        [ "$IDE" = "1" ]     && EXTRA_FLAGS+=( --ide )
        [ "$CONTINUE" = "1" ] && EXTRA_FLAGS+=( --continue )
        [ -n "$RESUME" ]     && EXTRA_FLAGS+=( --resume "$RESUME" )
        [ "$FORK" = "1" ]    && EXTRA_FLAGS+=( --fork-session )
        if [ "$STRICT_MCP" = "1" ] && [ -f "$PROJECT_REPO/.mcp.json" ]; then
            EXTRA_FLAGS+=( --strict-mcp-config )
        fi
        ;;
    codex)
        if [ -n "$RESUME" ]; then EXTRA_FLAGS+=( resume "$RESUME" ); fi
        # codex's session resumption uses subcommand syntax; the adapter
        # currently launches plain `codex` — full --resume support is a
        # follow-up.
        ;;
    gemini)
        if [ -n "$RESUME" ]; then EXTRA_FLAGS+=( --resume "$RESUME" ); fi
        if [ -n "$MODEL" ]; then EXTRA_FLAGS+=( --model "$MODEL" ); fi
        ;;
esac

if [ "${#PASSTHROUGH[@]}" -gt 0 ]; then
    EXTRA_FLAGS+=( "${PASSTHROUGH[@]}" )
fi

# ---- dry-run exit -----------------------------------------------------------

if [ "$DRY_RUN" = "1" ]; then
    echo
    echo "Dry run — would exec via runtime '$RUNTIME':"
    case "$RUNTIME" in
        claude)
            printf '  claude --add-dir %q --permission-mode %s' \
                "$PROJECT_REPO" \
                "$( [ "$SAFE" = "1" ] && printf 'default' || printf 'bypassPermissions' )"
            [ "$AGENT_COUNT" -gt 0 ] && [ "$NO_AGENTS" != "1" ] && printf " --agents '<%d agents JSON>'" "$AGENT_COUNT"
            [ -f "$PROJECT_REPO/.mcp.json" ] && printf ' --mcp-config %q' "$PROJECT_REPO/.mcp.json"
            ;;
        codex)
            printf '  codex --add-dir %q' "$PROJECT_REPO"
            [ "$SAFE" != "1" ] && printf ' --dangerously-bypass-approvals-and-sandbox'
            [ "$AGENT_COUNT" -gt 0 ] && [ "$NO_AGENTS" != "1" ] && printf "  # + %d agents staged at %s/.codex/agents/yakos-*.toml" "$AGENT_COUNT" "$PROJECT_REPO"
            ;;
        gemini)
            printf '  gemini --include-directories %q' "$PROJECT_REPO"
            [ "$SAFE" != "1" ] && printf ' --approval-mode=yolo'
            [ "$AGENT_COUNT" -gt 0 ] && [ "$NO_AGENTS" != "1" ] && printf "  # + %d agents staged at %s/.gemini/agents/yakos-*.md" "$AGENT_COUNT" "$PROJECT_REPO"
            ;;
    esac
    if [ "${#EXTRA_FLAGS[@]}" -gt 0 ]; then
        for f in "${EXTRA_FLAGS[@]}"; do printf ' %q' "$f"; done
    fi
    printf '\n'
    exit 0
fi

# ---- materialize agents -----------------------------------------------------

if [ "$NO_AGENTS" != "1" ]; then
    yk_rt_materialize_agents "$YAKOS_ROOT" "$PROJECT_REPO" >/dev/null 2>&1 || \
        ct_log "WARN: agent materialization for runtime '$RUNTIME' returned non-zero"
fi

# ---- audit trail -----------------------------------------------------------

ts="$(ct_iso_now_z)"
session_event="$(jq -cn \
    --arg t "$ts" \
    --arg name "$NAME" \
    --arg repo "$PROJECT_REPO" \
    --arg perm "$PERM_MODE" \
    --arg runtime "$RUNTIME" \
    --argjson agents "$AGENT_COUNT" \
    '{type:"session_launched", ts:$t, project:$name, repo:$repo,
      runtime:$runtime, permission_mode:$perm, agent_count:$agents}')"

SESSION_HISTORY="$CONTROL_DIR/work/current/.session-started-history.ndjson"
if [ -d "$(dirname -- "$SESSION_HISTORY")" ]; then
    printf '%s\n' "$session_event" >> "$SESSION_HISTORY" 2>/dev/null || \
        ct_log "WARN: could not append to $SESSION_HISTORY"
fi

mkdir -p "$HOME/.yakos-state" 2>/dev/null || true
LAUNCH_LOG="$HOME/.yakos-state/launch-log.ndjson"
ct_rotate_log "$LAUNCH_LOG" 2>/dev/null || true
printf '%s\n' "$session_event" >> "$LAUNCH_LOG" 2>/dev/null || \
    ct_log "WARN: could not append to $LAUNCH_LOG"

# ---- runtime-probe snapshot (drift detection) ------------------------------
# Capture each runtime CLI's --version + capability set. yakos doctor
# --probe-runtime compares this snapshot to the prior one and warns
# on schema/version drift so the operator knows when an adapter may
# need updating.
PROBE_DIR="$HOME/.yakos-state/runtime-probes"
mkdir -p "$PROBE_DIR" 2>/dev/null || true
case "$RUNTIME" in
    claude) rt_ver="$(claude --version 2>/dev/null | head -1 || echo unknown)" ;;
    codex)  rt_ver="$(codex --version 2>/dev/null | head -1 || echo unknown)" ;;
    gemini) rt_ver="$(gemini --version 2>/dev/null | head -1 || echo unknown)" ;;
    *)      rt_ver="unknown" ;;
esac
probe_snapshot="$(jq -cn \
    --arg t "$ts" \
    --arg runtime "$RUNTIME" \
    --arg version "$rt_ver" \
    --arg caps "$CAPS" \
    '{ts:$t, runtime:$runtime, version:$version, capabilities:$caps}')"
printf '%s\n' "$probe_snapshot" >> "$PROBE_DIR/${RUNTIME}.ndjson" 2>/dev/null || true

# ---- exec runtime ----------------------------------------------------------

cd "$CONTROL_DIR" || ct_die "start: failed to cd $CONTROL_DIR"

echo
yk_rt_launch "$PROJECT_REPO" "$PERM_MODE" "${EXTRA_FLAGS[@]}"
