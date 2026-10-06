/**
 * sidecar.mjs — Node.js Agent SDK sidecar for yakOS interactive sessions.
 *
 * Protocol (NDJSON over stdin/stdout; stderr = logs only):
 *
 * IN (stdin):
 *   {"v":1,"kind":"user_turn","text":"..."}
 *   {"v":1,"kind":"answer","toolUseId":"...","answers":{...},"response":"...","annotations":{...}}
 *   {"v":1,"kind":"shutdown"}
 *
 * OUT (stdout):
 *   {"v":1,"kind":"ready"}                          — after init
 *   {"v":1,"kind":"token","text":"..."}
 *   {"v":1,"kind":"thinking","text":"...","truncated":false,"redacted":false}
 *   {"v":1,"kind":"tool_use","toolName":"...","toolInput":"..."}
 *   {"v":1,"kind":"tool_result","toolName":"...","toolOutput":"...","isError":false}
 *   {"v":1,"kind":"ask_user_question","toolUseId":"...","questions":[...]}
 *   {"v":1,"kind":"summary","totalCostUsd":0.0,"usage":{...}}
 *   {"v":1,"kind":"error","text":"..."}
 *
 * Auth (K-137): ANTHROPIC_API_KEY is REQUIRED. Anthropic does not allow a Pro/Max
 * subscription's OAuth in the Agent SDK (terms of 2026-02-19), and without a key
 * the SDK would fall back to the operator's claude.ai login. main() therefore
 * refuses to start, before it announces "ready", unless ANTHROPIC_API_KEY holds
 * an API key: exit status 78 and a one-line reason on stderr that never contains
 * any part of a credential. The Go side (SDKEngine.Start) refuses first and also
 * strips subscription OAuth variables from this process's environment; this is
 * the second anchor for a sidecar launched any other way, and it strips the same
 * variables from process.env itself (CLAUDE_CODE_OAUTH* names in any case, and any
 * value holding an OAuth token) before the SDK copies it. `--check-env` prints the
 * variable names that remain and exits, for the tests. The CLI engine is the
 * interactive path for subscription users.
 *
 * AskUserQuestion flow:
 *   1. canUseTool callback receives tool_use for "AskUserQuestion".
 *   2. Sidecar emits ask_user_question line; parks a promise keyed by toolUseId.
 *   3. Go side sends {"v":1,"kind":"answer","toolUseId":"...","answers":{...},...}
 *   4. Sidecar resolves the promise with the exact canUseTool allow shape.
 *   5. Model continues the turn.
 */

import { query } from "@anthropic-ai/claude-agent-sdk";
import * as readline from "node:readline";

// ---------------------------------------------------------------------------
// NDJSON write helpers
// ---------------------------------------------------------------------------

/** Write one NDJSON line to stdout (the Go process reads this). */
function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

/** Write a log line to stderr (never to stdout — Go reads stdout). */
function log(msg, ...args) {
  process.stderr.write(`[sidecar] ${msg} ${args.map(String).join(" ")}\n`);
}

// ---------------------------------------------------------------------------
// Tool name registry — correlate tool_use ids with tool names for tool_result
// ---------------------------------------------------------------------------

/**
 * toolUseIdToName maps tool-use block ids to their tool name so that the
 * tool_result frame can carry a populated toolName field.  This mirrors the
 * toolIDToName map in the Go CLI engine path (dispatch/streamhelper.go).
 * Entries are added when an assistant tool_use block is seen and removed once
 * the corresponding tool_result is emitted.
 */
const toolUseIdToName = new Map();

// ---------------------------------------------------------------------------
// Pending question registry (one at a time per spec)
// ---------------------------------------------------------------------------

/** toolUseId → { resolve, reject } for parked AskUserQuestion promises. */
const pendingQuestions = new Map();

/**
 * Park an AskUserQuestion tool call.
 * Returns a promise that resolves with the exact canUseTool "allow" shape
 * once Go sends the matching "answer" frame.
 */
function parkQuestion(toolUseId, questions) {
  return new Promise((resolve, reject) => {
    pendingQuestions.set(toolUseId, { resolve, reject });
    emit({
      v: 1,
      kind: "ask_user_question",
      toolUseId,
      questions,
    });
  });
}

/**
 * Deliver an answer from Go for a pending question.
 * Resolves the parked promise with the exact canUseTool allow shape.
 */
