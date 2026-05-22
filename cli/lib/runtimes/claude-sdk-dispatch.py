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
  exit 0 on success, non-zero on error

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


def die(msg, code=1):
    sys.stderr.write(f"claude-sdk-dispatch: {msg}\n")
    sys.exit(code)


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

    options = ClaudeAgentOptions(**options_kwargs)

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

    return 0


def main() -> int:
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
