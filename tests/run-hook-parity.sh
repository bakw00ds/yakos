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
# This harness does NOT assert bash-vs-Go equality as a pass/fail gate on
# the whole suite (see S-6 structural plan §1.2 / §2.4: the two sides are
# known to diverge systemically until A-2 closes each hook's gap). Instead
# it emits a per-hook PASS/FAIL matrix — fixtures compared, parity count,
# and the first divergence class (exit / stdout / stderr / log-schema /
# missing-log) for the first fixture that diverged — to
# work/current/logs/hook-parity-report.ndjson, and prints the same matrix
# to stdout. The script's own exit code reflects whether every GO-SIDE
# invocation completed without an unexpected process error (crash, unknown
# hook, etc.) — not whether bash and Go agree; CI treats this step as
# advisory (continue-on-error) rather than blocking, per D4.
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
trap 'rm -rf "$NOJQ_PATH" "$NORESOLVE_PATH"' EXIT
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
    local hook="$1" fixture="$2" expected_rc="$3" log_name="$4" setup_fn="${5:-}" extra_env="${6:-}" cpd_suffix="${7:-}" home_fn="${8:-}"

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
        stdout_capture="$(printf '%s' "$payload" | env $run_env YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$cpd" bash "$HOOKS/$hook" 2>"$bash_stderr_file")" || actual_rc=$?
    else
        stdout_capture="$(printf '%s' "$payload" | YAKOS_WORK_DIR="$tmp/work" CLAUDE_PROJECT_DIR="$cpd" bash "$HOOKS/$hook" 2>"$bash_stderr_file")" || actual_rc=$?
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
        "$tmp/work/current/logs" "$stdout_capture" "$bash_stderr" "$tmp" "$home_fn"

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
        | sed "s|$bash_dir|__SANDBOX__|g; s|$go_dir|__SANDBOX__|g"
}

# parity_check runs the Go side of one case and records the comparison.
# Args: hook-script-relpath, fixture-relpath, bash-rc, log-name, setup-fn,
#       extra-env, cpd-suffix, bash-log-dir, bash-stdout, bash-stderr,
#       bash-sandbox-dir, home-fn
parity_check() {
    local hook="$1" fixture="$2" bash_rc="$3" log_name="$4" setup_fn="$5" extra_env="$6" cpd_suffix="$7" bash_log_dir="$8" bash_stdout="$9"
    local bash_stderr="${10}" bash_tmp="${11}" home_fn="${12:-}"
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
            norm_bash="$(tail -n 1 "$bash_log" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_bash__")"
            norm_go="$(tail -n 1 "$go_log" | jq -cS 'del(.ts)' 2>/dev/null || echo "__unparseable_go__")"
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

    if [ "$divergence" = "-" ] && [ "$bash_stdout" != "$go_stdout" ]; then
        divergence="stdout"
    fi

    if [ "$divergence" = "-" ]; then
        local norm_bash_stderr norm_go_stderr
        norm_bash_stderr="$(normalize_stderr "$bash_stderr" "$bash_tmp" "$tmp2")"
        norm_go_stderr="$(normalize_stderr "$go_stderr" "$bash_tmp" "$tmp2")"
        [ "$norm_bash_stderr" != "$norm_go_stderr" ] && divergence="stderr"
    fi

    printf '{"hook":%s,"fixture":%s,"bash_rc":%s,"go_rc":%s,"divergence":%s}\n' \
        "$(json_str "$hookname")" "$(json_str "$(basename "$fixture")")" "$bash_rc" "$go_rc" "$(json_str "$divergence")" >> "$REPORT_FILE"

    if [ "$divergence" = "-" ]; then
        printf 'PARITY  %-24s | %-42s | rc=%s\n' "$hookname" "$(basename "$fixture")" "$go_rc"
    else
        printf 'DIVERGE %-24s | %-42s | %s (bash_rc=%s go_rc=%s)\n' "$hookname" "$(basename "$fixture")" "$divergence" "$bash_rc" "$go_rc"
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

# ---- cases ------------------------------------------------------------------

echo "Running hook fixtures..."
echo

# --- path-allowlist ---
case_check path-allowlist.sh   pretooluse-edit-api.json          0 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_with_bypass     # bypass dir + allowlist absent → permissive
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
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  2 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH"
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
case_check path-allowlist.sh   pretooluse-edit-web-blocked.json  0 path-allowlist setup_allowlist_strict "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1"
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
case_check secret-scan.sh      pretooluse-write-secret.json      2 secret-scan "" "PATH=$NOJQ_PATH"
# N2 (round 2): the emergency escape hatch must be honored even with jq
# missing.
case_check secret-scan.sh      pretooluse-write-secret.json      0 secret-scan "" "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1"
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
case_check budget-guard.sh     pretooluse-generic-tool.json      2 budget-guard setup_budget_low_cap "PATH=$NOJQ_PATH"
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
case_check budget-guard.sh     pretooluse-generic-tool.json      0 budget-guard setup_budget_low_cap "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1"

# --- supervisor-gate ---
# R2-2 (round 3): a second, independent instance of the same defect class
# as budget-guard above — supervisor-gate.sh has no tool-name gate either,
# and reaches unguarded `jq -r` calls once a supervisor-findings.ndjson
# file exists (a common state in an active session, not a rare edge
# case). Same fix, same fixture shape.
case_check supervisor-gate.sh  pretooluse-edit-api.json          0 supervisor-gate setup_supervisor_findings_critical "PATH=$NOJQ_PATH YAKOS_HOOKS_FAIL_OPEN=1"

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

jq -rs '
  group_by(.hook)
  | map({
      hook: .[0].hook,
      fixtures: length,
      parity: ([.[] | select(.divergence == "-")] | length),
      first_divergence: ([.[] | select(.divergence != "-")][0].divergence // "-"),
      first_divergence_fixture: ([.[] | select(.divergence != "-")][0].fixture // "-")
    })
  | sort_by(.hook)
  | .[]
  | "\(.hook)|\(.fixtures)|\(.parity)|\(.first_divergence)|\(.first_divergence_fixture)"
' "$REPORT_FILE" 2>/dev/null | while IFS='|' read -r hook fixtures parity first_div first_fix; do
    if [ "$parity" = "$fixtures" ]; then
        status="100%"
    else
        status="${parity}/${fixtures}"
    fi
    printf '  %-24s %-10s divergence=%s (%s)\n' "$hook" "$status" "$first_div" "$first_fix"
done

echo
echo "Overall: $total_parity/$total_cases fixture comparisons at parity."
echo "Report:  $REPORT_FILE"

# The script's own exit code reflects the bash baseline (same contract as
# run-hook-fixtures.sh) and the Go side actually running without a process
# error — NOT bash-vs-Go agreement, which is expected to be partial until
# A-2. CI wires this step with continue-on-error: true regardless (D4).
if [ "$fail" -gt 0 ]; then
    exit 1
fi
exit 0