function deliverAnswer(frame) {
  const { toolUseId, answers, response, annotations } = frame;
  const pending = pendingQuestions.get(toolUseId);
  if (!pending) {
    log("warn: received answer for unknown toolUseId", toolUseId);
    return;
  }
  pendingQuestions.delete(toolUseId);

  // Reconstruct the AskUserQuestion input for the canUseTool response.
  // The Go side sends back the answers map and optional response/annotations.
  // frame.questions may be null/absent when the Go caller did not echo them
  // back (e.g. older client); fall back to [] so the SDK always gets a valid
  // array rather than null, which would fail schema validation.
  const updatedInput = { questions: frame.questions || [], answers: answers || {} };
  if (response !== undefined) updatedInput.response = response;
  if (annotations !== undefined) updatedInput.annotations = annotations;

  pending.resolve({
    behavior: "allow",
    updatedInput,
  });
}

// ---------------------------------------------------------------------------
// canUseTool callback
// ---------------------------------------------------------------------------

/**
 * canUseTool is called by the SDK before each tool execution.
 * - AskUserQuestion: park a promise; emit ask_user_question; wait for answer.
 * - All other tools: allow immediately.
 */
async function canUseTool(tool) {
  if (tool.name === "AskUserQuestion") {
    const input = tool.input;
    const questions = input.questions || [];
    const toolUseId = tool.id || `ask-${Date.now()}`;
    try {
      return await parkQuestion(toolUseId, questions);
    } catch (err) {
      log("error: AskUserQuestion promise rejected", err);
      return { behavior: "allow", updatedInput: tool.input };
    }
  }
  // All other tools: allow without modification.
  return { behavior: "allow" };
}

// ---------------------------------------------------------------------------
// Prompt generator (async generator kept open across turns)
// ---------------------------------------------------------------------------

/**
 * promptController holds the yield function so Go can inject user turns.
 *
 * The SDK is driven with a STREAMING AsyncIterable<SDKUserMessage>.
 * We keep the generator open indefinitely; each SendUserTurn call pushes
 * a new message into it.  Shutdown closes the generator.
 */
let promptController = null;

/**
 * createPromptGenerator returns an async generator that yields user turns
 * on demand.  The generator stays open until close() is called.
 */
function createPromptGenerator() {
  let resolveNext = null;
  let nextMessage = null;
  let closed = false;

  const generator = {
    [Symbol.asyncIterator]() {
      return this;
    },
    async next() {
      if (closed && nextMessage === null) {
        return { done: true, value: undefined };
      }
      if (nextMessage !== null) {
        const msg = nextMessage;
        nextMessage = null;
        return { done: false, value: msg };
      }
      // Park until the next message arrives.
      return new Promise((resolve) => {
        resolveNext = resolve;
      });
    },
    return() {
      closed = true;
      if (resolveNext) {
        resolveNext({ done: true, value: undefined });
        resolveNext = null;
      }
      return Promise.resolve({ done: true, value: undefined });
    },
  };

  const send = (message) => {
    nextMessage = message;
    if (resolveNext) {
      const resolve = resolveNext;
      resolveNext = null;
      const msg = nextMessage;
      nextMessage = null;
      resolve({ done: false, value: msg });
    }
  };

  const close = () => {
    closed = true;
    if (resolveNext) {
      resolveNext({ done: true, value: undefined });
      resolveNext = null;
    }
  };

  return { generator, send, close };
}

// ---------------------------------------------------------------------------
// Message type mapping: SDK output → NDJSON OUT frames
// ---------------------------------------------------------------------------

/**
 * Process one SDK output message and emit zero or more NDJSON lines.
 *
 * SDK message types:
 *   system      — init / thinking_tokens (skip)
 *   assistant   — content blocks: text, thinking, tool_use
 *   user        — tool_result blocks
 *   result      — terminal: total_cost_usd, usage
 *   stream_event — partials (with includePartialMessages:true); skip deltas
 */
