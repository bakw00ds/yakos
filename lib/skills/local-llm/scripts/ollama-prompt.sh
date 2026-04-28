#!/usr/bin/env bash
# Purpose: Invoke a local Ollama model with a templated prompt; write
#          response + sidecar metadata to work/current/artifacts/.
# Inputs:
#   --template <n>      Template name (without .prompt.txt extension)
#   --input <path>         File to feed as input to the model
#   --output <path>        Where to write the model's response
#   --model <model>        Ollama model tag (default: llama3.2:3b)
#   --max-bytes <N>        Reject input larger than N bytes (default 262144)
#   --force                Overwrite existing output file
# Outputs:
#   <output>               Model's response text
#   <output>.meta.json     Sidecar with tool/model/template/input/output/ts
# Exit codes:
#   0   success
#   2   user error (missing/bad args, missing input/template, output exists)
#   3   missing optional dependency (ollama)
#   4   resource limit exceeded (--max-bytes)
# Reads:   <template-file>, <input-file>
# Writes:  <output>, <output>.meta.json (one new tmp file inside $TMPDIR)
# Network: none (ollama runs locally; this script makes no HTTP calls)

set -euo pipefail

# Resolve helpers — paths.sh provides CLAUDE_PROJECT_DIR-aware resolution if
# called from inside a session; absent if running standalone.
SCRIPT_DIR="$(cd "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../../.." && pwd -P 2>/dev/null || true)"
if [ -n "${REPO_ROOT:-}" ] && [ -f "$REPO_ROOT/cli/lib/compat.sh" ]; then
    # shellcheck source=../../../../cli/lib/compat.sh
    . "$REPO_ROOT/cli/lib/compat.sh"
fi

usage() {
    cat <<EOF
Usage: ollama-prompt.sh --template <n> --input <path> --output <path> [opts]

Required:
    --template <n>     Template name (without .prompt.txt) from templates/
    --input <path>        Input file
    --output <path>       Output file path (relative resolves under CLAUDE_PROJECT_DIR)

Optional:
    --model <model>       Ollama model tag (default: llama3.2:3b)
    --max-bytes <N>       Max input bytes (default: 262144 / 256 KB)
    --force               Overwrite output if it exists
    -h, --help            Show this help

Templates: summarize, classify, extract, sanity-check
Output:    <output>           the model's response
           <output>.meta.json sidecar with tool/model/timestamp

Exit codes: 0=ok, 2=user error, 3=ollama missing, 4=max-bytes exceeded.
EOF
}

TEMPLATE=""
INPUT=""
OUTPUT=""
MODEL="${OLLAMA_MODEL:-llama3.2:3b}"
MAX_BYTES="${MAX_BYTES:-262144}"
FORCE=0

while [ $# -gt 0 ]; do
    case "$1" in
        --template)  TEMPLATE="${2:-}"; shift 2 || { echo "Error: --template requires a value" >&2; exit 2; } ;;
        --input)     INPUT="${2:-}"; shift 2 || { echo "Error: --input requires a value" >&2; exit 2; } ;;
        --output)    OUTPUT="${2:-}"; shift 2 || { echo "Error: --output requires a value" >&2; exit 2; } ;;
        --model)     MODEL="${2:-}"; shift 2 || { echo "Error: --model requires a value" >&2; exit 2; } ;;
        --max-bytes) MAX_BYTES="${2:-}"; shift 2 || { echo "Error: --max-bytes requires a value" >&2; exit 2; } ;;
        --force)     FORCE=1; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           echo "Unknown arg: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[ -n "$TEMPLATE" ] || { echo "Error: --template required" >&2; exit 2; }
[ -n "$INPUT" ]    || { echo "Error: --input required" >&2; exit 2; }
[ -n "$OUTPUT" ]   || { echo "Error: --output required" >&2; exit 2; }

# Validate user-controlled inputs first; the "ollama missing" message
# should never mask a bad-args error the user can fix without ollama.

