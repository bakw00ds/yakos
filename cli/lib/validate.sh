#!/usr/bin/env bash
# validate.sh — schema + reference validation.
#
# Three modes:
#   yakos validate                framework lib/
#   yakos validate <path>         project .claude/ (rooted at <path>)
#   yakos validate --all          both
#
# v0.1 lib/ is empty (agents/skills/rules arrive in Batch 3). This
# subcommand handles the empty case cleanly with informational output.
# Frontmatter+reference validation against populated lib/ is layered in
# Batch 3 alongside the agent/skill files.
#
# Validation uses python3 if available for proper YAML/JSON parsing;
# without python3 it degrades to grep-based checks and surfaces a
# "limited validation" warning.

set -eu

: "${YAKOS_ROOT:?YAKOS_ROOT must be set; run via 'yakos validate'}"
: "${YAKOS_LIB:?YAKOS_LIB must be set; run via 'yakos validate'}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"

ALL=0
TARGETS=()
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            cat <<EOF
yakos validate [<project-path>] [--all]

Schema + reference validation. Three modes:

  yakos validate                Validate the framework's lib/ (this repo).
  yakos validate <path>         Validate <path>/.claude/ for a project.
  yakos validate --all          Validate framework lib/ AND the project's
                                .claude/ (must also pass <path>).

v0.1 lib/ is intentionally empty — this command handles the empty
case and reports cleanly. Full frontmatter+reference validation runs
once Batch 3 populates lib/agents/, lib/skills/, lib/rules/.

Uses python3 if available for full YAML/JSON parsing; degrades to
grep-based checks otherwise (with a "limited validation" warning).
EOF
            exit 0
            ;;
        --all) ALL=1 ;;
        --*) ct_die "validate: unknown flag '$arg'" ;;
        *) TARGETS+=("$arg") ;;
    esac
done

errors=0
warnings=0

err()  { printf '  [err]  %s\n' "$*"; errors=$((errors + 1)); }
warn() { printf '  [warn] %s\n' "$*"; warnings=$((warnings + 1)); }
ok()   { printf '  [ok]   %s\n' "$*"; }
info() { printf '  [info] %s\n' "$*"; }

# ---- python3 capability check -----------------------------------------------

PYTHON_OK=0
if command -v python3 >/dev/null 2>&1; then
    PYTHON_OK=1
fi
if [ "$PYTHON_OK" = "0" ]; then
    warn "python3 not found; running limited validation (grep-based checks only)"
fi

# ---- per-tree validators ----------------------------------------------------

count_dir_files() {
    # Number of files matching a name pattern under a tree, excluding
    # .gitkeep / README.md and dotfiles
    local tree="$1" pattern="$2"
    [ -d "$tree" ] || { echo 0; return; }
    find "$tree" -type f -name "$pattern" \
        ! -name '.*' ! -name 'README.md' 2>/dev/null | wc -l | tr -d ' '
}

validate_yaml_frontmatter() {
    # Returns 0 if the file appears to have well-formed YAML frontmatter
    # (i.e. starts with --- and has a closing ---). Strict YAML parsing
    # requires python3.
    local f="$1"
    [ -f "$f" ] || return 1
    # The file must start with --- on line 1
    head -n 1 "$f" 2>/dev/null | grep -q '^---$' || return 1
    # And have a closing --- somewhere in the next 200 lines
    head -n 200 "$f" 2>/dev/null | tail -n +2 | grep -qx '^---$'
}

validate_python_frontmatter() {
    # Strict YAML parse. Requires python3.
    local f="$1"
    python3 - "$f" <<'PY' 2>/dev/null
import sys, re
path = sys.argv[1]
try:
    with open(path, "r", encoding="utf-8") as fh:
        text = fh.read()
except Exception as e:
    print(f"read error: {e}", file=sys.stderr)
    sys.exit(2)
m = re.match(r"^---\n(.*?)\n---\n", text, re.DOTALL)
if not m:
    print("no YAML frontmatter block", file=sys.stderr)
    sys.exit(3)
body = m.group(1)
try:
    import yaml  # type: ignore
    yaml.safe_load(body)
except ImportError:
    # Best-effort: just check key: value structure
    for line in body.splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if ":" not in line and not line.startswith("- "):
            print(f"suspicious frontmatter line: {line}", file=sys.stderr)
            sys.exit(4)
except Exception as e:
    print(f"yaml parse error: {e}", file=sys.stderr)
    sys.exit(5)
PY
    return $?
}

