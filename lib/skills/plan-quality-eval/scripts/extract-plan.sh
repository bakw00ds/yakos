#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# extract-plan.sh — parse a plan.md into canonical JSON for scoring.
#
# Usage:
#   bash extract-plan.sh <plan.md>
#
# Output (stdout): JSON object:
#   {
#     "plan_id": "p-<yyyymmdd>-<hhmmss>-<sha8>",
#     "tasks": [
#       {
#         "id": "T-1",
#         "desc": "...",
#         "agent_type": "...",
#         "blockedBy": ["T-0"],
#         "blockedBy_reason": "...",
#         "acceptance": "...",
#         "estimate_days": 1.0
#       },
#       ...
#     ],
#     "assumptions": ["...", "..."],
#     "risks": ["...", "..."],
#     "raw_md_hash": "sha256 of raw plan.md content"
#   }
#
# Exits 0 on success, 2 on parsing failure (missing required sections),
# 1 on usage error.
#
# Expected plan.md format:
#   ---
#   plan_id: p-20260526-120000-abc12345
#   ---
#
#   ## Assumptions
#   - item 1
#   - item 2
#
#   ## Tasks
#   ### T-1: Short description
#   agent_type: backend
#   estimate: 1 day
#   blockedBy: []
#   blockedBy_reason: ""
#   done_means: >
#     File api/handlers/foo.go exists and returns 200 on GET /foo.
#
#   ### T-2: Another task
#   ...
#
#   ## Risks
#   - Migration could fail; rollback: restore snapshot.
#
# If plan_id is absent from frontmatter, one is generated from the file
# mtime + content hash.

set -eu

PLAN_FILE="${1:-}"
if [ -z "$PLAN_FILE" ]; then
    echo "Usage: extract-plan.sh <plan.md>" >&2
    exit 1
fi
if [ ! -f "$PLAN_FILE" ]; then
    echo "extract-plan.sh: file not found: $PLAN_FILE" >&2
    exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "extract-plan.sh: jq is required (brew install jq)" >&2
    exit 1
fi

# ---- hash -------------------------------------------------------------------
if command -v shasum >/dev/null 2>&1; then
    RAW_HASH="$(shasum -a 256 "$PLAN_FILE" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
    RAW_HASH="$(sha256sum "$PLAN_FILE" | awk '{print $1}')"
elif command -v openssl >/dev/null 2>&1; then
    RAW_HASH="$(openssl dgst -sha256 "$PLAN_FILE" | awk '{print $NF}')"
else
    RAW_HASH="unknown"
fi

PLAN_CONTENT="$(cat "$PLAN_FILE")"

# ---- extract plan_id from frontmatter --------------------------------------
PLAN_ID=""
# Look for YAML frontmatter block (lines between first --- pair)
in_fm=0
while IFS= read -r line; do
    if [ "$line" = "---" ]; then
        if [ "$in_fm" = "0" ]; then
            in_fm=1
            continue
        else
            break
        fi
    fi
    if [ "$in_fm" = "1" ]; then
        case "$line" in
            plan_id:*)
                PLAN_ID="${line#plan_id:}"
                PLAN_ID="$(printf '%s' "$PLAN_ID" | sed 's/^[[:space:]]*//' | sed 's/[[:space:]]*$//')"
                ;;
        esac
    fi
done <<EOF
$PLAN_CONTENT
EOF

# Generate plan_id if not found
if [ -z "$PLAN_ID" ]; then
    TS="$(date -u +%Y%m%d-%H%M%S)"
    SHA8="${RAW_HASH:0:8}"
    PLAN_ID="p-${TS}-${SHA8}"
fi

# ---- extract sections via awk -----------------------------------------------
# We parse three sections: Assumptions, Tasks, Risks
# Section boundary: starts at "## <name>" H2, ends at next "## " H2 or EOF.

extract_section() {
    local section_name="$1"
    awk -v sec="$section_name" '
        /^## / {
            if ($0 ~ "^## " sec) { in_sec=1; next }
            else { in_sec=0 }
        }
        in_sec { print }
    ' "$PLAN_FILE"
}

ASSUMPTIONS_RAW="$(extract_section "Assumptions")"
TASKS_RAW="$(extract_section "Tasks")"
RISKS_RAW="$(extract_section "Risks")"

# Validate required sections exist
if [ -z "$TASKS_RAW" ]; then
    echo "extract-plan.sh: missing '## Tasks' section in $PLAN_FILE" >&2
    exit 2
fi

# ---- parse assumptions (bullet list) ----------------------------------------
# Each non-empty line starting with "- " is an assumption item.
parse_bullets() {
    local raw="$1"
    printf '%s\n' "$raw" | awk '
        /^- / {
            item = substr($0, 3)
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", item)
            if (length(item) > 0) {
                if (first) { printf "," }
                printf "%s", item
                first = 1
            }
        }
        BEGIN { first = 0 }
    ' | jq -Rs 'split(",") | map(select(length > 0))'
}

