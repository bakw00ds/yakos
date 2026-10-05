#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""
claude-sdk-dispatch.py — invoke Anthropic Claude Agent SDK for yakOS.

Invoked by cli/lib/runtimes/claude-sdk.sh::yk_rt_claude_sdk_dispatch.

Inputs (env vars):
  YAKOS_AGENT_ID      — agent id (key in the composed agents JSON)
  YAKOS_PROJECT_DIR   — project repo path (passed as cwd to the SDK)
  YAKOS_AGENTS_JSON   — composed agents JSON (output of yk_agents_compose)
  YAKOS_USAGE_OUT     — optional path; if set, write usage JSON here

Inputs (stdin):
  task prompt (UTF-8 text)

Outputs:
  stdout: assistant response text
  stderr: errors / log lines
  exit 0 on success, non-zero on error (78 when ANTHROPIC_API_KEY is missing,
  see below)

Auth (K-137): ANTHROPIC_API_KEY is REQUIRED. Anthropic's terms (2026-02-19)
allow a Pro or Max subscription's OAuth only in Claude Code and claude.ai, not in
the Agent SDK, which this script runs. main() refuses first thing, before it
reads its other inputs or imports the SDK: exit status 78 (sysexits EX_CONFIG)
and one stderr line that never contains any part of a credential. It then removes
subscription OAuth variables from its own environment, which the SDK and the
Claude Code CLI it bundles inherit. claude-sdk.sh (yk_rt_claude_sdk_dispatch)
gates and scrubs first; this is the second anchor for a script started any other
way. Twins: cli-go/internal/interactive/sidecar/sidecar.mjs and
cli-go/internal/runtime/sdk_env.go.

v0.26 (Plan 5 M3) — verified against the SDK's examples/ + types.py
(not the README, which is incomplete). Real surface used:

  - Per-agent `model` via AgentDefinition (alias: "sonnet"/"opus"/
    "haiku"/"inherit" OR full model ID).
  - Sub-agents via `agents={name: AgentDefinition(...)}` dict in
    ClaudeAgentOptions; all teammates from the composition are
    surfaced so the dispatched agent CAN delegate via Task.
  - ResultMessage.total_cost_usd, .duration_ms, .num_turns, .usage,
    .model_usage, .permission_denials — written to YAKOS_USAGE_OUT
    when set.
  - `tools=` (base set) vs `allowed_tools=` (auto-allowed without
    prompting). yakOS's per-agent tools: frontmatter restricts the
    AGENT's tool surface, so it maps to AgentDefinition.tools.
