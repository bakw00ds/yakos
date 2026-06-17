package dispatch

// stream.go implements RunStream: an unframed, token-streaming variant of Run.
//
// Design:
//   - Mirrors Run's setup (roster compose, model resolution, identity validation,
//     governor) by going through the Service facade.
//   - Step 8 uses execWithStreaming instead of execWithStderrCapture.
//   - execWithStreaming attaches cmd.StdoutPipe() and reads stdout with a
//     fixed-size read buffer and a manual per-line byte counter.  Any single
//     line that exceeds maxStreamLineBytes is truncated: the accumulated bytes
//     are discarded, a warning is logged once, and reading continues until the
//     next '\n' is found.  This is the REAL per-line cap — bufio.NewReaderSize
//     alone does NOT cap ReadBytes (it accumulates a growing slice regardless of
//     the buffer hint).
//   - Stderr is attached as cmd.Stderr = &bytes.Buffer and drained after
//     cmd.Wait() returns.  It is NOT read in a parallel goroutine.
//   - Writes dispatch_started and dispatch_finished events via the same
//     writeStarted/writeFinished functions as Run (parity).
//   - Emits StreamChunk{Type:"token"} for each incremental text delta.
//   - Emits StreamChunk{Type:"summary"} as the final chunk with cost/metrics.
//
// Per-runtime streaming behaviour:
//   - claude: true incremental streaming (multiple token chunks via ParseStreamLine).
//   - codex: buffered (one token chunk emitted when process exits).
//   - agy/gemini: buffered plaintext (one token chunk, cost unavailable).
//
// Test seam: streamRunFn (parallel to runFn) can be swapped in tests.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// maxStreamLineBytes is the hard per-line byte cap for the streaming reader.
// Any line whose length exceeds this value is truncated: bytes accumulated so
// far are discarded, a debug log is emitted once per truncation, and reading
// resumes at the next '\n'.  2MB is generous enough for the claude system/init
// event (~50KB with a full skill roster) while remaining well within normal
// heap budgets.
const maxStreamLineBytes = 2 * 1024 * 1024

// maxBufferedOutputBytes is the ceiling for the total accumulated output on the
// buffered (codex/agy) path.  A runtime that emits more than this will have its
// output silently truncated at this limit.  The truncation marker
// "[...output truncated...]" is appended so callers know data was dropped.
const maxBufferedOutputBytes = 32 * 1024 * 1024 // 32 MB

// maxTaskBytes is the facade-level limit on Task / UserText size.  Enforced in
// RunStream (and separately in Run) before any subprocess is forked.  1 MB is
// generous for an LLM prompt while keeping the boundary well below the 4 MB
// gRPC frame cap so Phase 3b's SSE/REST path (no gRPC framing) stays safe.
const maxTaskBytes = 1 * 1024 * 1024 // 1 MB

// maxToolOutputBytes is the hard truncation ceiling for a single tool_result
// output before it is emitted as a StreamChunk.  Tool output is untrusted text
// from the runtime (bash stdout, file contents, etc.); bounding it prevents
// large outputs from bloating SSE frames and transcript entries.
//
// 16 KB is generous for a code snippet or command output while keeping each
// SSE frame well under typical reverse-proxy buffer limits.
const maxToolOutputBytes = 16 * 1024

// toolOutputTruncationMarker is appended to a tool output that was cut at
// maxToolOutputBytes.  Chosen to be unmistakable in context.
const toolOutputTruncationMarker = "\n[...tool output truncated...]"

// toolInputTruncationMarker is appended to a tool input (the JSON args
// accumulated from input_json_delta fragments) that was cut at maxToolOutputBytes.
// Kept distinct from toolOutputTruncationMarker so the user can tell at a
// glance whether it was the input or the output that was truncated.
const toolInputTruncationMarker = "\n[...tool input truncated...]"