# Build assumptions array (JSON)
ASSUMPTIONS_JSON="$(printf '%s\n' "$ASSUMPTIONS_RAW" | awk '/^- /{print substr($0,3)}' | jq -R . | jq -s .)"
RISKS_JSON="$(printf '%s\n' "$RISKS_RAW" | awk '/^- /{print substr($0,3)}' | jq -R . | jq -s .)"

# ---- parse tasks (### T-N: heading + key: value pairs) ----------------------
# Each task block starts at "### " and ends at the next "### " or end of section.
# We build a JSON array of task objects.

parse_tasks() {
    python3 - "$PLAN_FILE" <<'PYEOF'
import sys
import re
import json

plan_file = sys.argv[1]
with open(plan_file) as f:
    content = f.read()

# Find the ## Tasks section
tasks_match = re.search(r'^## Tasks\s*\n(.*?)(?=^## |\Z)', content, re.MULTILINE | re.DOTALL)
if not tasks_match:
    print(json.dumps([]))
    sys.exit(0)

tasks_text = tasks_match.group(1)

# Split into individual task blocks at "### "
task_blocks = re.split(r'\n(?=### )', tasks_text.strip())

tasks = []
for block in task_blocks:
    block = block.strip()
    if not block:
        continue

    task = {
        "id": "",
        "desc": "",
        "agent_type": "",
        "blockedBy": [],
        "blockedBy_reason": "",
        "acceptance": "",
        "estimate_days": 0.0
    }

    lines = block.splitlines()
    header_line = lines[0] if lines else ""

    # Parse "### T-N: Description" or "### N. Description"
    m = re.match(r'^###\s+([\w-]+):\s*(.*)', header_line)
    if not m:
        m = re.match(r'^###\s+(\d+)\.\s*(.*)', header_line)
    if m:
        task["id"] = m.group(1)
        task["desc"] = m.group(2).strip()
    else:
        # Fallback: use the whole header as desc
        task["desc"] = re.sub(r'^#+\s*', '', header_line).strip()
        task["id"] = "T-" + str(len(tasks) + 1)

    # Parse key: value pairs in the block
    body_text = "\n".join(lines[1:])

    for key, pattern in [
        ("agent_type", r'agent_type\s*:\s*(.+)'),
        ("blockedBy_reason", r'blockedBy_reason\s*:\s*(.+)'),
        ("done_means", r'done_means\s*:\s*(.+)'),
        ("acceptance", r'(?:done_means|acceptance|done means)\s*:\s*(.+)'),
    ]:
        m2 = re.search(pattern, body_text, re.IGNORECASE)
        if m2 and key not in ("acceptance",):
            task[key] = m2.group(1).strip().strip('"').strip("'")

    # acceptance: prefer done_means, fallback to acceptance field
    for pat in [r'done_means\s*:\s*(.+)', r'acceptance\s*:\s*(.+)']:
        m2 = re.search(pat, body_text, re.IGNORECASE)
        if m2:
            task["acceptance"] = m2.group(1).strip().strip('"').strip("'")
            break

    # Parse blockedBy (list or empty)
    m2 = re.search(r'blockedBy\s*:\s*\[([^\]]*)\]', body_text)
    if m2:
        raw_list = m2.group(1)
        items = [x.strip().strip('"').strip("'") for x in raw_list.split(',') if x.strip()]
        task["blockedBy"] = items
    else:
        m2 = re.search(r'blockedBy\s*:\s*(\S+)', body_text)
        if m2 and m2.group(1) not in ('[]', 'none', 'null', '""', "''"):
            task["blockedBy"] = [m2.group(1).strip()]

    # Parse estimate
    m2 = re.search(r'estimate\s*:\s*([\d.]+)\s*(day|hour|week)', body_text, re.IGNORECASE)
    if m2:
        val = float(m2.group(1))
        unit = m2.group(2).lower()
        if unit == "hour":
            val = val / 8.0
        elif unit == "week":
            val = val * 5.0
        task["estimate_days"] = round(val, 2)

    tasks.append(task)

print(json.dumps(tasks))
PYEOF
}

TASKS_JSON="$(parse_tasks)"

# ---- assemble final JSON -----------------------------------------------------
jq -n \
    --arg plan_id "$PLAN_ID" \
    --arg raw_md_hash "$RAW_HASH" \
    --argjson tasks "$TASKS_JSON" \
    --argjson assumptions "$ASSUMPTIONS_JSON" \
    --argjson risks "$RISKS_JSON" \
    '{
        plan_id: $plan_id,
        tasks: $tasks,
        assumptions: $assumptions,
        risks: $risks,
        raw_md_hash: $raw_md_hash
    }'