validate_tree() {
    local label="$1" base="$2"
    echo
    echo "$label: $base"
    if [ ! -d "$base" ]; then
        info "directory does not exist; nothing to validate"
        return 0
    fi
    local n_agents n_skills n_rules
    n_agents="$(count_dir_files "$base/agents" '*.md')"
    n_skills="$(count_dir_files "$base/skills" 'SKILL.md')"
    n_rules="$(count_dir_files "$base/rules" '*.md')"
    info "agents: $n_agents | skills: $n_skills | rules: $n_rules"

    if [ "$n_agents" -eq 0 ] && [ "$n_skills" -eq 0 ] && [ "$n_rules" -eq 0 ]; then
        info "no agents/skills/rules to validate (this is expected for the empty lib/ in v0.1)"
        return 0
    fi

    # When populated (Batch 3+), run full frontmatter checks
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        if [ "$PYTHON_OK" = "1" ]; then
            if ! validate_python_frontmatter "$f"; then
                err "$f: bad YAML frontmatter"
                continue
            fi
        else
            if ! validate_yaml_frontmatter "$f"; then
                warn "$f: missing or malformed --- frontmatter (limited check)"
                continue
            fi
        fi
        ok "$f"
    done < <(find "$base/agents" -type f -name '*.md' ! -name 'README.md' 2>/dev/null
             find "$base/skills" -type f -name 'SKILL.md' 2>/dev/null
             find "$base/rules"  -type f -name '*.md' ! -name 'README.md' ! -name 'INDEX.md' 2>/dev/null)

    # settings.json (project-only)
    if [ -f "$base/settings.json" ]; then
        if ct_json_valid "$base/settings.json"; then
            ok "$base/settings.json: valid JSON"
        else
            err "$base/settings.json: not valid JSON"
        fi
    fi

    if [ -f "$base/path-allowlist.json" ]; then
        if ct_json_valid "$base/path-allowlist.json"; then
            ok "$base/path-allowlist.json: valid JSON"
        else
            err "$base/path-allowlist.json: not valid JSON"
        fi
    fi

    # Line-budget WARNs (per Phase 1.5 §10 + STYLE.md §7)
    check_line_budgets "$base"

    # Playbook reference resolution — broken references are ERRORs.
    check_playbook_references "$base"
}

# ---- standards checks (framework-mode only; STYLE.md §1-§7) ----------------

check_line_budgets() {
    # WARN on any agent/skill/rule file outside its line budget.
    local base="$1"
    [ -d "$base" ] || return 0

    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local n
        n="$(wc -l < "$f" | tr -d ' ')"
        if [ "$n" -lt 80 ] || [ "$n" -gt 140 ]; then
            warn "$f: agent file is $n lines (budget 80-140)"
        fi
    done < <(find "$base/agents" -type f -name '*.md' ! -name 'README.md' 2>/dev/null)

    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local n
        n="$(wc -l < "$f" | tr -d ' ')"
        if [ "$n" -lt 80 ] || [ "$n" -gt 180 ]; then
            warn "$f: skill is $n lines (budget 80-180)"
        fi
    done < <(find "$base/skills" -type f -name 'SKILL.md' 2>/dev/null)

    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local n
        n="$(wc -l < "$f" | tr -d ' ')"
        if [ "$n" -lt 60 ] || [ "$n" -gt 150 ]; then
            warn "$f: rule is $n lines (budget 60-150)"
        fi
    done < <(find "$base/rules" -type f -name '*.md' ! -name 'INDEX.md' ! -name 'README.md' 2>/dev/null)
}

check_playbook_references() {
    # Walk every agent .md and rule .md under $base and verify each
    # `playbook:<name>` reference resolves to a real file in
    # $YAKOS_ROOT/lib/playbooks/<name>.md (or the project's own
    # playbooks/ directory if that ever lands).
    #
    # Broken references are ERRORs (per spec — playbooks are
    # institutional knowledge; a broken ref means the agent will read
    # a missing reference at session time, which is a real defect).
    local base="$1"
    [ -d "$base" ] || return 0

    local pb_dir="$YAKOS_ROOT/lib/playbooks"

    # Collect referenced playbooks across all md files in the base
    local refs
    refs="$(grep -rhE '^[[:space:]]*-[[:space:]]*playbook:[A-Za-z0-9._/-]+' \
                  "$base/agents" "$base/rules" "$base/skills" 2>/dev/null \
            | sed -E 's/.*playbook:([A-Za-z0-9._/-]+).*/\1/' \
            | sort -u || true)"

    [ -n "$refs" ] || return 0

    while IFS= read -r ref; do
        [ -n "$ref" ] || continue
        local target="$pb_dir/${ref}.md"
        if [ ! -f "$target" ]; then
            err "broken playbook reference: 'playbook:$ref' — no file at $target"
        fi
    done <<< "$refs"
}

