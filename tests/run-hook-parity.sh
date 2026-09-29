#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-parity.sh — bash-vs-Go parity harness for the Tier-0 hooks (S-6 A-1).
#
# Modelled directly on tests/run-hook-fixtures.sh (same fixture corpus, same
# temp-$CLAUDE_PROJECT_DIR setup_* functions, same no-jq / no-realpath PATH
# harness, same runtime-assembled synthetic secrets — all reused verbatim
# below) but with a dual-run case_check: for every (hook, fixture,
# expected-outcome) tuple it runs BOTH
#
#   1. bash lib/hooks/<name>.sh          < fixture   (the existing baseline)
#   2. YAKOS_HOOKS=go yakos hook run <name> < fixture (S-6 A-1's new path)
#
# in separate temp CLAUDE_PROJECT_DIR/work sandboxes with identical
# env/payload, and compares: exit code (exact), stdout (exact), the last
# NDJSON log record (parsed and compared field-by-field with `ts` masked —
# a hook that never logs on the bash side is compared as "no record" on
# both), and stderr (compared modulo a leading "[<ts>] " prefix bash's
# ct_log-style hooks add and Go does not, and modulo each side's own
# mktemp -d sandbox path, since those never match across sides even for
# the same fixture).
#
# Gating (K-87 A-2b). The script prints a per-hook matrix (fixtures compared,
# parity count, accepted count, first unaccepted divergence class) and writes
# one NDJSON record per comparison to work/current/logs/hook-parity-report.ndjson.
# Its exit code is non-zero when:
#   * a bash-baseline assertion fails (same as run-hook-fixtures.sh), or
#   * a hook named in YAKOS_PARITY_REQUIRE_HOOKS (default: path-allowlist, the
#     security-critical hook) has any divergence that is not an ACCEPTED one, or
#   * an accept annotation is stale (the two sides now agree; remove it).
# Every other hook stays advisory: it appears in the matrix but cannot fail
# the run, exactly as before.
#
# Accepted divergences. A tuple may carry a 9th argument "<go-rc>:<reason>"
# documenting a KNOWN, INTENTIONAL bash-vs-Go difference. It pins Go's exit
# code (so the case still guards Go's decision) and the reason is printed next
# to the case. Two kinds are in use:
#   * architectural: Go never shells out to jq, so a jq-less or jq-broken PATH
#     cannot put it into bash's "degraded input" state; it evaluates the
#     payload and decides on policy instead. Where bash fails closed the two
#     agree on rc but not on the log record; where bash passes through
#     (YAKOS_HOOKS_FAIL_OPEN=1) Go is stricter. On an ALLOWED path with a
#     missing or garbage-printing jq, bash fails closed (2) and Go allows (0).
#   * bash bug: Go deliberately does NOT reproduce a bash weakness that is not
#     fixed on the bash side (currently: supervisor-gate's jq crash on a
#     non-object finding). path-allowlist's bash weaknesses (lexical-only
#     symlink check, NUL/newline handling, non-array deny, bash 3.2 crash,
#     garbage jq) were fixed in K-87 A-2b round 2 and are exact-parity cases.
#
# Iteration helpers (env):
#   YAKOS_PARITY_ONLY=<hook>       run only that hook's tuples
#   YAKOS_PARITY_FIXTURE=<substr>  run only tuples whose fixture name contains it
#   YAKOS_PARITY_VERBOSE=1         print bash-vs-Go stdout/stderr/log side by
#                                  side for every raw divergence
#   YAKOS_PARITY_REQUIRE_HOOKS     space-separated gate list (default path-allowlist;
#                                  set to "" to make the whole run advisory)
#
# Env:
#   YAKOS_GO_BINARY   Go yakos binary (default: <repo-root>/bin/yakos, same
#                      default as internal/paritytest).
#   YAKOS_HOOK_NOW     forwarded to both sides for deterministic timestamps
#                      (masked out of the log comparison regardless).

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
HOOKS="$REPO_ROOT/lib/hooks"
FIXT="$REPO_ROOT/tests/fixtures/hooks"
# Interpreter the BASH side of every case runs under (default: whatever `bash`
# resolves to). CI sets YAKOS_HOOK_BASH=/bin/bash on macOS to exercise stock
# bash 3.2 explicitly: a case whose PATH is overridden would otherwise pick a
# different bash than the one the script itself runs under.
HOOK_BASH="${YAKOS_HOOK_BASH:-bash}"

# ---- jq resolution (K-81 rider) ---------------------------------------------
#
# This script (and the hooks it drives) need a real jq. A sandboxed PATH
# (CI runners, `env -i`, agent sandboxes) may not include jq's directory
# even though it is installed; resolve it explicitly, put its directory on
# PATH for the rest of the run, and fail with a clear message if there
# genuinely is none. The no-jq cases below build their own jq-less PATH
# from directory listings, so this never leaks jq into them.
if ! command -v jq >/dev/null 2>&1; then
    _jq_found=""
    for _c in /opt/homebrew/bin/jq /usr/local/bin/jq /usr/bin/jq; do
        if [ -x "$_c" ]; then _jq_found="$_c"; break; fi
    done
    if [ -z "$_jq_found" ]; then
        echo "$(basename -- "$0"): jq is required but was not found on PATH, /opt/homebrew/bin, /usr/local/bin or /usr/bin." >&2
        echo "$(basename -- "$0"): install jq (brew install jq / apt-get install jq) and re-run." >&2
        exit 1
    fi
    PATH="$(dirname -- "$_jq_found"):$PATH"
    export PATH
fi
GO_BINARY="${YAKOS_GO_BINARY:-$REPO_ROOT/bin/yakos}"

if [ ! -x "$GO_BINARY" ]; then
    echo "run-hook-parity: Go binary not found or not executable at $GO_BINARY" >&2
    echo "run-hook-parity: build it first (make build) or set YAKOS_GO_BINARY" >&2
    exit 1
fi

REPORT_DIR="${YAKOS_HOOK_PARITY_REPORT_DIR:-$REPO_ROOT/work/current/logs}"
mkdir -p "$REPORT_DIR"
REPORT_FILE="$REPORT_DIR/hook-parity-report.ndjson"
: > "$REPORT_FILE"

pass=0
fail=0
# The per-hook PASS/FAIL matrix is computed at the end from $REPORT_FILE
# (one NDJSON record per case_check call) rather than kept in bash
# associative arrays — those need bash 4+, and macOS ships bash 3.2 as
# /bin/bash, which GitHub's macos-latest runner PATH can still resolve to
# ahead of a newer Homebrew bash. jq is already a hard dependency of this
# repo's hook fixtures (see NOJQ_PATH above — it exists specifically to
# simulate jq's ABSENCE as a test condition, meaning jq is assumed present
# otherwise), so leaning on it here for the summary costs nothing new.
fail_log=""

