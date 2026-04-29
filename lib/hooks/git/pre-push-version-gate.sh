#!/usr/bin/env bash
# Purpose: Refuse `git push` if substantive code changed since the last
#   version tag without a corresponding VERSION change in the push.
#   Doc-only changes pass through.
# Inputs:
#   stdin: per `git pre-push` hook spec — one line per ref being pushed:
#          <local_ref> <local_oid> <remote_ref> <remote_oid>
#          (currently consumed but not used; ref-level filtering is v0.3+)
#   YAKOS_GATE_DISABLE=1   bypass the gate (logged as overridden)
#   YAKOS_GATE_VERBOSE=1   print classification breakdown
# Outputs:
#   stdout/stderr: human-readable status messages
#   ~/.yakos/gate-log.ndjson: append-only audit trail (allow/refuse/override)
# Exit codes: 0 push allowed, 1 push refused, 2 internal error
#
# Classification by path:
#   docs/**, *.md (top-level)             → DOC_ONLY
#   lib/agents/**, lib/skills/**,
#     lib/rules/**, lib/playbooks/**      → MINOR_ADDITIVE (new file)
#                                           PATCH_REFINEMENT (modified)
#   lib/hooks/** (non-lib subdir)         → MINOR_ADDITIVE (new) / PATCH_REFINEMENT (mod)
#   cli/lib/**, lib/hooks/lib/**          → PATCH_REFACTOR
#   *_schema.json, frontmatter spec docs  → MAJOR_BREAKING
#   anything else                         → DEFAULT_PATCH
#
# Required bump from highest classification:
#   MAJOR_BREAKING  → major required
#   MINOR_ADDITIVE  → minor required
#   PATCH_*         → patch required
#   DOC_ONLY only   → no bump required (push allowed)

set -euo pipefail

# ---- Setup -----------------------------------------------------------------

GATE_LOG_DIR="${YAKOS_GATE_LOG_DIR:-$HOME/.yakos}"
GATE_LOG="${GATE_LOG_DIR}/gate-log.ndjson"

log_decision() {
    # $1=decision (allow|refuse|override|error)
    # $2=reason (free text, no newlines)
    # $3=required-tier (major|minor|patch|hotfix|none)
    # $4=actual-bump (major|minor|patch|hotfix|none|unknown)
    mkdir -p "$GATE_LOG_DIR" 2>/dev/null || true
    local ts
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    local repo
    repo="$(git rev-parse --show-toplevel 2>/dev/null || echo unknown)"
    local head
    head="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
    local last_tag
    last_tag="${LAST_TAG:-none}"
    printf '{"ts":"%s","repo":"%s","head":"%s","last_tag":"%s","decision":"%s","required":"%s","actual":"%s","reason":"%s"}\n' \
        "$ts" "$repo" "$head" "$last_tag" "$1" "$3" "$4" "$2" \
        >> "$GATE_LOG" 2>/dev/null || true
}

verbose() {
    if [ "${YAKOS_GATE_VERBOSE:-0}" = "1" ]; then
        printf '%s\n' "$*" >&2
    fi
}

# ---- Override path ---------------------------------------------------------

if [ "${YAKOS_GATE_DISABLE:-0}" = "1" ]; then
    log_decision override "YAKOS_GATE_DISABLE=1" unknown unknown
    echo "yakos pre-push gate: bypassed via YAKOS_GATE_DISABLE=1 (logged)" >&2
    # Drain stdin; some git versions block on it.
    cat >/dev/null 2>&1 || true
    exit 0
fi

# Drain stdin (we don't need ref-level data in v0.2)
cat >/dev/null 2>&1 || true

# ---- Inside a git repo? ---------------------------------------------------

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "yakos pre-push gate: not inside a git repo; nothing to gate" >&2
    log_decision allow "not-a-git-repo" none none
    exit 0
fi

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# ---- Find last version tag ------------------------------------------------

# Match v0.0.0 OR v0.0.0.0 (three- or four-part).
LAST_TAG="$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' HEAD 2>/dev/null || echo "")"

if [ -z "$LAST_TAG" ]; then
    echo "yakos pre-push gate: no prior version tag found; allowing push (first release scenario)" >&2
    log_decision allow "no-prior-tag" none none
    exit 0
