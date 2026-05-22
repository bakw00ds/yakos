#!/usr/bin/env bash
# Purpose: skill.sh — manage skill candidates produced by the librarian.
#
# Subcommands:
#   yakos skill candidates                 # list pending candidates
#   yakos skill candidates --review        # interactive review
#   yakos skill promote <slug> [--global]  # candidate → real SKILL.md
#   yakos skill reject  <slug> [--reason]  # discard with audit-log entry
#   yakos skill defer   <slug> <N>         # re-propose after N more cycles
#   yakos skill stats                      # promotion / rejection ratios
#
# Reads:  <work>/current/skill-candidates.md (librarian-written)
#         ~/.yakos-state/skill-graveyard.ndjson (rejected history)
# Writes: <project>/.claude/skills/<slug>/SKILL.md (on promote)
#         lib/skills/<slug>/SKILL.md (on promote --global; rare)
#         ~/.yakos-state/promotion-log.ndjson
#         ~/.yakos-state/skill-graveyard.ndjson (on reject)
#         <work>/current/skill-candidates.md (removes promoted/rejected entries)

set -eu

: "${YAKOS_LIB:?skill.sh: YAKOS_LIB must be set}"
# shellcheck source=compat.sh
. "$YAKOS_LIB/compat.sh"
# shellcheck source=paths.sh
. "$YAKOS_LIB/paths.sh"

PROMOTION_LOG="$HOME/.yakos-state/promotion-log.ndjson"
GRAVEYARD="$HOME/.yakos-state/skill-graveyard.ndjson"

# --- Helpers ---------------------------------------------------------

_skill_candidates_file() {
    local current_dir
    current_dir="$(yakos_current_dir)"
    printf '%s/skill-candidates.md' "$current_dir"
}

_skill_extract_candidate() {
    # Print a single candidate section from candidates.md by slug.
    # Sections look like "## candidate: <slug> (<iso>)"; section ends at
    # next "## " heading.
    local slug="$1" file="$2"
    awk -v s="$slug" '
        /^## candidate: / {
            in_sec = ($0 ~ "## candidate: " s " ")
            if (in_sec) { print; next }
        }
        in_sec { if (/^## /) { in_sec=0; next } else print }
    ' "$file"
}

_skill_evidence_fingerprint() {
    # Hash a candidate's source-evidence cycle numbers into a fingerprint.
    # Per Plan 3 §16.1: librarian could re-propose a rejected idea under a
    # new slug ('cleanup-files' → 'clean-up-files') and bypass repeat-
    # rejection. Fingerprint dedup catches identical-evidence proposals
    # regardless of slug paraphrase.
    local slug="$1" file="$2"
    local cycles
    cycles="$(_skill_extract_candidate "$slug" "$file" 2>/dev/null \
        | grep -oE 'Cycle [0-9]+' | sort -u | tr '\n' ',')"
    [ -n "$cycles" ] || { printf 'no-evidence\n'; return; }
    if command -v sha256sum >/dev/null 2>&1; then
        printf '%s' "$cycles" | sha256sum | awk '{print substr($1, 1, 12)}'
    elif command -v shasum >/dev/null 2>&1; then
        printf '%s' "$cycles" | shasum -a 256 | awk '{print substr($1, 1, 12)}'
    else
        # Fallback: cksum (32-bit; weak but functional).
        printf '%s' "$cycles" | cksum | awk '{printf "cksum-%s\n", $1}'
    fi
}

_skill_graveyard_count() {
    # Count rejections matching this slug OR the same evidence fingerprint
    # (catches renamed-but-identical candidates per Plan 3 §16.1).
    local slug="$1" fingerprint="${2:-}"
    [ -f "$GRAVEYARD" ] || { echo 0; return; }
    if [ -n "$fingerprint" ] && [ "$fingerprint" != "no-evidence" ]; then
        jq -r --arg s "$slug" --arg fp "$fingerprint" \
            'select(.slug == $s or .evidence_fingerprint == $fp) | .slug' \
            "$GRAVEYARD" 2>/dev/null | wc -l | tr -d ' '
    else
        jq -r --arg s "$slug" 'select(.slug == $s) | .slug' \
            "$GRAVEYARD" 2>/dev/null | wc -l | tr -d ' '
    fi
}

_skill_promotion_log() {
    # Append to promotion log (NDJSON).
    local action="$1" slug="$2" extra="${3:-{\}}"
    local ts
    ts="$(ct_iso_now_z)"
    mkdir -p "$(dirname "$PROMOTION_LOG")"
    local line
    line="$(jq -nc \
        --arg ts "$ts" --arg a "$action" --arg s "$slug" \
        --argjson extra "$extra" \
        '{ts: $ts, action: $a, slug: $s} + $extra')"
    printf '%s\n' "$line" >> "$PROMOTION_LOG"
}

_skill_strip_candidate() {
    # Remove a single candidate section from candidates.md.
    local slug="$1" file="$2"
    local tmp
    tmp="$(mktemp -t yakos-skill.XXXXXX)"
    awk -v s="$slug" '
        /^## candidate: / { in_sec = ($0 ~ "## candidate: " s " ") }
        !in_sec { print }
    ' "$file" > "$tmp" && mv "$tmp" "$file"
}

# --- Subcommands -----------------------------------------------------

cmd_candidates() {
    local file
    file="$(_skill_candidates_file)"
    [ -f "$file" ] || { echo "(no pending candidates)"; return 0; }

    # List candidates with slug + confidence + evidence count.
    awk '
        /^## candidate: / {
            sub("^## candidate: ", ""); slug=$1
            sub(" .*", "", slug)
        }
        /\*\*Confidence\*\*:/ { conf=$2 }
        /\*\*Source evidence\*\*/ { evid_count=0 }
        /^- Cycle [0-9]+/ { evid_count++ }
        /^## / && !/^## candidate: / && slug != "" {
            printf "  %s (conf=%s, evidence=%d)\n", slug, conf, evid_count
            slug=""; conf=""; evid_count=0
        }
        END {
            if (slug != "") {
                printf "  %s (conf=%s, evidence=%d)\n", slug, conf, evid_count
            }
        }
    ' "$file"

    if [ "${1:-}" = "--review" ]; then
        # TODO(M2): interactive loop — for each candidate, show full
        # body, prompt for [p]romote/[r]eject/[d]efer/[s]kip. Loop
        # until exhausted.
        ct_log "interactive review not yet implemented (M2 follow-up)"
    fi
}

