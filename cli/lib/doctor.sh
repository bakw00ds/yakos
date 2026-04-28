#!/usr/bin/env bash
# doctor.sh — verify YakOS install + environment health.
#
# Exits 0 if everything looks good or only WARN findings; exits 1 if any
# ERROR-severity finding is reported.

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos doctor'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos doctor — verify YakOS install + environment health

Checks:
    Required commands (bash, git, jq)
    Optional commands (gtimeout, gsed, shellcheck, python3) — surfaced as INFO
    ~/.yakos pointer exists and resolves to an existing repo
    Symlinks under ~/.claude/{agents,skills,rules,playbooks}/ that target
        YakOS resolve cleanly
    ~/.claude/settings.json is valid JSON if present
    ~/.claude/projects/ is intact (informational; never modified)

Usage: yakos doctor

Exit code:
    0   No errors (warnings/info OK)
    1   One or more errors
EOF
            exit 0
            ;;
        *)
            ct_die "doctor: unknown argument '$arg' (try --help)"
            ;;
    esac
done

CLAUDE_DIR="$HOME/.claude"
YAKOS_POINTER="$HOME/.yakos"
SETTINGS_FILE="$CLAUDE_DIR/settings.json"

errors=0
warnings=0

ok()    { printf '  [ok]   %s\n' "$*"; }
info()  { printf '  [info] %s\n' "$*"; }
warn()  { printf '  [warn] %s\n' "$*"; warnings=$((warnings + 1)); }
err()   { printf '  [err]  %s\n' "$*"; errors=$((errors + 1)); }

echo "yakos doctor"
echo ""

# ---- required commands ------------------------------------------------------

echo "Required commands"
for c in bash git jq; do
    if command -v "$c" >/dev/null 2>&1; then
        ok "$c: $(command -v "$c")"
    else
        err "$c: not found in PATH"
    fi
done
echo ""

# ---- optional commands ------------------------------------------------------

echo "Optional commands"
for c in gtimeout gsed shellcheck python3 realpath; do
    if command -v "$c" >/dev/null 2>&1; then
        ok "$c: $(command -v "$c")"
    else
        info "$c: not present (compat handles macOS BSD fallback)"
    fi
done
echo ""

# ---- pointer ----------------------------------------------------------------

echo "Install pointer"
if [ ! -f "$YAKOS_POINTER" ]; then
    err "$YAKOS_POINTER does not exist; run 'yakos install'"
else
    pointer_target="$(cat "$YAKOS_POINTER" 2>/dev/null || true)"
    if [ -z "$pointer_target" ]; then
        err "$YAKOS_POINTER is empty"
    elif [ ! -d "$pointer_target" ]; then
        err "$YAKOS_POINTER points to '$pointer_target' which does not exist"
    elif [ ! -d "$pointer_target/lib" ]; then
        err "$YAKOS_POINTER points to '$pointer_target' but no lib/ directory there"
    else
        ok "$YAKOS_POINTER → $pointer_target"
    fi
fi
echo ""

# ---- symlinks ---------------------------------------------------------------

echo "YakOS symlinks under $CLAUDE_DIR"
if [ ! -d "$CLAUDE_DIR" ]; then
    info "$CLAUDE_DIR does not exist (run 'yakos install')"
else
    pointer_target="$(cat "$YAKOS_POINTER" 2>/dev/null || true)"
    total_yakos=0
    total_broken=0
    total_foreign=0
    for sub in agents skills rules playbooks; do
        d="$CLAUDE_DIR/$sub"
        [ -d "$d" ] || continue
        while IFS= read -r link; do
            [ -n "$link" ] || continue
            tgt="$(ct_realpath "$link" 2>/dev/null || true)"
            if [ ! -e "$tgt" ]; then
                err "broken symlink: $link → $tgt"
                total_broken=$((total_broken + 1))
                continue
            fi
            case "$tgt" in
                "${pointer_target:-__none__}"/*)
                    total_yakos=$((total_yakos + 1))
                    ;;
                *)
                    total_foreign=$((total_foreign + 1))
                    ;;
            esac
        done < <(find "$d" -type l 2>/dev/null)
    done
    if [ "$total_yakos" -eq 0 ] && [ "$total_broken" -eq 0 ] && [ "$total_foreign" -eq 0 ]; then
        info "no symlinks present (lib/ is empty in v0.1; agents/skills come in Batch 3)"
    else
        ok "$total_yakos YakOS-owned symlink(s) resolved"
        if [ "$total_foreign" -gt 0 ]; then
            info "$total_foreign symlink(s) under those dirs point outside YakOS (left alone)"
        fi
    fi
fi
echo ""

# ---- settings.json ----------------------------------------------------------

echo "settings.json"
if [ ! -f "$SETTINGS_FILE" ]; then
    info "$SETTINGS_FILE does not exist (run 'yakos install' to create)"
else
    if ct_json_valid "$SETTINGS_FILE"; then
        ok "$SETTINGS_FILE is valid JSON"
        agent_teams="$(jq -r '.env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS // empty' "$SETTINGS_FILE" 2>/dev/null || true)"
        if [ "$agent_teams" = "1" ]; then
            ok "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS = 1"
        else
            warn "env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS not set to '1' (agent teams disabled)"
        fi
    else
        err "$SETTINGS_FILE is not valid JSON"
    fi
fi
echo ""

# ---- auto-memory (informational only) --------------------------------------

echo "Auto-memory"
if [ -d "$CLAUDE_DIR/projects" ]; then
    n_proj="$(find "$CLAUDE_DIR/projects" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')"
    info "$CLAUDE_DIR/projects/ exists ($n_proj project dir(s)). YakOS never modifies this."
else
    info "$CLAUDE_DIR/projects/ does not exist (no auto-memory yet)"
fi
echo ""

# ---- summary ----------------------------------------------------------------

echo "Summary: $errors error(s), $warnings warning(s)"
if [ "$errors" -gt 0 ]; then
    exit 1
fi
exit 0
