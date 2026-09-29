#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# run-hook-bypass-scope-test.sh — K-99: ho_check_bypass scope matching is
# exact-or-glob (was substring). Sources lib/hooks/lib/hook-output.sh and
# drives ho_check_bypass directly against a scratch hook-bypass.md. The Go
# twin's table lives in cli-go/internal/hooks/hookbypass/hookbypass_test.go;
# the hook-level bash-vs-Go comparison is in run-hook-parity.sh.
#
# Usage: bash tests/run-hook-bypass-scope-test.sh   (bash 3.2+ safe)
set -u

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
TMP="$(mktemp -d -t yakos-bpscope-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/work/current"
export CLAUDE_PROJECT_DIR="$TMP" YAKOS_WORK_DIR="$TMP/work"

# shellcheck source=../lib/hooks/lib/hook-output.sh
. "$REPO_ROOT/lib/hooks/lib/hook-output.sh"

pass=0
fail=0
FILE="$TMP/work/current/hook-bypass.md"

# write_entry <hook> <scope-text> — one active entry
write_entry() {
    printf '# Active hook bypasses\n\n## Active entries\n\n## bypass:t\n\n**Hook:** %s\n**Scope:** %s\n' "$1" "$2" > "$FILE"
}

# expect <Y|n> <label> <hook> <entry-scope> <probe> [expected-stderr]
# stderr must equal the given text, or be empty when none is given.
expect() {
    local want="$1" label="$2" hook="$3" entry="$4" probe="$5" warn="${6:-}"
    local got=n err
    write_entry "$hook" "$entry"
    err="$(ho_check_bypass "$hook" "$probe" 2>&1 >/dev/null)"
    if ho_check_bypass "$hook" "$probe" >/dev/null 2>&1; then got=Y; fi
    if [ "$got" = "$want" ] && [ "$err" = "$warn" ]; then
        pass=$((pass + 1))
    else
        fail=$((fail + 1))
        printf 'FAIL %s: entry=[%s] probe=[%s] want=%s got=%s stderr=[%s]\n' "$label" "$entry" "$probe" "$want" "$got" "$err"
    fi
}

W='WARN: bypass entry has empty scope, ignored'

expect Y "exact"                    secret-scan 'web/secret.env'               'web/secret.env'
expect n "longer entry (the bug)"   secret-scan 'web/secret.env-rotation'      'web/secret.env'
expect Y "longer entry, own probe"  secret-scan 'web/secret.env-rotation'      'web/secret.env-rotation'
expect n "free text contains path"  secret-scan 'path=web/index.js reason=x'   'web/index.js'
expect n "bare prefix"              secret-scan 'web'                          'web/index.js'
expect n "bare prefix slash"        secret-scan 'web/'                         'web/index.js'
expect Y "prefix/**"                secret-scan 'web/**'                       'web/a/b.env'
expect n "prefix/** other dir"      secret-scan 'web/**'                       'webx/y'
expect n "prefix/** the dir itself" secret-scan 'web/**'                       'web'
expect Y "* crosses /"              secret-scan 'web/*'                        'web/a/b'
expect Y "leading glob"             secret-scan '*.env'                        'web/secret.env'
expect Y "bare star"                secret-scan '*'                            'any/thing'
expect n "? alone is not a glob"    secret-scan 'web/sec?et.env'               'web/secret.env'
expect n "case-sensitive"           secret-scan 'Web/Secret.env'               'web/secret.env'
expect n "backslash is not a slash" secret-scan 'web/secret.env'               'web\secret.env'
expect n "empty entry ignored+warn" secret-scan ''                             'web/secret.env' "$W"
expect n "blank entry ignored+warn" secret-scan '   '                          'web/secret.env' "$W"
expect n "empty probe (exact entry)" secret-scan 'web/secret.env'              ''
expect n "empty probe (star entry)" secret-scan '*'                            ''
write_entry path-allowlist 'web/secret.env'
if ho_check_bypass secret-scan 'web/secret.env' >/dev/null 2>&1; then fail=$((fail + 1)); echo "FAIL wrong hook matched"; else pass=$((pass + 1)); fi
expect Y "peer-claim exact"         peer-claim 'file=a/b.ts peer=alice@dev01'  'file=a/b.ts peer=alice@dev01'
expect Y "peer-claim glob"          peer-claim 'file=* peer=alice@dev01'       'file=a/b.ts peer=alice@dev01'
expect n "peer-claim wrong peer"    peer-claim 'file=* peer=alice@dev01'       'file=a/b.ts peer=bob@dev01'
expect n "peer-claim peer only"     peer-claim 'peer=alice@dev01'              'file=a/b.ts peer=alice@dev01'
expect Y "budget cap exact"         budget 'cap=max_tool_calls'                'cap=max_tool_calls'
expect n "budget other cap"         budget 'cap=max_tool_calls'                'cap=max_wall_seconds'

# CRLF file and Scope-before-Hook order.
printf '## Active entries\r\n## bypass:c\r\n**Hook:** secret-scan\r\n**Scope:** web/**\r\n' > "$FILE"
if ho_check_bypass secret-scan 'web/a' >/dev/null 2>&1; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL CRLF glob"; fi
printf '## Active entries\n## bypass:o\n**Scope:** api/main.go\n**Hook:** secret-scan\n' > "$FILE"
if ho_check_bypass secret-scan 'api/main.go' >/dev/null 2>&1; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL scope-before-hook"; fi

# Entries before the Active heading are ignored.
printf '## bypass:ex\n**Hook:** secret-scan\n**Scope:** api/main.go\n## Active entries\n' > "$FILE"
if ho_check_bypass secret-scan 'api/main.go' >/dev/null 2>&1; then fail=$((fail + 1)); echo "FAIL example block honored"; else pass=$((pass + 1)); fi

# Early return: a matching entry before a blank one must not warn.
printf '## Active entries\n## bypass:a\n**Hook:** secret-scan\n**Scope:** web/x\n## bypass:b\n**Hook:** secret-scan\n**Scope:**\n' > "$FILE"
err="$(ho_check_bypass secret-scan 'web/x' 2>&1 >/dev/null)"
if [ -z "$err" ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL warned after a match: $err"; fi

printf 'bypass-scope: %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