// StreamChunk is one incremental unit of streaming output.
type StreamChunk struct {
	// Type is "token" for incremental text, "summary" for the terminal record,
	// "tool_use" when the agent invoked a tool, or "tool_result" when the tool
	// returned a result.
	Type string

	// Text holds the token text (Type=="token") or the full result text (Type=="summary").
	Text string

	// ToolName is the human-readable tool name (Type=="tool_use" or "tool_result").
	// Examples: "Bash", "Read", "Write".
	ToolName string

	// ToolInput is the JSON-encoded tool arguments (Type=="tool_use").
	// For a Bash invocation this is typically {"command":"ls -la"}.
	ToolInput string

	// ToolOutput is the truncated tool result content (Type=="tool_result").
	// Always <= maxToolOutputBytes + len(toolOutputTruncationMarker).
	ToolOutput string

	// IsError is true when the tool_result represents a tool-level error
	// (Type=="tool_result" only).
	IsError bool

	// The following fields are populated only on Type=="summary".
	ExitCode      int
	DurationS     float64
	OutputBytes   int64
	ModelResolved string
	TotalCostUSD  float64
}

// chatCmdProvider is the interface implemented by adapters that support the
// unframed chat execution mode.  Both the real adapters (ClaudeAdapter,
// CodexAdapter, AgyAdapter, GeminiAdapter) and the fake streaming adapter in
// tests implement this interface.
type chatCmdProvider interface {
	ChatExecCmd(ctx context.Context, req runtime.ChatDispatchRequest) *exec.Cmd
}

// streamRunFn is the package-level streaming execution function.  It mirrors
// runFn: defaults to the real implementation but can be replaced in tests.
var streamRunFn = func(
	ctx context.Context,
	req Request,
	adapter runtime.Adapter,
	chatReq runtime.ChatDispatchRequest,
	onChunk func(StreamChunk),
) (Result, error) {
	return execWithStreaming(ctx, req, adapter, chatReq, onChunk)
}

