#!/usr/bin/env bash
# doctor.sh — verify YakOS install + environment health.
#
# Exits 0 if everything looks good or only WARN findings; exits 1 if any
# ERROR-severity finding is reported.

set -eu

: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos doctor'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

PROJECT_PATH=""
PROBE_RUNTIME=0
FIX=0
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos doctor [<project-path>] [--probe-runtime] — verify YakOS install + environment health

Without arguments, checks:
    Required commands (bash, git, jq)
    Optional commands (gtimeout, gsed, shellcheck, python3) — surfaced as INFO
    ~/.yakos pointer exists and resolves to an existing repo
    Symlinks under ~/.claude/{agents,skills,rules,playbooks}/ that target
        YakOS resolve cleanly
    ~/.claude/settings.json is valid JSON if present
    ~/.claude/projects/ is intact (informational; never modified)

If <project-path> is passed, additionally checks:
    For each file in <project>/scripts/hooks/, compares the file's SHA-256
    against its .framework-hash sibling (written by 'yakos init') and
    surfaces DRIFT (informational, not an error — projects are expected
    to customize).
    Pre-push version gate installation status and drift.

If --fix is passed, attempts auto-remediation of cheap fixes:
    - missing ~/.yakos-state subdirs (memory, runtime-probes)
    - missing yakOS gitignore patterns in <project>/.gitignore
    - missing per-project .session-started-history.ndjson
    - missing or stale .framework-hash siblings on hook scripts
      (only refreshes when the hook content matches framework src;
      preserves intentional project drift)

If --probe-runtime is passed, additionally reports:
    Filesystem-side state of Claude Code Agent Teams (~/.claude/teams/,
    ~/.claude/tasks/, count of active teams, inbox files).
    The last known state of in-session-only runtime tools (TaskCreate /
    TaskList / TaskUpdate) recorded at ~/.yakos-state/runtime-probe.json.
    The exact prompt to ask in a Claude Code session to refresh the
    last-known state.

Usage: yakos doctor [<project-path>] [--probe-runtime]

Exit code:
    0   No errors (warnings/info/drift OK)
    1   One or more errors
EOF
            exit 0
            ;;
        --probe-runtime) PROBE_RUNTIME=1 ;;
        --fix) FIX=1 ;;
        --*)
            ct_die "doctor: unknown flag '$arg'"
            ;;
        *)
            if [ -z "$PROJECT_PATH" ]; then PROJECT_PATH="$arg"
            else ct_die "doctor: too many positional args"
            fi
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

# ---- project hook drift (only when project path passed) --------------------

if [ -n "$PROJECT_PATH" ]; then
    echo "Project hook drift: $PROJECT_PATH/scripts/hooks/"
    if [ ! -d "$PROJECT_PATH" ]; then
        err "project path not found: $PROJECT_PATH"
    elif [ ! -d "$PROJECT_PATH/scripts/hooks" ]; then
        info "$PROJECT_PATH/scripts/hooks/ does not exist (run 'yakos init <name> --project $PROJECT_PATH')"
    else
        drift=0
        unhashed=0
        clean=0
        while IFS= read -r f; do
            [ -n "$f" ] || continue
            hash_file="${f}.framework-hash"
            if [ ! -f "$hash_file" ]; then
                unhashed=$((unhashed + 1))
                continue
            fi
            expected="$(cat "$hash_file")"
            actual="$(ct_sha256 "$f")"
            if [ "$expected" = "$actual" ]; then
                clean=$((clean + 1))
            else
                rel="${f#$PROJECT_PATH/}"
                info "DRIFT: $rel  (expected $expected, got $actual; not an error — projects are expected to customize)"
                drift=$((drift + 1))
            fi
        done < <(find "$PROJECT_PATH/scripts/hooks" -type f \
                       ! -name '*.framework-hash' \
                       ! -name '.gitkeep' \
                       ! -name 'README.md' 2>/dev/null)
        ok "$clean clean, $drift drifted, $unhashed unhashed (no .framework-hash sibling)"
    fi
    echo ""

    # Pre-push version gate status (project-side git hook)
    echo "Pre-push version gate: $PROJECT_PATH/.git/hooks/pre-push"
    GATE_DST="$PROJECT_PATH/.git/hooks/pre-push"
    GATE_HASH_SIBLING="${GATE_DST}.framework-hash"
    GATE_SRC="$YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh"
    if [ ! -e "$GATE_DST" ]; then
        info "not installed (install: 'yakos git-hooks install' from inside the project, or 'yakos init <name> --project $PROJECT_PATH --with-gate')"
    elif [ ! -f "$GATE_HASH_SIBLING" ]; then
        info "present but not YakOS-owned (no .framework-hash sibling)"
    elif [ ! -f "$GATE_SRC" ]; then
        info "installed but framework source missing at $GATE_SRC"
    else
        gate_expected="$(cat "$GATE_HASH_SIBLING")"
        gate_actual_src="$(ct_sha256 "$GATE_SRC")"
        if [ "$gate_expected" = "$gate_actual_src" ]; then
            ok "installed (current version, no drift)"
        else
            info "DRIFT: installed gate differs from framework (refresh: 'yakos git-hooks install --force')"
        fi
    fi
    echo ""
