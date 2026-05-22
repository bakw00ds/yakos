#!/usr/bin/env bash
# auth.sh — wrapper for per-runtime auth flows.
#
# Purpose: give the operator a single command to log in to claude / codex /
# gemini without remembering each CLI's flow. Also lets `yakos doctor`
# and `yakos start` surface auth state uniformly.
#
# Subcommands:
#   yakos auth status [<runtime>]  — report auth + cli state for one or all runtimes
#   yakos auth login <runtime>     — shell into the runtime's login flow
#   yakos auth logout <runtime>    — best-effort credential removal
#   yakos auth set-default <runtime> — persist to ~/.yakos-state/default-runtime
#
# yakOS itself NEVER stores or rotates credentials — it just hands off to
# the runtime's own login/logout commands and reports state.

set -eu

: "${YAKOS_LIB:?auth.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

usage() {
    cat <<'EOF'
yakos auth <subcommand> [args...]

Subcommands:
  status [<runtime>]      Report cli + auth state. Defaults to all known runtimes.
  login <runtime>         Run the runtime's login flow:
                          - claude:  prints '/login' instruction + opens claude
                          - codex:   exec 'codex login'
                          - gemini:  prints OAuth / API key options
                          With --as-default, also persist the runtime as the
                          yakos start default.
  logout <runtime>        Best-effort credential removal:
                          - claude:  unset ANTHROPIC_API_KEY hint; remove ~/.claude/auth.json
                          - codex:   exec 'codex logout' if the binary supports it
                          - gemini:  point at gemini's logout flow
  set-default <runtime>   Persist the runtime as yakos start's default
                          (writes ~/.yakos-state/default-runtime).

Examples:
  yakos auth status
  yakos auth login codex --as-default
  yakos auth set-default claude
EOF
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
esac

# ---- helpers ---------------------------------------------------------------

print_runtime_status() {
    local id="$1"
    local adapter="$YAKOS_LIB/runtimes/${id}.sh"
    if [ ! -f "$adapter" ]; then
        printf '  %-8s\n' "$id"
        printf '    cli:    %-15s %s\n' "n/a" "(adapter not yet shipped in this yakOS version)"
        printf '    auth:   %-15s\n' "n/a"
        printf '    caps:   (none — adapter pending)\n'
        echo
        return 0
    fi
    yk_rt_load "$id"

    local cli_state="missing"
    local auth_state="not configured"
    local cli_hint=""
    local auth_hint=""

    if yk_rt_check_cli 2>/dev/null; then
        cli_state="OK"
    else
        case "$id" in
            claude)          cli_hint="install: https://docs.claude.com/en/docs/claude-code" ;;
            claude-sdk)      cli_hint="install: pip install claude-agent-sdk (python 3.10+)" ;;
            codex)           cli_hint="install: npm install -g @openai/codex" ;;
            gemini)          cli_hint="install: npm install -g @google/gemini-cli (DEPRECATED 2026-06-18; use agy)" ;;
            agy)             cli_hint="install: curl -fsSL https://antigravity.google/cli/install.sh | bash" ;;
            antigravity-sdk) cli_hint="install: pip install google-antigravity (bundles compiled binary)" ;;
        esac
    fi

    if [ "$cli_state" = "OK" ] && yk_rt_check_auth 2>/dev/null; then
        auth_state="OK"
    elif [ "$cli_state" = "OK" ]; then
        auth_hint="run: yakos auth login $id"
    fi

    local default_marker=""
    if [ "$id" = "$(yk_rt_default)" ]; then
        default_marker=" (default)"
    fi

    printf '  %-8s%s\n' "$id" "$default_marker"
    printf '    cli:    %-15s %s\n' "$cli_state" "$cli_hint"
    printf '    auth:   %-15s %s\n' "$auth_state" "$auth_hint"
    printf '    caps:   %s\n' "$(yk_rt_capabilities)"
    echo
}

# ---- subcommand: status -----------------------------------------------------

if [ "$SUB" = "status" ]; then
    target="${1:-}"
    echo "yakos auth — runtime status"
    echo
    if [ -n "$target" ]; then
        yk_rt_is_known "$target" || ct_die "auth status: unknown runtime '$target'"
        print_runtime_status "$target"
    else
        for id in $(yk_rt_known); do
            print_runtime_status "$id"
        done
    fi
    exit 0
fi

# ---- subcommand: set-default ------------------------------------------------

if [ "$SUB" = "set-default" ]; then
    target="${1:-}"
    [ -n "$target" ] || ct_die "auth set-default: <runtime> required"
    yk_rt_is_known "$target" || ct_die "auth set-default: unknown runtime '$target'"
    yk_rt_set_default "$target"
    echo "default runtime set to: $target (~/.yakos-state/default-runtime)"
    exit 0
fi

# ---- subcommand: login ------------------------------------------------------