# ---- no-jq PATH (security review C5 regression coverage) -------------------
#
# A directory of symlinks to every binary on the real PATH except jq, so a
# case can simulate "jq is not installed" while keeping every other tool
# (bash, grep, cat, mkdir, date, ...) available. Built once; cleaned up on
# exit via the trap below.
NOJQ_PATH="$(mktemp -d -t yakos-hookfix-nojq-XXXXXX)"
trap 'rm -rf "$NOJQ_PATH" "$NORESOLVE_PATH" "$FAKEJQ_GARBAGE_PATH" "$FAKEJQ_FAIL_PATH" "$NOYAKOS_PATH"' EXIT
for _dir in /usr/bin /bin /usr/local/bin; do
    [ -d "$_dir" ] || continue
    for _bin in "$_dir"/*; do
        [ -x "$_bin" ] || continue
        _name="$(basename -- "$_bin")"
        [ "$_name" = "jq" ] && continue
        ln -sf "$_bin" "$NOJQ_PATH/$_name" 2>/dev/null || true
    done
done

# ---- no-realpath/python3 PATH (security review N3 regression coverage) -----
#
# Mirrors NOJQ_PATH above, but strips `realpath` and `python3` instead of
# `jq` — the manual cd+pwd -P / readlink fallback in ps_realpath is the
# code path under test, and on this exact platform (macOS) it's the ONLY
# fallback that ever runs anyway, since BSD `realpath` has no `-m` and
# plain `realpath` errors on a non-existent target.
NORESOLVE_PATH="$(mktemp -d -t yakos-hookfix-noresolve-XXXXXX)"
for _dir in /usr/bin /bin /usr/local/bin; do
    [ -d "$_dir" ] || continue
    for _bin in "$_dir"/*; do
        [ -x "$_bin" ] || continue
        _name="$(basename -- "$_bin")"
        case "$_name" in
            realpath|python3) continue ;;
        esac
        ln -sf "$_bin" "$NORESOLVE_PATH/$_name" 2>/dev/null || true
    done
done

# ---- broken-jq PATHs (K-87 A-2b adversarial coverage) ------------------------
#
# Same construction as NOJQ_PATH, but a `jq` IS present and misbehaves:
#   FAKEJQ_GARBAGE_PATH  jq exits 0 and prints "garbage" for every query — a
#                        jq that is present but returns invalid output.
#   FAKEJQ_FAIL_PATH     jq exits 5 with no output — a crashing jq.
FAKEJQ_GARBAGE_PATH="$(mktemp -d -t yakos-hookfix-fakejq-g-XXXXXX)"
FAKEJQ_FAIL_PATH="$(mktemp -d -t yakos-hookfix-fakejq-f-XXXXXX)"
for _dir in /usr/bin /bin /usr/local/bin; do
    [ -d "$_dir" ] || continue
    for _bin in "$_dir"/*; do
        [ -x "$_bin" ] || continue
        _name="$(basename -- "$_bin")"
        [ "$_name" = "jq" ] && continue
        ln -sf "$_bin" "$FAKEJQ_GARBAGE_PATH/$_name" 2>/dev/null || true
        ln -sf "$_bin" "$FAKEJQ_FAIL_PATH/$_name" 2>/dev/null || true
    done
done
printf '#!/bin/sh\necho garbage\n' > "$FAKEJQ_GARBAGE_PATH/jq"
printf '#!/bin/sh\nexit 5\n' > "$FAKEJQ_FAIL_PATH/jq"
chmod +x "$FAKEJQ_GARBAGE_PATH/jq" "$FAKEJQ_FAIL_PATH/jq"

# ---- no-yakos PATH (retro-dispatch) ------------------------------------------
#
# retro-dispatch shells out to `yakos dispatch librarian` in the background
# when a .retro-due marker exists and a yakos binary is found. A fixture must
# never launch that, on a machine where yakos IS installed. This PATH has
# every binary EXCEPT yakos, so the "yakos not found" branch is the only one
# reachable.
NOYAKOS_PATH="$(mktemp -d -t yakos-hookfix-noyakos-XXXXXX)"
for _dir in /usr/bin /bin /usr/local/bin /opt/homebrew/bin; do
    [ -d "$_dir" ] || continue
    for _bin in "$_dir"/*; do
        [ -x "$_bin" ] || continue
        _name="$(basename -- "$_bin")"
        [ "$_name" = "yakos" ] && continue
        ln -sf "$_bin" "$NOYAKOS_PATH/$_name" 2>/dev/null || true
    done
done

# ---- synthetic secret literals (GitHub push-protection safety) -------------
#
# GitHub's push protection scans every line of every commit in a push for
# known secret-detector patterns (AWS access keys, Stripe live keys, Slack
# tokens, GitHub tokens, Google API keys, PEM private key blocks, ...) and
# rejects the push if any commit — including old ones a rewritten history
# still contains — matches. This repo's hook fixtures deliberately
# construct fake-but-pattern-matching secrets so secret-scan.sh's regexes
# have something to fire on; that is exactly the shape push protection is
# built to catch, regardless of intent.
#
# Each fixture below carries a placeholder token (`__SECRET_<NAME>__`)
# instead of the literal value. The functions here assemble the real value
# at RUNTIME from two or more separately-quoted string literals, chosen so
# the detector-matching substring never appears contiguous in this file's
# own text either — each secret is split across a quote boundary below, so
# the full value only ever exists assembled in process memory, after this
# script is already running, never as a single token on disk.
# case_check substitutes each placeholder into the fixture payload after
# the __CLAUDE_PROJECT_DIR__ substitution, so no file in this tree and no
# line in this script contains a contiguous detector-matching string.
_secret_aws() { printf '%s%s' 'AKIA' '0123456789ABCDEF'; }
_secret_stripe() { printf '%s%s' 'sk_live' '_0123456789abcdefghijklmn'; }
_secret_slack() { printf '%s%s' 'xox' 'b-0123456789-abcdefghijklmnop'; }
_secret_anthropic() { printf '%s%s%s' 'sk-ant' '-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRST' 'UVWXYZ0123456789_-abcdefghijklmnopqrstuvwxyzABC'; }
_secret_google() { printf '%s%s' 'AIz' 'aabcdefghijklmnopqrstuvwxyzABCDEFGHI'; }
_secret_ghp() { printf '%s%s' 'ghp' '_0123456789abcdefghij0123456789abcdef'; }
_secret_ghpat() { printf '%s%s' 'github_pat' '_0123456789abcdefghijkl_0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyzABCD'; }
# The PEM value is assembled from header/body/footer pieces too, split
# mid-word, even though the body is obviously synthetic base64 and a bare
# BEGIN/PRIVATE-KEY header alone is unlikely to be what push protection's
# detector keys on — belt and suspenders. `\n` here is the literal
# two-character JSON escape (backslash + n), not a real newline — it has
# to stay that way to remain valid JSON once substituted into a fixture.
_secret_pem() {
    printf '%s%s%s%s' \
        '-----BEGIN RSA PRIVATE' ' KEY-----\nMIIBOgIBAAJBAKj34GkxFhD91RIkY7yOaeQz3QUcz2eGZLwqIkTLdw9ZuNJ5R2Hd\n-----END RSA PRIVATE' ' KEY' '-----\n'
}

case_check() {
    # Args: hook-script-relpath, fixture-relpath, expected-rc, expected-log-name, [setup-fn], [extra-env-assignment], [cpd-suffix], [home-fn]
    #
    # extra-env-assignment, if given, is one or more space-separated
    # "NAME=value" assignments exported into the hook's environment for
    # this one invocation (used by the missing-jq fail-closed cases to
    # override PATH, and to combine that with an escape-hatch env var —
    # e.g. "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1").
    #
    # cpd-suffix, if given, is appended to the temp dir before it's passed
    # to the hook as CLAUDE_PROJECT_DIR — used by the trailing-slash
    # regression case (security review R2-1, round 3: CLAUDE_PROJECT_DIR=
    # "$tmp/" must behave identically to "$tmp"). The __CLAUDE_PROJECT_DIR__
    # substitution in the fixture body always uses the bare (no-suffix)
    # temp dir, since that's the real filesystem path setup_fn created
    # directories under.
    #
    # home-fn, if given, is a function name called as "$home_fn"
    # "$tmp/home" — it should populate $tmp/home (e.g. $tmp/home/.yakos-state/
    # settings.json) and the resulting $tmp/home is exported as HOME for
    # this one invocation (bash side; the Go side gets its own sandboxed
    # HOME under $tmp2/home via parity_check below — never bash's $tmp/home,
    # since each side needs its own on-disk sandbox). Added in S-6 A-2a
    # round 3 (re-review finding 1) because cycle-counter is the first hook
    # under case_check test whose behavior genuinely depends on
    # $HOME/.yakos-state/settings.json content — every other $HOME-reading
    # hook's case_check coverage deliberately avoids exercising $HOME (see
    # setup_with_team_created_history below for the prior documented
    # rationale); sandboxing HOME per-case here is what makes it safe to
    # finally cover that path without depending on the real machine's
    # ~/.yakos-state contents.
    local hook="$1" fixture="$2" expected_rc="$3" log_name="$4" setup_fn="${5:-}" extra_env="${6:-}" cpd_suffix="${7:-}" home_fn="${8:-}" accept="${9:-}"

    # YAKOS_PARITY_ONLY=<hook-name> restricts the run to one hook (fast
    # iteration while closing a hook's gaps).
    if [ -n "${YAKOS_PARITY_ONLY:-}" ] && [ "$(basename "$hook" .sh)" != "$YAKOS_PARITY_ONLY" ]; then
        return 0
    fi

    # YAKOS_PARITY_FIXTURE=<substring> restricts the run to tuples whose
    # fixture file name contains it (mutation testing: run just the fixtures
    # that guard the check under test).
    if [ -n "${YAKOS_PARITY_FIXTURE:-}" ]; then
        case "$fixture" in
            *"$YAKOS_PARITY_FIXTURE"*) ;;
            *) return 0 ;;
        esac
    fi

    local tmp
    tmp="$(mktemp -d -t yakos-hookfix-XXXXXX)"
    mkdir -p "$tmp/.claude" "$tmp/work/current/logs"

    if [ -n "$setup_fn" ]; then
        "$setup_fn" "$tmp"
    fi

    # NOTE: extra_env is deliberately NOT mutated here (unlike
    # run-hook-fixtures.sh's simpler bash-only case_check) — the unmutated
    # value is forwarded to parity_check below, which builds its own
    # HOME=$tmp2/home for the Go side. A local-only run_env carries the
    # bash-side HOME= assignment instead.
    local run_env="$extra_env"
    if [ -n "$home_fn" ]; then
        mkdir -p "$tmp/home"
        "$home_fn" "$tmp/home"
        if [ -n "$run_env" ]; then
            run_env="$run_env HOME=$tmp/home"
        else
            run_env="HOME=$tmp/home"
        fi
    fi

    run_env="${run_env//__TMP__/$tmp}"

    local payload cpd
    payload="$(sed "s|__CLAUDE_PROJECT_DIR__|$tmp|g" "$FIXT/$fixture")"
    cpd="${tmp}${cpd_suffix}"

    # Assemble any synthetic-secret placeholders (see the _secret_* functions
    # above) into their real values — a no-op for fixtures that carry none.
    # Plain substring replacement (no glob metacharacters in the search
    # tokens), so this is safe even though some assembled values themselves
    # contain regex-special characters.
    payload="${payload//__SECRET_AWS__/$(_secret_aws)}"
    payload="${payload//__SECRET_STRIPE__/$(_secret_stripe)}"
    payload="${payload//__SECRET_SLACK__/$(_secret_slack)}"
    payload="${payload//__SECRET_ANTHROPIC__/$(_secret_anthropic)}"
    payload="${payload//__SECRET_GOOGLE__/$(_secret_google)}"
    payload="${payload//__SECRET_GHP__/$(_secret_ghp)}"
    payload="${payload//__SECRET_GHPAT__/$(_secret_ghpat)}"
    payload="${payload//__SECRET_PEM__/$(_secret_pem)}"

    # Pin work-directory resolution to the temp dir so hooks write here,
    # not into ~/agent-control/.
    local actual_rc=0
    local stdout_capture
    local bash_stderr_file
    bash_stderr_file="$(mktemp)"
    if [ -n "$run_env" ]; then
        # shellcheck disable=SC2086  # intentional: run_env may carry
        # multiple space-separated NAME=value assignments.
        stdout_capture="$(printf '%s' "$payload" | env $run_env YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$cpd" "$HOOK_BASH" "$HOOKS/$hook" 2>"$bash_stderr_file")" || actual_rc=$?
    else
        stdout_capture="$(printf '%s' "$payload" | YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$cpd" "$HOOK_BASH" "$HOOKS/$hook" 2>"$bash_stderr_file")" || actual_rc=$?
    fi
    local bash_stderr
    bash_stderr="$(cat "$bash_stderr_file")"
    rm -f "$bash_stderr_file"

    # Verify rc
    local rc_ok=0
    [ "$actual_rc" = "$expected_rc" ] && rc_ok=1

    # Verify log was written (when expected)
    local log_ok=1
    if [ -n "$log_name" ]; then
        if [ ! -f "$tmp/work/current/logs/${log_name}.ndjson" ]; then
            log_ok=0
        else
            # Confirm the last line is parseable JSON
            tail -n 1 "$tmp/work/current/logs/${log_name}.ndjson" | jq empty 2>/dev/null || log_ok=0
        fi
    fi

    if [ "$rc_ok" = "1" ] && [ "$log_ok" = "1" ]; then
        printf 'PASS  %-30s | %-30s | rc=%s log=%s\n' "$(basename "$hook")" "$(basename "$fixture")" "$actual_rc" "$log_name"
        pass=$((pass + 1))
    else
        printf 'FAIL  %-30s | %-30s | rc=%s (want %s) log=%s rc_ok=%s log_ok=%s\n' \
            "$(basename "$hook")" "$(basename "$fixture")" "$actual_rc" "$expected_rc" "$log_name" "$rc_ok" "$log_ok"
        fail=$((fail + 1))
        fail_log="${fail_log}---- $hook on $fixture ----\nstdout: $stdout_capture\nlog file: $tmp/work/current/logs/${log_name}.ndjson\n$(ls -la "$tmp/work/current/logs/" 2>&1)\n"
    fi

    # ---- Go side (S-6 A-1) --------------------------------------------------
    # Re-run the identical (fixture, setup_fn, extra_env, cpd_suffix) tuple
    # against `yakos hook run <name>` in a fresh sandbox, and compare against
    # the bash run captured above.
    parity_check "$hook" "$fixture" "$actual_rc" "$log_name" "$setup_fn" "$extra_env" "$cpd_suffix" \
        "$tmp/work/current/logs" "$stdout_capture" "$bash_stderr" "$tmp" "$home_fn" "$accept"

    rm -rf "$tmp"
}

# normalize_stderr strips the two axes of expected-and-harmless divergence
# from a captured stderr blob before comparison:
#
#   1. A leading "[<ts>] " prefix — bash's ct_log()-style hooks
#      (compat.sh's ct_log, and the per-hook copies cycle-counter.sh /
#      plan-quality-gate.sh inline when compat.sh isn't sourced) add this;
#      Go's hookio/hooklog output never does. Stripped per-line so it
#      matches regardless of where in a multi-line stderr blob it occurs.
#   2. Each side's own mktemp -d sandbox path — every case_check /
#      parity_check pair gets a FRESH temp dir on each side (never the
#      same path even for bash vs. Go of the very same fixture), so any
#      message that echoes an absolute path under the sandbox (a file
#      path from the fixture's tool_input, a log-dir path in an error
#      string) would show a spurious divergence without this.
#
# Args: raw-stderr, bash-sandbox-dir, go-sandbox-dir
normalize_stderr() {
    local text="$1" bash_dir="$2" go_dir="$3"
    printf '%s' "$text" \
        | sed -E 's/^\[[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z\] //' \
        | sed "s|/private$bash_dir|__SANDBOX__|g; s|/private$go_dir|__SANDBOX__|g; s|$bash_dir|__SANDBOX__|g; s|$go_dir|__SANDBOX__|g"
}

# parity_check runs the Go side of one case and records the comparison.
# Args: hook-script-relpath, fixture-relpath, bash-rc, log-name, setup-fn,
#       extra-env, cpd-suffix, bash-log-dir, bash-stdout, bash-stderr,
#       bash-sandbox-dir, home-fn
parity_check() {
    local hook="$1" fixture="$2" bash_rc="$3" log_name="$4" setup_fn="$5" extra_env="$6" cpd_suffix="$7" bash_log_dir="$8" bash_stdout="$9"
    local bash_stderr="${10}" bash_tmp="${11}" home_fn="${12:-}" accept="${13:-}"
    local hookname
    hookname="$(basename "$hook" .sh)"

    local tmp2
    tmp2="$(mktemp -d -t yakos-hookparity-go-XXXXXX)"
    mkdir -p "$tmp2/.claude" "$tmp2/work/current/logs"
    if [ -n "$setup_fn" ]; then
        "$setup_fn" "$tmp2"
    fi

    # Go side gets its OWN sandboxed HOME under $tmp2/home (never bash's
    # $tmp/home) — see case_check's home-fn doc comment above.
    local go_run_env="$extra_env"
    if [ -n "$home_fn" ]; then
        mkdir -p "$tmp2/home"
        "$home_fn" "$tmp2/home"
        if [ -n "$go_run_env" ]; then
            go_run_env="$go_run_env HOME=$tmp2/home"
        else
            go_run_env="HOME=$tmp2/home"
        fi
    fi

    go_run_env="${go_run_env//__TMP__/$tmp2}"

    local payload cpd
    payload="$(sed "s|__CLAUDE_PROJECT_DIR__|$tmp2|g" "$FIXT/$fixture")"
    cpd="${tmp2}${cpd_suffix}"
    payload="${payload//__SECRET_AWS__/$(_secret_aws)}"
    payload="${payload//__SECRET_STRIPE__/$(_secret_stripe)}"
    payload="${payload//__SECRET_SLACK__/$(_secret_slack)}"
    payload="${payload//__SECRET_ANTHROPIC__/$(_secret_anthropic)}"
    payload="${payload//__SECRET_GOOGLE__/$(_secret_google)}"
    payload="${payload//__SECRET_GHP__/$(_secret_ghp)}"
    payload="${payload//__SECRET_GHPAT__/$(_secret_ghpat)}"
    payload="${payload//__SECRET_PEM__/$(_secret_pem)}"

    local go_rc=0 go_stdout go_stderr_file
    go_stderr_file="$(mktemp)"
    # YAKOS_IMPL=go is required: with it unset, the Go binary's own impl
    # gate (main.go selectImpl) defaults to passthrough whenever a bash
    # cli/yakos exists on disk (true in this repo) — meaning without this,
    # every invocation here would silently shell out to bash yakos instead
    # of exercising the Go-native `hook run` path at all (the same trap
    # documented for `yakos serve` daemon ops).
    if [ -n "$go_run_env" ]; then
        # shellcheck disable=SC2086  # see the bash-side comment above.
        go_stdout="$(printf '%s' "$payload" | env $go_run_env YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$tmp2/work" CLAUDE_PROJECT_DIR="$cpd" "$GO_BINARY" hook run "$hookname" 2>"$go_stderr_file")" || go_rc=$?
    else
        go_stdout="$(printf '%s' "$payload" | YAKOS_IMPL=go YAKOS_HOOKS=go YAKOS_WORK_DIR="$tmp2/work" CLAUDE_PROJECT_DIR="$cpd" "$GO_BINARY" hook run "$hookname" 2>"$go_stderr_file")" || go_rc=$?
    fi
    local go_stderr
    go_stderr="$(cat "$go_stderr_file")"
    rm -f "$go_stderr_file"

    local divergence="-"
    [ "$go_rc" != "$bash_rc" ] && divergence="exit"

    if [ "$divergence" = "-" ] && [ -n "$log_name" ]; then
        local bash_log="$bash_log_dir/${log_name}.ndjson" go_log="$tmp2/work/current/logs/${log_name}.ndjson"
        local bash_has=0 go_has=0
        [ -f "$bash_log" ] && bash_has=1
        [ -f "$go_log" ] && go_has=1
        if [ "$bash_has" != "$go_has" ]; then
            if [ "$go_has" = "1" ]; then divergence="log-present-go-only"; else divergence="log-missing-go"; fi
        elif [ "$bash_has" = "1" ]; then
            local norm_bash norm_go
            # Each side's own mktemp -d sandbox path (and its macOS
            # /private-prefixed realpath form) is masked, exactly as
            # normalize_stderr does for stderr: a record that embeds an
            # absolute path (path-allowlist's project_root / resolved /
            # root-equal file_path) can never match across two sandboxes.
            # ts is masked (never equal across two runs), and so is
            # elapsed_seconds (budget-guard: wall-clock since a state file
            # written at case setup, so the two sides' runs can straddle a
            # second boundary; Go's elapsed arithmetic is covered by its own
            # unit tests instead). plan-quality-score's debounce record carries
            # age=<n>s / age_s for the same wall-clock reason (K-99).
            norm_bash="$(tail -n 1 "$bash_log" | sed "s|/private$bash_tmp|__SANDBOX__|g; s|$bash_tmp|__SANDBOX__|g; s|age=[0-9-]*s|age=Ns|g" | jq -cS 'del(.ts, .elapsed_seconds, .age_s)' 2>/dev/null || echo "__unparseable_bash__")"
            norm_go="$(tail -n 1 "$go_log" | sed "s|/private$tmp2|__SANDBOX__|g; s|$tmp2|__SANDBOX__|g; s|age=[0-9-]*s|age=Ns|g" | jq -cS 'del(.ts, .elapsed_seconds, .age_s)' 2>/dev/null || echo "__unparseable_go__")"
            [ "$norm_bash" != "$norm_go" ] && divergence="log-schema"
        fi
    fi

    # messages.ndjson — the mailbox-mirror audit trail — was never
    # compared at all before this (S-6 A-2a round 2 review finding 5):
    # only exit code, the hooklog record, stdout, and stderr were. This
    # is a no-op ("no file, no file") for every hook other than
    # mailbox-mirror, so it's safe to run unconditionally for every case.
    if [ "$divergence" = "-" ]; then
        local bash_msgs
        bash_msgs="$(dirname -- "$bash_log_dir")/messages.ndjson"
        local go_msgs="$tmp2/work/current/messages.ndjson"
        local bash_msgs_has=0 go_msgs_has=0
        [ -f "$bash_msgs" ] && bash_msgs_has=1
        [ -f "$go_msgs" ] && go_msgs_has=1
        if [ "$bash_msgs_has" != "$go_msgs_has" ]; then
            if [ "$go_msgs_has" = "1" ]; then divergence="messages-present-go-only"; else divergence="messages-missing-go"; fi
        elif [ "$bash_msgs_has" = "1" ]; then
            local norm_bash_msgs norm_go_msgs
            norm_bash_msgs="$(tail -n 1 "$bash_msgs" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_bash_msgs__")"
            norm_go_msgs="$(tail -n 1 "$go_msgs" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_go_msgs__")"
            [ "$norm_bash_msgs" != "$norm_go_msgs" ] && divergence="messages-ndjson"
        fi
    fi

    # supervisor-buffer.ndjson (supervisor-stream's rolling event window): the
    # only place the payload-derived agent and session_id land in a schema the
    # two sides share. No-op ("no file, no file") for every other hook.
    if [ "$divergence" = "-" ]; then
        local bash_sbuf go_sbuf
        bash_sbuf="$(dirname -- "$bash_log_dir")/supervisor-buffer.ndjson"
        go_sbuf="$tmp2/work/current/supervisor-buffer.ndjson"
        if [ -f "$bash_sbuf" ] || [ -f "$go_sbuf" ]; then
            if [ -f "$bash_sbuf" ] && [ -f "$go_sbuf" ]; then
                local norm_bash_sbuf norm_go_sbuf
                norm_bash_sbuf="$(tail -n 1 "$bash_sbuf" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_bash_sbuf__")"
                norm_go_sbuf="$(tail -n 1 "$go_sbuf" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_go_sbuf__")"
                [ "$norm_bash_sbuf" != "$norm_go_sbuf" ] && divergence="supervisor-buffer"
            elif [ -f "$go_sbuf" ]; then
                divergence="supervisor-buffer-present-go-only"
            else
                divergence="supervisor-buffer-missing-go"
            fi
        fi
    fi

    if [ "$divergence" = "-" ] && [ "$bash_stdout" != "$go_stdout" ]; then
        divergence="stdout"
    fi

    if [ "$divergence" = "-" ]; then
        local norm_bash_stderr norm_go_stderr
        norm_bash_stderr="$(normalize_stderr "$bash_stderr" "$bash_tmp" "$tmp2")"
        norm_go_stderr="$(normalize_stderr "$go_stderr" "$bash_tmp" "$tmp2")"
        [ "$norm_bash_stderr" != "$norm_go_stderr" ] && divergence="stderr"
    fi

    # Accepted (documented, intentional) divergences. A tuple carries an
    # `accept` annotation of the form "<go-rc>:<reason>" when bash and Go are
    # KNOWN to differ on purpose — e.g. Go never depends on jq, or Go is
    # stricter than a bash weakness. The annotation pins Go's exit code, so an
    # accepted case still guards Go's decision; it does not excuse it. If the
    # two sides start agreeing, the stale annotation is itself flagged.
    local raw_divergence="$divergence" accepted_reason=""
    if [ -n "$accept" ]; then
        local acc_rc="${accept%%:*}"
        accepted_reason="${accept#*:}"
        if [ "$divergence" = "-" ]; then
            divergence="accept-stale"
        elif [ "$go_rc" = "$acc_rc" ]; then
            divergence="accepted"
        fi
    fi

    printf '{"hook":%s,"fixture":%s,"bash_rc":%s,"go_rc":%s,"divergence":%s,"raw_divergence":%s,"accepted_reason":%s}\n' \
        "$(json_str "$hookname")" "$(json_str "$(basename "$fixture")")" "$bash_rc" "$go_rc" \
        "$(json_str "$divergence")" "$(json_str "$raw_divergence")" "$(json_str "$accepted_reason")" >> "$REPORT_FILE"

    if [ "$divergence" = "-" ]; then
        printf 'PARITY  %-24s | %-42s | rc=%s\n' "$hookname" "$(basename "$fixture")" "$go_rc"
    elif [ "$divergence" = "accepted" ]; then
        printf 'ACCEPT  %-24s | %-42s | %s (bash_rc=%s go_rc=%s) -- %s\n' "$hookname" "$(basename "$fixture")" "$raw_divergence" "$bash_rc" "$go_rc" "$accepted_reason"
    else
        printf 'DIVERGE %-24s | %-42s | %s (bash_rc=%s go_rc=%s)\n' "$hookname" "$(basename "$fixture")" "$divergence" "$bash_rc" "$go_rc"
    fi

    # YAKOS_PARITY_VERBOSE=1: side-by-side evidence for every raw divergence.
    if [ "${YAKOS_PARITY_VERBOSE:-0}" = "1" ] && [ "$raw_divergence" != "-" ]; then
        {
            echo "    --- bash: rc=$bash_rc"
            echo "    stdout: $bash_stdout"
            echo "    stderr: $bash_stderr"
            [ -n "$log_name" ] && [ -f "$bash_log_dir/${log_name}.ndjson" ] && echo "    log:    $(tail -n 1 "$bash_log_dir/${log_name}.ndjson")"
            echo "    --- go:   rc=$go_rc"
            echo "    stdout: $go_stdout"
            echo "    stderr: $go_stderr"
            [ -n "$log_name" ] && [ -f "$tmp2/work/current/logs/${log_name}.ndjson" ] && echo "    log:    $(tail -n 1 "$tmp2/work/current/logs/${log_name}.ndjson")"
        } | sed "s|$bash_tmp|<bash-sandbox>|g; s|$tmp2|<go-sandbox>|g"
    fi

    rm -rf "$tmp2"
}

# json_str prints its argument as a JSON string literal (jq -Rn is the
# simplest portable way to get correct escaping without assuming any
# particular jq version's string-building flags).
json_str() {
    printf '%s' "$1" | jq -Rs '.' 2>/dev/null | tr -d '\n' || printf '"%s"' "$1"
}

# ---- setup helpers ----------------------------------------------------------

setup_allowlist_strict() {
    # go-api can edit api/** and internal/**, but not web/**.
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "lead": {"allow": ["**"], "deny": [".env", ".env.*"]},
  "go-api": {
    "allow": ["api/**", "internal/**"],
    "deny": ["api/migrations/**"]
  }
}
EOF
}

setup_no_allowlist() {
    : # no path-allowlist.json
}

setup_allowlist_strict_namespaced() {
    # Same policy as setup_allowlist_strict but keyed on bare "go-api".
    # Used to verify that a namespaced agent ("yakos:go-api") hits the
    # same policy after hi_sender_role strips the prefix.
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "lead": {"allow": ["**"], "deny": [".env", ".env.*"]},
  "go-api": {
    "allow": ["api/**", "internal/**"],
    "deny": ["api/migrations/**"]
  }
}
EOF
}

setup_with_bypass() {
    # Bypass file with one active entry covering path-allowlist on web/index.js
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:web-test-fixture

**Hook:** path-allowlist
**Reason:** test fixture for bypass mechanism
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** web/index.js
**Follow-up:** none — fixture only
EOF
}

setup_allowlist_bypass_narrow_scope() {
    # A hook-bypass.md entry for path-allowlist, but scoped to one
    # unrelated file — security review R2-3, round 3: before the fix, an
    # empty probe scope in the degraded-input check matched ANY entry for
    # the hook, so this narrow, unrelated entry would have silently
    # disabled path-allowlist's fail-closed behavior for every future
    # degraded-input event. Must NOT cover a degraded-input event.
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:narrow-scope-fixture

**Hook:** path-allowlist
**Reason:** test fixture — narrow scope, unrelated to degraded input
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** api/legacy/vendor.pem
**Follow-up:** none — fixture only
EOF
}

setup_allowlist_bypass_degraded_input_scope() {
    # The explicit sentinel scope (security review R2-3, round 3) — this
    # one DOES cover a degraded-input event, on purpose.
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:degraded-input-fixture

**Hook:** path-allowlist
**Reason:** test fixture — explicit degraded-input opt-in
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** degraded-input
**Follow-up:** none — fixture only
EOF
}

setup_allowlist_bypass_substring_collision_scope() {
    # A Scope that CONTAINS the "degraded-input" sentinel as a substring
    # (an ordinary filename) but does not equal it — security review
    # R3-2, round 4: before the exact-match fix, this satisfied the
    # degraded-input check via ho_check_bypass's substring semantics,
    # even though the operator almost certainly meant an unrelated file
    # bypass, not "yes, disable fail-closed behavior on broken input."
    # Must NOT cover a degraded-input event.
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:substring-collision-fixture

**Hook:** path-allowlist
**Reason:** test fixture — Scope contains the sentinel as a substring
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** api/degraded-input.go
**Follow-up:** none — fixture only
EOF
}

# ---- K-99: hook-bypass Scope is exact-or-glob (was substring) -------------
# Each setup writes the strict go-api allowlist (web/** is not allowed, so
# web/index.js blocks) plus one active path-allowlist bypass entry with the
# given Scope. Probe scope = "web/index.js".
_bp_scope_setup() {
    setup_allowlist_strict "$1"
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:k99-scope-fixture

**Hook:** path-allowlist
**Reason:** K-99 scope-matching fixture
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** $2
**Follow-up:** none — fixture only
EOF
}
setup_bp_scope_exact()      { _bp_scope_setup "$1" 'web/index.js'; }
setup_bp_scope_longer()     { _bp_scope_setup "$1" 'web/index.js-rotation'; }
setup_bp_scope_freetext()   { _bp_scope_setup "$1" 'path=web/index.js reason=intentional'; }
setup_bp_scope_glob()       { _bp_scope_setup "$1" 'web/**'; }
setup_bp_scope_bare_prefix() { _bp_scope_setup "$1" 'web'; }
setup_bp_scope_empty()      { _bp_scope_setup "$1" ''; }
setup_bp_scope_case()       { _bp_scope_setup "$1" 'Web/Index.js'; }

setup_with_decisions_stale() {
    mkdir -p "$1/work/current"
    # Touch decisions.md as 3h old
    touch -t "$(date -u -v-3H +%Y%m%d%H%M 2>/dev/null || date -u -d '-3 hours' +%Y%m%d%H%M)" "$1/work/current/decisions.md" 2>/dev/null
    : > "$1/work/current/decisions.md"
}

setup_with_team_created_history() {
    # Exercises session-end-check's team_name lookup and
    # scratchpad_size_bytes fields (S-6 A-2a round 2 review finding 1):
    # a .session-started-history.ndjson team_created record matching the
    # fixture's session_id, plus real scratchpad content so
    # scratchpad_size_bytes is nonzero on both sides. Deliberately does
    # NOT attempt to exercise the team-inbox snapshot feature here — that
    # reads $HOME/.claude/teams/<team>/inboxes, which this harness never
    # sandboxes (see the harness's own top-of-file comment on
    # NOJQ_PATH/NORESOLVE_PATH for why other cross-cutting env is
    # sandboxed the way it is); a fixture that depended on the real
    # $HOME's team-inbox state would be non-deterministic across
    # machines/CI runners. The inbox-snapshot code path is covered
    # instead by cli-go/internal/hooks/sessionendcheck's own
    # t.TempDir()-based unit tests via the Hook.TeamsDir override.
    mkdir -p "$1/work/current"
    printf '%s\n' \
        '{"ts":"2026-01-15T09:00:00Z","event":"team_created","team_name":"fixture-team","session_id":"fixture-sessionend-with-team-0001"}' \
        > "$1/work/current/.session-started-history.ndjson"
    head -c 16384 /dev/zero > "$1/work/current/scratchpad-filler.bin" 2>/dev/null \
        || dd if=/dev/zero of="$1/work/current/scratchpad-filler.bin" bs=1024 count=16 2>/dev/null
}

setup_allowlist_deny_pem() {
    # go-api may write anywhere under api/, EXCEPT *.pem (any case — H5b).
    mkdir -p "$1/api/creds"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"],
    "deny": ["*.pem"]
  }
}
EOF
}

setup_allowlist_notebook() {
    # go-api may only touch api/**; used for the NotebookEdit coverage cases.
    mkdir -p "$1/api" "$1/web"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"]
  }
}
EOF
}

setup_symlink_escape() {
    # go-api may write anywhere under api/, but api/escape-link is a
    # symlink to a file outside the project root entirely (M7 regression:
    # a lexically-fine path that resolves outside the tree via symlink).
    mkdir -p "$1/api"
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {
    "allow": ["api/**"]
  }
}
EOF
    ln -sf /etc/hosts "$1/api/escape-link"
}

setup_budget_low_cap() {
    # max_tool_calls: 1, with state already at count=1 from a prior call —
    # the next PreToolUse call pushes the count to 2, over the cap.
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: true
  max_tool_calls: 1
EOF
    mkdir -p "$1/work/current"
    cat > "$1/work/current/.budget-state.json" <<EOF
{"session_id":"fixture-generic-tool-0001","started_at":$(date +%s),"tool_call_count":1,"last_tool":"Read","last_tool_run_count":1}
EOF
}

setup_supervisor_findings_critical() {
    # A supervisor-findings.ndjson with one CRITICAL finding — the state
    # that makes supervisor-gate.sh reach its unguarded `jq -r` calls
    # (security review R2-2, round 3). block_on_critical isn't relevant
    # to this fixture: with jq missing, the escape hatch fires before the
    # hook ever gets far enough to read that config value.
    mkdir -p "$1/work/current"
    echo '{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture","recommended_action":"halt"}' \
        > "$1/work/current/supervisor-findings.ndjson"
}

setup_budget_headroom() {
    # max_tool_calls: 500 — nowhere near the cap, first call of the session.
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: true
  max_tool_calls: 500
EOF
}

setup_budget_low_cap_disabled() {
    # Same over-cap state as setup_budget_low_cap, plus YAKOS_BUDGET_DISABLE
    # is passed via extra_env at the call site — this setup only needs the
    # over-cap state so the *_DISABLE reorder (security review N2) is what
    # makes the difference, not an absent cap.
    setup_budget_low_cap "$1"
}

setup_allowlist_deny_only_goapi() {
    # go-api has a deny-only policy (no "allow" key at all) — used for the
    # N1 absolute-out-of-root-path regression under a policy shape that
    # never even reaches the allow-matching branch.
    cat > "$1/.claude/path-allowlist.json" <<'EOF'
{
  "go-api": {"deny": ["api/migrations/**"]}
}
EOF
}

setup_allowlist_corrupt_truncated() {
    # A policy file that exists but is truncated mid-write (security review
    # N4.1) — must BLOCK under HOOK_FAIL_CLOSED, not silently disable
    # enforcement the way an absent file does.
    printf '{"go-api": {"allow"' > "$1/.claude/path-allowlist.json"
}

# ---- cycle-counter $HOME/.yakos-state/settings.json home-fn helpers --------
#
# S-6 A-2a round 3 (re-review finding 1): a malformed or wrong-shape
# settings.json crashed bash's cycle-counter.sh (set -eu + a bare jq
# assignment) before the counter was ever incremented, while the Go port's
# loadSettings already degraded gracefully — while at 100% for its own 1
# fixture, cycle-counter's bash-vs-Go parity was untested for exactly the
# case where the two sides used to diverge most. Fixed with a `|| n=""` /
# `|| val="true"` guard on both jq reads in lib/hooks/cycle-counter.sh.
# These three home-fns (called with $tmp/home or $tmp2/home — each side's
# own sandboxed HOME, see case_check's/parity_check's home-fn doc comments
# above) regression-guard the fix on BOTH sides identically.
setup_cycle_counter_malformed_settings() {
    # Unparseable JSON (truncated mid-object) — jq compile/parse error.
    mkdir -p "$1/.yakos-state"
    printf '%s' '{"retro": {cycle_length: 3' > "$1/.yakos-state/settings.json"
}

setup_cycle_counter_wrong_shape_settings() {
    # Valid JSON, wrong top-level shape (array, not object) — jq runtime
    # error ("Cannot index array with string") on `.retro.cycle_length`.
    mkdir -p "$1/.yakos-state"
    printf '%s\n' '[1, 2, 3]' > "$1/.yakos-state/settings.json"
}

setup_cycle_counter_missing_settings() {
    # No ~/.yakos-state directory at all — the sandboxed-HOME control
    # case for the default/missing-file path, deterministic across
    # machines/CI runners on both sides.
    :
}

# K-106: cycle_length guard. Each home-fn writes one settings.json variant;
# bash and Go must both fall back to the default (10) with one WARN naming
# the value (none for null/valid), counter written, identical log record.
_cc_write_settings() { mkdir -p "$1/.yakos-state"; printf '%s\n' "$2" > "$1/.yakos-state/settings.json"; }
setup_cycle_counter_len_zero() { _cc_write_settings "$1" '{"retro":{"cycle_length":0}}'; }
setup_cycle_counter_len_negative() { _cc_write_settings "$1" '{"retro":{"cycle_length":-1}}'; }
setup_cycle_counter_len_abc() { _cc_write_settings "$1" '{"retro":{"cycle_length":"abc"}}'; }
setup_cycle_counter_len_emptystr() { _cc_write_settings "$1" '{"retro":{"cycle_length":""}}'; }
setup_cycle_counter_len_null() { _cc_write_settings "$1" '{"retro":{"cycle_length":null}}'; }
setup_cycle_counter_len_huge() { _cc_write_settings "$1" '{"retro":{"cycle_length":1e9}}'; }
setup_cycle_counter_len_valid10() { _cc_write_settings "$1" '{"retro":{"cycle_length":10}}'; }

# ---- K-87 A-2b path-allowlist setups ----------------------------------------

_pa_write_policy() { printf '%s' "$2" > "$1/.claude/path-allowlist.json"; }

setup_allowlist_allow_empty() { _pa_write_policy "$1" '{"go-api":{"allow":[]}}'; }
setup_allowlist_allow_string() { _pa_write_policy "$1" '{"go-api":{"allow":"api/**"}}'; }
setup_allowlist_allow_false() { _pa_write_policy "$1" '{"go-api":{"allow":false}}'; }
setup_allowlist_allow_zero() { _pa_write_policy "$1" '{"go-api":{"allow":0}}'; }
setup_allowlist_allow_object() { _pa_write_policy "$1" '{"go-api":{"allow":{"a":"api/**"}}}'; }
setup_allowlist_allow_null() { _pa_write_policy "$1" '{"go-api":{"allow":null}}'; }
setup_allowlist_deny_string() { _pa_write_policy "$1" '{"go-api":{"deny":"api/**","allow":["**"]}}'; }
setup_allowlist_policy_not_object() { _pa_write_policy "$1" '{"go-api":"oops"}'; }
setup_allowlist_policy_false() { _pa_write_policy "$1" '{"go-api":false}'; }
setup_allowlist_toplevel_array() { _pa_write_policy "$1" '[]'; }

setup_allowlist_allow_empty_bypassed() {
    # allow: [] (deny-all) plus a bypass entry scoped to the fixture's file —
    # the bypass must still work for the deny-all branch.
    setup_allowlist_allow_empty "$1"
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:allow-empty-fixture

**Hook:** path-allowlist
**Reason:** test fixture for the deny-all bypass branch
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** api/main.go
**Follow-up:** none — fixture only
EOF
}

setup_symlink_dir_escape() {
    # api/escape-dir is a symlink to a directory OUTSIDE the project root
    # (/etc — never written, the hook only reads the path), used by the
    # "..-after-a-symlink" case: the OS resolves "escape-dir/sub/../.." to
    # "/", not to "api".
    mkdir -p "$1/api"
    _pa_write_policy "$1" '{"go-api":{"allow":["api/**"]}}'
    ln -sf /etc "$1/api/escape-dir"
}

setup_symlink_inroot_dotdot() {
    # api/inl is a symlink to a directory INSIDE the project root; "inl/.."
    # therefore stays in-root under both lexical and physical resolution.
    mkdir -p "$1/api" "$1/internal/deep"
    _pa_write_policy "$1" '{"go-api":{"allow":["api/**","internal/**"]}}'
    ln -sf "$1/internal/deep" "$1/api/inl"
}

# ---- K-87 A-2b supervisor-gate setups ----------------------------------------

_sg_findings() { mkdir -p "$1/work/current"; printf '%s\n' "$2" > "$1/work/current/supervisor-findings.ndjson"; }
_SG_CRIT='{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture critical","recommended_action":"halt"}'
setup_sg_pass() { _sg_findings "$1" '{"ts":"2026-09-20T00:00:00Z","overall":"PASS","rationale":"all good","recommended_action":"continue"}'; }
setup_sg_warn() { _sg_findings "$1" '{"ts":"2026-09-20T00:00:00Z","overall":"WARN","rationale":"drifting","recommended_action":"review"}'; }
setup_sg_critical() { _sg_findings "$1" "$_SG_CRIT"; }
setup_sg_unknown() { _sg_findings "$1" '{"ts":"2026-09-20T00:00:00Z","overall":"MYSTERY","rationale":"x"}'; }
setup_sg_invalid_json() { _sg_findings "$1" '{"overall":"CRITICAL"'; }
setup_sg_nonobject() { _sg_findings "$1" '[]'; }
setup_sg_blank_last_line() { mkdir -p "$1/work/current"; printf '%s\n\n' "$_SG_CRIT" > "$1/work/current/supervisor-findings.ndjson"; }
setup_sg_critical_passive() {
    _sg_findings "$1" "$_SG_CRIT"
    printf 'supervisor:\n  block_on_critical: false\n' > "$1/.yakos.yml"
}
setup_sg_critical_passive_comment() {
    # A trailing comment defeats the script's awk/tr comparison: still blocks.
    _sg_findings "$1" "$_SG_CRIT"
    printf 'supervisor:\n  block_on_critical: false # passive\n' > "$1/.yakos.yml"
}
setup_sg_disabled() {
    _sg_findings "$1" "$_SG_CRIT"
    printf 'supervisor:\n  enabled: false\n' > "$1/.yakos.yml"
}
setup_sg_critical_bypassed() {
    _sg_findings "$1" "$_SG_CRIT"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:sg-fixture

**Hook:** supervisor
**Reason:** test fixture for the supervisor-gate bypass branch
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** finding=2026-09-20T00:00:00Z
**Follow-up:** none — fixture only
EOF
}

# ---- K-87 A-2b budget-guard setups -------------------------------------------

setup_budget_repeat_cap() {
    # max_repeat_same_tool: 2 with the previous call already the 2nd Read in
    # a row — this Read is the 3rd, over the cap.
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: true
  max_repeat_same_tool: 2
EOF
    mkdir -p "$1/work/current"
    cat > "$1/work/current/.budget-state.json" <<EOF
{"session_id":"fixture-generic-tool-0001","started_at":$(date +%s),"tool_call_count":2,"last_tool":"Read","last_tool_run_count":2}
EOF
}
setup_budget_low_cap_bypassed() {
    setup_budget_low_cap "$1"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:budget-fixture

**Hook:** budget
**Reason:** test fixture for the budget bypass branch
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** cap=max_tool_calls
**Follow-up:** none — fixture only
EOF
}
setup_budget_disabled_in_yml() {
    setup_budget_low_cap "$1"
    cat > "$1/.yakos.yml" <<'EOF'
budget:
  enabled: false
  max_tool_calls: 1
EOF
}
setup_budget_corrupt_state() {
    setup_budget_headroom "$1"
    mkdir -p "$1/work/current"
    printf 'not json at all' > "$1/work/current/.budget-state.json"
}

# ---- K-87 A-2b output-injection-scan setups ----------------------------------

setup_injection_scan_disabled_in_yml() {
    printf 'injection_scan:\n  enabled: false\n' > "$1/.yakos.yml"
}

# ---- K-87 A-2b: hooks that had no fixture coverage ---------------------------
#
# extra_env values may contain the literal token __TMP__, replaced with each
# side's own sandbox dir (bash: $tmp, Go: $tmp2) so a fixture can point an
# env var (YAKOS_COORD_ROOT, YAKOS_PLAN_QUALITY_LOG, ...) INTO its sandbox.

home_noop() { : ; }   # an empty sandboxed $HOME (keeps the real ~/.yakos-state out of the run)

# -- context-inject --
_ci_yml() { printf 'context_inject:\n  enabled: true\n%b' "$2" > "$1/.yakos.yml"; }
setup_ci_with_decisions() {
    _ci_yml "$1" ""
    mkdir -p "$1/work/current"
    printf '# decisions\n- d1: use postgres\n- d2: ship friday\n' > "$1/work/current/decisions.md"
}
setup_ci_empty() { _ci_yml "$1" ""; }
setup_ci_decisions_section_off() {
    _ci_yml "$1" "  inject_decisions: false\n"
    mkdir -p "$1/work/current"
    printf '# decisions\n- d1\n' > "$1/work/current/decisions.md"
}
setup_ci_with_critical() {
    _ci_yml "$1" ""
    mkdir -p "$1/work/current"
    printf '%s\n' '{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture critical","recommended_action":"halt"}' > "$1/work/current/supervisor-findings.ndjson"
}

# -- context-threshold: a claude transcript of N bytes in the sandboxed HOME --
_ct_transcript() {
    local proj enc
    proj="$(dirname "$1")"
    enc="${proj//\//-}"; enc="${enc//./-}"
    mkdir -p "$1/.claude/projects/$enc"
    head -c "$2" /dev/zero | tr '\0' 'x' > "$1/.claude/projects/$enc/transcript-fixture-generic-tool-0001.jsonl"
}
home_ct_notice() { _ct_transcript "$1" 640000; }   # ~80% of the 200k-token window: over the 75% notice line
home_ct_low() { _ct_transcript "$1" 80000; }       # ~10%

# -- supervisor-ack-gate --
_sag_findings() { mkdir -p "$1/work/current"; printf '%s\n' "$2" > "$1/work/current/supervisor-findings.ndjson"; }
_SAG_HALT='{"ts":"2026-09-20T00:00:00Z","overall":"CRITICAL","rationale":"fixture critical","recommended_action":"halt"}'
setup_sag_halt() { _sag_findings "$1" "$_SAG_HALT"; }
setup_sag_continue() { _sag_findings "$1" '{"ts":"2026-09-20T00:00:00Z","overall":"PASS","rationale":"fine","recommended_action":"continue"}'; }
setup_sag_gate_off() { _sag_findings "$1" "$_SAG_HALT"; printf 'supervise:\n  gate_on_escalation: false\n' > "$1/.yakos.yml"; }
setup_sag_malformed_then_halt() {
    mkdir -p "$1/work/current"
    printf '{"broken"\n%s\n' "$_SAG_HALT" > "$1/work/current/supervisor-findings.ndjson"
}
home_sag_acked() {
    mkdir -p "$1/.yakos-state"
    printf '{"project":"proj","finding_id":"f-2026-09-20T00-00-00Z-proj-1"}\n' > "$1/.yakos-state/supervisor-acks.ndjson"
}

# -- supervisor-stream --
setup_ss_passfilter() { printf 'supervisor:\n  score_every_n_calls: 1000\n' > "$1/.yakos.yml"; }
setup_ss_prefilter_off() { printf 'supervisor:\n  score_every_n_calls: 1000\n  pre_filter:\n    enabled: false\n' > "$1/.yakos.yml"; }
setup_ss_disabled() { printf 'supervisor:\n  enabled: false\n' > "$1/.yakos.yml"; }

# -- retro-dispatch --
setup_rd_marker() { mkdir -p "$1/work/current"; : > "$1/work/current/.retro-due"; }
home_rd_auto_off() { mkdir -p "$1/.yakos-state"; printf '{"retro":{"auto_dispatch":false}}' > "$1/.yakos-state/settings.json"; }
home_rd_inflight() {
    # $$ is the harness shell itself: alive for the whole hook run, so
    # `kill -0` sees a live prior dispatch on both the bash and Go sides.
    mkdir -p "$1/.yakos-state"
    printf '%s' "$$" > "$1/.yakos-state/retro-dispatch.pid"
}

# -- plan-outcome-capture --
setup_poc_scored() { printf '{"type":"plan_scored","plan_id":"p1","project":"%s"}\n' "$1" > "$1/pql.ndjson"; }
setup_poc_complete() {
    printf '{"type":"plan_scored","plan_id":"p1","project":"%s"}\n{"type":"plan_outcome","plan_id":"p1"}\n' "$1" > "$1/pql.ndjson"
}

# -- plan-quality-gate (PreToolUse marker path) --
setup_pqg_blocked() { mkdir -p "$1/work/current"; printf '{"plan_id":"p-1","reason":"score 40 below threshold 70"}' > "$1/work/current/.plan-blocked"; }
setup_pqg_blocked_but_disabled() { setup_pqg_blocked "$1"; printf 'plan_quality:\n  enabled: false\n' > "$1/.yakos.yml"; }

# -- peer-claim / peer-claim-confirm (coordination dir inside the sandbox) --
setup_pc_coord() { mkdir -p "$1/coord/proj/coord"; }
_pc_claim() {
    # _pc_claim <sandbox> <user> <host> <pid> <expires_at>
    setup_pc_coord "$1"
    printf '{"generated_at":"2026-09-20T00:00:00Z","claims":[{"path":"src/auth/login.ts","owners":[{"user":"%s","host":"%s","pid":%s,"agent":"frontend-pro","status":"confirmed","expires_at":"%s"}]}]}\n' \
        "$2" "$3" "$4" "$5" > "$1/coord/proj/coord/active-claims.json"
}
setup_pc_peer_claim() { _pc_claim "$1" alice dev01 1001 2099-01-01T00:00:00Z; }
setup_pc_own_claim() { _pc_claim "$1" bob dev01 2002 2099-01-01T00:00:00Z; }
setup_pc_expired_claim() { _pc_claim "$1" alice dev01 1001 2000-01-01T00:00:00Z; }
setup_pc_peer_claim_bypassed() {
    setup_pc_peer_claim "$1"
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:pc-fixture

**Hook:** peer-claim
**Reason:** test fixture for the peer-claim bypass branch
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** file=src/auth/login.ts peer=alice@dev01
**Follow-up:** none — fixture only
EOF
}

# -- task-complete-dispatch --
setup_tcd_bypass() {
    mkdir -p "$1/work/current"
    cat > "$1/work/current/hook-bypass.md" <<EOF
# Active hook bypasses

## Active entries

## bypass:tcd-fixture

**Hook:** task-complete-dispatch
**Reason:** test fixture for the bypass_active field
**Approved by:** TestSuite
**Created:** $(date -u +%Y-%m-%dT%H:%M:%SZ)
**Expires:** $(date -u -v+1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+1 hour' +%Y-%m-%dT%H:%M:%SZ)
**Scope:** backend
**Follow-up:** none — fixture only
EOF
}

# ---- K-87 A-2b round 2 setups ----------------------------------------------------
setup_allowlist_midstar_deny() { _pa_write_policy "$1" '{"go-api":{"allow":["api/**"],"deny":["api/*/secret.go"]}}'; }
setup_symlink_rootlink() {
    # api/rootlink points at the project root itself: "rootlink/.." is the
    # parent of the project, but lexically collapses to "api".
    mkdir -p "$1/api"
    _pa_write_policy "$1" '{"go-api":{"allow":["api/**"]}}'
    ln -sf "$1" "$1/api/rootlink"
}