check_shebang_strict_mode() {
    # WARN if a shell script under cli/ or lib/hooks/ lacks the shebang
    # or `set -euo pipefail`.
    local roots=("$YAKOS_ROOT/cli" "$YAKOS_ROOT/lib/hooks")
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local first
        first="$(head -n 1 "$f" 2>/dev/null || true)"
        case "$first" in
            '#!/usr/bin/env bash'|'#!/bin/bash') ;;
            *) warn "$f: missing or non-bash shebang"; continue ;;
        esac
        # Files that look like sourced libraries (use `return 0` source-guard
        # pattern) intentionally don't run `set -e` — that would propagate
        # into the caller's shell.
        if grep -qE 'return 0 2>/dev/null \|\| exit 0' "$f" 2>/dev/null; then
            continue
        fi
        # Look for set -euo pipefail (or set -eu — close enough for v0.1)
        if ! head -n 50 "$f" 2>/dev/null | grep -qE '^set -e[a-z]*[u][a-z]*'; then
            warn "$f: missing 'set -e[u][o pipefail]' near top"
        fi
    done < <(find "${roots[@]}" -type f -name '*.sh' ! -name '*.framework-hash' 2>/dev/null)
    # Also check the entry point
    [ -f "$YAKOS_ROOT/cli/yakos" ] && {
        if ! head -n 50 "$YAKOS_ROOT/cli/yakos" | grep -qE '^set -e'; then
            warn "$YAKOS_ROOT/cli/yakos: missing 'set -e' near top"
        fi
    }
}

check_header_purpose() {
    # WARN if a script in cli/ or lib/hooks/ doesn't have a "Purpose:" line
    # somewhere in the first 30 lines.
    local roots=("$YAKOS_ROOT/cli" "$YAKOS_ROOT/lib/hooks")
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        # Skip the framework-hash sibling files
        case "$f" in *.framework-hash) continue ;; esac
        if ! head -n 30 "$f" 2>/dev/null | grep -qE '^# (Purpose|purpose):'; then
            warn "$f: header lacks 'Purpose:' line (see STYLE.md §2)"
        fi
    done < <(find "${roots[@]}" -type f -name '*.sh' 2>/dev/null)
}

check_executable_bits() {
    # ERROR (not WARN) on hook .sh files that aren't executable. This
    # would actually break hook execution.
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        if [ ! -x "$f" ]; then
            err "$f: hook script is not executable (chmod +x to fix)"
        fi
    done < <(find "$YAKOS_ROOT/lib/hooks" -maxdepth 2 -type f -name '*.sh' \
                 ! -path '*/lib/*' 2>/dev/null)
}

check_todo_only_files() {
    # WARN on files where >50% of non-empty lines start with "# TODO" or "// TODO".
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local total nonempty todo
        total="$(wc -l < "$f" | tr -d ' ')"
        [ "$total" -lt 5 ] && continue
        nonempty="$(grep -cv '^[[:space:]]*$' "$f" 2>/dev/null | tr -d ' ')"
        [ "${nonempty:-0}" -lt 5 ] && continue
        todo="$(grep -cE '^[[:space:]]*(#|//)[[:space:]]*TODO' "$f" 2>/dev/null | tr -d ' ')"
        if [ "${todo:-0}" -gt 0 ] && [ "$((todo * 2))" -gt "$nonempty" ]; then
            warn "$f: appears to be TODO-only ($todo TODOs of $nonempty non-empty lines)"
        fi
    done < <(find "$YAKOS_ROOT/cli" "$YAKOS_ROOT/lib" -type f \
                 \( -name '*.sh' -o -name '*.md' \) 2>/dev/null)
}