function processMessage(msg) {
  switch (msg.type) {
    case "assistant": {
      const content = msg.message?.content || [];
      for (const block of content) {
        if (block.type === "text") {
          emit({ v: 1, kind: "token", text: block.text });
        } else if (block.type === "thinking") {
          emit({
            v: 1,
            kind: "thinking",
            text: block.thinking || "",
            truncated: false,
            redacted: false,
          });
        } else if (block.type === "redacted_thinking") {
          emit({
            v: 1,
            kind: "thinking",
            text: "",
            truncated: false,
            redacted: true,
          });
        } else if (block.type === "tool_use") {
          // Register the tool name for tool_result correlation (S3).
          // The SDK provides block.id as the stable correlation key.
          if (block.id) {
            toolUseIdToName.set(block.id, block.name);
          }
          // Non-AskUserQuestion tool_use events are emitted as tool_use chunks.
          // AskUserQuestion is handled by canUseTool and emitted as ask_user_question.
          if (block.name !== "AskUserQuestion") {
            emit({
              v: 1,
              kind: "tool_use",
              toolName: block.name,
              toolInput: JSON.stringify(block.input || {}),
            });
          }
        }
      }
      break;
    }

    case "user": {
      // tool_result blocks from the user role.
      // block.tool_use_id correlates with the earlier tool_use block's id.
      const content = msg.message?.content || [];
      for (const block of content) {
        if (block.type === "tool_result") {
          const toolContent = Array.isArray(block.content)
            ? block.content.map((c) => (c.type === "text" ? c.text : "")).join("")
            : String(block.content || "");
          // Resolve the tool name from the correlation map (S3 parity with CLI engine).
          const toolName = toolUseIdToName.get(block.tool_use_id) || "";
          // Remove the entry once used to bound map growth across long sessions.
          if (block.tool_use_id) {
            toolUseIdToName.delete(block.tool_use_id);
          }
          emit({
            v: 1,
            kind: "tool_result",
            toolName,
            toolOutput: toolContent,
            isError: block.is_error || false,
          });
        }
      }
      break;
    }

    case "result": {
      emit({
        v: 1,
        kind: "summary",
        totalCostUsd: msg.total_cost_usd || 0,
        usage: msg.usage || {},
      });
      break;
    }

    case "system":
      // Init / thinking_tokens: skip (no useful chunk to emit upstream).
      break;

    // stream_event partials (includePartialMessages:true): ignore deltas here
    // because the assistant/user/result messages carry the completed blocks.
    case "stream_event":
      break;

    default:
      // Unknown message type: log to stderr, don't crash.
      log("unknown SDK message type", msg.type);
  }
}

// ---------------------------------------------------------------------------
// SDK query runner
// ---------------------------------------------------------------------------

/**
 * runQuery wraps one call to query() and processes every output message.
 * The prompt generator stays open across turns; query() must be called only
 * once with the same generator to preserve multi-turn context.
 *
 * NOTE: per the spike findings, query() must be driven with a STREAMING
 * AsyncIterable — NOT a string — so that canUseTool injected answers are
 * applied and the generator stays open for multi-turn.
 */
async function runQueryLoop(generator, options) {
  for await (const msg of query({ prompt: generator, options })) {
    try {
      processMessage(msg);
    } catch (err) {
      log("error processing message", err);
      emit({ v: 1, kind: "error", text: String(err) });
    }
  }
}

// ---------------------------------------------------------------------------
// Stdin frame reader
// ---------------------------------------------------------------------------

/**
 * startStdinReader reads NDJSON frames from stdin and dispatches them.
 * Returns a promise that resolves when stdin closes or a shutdown frame arrives.
 */
function startStdinReader(onUserTurn, onAnswer, onShutdown) {
  return new Promise((resolve) => {
    const rl = readline.createInterface({
      input: process.stdin,
      crlfDelay: Infinity,
    });

    rl.on("line", (line) => {
      const trimmed = line.trim();
      if (!trimmed) return;

      let frame;
      try {
        frame = JSON.parse(trimmed);
      } catch (err) {
        log("warn: invalid JSON on stdin:", trimmed.slice(0, 200));
        return;
      }

      if (frame.v !== 1) {
        log("warn: unsupported protocol version", frame.v);
        return;
      }

      switch (frame.kind) {
        case "user_turn":
          onUserTurn(frame.text || "");
          break;
        case "answer":
          onAnswer(frame);
          break;
        case "shutdown":
          rl.close();
          resolve();
          break;
        default:
          log("warn: unknown frame kind", frame.kind);
      }
    });

    rl.on("close", () => {
      resolve();
    });

    rl.on("error", (err) => {
      log("stdin error:", err);
      resolve();
    });
  });
}

// ---------------------------------------------------------------------------
// API-key gate (K-137)
// ---------------------------------------------------------------------------

/** Exit status when the gate refuses: sysexits EX_CONFIG. Pinned by the Go tests. */
const EXIT_API_KEY_REQUIRED = 78;

/**
 * Prefixes of the tokens a subscription login produces: access (oat) and refresh
 * (ort). The name has no "auth" or "login" on purpose: a secrets scanner reads
 * such a name next to two strings as a credential pair (see claude-sdk-dispatch.py).
 */
const SUBSCRIPTION_TOKEN_PREFIXES = ["sk-ant-oat", "sk-ant-ort"];

/**
 * apiKeyRefusal returns why this sidecar must not start, or "" when it may.
 *
 * It reads only ANTHROPIC_API_KEY and returns a constant sentence: no part of
 * the value, and no other variable, is ever echoed.
 */
