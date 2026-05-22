#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: mcp.sh — install/uninstall/status the yakOS MCP server in a
# project's .mcp.json (or globally in ~/.claude/mcp.json).
#
# The MCP server (cli/lib/mcp/yakos-mcp-server.py) exposes per-runtime
# dispatch + continue tools to a Claude Code session. Once installed,
# the session can call dispatch_codex / continue_agy / etc. as
# structured tool calls rather than shelling out via Bash.
#
# See docs/mcp-integration.md for the full operator guide.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos mcp'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos mcp'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

SERVER_SCRIPT="$YAKOS_ROOT/cli/lib/mcp/yakos-mcp-server.py"
SERVER_NAME="yakos-dispatch"

usage() {
    cat <<'EOF'
yakos mcp <subcommand> [args...]

Manage the yakOS MCP server registration in a project's .mcp.json.

Subcommands:
  install [--project <path>]     Add the yakos-dispatch entry to the
                                 project's .mcp.json (creates the file
                                 if absent; merges other servers).
                                 Default project: cwd.
  uninstall [--project <path>]   Remove the yakos-dispatch entry.
  status [--project <path>]      Show whether the entry is present.
  probe                          Verify the 'mcp' Python package is
                                 installed (the server needs it).

The MCP server itself lives at:
  cli/lib/mcp/yakos-mcp-server.py

Tools exposed once installed (visible to Claude Code in the session):
  dispatch_codex, dispatch_agy, dispatch_claude_sdk,
  dispatch_antigravity_sdk, dispatch_claude
  continue_codex, continue_agy, continue_claude_sdk, continue_claude
  (antigravity-sdk continue intentionally absent — SDK lacks cross-
  process resume support at v0.31)

Examples:
  yakos mcp install                                     # cwd
  yakos mcp install --project ~/code/myapp
  yakos mcp status
  yakos mcp uninstall

After install, restart the Claude Code session in that project and
the yakos-dispatch tools become available to the lead.

See docs/mcp-integration.md for the full guide.
EOF
}

resolve_project() {
    if [ -n "${1:-}" ]; then
        if [ ! -d "$1" ]; then
            ct_die "mcp: --project '$1' does not exist"
        fi
        ct_realpath "$1"
        return
    fi
    pwd -P
}

parse_args() {
    PROJECT=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --project) shift; PROJECT="${1:-}" ;;
            --project=*) PROJECT="${1#--project=}" ;;
            -*) ct_die "mcp $SUB: unknown flag '$1'" ;;
            *) ct_die "mcp $SUB: unexpected arg '$1'" ;;
        esac
        shift
    done
    PROJECT="$(resolve_project "$PROJECT")"
    MCP_FILE="$PROJECT/.mcp.json"
}

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    install|uninstall|status|probe) ;;
    *) ct_die "mcp: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- probe ------------------------------------------------------------------

if [ "$SUB" = "probe" ]; then
    if python3 -c 'import mcp' 2>/dev/null; then
        echo "yakos mcp probe: OK — 'mcp' package importable"
        exit 0
    fi
    cat <<'EOF'
yakos mcp probe: FAIL — 'mcp' Python package not installed.

Install it with:
  pip install mcp

The yakOS MCP server (cli/lib/mcp/yakos-mcp-server.py) needs this
package to talk to Claude Code via the Model Context Protocol.
EOF
    exit 1
fi

parse_args "$@"

# ---- install ----------------------------------------------------------------