cmd_promote() {
    local slug="$1"
    local global=0
    [ "${2:-}" = "--global" ] && global=1

    local file project
    file="$(_skill_candidates_file)"
    project="$(cat "$(yakos_current_dir)/../../.project-path" 2>/dev/null)"
    [ -d "$project" ] || ct_die "could not resolve project dir"

    # Check graveyard for repeat-rejection signal — by slug AND by evidence
    # fingerprint, so renamed candidates don't bypass the warning (§16.1).
    local fingerprint rejected_count
    fingerprint="$(_skill_evidence_fingerprint "$slug" "$file")"
    rejected_count="$(_skill_graveyard_count "$slug" "$fingerprint")"
    if [ "$rejected_count" -ge 3 ]; then
        ct_log "WARN: '$slug' (or equivalent-evidence variant) has been rejected $rejected_count times before."
        ct_log "      Evidence fingerprint: $fingerprint"
        printf "Promote anyway? [y/N] " >&2
        read -r ans
        [ "$ans" = "y" ] || { ct_log "aborted"; exit 0; }
    fi

    local body
    body="$(_skill_extract_candidate "$slug" "$file")"
    [ -n "$body" ] || ct_die "candidate '$slug' not found in $file"

    local skill_dir
    if [ "$global" = "1" ]; then
        skill_dir="$YAKOS_ROOT/lib/skills/$slug"
    else
        skill_dir="$project/.claude/skills/$slug"
    fi

    mkdir -p "$skill_dir"
    # Render candidate body through skill.template.md.
    # TODO(M2): real template rendering with name/description/etc fields
    # extracted from candidate body. For draft, inline a minimal version.
    {
        printf '%s\n' '---'
        printf 'name: %s\n' "$slug"
        printf 'description: "<filled-by-template-on-promote>"\n'
        printf 'allowed-tools: Read Edit Bash Grep\n'
        printf 'argument-hint: "<filled>"\n'
        printf 'mode: [implement]\n'
        printf '%s\n\n' '---'
        printf '# %s\n\n' "$slug"
        printf '%s\n' "$body"
    } > "$skill_dir/SKILL.md"

    # Validate the new skill.
    if ! "$YAKOS_LIB/validate.sh" --strict "$project" >/dev/null 2>&1; then
        ct_log "ERROR: promoted skill failed yakos validate --strict; reverting"
        rm -rf "$skill_dir"
        _skill_promotion_log "promote_failed" "$slug" "{\"reason\": \"validate_failed\"}"
        exit 1
    fi

    _skill_promotion_log "promoted" "$slug" "{\"global\": $global, \"by_user\": \"$(id -un)\"}"
    _skill_strip_candidate "$slug" "$file"
    ct_log "promoted: $skill_dir/SKILL.md"
}

cmd_reject() {
    local slug="$1"
    shift
    local reason=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --reason) reason="$2"; shift 2 ;;
            *) shift ;;
        esac
    done

    local file
    file="$(_skill_candidates_file)"
    local body
    body="$(_skill_extract_candidate "$slug" "$file")"
    [ -n "$body" ] || ct_die "candidate '$slug' not found"

    # Compute evidence fingerprint BEFORE stripping (§16.1).
    local fingerprint
    fingerprint="$(_skill_evidence_fingerprint "$slug" "$file")"

    # Append to graveyard with fingerprint for later dedup.
    mkdir -p "$(dirname "$GRAVEYARD")"
    local entry
    entry="$(jq -nc \
        --arg ts "$(ct_iso_now_z)" --arg s "$slug" --arg r "$reason" \
        --arg u "$(id -un)" --arg fp "$fingerprint" \
        '{ts: $ts, slug: $s, reason: $r, by_user: $u, evidence_fingerprint: $fp}')"
    printf '%s\n' "$entry" >> "$GRAVEYARD"

    _skill_promotion_log "rejected" "$slug" \
        "{\"reason\": \"$reason\", \"evidence_fingerprint\": \"$fingerprint\"}"
    _skill_strip_candidate "$slug" "$file"
    ct_log "rejected: $slug (fingerprint $fingerprint)"
}

