#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-retro-bash-roundtrip-test.sh — bash `yakos retro` disable/enable round
# trip against the real cycle-counter hook (K-89 review finding 1).
#
# `retro.sh disable|enable` used to crash with "$2: unbound variable"
# (_retro_settings_set took two args, callers passed one expression), so the
# bash CLI never wrote the flag. This drives disable -> status -> hook ->
# enable -> status -> hook end to end.

set -eu
REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
RETRO="$REPO_ROOT/cli/lib/retro.sh"
HOOK="$REPO_ROOT/lib/hooks/cycle-counter.sh"

pass=0; fail=0
ok()  { printf '  \033[32mOK\033[0m   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-retro-rt-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM
export HOME="$TMP/home"
export YAKOS_LIB="$REPO_ROOT/cli/lib"
export YAKOS_PROJECT_NAME="rt"
export YAKOS_WORK_DIR="$HOME/agent-control/rt/work"
CUR="$YAKOS_WORK_DIR/current"
mkdir -p "$CUR/logs" "$HOME/.yakos-state"

retro() { bash "$RETRO" "$@" 2>&1; }
status_auto() { retro status | sed -n 's/^  Auto-dispatch: *//p'; }
fire_hook() { # reach cycle 10 fresh
    rm -f "$CUR/.retro-due"; printf '9\n' > "$CUR/.cycle-count"
    printf '{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"p"}\n' \
        | bash "$HOOK" >/dev/null 2>&1 || true
}

for fresh in nofile withfile; do
    echo "=== round trip ($fresh) ==="
    rm -f "$HOME/.yakos-state/settings.json"
    [ "$fresh" = withfile ] && echo '{"other":1,"retro":{"cycle_length":10}}' > "$HOME/.yakos-state/settings.json"

    out="$(retro disable)" && ok "$fresh: disable exits 0" || bad "$fresh: disable failed: $out"
    [ "$(jq -r '.retro.auto_dispatch' "$HOME/.yakos-state/settings.json" 2>/dev/null)" = false ] \
        && ok "$fresh: settings.json auto_dispatch=false" || bad "$fresh: settings.json not false"
    [ "$fresh" = withfile ] && { [ "$(jq -r '.other' "$HOME/.yakos-state/settings.json")" = 1 ] \
        && ok "$fresh: other keys preserved" || bad "$fresh: other keys lost"; }
    [ "$(status_auto)" = false ] && ok "$fresh: status reports false" || bad "$fresh: status = $(status_auto)"
    fire_hook
    [ ! -f "$CUR/.retro-due" ] && ok "$fresh: no .retro-due after disable" || bad "$fresh: .retro-due written after disable"

    out="$(retro enable)" && ok "$fresh: enable exits 0" || bad "$fresh: enable failed: $out"
    [ "$(status_auto)" = true ] && ok "$fresh: status reports true" || bad "$fresh: status = $(status_auto)"
    fire_hook
    [ -f "$CUR/.retro-due" ] && ok "$fresh: .retro-due written after enable" || bad "$fresh: no .retro-due after enable"
done

printf '\n  %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
