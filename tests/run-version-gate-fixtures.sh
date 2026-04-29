#!/usr/bin/env bash
# Purpose: Drive the gate hook's classify_file() against documented fixtures.
#   Each fixture under tests/fixtures/version/change-classification/ is a
#   newline-separated list of paths; the filename encodes the expected
#   classification. The runner sources the gate hook and invokes
#   classify_file on each path, asserting the result matches the fixture's
#   filename-encoded expectation.
# Inputs:  none
# Outputs: stdout: per-fixture pass/fail; stderr: error context.
# Reads:   tests/fixtures/version/change-classification/*.txt
#          lib/hooks/git/pre-push-version-gate.sh
# Writes:  nothing
# Exit codes: 0 all pass, 1 one or more fixture mismatches.
set -eu

REPO_ROOT="$(cd "$(dirname -- "$0")/.." && pwd -P)"
GATE_SCRIPT="$REPO_ROOT/lib/hooks/git/pre-push-version-gate.sh"
FIXTURE_DIR="$REPO_ROOT/tests/fixtures/version/change-classification"

if [ ! -f "$GATE_SCRIPT" ]; then
    echo "ERROR: gate script not found at $GATE_SCRIPT" >&2
    exit 1
fi
if [ ! -d "$FIXTURE_DIR" ]; then
    echo "ERROR: fixture dir not found at $FIXTURE_DIR" >&2
    exit 1
fi

# The gate script is a full PreToolUse-style hook (reads stdin, runs
# git operations). We don't want to execute the whole thing — we want
# to extract just classify_file() and is_public_surface() and run them
# against fixture inputs. Approach: source the gate script in a
# subshell with stdin redirected from /dev/null and the early-exit
# paths short-circuited via a no-op git override, OR copy the
# functions out by parsing the script.
#
# Pragmatic approach: extract the function bodies via awk and source
# only those into a child shell.

extract_functions() {
    awk '
        /^classify_file\(\)/        { capture = "classify"; depth = 0 }
        /^is_public_surface\(\)/    { capture = "is_public"; depth = 0 }
        capture {
            print
            for (i = 1; i <= length($0); i++) {
                c = substr($0, i, 1)
                if (c == "{") depth++
                else if (c == "}") {
                    depth--
                    if (depth == 0 && i == length($0)) {
                        capture = ""
                        next
                    }
                }
            }
        }
    ' "$GATE_SCRIPT"
}

# Map fixture filename → expected classification token.
fixture_to_expected() {
    case "$1" in
        doc-only)         echo DOC_ONLY ;;
        patch-refactor)   echo PATCH_REFACTOR ;;
        patch-refinement) echo PATCH_REFINEMENT ;;
        default-patch)    echo DEFAULT_PATCH ;;
        minor-additive)   echo MINOR_ADDITIVE ;;
        major-breaking)   echo MAJOR_BREAKING ;;
        *) return 1 ;;
    esac
}

# Generate a child shell that has the extracted functions plus a tiny
# driver. We embed via a heredoc to avoid eval-of-extracted-text edge
# cases. Functions are extracted to a tmp file, then sourced.
tmp_funcs="$(mktemp -t yakos-fixture-runner-XXXXX.sh)"
trap 'rm -f "$tmp_funcs"' EXIT
extract_functions > "$tmp_funcs"

if ! [ -s "$tmp_funcs" ]; then
    echo "ERROR: failed to extract classify_file/is_public_surface from gate script" >&2
    exit 1
fi

total=0
pass=0
fail=0

for fixture_file in "$FIXTURE_DIR"/*.txt; do
    [ -f "$fixture_file" ] || continue
    fixture_name="$(basename -- "$fixture_file" .txt)"
    expected="$(fixture_to_expected "$fixture_name" || echo UNKNOWN)"
    if [ "$expected" = "UNKNOWN" ]; then
        echo "SKIP: $fixture_name — no expected-class mapping"
        continue
    fi

    while IFS= read -r path; do
        [ -n "$path" ] || continue
        case "$path" in '#'*) continue ;; esac

        total=$((total + 1))
        # Most fixtures represent modifications (is_new=0). For
        # MINOR_ADDITIVE we want is_new=1 (new file).
        is_new=0
        case "$expected" in MINOR_ADDITIVE) is_new=1 ;; esac

        actual="$(bash -c '. "$0"; classify_file "$1" "$2"' "$tmp_funcs" "$path" "$is_new" 2>/dev/null)"

        if [ "$actual" = "$expected" ]; then
            pass=$((pass + 1))
        else
            fail=$((fail + 1))
            echo "FAIL  $path → got=$actual  expected=$expected  (fixture: $fixture_name)"
        fi
    done < "$fixture_file"
done

echo
echo "Results: $pass/$total pass, $fail fail"
if [ "$fail" -gt 0 ]; then
    exit 1
fi
exit 0
