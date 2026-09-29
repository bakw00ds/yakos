#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-supervisor-stream-test.sh — K-112 (a) regression test for
# lib/hooks/supervisor-stream.sh and its Go twin.
#
# (a) Bash tool calls: tool_input.command / description reach the risk-regex
#     pre-filter. rm -rf, curl | sh, git push --force and a `>` write to a
#     sensitive path ESCALATE; a benign `ls` does not. The bash and Go sides
#     must agree on the trigger and on the buffered event.
#
# The Go half runs only when bin/yakos exists (make build). Run under both
# `bash` and `/bin/bash`.
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOK="$REPO_ROOT/lib/hooks/supervisor-stream.sh"
FIXT="$REPO_ROOT/tests/fixtures/hooks"
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"
HAVE_GO=1; [ -x "$GO_BINARY" ] || HAVE_GO=0

unset YAKOS_ROOT YAKOS_LIB YAKOS_CLI YAKOS_SUPERVISOR_DISABLE
pass=0; fail=0
ok()  { printf '  OK   %s\n' "$1"; pass=$((pass + 1)); }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

TMP="$(mktemp -d -t yakos-ss-test-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# run_side <bash|go> <sandbox> <fixture> [extra env assignments...]
run_side() {
    local side="$1" sb="$2" fixture="$3"; shift 3
    if [ "$side" = "bash" ]; then
        env "$@" YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "${BASH:-bash}" "$HOOK" < "$FIXT/$fixture" >/dev/null 2>&1
    else
        env "$@" YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$sb/work" CLAUDE_PROJECT_DIR="$sb" "$GO_BINARY" hook run supervisor-stream < "$FIXT/$fixture" >/dev/null 2>&1
    fi
}

mksb() { # mksb <name> <yml-body>
    local sb="$TMP/$1"
    mkdir -p "$sb/.claude" "$sb/work/current/logs"
    printf '%s' "$2" > "$sb/.yakos.yml"
    printf '%s' "$sb"
}

# ---- (a) escalation ---------------------------------------------------------
sides="bash"; [ "$HAVE_GO" = 1 ] && sides="bash go"
for spec in "posttooluse-bash-ss-rm-rf.json:1" "posttooluse-bash-ss-curl-pipe-sh.json:1" \
            "posttooluse-bash-ss-git-push-force.json:1" "posttooluse-bash-ss-redirect-env.json:1" \
            "posttooluse-bash-ss-ls.json:0"; do
    fx="${spec%%:*}"; want="${spec##*:}"
    bufs=""
    for side in $sides; do
        sb="$(mksb "a-$side-$fx" $'supervisor:\n  score_every_n_calls: 1000\n')"
        run_side "$side" "$sb" "$fx"
        log="$sb/work/current/logs/supervisor-stream.ndjson"
        got=0
        if grep -q 'ESCALATE\|"pre_filter":"escalate"' "$log" 2>/dev/null; then got=1; fi
        if [ "$got" = "$want" ]; then
            ok "(a) $side $fx escalate=$got"
        else
            bad "(a) $side $fx escalate=$got want $want"
        fi
        # Escalation must have ticked the counter iff it escalated.
        cnt=0; [ -f "$sb/work/current/.supervisor-counter" ] && cnt="$(tr -d '[:space:]' < "$sb/work/current/.supervisor-counter")"
        [ "$cnt" = "$want" ] || bad "(a) $side $fx counter=$cnt want $want"
        bufs="$bufs$(tail -n 1 "$sb/work/current/supervisor-buffer.ndjson" | jq -cS 'del(.ts)')"$'\n'
    done
    if [ "$HAVE_GO" = 1 ]; then
        b1="$(printf '%s' "$bufs" | sed -n 1p)"; b2="$(printf '%s' "$bufs" | sed -n 2p)"
        if [ "$b1" = "$b2" ]; then ok "(a) $fx buffered event identical bash/go"; else bad "(a) $fx buffer differs: bash=$b1 go=$b2"; fi
    fi
    # The command must be buffered as a preview.
    if [ "$want" = 1 ]; then
        printf '%s' "$bufs" | sed -n 1p | jq -e '.input.command_preview != null' >/dev/null 2>&1 \
            && ok "(a) $fx command_preview buffered" || bad "(a) $fx command_preview missing"
    fi
done

printf '\nsupervisor-stream: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