# Resolve template
TEMPLATE_FILE="$SCRIPT_DIR/../templates/${TEMPLATE}.prompt.txt"
if [ ! -f "$TEMPLATE_FILE" ]; then
    {
        echo "Error: template not found: $TEMPLATE_FILE"
        echo "Available templates:"
        find "$SCRIPT_DIR/../templates/" -maxdepth 1 -type f -name '*.prompt.txt' 2>/dev/null \
            | sed 's|.*/||; s|\.prompt\.txt$||; s|^|  |'
    } >&2
    exit 2
fi

# Validate input
[ -f "$INPUT" ] || { echo "Error: input not found: $INPUT" >&2; exit 2; }

# Max-bytes guard — enforced BEFORE we try to feed the model. Most local
# models silently truncate or produce garbage on inputs that exceed their
# context window; failing loud here is the safer move.
INPUT_BYTES="$(wc -c < "$INPUT" | tr -d ' ')"
if [ "$INPUT_BYTES" -gt "$MAX_BYTES" ]; then
    cat >&2 <<EOF
Error: input file exceeds max bytes.
    Input: $INPUT
    Size:  $INPUT_BYTES bytes
    Max:   $MAX_BYTES bytes (override with --max-bytes)

Most local models have small context windows; large inputs typically
fail silently or produce truncated output. Consider:
    - Splitting the input into chunks and summarizing each
    - Using a model with a larger context window (--model)
    - Pre-filtering the input
EOF
    exit 4
fi

# All user-input validation passed. Now check ollama is available.
if ! command -v ollama >/dev/null 2>&1; then
    cat >&2 <<EOF
Error: ollama not found on PATH.

Install Ollama from https://ollama.com (no network call made by this
script). After install, pull a model:
    ollama pull $MODEL

This skill is optional; YakOS works without it.
EOF
    exit 3
fi

# Resolve relative output paths against CLAUDE_PROJECT_DIR if set.
case "$OUTPUT" in
    /*) ;;
    *)  if [ -n "${CLAUDE_PROJECT_DIR:-}" ]; then
            OUTPUT="${CLAUDE_PROJECT_DIR}/${OUTPUT}"
        fi
        ;;
esac

if [ -e "$OUTPUT" ] && [ "$FORCE" -eq 0 ]; then
    echo "Error: output exists; use --force to overwrite: $OUTPUT" >&2
    exit 2
fi

mkdir -p "$(dirname -- "$OUTPUT")"

# Stream input via temp file — never load entire input into a shell variable.
# This handles binary-safe content and avoids ARG_MAX issues on large prompts.
TMP_PROMPT="$(mktemp)"
trap 'rm -f "$TMP_PROMPT"' EXIT

cat "$TEMPLATE_FILE" > "$TMP_PROMPT"
printf '\n\n---INPUT START---\n' >> "$TMP_PROMPT"
cat "$INPUT" >> "$TMP_PROMPT"
printf '\n---INPUT END---\n' >> "$TMP_PROMPT"

TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "Running $MODEL with template '$TEMPLATE'..." >&2
ollama run "$MODEL" < "$TMP_PROMPT" > "$OUTPUT"

# Generate sidecar metadata via jq — NEVER via heredoc/shell interpolation.
# jq's --arg substitution handles paths/names with quotes, backslashes,
# newlines correctly. Heredoc would break on adversarial filenames.
META="${OUTPUT}.meta.json"
jq -n \
    --arg tool "ollama" \
    --arg model "$MODEL" \
    --arg template "$TEMPLATE" \
    --arg input "$INPUT" \
    --arg output "$OUTPUT" \
    --arg created_at "$TS" \
    --arg yakos_skill "local-llm" \
    --arg yakos_version "0.1.0" \
    '{
        tool: $tool,
        model: $model,
        template: $template,
        input: $input,
        output: $output,
        created_at: $created_at,
        yakos_skill: $yakos_skill,
        yakos_version: $yakos_version
    }' > "$META"

echo "Wrote $OUTPUT" >&2
echo "Wrote $META" >&2