"""

import json
import os
import sys

EXIT_API_KEY_REQUIRED = 78  # sysexits EX_CONFIG, as in sidecar.mjs

# Prefixes of subscription OAuth tokens: access (oat) and refresh (ort).
_OAUTH_TOKEN_MARKERS = ("sk-ant-oat", "sk-ant-ort")


# The two refusal sentences are constants on purpose: nothing computed from the
# environment is ever written to stderr. The names are plain on purpose too:
# CodeQL treats a value assigned to a credential-looking name (OAUTH, KEY, TOKEN)
# as sensitive data and flags every log call it reaches.
_MSG_UNSET = ("refusing to run: ANTHROPIC_API_KEY is not set; the Agent SDK does "
                 "not run on a claude.ai subscription login (set an API key, or use "
                 "the claude runtime, which is Claude Code itself)")
_MSG_SUBSCRIPTION = ("refusing to run: ANTHROPIC_API_KEY holds a subscription OAuth "
                 "token, not an API key; the Agent SDK does not accept those (set an "
                 "API key, or use the claude runtime, which is Claude Code itself)")


def die(msg, code=1):
    sys.stderr.write(f"claude-sdk-dispatch: {msg}\n")
    sys.exit(code)


def _is_oauth_value(value):
    low = value.lower()
    return any(marker in low for marker in _OAUTH_TOKEN_MARKERS)


def startup_check(environ):
    """Return "ok", "unset" or "oauth": whether this script may run.

    Reads only ANTHROPIC_API_KEY and returns a bare state word. main() turns the
    state into one of two constant sentences, so no part of the value, and no
    other variable, is ever echoed.
    """
    key = (environ.get("ANTHROPIC_API_KEY") or "").strip()
    if not key:
        return "unset"
    if _is_oauth_value(key):
        return "oauth"
    return "ok"


def scrub_oauth_env(environ):
    """Delete subscription OAuth variables from environ (os.environ in main).

    Any CLAUDE_CODE_OAUTH* name goes, in any case, and so does any variable whose
    value contains an OAuth token marker. The SDK and the Claude Code CLI it
    bundles inherit this environment.
    """
    for name in list(environ):
        if name.upper().startswith("CLAUDE_CODE_OAUTH") or _is_oauth_value(environ[name]):
            del environ[name]


def read_env():
    agent_id = os.environ.get("YAKOS_AGENT_ID")
    if not agent_id:
        die("YAKOS_AGENT_ID env var required")
    project = os.environ.get("YAKOS_PROJECT_DIR")
    if not project:
        die("YAKOS_PROJECT_DIR env var required")
    agents_raw = os.environ.get("YAKOS_AGENTS_JSON")
    if not agents_raw:
        die("YAKOS_AGENTS_JSON env var required")
    try:
        agents = json.loads(agents_raw)
    except json.JSONDecodeError as exc:
        die(f"invalid YAKOS_AGENTS_JSON: {exc}")
    if agent_id not in agents:
        die(f"agent '{agent_id}' not in composed agents "
            f"(have: {sorted(agents.keys())[:5]}...)")
    return agent_id, project, agents


def _build_agent_definition(AgentDefinition, agent_def: dict):
    """Translate a yakOS composed-agent entry into a SDK AgentDefinition.

    yakOS agent shape (post-compose):
      {
        "prompt": "...full agent body...",
        "tools": ["Read", "Edit", ...],
        "model": "sonnet" | "opus" | "haiku" | full-model-id,
        "description": "one-line description"
      }
    Fields beyond description+prompt are optional.
    """
    kwargs = {
        "description": agent_def.get("description")
            or f"yakOS-composed agent",
        "prompt": agent_def.get("prompt", ""),
    }
    tools = agent_def.get("tools")
    if isinstance(tools, list) and tools:
        kwargs["tools"] = tools
    model = agent_def.get("model")
    if isinstance(model, str) and model:
        kwargs["model"] = model
    return AgentDefinition(**kwargs)


def _extract_usage(msg) -> dict:
    """Pull cost + telemetry from a ResultMessage.

    Documented (claude-agent-sdk types.py): total_cost_usd,
    duration_ms, duration_api_ms, num_turns, session_id,
    stop_reason, usage, model_usage, permission_denials.
    """
    data = {}
    for attr in ("total_cost_usd", "duration_ms", "duration_api_ms",
                 "num_turns", "session_id", "stop_reason",
                 "usage", "model_usage", "permission_denials",
                 "is_error", "subtype"):
        if hasattr(msg, attr):
            v = getattr(msg, attr)
            if v is not None:
                data[attr] = v
    return data


async def run(agent_id: str, project: str, agents: dict, task: str) -> int:
    try:
        from claude_agent_sdk import (
            AgentDefinition,
            AssistantMessage,
            ClaudeAgentOptions,
            ResultMessage,
            TextBlock,
            query,
        )
    except ImportError as exc:
        die(f"claude-agent-sdk not importable: {exc}. "
            f"Install: pip install claude-agent-sdk (python 3.10+)")

    # The dispatched agent runs as the primary system_prompt — that's
    # yakOS's headless-dispatch semantic. Other composed agents are
    # made AVAILABLE as sub-agents via options.agents so the dispatched
    # agent can delegate via Task if its prompt expects to.
    dispatched = agents[agent_id]

    options_kwargs = {
        "cwd": project,
        "system_prompt": dispatched.get("prompt", ""),
    }
    tools = dispatched.get("tools")
    if isinstance(tools, list) and tools:
        # tools= sets the BASE set the agent can call; this is the
        # right knob for yakOS frontmatter `tools:` restrictions.
        options_kwargs["tools"] = tools
    model = dispatched.get("model")
    if isinstance(model, str) and model:
        options_kwargs["model"] = model

    # Multi-turn (v0.31+): if YAKOS_CONVERSATION_ID is set, resume that
    # SDK session. The Claude Agent SDK accepts a `resume` option that
    # takes a session_id. If the upstream SDK rejects the kwarg (older
    # version), we catch + fall back to fresh.
    resume_id = os.environ.get("YAKOS_CONVERSATION_ID")
    if resume_id:
        options_kwargs["resume"] = resume_id

    # Sub-agent availability: surface every OTHER agent in the
    # composition. If the composition has only the dispatched agent,
    # skip — empty agents= dict is fine but a one-item dict containing
    # only the same agent the SDK is already running is confusing.
    sibling_defs = {
        name: _build_agent_definition(AgentDefinition, defn)
        for name, defn in agents.items()
        if name != agent_id
    }
    if sibling_defs:
        options_kwargs["agents"] = sibling_defs

    try:
        options = ClaudeAgentOptions(**options_kwargs)
    except TypeError as exc:
        # The `resume` field may not be supported on older SDK versions.
        # Retry without it and warn.
        if "resume" in str(exc) and "resume" in options_kwargs:
            sys.stderr.write(
                f"claude-sdk-dispatch: resume not supported by installed "
                f"claude-agent-sdk; starting fresh ({exc})\n"
            )
            del options_kwargs["resume"]
            options = ClaudeAgentOptions(**options_kwargs)
        else:
            raise

    text_chunks = []
    usage_data = None

    async for msg in query(prompt=task, options=options):
        if isinstance(msg, AssistantMessage):
            for block in msg.content:
                if isinstance(block, TextBlock):
                    text_chunks.append(block.text)
        elif isinstance(msg, ResultMessage):
            usage_data = _extract_usage(msg)

    sys.stdout.write("".join(text_chunks))
    sys.stdout.flush()

    usage_out = os.environ.get("YAKOS_USAGE_OUT")
    if usage_out:
        try:
            with open(usage_out, "w") as f:
                json.dump(usage_data or {"source": "claude-sdk-no-result-message"},
                          f, default=str)
        except OSError as exc:
            sys.stderr.write(
                f"claude-sdk-dispatch: could not write usage to "
                f"{usage_out}: {exc}\n"
            )

    # Capture session_id for multi-turn resume (v0.31+). ResultMessage
    # carries the session id used by this run; mcp-server stashes it for
    # subsequent continue_claude_sdk calls.
    session_out = os.environ.get("YAKOS_SESSION_OUT")
    if session_out and usage_data and usage_data.get("session_id"):
        try:
            with open(session_out, "w") as f:
                f.write(str(usage_data["session_id"]))
        except OSError as exc:
            sys.stderr.write(
                f"claude-sdk-dispatch: could not write session id to "
                f"{session_out}: {exc}\n"
            )

    return 0


def main() -> int:
    # K-137 hard gate, before anything else: no inputs read, no SDK imported.
    state = startup_check(os.environ)
    if state == "unset":
        die(_MSG_UNSET, EXIT_API_KEY_REQUIRED)
    if state == "oauth":
        die(_MSG_SUBSCRIPTION, EXIT_API_KEY_REQUIRED)
    scrub_oauth_env(os.environ)

    agent_id, project, agents = read_env()
    task = sys.stdin.read()
    if not task:
        die("empty task on stdin")

    try:
        import anyio
    except ImportError as exc:
        die(f"anyio not importable: {exc}. anyio is a dependency of "
            f"claude-agent-sdk; reinstall the SDK to pull it in")

    try:
        anyio.run(run, agent_id, project, agents, task)
    except Exception as exc:
        die(f"agent invocation failed: {type(exc).__name__}: {exc}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
