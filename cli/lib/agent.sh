#!/usr/bin/env bash
# agent.sh — agent-file lifecycle commands.
#
# Subcommands:
#   yakos agent new <name> [--runtime <id>] [--project <path>] [--extends <id>]
#                          [--role specialist|reviewer|maintainer]
#                          [--domain <text>] [--model <alias>]
#                          [--tools "Read, Edit, Bash"]
#   yakos agents lint [<project>]   — verify all project agents resolve cleanly
#
# (The plural form 'agents' is also routed here for the lint subcommand.)

set -eu

: "${YAKOS_ROOT:?agent.sh: YAKOS_ROOT must be set}"
: "${YAKOS_LIB:?agent.sh: YAKOS_LIB must be set}"
# shellcheck source=./compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=./agents-compose.sh
. "$YAKOS_LIB/agents-compose.sh"
# shellcheck source=./runtime-resolve.sh
. "$YAKOS_LIB/runtime-resolve.sh"

usage() {
    cat <<'EOF'
yakos agent <subcommand> [args...]
yakos agents <subcommand> [args...]    (alias for the plural)

Subcommands:
  new <name>      Scaffold a new project agent file at
                  <project>/.claude/agents/<name>.md.
                  Flags:
                    --runtime <id>    claude (default) | codex | gemini
                    --project <path>  Project repo. Defaults to inferring
                                      from cwd.
                    --extends <id>    Framework template to extend.
                    --role <r>        specialist | reviewer | maintainer
                                      (default: specialist).
                    --domain <text>   Free-form domain tag.
                    --model <alias>   opus | sonnet | haiku | <full>.
                    --tools "<list>"  Comma-separated tool names.
                    --force           Overwrite if exists.

  lint [<project>]    Audit every agent file:
                      - frontmatter has required fields (id, role, domain)
                      - runtime: (if set) is in known list
                      - runtime-fallback: (if set) all entries valid
                      - extends: target exists
                      - body has '## Purpose' section
                      Exit 0 clean; 1 if any error.

Examples:
  yakos agent new codex-helper --runtime codex --domain cross-cutting
  yakos agent new myapp-backend --extends backend --runtime claude
  yakos agents lint
EOF
}

