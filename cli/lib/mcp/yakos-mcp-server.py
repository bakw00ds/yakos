#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""
yakos-mcp-server.py — Model Context Protocol server exposing cross-runtime
yakOS dispatches as native tools to a Claude Code session.

Lets a Claude Code session call codex / agy / SDK agents as structured
tool calls instead of shelling out via Bash. The session sees:

  dispatch_codex(agent: str, task: str) -> response text + conversation_id
  dispatch_agy(...)
  dispatch_claude_sdk(...)
  dispatch_antigravity_sdk(...)

And for multi-turn:

  continue_codex(conversation_id: str, task: str) -> response text
  continue_agy(...)
  continue_claude_sdk(...)
  (continue_antigravity_sdk is intentionally absent — see note below)

Each tool shells `yakos dispatch <agent> '<task>' --runtime <id>` under
the hood. Multi-turn continues honor YAKOS_CONVERSATION_ID env var which
the runtime adapters interpret per-runtime (codex resume, agy
--conversation, claude --resume, claude-sdk session resume).

State at ~/.yakos-state/mcp-conversations.json: yakOS conversation_id →
{runtime, agent, native_id, created_at, last_active_at}.

Dependencies (operator-installed):
  pip install mcp           # Anthropic's MCP Python SDK
  pip install anyio         # transitive

Wired via .mcp.json:
  {"mcpServers": {"yakos-dispatch": {
    "command": "python3",
    "args": ["<absolute path to this file>"]
  }}}

Or use `yakos mcp install` which writes the right entry for you.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Any

# MCP SDK — imported lazily so a helpful error message can be raised if
# the operator hasn't installed it.
try:
    from mcp.server import Server
    from mcp.server.stdio import stdio_server
    import mcp.types as mcp_types
except ImportError as exc:
    sys.stderr.write(
        f"yakos-mcp-server: 'mcp' package not installed: {exc}\n"
        f"Install it with: pip install mcp\n"
        f"(this is the Anthropic Model Context Protocol Python SDK)\n"
    )
    sys.exit(1)

import asyncio


# ---------------------------------------------------------------------------
# Conversation state
# ---------------------------------------------------------------------------

STATE_DIR = Path(os.environ.get("YAKOS_STATE_DIR",
                                str(Path.home() / ".yakos-state")))
STATE_FILE = STATE_DIR / "mcp-conversations.json"
STATE_DIR.mkdir(parents=True, exist_ok=True)


def _load_state() -> dict:
    if not STATE_FILE.exists():
        return {"conversations": {}}
    try:
        return json.loads(STATE_FILE.read_text())
    except (OSError, json.JSONDecodeError):
        return {"conversations": {}}


def _save_state(state: dict) -> None:
    tmp = STATE_FILE.with_suffix(".json.tmp")
    try:
        tmp.write_text(json.dumps(state, indent=2))
        tmp.replace(STATE_FILE)
    except OSError as exc:
        sys.stderr.write(f"yakos-mcp-server: could not save state: {exc}\n")


def _record_conversation(yakos_id: str, runtime: str, agent: str,
                         native_id: str | None) -> None:
    state = _load_state()
    now = int(time.time())
    state["conversations"][yakos_id] = {
        "runtime": runtime,
        "agent": agent,
        "native_id": native_id,
        "created_at": now,
        "last_active_at": now,
    }
    _save_state(state)


def _lookup_conversation(yakos_id: str) -> dict | None:
    state = _load_state()
    return state["conversations"].get(yakos_id)


def _touch_conversation(yakos_id: str) -> None:
    state = _load_state()
    if yakos_id in state["conversations"]:
        state["conversations"][yakos_id]["last_active_at"] = int(time.time())
        _save_state(state)


# ---------------------------------------------------------------------------
# Dispatch helpers
# ---------------------------------------------------------------------------

def _find_yakos_cli() -> str:
    """Locate the yakos CLI binary.

    Search order:
      1. $YAKOS_CLI env var (explicit override)
      2. $YAKOS_ROOT/cli/yakos (set by the parent shell that started us)
      3. PATH (operator put yakos on PATH after install)
    """
    explicit = os.environ.get("YAKOS_CLI")
    if explicit and Path(explicit).is_file():
        return explicit
    root = os.environ.get("YAKOS_ROOT")
    if root:
        candidate = Path(root) / "cli" / "yakos"
        if candidate.is_file():
            return str(candidate)
    import shutil
    found = shutil.which("yakos")
    if found:
        return found
    raise RuntimeError(
        "yakos-mcp-server: could not locate the 'yakos' CLI. "
        "Set YAKOS_CLI or YAKOS_ROOT, or put yakos on PATH."
    )


