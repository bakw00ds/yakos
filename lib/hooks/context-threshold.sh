#!/usr/bin/env bash
# Purpose: context-threshold.sh — emit NOTE at 75%; auto-checkpoint at 90%.
#
# Hook context: UserPromptSubmit (cheap; runs before every prompt).
# Reads:  $YAKOS_RUNTIME (env)
#         ~/.yakos-state/settings.json (.context_thresholds.notice, .warning)
#         per-runtime token-usage probe (claude: transcript size estimate)
# Writes: <work>/current/logs/context-threshold.ndjson
#         <work>/current/checkpoints/<iso>/ (if >= warning threshold)
#
# Telemetry hook — always exit 0.

set -eu

HOOK_DIR="$(cd "$(dirname -- "$0")" && pwd -P)"
# shellcheck source=lib/hook-input.sh
. "$HOOK_DIR/lib/hook-input.sh"
# shellcheck source=lib/hook-output.sh
. "$HOOK_DIR/lib/hook-output.sh"
# shellcheck source=lib/paths.sh
. "$HOOK_DIR/lib/paths.sh"

hi_init

# Defaults (operator-tunable via ~/.yakos-state/settings.json)
NOTICE_PCT=75
WARNING_PCT=90
settings_file="$HOME/.yakos-state/settings.json"
if [ -f "$settings_file" ] && command -v jq >/dev/null 2>&1; then
    n="$(jq -r '.context_thresholds.notice // empty' "$settings_file" 2>/dev/null)"
    w="$(jq -r '.context_thresholds.warning // empty' "$settings_file" 2>/dev/null)"
    case "$n" in ''|*[!0-9]*) : ;; *) NOTICE_PCT="$n" ;; esac
    case "$w" in ''|*[!0-9]*) : ;; *) WARNING_PCT="$w" ;; esac
fi

current_dir="$(yakos_current_dir)"
[ -d "$current_dir" ] || exit 0

# --- Token-usage probe (per-runtime) ---------------------------------
# Returns approximate percent of context window used (0-100).
# TODO(M3.1): each runtime has a different probe. Start with claude;
# codex and agy follow once probes are confirmed.

_probe_context_pct_claude() {
    # Estimate from transcript JSONL size. claude transcripts live at
    # ~/.claude/projects/<encoded>/transcript-<id>.jsonl. The
    # session_id is available via hi_session_id; the encoded project
    # path via ct_encode_project_path.
    local session_id encoded transcript size estimated_tokens window_size pct
    session_id="$(hi_session_id 2>/dev/null || true)"
    [ -n "$session_id" ] || return 1

    local project="${CLAUDE_PROJECT_DIR:-$PWD}"
    encoded="$(ct_encode_project_path "$project")"
    transcript="$HOME/.claude/projects/$encoded/transcript-$session_id.jsonl"
    [ -f "$transcript" ] || return 1

    # Rough estimate: bytes / 4 = tokens. Refine in M3.1 with real probe.
    size="$(wc -c < "$transcript" 2>/dev/null || echo 0)"
    estimated_tokens=$((size / 4))

    # Window size — heuristic per model. TODO(M3.1): read from session
    # metadata if available; otherwise default to 200k (Sonnet 4.6 / Opus 4.7).
    window_size=200000
    pct=$((estimated_tokens * 100 / window_size))
    [ "$pct" -gt 100 ] && pct=100
    printf '%d\n' "$pct"
}

_probe_context_pct_codex() {
    # TODO(M3.1): codex transcript probe. Likely ~/.codex/sessions/<id>.
    return 1
}

_probe_context_pct_agy() {
    # TODO(M3.1): agy transcript probe. ~/.config/antigravity/ or similar.
    return 1
}

runtime="${YAKOS_RUNTIME:-claude}"
pct=""
case "$runtime" in
    claude) pct="$(_probe_context_pct_claude || true)" ;;
    codex)  pct="$(_probe_context_pct_codex  || true)" ;;
    agy|gemini) pct="$(_probe_context_pct_agy || true)" ;;
esac

# If we couldn't probe, log REPORT and exit. Don't fail.
if [ -z "$pct" ]; then
    ho_log "context-threshold" REPORT "probe_unavailable" "runtime=$runtime" \
        "{\"runtime\": \"$runtime\"}"
    exit 0
fi

# --- Threshold logic -------------------------------------------------

if [ "$pct" -ge "$WARNING_PCT" ]; then
    severity=WARN
    # Auto-checkpoint at warning threshold.
    checkpoint_dir="$current_dir/checkpoints/$(ct_iso_utc)"
    mkdir -p "$checkpoint_dir"
    # Copy key scratchpad files into the checkpoint.
    for f in plan.md contracts.md decisions.md status.md; do
        [ -f "$current_dir/$f" ] && cp "$current_dir/$f" "$checkpoint_dir/" 2>/dev/null
    done
    # Write checkpoint manifest.
    {
        printf 'checkpoint: %s\n' "$(basename "$checkpoint_dir")"
        printf 'context_pct: %s\n' "$pct"
        printf 'session_id: %s\n' "$(hi_session_id 2>/dev/null || echo unknown)"
        printf 'runtime: %s\n' "$runtime"
        printf 'created: %s\n' "$(ct_iso_now_z)"
    } > "$checkpoint_dir/manifest.txt"
    ct_log "WARN: context at $pct% (threshold $WARNING_PCT%). Auto-checkpoint created at $checkpoint_dir. Recommend 'yakos compact now' or '/compact'."
elif [ "$pct" -ge "$NOTICE_PCT" ]; then
    severity=WARN
    ct_log "NOTE: context at $pct% (threshold $NOTICE_PCT%). Recommend 'yakos compact now' or '/compact' before continuing."
else
    severity=REPORT
fi

ho_log "context-threshold" "$severity" "checked" \
    "pct=$pct notice=$NOTICE_PCT warning=$WARNING_PCT runtime=$runtime" \
    "{\"pct\": $pct, \"notice\": $NOTICE_PCT, \"warning\": $WARNING_PCT, \"runtime\": \"$runtime\"}"

exit 0
