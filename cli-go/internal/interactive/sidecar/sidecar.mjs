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
 * Auth: apiKeySource:"none" reuses the installed claude CLI keychain credentials.
 * No API key needed in the environment.
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
      // tool_result blocks from the user role
      const content = msg.message?.content || [];
      for (const block of content) {
        if (block.type === "tool_result") {
          const toolContent = Array.isArray(block.content)
            ? block.content.map((c) => (c.type === "text" ? c.text : "")).join("")
            : String(block.content || "");
          // Look up the tool name from our pending registry (best-effort).
          emit({
            v: 1,
            kind: "tool_result",
            toolName: "",
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
// Main
// ---------------------------------------------------------------------------

async function main() {
  // Build the options for query().
  // apiKeySource:"none" reuses the installed claude CLI keychain credentials.
  const options = {
    permissionMode: "bypassPermissions",
    includePartialMessages: true,
    apiKeySource: "none",
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
