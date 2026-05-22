#!/usr/bin/env bash
# Purpose: pre-push-promotion-gate.sh — refuse pushes that violate env promotion order.
#
# This is an ADDITIONAL git pre-push hook composed alongside the existing
# pre-push-version-gate.sh. Installed at <repo>/.git/hooks/pre-push.d/
# if the project's <repo>/.git/hooks/pre-push is the yakOS composer.
#
# Or: extend lib/hooks/git/pre-push-version-gate.sh inline. The cleaner
# answer for M5 is a separate hook composed by a small wrapper. Plan
# documents both options; this is the wrapper-friendly variant.
#
# Behavior:
#   - Pushing to <prod-branch> from any branch ≠ <test-branch> → REFUSE
#   - Pushing to <test-branch> from any branch ≠ <dev-branch> (and not a
#     feature branch) → REFUSE
#   - Direct push to <dev-branch> → ALLOW (dev is the inbox)
#   - YAKOS_PROMOTION_OVERRIDE=1 → bypass + log
#
# Reads:  <repo>/.yakos.yml (environments section)
#         stdin (git pre-push protocol)
# Writes: ~/.yakos-state/gate-log.ndjson

set -eu

YAKOS_LIB="${YAKOS_LIB:-$HOME/.yakos/cli/lib}"
# shellcheck disable=SC1091
. "$YAKOS_LIB/compat.sh"

LOG_DIR="${YAKOS_GATE_LOG_DIR:-$HOME/.yakos-state}"
LOG="$LOG_DIR/gate-log.ndjson"

repo="$(git rev-parse --show-toplevel 2>/dev/null)"
[ -n "$repo" ] || { exit 0; }  # not a git repo
yakos_yml="$repo/.yakos.yml"
[ -f "$yakos_yml" ] || exit 0  # no envs configured; allow

# Override bypass.
if [ "${YAKOS_PROMOTION_OVERRIDE:-0}" = "1" ]; then
    mkdir -p "$LOG_DIR"
    jq -nc \
        --arg ts "$(ct_iso_now_z)" --arg repo "$repo" \
        --arg head "$(git rev-parse HEAD)" \
        --arg decision "override" --arg reason "YAKOS_PROMOTION_OVERRIDE=1" \
        '{ts:$ts, repo:$repo, head:$head, gate:"promotion", decision:$decision, reason:$reason}' \
        >> "$LOG" 2>/dev/null || true
    ct_log "promotion gate: bypass via YAKOS_PROMOTION_OVERRIDE"
    cat > /dev/null  # drain stdin
    exit 0
fi

# Read configured branches. Simple awk parser; no proper yaml dep.
_yaml_get() {
    # _yaml_get <env> "branch" — returns the branch for that env.
    awk -v env="$1" -v key="$2" '
        $0 ~ "^  " env ":" { in_env=1; next }
        in_env && $0 ~ "^  [a-z]" && $0 !~ "^    " { in_env=0 }
        in_env && $0 ~ "^    " key ": " {
            sub("^    " key ": ", ""); gsub(/^"|"$/, "")
            print; exit
        }
    ' "$yakos_yml"
}

dev_branch="$(_yaml_get dev branch)"
test_branch="$(_yaml_get test branch)"
prod_branch="$(_yaml_get prod branch)"

# Read stdin: each line is "<local_ref> <local_oid> <remote_ref> <remote_oid>".
violation=""
while read -r local_ref local_oid remote_ref remote_oid; do
    # Strip refs/heads/ prefix.
    target_branch="${remote_ref#refs/heads/}"
    source_branch="${local_ref#refs/heads/}"

    case "$target_branch" in
        "$prod_branch")
            if [ "$source_branch" != "$test_branch" ]; then
                violation="$violation\n  pushing $source_branch → $prod_branch (must promote via $test_branch)"
            fi
            ;;
        "$test_branch")
            # Allow push from dev_branch, or from feature branches that PR through dev.
            # For v1: refuse direct pushes from anything other than dev_branch.
            # Feature branches → test direct is rare and probably wrong; refuse.
            if [ "$source_branch" != "$test_branch" ] && [ "$source_branch" != "$dev_branch" ]; then
                violation="$violation\n  pushing $source_branch → $test_branch (must promote via $dev_branch)"
            fi
            ;;
    esac
done

if [ -n "$violation" ]; then
    cat >&2 <<EOF

╭─ yakOS promotion gate ─────────────────────────────────────╮
│ Refused. Direct pushes outside the dev → test → prod        │
│ promotion order are not allowed.                             │
$violation
│                                                              │
│ To promote properly:                                         │
│   yakos env promote dev test                                 │
│   yakos env promote test prod                                │
│                                                              │
│ To bypass (logged):                                          │
│   YAKOS_PROMOTION_OVERRIDE=1 git push                        │
│                                                              │
│ See lib/rules/environment-discipline.md.                     │
╰──────────────────────────────────────────────────────────────╯

EOF
    mkdir -p "$LOG_DIR"
    jq -nc \
        --arg ts "$(ct_iso_now_z)" --arg repo "$repo" \
        --arg head "$(git rev-parse HEAD)" \
        --arg decision "refuse" \
        --arg reason "$violation" \
        '{ts:$ts, repo:$repo, head:$head, gate:"promotion", decision:$decision, reason:$reason}' \
        >> "$LOG" 2>/dev/null || true
    exit 1
fi

# All good. Log allow.
mkdir -p "$LOG_DIR"
jq -nc \
    --arg ts "$(ct_iso_now_z)" --arg repo "$repo" \
    --arg head "$(git rev-parse HEAD)" \
    --arg decision "allow" --arg reason "promotion order ok" \
    '{ts:$ts, repo:$repo, head:$head, gate:"promotion", decision:$decision, reason:$reason}' \
    >> "$LOG" 2>/dev/null || true
exit 0