setup_symlink_lnk_to_api() {
    # api/lnk -> <root>/api, allow **: "lnk/nope/../../../x.go" (nope does NOT
    # exist) lexically collapses to x.go (allowed) but physically is the
    # project's PARENT. With no realpath/python3 the manual fallback must
    # collapse the unresolved "nope/../../.." tail or it compares an
    # uncollapsed string that merely starts with the root.
    mkdir -p "$1/api"
    _pa_write_policy "$1" '{"go-api":{"allow":["**"]}}'
    ln -sf "$1/api" "$1/api/lnk"
}
setup_symlink_cycle() {
    mkdir -p "$1/api"
    _pa_write_policy "$1" '{"go-api":{"allow":["api/**"]}}'
    ln -sf "$1/api/b" "$1/api/a"
    ln -sf "$1/api/a" "$1/api/b"
}

# ---- cases ------------------------------------------------------------------

echo "Running hook fixtures..."
echo

# --- path-allowlist ---
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_with_bypass     # bypass dir + allowlist absent → permissive
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_bp_scope_exact       # K-99: exact scope bypasses
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_bp_scope_glob        # K-99: web/** glob bypasses
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_bp_scope_longer      # K-99: web/index.js-rotation must NOT bypass web/index.js
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_bp_scope_freetext    # K-99: free-text scope containing the path no longer bypasses
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_bp_scope_bare_prefix # K-99: bare prefix needs prefix/**
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_bp_scope_empty       # K-99: empty scope ignored
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_bp_scope_case        # K-99: case-sensitive, like bash
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_no_allowlist    # no allowlist → permissive WARN
# namespaced agent ("yakos:go-api") must hit bare-keyed policy ("go-api")
case_check path-allowlist.sh   pretooluse-edit-api-namespaced.json 0 path-allowlist setup_allowlist_strict_namespaced
# C3: lexical "../" traversal must be rejected even though it starts inside
# an allowed prefix ("api/...").
case_check path-allowlist.sh   pretooluse-write-traversal.json   2 path-allowlist setup_allowlist_strict
# C4: NotebookEdit must be covered by the same allow/deny policy as
# Edit/Write/MultiEdit.
case_check path-allowlist.sh   pretooluse-notebookedit-api-ok.json      0 path-allowlist setup_allowlist_notebook
case_check path-allowlist.sh   pretooluse-notebookedit-web-blocked.json 2 path-allowlist setup_allowlist_notebook
# H5b: deny matching is case-insensitive (APFS/NTFS default to
# case-insensitive filesystems, so ".env" vs ".ENV" is the same inode).
case_check path-allowlist.sh   pretooluse-write-dotenv-upper.json 2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-pem-upper.json    2 path-allowlist setup_allowlist_deny_pem
# M7: a lexically-fine path that is a symlink resolving outside the
# project root must be blocked regardless of what the allow list says.
case_check path-allowlist.sh   pretooluse-write-symlink-escape.json 2 path-allowlist setup_symlink_escape
# C5: missing jq must fail CLOSED (block), not silently pass every write.
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH" "" "" "2:Go never shells out to jq; a jq-less PATH cannot degrade it, so it decides on policy where bash fails closed on degraded input"
# N1 (round 2): an absolute out-of-root file_path — the shape Claude Code
# actually sends — must be rejected even under an allow:["**"] policy or a
# deny-only policy, and an in-root ABSOLUTE path must still PASS (the
# prefix-strip's job). Before the fix, ps_lexical_normalize silently
# dropped the leading "/" and the M7 check re-anchored the target under
# the project root, so both of these previously PASSED.
case_check path-allowlist.sh   pretooluse-write-absolute-outroot.json       2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-absolute-outroot-goapi.json 2 path-allowlist setup_allowlist_deny_only_goapi
case_check path-allowlist.sh   pretooluse-write-absolute-inroot.json        0 path-allowlist setup_allowlist_strict
# N3 (round 2): the symlink-escape case must still block when NEITHER GNU
# realpath -m NOR python3 is on PATH (the manual ps_realpath fallback's own
# job) — mirrors the NOJQ_PATH pattern above, minus realpath/python3
# instead of minus jq.
case_check path-allowlist.sh   pretooluse-write-symlink-escape.json 2 path-allowlist setup_symlink_escape "PATH=$NORESOLVE_PATH"
# N4.1 (round 2): a policy file that EXISTS but doesn't parse (truncated
# write) must BLOCK, not silently behave like "no policy file".
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json 2 path-allowlist setup_allowlist_corrupt_truncated
# N2 (round 2): the emergency escape hatch must be honored even with jq
# missing, and only when actually set.
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1" "" "" "2:Go never shells out to jq; a jq-less PATH cannot degrade it, so it decides on policy where bash fails closed on degraded input"
# C5 residue (round 2, addendum): same hi_init gap as secret-scan below —
# an empty pipe and a non-object JSON payload must both fail closed.
case_check path-allowlist.sh   pretooluse-write-empty-stdin.json      2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-json-array-not-object.json  2 path-allowlist setup_allowlist_strict
# R2-1 (round 3, regression from round 1): a trailing slash on
# CLAUDE_PROJECT_DIR must not defeat the in-root prefix strip — the
# pattern "$CLAUDE_PROJECT_DIR/*" becomes a literal double-slash
# requirement otherwise, so rel_file stays absolute and the N1 guard
# refuses every in-root write. cpd_suffix="/" appends the trailing slash
# to CLAUDE_PROJECT_DIR only (not to the fixture's embedded path).
case_check path-allowlist.sh   pretooluse-write-inroot-via-placeholder.json 0 path-allowlist setup_allowlist_notebook "" "/"
# R3-1 (round 4): a single `${VAR%/}` strips only ONE trailing slash, so
# a doubled trailing slash reproduced the exact same R2-1 false-block.
# The normalization loop must strip all of them.
case_check path-allowlist.sh   pretooluse-write-inroot-via-placeholder.json 0 path-allowlist setup_allowlist_notebook "" "//"
# R2-5 (round 3, cosmetic): a file_path exactly equal to the project root
# must block (you can't write a file over a directory) with an accurate
# reason, not the generic "absolute and outside the project root" one.
case_check path-allowlist.sh   pretooluse-write-root-equal.json 2 path-allowlist setup_allowlist_notebook
# R2-3 (round 3): the degraded-input bypass now requires the explicit
# "degraded-input" Scope sentinel — a bypass entry scoped to an unrelated
# file must NOT cover a degraded-input event.
case_check path-allowlist.sh   pretooluse-write-empty-stdin.json 2 path-allowlist setup_allowlist_bypass_narrow_scope
case_check path-allowlist.sh   pretooluse-write-empty-stdin.json 0 path-allowlist setup_allowlist_bypass_degraded_input_scope
# R3-2 (round 4): the sentinel match must be EXACT, not substring — a
# Scope that merely CONTAINS "degraded-input" (an ordinary filename) must
# NOT cover a degraded-input event either.
case_check path-allowlist.sh   pretooluse-write-empty-stdin.json 2 path-allowlist setup_allowlist_bypass_substring_collision_scope

