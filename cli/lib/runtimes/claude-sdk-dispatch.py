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

The Claude Agent SDK is async-only (uses anyio). This script wraps a
single query() invocation; for bidirectional / multi-turn use cases the
adapter should be extended to use ClaudeSDKClient.
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
    return agent_id, project, agents[agent_id]


async def run(agent_id: str, project: str, agent_def: dict, task: str) -> int:
    # Import inside async run() so import failures surface as die() not
    # as a Python startup error.
    try:
        from claude_agent_sdk import (
            query,
            ClaudeAgentOptions,
            AssistantMessage,
            ResultMessage,
            TextBlock,
        )
    except ImportError as exc:
        die(f"claude-agent-sdk not importable: {exc}. "
            f"Install: pip install claude-agent-sdk (python 3.10+)")

    # Build ClaudeAgentOptions from the composed agent JSON.
    # README-confirmed options: system_prompt, allowed_tools,
    # disallowed_tools, cwd, mcp_servers, cli_path.
    # Note: README does NOT document a model parameter; the SDK uses the
    # bundled CLI's default. yakOS's per-agent model: frontmatter is
    # recorded as intent but does not pass through to the SDK at v0.24.
    options_kwargs = {
        "cwd": project,
        "system_prompt": agent_def.get("prompt", ""),
    }
    tools = agent_def.get("tools")
    if tools and isinstance(tools, list) and tools:
        options_kwargs["allowed_tools"] = tools

    options = ClaudeAgentOptions(**options_kwargs)

    # Accumulate assistant text + (if surfaced) usage from ResultMessage.
    text_chunks = []
    usage_data = None

    async for msg in query(prompt=task, options=options):
        if isinstance(msg, AssistantMessage):
            for block in msg.content:
                if isinstance(block, TextBlock):
                    text_chunks.append(block.text)
        elif isinstance(msg, ResultMessage):
            # README doesn't document ResultMessage fields explicitly;
            # probe for known-likely attribute names. If absent we fall
            # back to byte-estimate (handled by the bash wrapper or
            # YAKOS_USAGE_OUT consumer).
            usage_data = {}
            for attr in ("usage", "input_tokens", "output_tokens",
                         "duration_ms", "total_cost_usd"):
                if hasattr(msg, attr):
                    usage_data[attr] = getattr(msg, attr)
            # If `.usage` is itself a nested object, flatten common fields.
            if "usage" in usage_data and not isinstance(usage_data["usage"],
                                                       (int, float, str)):
                usage_obj = usage_data.pop("usage")
                for attr in ("input_tokens", "output_tokens",
                             "cache_read_input_tokens",
                             "cache_creation_input_tokens"):
                    if hasattr(usage_obj, attr):
                        usage_data[attr] = getattr(usage_obj, attr)

    # Write assistant text to stdout.
    sys.stdout.write("".join(text_chunks))
    sys.stdout.flush()

    # Write usage telemetry if available + requested.
    usage_out = os.environ.get("YAKOS_USAGE_OUT")
    if usage_out and usage_data:
        try:
            with open(usage_out, "w") as f:
                json.dump(usage_data, f)
        except OSError as exc:
            sys.stderr.write(
                f"claude-sdk-dispatch: could not write usage to "
                f"{usage_out}: {exc}\n"
            )
    elif usage_out:
        # Best-effort: write empty marker so the bash wrapper knows we
        # tried but had no data.
        try:
            with open(usage_out, "w") as f:
                json.dump({"source": "claude-sdk-no-result-message"}, f)
        except OSError:
            pass

    return 0


def main() -> int:
    agent_id, project, agent_def = read_env()
    task = sys.stdin.read()
    if not task:
        die("empty task on stdin")

    # anyio.run is the SDK's documented entry point per the README example.
    try:
        import anyio
    except ImportError as exc:
        die(f"anyio not importable: {exc}. anyio is a dependency of "
            f"claude-agent-sdk; reinstall the SDK to pull it in")

    try:
        anyio.run(run, agent_id, project, agent_def, task)
    except Exception as exc:
        die(f"agent invocation failed: {type(exc).__name__}: {exc}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