if [ "$SUB" = "login" ]; then
    target=""
    AS_DEFAULT=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --as-default) AS_DEFAULT=1 ;;
            -*) ct_die "auth login: unknown flag '$1'" ;;
            *)
                if [ -z "$target" ]; then target="$1"
                else ct_die "auth login: too many positional args"
                fi
                ;;
        esac
        shift
    done

    [ -n "$target" ] || ct_die "auth login: <runtime> required (claude|claude-sdk|codex|gemini|agy|antigravity-sdk)"
    yk_rt_is_known "$target" || ct_die "auth login: unknown runtime '$target'"

    # SDK adapters delegate their login UX to the underlying CLI's flow
    # since they share credentials with the bundled binary.
    case "$target" in
        claude-sdk)
            ct_log "claude-sdk: bundles Claude Code CLI; using claude auth flow"
            target_for_login=claude
            ;;
        antigravity-sdk)
            ct_log "antigravity-sdk: shares Google auth posture; using agy auth flow"
            ct_log "antigravity-sdk: SDK additionally reads GEMINI_API_KEY directly"
            target_for_login=agy
            ;;
        *)
            target_for_login="$target"
            ;;
    esac

    yk_rt_load "$target"
    yk_rt_check_cli || ct_die "auth login: '$target' CLI not on PATH; install it first"

    case "$target_for_login" in
        claude)
            cat <<'EOF'
claude does not have a non-interactive login command. Two options:

  1. Set the env var:
        export ANTHROPIC_API_KEY="sk-ant-..."

  2. OAuth flow inside a claude session:
        claude   # then type '/login' and follow the browser flow.

After either, run 'yakos auth status claude' to verify.
EOF
            ;;
        codex)
            echo "Launching 'codex login'..."
            codex login
            ;;
        gemini)
            cat <<'EOF'
gemini-cli supports three auth paths:

  1. OAuth (free tier, recommended for personal use):
        gemini   # interactive auth on first launch

  2. API key (Google AI Studio):
        export GEMINI_API_KEY="..."
        # Or persist via gemini's settings.

  3. Vertex AI (enterprise):
        gcloud auth application-default login
        export GOOGLE_GENAI_USE_VERTEXAI=true

After configuring one, run 'yakos auth status gemini' to verify.

NOTE: Gemini CLI stops serving requests 2026-06-18. Migrate to agy:
      yakos auth login agy --as-default
EOF
            ;;
        agy)
            cat <<'EOF'
agy (Antigravity CLI) uses Google OAuth via system keyring on first
run. yakOS doesn't drive the OAuth flow — agy does, interactively.

  1. Run agy once interactively:
        agy
        # Browser opens; complete Google OAuth.
        # Close the TUI when done (the credentials are now cached).

  2. (Optional) headless / SSH / CI:
        export ANTIGRAVITY_API_KEY="..."

After either, run 'yakos auth status agy' to verify.
Credentials live in your keychain + ~/.gemini/antigravity-cli/.
EOF
            ;;
    esac

    if [ "$AS_DEFAULT" = "1" ]; then
        yk_rt_set_default "$target"
        echo
        echo "default runtime set to: $target"
    fi
    exit 0
fi

# ---- subcommand: logout -----------------------------------------------------

if [ "$SUB" = "logout" ]; then
    target="${1:-}"
    [ -n "$target" ] || ct_die "auth logout: <runtime> required"
    yk_rt_is_known "$target" || ct_die "auth logout: unknown runtime '$target'"

    case "$target" in
        claude|claude-sdk)
            if [ "$target" = "claude-sdk" ]; then
                ct_log "claude-sdk: shares credentials with claude (bundled CLI); routing logout to claude"
            fi
            if [ -f "$HOME/.claude/auth.json" ]; then
                rm -f "$HOME/.claude/auth.json"
                echo "removed ~/.claude/auth.json"
            else
                echo "no claude auth.json found"
            fi
            echo "if ANTHROPIC_API_KEY is set in your shell rc, unset it manually."
            ;;
        codex)
            if command -v codex >/dev/null 2>&1; then
                codex logout 2>/dev/null || ct_log "codex logout returned non-zero (may not be supported in this version)"
            fi
            codex_home="${CODEX_HOME:-$HOME/.codex}"
            if [ -f "$codex_home/auth.json" ]; then
                rm -f "$codex_home/auth.json"
                echo "removed $codex_home/auth.json"
            fi
            ;;
        gemini)
            cat <<'EOF'
gemini-cli stores OAuth state in its own config dir. To clear:
    rm -rf ~/.gemini/auth.json    # if present
    unset GEMINI_API_KEY
    unset GOOGLE_GENAI_USE_VERTEXAI
EOF
            ;;
        agy|antigravity-sdk)
            if [ "$target" = "antigravity-sdk" ]; then
                ct_log "antigravity-sdk: shares Google credential surface with agy; routing logout to agy"
                ct_log "antigravity-sdk: SDK also reads GEMINI_API_KEY directly — unset it separately"
            fi
            cat <<'EOF'
agy stores OAuth state in your system keychain plus
~/.gemini/antigravity-cli/. To fully sign out:
    1. Remove keychain entry (macOS: Keychain Access → search "antigravity"
       or "gemini"; Linux: secret-tool clear; Windows: Credential Manager)
    2. rm -rf ~/.gemini/antigravity-cli/conversations
    3. unset ANTIGRAVITY_API_KEY
    4. unset GEMINI_API_KEY

Or simpler: re-run 'agy' interactively to reauthenticate as a different
account; the keychain entry gets overwritten.
EOF
            ;;
    esac
    exit 0
fi

ct_die "auth: unknown subcommand '$SUB' (try 'yakos auth --help')"