# --- path-log ---
case_check path-log.sh         pretooluse-edit-api.json          0 path-log
case_check path-log.sh         pretooluse-edit-web-blocked.json  0 path-log
case_check path-log.sh         pretooluse-write-secret.json      0 path-log

# --- secret-scan ---
case_check secret-scan.sh      pretooluse-edit-api.json          0 secret-scan
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan
# H5a: the PEM rule starts with "-", which grep parsed as an option string
# before the -e fix — it must actually fire.
case_check secret-scan.sh      pretooluse-write-pem-secret.json  2 secret-scan
# C4: NotebookEdit's .new_source must be scanned like any other write.
case_check secret-scan.sh      pretooluse-notebookedit-secret.json 2 secret-scan
case_check secret-scan.sh      pretooluse-notebookedit-api-ok.json  0 secret-scan
# N4.2 (round 2): every PATTERNS entry needs its own firing fixture — only
# AWS and PEM had one before this round, so a future `-e`-class regression
# in the other six would have gone unnoticed. Also widened the PEM rule
# itself to `-----BEGIN [A-Z0-9 ]*PRIVATE KEY` so ENCRYPTED PRIVATE KEY and
# PGP PRIVATE KEY BLOCK (previously missed) fire too.
case_check secret-scan.sh      pretooluse-write-github-token.json    2 secret-scan
case_check secret-scan.sh      pretooluse-write-github-token-fg.json 2 secret-scan
case_check secret-scan.sh      pretooluse-write-slack-token.json     2 secret-scan
case_check secret-scan.sh      pretooluse-write-stripe-key.json      2 secret-scan
case_check secret-scan.sh      pretooluse-write-anthropic-key.json   2 secret-scan
case_check secret-scan.sh      pretooluse-write-google-key.json      2 secret-scan
# C5: missing jq must fail CLOSED (block), not silently pass every write.
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan "" "PATH=$NOJQ_PATH" "" "" "2:Go never shells out to jq, so a jq-less PATH cannot degrade it; it evaluates the payload and enforces its decision where bash fails closed (or, with YAKOS_HOOKS_FAIL_OPEN=1, passes) on degraded input"
# N2 (round 2): the emergency escape hatch must be honored even with jq
# missing.
case_check secret-scan.sh      pretooluse-write-secret.json      0 secret-scan "" "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1" "" "" "2:Go never shells out to jq, so a jq-less PATH cannot degrade it; it evaluates the payload and enforces its decision where bash fails closed (or, with YAKOS_HOOKS_FAIL_OPEN=1, passes) on degraded input"
# C5 residue (round 2, addendum): hi_init used to pass on an empty pipe
# (the `[ -n "$HI_INPUT" ] &&` guard skipped validation on a zero-byte
# read) and on valid-JSON-that-isn't-an-object (`jq empty` accepts an
# array/string/number/null, not just an object). Both must now fail
# closed like malformed JSON does.
case_check secret-scan.sh      pretooluse-write-empty-stdin.json      2 secret-scan
case_check secret-scan.sh      pretooluse-json-array-not-object.json  2 secret-scan
# The escape hatch must still reach both of the above.
case_check secret-scan.sh      pretooluse-write-empty-stdin.json      0 secret-scan "" "YAKOS_HOOKS_FAIL_OPEN=1"
# M6 residue (round 2, addendum): a non-string value at any unioned field
# (new_string here is a number) used to make the whole jq union error,
# which silently swallowed to an empty write_text with NO log record —
# fail open with no trace. content still carries a real secret and must
# still be caught.
case_check secret-scan.sh      pretooluse-edit-newstring-number-secret-content.json 2 secret-scan
# .edits itself can also be the wrong shape (an object instead of an
# array) — must not crash the scan, and a real secret in .new_source
# (NotebookEdit) must still be caught.
case_check secret-scan.sh      pretooluse-notebookedit-edits-object-secret-newsource.json 2 secret-scan
# R2-4 (round 3): a secret inside an array of strings, or an array-valued
# .new_source (the canonical shape of a Jupyter cell's `source`), used to
# be DROPPED by the round-2 select(type=="string") per-field filter — the
# recursive `.. | strings` walk must catch both. A payload with no
# scannable content at all must still pass, AND leave a REPORT record
# (previously a bare exit 0 with no log at all).
case_check secret-scan.sh      pretooluse-write-content-array-secret.json 2 secret-scan
case_check secret-scan.sh      pretooluse-notebookedit-newsource-array-secret.json 2 secret-scan
case_check secret-scan.sh      pretooluse-edit-no-content-fields.json 0 secret-scan
# R3-3 (round 4, regression from round 3): the R2-4 recursive walk
# scanned .edits WHOLE, so a MultiEdit redacting a leaked secret (secret
# only in old_string, clean new_string) was blocked, while the identical
# single-Edit remediation passed — main never scanned old_string at all.
# Scoping the walk to map(.new_string) makes both forms agree: a secret
# being actively REMOVED is allowed in both, a secret being introduced
# or left in a new_string is blocked in both.
case_check secret-scan.sh      pretooluse-multiedit-secret-only-oldstring.json 0 secret-scan
case_check secret-scan.sh      pretooluse-multiedit-secret-newstring.json      2 secret-scan

