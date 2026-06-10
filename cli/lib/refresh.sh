#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: refresh.sh — detect and repair per-project deployment drift.
#
# Fixes three classes of drift that accumulate when the framework evolves
# after a project was first bootstrapped:
#
#   1. Hook script drift   — <project>/scripts/hooks/*.sh out of date vs
#                            lib/hooks/*.sh (and lib/hooks/lib/)
#   2. settings.json drift — hook registrations missing or superseded vs
#                            lib/settings/settings.template.json
#   3. Agent symlink drift — ~/.claude/agents/<id>.md missing or wrong
#                            vs lib/agents/<id>.md
#
# Subcommand: yakos refresh [--project <path>|--all] [--dry-run]
#
# Project discovery:
#   --project <path>  use that path
#   --all             scan ~/agent-control/*/.project-path + ~/github/*/.claude/settings.json
#   (default)         infer from cwd, same logic as yakos start

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos refresh'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos refresh'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

# ---- parse args -------------------------------------------------------------

DRY_RUN=0
EXPLICIT_PROJECT=""
ALL_PROJECTS=0

usage() {
    cat <<EOF
yakos refresh [--project <path>|--all] [--dry-run]

Detect and repair per-project deployment drift:
  - Hook scripts in <project>/scripts/hooks/ synced from lib/hooks/
  - settings.json hook registrations smart-merged from lib/settings/settings.template.json
  - ~/.claude/agents/ symlinks refreshed from lib/agents/

Options:
  --project <path>  Repair a specific project path.
  --all             Discover all wired projects (~/agent-control/*/ +
                    ~/github/*/.claude/settings.json) and refresh each.
  --dry-run         Print what WOULD change without writing anything.
  --help, -h        Print this help.

Without --project or --all, infers from cwd (same as yakos start).

Exit codes:
  0   Success (including no-op when already in sync)
  1   Error (bad project path, corrupt JSON, etc.)
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --dry-run) DRY_RUN=1 ;;
        --all) ALL_PROJECTS=1 ;;
        --project)
            shift
            [ "$#" -gt 0 ] || ct_die "refresh: --project requires a path"
            EXPLICIT_PROJECT="$1"
            ;;
        --project=*)
            EXPLICIT_PROJECT="${1#--project=}"
            ;;
        *)
            ct_die "refresh: unknown argument '$1' (try --help)"
            ;;
    esac
    shift
done

# ---- project discovery ------------------------------------------------------

_is_yakos_project() {
    # A directory is "yakos-wired" if it has a .claude/settings.json that
    # references scripts/hooks/ (the marker left by yakos init).
    local dir="$1"
    local sf="$dir/.claude/settings.json"
    [ -f "$sf" ] && grep -q 'scripts/hooks/' "$sf" 2>/dev/null
}

_collect_projects() {
    # Emit one project path per line. No duplicates.
    local ac_root="$HOME/agent-control"
    local gh_root="$HOME/github"
    local seen=""

    # From agent-control .project-path files (most reliable)
    if [ -d "$ac_root" ]; then
        for pp in "$ac_root"/*/.project-path; do
            [ -f "$pp" ] || continue
            proj="$(head -1 "$pp" 2>/dev/null)"
            [ -n "$proj" ] || continue
            [ -d "$proj" ] || continue
            proj_abs="$(ct_realpath "$proj")"
            case " $seen " in *" $proj_abs "*) continue;; esac
            seen="$seen $proj_abs"
            echo "$proj_abs"
        done
    fi

    # From ~/github/* .claude/settings.json that look yakos-wired
    if [ -d "$gh_root" ]; then
        for sf in "$gh_root"/*/.claude/settings.json; do
            [ -f "$sf" ] || continue
            grep -q 'scripts/hooks/' "$sf" 2>/dev/null || continue
            proj_abs="$(ct_realpath "$(dirname -- "$(dirname -- "$sf")")")"
            case " $seen " in *" $proj_abs "*) continue;; esac
            seen="$seen $proj_abs"
            echo "$proj_abs"
        done
    fi
}