fi

verbose "yakos pre-push gate: last tag = $LAST_TAG"

# ---- VERSION change? -------------------------------------------------------

VERSION_FILE="${YAKOS_VERSION_PATH:-VERSION}"
version_changed=0
version_at_tag=""
version_at_head=""

if [ -f "$VERSION_FILE" ]; then
    version_at_head="$(tr -d '[:space:]' < "$VERSION_FILE")"
    version_at_tag="$(git show "$LAST_TAG:$VERSION_FILE" 2>/dev/null | tr -d '[:space:]' || echo "")"
    if [ "$version_at_head" != "$version_at_tag" ] && [ -n "$version_at_head" ]; then
        version_changed=1
    fi
fi

# Detect hotfix-only bump (only the 4th component changed). Hotfix is
# emergency tier and bypasses classification — allow the push.
if [ "$version_changed" -eq 1 ] && [ -n "$version_at_tag" ]; then
    # Parse both versions as up to 4 components.
    IFS='.' read -r htM htm htp hth <<< "$version_at_head"
    IFS='.' read -r ttM ttm ttp tth <<< "$version_at_tag"
    htM="${htM:-0}"; htm="${htm:-0}"; htp="${htp:-0}"; hth="${hth:-0}"
    ttM="${ttM:-0}"; ttm="${ttm:-0}"; ttp="${ttp:-0}"; tth="${tth:-0}"
    if [ "$htM" = "$ttM" ] && [ "$htm" = "$ttm" ] && [ "$htp" = "$ttp" ] && [ "$hth" != "$tth" ]; then
        echo "yakos pre-push gate: hotfix-only bump ($version_at_tag → $version_at_head); allowing push" >&2
        log_decision allow "hotfix-bump" hotfix hotfix
        exit 0
    fi
fi

# ---- Classify changed files ----------------------------------------------

# Gather changed files since the tag.
CHANGED="$(git diff --name-only --diff-filter=ACMRT "$LAST_TAG..HEAD" 2>/dev/null || echo "")"
NEW="$(git diff --name-only --diff-filter=A "$LAST_TAG..HEAD" 2>/dev/null || echo "")"

if [ -z "$CHANGED" ]; then
    echo "yakos pre-push gate: no file changes since $LAST_TAG; allowing push" >&2
    log_decision allow "no-changes" none none
    exit 0
fi