# --- budget-guard ---
# Previously had zero shell fixtures (Go unit test only, per the security
# review's fixture-coverage audit). Basic block+allow pair, plus the C5
# missing-jq fail-closed case.
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_headroom
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_low_cap
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_low_cap "PATH=$NOJQ_PATH" "" "" "2:Go never shells out to jq, so a jq-less PATH cannot degrade it; it evaluates the payload and enforces its decision where bash fails closed (or, with YAKOS_HOOKS_FAIL_OPEN=1, passes) on degraded input"
# N2 (round 2): budget-guard matches EVERY tool call ("*"), so this is the
# hook where a missing jq previously locked an operator out of the whole
# session. Its own emergency var, YAKOS_BUDGET_DISABLE, is now checked
# BEFORE hi_init, so it must reach the hook even with jq missing (and the
# hook exits before ever touching jq, so it's a clean rc=0). With NEITHER
# set (the case right above this one), missing jq still BLOCKs.
case_check budget-guard.sh     pretooluse-generic-tool.json      0 "" setup_budget_low_cap_disabled "PATH=$NOJQ_PATH YAKOS_BUDGET_DISABLE=1"
# R2-2 (round 3): YAKOS_HOOKS_FAIL_OPEN=1 lets execution continue past
# hi_init with jq still missing, and this hook has no tool-name gate
# (matcher "*"), so it used to reach an unguarded `jq -nc` downstream and
# crash with "jq: command not found" / rc=127 on every tool call. Must
# now be a clean rc=0 with no crash. This exact combination was
# deliberately NOT asserted in round 2 (see the comment that used to sit
# here) because it was known-broken; now fixed and locked in.
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_low_cap "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1" "" "" "2:Go never shells out to jq, so a jq-less PATH cannot degrade it; it evaluates the payload and enforces its decision where bash fails closed (or, with YAKOS_HOOKS_FAIL_OPEN=1, passes) on degraded input"