// RunStream executes an unframed streaming dispatch through the Service.
//
// It mirrors Service.Run's setup (identity validation, governor semaphore,
// bus events, dispatch_started/finished logging) but step 8 uses
// execWithStreaming, which attaches a stdout pipe and calls onChunk for each
// incremental token.
//
// The final StreamChunk{Type:"summary"} carries the same cost/metrics fields
// as a non-streaming dispatch_finished event.  dispatch_finished is written to
// the NDJSON log so cost accounting and the metrics dashboard see streaming
// dispatches identically to one-shot dispatches.
//
// onChunk is called synchronously from the streaming goroutine — do not block
// inside it longer than a fast fan-out write.  Cancelling ctx kills the
// subprocess and causes RunStream to return; any partial chunks already
// delivered to onChunk are kept.
func (s *Service) RunStream(ctx context.Context, p Params, onChunk func(StreamChunk)) (Result, error) {
	// --- Phase 6b: role enforcement (mTLS / Resolved path only) ---
	// Mirrors Service.Run enforcement — see service.go for rationale.
	if p.ResolvedIdentity.Populated {
		if !p.ResolvedIdentity.Identity.Role.Allows(netid.RoleDispatch) {
			// Generic error returned to caller — role details stay server-side.
			return Result{}, fmt.Errorf("dispatch: forbidden: insufficient role")
		}
	}

	// --- Resolve project and yakos root (mirrors Service.Run) ---
	project := p.Project
	if project == "" {
		project = s.cfg.WorkspaceRoot
	}
	yakosRoot := p.YakosRoot
	if yakosRoot == "" {
		yakosRoot = s.cfg.YakosRoot
	}
	if yakosRoot == "" {
		return Result{}, fmt.Errorf("dispatch: yakos_root is required (set in ServiceConfig or per-request Params.YakosRoot)")
	}

	// --- Task / UserText size bound (facade chokepoint) ---
	// Enforced here before any subprocess is forked so all transports inherit
	// the check regardless of whether they impose their own frame cap.
	if len(p.Task) > maxTaskBytes {
		return Result{}, fmt.Errorf("dispatch: task exceeds maximum size (%d bytes; limit %d)", len(p.Task), maxTaskBytes)
	}

	// --- Validate and stamp identity (mirrors Service.Run) ---
	if err := validateIdentityField("operator_id", p.OperatorID); err != nil {
		return Result{}, err
	}
	if err := validateIdentityField("conversation_id", p.ConversationID); err != nil {
		return Result{}, err
	}
	if err := validateIdentityField("session_id", p.SessionID); err != nil {
		return Result{}, err
	}

	// --- Dual-regime operator_id (mirrors Service.Run) ---
	var operatorID string
	if p.ResolvedIdentity.Populated && p.ResolvedIdentity.Identity.Authenticated {
		// Cert CN wins; never use caller-supplied OperatorID.
		operatorID = p.ResolvedIdentity.Identity.OperatorID
	} else {
		// Cooperative-label path (loopback or unresolved): existing logic preserved.
		operatorID = p.OperatorID
		if operatorID != "" && !p.isMCPStamped {
			for _, prefix := range reservedOperatorPrefixes {
				if strings.HasPrefix(operatorID, prefix) {
					operatorID = ""
					break
				}
			}
		}
		if operatorID == "" {
			operatorID = s.opID
		}
	}

	// --- Validate inputs ---
	if p.Agent == "" {
		return Result{}, fmt.Errorf("dispatch: agent name is required")
	}
	if p.Task == "" {
		return Result{}, fmt.Errorf("dispatch: task is required")
	}

	// --- Resolve model and agent (mirrors Run steps 2-5) ---
	modelChosenBy := "frontmatter"
	modelResolved := "sonnet"

	if p.Model != "" {
		if !runtime.ValidateTier(p.Model) {
			return Result{}, fmt.Errorf("dispatch: invalid model tier %q (must be haiku|sonnet|opus|fable)", p.Model)
		}
		modelResolved = p.Model
		modelChosenBy = "override"
	}

	roster, err := agentscompose.Compose(yakosRoot, project)
	if err != nil {
		return Result{}, fmt.Errorf("dispatch: compose agents: %w", err)
	}

	// See resolve.go for resolution order (specialist → generic runtime → error).
	targetAgent, err := resolveAgent(roster, p.Agent, yakosRoot, project)
	if err != nil {
		return Result{}, err
	}

	if p.Model == "" && targetAgent.Model != "" {
		modelResolved = targetAgent.Model
		_ = modelChosenBy // keep "frontmatter"
	}

	// See resolve.go for precedence (override → known-runtime name → "claude").
	runtimeName := resolveRuntime(p.Agent, p.Runtime)
	adapter, err := runtime.Resolve(runtimeName)
	if err != nil {
		return Result{}, fmt.Errorf("dispatch: %w", err)
	}

	req := Request{
		AgentName:       p.Agent,
		Task:            p.Task,
		Project:         project,
		Runtime:         runtimeName,
		Model:           p.Model,
		YakosRoot:       yakosRoot,
		Timeout:         p.Timeout,
		OperatorID:      operatorID,
		ConversationID:  p.ConversationID,
		SessionID:       p.SessionID,
		ModelChosenBy:   modelChosenBy,
		ModelResolved:   modelResolved,
		WorkDirOverride: p.WorkDirOverride,
	}

	chatReq := runtime.ChatDispatchRequest{
		Project:           project,
		UserText:          p.Task,
		AgentSystemPrompt: targetAgent.Prompt,
		ModelOverride:     modelResolved,
		WorkDirOverride:   p.WorkDirOverride,
		// AllowRoot is not plumbed through Params (CLI-only flag); defaults to
		// false for console/gRPC-originated streaming dispatches.
	}

	// --- Acquire governor slot (mirrors Service.Run) ---
	select {
	case <-s.sem:
		defer func() { s.sem <- struct{}{} }()
	case <-ctx.Done():
		return Result{}, fmt.Errorf("dispatch: service at capacity, request cancelled: %w", ctx.Err())
	}

	// --- Bus: dispatch started ---
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{
			Agent:   p.Agent,
			Project: project,
			TS:      time.Now().UTC(),
		})
	}

	// --- Execute (streaming) ---
	result, execErr := streamRunFn(ctx, req, adapter, chatReq, onChunk)

	// --- Bus: dispatch finished ---
	if s.cfg.Bus != nil {
		exitCode := result.ExitCode
		if execErr != nil {
			exitCode = -1
		}
		s.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{
			Agent:    p.Agent,
			Project:  project,
			ExitCode: exitCode,
			TS:       time.Now().UTC(),
		})
	}

	return result, execErr
}