def _run_dispatch(runtime: str, agent: str, task: str,
                  conversation_native_id: str | None = None,
                  project_dir: str | None = None) -> dict:
    """Shell out to `yakos dispatch` and return result dict.

    Returns:
      {ok, response, native_session_id, duration_ms, stderr}
    """
    yakos = _find_yakos_cli()
    env = os.environ.copy()
    if conversation_native_id:
        env["YAKOS_CONVERSATION_ID"] = conversation_native_id
    # Capture a usage file so we can read telemetry back.
    usage_out = STATE_DIR / f"mcp-usage-{uuid.uuid4().hex}.json"
    env["YAKOS_USAGE_OUT"] = str(usage_out)
    # Capture native session id (codex/claude/etc) so we can resume later.
    session_out = STATE_DIR / f"mcp-session-{uuid.uuid4().hex}.txt"
    env["YAKOS_SESSION_OUT"] = str(session_out)

    cmd = [yakos, "dispatch", agent, task, "--runtime", runtime]
    if project_dir:
        cmd.extend(["--project", project_dir])

    started = time.time()
    proc = subprocess.run(cmd, env=env, capture_output=True, text=True,
                          timeout=600)
    duration_ms = int((time.time() - started) * 1000)

    native_id = None
    if session_out.exists():
        try:
            native_id = session_out.read_text().strip() or None
        except OSError:
            pass
        try:
            session_out.unlink()
        except OSError:
            pass
    usage = {}
    if usage_out.exists():
        try:
            usage = json.loads(usage_out.read_text())
        except (OSError, json.JSONDecodeError):
            pass
        try:
            usage_out.unlink()
        except OSError:
            pass

    return {
        "ok": proc.returncode == 0,
        "rc": proc.returncode,
        "response": proc.stdout,
        "stderr": proc.stderr,
        "native_session_id": native_id,
        "duration_ms": duration_ms,
        "usage": usage,
    }


# ---------------------------------------------------------------------------
# MCP server: tool definitions
# ---------------------------------------------------------------------------

server = Server("yakos-dispatch")

# Per-runtime config: (runtime_id, display_name, supports_resume,
# resume_note).
RUNTIMES = [
    ("codex", "OpenAI Codex", True,
     "Uses 'codex resume <session_id>'."),
    ("agy", "Google Antigravity (CLI)", True,
     "Uses '--conversation <uuid>'."),
    ("claude-sdk", "Claude Agent SDK (Python)", True,
     "Uses options.resume_session_id."),
    ("antigravity-sdk", "Antigravity SDK (Python)", False,
     "SDK has no documented cross-process session resume at v0.31; "
     "each dispatch starts fresh. Use 'agy' for multi-turn against Gemini."),
    ("claude", "Claude Code CLI (peer session)", True,
     "Uses '--resume <session_id>'. Most useful when you want a "
     "second claude session for parallel work alongside this one."),
]


@server.list_tools()
async def list_tools() -> list[mcp_types.Tool]:
    tools = []
    for rt_id, display, supports_resume, _ in RUNTIMES:
        # One-shot dispatch tool
        tools.append(mcp_types.Tool(
            name=f"dispatch_{rt_id.replace('-', '_')}",
            description=(
                f"Dispatch a yakOS agent to {display} (runtime '{rt_id}'). "
                f"Starts a fresh conversation. Returns response text + a "
                f"yakos conversation_id you can pass to "
                f"continue_{rt_id.replace('-', '_')} for multi-turn "
                f"(if supported by the runtime)."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "agent": {
                        "type": "string",
                        "description": "yakOS agent name (e.g. 'backend-pro', "
                                       "'code-reviewer'). Must exist in the "
                                       "framework or project agent inventory."
                    },
                    "task": {
                        "type": "string",
                        "description": "Task prompt for the agent (full text; "
                                       "framework will prepend system_prompt)."
                    },
                    "project_dir": {
                        "type": "string",
                        "description": "Optional: absolute path to a yakOS-"
                                       "bootstrapped project. Defaults to the "
                                       "MCP server's cwd (the project that "
                                       "started this session)."
                    },
                },
                "required": ["agent", "task"]
            }
        ))
        # Multi-turn continue tool (only for supported runtimes)
        if supports_resume:
            tools.append(mcp_types.Tool(
                name=f"continue_{rt_id.replace('-', '_')}",
                description=(
                    f"Continue an existing {display} conversation by "
                    f"conversation_id. The conversation_id comes from a "
                    f"previous dispatch_{rt_id.replace('-', '_')} call."
                ),
                inputSchema={
                    "type": "object",
                    "properties": {
                        "conversation_id": {
                            "type": "string",
                            "description": "yakOS conversation_id from a "
                                           "previous dispatch."
                        },
                        "task": {
                            "type": "string",
                            "description": "Next turn's prompt."
                        },
                    },
                    "required": ["conversation_id", "task"]
                }
            ))

    return tools


