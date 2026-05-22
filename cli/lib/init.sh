#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: init.sh — bootstrap a project for use with YakOS.
#
# Behavior (build prompt §"`yakos init` — required behavior"):
#   1. Verify project path exists and is a git repo.
#   2. Create ~/agent-control/<name>/.
#   3. Create work/current/{logs,artifacts,reports}/.
#   4. Write work/current/.session-started-history with [].
#   5. Write .gitignore with `*`.
#   6. Write settings.local.json template.
#   7. Write work/current/hook-bypass.md template.
#   8. Copy <yakos>/lib/hooks/*.sh into <project>/scripts/hooks/, with
#      .framework-hash siblings. Skip existing files unless --force.
#   9. Copy <yakos>/lib/settings/path-allowlist.template.json into
#      <project>/.claude/path-allowlist.json if missing.
#  10. Copy <yakos>/lib/settings/settings.template.json into
#      <project>/.claude/settings.json if missing.
#  11. Ensure ~/.claude/projects/<encoded-path>/MEMORY.md exists.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos init'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos init'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

NAME=""
PROJECT=""
FORCE=0
WITH_GATE=0

usage() {
    cat <<EOF
yakos init <name> --project <path> [--force] [--with-gate]

Bootstrap a project for use with YakOS:
  - Creates ~/agent-control/<n>/ with work/current/{logs,artifacts,reports}/
  - Copies the framework's reference hook scripts into
    <project>/scripts/hooks/, each with a .framework-hash sibling so
    'yakos doctor' can detect drift later.
  - Creates <project>/.claude/{settings.json,path-allowlist.json} from
    templates if those files don't already exist.
  - Ensures ~/.claude/projects/<encoded>/MEMORY.md exists for auto-memory.

Arguments:
  <name>             Short identifier for this project (alphanumeric,
                     hyphen, underscore).
  --project <path>   Absolute path to the project's git repo.
  --force            Overwrite existing files in <project>/scripts/hooks/.
                     Other files are never overwritten in v0.1.
  --with-gate        Also install the pre-push version gate into
                     <project>/.git/hooks/pre-push. Equivalent to
                     running 'yakos git-hooks install' from the project
                     after init. Refuses if a non-YakOS pre-push hook
                     exists (rerun with --force to overwrite).
  --help, -h         Print this help.
EOF
}

# ---- parse args -------------------------------------------------------------

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --project)
            shift
            [ "$#" -gt 0 ] || ct_die "init: --project requires a path"
            PROJECT="$1"
            ;;
        --project=*)
            PROJECT="${1#--project=}"
            ;;
        --force) FORCE=1 ;;
        --with-gate) WITH_GATE=1 ;;
        --*) ct_die "init: unknown flag '$1' (try --help)" ;;
        *)
            if [ -z "$NAME" ]; then
                NAME="$1"
            else
                ct_die "init: unexpected positional argument '$1'"
            fi
            ;;
    esac
    shift
done

[ -n "$NAME" ] || { usage >&2; ct_die "init: missing <name>"; }
[ -n "$PROJECT" ] || { usage >&2; ct_die "init: --project <path> is required"; }

case "$NAME" in
    *[!A-Za-z0-9_-]*)
        ct_die "init: <name> must be alphanumeric (with - or _ allowed): got '$NAME'"
        ;;
esac

# ---- validate project --------------------------------------------------------

if [ ! -d "$PROJECT" ]; then
    ct_die "init: project path not found: $PROJECT"
fi
PROJECT_ABS="$(ct_realpath "$PROJECT")"

if [ ! -d "$PROJECT_ABS/.git" ] && ! git -C "$PROJECT_ABS" rev-parse --git-dir >/dev/null 2>&1; then
    ct_die "init: $PROJECT_ABS is not a git repository"
fi

CONTROL_DIR="$HOME/agent-control/$NAME"
WORK_DIR="$CONTROL_DIR/work"
CURRENT_DIR="$WORK_DIR/current"

ct_log "yakos init '$NAME' --project $PROJECT_ABS"
ct_log "control dir: $CONTROL_DIR"

if [ -d "$CONTROL_DIR" ] && [ "$FORCE" != "1" ]; then
    ct_log "note: $CONTROL_DIR already exists; existing files will be preserved (use --force to refresh hook copies)"
fi

# ---- 2-7: agent-control tree ------------------------------------------------

mkdir -p "$CURRENT_DIR/logs" "$CURRENT_DIR/artifacts" "$CURRENT_DIR/reports" "$WORK_DIR/archive"

# .session-started-history.ndjson — append-only NDJSON; team-lifecycle.sh
# appends a "team_created" event on each TeamCreate. Empty file is a valid
# zero-line NDJSON.
SESSION_HISTORY="$CURRENT_DIR/.session-started-history.ndjson"
if [ ! -f "$SESSION_HISTORY" ]; then
    : > "$SESSION_HISTORY"
    ct_log "wrote $SESSION_HISTORY"