// execWithStreaming is the real streaming executor.  It:
//  1. Selects the unframed chat exec cmd from the adapter.
//  2. Attaches stderr as cmd.Stderr = &bytes.Buffer (drained after cmd.Wait).
//  3. Writes dispatch_started before exec and dispatch_finished after.
//  4. Reads stdout line-by-line with a fixed-size read buffer and a manual
//     per-line length guard.  Lines exceeding maxStreamLineBytes are truncated
//     (remaining bytes skipped to next '\n'); the guard is the REAL cap.
//  5. Calls onChunk for each token; emits a summary chunk at the end.
func execWithStreaming(
	ctx context.Context,
	req Request,
	adapter runtime.Adapter,
	chatReq runtime.ChatDispatchRequest,
	onChunk func(StreamChunk),
) (Result, error) {
	logPath := dispatchLogPath()
	tsStart := time.Now()
	writeStarted(req, tsStart, logPath)

	cp, hasChatCmd := adapter.(chatCmdProvider)

	var (
		allText       []byte
		exitCode      int
		execErr       error
		stderrBuf     bytes.Buffer
		costUSD       float64
		usageCost     *cost.Usage
		textBlocks    = make(map[int]struct{})
		toolUseBlocks = make(map[int]*runtime.ToolEvent) // index → in-progress tool_use
		toolIDToName  = make(map[string]string)           // tool-use id → name for tool_result correlation
	)

	if hasChatCmd {
		// Use the unframed chat exec path (claude: streaming; codex/agy: buffered).
		cmd := cp.ChatExecCmd(ctx, chatReq)

		stdoutPipe, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			tsEnd := time.Now()
			writeFinished(req, Result{
				ExitCode:      -1,
				DurationS:     tsEnd.Sub(tsStart).Seconds(),
				ModelChosenBy: req.ModelChosenBy,
				ModelResolved: req.ModelResolved,
			}, tsEnd, logPath)
			return Result{ExitCode: -1}, fmt.Errorf("dispatch: stream: stdout pipe: %w", pipeErr)
		}
		// Stderr is attached as a plain buffer and drained by the OS write path.
		// It is NOT read in a parallel goroutine — cmd.Wait() ensures all stderr
		// bytes have landed before we inspect stderrBuf.
		cmd.Stderr = &stderrBuf

		if startErr := cmd.Start(); startErr != nil {
			tsEnd := time.Now()
			writeFinished(req, Result{
				ExitCode:      -1,
				DurationS:     tsEnd.Sub(tsStart).Seconds(),
				ModelChosenBy: req.ModelChosenBy,
				ModelResolved: req.ModelResolved,
			}, tsEnd, logPath)
			return Result{ExitCode: -1}, fmt.Errorf("dispatch: stream: start: %w", startErr)
		}

		// Read stdout with a fixed-size read buffer and a manual per-line byte
		// counter.  This is the REAL per-line cap:
		//   - readBuf is the I/O buffer; bytes are never accumulated beyond it
		//     before we process and classify them.
		//   - lineLen tracks bytes accumulated in lineBuf since the last '\n'.
		//   - When lineLen would exceed maxStreamLineBytes, we set overlong=true,
		//     discard further bytes for that line, log once, and resume on '\n'.
		//   - When a '\n' is found (or EOF), we process the accumulated lineBuf
		//     (if not overlong) and reset.
		//
		// NOTE: Phase 4 will add process-group kill on context cancel to reap
		// grandchild processes spawned by the runtime.  For now, exec.CommandContext
		// only kills the direct child; orphan grandchildren are a pre-existing
		// limitation tracked for Phase 4.
		const readBufSize = 64 * 1024 // 64 KB I/O read granule
		readBuf := make([]byte, readBufSize)
		var lineBuf []byte
		overlong := false
		bufferedOutputTruncated := false

		isClaudeRuntime := adapter.Name() == "claude"

	readLoop:
		for {
			n, readErr := stdoutPipe.Read(readBuf)
			if n > 0 {
				chunk := readBuf[:n]
				for len(chunk) > 0 {
					// Find the next '\n' in the remaining chunk.
					nlIdx := bytes.IndexByte(chunk, '\n')
					var segment []byte
					var haveNewline bool
					if nlIdx >= 0 {
						segment = chunk[:nlIdx]
						chunk = chunk[nlIdx+1:]
						haveNewline = true
					} else {
						segment = chunk
						chunk = nil
					}

					if overlong {
						// Already discarding this line; skip until newline.
						if haveNewline {
							overlong = false
							lineBuf = lineBuf[:0]
						}
					} else {
						if len(lineBuf)+len(segment) > maxStreamLineBytes {
							// This line would exceed the cap.  Discard what we have.
							slog.Debug("dispatch: stream: line exceeds maxStreamLineBytes; truncating",
								"agent", req.AgentName,
								"accumulated_bytes", len(lineBuf)+len(segment),
								"limit", maxStreamLineBytes,
							)
							lineBuf = lineBuf[:0]
							overlong = !haveNewline // if newline present, already done
						} else {
							lineBuf = append(lineBuf, segment...)
							if haveNewline {
								// Complete line ready: process it.
								line := bytes.TrimRight(lineBuf, "\r")
								if len(line) > 0 {
									if isClaudeRuntime {
										tok, isResult, lineCost, lineUsage, toolEv := runtime.ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
										if tok != "" {
											allText = append(allText, tok...)
											onChunk(StreamChunk{Type: "token", Text: tok})
										}
										if isResult {
											costUSD = lineCost
											usageCost = lineUsage
										}
										if toolEv != nil {
											emitToolChunk(toolEv, onChunk)
										}
									} else {
										// Buffered path: accumulate with ceiling check.
										if !bufferedOutputTruncated {
											if len(allText)+len(line)+1 > maxBufferedOutputBytes {
												allText = append(allText, []byte("\n[...output truncated...]")...)
												bufferedOutputTruncated = true
											} else {
												allText = append(allText, line...)
												allText = append(allText, '\n')
											}
										}
									}
								}
								lineBuf = lineBuf[:0]
							}
						}
					}
				}
			}

			if readErr != nil {
				if readErr != io.EOF {
					// Non-EOF read error: log at debug; don't abort — the subprocess
					// may still have exited cleanly.  cmd.Wait() below captures the
					// real exit status.
					slog.Debug("dispatch: stream: stdout read error", "agent", req.AgentName, "err", readErr)
				}
				break readLoop
			}
		}

		// Process any remaining bytes in lineBuf (line without trailing newline).
		if !overlong && len(lineBuf) > 0 {
			line := bytes.TrimRight(lineBuf, "\r")
			if len(line) > 0 {
				if isClaudeRuntime {
					tok, isResult, lineCost, lineUsage, toolEv := runtime.ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
					if tok != "" {
						allText = append(allText, tok...)
						onChunk(StreamChunk{Type: "token", Text: tok})
					}
					if isResult {
						costUSD = lineCost
						usageCost = lineUsage
					}
					if toolEv != nil {
						emitToolChunk(toolEv, onChunk)
					}
				} else if !bufferedOutputTruncated {
					allText = append(allText, line...)
				}
			}
		}

		// Wait for the process and collect exit code.
		waitErr := cmd.Wait()
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				execErr = waitErr
			}
		}

		// For non-claude runtimes: emit the accumulated text as one token chunk.
		if !isClaudeRuntime && len(allText) > 0 {
			text := string(bytes.TrimRight(allText, "\n"))
			onChunk(StreamChunk{Type: "token", Text: text})
		}

	} else {
		// Fallback: adapter has no ChatExecCmd (e.g. fully mocked in non-chat tests).
		// Use the standard Dispatch path and emit one token chunk.
		dispatchReq := runtime.DispatchRequest{
			Project:        chatReq.Project,
			AgentName:      req.AgentName,
			Task:           chatReq.UserText,
			ModelOverride:  chatReq.ModelOverride,
			AllowRoot:      chatReq.AllowRoot,
			ConversationID: req.ConversationID,
		}
		res, dispErr := adapter.Dispatch(ctx, dispatchReq)
		if dispErr != nil {
			execErr = dispErr
		} else {
			exitCode = res.ExitCode
			allText = res.Stdout
			if len(allText) > 0 {
				onChunk(StreamChunk{Type: "token", Text: string(allText)})
			}
		}
	}

	tsEnd := time.Now()
	durationS := tsEnd.Sub(tsStart).Seconds()
	outputBytes := int64(len(allText))

	stderrTail, stderrTrunc := processStderr(stderrBuf.Bytes())
	if exitCode == 0 {
		stderrTail = ""
		stderrTrunc = false
	}

	result := Result{
		ExitCode:      exitCode,
		DurationS:     durationS,
		OutputBytes:   outputBytes,
		TaskBytes:     int64(len(req.Task)),
		StderrTail:    stderrTail,
		StderrTrunc:   stderrTrunc,
		Usage:         usageCost,
		ModelChosenBy: req.ModelChosenBy,
		ModelResolved: req.ModelResolved,
	}

	// Write dispatch_finished identically to Run (parity invariant).
	writeFinished(req, result, tsEnd, logPath)

	// Emit the terminal summary chunk.
	onChunk(StreamChunk{
		Type:          "summary",
		ExitCode:      exitCode,
		DurationS:     durationS,
		OutputBytes:   outputBytes,
		ModelResolved: req.ModelResolved,
		TotalCostUSD:  costUSD,
	})

	if execErr != nil {
		return result, fmt.Errorf("dispatch: stream: runtime error: %w", execErr)
	}
	return result, nil
}