check_dark_code() {
    # WARN on hooks/scripts not referenced from CLI dispatch, settings template,
    # SKILL.md, or any docs/ markdown file.
    local refs_file="${TMPDIR:-/tmp}/yakos-validate-refs-$$"
    {
        [ -f "$YAKOS_ROOT/cli/yakos" ] && cat "$YAKOS_ROOT/cli/yakos"
        find "$YAKOS_ROOT/lib/settings" -type f \( -name '*.json' -o -name '*.template.*' \) -exec cat {} \; 2>/dev/null
        find "$YAKOS_ROOT/lib/skills" -name 'SKILL.md' -exec cat {} \; 2>/dev/null
        find "$YAKOS_ROOT/docs" -name '*.md' -exec cat {} \; 2>/dev/null
        find "$YAKOS_ROOT" -maxdepth 1 -name '*.md' -exec cat {} \; 2>/dev/null
        find "$YAKOS_ROOT/lib/hooks" -name 'README.md' -exec cat {} \; 2>/dev/null
    } > "$refs_file" 2>/dev/null

    while IFS= read -r f; do
        [ -n "$f" ] || continue
        local base
        base="$(basename "$f")"
        # Skip framework helpers (always referenced indirectly via sourcing)
        case "$base" in
            hook-input.sh|hook-output.sh|paths.sh|compat.sh|agents-compose.sh|runtime-resolve.sh|README.md) continue ;;
        esac
        # Skip runtime adapters (sourced by runtime-resolve.sh, not dispatched directly)
        case "$f" in
            */cli/lib/runtimes/*.sh) continue ;;
        esac
        if ! grep -qF "$base" "$refs_file" 2>/dev/null; then
            warn "$f: potential dark code — '$base' is not referenced anywhere (settings.template.json, SKILL.md, docs/, or CLI)"
        fi
    done < <(find "$YAKOS_ROOT/lib/hooks" "$YAKOS_ROOT/cli/lib" -type f -name '*.sh' 2>/dev/null)

    rm -f "$refs_file" 2>/dev/null || true
}

check_skill_md_sections() {
    # WARN on SKILL.md missing required sections.
    local required=("Purpose" "Scope" "Automated pass" "Manual pass" "Known gotchas")
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        for sec in "${required[@]}"; do
            if ! grep -qE "^##[[:space:]]+${sec}\b" "$f" 2>/dev/null; then
                warn "$f: SKILL.md missing section '## ${sec}'"
            fi
        done
    done < <(find "$YAKOS_ROOT/lib/skills" -name 'SKILL.md' 2>/dev/null)
}

check_agent_md_sections() {
    # WARN on agent files missing required sections.
    local required=("Purpose" "Execution" "Special rules" "Handling peer messages" "Personality")
    while IFS= read -r f; do
        [ -n "$f" ] || continue
        # Skip lead-template until Batch 3 ships content
        case "$(basename "$f")" in lead-template.md) continue ;; esac
        for sec in "${required[@]}"; do
            if ! grep -qE "^##[[:space:]]+${sec}\b" "$f" 2>/dev/null; then
                warn "$f: agent file missing section '## ${sec}'"
            fi
        done
    done < <(find "$YAKOS_ROOT/lib/agents" -type f -name '*.md' \
                 ! -name 'README.md' ! -name 'INDEX.md' 2>/dev/null)
}

run_standards_checks() {
    echo
    echo "Standards checks (WARN-only in v0.1; see STYLE.md):"
    check_shebang_strict_mode
    check_header_purpose
    check_executable_bits
    check_todo_only_files
    check_dark_code
    check_skill_md_sections
    check_agent_md_sections
}

# ---- mode selection ---------------------------------------------------------

if [ "$ALL" = "1" ]; then
    validate_tree "framework" "$YAKOS_ROOT/lib"
    run_standards_checks
    if [ "${#TARGETS[@]}" -gt 0 ]; then
        for t in "${TARGETS[@]}"; do
            validate_tree "project" "$t/.claude"
        done
    else
        warn "--all without a project path; only framework lib/ was validated"
    fi
elif [ "${#TARGETS[@]}" -eq 0 ]; then
    validate_tree "framework" "$YAKOS_ROOT/lib"
    run_standards_checks
else
    for t in "${TARGETS[@]}"; do
        # If the user passes <project>/.claude directly we accept that too
        case "$t" in
            */.claude|*/.claude/) base="$t" ;;
            *) base="$t/.claude" ;;
        esac
        validate_tree "project" "$base"
    done
fi

echo
echo "Summary: $errors error(s), $warnings warning(s)"
if [ "$errors" -gt 0 ]; then
    exit 1
fi
exit 0
