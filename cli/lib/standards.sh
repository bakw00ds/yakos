#!/usr/bin/env bash
# Purpose: standards.sh — manage cross-project standards opt-ins (Plan 4 M4).
#
# Cross-project standards (Plan 4) commit a project to one or more
# of: logging, changelog-ui, monitors, feedback, architecture-viz,
# about-page. Operators opt in via .yakos.yml profile.standards.*.
# This CLI gives them a friendlier surface than hand-editing YAML.
#
# Subcommands:
#   yakos standards list                # show all 6 standards + state
#   yakos standards enable  <name>      # set profile.standards.<name> = true
#   yakos standards disable <name>      # set profile.standards.<name> = false
#   yakos standards check               # report what each active standard
#                                       # would catch (audit-time preview)
#   yakos standards init                # interactive profile selection
#
# Reads/writes:  <project>/.yakos.yml
# Per-project: detects project from cwd (same logic as `yakos`).

set -eu

: "${YAKOS_LIB:?standards.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./project-config.sh
. "$YAKOS_LIB/project-config.sh"

# Standards inventory. Update when new standards ship.
STANDARDS="logging changelog-ui monitors feedback architecture-viz about-page"

# Profile-type → default-true standards (mirrors lib/settings/yakos.yml.template).
_standards_for_type() {
    case "$1" in
        web-app)        echo "logging changelog-ui monitors feedback architecture-viz about-page" ;;
        service)        echo "logging monitors architecture-viz" ;;
        library)        echo "architecture-viz about-page" ;;
        cli-tool)       echo "logging about-page" ;;
        data-pipeline)  echo "logging monitors" ;;
        *)              echo "" ;;
    esac
}