infer_project() {
    local cwd_real ac_root rest p pr
    cwd_real="$(ct_realpath "$PWD")"
    ac_root="$HOME/agent-control"
    case "$cwd_real" in
        "$ac_root"/*)
            rest="${cwd_real#$ac_root/}"
            local name="${rest%%/*}"
            [ -f "$ac_root/$name/.project-path" ] && head -1 "$ac_root/$name/.project-path" && return 0
            ;;
    esac
    if [ -d "$ac_root" ]; then
        for cd_path in "$ac_root"/*/.project-path; do
            [ -f "$cd_path" ] || continue
            p="$(head -1 "$cd_path")"
            pr="$(ct_realpath "$p" 2>/dev/null || true)"
            case "$cwd_real" in
                "$pr"|"$pr"/*) printf '%s\n' "$pr"; return 0 ;;
            esac
        done
    fi
    return 1
}

# ---- subcommand dispatch ---------------------------------------------------

SUB="${1:-}"
[ "$#" -gt 0 ] && shift || true

case "$SUB" in
    "" | -h | --help | help) usage; exit 0 ;;
    new|lint) ;;
    *) ct_die "agent: unknown subcommand '$SUB' (try --help)" ;;
esac

# ---- subcommand: new ------------------------------------------------------

if [ "$SUB" = "new" ]; then
    NAME=""
    RUNTIME=""
    PROJECT=""
    EXTENDS=""
    ROLE="specialist"
    DOMAIN=""
    MODEL="sonnet"
    TOOLS="Read, Edit, Bash, Grep, SendMessage"
    FORCE=0

    while [ "$#" -gt 0 ]; do
        case "$1" in
            -h|--help) usage; exit 0 ;;
            --runtime) shift; RUNTIME="$1" ;;
            --runtime=*) RUNTIME="${1#--runtime=}" ;;
            --project) shift; PROJECT="$1" ;;
            --project=*) PROJECT="${1#--project=}" ;;
            --extends) shift; EXTENDS="$1" ;;
            --extends=*) EXTENDS="${1#--extends=}" ;;
            --role) shift; ROLE="$1" ;;
            --role=*) ROLE="${1#--role=}" ;;
            --domain) shift; DOMAIN="$1" ;;
            --domain=*) DOMAIN="${1#--domain=}" ;;
            --model) shift; MODEL="$1" ;;
            --model=*) MODEL="${1#--model=}" ;;
            --tools) shift; TOOLS="$1" ;;
            --tools=*) TOOLS="${1#--tools=}" ;;
            --force) FORCE=1 ;;
            -*) ct_die "agent new: unknown flag '$1'" ;;
            *)
                if [ -z "$NAME" ]; then NAME="$1"
                else ct_die "agent new: too many positional args"
                fi
                ;;
        esac
        shift
    done

    [ -n "$NAME" ] || { usage >&2; ct_die "agent new: <name> required"; }
    case "$NAME" in *[!A-Za-z0-9_-]*) ct_die "agent new: <name> must be alphanumeric (-_ allowed)" ;; esac

    if [ -z "$PROJECT" ]; then
        PROJECT="$(infer_project || true)"
    fi
    [ -n "$PROJECT" ] || ct_die "agent new: --project <path> required (cannot infer from cwd)"
    [ -d "$PROJECT" ] || ct_die "agent new: project not found: $PROJECT"

    if [ -n "$RUNTIME" ]; then
        yk_rt_is_known "$RUNTIME" || ct_die "agent new: unknown runtime '$RUNTIME' (known: $(yk_rt_known | tr '\n' ' '))"
    fi
    if [ -n "$EXTENDS" ]; then
        if [ ! -f "$YAKOS_ROOT/lib/agents/${EXTENDS}.md" ] && [ ! -f "$PROJECT/.claude/agents/${EXTENDS}.md" ]; then
            ct_die "agent new: --extends '$EXTENDS' not found in framework or project agents"
        fi
    fi
    [ -n "$DOMAIN" ] || DOMAIN="cross-cutting"

    out_dir="$PROJECT/.claude/agents"
    out_file="$out_dir/${NAME}.md"
    mkdir -p "$out_dir"

    if [ -f "$out_file" ] && [ "$FORCE" != "1" ]; then
        ct_die "agent new: $out_file already exists (use --force to overwrite)"
    fi

    {
        printf -- '---\n'
        printf 'id: %s\n' "$NAME"
        printf 'role: %s\n' "$ROLE"
        printf 'domain: %s\n' "$DOMAIN"
        printf 'mode: [feature]\n'
        printf 'tools: [%s]\n' "$TOOLS"
        printf 'model: %s\n' "$MODEL"
        [ -n "$EXTENDS" ] && printf 'extends: %s\n' "$EXTENDS"
        if [ -n "$RUNTIME" ] && [ "$RUNTIME" != "claude" ]; then
            printf 'runtime: %s\n' "$RUNTIME"
        fi
        printf 'references: []\n'
        printf -- '---\n\n'
        printf '# %s\n\n' "$(printf '%s' "$NAME" | tr '-' ' ' | awk '{for (i=1; i<=NF; i++) $i = toupper(substr($i,1,1)) tolower(substr($i,2)); print}')"
        printf '## Purpose\n\n'
        printf 'TODO — what does this agent do? Why does it exist?\n\n'
        printf '## Execution\n\n'
        printf '1. TODO — first step\n\n'
        printf '## Special rules\n\n'
        printf -- '- TODO — what should this agent never do?\n\n'
        printf '## When to push back / escalate\n\n'
        printf '1. TODO — when to push back\n'
        printf '2. TODO — when to escalate\n'
        printf '3. **Done means:** TODO\n\n'
        printf '## Personality\n\n'
        printf 'TODO — short paragraph on disposition.\n'
    } > "$out_file"

    cat <<EOF
wrote $out_file

Frontmatter:
  id:       $NAME
  role:     $ROLE
  domain:   $DOMAIN
  model:    $MODEL
  tools:    [$TOOLS]
$( [ -n "$EXTENDS" ] && printf '  extends:  %s\n' "$EXTENDS" )$( [ -n "$RUNTIME" ] && [ "$RUNTIME" != "claude" ] && printf '  runtime:  %s\n' "$RUNTIME" )

Next steps:
  1. Edit $out_file — fill in the TODO sections.
  2. yakos agents lint    # verify the file parses
$( [ -n "$RUNTIME" ] && [ "$RUNTIME" != "claude" ] && printf "  3. yakos auth status %s   # ensure the chosen runtime is configured\n" "$RUNTIME" )
EOF
    exit 0
fi

# ---- subcommand: lint -----------------------------------------------------

if [ "$SUB" = "lint" ]; then
    PROJECT="${1:-}"
    if [ -z "$PROJECT" ]; then
        PROJECT="$(infer_project || true)"
    fi
    [ -n "$PROJECT" ] || ct_die "agents lint: <project> required"

    errors=0
    warnings=0
    total=0

    err()  { printf '  [ERR]  %s\n' "$*" >&2; errors=$((errors + 1)); }
    warn() { printf '  [warn] %s\n' "$*"; warnings=$((warnings + 1)); }
    ok()   { printf '  [ok]   %s\n' "$*"; }

    echo "yakos agents lint — $PROJECT"
    echo

    # Lint each project + framework agent. Project agents at
    # <project>/.claude/agents/*.md; framework agents resolved by
    # extends: are validated as a side-effect.
    proj_dir="$PROJECT/.claude/agents"
    if [ ! -d "$proj_dir" ]; then
        echo "no project agents at $proj_dir (nothing to lint)"
        exit 0
    fi

    for f in "$proj_dir"/*.md; do
        [ -f "$f" ] || continue
        case "$(basename -- "$f")" in README.md) continue ;; esac
        total=$((total + 1))
        local_errs=0

        fm="$(yk_agents_extract_frontmatter "$f")"
        body="$(yk_agents_extract_body "$f")"

        # Required fields
        for req in id role domain; do
            v="$(yk_agents_fm_get "$fm" "$req")"
            if [ -z "$v" ]; then
                err "$(basename -- "$f"): missing required frontmatter field '$req'"
                local_errs=$((local_errs + 1))
            fi
        done

        # runtime: must be a known runtime if set
        rt="$(yk_agents_fm_get "$fm" "runtime")"
        if [ -n "$rt" ]; then
            if ! yk_rt_is_known "$rt"; then
                err "$(basename -- "$f"): runtime '$rt' is not in known list ($(yk_rt_known | tr '\n' ' '))"
                local_errs=$((local_errs + 1))
            fi
        fi

        # runtime-fallback: all entries valid
        fb="$(yk_agents_fm_list "$fm" "runtime-fallback" || true)"
        if [ -n "$fb" ]; then
            while IFS= read -r entry; do
                [ -n "$entry" ] || continue
                if ! yk_rt_is_known "$entry"; then
                    err "$(basename -- "$f"): runtime-fallback entry '$entry' is not a known runtime"
                    local_errs=$((local_errs + 1))
                fi
            done <<< "$fb"
        fi

        # extends: target exists in framework or project
        ext="$(yk_agents_fm_get "$fm" "extends")"
        if [ -n "$ext" ]; then
            if [ ! -f "$YAKOS_ROOT/lib/agents/${ext}.md" ] && [ ! -f "$proj_dir/${ext}.md" ]; then
                err "$(basename -- "$f"): extends '$ext' not found in framework or project agents"
                local_errs=$((local_errs + 1))
            fi
        fi

        # Body has Purpose section
        if ! printf '%s' "$body" | grep -qE '^##[[:space:]]+Purpose[[:space:]]*$'; then
            warn "$(basename -- "$f"): body missing '## Purpose' section"
            warnings=$((warnings + 1))
        fi

        if [ "$local_errs" = "0" ]; then
            ok "$(basename -- "$f")"
        fi
    done

    echo
    echo "lint summary: $total agent(s), $errors error(s), $warnings warning(s)"
    [ "$errors" -eq 0 ] && exit 0 || exit 1
fi