function apiKeyRefusal(env) {
  const key = String(env.ANTHROPIC_API_KEY ?? "").trim();
  if (key === "") {
    return (
      "ANTHROPIC_API_KEY is not set; the Agent SDK engine does not run on a claude.ai subscription login " +
      "(set an API key, or use the CLI engine for interactive chat)"
    );
  }
  const lower = key.toLowerCase();
  if (SUBSCRIPTION_TOKEN_PREFIXES.some((prefix) => lower.includes(prefix))) {
    return (
      "ANTHROPIC_API_KEY holds a subscription OAuth token, not an API key; the Agent SDK engine does not accept " +
      "those (set an API key, or use the CLI engine for interactive chat)"
    );
  }
  return "";
}

/**
 * oauthEnvNames returns the names in env that carry subscription OAuth material:
 * any CLAUDE_CODE_OAUTH* name in any case, and any variable whose value contains an
 * OAuth token marker, whatever its name except yakOS's own. A name that starts with
 * YAKOS_ (exact case) is never judged by its value: the composed agent roster can
 * mention a token prefix in prose, and the yakOS hooks the bundled CLI runs read
 * YAKOS_ variables; nothing reads one as a credential. A name that only contains
 * YAKOS_, or spells it in lowercase, is an ordinary name. Names only; no value is
 * read out.
 */
function oauthEnvNames(env) {
  return Object.keys(env).filter((name) => {
    if (name.toUpperCase().startsWith("CLAUDE_CODE_OAUTH")) return true;
    if (name.startsWith("YAKOS_")) return false;
    const value = env[name];
    if (typeof value !== "string") return false;
    const lower = value.toLowerCase();
    return SUBSCRIPTION_TOKEN_PREFIXES.some((prefix) => lower.includes(prefix));
  });
}

/**
 * scrubOAuthEnv deletes those variables from env (process.env in main). The SDK
 * copies process.env for the Claude Code it spawns, so a sidecar started outside
 * SDKEngine.Start, which strips the same variables before it spawns node, is as
 * clean as one started through it. The Go side and claude-sdk-dispatch.py do the
 * same.
 */
function scrubOAuthEnv(env) {
  for (const name of oauthEnvNames(env)) {
    delete env[name];
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  // K-137 hard gate, before anything else: no "ready" frame, no SDK, no login
  // fallback. Set the exit code and return instead of calling process.exit() so
  // the stderr line is flushed; nothing is listening yet, so the process ends.
  const refusal = apiKeyRefusal(process.env);
  if (refusal !== "") {
    process.stderr.write(`[sidecar] refusing to start: ${refusal}\n`);
    process.exitCode = EXIT_API_KEY_REQUIRED;
    return;
  }

  // Then remove subscription OAuth variables from the environment the SDK will
  // copy (see scrubOAuthEnv).
  scrubOAuthEnv(process.env);

  // Inspection seam for the tests: print the variable NAMES the SDK would inherit,
  // one JSON line, and stop before the SDK starts. Never used by SDKEngine.
  if (process.argv.includes("--check-env")) {
    process.stdout.write(JSON.stringify({ names: Object.keys(process.env).sort() }) + "\n");
    return;
  }

  // Build the options for query(). Auth is the ANTHROPIC_API_KEY checked above;
  // no keychain or login fallback is configured.
  const options = {
    permissionMode: "bypassPermissions",
    includePartialMessages: true,
    canUseTool,
  };

  // Optional model override from command-line args: --model <name>
  for (let i = 2; i < process.argv.length; i++) {
    if (process.argv[i] === "--model" && process.argv[i + 1]) {
      options.model = process.argv[i + 1];
      i++;
    }
  }

  // Create the prompt generator (kept open across turns).
  const { generator, send: sendToModel, close: closeGenerator } = createPromptGenerator();

  // Emit ready immediately so the Go process knows auth succeeded.
  emit({ v: 1, kind: "ready" });

  // Start the SDK query loop in the background.
  // It blocks on the generator and processes messages as they arrive.
  const queryPromise = runQueryLoop(generator, options).catch((err) => {
    log("query loop error:", err);
    emit({ v: 1, kind: "error", text: String(err) });
  });

  // Read stdin frames.
  await startStdinReader(
    // user_turn: inject into the generator.
    (text) => {
      sendToModel({
        role: "user",
        content: [{ type: "text", text }],
      });
    },
    // answer: deliver to pending question promise.
    (frame) => {
      deliverAnswer(frame);
    },
    // shutdown: close generator.
    () => {
      closeGenerator();
    }
  );

  // Close generator on stdin close (covers both explicit shutdown and pipe EOF).
  closeGenerator();

  // Wait for the SDK loop to drain.
  await queryPromise;

  log("sidecar exiting");
}

main().catch((err) => {
  process.stderr.write(`[sidecar] fatal: ${err}\n`);
  process.exit(1);
});