# --- supervisor-gate ---
# R2-2 (round 3): a second, independent instance of the same defect class
# as budget-guard above — supervisor-gate.sh has no tool-name gate either,
# and reaches unguarded `jq -r` calls once a supervisor-findings.ndjson
# file exists (a common state in an active session, not a rare edge
# case). Same fix, same fixture shape.
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_supervisor_findings_critical "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1" "" "" "2:Go never shells out to jq, so a jq-less PATH cannot degrade it; it evaluates the payload and enforces its decision where bash fails closed (or, with YAKOS_HOOKS_FAIL_OPEN=1, passes) on degraded input"

# --- mailbox-mirror ---
case_check mailbox-mirror.sh   sendmessage-peer.json             0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-from-lead.json        0 mailbox-mirror
case_check mailbox-mirror.sh   sendmessage-to-lead.json          0 mailbox-mirror
# HTML-special chars (<, >, &) in summary/body — regression guard for S-6
# A-2a round 2 review finding 5 (Go's default json.Marshal HTML-escaped
# these; bash's jq -nc never does). The new messages.ndjson content
# comparison in parity_check (above) is what actually catches a
# regression here — exit code and the hooklog record alone wouldn't.
case_check mailbox-mirror.sh   sendmessage-html-chars.json       0 mailbox-mirror

# --- team-lifecycle ---
case_check team-lifecycle.sh   teamcreate.json                   0 team-lifecycle
case_check team-lifecycle.sh   agent-spawn.json                  0 team-lifecycle
# namespaced subagent_type (yakos: prefix) must also pass
case_check team-lifecycle.sh   agent-spawn-namespaced.json       0 team-lifecycle

# --- output-injection-scan ---
# C1 (security-review-2026-09-14.md): the Flows workflow engine splices one
# node's raw output into a downstream node's prompt via
# ${nodes.<id>.output}, dispatched under bypassPermissions with no human in
# the loop. This hook now recognizes a SYNTHETIC tool_name,
# "WorkflowNodeOutput" — sent only by cli-go/internal/workflow/
# output_scan.go, never by Claude Code's own PreToolUse/PostToolUse hook
# dispatch — and BLOCKS (rc=2) on a match there, unlike its long-standing
# WARN-only (rc=0), non-blocking behavior for every real tool name
# (Bash/Read/WebFetch/mcp__*).
#
# 1. A workflow node output matching a known injection pattern must BLOCK.
case_check output-injection-scan.sh posttooluse-workflow-node-output-injected.json 2 output-injection-scan
# 2. Benign workflow node output must still PASS (no false-positive block).
case_check output-injection-scan.sh posttooluse-workflow-node-output-benign.json   0 output-injection-scan
# 3. The exact same injection-pattern text, but on a REAL tool_name (Bash)
#    rather than the synthetic WorkflowNodeOutput value, must keep the
#    original WARN-only behavior — this is the regression guard that a
#    future change to the workflow branch must not widen into the
#    long-standing PostToolUse path.
case_check output-injection-scan.sh posttooluse-bash-output-injected.json         0 output-injection-scan
#
# N2 (s3-flows-security-review-r2-2026-09-21.md): round 2's fix for the
# BSD-grep RE_DUP_MAX limit on pattern 9 (long base64 blob) narrowed the
# threshold to {255,} to get *some* match on macOS, but {255,} is not
# equivalent to the intended {400,} — it hard-blocked ordinary 255-399-char
# base64/hex runs on the now-BLOCKING workflow-node path. Restored the
# 400-char threshold via a portable `grep -oE | awk` form (no RE_DUP_MAX
# limit on any grep implementation). These three fixtures pin the boundary:
# 4. A 308-char base64 run (below the 400-char threshold) must PASS.
case_check output-injection-scan.sh posttooluse-workflow-node-output-base64-below-threshold.json 0 output-injection-scan
# 5. A 260-char hex run (below the 400-char threshold) must PASS.
case_check output-injection-scan.sh posttooluse-workflow-node-output-hex-below-threshold.json    0 output-injection-scan
# 6. A 400-char base64 run (at the intended threshold) must still BLOCK.
case_check output-injection-scan.sh posttooluse-workflow-node-output-base64-at-threshold.json    2 output-injection-scan

# --- session-end-check ---
case_check session-end-check.sh sessionend-clean.json            0 session-end-check
case_check session-end-check.sh sessionend-stuck.json            0 session-end-check setup_with_decisions_stale
case_check session-end-check.sh sessionend-with-team.json        0 session-end-check setup_with_team_created_history

# --- task-* (REPORT-only in v0.1) ---
case_check task-dependency-gate.sh    taskcompleted-blocked.json   0 task-dependency-gate
case_check task-dependency-gate.sh    taskcompleted-unblocked.json 0 task-dependency-gate
case_check task-complete-dispatch.sh  taskcompleted-backend.json   0 task-complete-dispatch
case_check task-complete-dispatch.sh  taskcompleted-frontend.json  0 task-complete-dispatch

# --- cycle-counter ---
# S-6 A-2a: cycle-counter had zero case_check coverage before this WP (A-1
# report §"A-2 sizing") — the hook doesn't gate on tool_name/hook_event_name
# at all, so any well-formed fixture with a session_id + agent_type
# exercises it; reusing pretooluse-generic-tool.json rather than adding a
# new fixture file.
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter
# S-6 A-2a round 3 (re-review finding 1): malformed / wrong-shape / missing
# ~/.yakos-state/settings.json — bash used to crash (rc=5, zero writes)
# while Go degraded gracefully; both sides must now be byte-parity (rc=0,
# default cycle length, counter written, identical log record) after the
# `|| n=""` / `|| val="true"` bash guard. See the home-fn helpers above.
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_malformed_settings
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_wrong_shape_settings
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_missing_settings
# K-106: cycle_length 0 / -1 / "abc" / "" / null / 1e9 / 10 — bash and Go
# must agree (rc=0, default cadence, one WARN for the bad ones).
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_zero
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_negative
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_abc
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_emptystr
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_null
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_huge
case_check cycle-counter.sh    pretooluse-generic-tool.json      0 cycle-counter "" "" "" setup_cycle_counter_len_valid10