cmd_defer() {
    local slug="$1" cycles="$2"
    # Mark candidate with a "defer-until cycle" annotation; librarian
    # filters these out until the cycle count exceeds the defer marker.
    # TODO(M2): librarian needs to read this annotation. For draft,
    # write to a sidecar file <work>/current/skill-deferrals.ndjson.
    local deferrals_file
    deferrals_file="$(yakos_current_dir)/skill-deferrals.ndjson"
    local current_cycle
    current_cycle="$(cat "$(yakos_current_dir)/.cycle-count" 2>/dev/null || echo 0)"
    local entry
    entry="$(jq -nc --arg s "$slug" --arg cur "$current_cycle" --arg n "$cycles" \
        '{slug: $s, deferred_at_cycle: ($cur | tonumber), defer_until_cycle: ($cur | tonumber) + ($n | tonumber)}')"
    printf '%s\n' "$entry" >> "$deferrals_file"
    _skill_promotion_log "deferred" "$slug" "{\"cycles\": $cycles}"
    ct_log "deferred '$slug' for $cycles more cycles"
}

cmd_stats() {
    [ -f "$PROMOTION_LOG" ] || { echo "no promotion log yet"; return 0; }
    local proposed promoted rejected deferred
    proposed="$(jq -r 'select(.action == "proposed") | .slug' "$PROMOTION_LOG" 2>/dev/null | wc -l | tr -d ' ')"
    promoted="$(jq -r 'select(.action == "promoted") | .slug' "$PROMOTION_LOG" 2>/dev/null | wc -l | tr -d ' ')"
    rejected="$(jq -r 'select(.action == "rejected") | .slug' "$PROMOTION_LOG" 2>/dev/null | wc -l | tr -d ' ')"
    deferred="$(jq -r 'select(.action == "deferred") | .slug' "$PROMOTION_LOG" 2>/dev/null | wc -l | tr -d ' ')"
    local rate
    rate="$(awk -v p="$promoted" -v t="$proposed" 'BEGIN { printf "%.1f", (t > 0) ? (p/t)*100 : 0 }')"

    cat <<EOF
Skill candidate stats (lifetime, user $(id -un)):
  Proposed:  $proposed
  Promoted:  $promoted
  Rejected:  $rejected
  Deferred:  $deferred
  Promotion rate: ${rate}%
EOF

    # §16.2 — calibration warnings when sample is large enough to be meaningful.
    if [ "$proposed" -ge 100 ]; then
        local rate_int
        rate_int="$(awk -v r="$rate" 'BEGIN { printf "%d", r + 0 }')"
        if [ "$rate_int" -lt 5 ]; then
            echo ""
            ct_log "[warn] promotion rate <5% over $proposed candidates — librarian may be over-eager"
            ct_log "       Tune: lower 'skill_candidates.min_confidence' in ~/.yakos-state/settings.json"
            ct_log "       or 'skill_candidates.min_evidence_count' to slow proposals"
        elif [ "$rate_int" -gt 40 ]; then
            echo ""
            ct_log "[warn] promotion rate >40% over $proposed candidates — librarian may be under-skeptical"
            ct_log "       Tune: raise thresholds in ~/.yakos-state/settings.json"
        fi
    elif [ "$proposed" -gt 0 ] && [ "$proposed" -lt 20 ]; then
        echo ""
        ct_log "[info] sample size $proposed; promotion rate not yet calibration-meaningful (need >=100)"
    fi
}

# --- Dispatch --------------------------------------------------------

case "${1:-help}" in
    candidates) shift; cmd_candidates "$@" ;;
    promote)    shift; cmd_promote "$@" ;;
    reject)     shift; cmd_reject "$@" ;;
    defer)      shift; cmd_defer "$@" ;;
    stats)      shift; cmd_stats "$@" ;;
    help|--help|-h)
        cat <<EOF
yakos skill — manage skill candidates produced by the librarian

Usage:
  yakos skill candidates [--review]
  yakos skill promote <slug> [--global]
  yakos skill reject <slug> [--reason "<text>"]
  yakos skill defer <slug> <N>
  yakos skill stats

See lib/agents/librarian.md and lib/skills/skill-promote/SKILL.md.
EOF
        ;;
    *) ct_die "yakos skill: unknown subcommand '$1' (try 'yakos skill help')" ;;
esac
