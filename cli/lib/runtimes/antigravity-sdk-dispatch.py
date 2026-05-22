#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""
antigravity-sdk-dispatch.py — invoke Google Antigravity SDK for yakOS.

Invoked by cli/lib/runtimes/antigravity-sdk.sh::yk_rt_antigravity_sdk_dispatch.

Inputs (env vars):
  YAKOS_AGENT_ID      — agent id (key in the composed agents JSON)
  YAKOS_PROJECT_DIR   — project repo path (script chdir's here before Agent)
  YAKOS_AGENTS_JSON   — composed agents JSON (output of yk_agents_compose)
  YAKOS_USAGE_OUT     — optional path; if set, write usage JSON here
  GEMINI_API_KEY      — Antigravity SDK auth (passed through; SDK reads directly)

Inputs (stdin):
  task prompt (UTF-8 text)

Outputs:
  stdout: agent response text (streamed)
  stderr: errors / log lines
  exit 0 on success, non-zero on error

v0.26 (Plan 5 M3) — verified against examples/getting_started/
{subagents.py,observability.py,persona_config.py}. Real surface used:

  - Sub-agents via CapabilitiesConfig(enable_subagents=True); agent
    autonomously spawns subagents via the BuiltinTools.START_SUBAGENT
    tool when its prompt warrants delegation. yakOS does NOT pre-bind
    teammate definitions (the Antigravity SDK has no equivalent of
    Claude's options.agents={} dict; subagents are dispatched ad-hoc).
  - Token usage via agent.conversation.total_usage (prompt_token_count,
    response_token_count, total_token_count, thoughts_token_count).
  - system_instructions via LocalAgentConfig(system_instructions=...);
    this is a TEMPLATED override (preserves SDK scaffolding for
    environmental context, skills, subagent rules).
"""

import json
import os
import sys


def die(msg, code=1):
    sys.stderr.write(f"antigravity-sdk-dispatch: {msg}\n")
    sys.exit(code)


def read_env():
    agent_id = os.environ.get("YAKOS_AGENT_ID")
    if not agent_id:
        die("YAKOS_AGENT_ID env var required")
    project = os.environ.get("YAKOS_PROJECT_DIR")
    if not project:
        die("YAKOS_PROJECT_DIR env var required")
    if not os.path.isdir(project):
        die(f"YAKOS_PROJECT_DIR not a directory: {project}")
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
    return agent_id, project, agents[agent_id], agents


def _serialize_usage(usage) -> dict:
    """Convert an Antigravity TotalUsage object into a plain dict.

    Per examples/getting_started/observability.py, the object exposes
    prompt_token_count, response_token_count, total_token_count, and
    thoughts_token_count. Use hasattr to be tolerant of API drift.
    """
    if usage is None:
        return {}
    out = {}
    for attr in ("prompt_token_count", "response_token_count",
                 "total_token_count", "thoughts_token_count",
                 "cache_read_token_count", "cache_creation_token_count"):
        if hasattr(usage, attr):
            v = getattr(usage, attr)
            if v is not None:
                out[attr] = v
    return out


async def run(agent_id: str, project: str, agent_def: dict,
              all_agents: dict, task: str) -> int:
    try:
        from google.antigravity import (
            Agent,
            LocalAgentConfig,
        )
        from google.antigravity import types as ag_types
    except ImportError as exc:
        die(f"google-antigravity not importable: {exc}. "
            f"Install: pip install google-antigravity (compiled binary "
            f"ships in the PyPI wheel; clone-only is insufficient)")

    # Match agy CLI's --add-dir semantics by chdir'ing to the project
    # root before constructing Agent. The SDK has no documented cwd=
    # parameter on LocalAgentConfig.
    os.chdir(project)

    # CapabilitiesConfig knobs (per examples/getting_started/subagents.py):
    #   - enable_subagents=True lets the agent autonomously spawn
    #     subagents via BuiltinTools.START_SUBAGENT
    # The unconditional CapabilitiesConfig() in v0.25 enabled writes
    # but NOT subagents (subagents are off by default even when caps
    # are enabled, per the example needing the explicit flag).
    enable_subagents = len(all_agents) > 1
    capabilities = ag_types.CapabilitiesConfig(
        enable_subagents=enable_subagents,
    )

    config = LocalAgentConfig(
        system_instructions=agent_def.get("prompt", ""),
        capabilities=capabilities,
    )

    usage_data = {}
    async with Agent(config) as agent:
        response = await agent.chat(task)
        async for token in response:
            sys.stdout.write(token)
            sys.stdout.flush()
        sys.stdout.write("\n")

        # Capture token usage via the Conversation API (Layer 2).
        # agent.conversation.total_usage is the documented surface per
        # examples/getting_started/observability.py.
        if hasattr(agent, "conversation"):
            usage_data = _serialize_usage(
                getattr(agent.conversation, "total_usage", None)
            )
            if hasattr(agent.conversation, "turn_count"):
                usage_data["turn_count"] = agent.conversation.turn_count

    usage_out = os.environ.get("YAKOS_USAGE_OUT")
    if usage_out:
        try:
            with open(usage_out, "w") as f:
                json.dump(usage_data or {"source": "antigravity-sdk-no-usage"},
                          f, default=str)
        except OSError as exc:
            sys.stderr.write(
                f"antigravity-sdk-dispatch: could not write usage to "
                f"{usage_out}: {exc}\n"
            )

    return 0


def main() -> int:
    agent_id, project, agent_def, all_agents = read_env()
    task = sys.stdin.read()
    if not task:
        die("empty task on stdin")

    try:
        import asyncio
    except ImportError as exc:
        die(f"asyncio not importable: {exc} (stdlib; python install broken)")

    try:
        asyncio.run(run(agent_id, project, agent_def, all_agents, task))
    except Exception as exc:
        die(f"agent invocation failed: {type(exc).__name__}: {exc}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