# --- path-allowlist: K-87 A-2b gap closure -----------------------------------
#
# K-81 allow semantics: an allow ARRAY constrains (empty = deny-all); missing
# or null = no allow-list; any other type is malformed and blocks.
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_allow_empty
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_allow_empty_bypassed
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_allow_string
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_allow_false
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_allow_zero
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_allow_object
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_allow_null
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_policy_false
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_policy_not_object
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_toplevel_array
# A non-array deny is malformed and blocks on BOTH sides (bash used to yield no
# deny patterns from `.deny // [] | .[]`, silently disabling deny; fixed).
case_check path-allowlist.sh   pretooluse-edit-api.json          2 path-allowlist setup_allowlist_deny_string
# Glob semantics: '*' spans '/' (bash case), so deny recursion works; deny is case-insensitive.
case_check path-allowlist.sh   pretooluse-write-deep-deny.json           2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-deny-mixed-case-dir.json 2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-inroot-dotdot.json       0 path-allowlist setup_allowlist_strict
# Symlink handling, including ".." applied AFTER a symlink (bash used to check only
# the lexically collapsed path; both sides now also resolve the path as written).
case_check path-allowlist.sh   pretooluse-write-inroot-symlink-dotdot.json 0 path-allowlist setup_symlink_inroot_dotdot
case_check path-allowlist.sh   pretooluse-write-dotdot-after-symlink.json 2 path-allowlist setup_symlink_dir_escape
# NUL byte: refused outright on both sides (bash's command substitution used to drop it).
case_check path-allowlist.sh   pretooluse-write-nul-in-path.json 2 path-allowlist setup_allowlist_strict
# Broken jq (present but misbehaving): bash now fails closed on a jq that prints garbage;
# Go never shells out to jq.
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 "" setup_allowlist_strict "PATH=$FAKEJQ_GARBAGE_PATH" "" "" "2:bash fails closed on degraded input (jq printing garbage); Go is jq-independent and blocks on policy (same exit code, different log record)"
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict "PATH=$FAKEJQ_FAIL_PATH" "" "" "2:bash fails closed on a crashing jq (degraded input); Go is jq-independent and blocks on policy"

# --- supervisor-gate: K-87 A-2b -----------------------------------------------
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_pass
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_warn
case_check supervisor-gate.sh  pretooluse-edit-api.json          2 supervisor-gate setup_sg_critical
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_critical_passive
case_check supervisor-gate.sh  pretooluse-edit-api.json          2 supervisor-gate setup_sg_critical_passive_comment
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_critical_bypassed
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_unknown
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_sg_invalid_json
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 "" setup_sg_disabled
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 "" setup_sg_blank_last_line
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 "" "" "YAKOS_SUPERVISOR_DISABLE=1"
# Accepted: a valid-JSON NON-object last line (e.g. []) crashes bash (jq error
# under set -e, rc=5 — a non-blocking hook error); Go treats it as an unusable
# finding, logs a WARN and passes (rc=0).
case_check supervisor-gate.sh  pretooluse-edit-api.json          5 "" setup_sg_nonobject "" "" "" "0:bash crashes (rc=5, jq error under set -e) on a valid-JSON non-object last line; Go treats it as an unusable finding and passes with a WARN"

# --- budget-guard: K-87 A-2b ----------------------------------------------------
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_repeat_cap
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_low_cap_bypassed
case_check budget-guard.sh     pretooluse-generic-tool.json      0 "" setup_budget_disabled_in_yml
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_corrupt_state
case_check budget-guard.sh     pretooluse-generic-tool.json      0 "" setup_no_allowlist

# --- output-injection-scan: K-87 A-2b -------------------------------------------
case_check output-injection-scan.sh posttooluse-bash-clean.json               0 output-injection-scan
case_check output-injection-scan.sh posttooluse-mcp-injected.json             0 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-response-object.json     0 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-zero-width.json          0 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-zero-width-below.json    0 output-injection-scan
case_check output-injection-scan.sh posttooluse-read-dsa-key.json             0 output-injection-scan
case_check output-injection-scan.sh posttooluse-read-rsa-key.json             0 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-multiline-phrase.json    0 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-system-line.json         0 output-injection-scan
case_check output-injection-scan.sh posttooluse-workflow-node-output-object.json 2 output-injection-scan
case_check output-injection-scan.sh posttooluse-bash-output-injected.json     0 "" "" "YAKOS_INJECTION_SCAN_DISABLE=1"
case_check output-injection-scan.sh posttooluse-bash-output-injected.json     0 "" setup_injection_scan_disabled_in_yml
# The two disable switches quiet ONLY the WARN-only path; the blocking workflow path ignores them.
case_check output-injection-scan.sh posttooluse-workflow-node-output-injected.json 2 output-injection-scan "" "YAKOS_INJECTION_SCAN_DISABLE=1"
case_check output-injection-scan.sh posttooluse-workflow-node-output-injected.json 2 output-injection-scan setup_injection_scan_disabled_in_yml

# --- context-inject: K-87 A-2b ---------------------------------------------------
case_check context-inject.sh   pretooluse-generic-tool.json 0 context-inject setup_ci_with_decisions
case_check context-inject.sh   pretooluse-generic-tool.json 0 context-inject setup_ci_empty
case_check context-inject.sh   pretooluse-generic-tool.json 0 context-inject setup_ci_with_critical
case_check context-inject.sh   pretooluse-generic-tool.json 0 context-inject setup_ci_decisions_section_off
case_check context-inject.sh   pretooluse-generic-tool.json 0 "" setup_no_allowlist
case_check context-inject.sh   pretooluse-generic-tool.json 0 "" setup_ci_with_decisions "YAKOS_CONTEXT_INJECT_DISABLE=1"

# --- context-threshold ------------------------------------------------------------
case_check context-threshold.sh pretooluse-generic-tool.json 0 context-threshold "" "" "" home_noop
case_check context-threshold.sh pretooluse-generic-tool.json 0 context-threshold "" "" "" home_ct_low
case_check context-threshold.sh pretooluse-generic-tool.json 0 context-threshold "" "" "" home_ct_notice

# --- supervisor-ack-gate ----------------------------------------------------------
case_check supervisor-ack-gate.sh teamcreate.json 0 supervisor-ack-gate "" "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh teamcreate.json 0 supervisor-ack-gate setup_sag_continue "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh teamcreate.json 2 supervisor-ack-gate setup_sag_halt "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh agent-spawn.json 2 supervisor-ack-gate setup_sag_halt "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh teamcreate.json 2 supervisor-ack-gate setup_sag_malformed_then_halt "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh teamcreate.json 0 supervisor-ack-gate setup_sag_halt "YAKOS_PROJECT_NAME=proj" "" home_sag_acked
case_check supervisor-ack-gate.sh teamcreate.json 0 "" setup_sag_gate_off "YAKOS_PROJECT_NAME=proj" "" home_noop
case_check supervisor-ack-gate.sh teamcreate.json 0 "" setup_sag_halt "YAKOS_PROJECT_NAME=proj YAKOS_SUPERVISOR_DISABLE=1" "" home_noop
case_check supervisor-ack-gate.sh pretooluse-generic-tool.json 0 "" setup_sag_halt "YAKOS_PROJECT_NAME=proj" "" home_noop

# --- supervisor-stream ------------------------------------------------------------
case_check supervisor-stream.sh pretooluse-edit-api.json   0 supervisor-stream setup_ss_passfilter
case_check supervisor-stream.sh pretooluse-edit-risky.json 0 supervisor-stream setup_ss_passfilter
case_check supervisor-stream.sh pretooluse-edit-api.json   0 supervisor-stream setup_ss_prefilter_off
case_check supervisor-stream.sh pretooluse-edit-api.json   0 "" setup_ss_disabled
case_check supervisor-stream.sh pretooluse-edit-risky.json 0 "" setup_ss_passfilter "YAKOS_SUPERVISOR_DISABLE=1"

# --- retro-dispatch ---------------------------------------------------------------
case_check retro-dispatch.sh   pretooluse-generic-tool.json 0 "" "" "" "" home_noop
case_check retro-dispatch.sh   pretooluse-generic-tool.json 0 retro-dispatch setup_rd_marker "" "" home_rd_auto_off
case_check retro-dispatch.sh   pretooluse-generic-tool.json 0 retro-dispatch setup_rd_marker "" "" home_rd_inflight
case_check retro-dispatch.sh   pretooluse-generic-tool.json 0 retro-dispatch setup_rd_marker "PATH=$NOYAKOS_PATH" "" home_noop

# --- plan-outcome-capture ---------------------------------------------------------
case_check plan-outcome-capture.sh pretooluse-generic-tool.json 0 plan-outcome-capture "" "YAKOS_PLAN_QUALITY_LOG=__TMP__/pql.ndjson YAKOS_DISPATCH_LOG=__TMP__/dl.ndjson"
case_check plan-outcome-capture.sh pretooluse-generic-tool.json 0 plan-outcome-capture setup_poc_scored "YAKOS_PLAN_QUALITY_LOG=__TMP__/pql.ndjson YAKOS_DISPATCH_LOG=__TMP__/dl.ndjson"
case_check plan-outcome-capture.sh pretooluse-generic-tool.json 0 plan-outcome-capture setup_poc_complete "YAKOS_PLAN_QUALITY_LOG=__TMP__/pql.ndjson YAKOS_DISPATCH_LOG=__TMP__/dl.ndjson"

# --- plan-quality-gate (PreToolUse marker gate) -------------------------------------
case_check plan-quality-gate.sh teamcreate.json 0 plan-quality-gate
case_check plan-quality-gate.sh teamcreate.json 2 plan-quality-gate setup_pqg_blocked
case_check plan-quality-gate.sh agent-spawn.json 2 plan-quality-gate setup_pqg_blocked
case_check plan-quality-gate.sh teamcreate.json 0 plan-quality-gate setup_pqg_blocked_but_disabled
case_check plan-quality-gate.sh teamcreate.json 0 "" setup_pqg_blocked "YAKOS_PLAN_QUALITY_DISABLE=1"
case_check plan-quality-gate.sh pretooluse-generic-tool.json 0 "" setup_pqg_blocked

# --- peer-claim / peer-claim-confirm ------------------------------------------------
case_check peer-claim.sh       pretooluse-peer-claim-block.json 0 "" "" "YAKOS_COORD_ROOT=__TMP__/nocoord YAKOS_PROJECT_NAME=proj"
case_check peer-claim.sh       pretooluse-peer-claim-block.json 0 "" setup_pc_coord "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim.sh       pretooluse-peer-claim-block.json 2 peer-claim setup_pc_peer_claim "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim.sh       pretooluse-peer-claim-block.json 0 peer-claim setup_pc_peer_claim_bypassed "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim.sh       pretooluse-peer-claim-block.json 0 "" setup_pc_own_claim "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim.sh       pretooluse-peer-claim-block.json 0 "" setup_pc_expired_claim "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim-confirm.sh posttooluse-peer-claim-confirm.json 0 peer-claim-confirm setup_pc_coord "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim-confirm.sh posttooluse-peer-claim-confirm.json 0 "" "" "YAKOS_COORD_ROOT=__TMP__/nocoord YAKOS_PROJECT_NAME=proj"
case_check peer-claim.sh       pretooluse-peer-claim-write-toolinput-only.json 2 peer-claim setup_pc_peer_claim "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"
case_check peer-claim-confirm.sh posttooluse-peer-claim-confirm-write-toolinput-only.json 0 peer-claim-confirm setup_pc_coord "YAKOS_COORD_ROOT=__TMP__/coord YAKOS_PROJECT_NAME=proj USER=bob HOSTNAME=dev01 YAKOS_SESSION_PID=2002"

# K-100 items 1-3: identity comes from the stdin payload (agent_type with the
# "yakos:" prefix and surrounding whitespace, session_id), never from
# YAKOS_AGENT_ROLE / CLAUDE_SESSION_ID. The decoy env values must not surface
# in either side's records.
case_check mailbox-mirror.sh   sendmessage-peer-prefixed-agent.json 0 mailbox-mirror "" "YAKOS_AGENT_ROLE=decoy-role CLAUDE_SESSION_ID=decoy-sid"
# log name is "" on purpose: the hooklog record schema differs (pre-existing, tracked
# separately); the buffer comparison above is what pins the payload-derived identity.
case_check supervisor-stream.sh pretooluse-edit-api.json   0 "" setup_ss_passfilter "YAKOS_AGENT_ROLE=decoy-role CLAUDE_SESSION_ID=decoy-sid"
# bash context-threshold.sh now sources lib/compat.sh (K-101), so both sides probe
# the transcript. The remaining divergence is the Go log schema (action/message
# instead of decision/reason, no agent/session_id/event), tracked as K-107.
case_check context-threshold.sh pretooluse-generic-tool.json 0 context-threshold "" "CLAUDE_SESSION_ID=decoy-sid" "" home_ct_notice "0:Go context-threshold log schema differs from bash: action/message instead of decision/reason, and no agent/session_id/event (K-107)"