fi
# Migrate legacy JSON-array file from Batch 1B if present
LEGACY_HISTORY="$CURRENT_DIR/.session-started-history"
if [ -f "$LEGACY_HISTORY" ] && jq -e 'type == "array"' "$LEGACY_HISTORY" >/dev/null 2>&1; then
    jq -c '.[]?' "$LEGACY_HISTORY" >> "$SESSION_HISTORY" 2>/dev/null || true
    rm -f "$LEGACY_HISTORY"
    ct_log "migrated legacy $LEGACY_HISTORY → $SESSION_HISTORY"
fi

# .gitignore
GIT_IGNORE="$CONTROL_DIR/.gitignore"
if [ ! -f "$GIT_IGNORE" ]; then
    cp "$YAKOS_ROOT/lib/settings/agent-control.gitignore.template" "$GIT_IGNORE"
    ct_log "wrote $GIT_IGNORE"
fi

# settings.local.json template
LOCAL_SETTINGS="$CONTROL_DIR/settings.local.json"
if [ ! -f "$LOCAL_SETTINGS" ]; then
    cp "$YAKOS_ROOT/lib/settings/settings.local.template.json" "$LOCAL_SETTINGS"
    ct_log "wrote $LOCAL_SETTINGS"
fi

# hook-bypass.md template
BYPASS_FILE="$CURRENT_DIR/hook-bypass.md"
if [ ! -f "$BYPASS_FILE" ]; then
    cp "$YAKOS_ROOT/lib/settings/hook-bypass.template.md" "$BYPASS_FILE"
    ct_log "wrote $BYPASS_FILE"
fi

# sessions.ndjson — empty until first archive
SESSIONS_LOG="$WORK_DIR/sessions.ndjson"
[ -f "$SESSIONS_LOG" ] || { : > "$SESSIONS_LOG"; ct_log "wrote $SESSIONS_LOG"; }

# .project-path — pointer back to the project repo; consumed by
# 'yakos team restart' and 'yakos status'
PROJECT_PATH_FILE="$CONTROL_DIR/.project-path"
printf '%s\n' "$PROJECT_ABS" > "$PROJECT_PATH_FILE"

# ---- 8: copy reference hooks into <project>/scripts/hooks/ ------------------

PROJECT_HOOKS_SRC="$YAKOS_ROOT/lib/hooks"
PROJECT_HOOKS_DST="$PROJECT_ABS/scripts/hooks"
mkdir -p "$PROJECT_HOOKS_DST"

hook_copied=0
hook_skipped=0
hook_overwritten=0

