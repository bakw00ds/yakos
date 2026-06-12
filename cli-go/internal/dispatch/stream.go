package dispatch

// stream.go implements RunStream: an unframed, token-streaming variant of Run.
//
// Design:
//   - Mirrors Run's setup (roster compose, model resolution, identity validation,
//     governor) by going through the Service facade.
//   - Step 8 uses execWithStreaming instead of execWithStderrCapture.
//   - execWithStreaming attaches cmd.StdoutPipe() and reads with a bounded
//     bufio.Reader (~2MB cap per line to tolerate the multi-KB system/init event).
//   - Stderr is captured in a parallel goroutine (preserves dispatch_finished
//     stderr_tail contract identical to Run).
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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// maxStreamLineBytes is the read cap for a single NDJSON line in the streaming
// reader. Must be large enough for the claude system/init event (~50KB in
// practice with a full skill roster).  2MB is generous and bounded.
const maxStreamLineBytes = 2 * 1024 * 1024

// StreamChunk is one incremental unit of streaming output.
type StreamChunk struct {
	// Type is "token" for incremental text or "summary" for the terminal record.
	Type string

	// Text holds the token text (Type=="token") or the full result text (Type=="summary").
	Text string

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

	operatorID := p.OperatorID
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

	var targetAgent *agentscompose.ComposedAgent
	for i := range roster {
		if roster[i].ID == p.Agent {
			targetAgent = &roster[i]
			break
		}
	}
	if targetAgent == nil {
		return Result{}, fmt.Errorf("dispatch: agent %q not found in composed set (yakosRoot=%s, project=%s)",
			p.Agent, yakosRoot, project)
	}

	if p.Model == "" && targetAgent.Model != "" {
		modelResolved = targetAgent.Model
		_ = modelChosenBy // keep "frontmatter"
	}

	runtimeName := p.Runtime
	if runtimeName == "" {
		runtimeName = "claude"
	}
	adapter, err := runtime.Resolve(runtimeName)
	if err != nil {
		return Result{}, fmt.Errorf("dispatch: %w", err)
	}

	req := Request{
		AgentName:      p.Agent,
		Task:           p.Task,
		Project:        project,
		Runtime:        runtimeName,
		Model:          p.Model,
		YakosRoot:      yakosRoot,
		Timeout:        p.Timeout,
		OperatorID:     operatorID,
		ConversationID: p.ConversationID,
		SessionID:      p.SessionID,
		ModelChosenBy:  modelChosenBy,
		ModelResolved:  modelResolved,
	}

	chatReq := runtime.ChatDispatchRequest{
		Project:           project,
		UserText:          p.Task,
		AgentSystemPrompt: targetAgent.Prompt,
		ModelOverride:     modelResolved,
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
//  2. Captures stderr in a parallel goroutine.
//  3. Writes dispatch_started before exec and dispatch_finished after.
//  4. Reads stdout line-by-line with a bounded reader.
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
		allText    []byte
		exitCode   int
		execErr    error
		stderrBuf  bytes.Buffer
		costUSD    float64
		textBlocks = make(map[int]struct{})
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

		// Read stdout line-by-line with a bounded reader.
		// bufio.Reader.ReadBytes('\n') is line-oriented; we set a generous buffer
		// to tolerate the multi-KB system/init event without allocating per call.
		reader := bufio.NewReaderSize(stdoutPipe, maxStreamLineBytes)

		isClaudeRuntime := adapter.Name() == "claude"

		for {
			line, readErr := reader.ReadBytes('\n')
			line = bytes.TrimRight(line, "\r\n")

			if len(line) > 0 {
				if isClaudeRuntime {
					// Incremental streaming path: parse each NDJSON line.
					tok, isResult, lineCost := runtime.ParseStreamLine(line, textBlocks)
					if tok != "" {
						allText = append(allText, tok...)
						onChunk(StreamChunk{Type: "token", Text: tok})
					}
					if isResult {
						costUSD = lineCost
					}
					// Lines exceeding maxStreamLineBytes will arrive as a partial
					// line (ReadBytes returns ErrBufferFull in bufio.Scanner but
					// bufio.Reader.ReadBytes returns a long slice).
					// We parse what we have; oversized lines are likely system/init
					// events that ParseStreamLine drops anyway.
				} else {
					// Buffered path (codex/agy/gemini): accumulate all text.
					allText = append(allText, line...)
					allText = append(allText, '\n')
				}
			}

			if readErr != nil {
				if readErr != io.EOF {
					// Non-EOF read error: surface but don't abort — the subprocess
					// may still have exited cleanly.
					_ = readErr // log at debug level in future
				}
				break
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