fi

# ---- optional integrations (informational; never blocks) -------------------
#
# Local LLM runtimes and external provider API keys. Detection is
# presence-only; **values are never printed**. These are NOT used by Claude
# Code or YakOS core; they're surfaced here for awareness when a user is
# configuring custom MCP servers, future provider routing (v0.2+), or the
# local-llm skill.

echo "Optional local/cross-model tooling"
for tool in ollama lms llama-server; do
    if command -v "$tool" >/dev/null 2>&1; then
        # Best-effort version probe; ignore failures.
        version="$("$tool" --version 2>/dev/null | head -n 1 | tr -d '\r' || echo "")"
        if [ -n "$version" ]; then
            ok "$tool: found ($version)"
        else
            ok "$tool: found"
        fi
    else
        info "$tool: not found"
    fi
done
echo ""

echo "External provider API keys (NOT used by YakOS or Claude Code core;"
echo "relevant only for custom MCP servers or future provider routing)"
for var in OPENAI_API_KEY ANTHROPIC_API_KEY GEMINI_API_KEY; do
    # The presence check uses indirect expansion via `eval` since bash 3.2
    # doesn't have ${!var}. Caution: never print "$value", only the result
    # of the test.
    eval "value=\${${var}:-}"
    if [ -n "${value:-}" ]; then
        ok "${var}: set"
    else
        info "${var}: not set"
    fi
done
echo ""
info "(All integrations above are optional. See COMPATIBILITY.md and COOKBOOK.md for usage.)"
echo ""

# ---- runtime feature probe (--probe-runtime only) -------------------------
#
# Doctor is a shell script; it can't actually call Claude Code's
# ToolSearch / Agent / TeamCreate to probe runtime tool availability.
# What it can do: report filesystem-side state, surface the last-known
# state of session-only tools (recorded by an in-session probe at
# ~/.yakos-state/runtime-probe.json), and tell the operator the exact prompt
# to refresh that state.

if [ "$PROBE_RUNTIME" = "1" ]; then
    echo "Claude Code runtime feature probe"

    # Filesystem-side state — directly observable.
    teams_dir="$HOME/.claude/teams"
    tasks_dir="$HOME/.claude/tasks"
    if [ -d "$teams_dir" ]; then
        n_teams="$(find "$teams_dir" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')"
        ok "$teams_dir/ exists ($n_teams team(s))"
    else
        info "$teams_dir/ does not exist (no teams created yet)"
    fi
    if [ -d "$tasks_dir" ]; then
        n_tasks="$(find "$tasks_dir" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')"
        n_tasksfiles="$(find "$tasks_dir" -type f ! -name '.lock' 2>/dev/null | wc -l | tr -d ' ')"
        if [ "$n_tasksfiles" = "0" ] && [ "$n_tasks" -gt 0 ]; then
            info "$tasks_dir/ has $n_tasks team dir(s); 0 task files (consistent with TaskCreate not exposed; see incident:v0.2.1-task-tools-not-exposed)"
        else
            info "$tasks_dir/ has $n_tasks team dir(s), $n_tasksfiles task file(s)"
        fi
    else
        info "$tasks_dir/ does not exist (no teams created yet)"
    fi
    n_inboxes="$(find "$teams_dir" -path '*/inboxes/*.json' 2>/dev/null | wc -l | tr -d ' ')"
    info "mailbox files (inboxes/*.json): $n_inboxes across all teams"
    echo ""

    # In-session tools — reported from the last in-session probe artifact,
    # if one exists.
    probe_file="$HOME/.yakos-state/runtime-probe.json"
    echo "In-session tool availability (last known state):"
    if [ -f "$probe_file" ]; then
        if command -v jq >/dev/null 2>&1; then
            ts="$(jq -r '.timestamp // "unknown"' "$probe_file" 2>/dev/null)"
            cc_ver="$(jq -r '.claude_code_version // "unknown"' "$probe_file" 2>/dev/null)"
            info "Last probe: $ts (Claude Code build: $cc_ver)"
            for tool in TaskCreate TaskList TaskUpdate; do
                state="$(jq -r --arg t "$tool" '.tools[$t] // "unknown"' "$probe_file" 2>/dev/null)"
                case "$state" in
                    available)    ok "$tool: AVAILABLE" ;;
                    unavailable)  info "$tool: NOT AVAILABLE" ;;
                    *)            info "$tool: $state" ;;
                esac
            done
        else
            info "(install jq for richer probe-file parsing)"
            info "  raw probe file at: $probe_file"
        fi
    else
        info "no probe file found at $probe_file"
        info "  per the most recent in-session probe (2026-04-29):"
        info "  TaskCreate / TaskList / TaskUpdate: NOT AVAILABLE in this Claude Code build"
        info "  see docs/architecture/phase-0.5-results.md and incident:v0.2.1-task-tools-not-exposed"
    fi
    echo ""

    echo "To refresh the last-known state, run this prompt in a Claude Code session:"
    cat <<'EOF'

  Run this in your next Claude Code session to refresh the runtime probe:

      Run ToolSearch with query "select:TaskCreate,TaskList,TaskUpdate".
      Report each tool as "available" if a schema returned, or "unavailable"
      if "No matching deferred tools found". Then write the result to
      ~/.yakos-state/runtime-probe.json with shape:
      {"timestamp":"<iso>","claude_code_version":"<env>","tools":{"TaskCreate":"available|unavailable",...}}