_infer_project_from_cwd() {
    local cwd_real
    cwd_real="$(cd "$PWD" 2>/dev/null && pwd -P)"
    local ac_root="$HOME/agent-control"

    # Inside ~/agent-control/<name>/ → read .project-path
    case "$cwd_real" in
        "$ac_root"/*)
            local rest="${cwd_real#$ac_root/}"
            local name="${rest%%/*}"
            local pp="$ac_root/$name/.project-path"
            if [ -f "$pp" ]; then
                head -1 "$pp" 2>/dev/null
                return
            fi
            ;;
    esac

    # Inside a known project repo
    if [ -d "$ac_root" ]; then
        for pp in "$ac_root"/*/.project-path; do
            [ -f "$pp" ] || continue
            proj="$(head -1 "$pp" 2>/dev/null)"
            proj_real="$(cd "$proj" 2>/dev/null && pwd -P || true)"
            [ -n "$proj_real" ] || continue
            case "$cwd_real" in
                "$proj_real"|"$proj_real"/*) echo "$proj_real"; return ;;
            esac
        done
    fi

    # cwd itself looks yakos-wired
    if _is_yakos_project "$cwd_real"; then
        echo "$cwd_real"
        return
    fi

    echo ""
}

# ---- hook script sync (Phase 2) ---------------------------------------------

# Counters (global, reset per project inside _refresh_one)
H_NEW=0
H_SYNC=0
H_OK=0

_sync_hooks() {
    local src_root="$1"    # lib/hooks
    local dst_root="$2"    # <project>/scripts/hooks

    H_NEW=0; H_SYNC=0; H_OK=0

    [ -d "$src_root" ] || return 0

    while IFS= read -r src; do
        [ -n "$src" ] || continue
        rel="${src#$src_root/}"
        # Skip non-hook files (same list as init.sh)
        case "$rel" in
            .gitkeep|README.md|*/README.md) continue ;;
            git/*) continue ;;
        esac
        dst="$dst_root/$rel"
        hash_file="${dst}.framework-hash"

        if [ ! -e "$dst" ]; then
            # NEW
            if [ "$DRY_RUN" = "1" ]; then
                printf '    [dry-run] hooks: would copy NEW  %s\n' "$rel"
            else
                mkdir -p "$(dirname -- "$dst")"
                cp "$src" "$dst"
                chmod +x "$dst" 2>/dev/null || true
                ct_sha256 "$src" > "$hash_file"
            fi
            H_NEW=$((H_NEW + 1))
        else
            # Exists — compare
            src_hash="$(ct_sha256 "$src")"
            dst_hash="$(ct_sha256 "$dst")"
            if [ "$src_hash" != "$dst_hash" ]; then
                # SYNC (different content)
                if [ "$DRY_RUN" = "1" ]; then
                    printf '    [dry-run] hooks: would sync STALE %s\n' "$rel"
                else
                    cp "$src" "$dst"
                    chmod +x "$dst" 2>/dev/null || true
                    echo "$src_hash" > "$hash_file"
                fi
                H_SYNC=$((H_SYNC + 1))
            else
                # OK — ensure hash sidecar is current even if content matches
                if [ "$DRY_RUN" != "1" ]; then
                    echo "$src_hash" > "$hash_file"
                fi
                H_OK=$((H_OK + 1))
            fi
        fi
    done < <(find -L "$src_root" -type f 2>/dev/null)
}

# ---- settings.json smart merge (Phase 3) ------------------------------------

# Counters (global, reset per project)
S_ADDED=0
S_REMOVED=0

_merge_settings() {
    local template_file="$1"
    local deployed_file="$2"

    S_ADDED=0; S_REMOVED=0

    [ -f "$template_file" ] || { ct_log "refresh: template not found at $template_file"; return 1; }
    [ -f "$deployed_file" ] || { ct_log "refresh: deployed settings not found at $deployed_file"; return 1; }

    if ! ct_json_valid "$template_file"; then
        ct_log "refresh: template JSON invalid at $template_file"
        return 1
    fi
    if ! ct_json_valid "$deployed_file"; then
        ct_log "refresh: deployed settings invalid JSON at $deployed_file"
        return 1
    fi

    # Use Python for the smart merge — matches the reference algorithm
    # exactly and handles edge cases better than a pure jq pipeline.
    # Stats are passed back via a temp file (cleanest cross-platform approach;
    # avoids the stderr-in-$()-subshell capture problem).
    local _stats_file
    _stats_file="$(mktemp /tmp/yakos-merge-stats-XXXXXX)"
    export _YAKOS_MERGE_STATS_FILE="$_stats_file"

    local py_output
    py_output="$(python3 - "$template_file" "$deployed_file" <<'PY'
import json
import sys

template_path = sys.argv[1]
deployed_path = sys.argv[2]

with open(template_path, encoding="utf-8") as fh:
    template = json.load(fh)
with open(deployed_path, encoding="utf-8") as fh:
    deployed = json.load(fh)

# Build a lookup: (event, command) -> matcher in template
# This lets us detect when a deployed entry uses a superseded matcher.
template_cmd_matcher = {}
for event, entries in template.get("hooks", {}).items():
    for entry in entries:
        m = entry.get("matcher", "*")
        for h in entry.get("hooks", []):
            cmd = h.get("command", "")
            if cmd:
                template_cmd_matcher[(event, cmd)] = m

# Build a set of (event, matcher, command) triples present in template
# for use in Phase B (add missing).
template_triples = set()
template_entries_by_event = {}  # event -> list of entry dicts (with matcher + hooks list)
for event, entries in template.get("hooks", {}).items():
    template_entries_by_event[event] = entries
    for entry in entries:
        m = entry.get("matcher", "*")
        for h in entry.get("hooks", []):
            cmd = h.get("command", "")
            if cmd:
                template_triples.add((event, m, cmd))

# Build set of (event, matcher, command) already in deployed
deployed_triples = set()
for event, entries in deployed.get("hooks", {}).items():
    for entry in entries:
        m = entry.get("matcher", "*")
        for h in entry.get("hooks", []):
            cmd = h.get("command", "")
            if cmd:
                deployed_triples.add((event, m, cmd))

stats = {"removed": 0, "added": 0}

# PHASE A: remove superseded
# For each hook in deployed whose (event, command) is in template with a
# DIFFERENT matcher, remove it. Template matcher wins.
for event in list(deployed.get("hooks", {}).keys()):
    entries = deployed["hooks"][event]
    new_entries = []
    for entry in entries:
        m = entry.get("matcher", "*")
        kept_hooks = []
        for h in entry.get("hooks", []):
            cmd = h.get("command", "")
            key = (event, cmd)
            if key in template_cmd_matcher and template_cmd_matcher[key] != m:
                stats["removed"] += 1
                continue
            kept_hooks.append(h)
        if kept_hooks:
            entry = dict(entry)
            entry["hooks"] = kept_hooks
            new_entries.append(entry)
        # else: entire entry dropped (all hooks removed)
    deployed["hooks"][event] = new_entries

# PHASE D: drop empty event keys after Phase A removals, BUT only if
# the event is not present in the template. Template-present empty
# arrays (like "TeammateIdle": []) are intentional and should be kept.
template_events = set(template.get("hooks", {}).keys())
for event in list(deployed.get("hooks", {}).keys()):
    if not deployed["hooks"][event] and event not in template_events:
        del deployed["hooks"][event]

# Re-compute deployed triples after Phase A removals so Phase B is accurate
deployed_triples_after_a = set()
for event, entries in deployed.get("hooks", {}).items():
    for entry in entries:
        m = entry.get("matcher", "*")
        for h in entry.get("hooks", []):
            cmd = h.get("command", "")
            if cmd:
                deployed_triples_after_a.add((event, m, cmd))

# PHASE B: add missing
# For each (event, matcher, command) in template not in deployed (after Phase A),
# add it. We add whole template entry blocks when we can, to preserve _doc fields.
# We also preserve the ordering from the template for newly added entries.
for event, t_entries in template_entries_by_event.items():
    if "hooks" not in deployed:
        deployed["hooks"] = {}
    for t_entry in t_entries:
        t_matcher = t_entry.get("matcher", "*")
        t_hooks = t_entry.get("hooks", [])
        # Determine which hooks from this template entry are missing
        missing_hooks = []
        for h in t_hooks:
            cmd = h.get("command", "")
            if cmd and (event, t_matcher, cmd) not in deployed_triples_after_a:
                missing_hooks.append(h)
                stats["added"] += 1
        if not missing_hooks:
            continue
        # Find or create a matching entry block in deployed
        if event not in deployed["hooks"]:
            deployed["hooks"][event] = []
        found_entry = None
        for d_entry in deployed["hooks"][event]:
            if d_entry.get("matcher", "*") == t_matcher:
                found_entry = d_entry
                break
        if found_entry is not None:
            # Append missing hooks to existing entry
            found_entry["hooks"] = found_entry.get("hooks", []) + missing_hooks
        else:
            # Create a new entry block, copying _doc from template if present
            new_entry = {}
            if "matcher" in t_entry:
                new_entry["matcher"] = t_entry["matcher"]
            if "_doc" in t_entry:
                new_entry["_doc"] = t_entry["_doc"]
            if "_doc_hook_order" in t_entry:
                new_entry["_doc_hook_order"] = t_entry["_doc_hook_order"]
            new_entry["hooks"] = missing_hooks
            deployed["hooks"][event].append(new_entry)

# PHASE C is implicit: deployed-only triples (project-local additions like
# kanban-stop.sh) are never removed because we only add or remove hooks
# that appear in the template's (event, command) key space.

print(json.dumps(deployed, indent=2, ensure_ascii=False))
sys.stdout.flush()
# Write stats to a named temp file; the shell reads it after this subprocess exits.
import os, tempfile
stats_path = os.environ.get("_YAKOS_MERGE_STATS_FILE", "")
if stats_path:
    with open(stats_path, "w") as sf:
        sf.write(f"{stats['removed']}:{stats['added']}\n")
PY
    )"

    local stats_line
    stats_line="$(cat "${_stats_file}" 2>/dev/null | tr -d '[:space:]' || true)"
    rm -f "$_stats_file"
    unset _YAKOS_MERGE_STATS_FILE

    # Parse stats: <removed>:<added>
    S_REMOVED=0; S_ADDED=0
    if [ -n "$stats_line" ]; then
        S_REMOVED="$(echo "$stats_line" | cut -d: -f1)"
        S_ADDED="$(echo "$stats_line" | cut -d: -f2)"
    fi

    # Validate the merged JSON before writing
    if ! echo "$py_output" | jq empty >/dev/null 2>&1; then
        ct_log "refresh: smart merge produced invalid JSON — aborting settings.json update"
        return 1
    fi

    if [ "$DRY_RUN" = "1" ]; then
        if [ "$S_REMOVED" -gt 0 ] || [ "$S_ADDED" -gt 0 ]; then
            printf '    [dry-run] settings: would remove %d superseded, add %d missing registrations\n' \
                "$S_REMOVED" "$S_ADDED"
        fi
        return 0
    fi

    # If nothing changed, skip the write entirely to preserve original formatting
    if [ "$S_ADDED" -eq 0 ] && [ "$S_REMOVED" -eq 0 ]; then
        return 0
    fi

    # Atomic write: temp → mv
    local tmp="${deployed_file}.yakos-refresh-tmp-$$"
    echo "$py_output" > "$tmp"
    # Final validation of temp file
    if ! jq empty "$tmp" >/dev/null 2>&1; then
        rm -f "$tmp"
        ct_log "refresh: temp file validation failed — original settings.json preserved"
        return 1
    fi
    mv "$tmp" "$deployed_file"
}

# ---- agent symlinks (Phase 4) -----------------------------------------------

# Counters
A_NEW=0
A_OK=0
A_WARN=0

_sync_agents() {
    local agents_src="$YAKOS_ROOT/lib/agents"
    local agents_dst="$HOME/.claude/agents"

    A_NEW=0; A_OK=0; A_WARN=0

    [ -d "$agents_src" ] || return 0

    if [ "$DRY_RUN" != "1" ]; then
        mkdir -p "$agents_dst"
    fi

    while IFS= read -r src; do
        [ -n "$src" ] || continue
        # Only top-level .md files (not README, not subdirs)
        rel="$(basename -- "$src")"
        case "$rel" in
            README.md) continue ;;
            *.md) : ;;
            *) continue ;;
        esac
        dst="$agents_dst/$rel"
        if [ ! -e "$dst" ] && [ ! -L "$dst" ]; then
            # NEW
            if [ "$DRY_RUN" = "1" ]; then
                printf '    [dry-run] agents: would create symlink %s\n' "$rel"
            else
                ln -s "$src" "$dst"
            fi
            A_NEW=$((A_NEW + 1))
        elif [ -L "$dst" ]; then
            # Symlink — verify target
            current_target="$(readlink "$dst" 2>/dev/null || true)"
            if [ "$current_target" != "$src" ]; then
                if [ "$DRY_RUN" = "1" ]; then
                    printf '    [dry-run] agents: would refresh symlink %s (was %s)\n' "$rel" "$current_target"
                    A_NEW=$((A_NEW + 1))
                else
                    ln -sf "$src" "$dst"
                    A_NEW=$((A_NEW + 1))
                fi
            else
                A_OK=$((A_OK + 1))
            fi
        else
            # Real file — leave alone + warn
            printf '    [warn] agents: %s is a real file (not a symlink) — operator-managed, skipping\n' "$dst"
            A_WARN=$((A_WARN + 1))
        fi
    done < <(find "$agents_src" -maxdepth 1 -name "*.md" -type f 2>/dev/null)
}

# ---- refresh a single project -----------------------------------------------

_refresh_one() {
    local proj="$1"
    local proj_abs
    proj_abs="$(ct_realpath "$proj" 2>/dev/null || echo "$proj")"

    if [ ! -d "$proj_abs" ]; then
        ct_log "refresh: project path not found: $proj_abs"
        return 1
    fi

    local hooks_src="$YAKOS_ROOT/lib/hooks"
    local hooks_dst="$proj_abs/scripts/hooks"
    local template="$YAKOS_ROOT/lib/settings/settings.template.json"
    local deployed="$proj_abs/.claude/settings.json"

    echo "  project: $proj_abs"

    # Phase 2: hook script sync
    if [ "$DRY_RUN" != "1" ]; then
        mkdir -p "$hooks_dst"
    fi
    _sync_hooks "$hooks_src" "$hooks_dst"
    local hooks_summary="new=$H_NEW synced=$H_SYNC ok=$H_OK"

    # Phase 3: settings.json smart merge
    local settings_summary="added=0 removed=0"
    if [ -f "$deployed" ]; then
        _merge_settings "$template" "$deployed"
        settings_summary="added=$S_ADDED removed=$S_REMOVED"
    else
        settings_summary="skipped (no .claude/settings.json)"
    fi

    # Phase 4: agent symlinks (done once globally, not per-project)
    # Caller handles this.

    # Summary line
    local drift="in sync"
    if [ "$H_NEW" -gt 0 ] || [ "$H_SYNC" -gt 0 ] || \
       [ "$S_ADDED" -gt 0 ] || [ "$S_REMOVED" -gt 0 ]; then
        drift="drift detected + repaired"
        [ "$DRY_RUN" = "1" ] && drift="drift detected (dry-run)"
    fi

    echo "    hooks:    $hooks_summary"
    echo "    settings: $settings_summary"
    echo "    status:   $drift"
}

# ---- main -------------------------------------------------------------------

HOOKS_SRC="$YAKOS_ROOT/lib/hooks"
TEMPLATE="$YAKOS_ROOT/lib/settings/settings.template.json"

if [ ! -d "$HOOKS_SRC" ]; then
    ct_die "refresh: lib/hooks not found at $HOOKS_SRC (bad YAKOS_ROOT?)"
fi
if [ ! -f "$TEMPLATE" ]; then
    ct_die "refresh: settings template not found at $TEMPLATE"
fi

if ! command -v python3 >/dev/null 2>&1; then
    ct_die "refresh: python3 is required for settings.json merge (brew install python3)"
fi
if ! command -v jq >/dev/null 2>&1; then
    ct_die "refresh: jq is required (brew install jq)"
fi

DRY_TAG=""
[ "$DRY_RUN" = "1" ] && DRY_TAG=" [DRY RUN]"
echo "yakos refresh${DRY_TAG}"
echo ""

# Collect target projects
TARGET_PROJECTS=""

if [ -n "$EXPLICIT_PROJECT" ]; then
    TARGET_PROJECTS="$EXPLICIT_PROJECT"
elif [ "$ALL_PROJECTS" = "1" ]; then
    TARGET_PROJECTS="$(_collect_projects)"
    if [ -z "$TARGET_PROJECTS" ]; then
        echo "No yakos-wired projects found under ~/agent-control/ or ~/github/."
        echo "Run 'yakos init <name> --project <path>' to bootstrap a project."
        exit 0
    fi
else
    # Infer from cwd
    inferred="$(_infer_project_from_cwd)"
    if [ -z "$inferred" ]; then
        ct_die "refresh: cannot infer project from cwd '$PWD'. Use --project <path> or --all."
    fi
    TARGET_PROJECTS="$inferred"
fi

# Phase 4: agent symlinks (once, globally, not per-project)
echo "Agent symlinks (~/.claude/agents/)"
_sync_agents
echo "  new=$A_NEW ok=$A_OK warns=$A_WARN"
echo ""

# Per-project phases
total=0
echo "Project hook + settings refresh:"
while IFS= read -r proj; do
    [ -n "$proj" ] || continue
    total=$((total + 1))
    # Capture drift before refresh
    _refresh_one "$proj"
    echo ""
done <<< "$TARGET_PROJECTS"

echo "Summary: $total project(s) processed"
if [ "$DRY_RUN" = "1" ]; then
    echo "(dry-run: no files written)"
fi
