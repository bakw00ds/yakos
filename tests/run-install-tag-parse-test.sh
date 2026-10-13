#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Purpose: Regression test for scripts/install.sh resolve_latest_version.
#          The tag was once parsed with a GNU-only sed `\?`, which BSD sed
#          (macOS) does not support, so the raw JSON line became the version.
# Inputs:  none
# Outputs: stdout: per-case pass/fail
# Reads:   scripts/install.sh
# Writes:  $TMPDIR/yakos-install-tag.<pid>/* (cleaned at end)
# Exit codes: 0 all pass, 1 any failure.
#
# Runs the real function under /bin/sh with the platform sed (/usr/bin/sed
# on macOS) and a stub curl that prints a captured GitHub API payload.

set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
WORKDIR="${TMPDIR:-/tmp}/yakos-install-tag.$$"
mkdir -p "$WORKDIR/bin"
trap 'rm -rf "$WORKDIR" 2>/dev/null || true' EXIT

PASS=0
FAIL=0
ok()   { printf '  [ok]   %s\n' "$*"; PASS=$((PASS + 1)); }
fail() { printf '  [FAIL] %s\n' "$*" >&2; FAIL=$((FAIL + 1)); }

# Extract just the function from the installer (no side effects).
awk '/^resolve_latest_version\(\) \{/{p=1} p{print} p&&/^\}/{exit}' \
    "$REPO_ROOT/scripts/install.sh" > "$WORKDIR/fn.sh"
[ -s "$WORKDIR/fn.sh" ] || { echo "cannot extract resolve_latest_version" >&2; exit 1; }

# Stub curl: prints $PAYLOAD_FILE.
cat > "$WORKDIR/bin/curl" <<'STUB'
#!/bin/sh
cat "$PAYLOAD_FILE"
STUB
chmod +x "$WORKDIR/bin/curl"

# run_case <name> <payload> <expect-stdout> <expect-exit>
run_case() {
    name="$1"; payload="$2"; want="$3"; want_rc="$4"
    printf '%s\n' "$payload" > "$WORKDIR/payload.json"
    set +e
    got="$(PAYLOAD_FILE="$WORKDIR/payload.json" PATH="$WORKDIR/bin:$PATH" \
        /bin/sh -c '. "$1"; GITHUB_API=x; resolve_latest_version' sh "$WORKDIR/fn.sh" 2>/dev/null)"
    rc=$?
    set -e
    if [ "$rc" = "$want_rc" ] && [ "$got" = "$want" ]; then
        ok "$name"
    else
        fail "$name: rc=$rc (want $want_rc) out='$got' (want '$want')"
    fi
}

printf 'sed under test: %s\n' "$(command -v sed)"

BODY='{
  "url": "https://api.github.com/repos/bakw00ds/yakos/releases/1",
  "tag_name": "v0.63.0.0",
  "name": "v0.63.0.0"
}'
run_case "v-prefixed tag yields bare version" "$BODY" "0.63.0.0" 0
run_case "tag line alone, two-space indent" '  "tag_name": "v0.63.0.0",' "0.63.0.0" 0
run_case "tag without v prefix" '  "tag_name": "0.64.1",' "0.64.1" 0
run_case "compact JSON, single line" '{"tag_name":"v1.2.3"}' "1.2.3" 0
run_case "missing tag_name fails" '{"message":"Not Found"}' "" 1
run_case "non-version tag is rejected" '  "tag_name": "nightly-abc",' "" 1

printf '%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