if [ "$SUB" = "install" ]; then
    [ -f "$SERVER_SCRIPT" ] || ct_die "mcp install: server script missing at $SERVER_SCRIPT"

    # Construct the new entry. Pass YAKOS_ROOT through env so the server
    # can locate the yakos CLI.
    entry="$(jq -nc \
        --arg cmd "python3" \
        --arg script "$SERVER_SCRIPT" \
        --arg root "$YAKOS_ROOT" \
        '{command: $cmd, args: [$script], env: {YAKOS_ROOT: $root}}')"

    if [ ! -f "$MCP_FILE" ]; then
        # Create fresh .mcp.json
        jq -n --arg name "$SERVER_NAME" --argjson entry "$entry" \
            '{mcpServers: {($name): $entry}}' > "$MCP_FILE"
        echo "wrote $MCP_FILE with $SERVER_NAME entry"
        echo "restart your Claude Code session in $PROJECT to load it"
        exit 0
    fi

    # Validate existing JSON
    if ! jq empty "$MCP_FILE" 2>/dev/null; then
        ct_die "mcp install: existing $MCP_FILE is not valid JSON; refusing to overwrite"
    fi

    # Backup, then merge our entry preserving other servers
    bak="$MCP_FILE.yakos-bak-$(ct_iso_utc)"
    cp "$MCP_FILE" "$bak"

    merged_tmp="$(mktemp -t yakos-mcp-merge.XXXXXX)"
    jq --arg name "$SERVER_NAME" --argjson entry "$entry" \
        '.mcpServers //= {} | .mcpServers[$name] = $entry' \
        "$MCP_FILE" > "$merged_tmp" \
        && mv "$merged_tmp" "$MCP_FILE" \
        || { rm -f "$merged_tmp"; ct_die "mcp install: merge failed; backup at $bak"; }

    echo "updated $MCP_FILE (added/refreshed $SERVER_NAME entry)"
    echo "backup at $bak"
    echo "restart your Claude Code session in $PROJECT to load it"
    exit 0
fi

# ---- uninstall --------------------------------------------------------------

if [ "$SUB" = "uninstall" ]; then
    if [ ! -f "$MCP_FILE" ]; then
        echo "mcp uninstall: $MCP_FILE not present (nothing to do)"
        exit 0
    fi
    if ! jq -e --arg name "$SERVER_NAME" '.mcpServers[$name]?' "$MCP_FILE" >/dev/null 2>&1; then
        echo "mcp uninstall: $SERVER_NAME entry not present in $MCP_FILE"
        exit 0
    fi

    bak="$MCP_FILE.yakos-bak-$(ct_iso_utc)"
    cp "$MCP_FILE" "$bak"

    merged_tmp="$(mktemp -t yakos-mcp-rm.XXXXXX)"
    jq --arg name "$SERVER_NAME" 'del(.mcpServers[$name])' "$MCP_FILE" > "$merged_tmp" \
        && mv "$merged_tmp" "$MCP_FILE" \
        || { rm -f "$merged_tmp"; ct_die "mcp uninstall: rewrite failed; backup at $bak"; }

    # If mcpServers is now empty and was the only key, leave it as `{}`
    # for clarity rather than removing the file entirely.
    echo "removed $SERVER_NAME from $MCP_FILE"
    echo "backup at $bak"
    exit 0
fi

# ---- status -----------------------------------------------------------------

if [ "$SUB" = "status" ]; then
    echo "yakos mcp status — project: $PROJECT"
    echo "  server script: $SERVER_SCRIPT $([ -f "$SERVER_SCRIPT" ] && echo '(present)' || echo '(MISSING)')"
    echo "  .mcp.json:     $MCP_FILE"
    if [ ! -f "$MCP_FILE" ]; then
        echo "    not present — run 'yakos mcp install'"
    elif jq -e --arg name "$SERVER_NAME" '.mcpServers[$name]?' "$MCP_FILE" >/dev/null 2>&1; then
        echo "    $SERVER_NAME entry: PRESENT"
        echo "    --- entry ---"
        jq --arg name "$SERVER_NAME" '.mcpServers[$name]' "$MCP_FILE" | sed 's/^/    /'
    else
        echo "    $SERVER_NAME entry: NOT INSTALLED (other servers may be present)"
    fi
    echo
    echo "  mcp python package:"
    if python3 -c 'import mcp' 2>/dev/null; then
        echo "    OK (importable)"
    else
        echo "    MISSING — run 'pip install mcp'"
    fi
    exit 0
fi

ct_die "mcp: internal — fell through dispatch (subcommand '$SUB')"