// emitToolChunk translates a runtime.ToolEvent into a StreamChunk and calls
// onChunk.  Both tool input and tool output are hard-truncated at
// maxToolOutputBytes before emit, on rune boundaries to avoid splitting
// multi-byte UTF-8 sequences.
//
// Truncation policy:
//   - tool_use  ToolInput:  capped at maxToolOutputBytes; marker is
//     toolInputTruncationMarker  ("...tool input truncated...").
//   - tool_result ToolOutput: capped at maxToolOutputBytes; marker is
//     toolOutputTruncationMarker ("...tool output truncated...").
//
// Using distinct markers lets the user tell at a glance which side was cut.
// Truncation is applied here (at the dispatch layer) so every downstream
// consumer (SSE hub, transcript, tests) inherits the same bound automatically.
func emitToolChunk(te *runtime.ToolEvent, onChunk func(StreamChunk)) {
	switch te.Kind {
	case "tool_use":
		input := truncateAtRuneBoundary(te.Input, maxToolOutputBytes, toolInputTruncationMarker, te.InputTruncated)
		onChunk(StreamChunk{
			Type:      "tool_use",
			ToolName:  te.ToolName,
			ToolInput: input,
		})

	case "tool_result":
		output := truncateAtRuneBoundary(te.Output, maxToolOutputBytes, toolOutputTruncationMarker, false)
		onChunk(StreamChunk{
			Type:       "tool_result",
			ToolName:   te.ToolName,
			ToolOutput: output,
			IsError:    te.IsError,
		})
	}
}

// truncateAtRuneBoundary truncates s to at most maxBytes bytes, finding the
// nearest rune boundary at or below the cap, then appends marker.
//
// If alreadyTruncated is true the marker is always appended even when len(s)
// is within maxBytes — this covers the case where the parser already stopped
// accumulating at the cap but the string has not yet had the marker appended.
//
// If len(s) <= maxBytes and !alreadyTruncated the string is returned verbatim.
func truncateAtRuneBoundary(s string, maxBytes int, marker string, alreadyTruncated bool) string {
	if len(s) <= maxBytes && !alreadyTruncated {
		return s
	}
	if len(s) > maxBytes {
		// Walk back from maxBytes until we find a valid rune boundary.
		end := maxBytes
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	return s + marker
}