# K-100 item 5: undecodable stdin (0 bytes) on NON-BLOCKING hooks. The registry
# FailClosed flag (checked against bash HOOK_FAIL_CLOSED=1 by
# TestRegistryFailClosedMatchesBashDeclaration) drives the entrypoint: these hooks
# WARN on stderr and pass with exit 0 and no stdout, exactly like bash's
# _hi_fail_or_warn. Where the log record differs it is an accepted, pinned
# divergence, see DEGRADED_ACCEPT below.
DEGRADED_ACCEPT="0:bash continues into the hook body with EMPTY input after the WARN and appends a log or telemetry record built from empty fields; Go exits 0 right after the identical WARN. Neither blocks nor writes stdout."
case_check context-inject.sh        pretooluse-write-empty-stdin.json 0 ""
case_check context-threshold.sh     pretooluse-write-empty-stdin.json 0 context-threshold "" "" "" "" "$DEGRADED_ACCEPT"
case_check cycle-counter.sh         pretooluse-write-empty-stdin.json 0 cycle-counter "" "" "" "" "$DEGRADED_ACCEPT"
case_check mailbox-mirror.sh        pretooluse-write-empty-stdin.json 0 ""
case_check output-injection-scan.sh pretooluse-write-empty-stdin.json 0 ""
case_check path-log.sh              pretooluse-write-empty-stdin.json 0 ""
case_check peer-claim-confirm.sh    pretooluse-write-empty-stdin.json 0 ""
case_check plan-outcome-capture.sh  pretooluse-write-empty-stdin.json 0 plan-outcome-capture "" "" "" "" "$DEGRADED_ACCEPT"
case_check plan-quality-score.sh    pretooluse-write-empty-stdin.json 0 ""
case_check retro-dispatch.sh        pretooluse-write-empty-stdin.json 0 ""
case_check session-end-check.sh     pretooluse-write-empty-stdin.json 0 session-end-check "" "" "" "" "$DEGRADED_ACCEPT"
case_check supervisor-stream.sh     pretooluse-write-empty-stdin.json 0 supervisor-stream "" "" "" "" "$DEGRADED_ACCEPT"
case_check task-complete-dispatch.sh pretooluse-write-empty-stdin.json 0 task-complete-dispatch "" "" "" "" "$DEGRADED_ACCEPT"
case_check task-dependency-gate.sh  pretooluse-write-empty-stdin.json 0 task-dependency-gate "" "" "" "" "$DEGRADED_ACCEPT"
case_check team-lifecycle.sh        pretooluse-write-empty-stdin.json 0 ""


# --- task-complete-dispatch: K-87 A-2b (would_run is framework-root-relative on both sides) ---
case_check task-complete-dispatch.sh  taskcompleted-backend.json   0 task-complete-dispatch setup_tcd_bypass
case_check task-complete-dispatch.sh  teamcreate.json              0 task-complete-dispatch

# --- path-allowlist: K-87 A-2b round 2 (bash hardening) ------------------------------
# bash 3.2 crash (fail-open, exit 1) when ".." pops the segment list to empty.
case_check path-allowlist.sh   pretooluse-write-dotdot-dotenv.json   2 path-allowlist setup_allowlist_strict
# Newline / NUL: refused outright on both sides.
case_check path-allowlist.sh   pretooluse-write-newline-traversal.json 2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-write-newline-deny.json      2 path-allowlist setup_allowlist_strict
# ".." after a symlink that points at the project root (physical parent escapes; lexical does not).
case_check path-allowlist.sh   pretooluse-write-rootlink-dotdot.json   2 path-allowlist setup_symlink_rootlink
case_check path-allowlist.sh   pretooluse-write-rootlink-dotdot.json   2 path-allowlist setup_symlink_rootlink "PATH=$NORESOLVE_PATH"
# '*' consuming '/' in the MIDDLE of a pattern (deny "api/*/secret.go").
case_check path-allowlist.sh   pretooluse-write-midstar-deny.json      2 path-allowlist setup_allowlist_midstar_deny
case_check path-allowlist.sh   pretooluse-write-midstar-ok.json        0 path-allowlist setup_allowlist_midstar_deny
# jq missing / garbage on an ALLOWED path: bash fails closed (2), Go evaluates and allows (0).
case_check path-allowlist.sh   pretooluse-edit-api.json                2 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH" "" "" "0:bash fails closed on degraded input (jq missing or printing garbage) even for an allowed path; Go never depends on jq, evaluates the payload and allows it"
case_check path-allowlist.sh   pretooluse-edit-api.json                2 "" setup_allowlist_strict "PATH=$FAKEJQ_GARBAGE_PATH" "" "" "0:bash fails closed on degraded input (jq missing or printing garbage) even for an allowed path; Go never depends on jq, evaluates the payload and allows it"

# realpath-fallback tail collapse (guards _ps_abs_normalize; only the NORESOLVE case reaches it).
case_check path-allowlist.sh   pretooluse-write-fallback-tail-dotdot.json 2 path-allowlist setup_symlink_lnk_to_api
case_check path-allowlist.sh   pretooluse-write-fallback-tail-dotdot.json 2 path-allowlist setup_symlink_lnk_to_api "PATH=$NORESOLVE_PATH"
# Symlink cycle: not an escape (the OS returns ELOOP), so bash's pass is harmless; Go fails closed.
case_check path-allowlist.sh   pretooluse-write-symlink-cycle.json 0 path-allowlist setup_symlink_cycle "" "" "" "2:a symlink cycle is not an escape (ELOOP on write) so bash passes; Go's resolver fails closed on an unresolvable chain"

# --- plan-quality-gate / plan-quality-score (K-81 split) ---
# The gate is the fail-closed PreToolUse half; the scorer is the conservative
# PostToolUse half. Exit codes must agree bash-vs-Go for every case below.
setup_plan_blocked() {
    mkdir -p "$1/work/current"
    printf '{"plan_id":"plan-abc123","reason":"aggregate 0.4 < threshold 0.75"}\n' > "$1/work/current/.plan-blocked"
}
setup_plan_blocked_dir() {
    mkdir -p "$1/work/current/.plan-blocked"
}
setup_plan_blocked_text() {
    mkdir -p "$1/work/current"
    printf 'plain text reason\n' > "$1/work/current/.plan-blocked"
}
case_check plan-quality-gate.sh agent-spawn.json                     0 plan-quality-gate
case_check plan-quality-gate.sh agent-spawn.json                     2 plan-quality-gate setup_plan_blocked
case_check plan-quality-gate.sh agent-spawn.json                     2 plan-quality-gate setup_plan_blocked_dir
case_check plan-quality-gate.sh agent-spawn.json                     2 plan-quality-gate setup_plan_blocked_text
case_check plan-quality-gate.sh pretooluse-agentx-tool.json          0 ""                  setup_plan_blocked
case_check plan-quality-gate.sh pretooluse-generic-tool.json         0 ""                  setup_plan_blocked
case_check plan-quality-gate.sh pretooluse-json-array-not-object.json 2 plan-quality-gate
case_check plan-quality-score.sh agent-spawn.json                    0 ""                  setup_plan_blocked
case_check plan-quality-score.sh pretooluse-generic-tool.json        0 ""
# Payload-shape regression (K-87): real payloads carry the target at
# tool_input.file_path only. plan_quality.enabled=false makes the recognised
# plan.md write observable as a "skipping" REPORT without invoking the scorer.
setup_pqs_disabled() { printf 'plan_quality:\n  enabled: false\n' > "$1/.yakos.yml"; }

# ---- K-99 plan-quality-score: score the file just written -----------------
# The scorer runs the real score-plan.sh against canned judge verdicts
# (YAKOS_PLAN_JUDGE_MOCK), on both sides, in a sandboxed HOME.
PQS_MOCK="$REPO_ROOT/tests/fixtures/plan-judge-mock"
PQS_PLANS="$REPO_ROOT/tests/fixtures/plans"
pqs_home() { :; }
_pqs_plan() {  # <tmp> <plan-fixture> <age-seconds> <yml-body>
    mkdir -p "$1/work/current"
    cp "$PQS_PLANS/$2" "$1/work/current/plan.md"
    if [ "$3" -gt 0 ]; then
        touch -t "$(date -u -v-"$3"S +%Y%m%d%H%M.%S 2>/dev/null || date -u -d "$3 seconds ago" +%Y%m%d%H%M.%S)" "$1/work/current/plan.md"
    fi
    printf '%s' "$4" > "$1/.yakos.yml"
}
PQS_BLOCK_YML=$'plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n'
PQS_SURFACE_YML=$'plan_quality:\n  enabled: true\n  mode: surface\n  threshold: 0.75\n'
setup_pqs_vague_block()   { _pqs_plan "$1" vague-plan.md 30 "$PQS_BLOCK_YML"; }
setup_pqs_vague_surface() { _pqs_plan "$1" vague-plan.md 30 "$PQS_SURFACE_YML"; }
setup_pqs_good_block()    { _pqs_plan "$1" good-plan.md 30 "$PQS_BLOCK_YML"; }
setup_pqs_dissent_block() { _pqs_plan "$1" dissent-plan.md 30 "$PQS_BLOCK_YML"; }
setup_pqs_fresh_plan()    { _pqs_plan "$1" vague-plan.md 0 "$PQS_BLOCK_YML"; }
case_check plan-quality-score.sh posttooluse-write-plan-md-toolinput-only.json 0 plan-quality-score setup_pqs_disabled
case_check plan-quality-score.sh posttooluse-write-plan-md-real.json 0 plan-quality-score setup_pqs_vague_block   "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/low-nodissent" "" pqs_home   # below threshold + block: .plan-blocked, block_next_tool
case_check plan-quality-score.sh posttooluse-write-plan-md-real.json 0 plan-quality-score setup_pqs_vague_surface "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/low-nodissent" "" pqs_home   # below threshold + surface: notes only
case_check plan-quality-score.sh posttooluse-write-plan-md-real.json 0 plan-quality-score setup_pqs_good_block    "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/good"         "" pqs_home   # above threshold: pass
case_check plan-quality-score.sh posttooluse-write-plan-md-real.json 0 plan-quality-score setup_pqs_dissent_block "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/dissent"      "" pqs_home   # dissent: surface, never block
case_check plan-quality-score.sh posttooluse-write-plan-md-real.json 0 plan-quality-score setup_pqs_fresh_plan    "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/vague"        "" pqs_home   # mtime < 5 s: debounced, nothing scored
case_check plan-quality-score.sh posttooluse-write-other-file.json   0 ""                 setup_pqs_vague_block   "YAKOS_PLAN_JUDGE_MOCK=$PQS_MOCK/low-nodissent" "" pqs_home   # non-plan.md write: silent no-op

# ---- summary ------------------------------------------------------------

echo
echo "Bash baseline: $pass passed, $fail failed (same assertions as run-hook-fixtures.sh)"
if [ "$fail" -gt 0 ]; then
    printf '\nBash baseline failure detail:\n%s\n' "$fail_log"
fi

echo
echo "Bash-vs-Go parity matrix (per hook: fixtures compared, parity count, first divergence):"
echo

# Computed from $REPORT_FILE rather than bash associative arrays — see the
# comment near pass=0/fail=0 above for why (bash 3.2 on macOS's /bin/bash
# has no declare -A).
total_cases="$(wc -l < "$REPORT_FILE" | tr -d ' ')"
total_parity="$(jq -s '[.[] | select(.divergence == "-")] | length' "$REPORT_FILE" 2>/dev/null || echo 0)"
total_accepted="$(jq -s '[.[] | select(.divergence == "accepted")] | length' "$REPORT_FILE" 2>/dev/null || echo 0)"
total_stale="$(jq -s '[.[] | select(.divergence == "accept-stale")] | length' "$REPORT_FILE" 2>/dev/null || echo 0)"

jq -rs '
  group_by(.hook)
  | map({
      hook: .[0].hook,
      fixtures: length,
      parity: ([.[] | select(.divergence == "-")] | length),
      accepted: ([.[] | select(.divergence == "accepted")] | length),
      first_divergence: ([.[] | select(.divergence != "-" and .divergence != "accepted")][0].divergence // "-"),
      first_divergence_fixture: ([.[] | select(.divergence != "-" and .divergence != "accepted")][0].fixture // "-")
    })
  | sort_by(.hook)
  | .[]
  | "\(.hook)|\(.fixtures)|\(.parity)|\(.accepted)|\(.first_divergence)|\(.first_divergence_fixture)"
' "$REPORT_FILE" 2>/dev/null | while IFS='|' read -r hook fixtures parity accepted first_div first_fix; do
    if [ "$parity" = "$fixtures" ]; then
        status="100%"
    else
        status="${parity}/${fixtures}"
    fi
    if [ "$accepted" != "0" ]; then
        status="$status +${accepted}acc"
    fi
    printf '  %-24s %-14s divergence=%s (%s)\n' "$hook" "$status" "$first_div" "$first_fix"
done

echo
echo "Overall: $total_parity/$total_cases fixture comparisons at parity (+$total_accepted accepted, documented divergences)."
echo "Report:  $REPORT_FILE"

# Enforcement gate. Every hook named in YAKOS_PARITY_REQUIRE_HOOKS (space-
# separated; default: path-allowlist, the security-critical hook whose Go port
# is expected to hold exact decision parity) must have NO unaccepted
# divergence and NO stale accept-annotation, or the script exits non-zero.
# Hooks not listed remain advisory, as before (see the header).
gate_fail=0
for _h in ${YAKOS_PARITY_REQUIRE_HOOKS-path-allowlist}; do
    if [ -n "${YAKOS_PARITY_ONLY:-}" ] && [ "$_h" != "$YAKOS_PARITY_ONLY" ]; then
        continue
    fi
    _bad="$(jq -s --arg h "$_h" '[.[] | select(.hook == $h and .divergence != "-" and .divergence != "accepted")] | length' "$REPORT_FILE" 2>/dev/null || echo 1)"
    if [ "$_bad" != "0" ]; then
        echo "PARITY GATE FAIL: $_h has $_bad unaccepted divergence(s)/stale annotation(s)" >&2
        gate_fail=1
    fi
done
if [ "$total_stale" != "0" ]; then
    echo "PARITY GATE FAIL: $total_stale stale accept annotation(s) (the divergence no longer exists; remove the annotation)" >&2
    gate_fail=1
fi

# Exit code: non-zero on a bash-baseline failure or a gate failure (see the
# header). Bash-vs-Go disagreement on any non-gated hook is reported in the
# matrix but does not fail the run.
if [ "$fail" -gt 0 ] || [ "$gate_fail" -gt 0 ]; then
    exit 1
fi
exit 0
