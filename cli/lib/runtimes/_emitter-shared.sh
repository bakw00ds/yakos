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

    # `|| rc=$?` keeps a non-zero exit from tripping `set -e` before the
    # tempfile is removed; the caller decides what the status means (3 is
    # "agent text refused", see yk_emit_nul_in_agent).
    local rc=0
    python3 -c "$py_script" "$agent_id" "$out_file" "$json_tmp" || rc=$?

    rm -f "$json_tmp" 2>/dev/null || true
    return "$rc"
}

# yk_emit_check_python
#   Return 0 if python3 is available; 1 otherwise.
yk_emit_check_python() {
    command -v python3 >/dev/null 2>&1
}

# ---------------------------------------------------------------------------
# jq fallback (python3 absent)
#
# The python emitters and the Go materializer
# (cli-go/internal/agentscompose/materialize_*.go) write the same bytes for the
# same agent JSON; these definitions make the jq fallback agree with them, so a
# host without python3 gets the same files rather than a close approximation.
# Rules, applied in this order:
#   one-line values (description, model, tools): line breaks become spaces, then
#       \ and " are escaped, then C0 controls except TAB and DEL become \u00XX
#   the TOML prompt: leading CR/LF dropped, trailing LF dropped, \ escaped,
#       """ broken up, then C0 controls except TAB and LF, DEL and a CR that does
#       not start a CRLF pair become \u00XX (TOML multi-line strings read CRLF as
#       a newline and reject the rest)
#   a model that is a Claude tier (haiku sonnet opus fable) is not written: the
#       composers produce one for an agent pinned to an alias such as balanced,
#       and neither codex nor agy has a model by that name
# Bytes are escaped with upper-case hex, as the chat path's tomlString does.
# ---------------------------------------------------------------------------
_YK_EMIT_JQ_DEFS='
def hex2: ["0","1","2","3","4","5","6","7","8","9","A","B","C","D","E","F"] as $h
          | $h[(. / 16 | floor)] + $h[. % 16];
def oneline: gsub("\r\n|\r|\n"; " ");
def bs: split("\\") | join("\\\\");
def dq: split("\"") | join("\\\"");
def tq: split("\"\"\"") | join("\\\"\\\"\\\"");
def ctl($re): gsub($re; "\\u00" + (.c | explode[0] | hex2));
def ctlline: ctl("(?<c>[\\x01-\\x08\\x0a-\\x1f\\x7f])");
def ctlblock: ctl("(?<c>[\\x01-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]|\\r(?!\\n))");
def quoteline: oneline | bs | dq | ctlline;
def promptbody: sub("\\A[\\r\\n]+"; "") | sub("\\n+\\z"; "");
def tier: . == "haiku" or . == "sonnet" or . == "opus" or . == "fable";
'

# yk_emit_nul_in_agent <agent-json> [tools]
#   Return 0 when a NUL byte sits in the description, prompt or model (and in
#   the tools when a second argument is given). Such text cannot be written to
#   a TOML or Markdown agent file and no persona has one, so the emitters
#   refuse it. jq is used because a shell variable cannot hold a NUL.
yk_emit_nul_in_agent() {
    local with_tools='[]'
    [ "$#" -ge 2 ] && with_tools='(.tools? // [])'
    printf '%s' "$1" | jq -e "[.description?, .prompt?, .model?] + $with_tools
        | map(select(type == \"string\") | explode | any(. == 0)) | any" >/dev/null 2>&1
}
