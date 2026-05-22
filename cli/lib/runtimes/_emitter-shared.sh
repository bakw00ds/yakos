#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# _emitter-shared.sh — shared helper for runtime adapter agent emitters.
#
# Purpose: factor out the python3-via-tempfile pattern so codex.sh and
# gemini.sh don't duplicate it. Both adapters take a composed-agent JSON
# blob and emit a runtime-native file (TOML for codex; markdown for
# gemini); the common scaffolding is identical.

set -eu

# yk_emit_run_python <agent-id> <out-file> <agent-json> <python-script>
#   Stage <agent-json> to a tempfile (avoids stdin/heredoc collision),
#   then run <python-script> with argv=[agent-id, out-file, json-tmp].
#   The python script is a heredoc string passed as the 4th arg.
#
#   Caller python script reads JSON from sys.argv[3], writes the
#   emitted file at sys.argv[2].
yk_emit_run_python() {
    local agent_id="$1"
    local out_file="$2"
    local agent_json="$3"
    local py_script="$4"

    local json_tmp
    json_tmp="$(mktemp -t yakos-emit.XXXXXX)"
    printf '%s' "$agent_json" > "$json_tmp"

    python3 -c "$py_script" "$agent_id" "$out_file" "$json_tmp"
    local rc=$?

    rm -f "$json_tmp" 2>/dev/null || true
    return "$rc"
}

# yk_emit_check_python
#   Return 0 if python3 is available; 1 otherwise.
yk_emit_check_python() {
    command -v python3 >/dev/null 2>&1
}