@server.call_tool()
async def call_tool(name: str, arguments: dict[str, Any]) -> list[Any]:
    # dispatch_* — fresh conversation
    if name.startswith("dispatch_"):
        rt_id = name[len("dispatch_"):].replace("_", "-")
        agent = arguments.get("agent")
        task = arguments.get("task")
        project_dir = arguments.get("project_dir")
        if not agent or not task:
            return [mcp_types.TextContent(
                type="text", text="error: 'agent' and 'task' are required"
            )]

        result = _run_dispatch(rt_id, agent, task, project_dir=project_dir)
        if not result["ok"]:
            return [mcp_types.TextContent(
                type="text",
                text=f"dispatch_{rt_id} failed (rc={result['rc']}):\n"
                     f"{result['stderr']}\n--- stdout ---\n{result['response']}"
            )]

        yakos_conv_id = uuid.uuid4().hex
        _record_conversation(yakos_conv_id, rt_id, agent,
                             result["native_session_id"])

        # Format response + metadata
        meta = {
            "conversation_id": yakos_conv_id,
            "runtime": rt_id,
            "agent": agent,
            "duration_ms": result["duration_ms"],
            "usage": result["usage"],
        }
        return [
            mcp_types.TextContent(type="text", text=result["response"]),
            mcp_types.TextContent(
                type="text",
                text=f"\n[yakos] {json.dumps(meta)}"
            ),
        ]

    # continue_* — resume by conversation_id
    if name.startswith("continue_"):
        rt_id = name[len("continue_"):].replace("_", "-")
        yakos_conv_id = arguments.get("conversation_id")
        task = arguments.get("task")
        if not yakos_conv_id or not task:
            return [mcp_types.TextContent(
                type="text",
                text="error: 'conversation_id' and 'task' are required"
            )]

        conv = _lookup_conversation(yakos_conv_id)
        if not conv:
            return [mcp_types.TextContent(
                type="text",
                text=f"error: unknown conversation_id '{yakos_conv_id}'. "
                     f"Did the conversation expire or get cleaned up?"
            )]
        if conv["runtime"] != rt_id:
            return [mcp_types.TextContent(
                type="text",
                text=f"error: conversation '{yakos_conv_id}' is on runtime "
                     f"'{conv['runtime']}', not '{rt_id}'. "
                     f"Call continue_{conv['runtime'].replace('-', '_')} "
                     f"instead."
            )]
        if not conv.get("native_id"):
            return [mcp_types.TextContent(
                type="text",
                text=f"error: conversation '{yakos_conv_id}' has no native "
                     f"resume id — the runtime's previous dispatch did not "
                     f"emit one. Multi-turn isn't possible for this thread; "
                     f"start a new dispatch."
            )]

        result = _run_dispatch(conv["runtime"], conv["agent"], task,
                               conversation_native_id=conv["native_id"])
        if not result["ok"]:
            return [mcp_types.TextContent(
                type="text",
                text=f"continue_{rt_id} failed (rc={result['rc']}):\n"
                     f"{result['stderr']}\n--- stdout ---\n{result['response']}"
            )]

        # Update last_active_at + native_id if the runtime emitted a new one
        _touch_conversation(yakos_conv_id)
        if result["native_session_id"] and \
                result["native_session_id"] != conv["native_id"]:
            state = _load_state()
            state["conversations"][yakos_conv_id]["native_id"] = \
                result["native_session_id"]
            _save_state(state)

        meta = {
            "conversation_id": yakos_conv_id,
            "runtime": rt_id,
            "agent": conv["agent"],
            "duration_ms": result["duration_ms"],
            "usage": result["usage"],
        }
        return [
            mcp_types.TextContent(type="text", text=result["response"]),
            mcp_types.TextContent(
                type="text",
                text=f"\n[yakos] {json.dumps(meta)}"
            ),
        ]

    return [mcp_types.TextContent(
        type="text", text=f"error: unknown tool '{name}'"
    )]


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

async def main() -> None:
    async with stdio_server() as (read, write):
        await server.run(read, write, server.create_initialization_options())


if __name__ == "__main__":
    asyncio.run(main())