classify_file() {
    # Echo the classification for a path.
    # $1=path, $2=is-new (1 or 0)
    local path="$1" is_new="$2"
    case "$path" in
        # Schema-defining files: major
        *_schema.json|*-schema.json|docs/architecture/*frontmatter*spec*) echo MAJOR_BREAKING; return ;;
        # Doc paths: doc-only
        docs/*|*.md|README|README.*|LICENSE*|CONTRIBUTING*|CHANGELOG*|VERSION) echo DOC_ONLY; return ;;
        # Skills/agents/rules/playbooks: additive vs refinement
        lib/agents/*|lib/skills/*|lib/rules/*|lib/playbooks/*)
            if [ "$is_new" = "1" ]; then echo MINOR_ADDITIVE; else echo PATCH_REFINEMENT; fi
            return
            ;;
        # Hook scripts: additive vs refinement (excluding lib/hooks/lib internals)
        lib/hooks/lib/*) echo PATCH_REFACTOR; return ;;
        lib/hooks/*)
            if [ "$is_new" = "1" ]; then echo MINOR_ADDITIVE; else echo PATCH_REFINEMENT; fi
            return
            ;;
        # CLI internals: refactor
        cli/lib/*|cli/yakos) echo PATCH_REFACTOR; return ;;
        # Tests, fixtures: doc-class for gating purposes (test-only changes
        # don't need a version bump under our v0.2 rules)
        tests/*) echo DOC_ONLY; return ;;
        # Examples: doc-only (they're demonstrations, not framework code)
        examples/*) echo DOC_ONLY; return ;;
        # Anything else: default to patch (conservative — require a bump)
        *) echo DEFAULT_PATCH; return ;;
    esac
}

# Build a deduplicated set of new files for is_new lookup.
new_set=" $(echo "$NEW" | tr '\n' ' ') "

# Track highest classification seen + per-class counts.
highest=DOC_ONLY
declare -i n_doc=0 n_patch=0 n_minor=0 n_major=0
classifications=""

while IFS= read -r f; do
    [ -n "$f" ] || continue
    is_new=0
    case "$new_set" in *" $f "*) is_new=1 ;; esac
    cls="$(classify_file "$f" "$is_new")"
    classifications="$classifications$cls $f"$'\n'
    case "$cls" in
        DOC_ONLY)         n_doc=$((n_doc + 1)) ;;
        DEFAULT_PATCH|PATCH_REFACTOR|PATCH_REFINEMENT) n_patch=$((n_patch + 1)) ;;
        MINOR_ADDITIVE)   n_minor=$((n_minor + 1)) ;;
        MAJOR_BREAKING)   n_major=$((n_major + 1)) ;;
    esac
done <<< "$CHANGED"

# Compute the required tier from highest classification.
required="none"
if [ "$n_major" -gt 0 ]; then
    highest=MAJOR_BREAKING; required=major
elif [ "$n_minor" -gt 0 ]; then
    highest=MINOR_ADDITIVE; required=minor
elif [ "$n_patch" -gt 0 ]; then
    highest=PATCH; required=patch
elif [ "$n_doc" -gt 0 ]; then
    highest=DOC_ONLY; required=none
fi

verbose "classification: doc=$n_doc patch=$n_patch minor=$n_minor major=$n_major (highest=$highest, required=$required)"

# ---- Decide allow / refuse ------------------------------------------------

if [ "$required" = "none" ]; then
    echo "yakos pre-push gate: doc-only changes since $LAST_TAG; allowing push" >&2
    log_decision allow "doc-only" none none
    exit 0
fi

# At this point a bump is required. Did the VERSION change?
if [ "$version_changed" -eq 1 ]; then
    echo "yakos pre-push gate: VERSION changed ($version_at_tag → $version_at_head); allowing push (required: $required)" >&2
    log_decision allow "version-bumped" "$required" "see-version-file"
    exit 0
fi

# Refuse.
echo "" >&2
echo "yakos pre-push gate: changes since $LAST_TAG require a $required bump." >&2
echo "" >&2
echo "Files classified:" >&2
case "$required" in
    major) echo "  MAJOR_BREAKING ($n_major file(s)):" >&2 ;;
    minor) echo "  MINOR_ADDITIVE ($n_minor file(s)):" >&2 ;;
    patch) echo "  PATCH_* ($n_patch file(s)):" >&2 ;;
esac
# Show up to 5 files at the highest classification.
shown=0
while IFS= read -r line; do
    [ -n "$line" ] || continue
    case "$line" in
        "$highest"*|MINOR_ADDITIVE*|MAJOR_BREAKING*|PATCH_*|DEFAULT_PATCH*)
            cls="${line%% *}"
            if [ "$required" = "major" ] && [ "$cls" = "MAJOR_BREAKING" ]; then
                echo "    ${line#* }" >&2
                shown=$((shown + 1))
            elif [ "$required" = "minor" ] && [ "$cls" = "MINOR_ADDITIVE" ]; then
                echo "    ${line#* }" >&2
                shown=$((shown + 1))
            elif [ "$required" = "patch" ] && case "$cls" in PATCH_*|DEFAULT_PATCH) true ;; *) false ;; esac; then
                echo "    ${line#* }" >&2
                shown=$((shown + 1))
            fi
            [ "$shown" -ge 5 ] && break
            ;;
    esac
done <<< "$classifications"
[ "$shown" -ge 5 ] && echo "    ..." >&2

echo "" >&2
echo "To proceed:" >&2
echo "  yakos version-bump --component $required" >&2
echo "  git push   # retry" >&2
echo "" >&2
echo "To override (use only when you know what you're doing):" >&2
echo "  YAKOS_GATE_DISABLE=1 git push   # logged to $GATE_LOG" >&2
echo "" >&2

log_decision refuse "version-not-bumped" "$required" none
exit 1
