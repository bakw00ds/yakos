#!/usr/bin/env python3
"""
antigravity-sdk-dispatch.py — invoke Google Antigravity SDK for yakOS.

Invoked by cli/lib/runtimes/antigravity-sdk.sh::yk_rt_antigravity_sdk_dispatch.

Inputs (env vars):
  YAKOS_AGENT_ID      — agent id (key in the composed agents JSON)
  YAKOS_PROJECT_DIR   — project repo path (script chdir's here before Agent)
  YAKOS_AGENTS_JSON   — composed agents JSON (output of yk_agents_compose)
  YAKOS_USAGE_OUT     — optional path; if set, write usage JSON here
  GEMINI_API_KEY      — Antigravity SDK auth (passed through; not consumed here)

Inputs (stdin):
  task prompt (UTF-8 text)

Outputs:
  stdout: agent response text (streamed)
  stderr: errors / log lines
  exit 0 on success, non-zero on error

Notes vs claude-sdk-dispatch.py:
- Uses asyncio directly (not anyio).
- Agent is a context manager: `async with Agent(config) as agent`.
- system_instructions (plural) — not system_prompt.
- SDK is read-only by default; we pass CapabilitiesConfig() to enable
  writes. Per-tool allowlisting is left to yakOS hooks since the SDK's
  tool namespace (view_file/run_command/...) doesn't map 1:1 to yakOS
  allowed_tools (Edit/Read/Write/Bash).
- No cwd= config parameter visible in README; we os.chdir() before
  constructing Agent to match agy CLI's --add-dir semantics.
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
    return agent_id, project, agents[agent_id]


async def run(agent_id: str, project: str, agent_def: dict, task: str) -> int:
    try:
        from google.antigravity import (
            Agent,
            LocalAgentConfig,
            CapabilitiesConfig,
        )
    except ImportError as exc:
        die(f"google-antigravity not importable: {exc}. "
            f"Install: pip install google-antigravity (compiled binary "
            f"ships in the PyPI wheel; clone-only is insufficient)")

    # Match agy CLI's --add-dir semantics by chdir'ing to the project root
    # before constructing Agent. README doesn't document a cwd= config; this
    # is the closest equivalent.
    os.chdir(project)

    # Build LocalAgentConfig. Capabilities enabled (override default
    # read-only); rely on yakOS path-allowlist hooks for write gating.
    config_kwargs = {
        "system_instructions": agent_def.get("prompt", ""),
        "capabilities": CapabilitiesConfig(),
    }
    # api_key not set here — SDK reads GEMINI_API_KEY from env automatically.

    config = LocalAgentConfig(**config_kwargs)

    # Stream tokens to stdout in real-time, accumulating none in memory.
    # Per README: `async for token in response` yields str text tokens.
    usage_data = None
    async with Agent(config) as agent:
        response = await agent.chat(task)
        async for token in response:
            sys.stdout.write(token)
            sys.stdout.flush()
        sys.stdout.write("\n")

        # Probe the response for usage data. README is silent on the
        # surface; check for known-likely attributes so we capture
        # whatever the SDK does expose.
        usage_data = {}
        for attr in ("usage", "input_tokens", "output_tokens",
                     "duration_ms", "total_cost_usd"):
            if hasattr(response, attr):
                usage_data[attr] = getattr(response, attr)

    # Write usage telemetry if available + requested.
    usage_out = os.environ.get("YAKOS_USAGE_OUT")
    if usage_out and usage_data:
        try:
            with open(usage_out, "w") as f:
                json.dump(usage_data, f, default=str)
        except OSError as exc:
            sys.stderr.write(
                f"antigravity-sdk-dispatch: could not write usage to "
                f"{usage_out}: {exc}\n"
            )
    elif usage_out:
        try:
            with open(usage_out, "w") as f:
                json.dump({"source": "antigravity-sdk-no-usage-surface"}, f)
        except OSError:
            pass

    return 0


def main() -> int:
    agent_id, project, agent_def = read_env()
    task = sys.stdin.read()
    if not task:
        die("empty task on stdin")

    try:
        import asyncio
    except ImportError as exc:
        die(f"asyncio not importable: {exc} (stdlib; python install broken)")

    try:
        asyncio.run(run(agent_id, project, agent_def, task))
    except Exception as exc:
        die(f"agent invocation failed: {type(exc).__name__}: {exc}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