if [ -d "$PROJECT_HOOKS_SRC" ]; then
    while IFS= read -r src; do
        [ -n "$src" ] || continue
        rel="${src#$PROJECT_HOOKS_SRC/}"
        # Skip framework-only files. `git/` is the YakOS git-hooks subdir
        # (project-side git hooks like pre-push-version-gate.sh) — those
        # belong in <repo>/.git/hooks/, NOT in <project>/scripts/hooks/,
        # and are managed by `yakos git-hooks install`.
        case "$rel" in
            .gitkeep|README.md|*/README.md) continue ;;
            git/*) continue ;;
        esac
        dst="$PROJECT_HOOKS_DST/$rel"
        mkdir -p "$(dirname -- "$dst")"
        if [ -e "$dst" ] && [ "$FORCE" != "1" ]; then
            hook_skipped=$((hook_skipped + 1))
            continue
        fi
        if [ -e "$dst" ]; then
            hook_overwritten=$((hook_overwritten + 1))
        else
            hook_copied=$((hook_copied + 1))
        fi
        cp "$src" "$dst"
        chmod +x "$dst" 2>/dev/null || true
        # framework-hash sibling
        ct_sha256 "$src" > "${dst}.framework-hash"
    done < <(find -L "$PROJECT_HOOKS_SRC" -type f 2>/dev/null)
fi

ct_log "hooks copied: $hook_copied new, $hook_overwritten overwritten, $hook_skipped skipped (use --force to refresh existing)"

# ---- 9-10: project .claude templates ----------------------------------------

PROJECT_CLAUDE="$PROJECT_ABS/.claude"
mkdir -p "$PROJECT_CLAUDE"

PATH_ALLOWLIST="$PROJECT_CLAUDE/path-allowlist.json"
if [ ! -f "$PATH_ALLOWLIST" ]; then
    cp "$YAKOS_ROOT/lib/settings/path-allowlist.template.json" "$PATH_ALLOWLIST"
    ct_log "wrote $PATH_ALLOWLIST"
fi

PROJECT_SETTINGS="$PROJECT_CLAUDE/settings.json"
if [ ! -f "$PROJECT_SETTINGS" ]; then
    cp "$YAKOS_ROOT/lib/settings/settings.template.json" "$PROJECT_SETTINGS"
    ct_log "wrote $PROJECT_SETTINGS"
fi

# ---- 11: auto-memory MEMORY.md ----------------------------------------------

ENCODED="$(ct_encode_project_path "$PROJECT_ABS")"
MEMORY_DIR="$HOME/.claude/projects/$ENCODED"
MEMORY_FILE="$MEMORY_DIR/MEMORY.md"
mkdir -p "$MEMORY_DIR"
if [ ! -f "$MEMORY_FILE" ]; then
    cat > "$MEMORY_FILE" <<EOF
# Memory index for $NAME

Project repo: $PROJECT_ABS
Initialized:  $(ct_iso_now_z)

This file is the auto-memory index for the project at the path above.
Add entries below as one-line links to memory files in this directory.
Lines past 200 may be truncated, so keep entries concise.

EOF
    ct_log "wrote $MEMORY_FILE"
fi

# ---- .yakos.yml project config (Plan 4 / v0.18+) ---------------------------
# Drop a sensible default at the project root. Idempotent; only writes if
# absent. Operator edits to opt into standards and environments.
YAKOS_YML="$PROJECT_ABS/.yakos.yml"
YAKOS_YML_TEMPLATE="$YAKOS_ROOT/lib/settings/yakos.yml.template"
if [ -f "$YAKOS_YML_TEMPLATE" ] && [ ! -f "$YAKOS_YML" ]; then
    cp "$YAKOS_YML_TEMPLATE" "$YAKOS_YML"
    ct_log "wrote $YAKOS_YML (edit to opt into standards + environments)"
fi

# ---- ensure project .gitignore covers yakOS-emitted runtime agent files -----
# yakOS materializes per-runtime agent files at <project>/.codex/agents/yakos-*.toml
# and <project>/.gemini/agents/yakos-*.md (v0.4.0+). They MUST NOT land in
# project commits — add the patterns to .gitignore if absent.
PROJECT_GITIGNORE="$PROJECT_ABS/.gitignore"
yakos_gi_marker="# yakOS — runtime-emitted agent files (v0.4.0+)"
if [ -f "$PROJECT_GITIGNORE" ] && grep -qF "$yakos_gi_marker" "$PROJECT_GITIGNORE" 2>/dev/null; then
    : # already present
else
    {
        printf '\n%s\n' "$yakos_gi_marker"
        printf '.codex/agents/yakos-*.toml\n'
        printf '.gemini/agents/yakos-*.md\n'
        printf '.gemini/settings.json.yakos-bak-*\n'
        printf '.agents/skills/yakos-*.md\n'
        printf '.agents/mcp_config.json\n'
        printf '.agents/hooks.json\n'
    } >> "$PROJECT_GITIGNORE"
    ct_log "appended yakos runtime-agent patterns to $PROJECT_GITIGNORE"
fi

# ---- optional: install pre-push version gate -------------------------------

GATE_STATUS="not requested (--with-gate to enable)"
if [ "$WITH_GATE" = "1" ]; then
    GATE_DST="$PROJECT_ABS/.git/hooks/pre-push"
    GATE_HASH_SIBLING="${GATE_DST}.framework-hash"
    GATE_SRC="$YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh"
    if [ ! -f "$GATE_SRC" ]; then
        ct_log "WARN: --with-gate requested but gate source missing at $GATE_SRC; skipping"
        GATE_STATUS="skipped (source missing)"
    elif [ -e "$GATE_DST" ] && [ "$FORCE" != "1" ]; then
        # Is it ours?
        if [ -f "$GATE_HASH_SIBLING" ]; then
            ct_log "pre-push gate already installed at $GATE_DST"
            GATE_STATUS="already installed"
        else
            ct_log "WARN: --with-gate requested but a non-YakOS pre-push hook exists at $GATE_DST; rerun with --force to overwrite"
            GATE_STATUS="skipped (non-YakOS hook present)"
        fi
    else
        mkdir -p "$(dirname -- "$GATE_DST")"
        cp "$GATE_SRC" "$GATE_DST"
        chmod +x "$GATE_DST"
        ct_sha256 "$GATE_SRC" > "$GATE_HASH_SIBLING"
        ct_log "installed pre-push version gate at $GATE_DST"
        GATE_STATUS="installed"
    fi
fi

# ---- summary ----------------------------------------------------------------

cat <<EOF

YakOS init complete for '$NAME'.

  Control dir:  $CONTROL_DIR
  Project:      $PROJECT_ABS
  Auto-memory:  $MEMORY_FILE
  Hooks:        $hook_copied new, $hook_overwritten overwritten, $hook_skipped skipped
  Push gate:    $GATE_STATUS

To start a session:
  yakos start $NAME

(Or run bare 'yakos' from $CONTROL_DIR or $PROJECT_ABS — auto-detects.)
Use 'yakos start $NAME --dry-run' to preview the launch command;
'yakos start $NAME --safe' to enable permission prompts.

Customize:
  $PATH_ALLOWLIST
  $PROJECT_SETTINGS
  $PROJECT_HOOKS_DST/*.sh
EOF