EOF
    echo ""

    # ---- yakos start agent injection projection ----------------------------
    # Report what `yakos start <name>` would register if invoked from the
    # given project. Closes "would my project agents actually bind?" without
    # requiring a real session launch.
    echo "yakos start projection (--agents JSON injection):"
    if [ -n "$PROJECT_PATH" ] && [ -d "$PROJECT_PATH" ]; then
        if [ -f "$YAKOS_LIB/agents-compose.sh" ]; then
            # shellcheck source=./agents-compose.sh
            . "$YAKOS_LIB/agents-compose.sh" 2>/dev/null || true
            if command -v yk_agents_compose >/dev/null 2>&1; then
                composed_json="$(yk_agents_compose "$YAKOS_ROOT" "$PROJECT_PATH" 2>/dev/null || echo '{}')"
                n_agents="$(printf '%s' "$composed_json" | jq 'length' 2>/dev/null || echo 0)"
                fw_count="$(find "$YAKOS_ROOT/lib/agents" -maxdepth 1 -name '*.md' \
                    ! -name 'README.md' ! -name 'lead-template.md' 2>/dev/null \
                    | wc -l | tr -d ' ')"
                proj_count=0
                if [ -d "$PROJECT_PATH/.claude/agents" ]; then
                    proj_count="$(find "$PROJECT_PATH/.claude/agents" -maxdepth 1 -name '*.md' \
                        ! -name 'README.md' 2>/dev/null | wc -l | tr -d ' ')"
                fi
                ok "would inject $n_agents agent(s) ($fw_count framework + $proj_count project; project overrides on id collision)"
                if [ "$n_agents" -gt 0 ]; then
                    info "addressable subagent_types after launch:"
                    printf '%s' "$composed_json" | jq -r 'keys[]' 2>/dev/null \
                        | sed 's/^/    /'
                fi
            else
                info "agents-compose.sh did not load cleanly; skipping projection"
            fi
        else
            info "agents-compose.sh not found at $YAKOS_LIB/agents-compose.sh"
        fi
    else
        info "pass <project-path> to see the agents that would be injected"
    fi
    echo ""

    # ---- launch log audit trail -----------------------------------------------
    launch_log="$HOME/.yakos-state/launch-log.ndjson"
    if [ -f "$launch_log" ]; then
        n_launches="$(wc -l < "$launch_log" 2>/dev/null | tr -d ' ')"
        last_launch="$(tail -1 "$launch_log" 2>/dev/null | jq -r '.ts // "unknown"' 2>/dev/null || echo unknown)"
        info "launch-log.ndjson: $n_launches recorded session launch(es); last at $last_launch"
    else
        info "launch-log.ndjson: none yet (run 'yakos start <name>' to populate)"
    fi
    echo ""

    # ---- runtime-probe drift detection (v0.6+) -------------------------------
    probe_dir="$HOME/.yakos-state/runtime-probes"
    echo "Runtime version drift (last 2 probes per runtime):"
    if [ -d "$probe_dir" ]; then
        for f in "$probe_dir"/*.ndjson; do
            [ -f "$f" ] || continue
            rt="$(basename -- "$f" .ndjson)"
            n="$(wc -l < "$f" 2>/dev/null | tr -d ' ')"
            if [ "$n" = "0" ]; then continue; fi
            cur="$(tail -1 "$f" | jq -r '.version' 2>/dev/null)"
            prev="$(tail -2 "$f" | head -1 | jq -r '.version' 2>/dev/null)"
            if [ "$cur" = "$prev" ] || [ "$n" = "1" ]; then
                ok "$rt: $cur ($n probe(s) on file)"
            else
                info "$rt: $cur (DRIFT — was '$prev'; review adapter for schema changes)"
            fi
        done
    else
        info "no runtime-probe history yet (run 'yakos start' against each runtime)"
    fi
    echo ""
fi

# ---- fix mode (v0.7+) -----------------------------------------------------
# Auto-remediate the cheap, idempotent fixes that don't require operator
# judgment: gitignore patterns, missing audit-log files, missing
# ~/.yakos-state subdirs, missing per-project session-history.

if [ "$FIX" = "1" ]; then
    echo ""
    echo "Auto-fix (--fix):"
    fixed=0

    # 1. ~/.yakos-state subdirs
    for sub in "" "/runtime-probes" "/memory"; do
        if [ ! -d "$HOME/.yakos-state$sub" ]; then
            mkdir -p "$HOME/.yakos-state$sub"
            ok "created $HOME/.yakos-state$sub"
            fixed=$((fixed + 1))
        fi
    done

    # 2. project-level fixes (only if PROJECT_PATH is set)
    if [ -n "$PROJECT_PATH" ] && [ -d "$PROJECT_PATH" ]; then
        # gitignore patterns
        gi="$PROJECT_PATH/.gitignore"
        if [ -f "$gi" ] && ! grep -qF '.codex/agents/yakos-' "$gi" 2>/dev/null; then
            {
                printf '\n# yakOS — runtime-emitted agent files (added by doctor --fix)\n'
                printf '.codex/agents/yakos-*.toml\n'
                printf '.gemini/agents/yakos-*.md\n'
                printf '.gemini/settings.json.yakos-bak-*\n'
            } >> "$gi"
            ok "appended yakos gitignore patterns to $gi"
            fixed=$((fixed + 1))
        fi

        # session-history file (some old projects pre-init-NDJSON)
        for nm in "$HOME/agent-control"/*; do
            [ -d "$nm" ] || continue
            ppf="$nm/.project-path"
            [ -f "$ppf" ] || continue
            pp="$(head -1 "$ppf")"
            if [ "$pp" = "$PROJECT_PATH" ] || [ "$pp" = "$(ct_realpath "$PROJECT_PATH")" ]; then
                hist="$nm/work/current/.session-started-history.ndjson"
                if [ ! -f "$hist" ] && [ -d "$nm/work/current" ]; then
                    : > "$hist"
                    ok "created $hist"
                    fixed=$((fixed + 1))
                fi
            fi
        done

        # framework-hash siblings on hooks (refresh stale)
        if [ -d "$PROJECT_PATH/scripts/hooks" ]; then
            for hf in "$PROJECT_PATH/scripts/hooks"/*.sh; do
                [ -f "$hf" ] || continue
                hash_sib="${hf}.framework-hash"
                src="$YAKOS_ROOT/lib/hooks/$(basename -- "$hf")"
                [ -f "$src" ] || continue
                expected="$(ct_sha256 "$src")"
                if [ ! -f "$hash_sib" ] || [ "$(cat "$hash_sib" 2>/dev/null)" != "$expected" ]; then
                    if [ ! -f "$hash_sib" ]; then
                        echo "$expected" > "$hash_sib"
                        ok "wrote missing framework-hash for $(basename -- "$hf")"
                        fixed=$((fixed + 1))
                    else
                        # Only refresh hash if hook content matches the framework src;
                        # if the project diverged, leave the hash alone (drift is
                        # informational, not an error).
                        if cmp -s "$hf" "$src"; then
                            echo "$expected" > "$hash_sib"
                            ok "refreshed stale framework-hash for $(basename -- "$hf")"
                            fixed=$((fixed + 1))
                        fi
                    fi
                fi
            done
        fi
    fi

    if [ "$fixed" = "0" ]; then
        info "nothing to fix"
    fi
    echo ""
fi

# ---- summary ----------------------------------------------------------------

echo "Summary: $errors error(s), $warnings warning(s)"
if [ "$errors" -gt 0 ]; then
    exit 1
fi
exit 0