# Resolve project repo dir. Falls back to walking ~/agent-control/.
_standards_project_dir() {
    if [ -n "${YAKOS_PROJECT_DIR:-}" ]; then
        printf '%s\n' "$YAKOS_PROJECT_DIR"
        return
    fi
    if [ -f "$PWD/.yakos.yml" ]; then
        printf '%s\n' "$PWD"
        return
    fi
    # Walk .project-path files for the current cwd.
    local ac_root="$HOME/agent-control"
    [ -d "$ac_root" ] || { ct_die "could not resolve project dir (run from project root or set YAKOS_PROJECT_DIR)"; }
    local cwd_real cd_path proj proj_real
    cwd_real="$(cd "$PWD" 2>/dev/null && pwd -P)"
    for cd_path in "$ac_root"/*/.project-path; do
        [ -f "$cd_path" ] || continue
        proj="$(head -1 "$cd_path" 2>/dev/null)"
        proj_real="$(cd "$proj" 2>/dev/null && pwd -P || true)"
        [ -n "$proj_real" ] || continue
        case "$cwd_real" in
            "$proj_real"|"$proj_real"/*) printf '%s\n' "$proj"; return ;;
        esac
    done
    ct_die "could not resolve project dir"
}

_standards_validate_name() {
    local name="$1"
    case " $STANDARDS " in
        *" $name "*) return 0 ;;
        *) ct_die "unknown standard '$name' (valid: $STANDARDS)" ;;
    esac
}

# --- Subcommands -----------------------------------------------------

cmd_list() {
    local project type
    project="$(_standards_project_dir)"
    local yml="$project/.yakos.yml"
    [ -f "$yml" ] || ct_die "$yml not found (run 'yakos init <name> --project $project' first)"

    type="$(yk_pcfg_get "$project" profile.type 2>/dev/null)"
    [ -n "$type" ] || type="(unset)"

    printf '\nProject: %s\nProfile type: %s\n\n' "$project" "$type"
    printf '  %-18s  %-9s  %s\n' "STANDARD" "STATE" "SUGGESTED FOR TYPE?"
    printf '  %-18s  %-9s  %s\n' "--------" "-----" "-------------------"

    local suggested
    suggested=" $(_standards_for_type "$type") "
    local s state suggest
    for s in $STANDARDS; do
        state="$(yk_pcfg_get "$project" "profile.standards.$s" 2>/dev/null)"
        case "$state" in
            true)  state="enabled" ;;
            false|"") state="disabled" ;;
        esac
        case "$suggested" in
            *" $s "*) suggest="yes" ;;
            *) suggest="no" ;;
        esac
        printf '  %-18s  %-9s  %s\n' "$s" "$state" "$suggest"
    done
    printf '\nEdit .yakos.yml directly or use:\n'
    printf '  yakos standards enable <name>\n'
    printf '  yakos standards disable <name>\n\n'
}

# Toggle a standard. Uses ct_sed_inplace; preserves surrounding YAML.
_standards_set() {
    local name="$1" value="$2"
    _standards_validate_name "$name"
    local project yml
    project="$(_standards_project_dir)"
    yml="$project/.yakos.yml"
    [ -f "$yml" ] || ct_die "$yml not found"

    # Match the line `    <name>: <true|false>` under standards: block.
    # Use awk for a more robust in-place rewrite.
    local tmp
    tmp="$(mktemp -t yakos-std.XXXXXX)"
    awk -v key="$name" -v val="$value" '
        BEGIN { in_std = 0; matched = 0 }
        /^[[:space:]]*standards:[[:space:]]*$/ { in_std = 1; print; next }
        in_std && /^[[:space:]]*[a-z]/ && $0 !~ "^[[:space:]]+" key ":" && $0 !~ "^[[:space:]]+[a-z-]+:" {
            # Left the indented block (something at a less-indented level).
            in_std = 0
        }
        in_std && $0 ~ "^[[:space:]]+" key "[[:space:]]*:" {
            # Replace the value, preserving leading whitespace + key + trailing comment.
            match($0, /^[[:space:]]+/)
            indent = substr($0, RSTART, RLENGTH)
            # Preserve trailing comments (everything after " #").
            comment = ""
            if (match($0, /[[:space:]]+#.*$/)) {
                comment = substr($0, RSTART)
            }
            print indent key ": " val comment
            matched = 1
            next
        }
        { print }
        END {
            if (!matched) {
                # Key not present; append at end of file.
                print "# Added by yakos standards " (val == "true" ? "enable" : "disable") ":"
                print "profile:"
                print "  standards:"
                print "    " key ": " val
            }
        }
    ' "$yml" > "$tmp" && mv "$tmp" "$yml"
    ct_log "standards: set $name = $value in $yml"
}

cmd_enable()  { _standards_set "$1" "true"; }
cmd_disable() { _standards_set "$1" "false"; }

cmd_check() {
    local project
    project="$(_standards_project_dir)"
    local yml="$project/.yakos.yml"
    [ -f "$yml" ] || ct_die "$yml not found"

    printf '\nStandards audit preview for: %s\n\n' "$project"

    local s state any_active=0
    for s in $STANDARDS; do
        state="$(yk_pcfg_get "$project" "profile.standards.$s" 2>/dev/null)"
        [ "$state" = "true" ] || continue
        any_active=1
        printf '== %s ==\n' "$s"
        # Print which playbook section would audit this standard.
        case "$s" in
            logging)
                printf '  playbook: lib/playbooks/02-code-quality.md §Logging discipline\n'
                printf '  detects: bare print/console.log/println! in production; missing structured fields; secrets in logs\n'
                printf '  scaffold: yakos invokes lib/skills/logging-scaffold/SKILL.md\n'
                ;;
            changelog-ui)
                printf '  playbook: lib/playbooks/04-docs-architecture.md §Changelog UI presence\n'
                printf '  detects: missing UI component; version mismatch with VERSION file\n'
                printf '  scaffold: lib/skills/changelog-ui-scaffold/SKILL.md\n'
                ;;
            monitors)
                printf '  playbook: lib/playbooks/08-infra-deploy-deps.md §Monitor presence\n'
                printf '  detects: missing supervisor/healthz/runbook; no-op healthchecks; missing restart policy\n'
                printf '  scaffold: lib/skills/monitor-scaffold/SKILL.md\n'
                ;;
            feedback)
                printf '  playbook: lib/playbooks/02-code-quality.md §Feedback wiring\n'
                printf '  detects: missing feedback table/API/widget; cite-without-data + resolved-without-citation orphans\n'
                printf '  scaffold: lib/skills/feedback-scaffold/SKILL.md\n'
                ;;
            architecture-viz)
                printf '  playbook: lib/playbooks/04-docs-architecture.md §Architecture viz presence\n'
                printf '  detects: missing docs/architecture/*.mmd; ADRs missing; stale diagrams; PNG-only diagrams\n'
                printf '  scaffold: lib/skills/architecture-viz-scaffold/SKILL.md\n'
                ;;
            about-page)
                printf '  playbook: lib/playbooks/04-docs-architecture.md §About page presence\n'
                printf '  detects: missing about route; placeholder text remaining; 90-day staleness\n'
                printf '  scaffold: lib/skills/about-page-scaffold/SKILL.md\n'
                ;;
        esac
        printf '\n'
    done

    if [ "$any_active" = "0" ]; then
        printf '  (no standards enabled in this project)\n'
        printf '\n  Enable with: yakos standards enable <name>\n\n'
        return 0
    fi

    printf 'To run the full audit (all domains + standards), invoke:\n'
    printf '  Skill: release-audit (skill:release-audit; see lib/skills/release-audit/SKILL.md)\n\n'
}

cmd_init() {
    local project
    project="$(_standards_project_dir)"
    local yml="$project/.yakos.yml"
    [ -f "$yml" ] || ct_die "$yml not found (run 'yakos init' first)"

    printf '\nInteractive profile selection for: %s\n' "$project"
    printf 'Project types: web-app | service | library | cli-tool | data-pipeline\n\n'
    printf 'Current profile.type: %s\n' "$(yk_pcfg_get "$project" profile.type 2>/dev/null || echo '(unset)')"
    printf 'Pick a project type: '
    read -r picked
    case "$picked" in
        web-app|service|library|cli-tool|data-pipeline) : ;;
        *) ct_die "unknown type '$picked'" ;;
    esac

    # Update profile.type in .yakos.yml via the same awk-rewrite pattern.
    local tmp
    tmp="$(mktemp -t yakos-init.XXXXXX)"
    awk -v t="$picked" '
        /^[[:space:]]+type:/ { sub(/type:.*/, "type: " t); print; next }
        { print }
    ' "$yml" > "$tmp" && mv "$tmp" "$yml"
    ct_log "standards: set profile.type = $picked"

    # Enable the suggested standards for the type.
    local suggested
    suggested="$(_standards_for_type "$picked")"
    if [ -n "$suggested" ]; then
        printf '\nEnable suggested standards for type %s? [Y/n] ' "$picked"
        read -r reply
        case "${reply:-y}" in
            n|N|no|NO) ct_log "skipped suggested-standards enable" ;;
            *)
                local s
                for s in $suggested; do
                    _standards_set "$s" "true"
                done
                ;;
        esac
    fi
    printf '\nDone. Run "yakos standards list" to verify.\n'
}

# --- Dispatch --------------------------------------------------------

case "${1:-help}" in
    list)     shift; cmd_list "$@" ;;
    enable)   shift; cmd_enable "$@" ;;
    disable)  shift; cmd_disable "$@" ;;
    check)    shift; cmd_check "$@" ;;
    init)     shift; cmd_init "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos standards — manage cross-project standards opt-ins (Plan 4)

  yakos standards list           # show all 6 standards + state
  yakos standards enable  <name>   # opt in
  yakos standards disable <name>   # opt out
  yakos standards check            # preview what active standards catch
  yakos standards init             # interactive profile + standards selection

Standards: logging | changelog-ui | monitors | feedback | architecture-viz | about-page

State lives in <project>/.yakos.yml under profile.standards.*. Edit
the file directly or use this CLI. See docs/cross-project-standards.md
for the full standards model.
EOF
        ;;
    *) ct_die "yakos standards: unknown subcommand '$1' (try 'yakos standards help')" ;;
esac
