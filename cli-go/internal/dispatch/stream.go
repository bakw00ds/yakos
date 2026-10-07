package dispatch

// stream.go implements RunStream: an unframed, token-streaming variant of Run.
//
// Design:
//   - Mirrors Run's setup (roster compose, model resolution, identity validation,
//     budget pre-flight, governor) by going through the Service facade.
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
//   - Writes dispatch_started and dispatch_finished events through the same
//     Account as Run (parity; Account is the only writer of both).
//   - Emits StreamChunk{Type:"token"} for each incremental text delta.
//   - Emits StreamChunk{Type:"summary"} as the final chunk with cost/metrics.
//
// Per-runtime streaming behaviour:
//   - claude: true incremental streaming (multiple token chunks via ParseStreamLine).
//   - codex, agy: streamed (K-144). Every stdout line goes through the runtime's
//     LineParser (runtime.ParserFor) as it arrives, and each decoded event is
//     emitted at once: token (the agent's TEXT, never the raw JSONL), thinking,
//     tool_use and tool_result chunks. The summary chunk carries the token usage
//     and the native session id; a failed turn adds an "error" chunk before it.
//   - plugin runtimes (any other name): the plain-text parser, one token chunk
//     token chunk with the whole text at exit (prose is not streamed, since a
//     stray line ahead of a structured stream is not part of the answer).
//
// Test seam: streamRunFn (parallel to runFn) can be swapped in tests.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
//
// Defined as runtime.MaxStreamLineBytes, the cap the LineParsers enforce on the
// lines they are fed, so the reader and the parsers can never disagree.
const maxStreamLineBytes = runtime.MaxStreamLineBytes

// maxBufferedOutputBytes is the ceiling for the total stdout fed to the
// LineParser on the non-claude path.  Lines past it are read and
// discarded rather than parsed, so a runaway runtime costs neither memory nor
// parser time.  The parser separately caps the TEXT it keeps
// (runtime.MaxParsedTextBytes).  Either limit appends bufferedTruncationMarker
// so callers know data was dropped.
const maxBufferedOutputBytes = 32 * 1024 * 1024 // 32 MB

// bufferedTruncationMarker is appended to buffered-path text that is
// incomplete: the input ceiling or the parser's text cap was hit, or an
// over-long line was dropped.
const bufferedTruncationMarker = "\n[...output truncated...]"

// MaxTaskBytes is the facade-level limit on Task / UserText size.  Enforced in
// RunStream (and separately in Run) before any subprocess is forked, and in
// POST /api/chat/send before writing to stdin.  1 MB is generous for an LLM
// prompt while keeping the boundary well below the 4 MB gRPC frame cap so
// Phase 3b's SSE/REST path (no gRPC framing) stays safe.
const MaxTaskBytes = 1 * 1024 * 1024 // 1 MB

// maxTaskBytes keeps the old unexported name as an alias so callers inside this
// package do not need updating.
const maxTaskBytes = MaxTaskBytes

// maxToolOutputBytes is the hard truncation ceiling for a single tool_result
// output before it is emitted as a StreamChunk.  Tool output is untrusted text
// from the runtime (bash stdout, file contents, etc.); bounding it prevents
// large outputs from bloating SSE frames and transcript entries.
//
// Defined directly in terms of runtime.MaxToolInputBytes (the parser's cap on
// accumulated tool_use Input) rather than duplicating the 16 KiB literal: the
// two ceilings must stay symmetric so a hostile stream cannot route oversized
// input through one side and bypass the other by drifting the constants
// apart. The cross-package test TestToolInputCap_MatchesOutputCap in
// stream_tool_test.go asserts both sides against the real runtime constant.
const maxToolOutputBytes = runtime.MaxToolInputBytes

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
	// "tool_use" when the agent invoked a tool, "tool_result" when the tool
	// returned a result, "thinking" for an incremental extended-thinking delta,
	// or "ask_user_question" when the SDK engine needs the operator to answer
	// an AskUserQuestion tool call before the model can continue.
	Type string

	// Text holds the token text (Type=="token") or the full result text (Type=="summary").
	Text string

	// Thinking holds an incremental thinking delta (Type=="thinking").
	// The per-delta text cap is enforced by the parser (runtime.ParseStreamLineWithThinking)
	// at maxThinkingBytes accumulated per block; ThinkingTruncated reflects the
	// parser's signal that the cap was hit.  emitThinkingChunk does NOT truncate.
	Thinking string

	// ThinkingTruncated is true when the Thinking text was cut at the cap
	// (Type=="thinking" only).
	ThinkingTruncated bool

	// ThinkingRedacted is true when this chunk represents a redacted_thinking
	// block whose content cannot be surfaced (Type=="thinking" only).
	ThinkingRedacted bool

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

	// RuntimeResolved is the runtime that actually ran the turn (an `auto` pane
	// learns it here). Summary only.
	RuntimeResolved string

	// Usage is the token usage the runtime reported (summary chunks only); nil
	// when it reported none. For claude it is the result event's input/output
	// tokens and cost; for the buffered runtimes it is the parsed usage with
	// cache counts. Counts follow runtime.Usage's convention.
	Usage *cost.Usage

	// NativeSessionID is the harness-native session id (summary chunks only), ""
	// when the stream carried none. It is NOT the console UI session id. A chat
	// handler stores claude's so the next one-shot turn can resume the
	// conversation; it is already checked with runtime.ValidSessionID.
	NativeSessionID string

	// AskUserQuestion fields — populated only when Type=="ask_user_question".
	// These are emitted by the SDK engine (P2b) when the model invokes the
	// AskUserQuestion tool and needs operator input before continuing.
	// P2c wires the SSE frame and the /api/chat/answer endpoint.

	// AskToolUseID is the SDK tool-use ID for this AskUserQuestion invocation.
	// Callers must echo it back in the /api/chat/answer body so AnswerQuestion
	// can resolve the parked promise in the sidecar.
	AskToolUseID string

	// AskQuestionsJSON is the JSON-encoded questions slice from the SDK's
	// AskUserQuestion input ([]AskUserQuestionItem).  The consumer (SSE encoder,
	// chat_handler) unmarshals this to surface question text, options, and
	// multiSelect flags to the frontend.
	AskQuestionsJSON string
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
	project, err := resolveProjectPath(p.Project)
	if err != nil {
		return Result{}, err
	}
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
	// The resume id is claude's own session id; it ends up on argv as
	// `--resume <id>`, so it gets the same alphabet check as the identity fields.
	if err := validateIdentityField("resume_session_id", p.ResumeSessionID); err != nil {
		return Result{}, err
	}
	for rt, id := range p.NativeSessions {
		if !slices.Contains(runtime.Known, rt) {
			return Result{}, fmt.Errorf("dispatch: native_sessions: unknown runtime")
		}
		if err := validateIdentityField("native_sessions id", id); err != nil {
			return Result{}, err
		}
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

	// --- Per-agent budget pre-flight (K-119; RunStream joined Run in K-136) ---
	// A streamed turn spends like a one-shot dispatch, so it is refused the same
	// way: a NEW dispatch for an agent in hard_stop never starts, before any
	// process is forked and before any event is written. Any other budget problem
	// fails open (see budgetPreflight).
	if err := budgetPreflight(Request{AgentName: p.Agent, Project: project}); err != nil {
		return Result{}, err
	}

	// --- Route: roster, agent, runtime, model (mirrors Run steps 2-5) ---
	// resolve.go routeDispatch is shared with Run, so both paths pick the
	// runtime and model by one rule. The daemon never supplies a
	// YAKOS_RUNTIME default (RuntimeEnvDefault stays empty here).
	rr, err := routeDispatch(ctx, routeInput{
		YakosRoot:       yakosRoot,
		Project:         project,
		Agent:           p.Agent,
		RuntimeOverride: p.Runtime,
		ModelOverride:   p.Model,
		TaskBytes:       int64(len(p.Task)),
		ConversationID:  p.ConversationID,
		Task:            p.Task,
		Extra:           p.ScanExtra,
	})
	if err != nil {
		noteRefused(Request{AgentName: p.Agent, Project: project, OperatorID: operatorID, ConversationID: p.ConversationID, SessionID: p.SessionID}, err)
		return Result{}, err
	}
	targetAgent := rr.Agent
	adapter := rr.Adapter

	req := Request{
		AgentName:       p.Agent,
		Task:            p.Task,
		Project:         project,
		Runtime:         rr.Runtime,
		RuntimeChosenBy: rr.RuntimeChosenBy,
		FallbackFrom:    rr.FallbackFrom,
		Model:           p.Model,
		YakosRoot:       yakosRoot,
		Timeout:         p.Timeout,
		OperatorID:      operatorID,
		ConversationID:  p.ConversationID,
		SessionID:       p.SessionID,
		ModelChosenBy:   rr.ModelChosenBy,
		ModelResolved:   rr.Model,
		WorkDirOverride: p.WorkDirOverride,
		Effort:          p.Effort,
		Surface:         p.Surface,
		RouteRule:       rr.Decision.RuleID,
		RouteReason:     rr.Decision.Reason,
		RouteClass:      rr.Decision.RouteClass,
		PolicySHA:       rr.Decision.PolicySHA,
	}

	chatReq := runtime.ChatDispatchRequest{
		Project:           project,
		UserText:          p.Task,
		AgentSystemPrompt: targetAgent.Prompt,
		ModelOverride:     rr.Model,
		ModelExplicit:     rr.ModelExplicit,
		WorkDirOverride:   p.WorkDirOverride,
		Effort:            p.Effort,
		// AllowRoot is not plumbed through Params (CLI-only flag); defaults to
		// false for console/gRPC-originated streaming dispatches.
	}
	// Continuity: resume the native session of the runtime that was resolved.
	// Ids were format-checked above.
	if id := p.NativeSessions[rr.Runtime]; id != "" {
		chatReq.ResumeSessionID, chatReq.ResumeRuntime = id, rr.Runtime
	} else if rr.Runtime == "claude" {
		chatReq.ResumeSessionID = p.ResumeSessionID
	}
	chatReq.AgentSystemPrompt = knowledgePersona(rr.Runtime, p, chatReq.AgentSystemPrompt, chatReq.ResumeSessionID != "")

	// --- Acquire governor slot (mirrors Service.Run) ---
	select {
	case <-s.sem:
		defer func() { s.sem <- struct{}{} }()
	case <-ctx.Done():
		return Result{}, fmt.Errorf("dispatch: service at capacity, request cancelled: %w", ctx.Err())
	}

	// --- Bus: dispatch started ---
	if s.cfg.Bus != nil {
		s.cfg.Bus.PublishMeta(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{
			Agent:   p.Agent,
			Project: project,
			TS:      time.Now().UTC(),
		}, wsbus.EventMeta{OwnerOperatorID: operatorID})
	}

	// --- Execute (streaming) ---
	result, execErr := streamRunFn(ctx, req, adapter, chatReq, onChunk)

	// --- Bus: dispatch finished ---
	if s.cfg.Bus != nil {
		exitCode := result.ExitCode
		if execErr != nil {
			exitCode = -1
		}
		s.cfg.Bus.PublishMeta(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{
			Agent:    p.Agent,
			Project:  project,
			ExitCode: exitCode,
			TS:       time.Now().UTC(),
		}, wsbus.EventMeta{OwnerOperatorID: operatorID})
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
	acct := NewAccount(req)
	acct.Start()
	tsStart := acct.Started()

	cp, hasChatCmd := adapter.(chatCmdProvider)
	var tap *feedScanner

	var (
		allText        []byte
		exitCode       int
		execErr        error
		stderrBuf      bytes.Buffer
		costUSD        float64
		usageCost      *cost.Usage
		nativeSession  string               // claude session_id from the result frame
		streamModelID  string               // claude: the model id the init line announced
		parsed         *runtime.ParseResult // buffered runtimes: the LineParser's outcome
		textBlocks     = make(map[int]struct{})
		toolUseBlocks  = make(map[int]*runtime.ToolEvent)          // index → in-progress tool_use
		toolIDToName   = make(map[string]string)                   // tool-use id → name for tool_result correlation
		thinkingBlocks = make(map[int]*runtime.ThinkingBlockEntry) // index → in-progress thinking block
	)

	if hasChatCmd {
		// Use the unframed chat exec path (every harness streams its events).
		// K-146: detect-and-report scan of the normalized events (nil for claude).
		ctx, cancelRun := context.WithCancel(ctx)
		defer cancelRun()
		tap = newFeedScanner(adapter.Name(), req.SessionID, req.Project, cancelRun)
		cmd := cp.ChatExecCmd(ctx, chatReq)
		runtime.ConfigureGroupKill(cmd) // ctx cancel kills the whole group; Wait is bounded

		stdoutPipe, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			tsEnd := time.Now()
			acct.FinishAt(Result{
				ExitCode:      -1,
				DurationS:     tsEnd.Sub(tsStart).Seconds(),
				ModelChosenBy: req.ModelChosenBy,
				ModelResolved: req.ModelResolved,
			}, tsEnd)
			return Result{ExitCode: -1}, fmt.Errorf("dispatch: stream: stdout pipe: %w", pipeErr)
		}
		// Stderr is attached as a plain buffer and drained by the OS write path.
		// It is NOT read in a parallel goroutine — cmd.Wait() ensures all stderr
		// bytes have landed before we inspect stderrBuf.
		cmd.Stderr = &stderrBuf

		if startErr := cmd.Start(); startErr != nil {
			tsEnd := time.Now()
			acct.FinishAt(Result{
				ExitCode:      -1,
				DurationS:     tsEnd.Sub(tsStart).Seconds(),
				ModelChosenBy: req.ModelChosenBy,
				ModelResolved: req.ModelResolved,
			}, tsEnd)
			return Result{ExitCode: -1}, fmt.Errorf("dispatch: stream: start: %w", startErr)
		}

		// Read stdout line-by-line via the shared ReadLineLoop helper (streamhelper.go)
		// so the one-shot path and the interactive path share one implementation.
		// The per-line byte-cap (maxStreamLineBytes) is enforced inside ReadLineLoop.
		bufferedOutputTruncated := false
		isClaudeRuntime := adapter.Name() == "claude"

		// K-135: every non-claude harness is normalized by its LineParser, so the
		// console receives the agent's text and token usage, not raw JSONL.
		var bufParser runtime.LineParser
		streamedText := false // a structured token chunk went out
		bufferedInputBytes := 0
		inputCeilingHit := false
		readerDropped := 0 // lines the reader dropped for length before the parser saw them
		if !isClaudeRuntime {
			bufParser = runtime.ParserFor(adapter.Name())
		}

		// Wire the parser state maps into a StreamParserState so ParseAndDispatch
		// can manage them.  The maps were already allocated above; we wrap them so
		// both paths share the same underlying maps (no copy).
		ps := &StreamParserState{
			TextBlocks:     textBlocks,
			ToolUseBlocks:  toolUseBlocks,
			ToolIDToName:   toolIDToName,
			ThinkingBlocks: thinkingBlocks,
		}

		ReadLineLoop(stdoutPipe, ReadLineLoopConfig{
			AgentName:      req.AgentName,
			KeepEmptyLines: !isClaudeRuntime, // plain text keeps its paragraph breaks
			OnOverlong: func() {
				bufferedOutputTruncated = true
				readerDropped++
			},
		}, func(line []byte) {
			if isClaudeRuntime {
				res := ParseAndDispatch(line, ps, onChunk)
				if res.Token != "" {
					allText = append(allText, res.Token...)
				}
				if res.IsResult {
					costUSD = res.CostUSD
					usageCost = res.Usage
					nativeSession = res.SessionID
				}
			} else if !inputCeilingHit {
				// Buffered path: feed the harness's LineParser, within the input
				// ceiling (once it is hit the rest is read and discarded). The
				// parser does not retain line; ReadLineLoop reuses its buffer.
				if bufferedInputBytes+len(line)+1 > maxBufferedOutputBytes {
					inputCeilingHit = true
					bufferedOutputTruncated = true
				} else {
					bufferedInputBytes += len(line) + 1
					for _, ev := range bufParser.Feed(line) {
						emitNativeEvent(ev, &streamedText, onChunk)
						tap.observe(ev)
					}
				}
			}
		})

		streamModelID = ps.ModelID

		// Wait for the process and collect exit code.
		waitErr := cmd.Wait()
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				execErr = waitErr
			}
		}

		// For non-claude runtimes the events were emitted as they arrived. What is
		// left is the lines held as possible prose (now known), a marker when
		// something was dropped, a warning for skipped lines, and, if the harness
		// reported a failure, an error chunk the UI renders as "Error: ...".
		if !isClaudeRuntime {
			pr := bufParser.Finish()
			pr.LinesDropped += readerDropped
			parsed = &pr
			if pr.Usage != (runtime.Usage{}) {
				u := pr.Usage
				usageCost = &u
			}
			text := pr.Text
			marker := ""
			if pr.Truncated || bufferedOutputTruncated {
				parsed.Truncated = true
				marker = bufferedTruncationMarker
			}
			text += marker
			switch {
			case pr.PlainText && text != "":
				// A stream that never produced an event is prose (a plugin runtime):
				// its text goes out whole, once it is known not to be a stray line.
				onChunk(StreamChunk{Type: "token", Text: text})
			case !streamedText && pr.Text != "":
				// Structured, but the answer only came on the final frame (agy's
				// single --output-format json envelope): deliver it now.
				onChunk(StreamChunk{Type: "token", Text: text})
			case marker != "":
				onChunk(StreamChunk{Type: "token", Text: marker})
			}
			allText = []byte(text)
			if n := pr.LinesSkipped + pr.LinesDropped; n > 0 {
				// A count only: the skipped lines are never echoed (they may be anything).
				slog.Warn("dispatch: stream: skipped unparseable output lines",
					"runtime", adapter.Name(), "skipped", pr.LinesSkipped, "dropped_over_cap", pr.LinesDropped)
			}
			if pr.Error != "" {
				onChunk(StreamChunk{Type: "error", Text: pr.Error})
			}
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

	noteRun(ctx, req.Project, req.Runtime, exitCode, execErr)
	if tap != nil && tap.cancelled != "" {
		onChunk(StreamChunk{Type: "error", Text: "dispatch cancelled: critical finding in the run's output (kill_on_critical)"})
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
		ExitCode:        exitCode,
		DurationS:       durationS,
		OutputBytes:     outputBytes,
		TaskBytes:       int64(len(req.Task)),
		StderrTail:      stderrTail,
		StderrTrunc:     stderrTrunc,
		Usage:           usageCost,
		ModelChosenBy:   req.ModelChosenBy,
		ModelResolved:   req.ModelResolved,
		RuntimeChosenBy: req.RuntimeChosenBy,
		FallbackFrom:    req.FallbackFrom,
		RouteRule:       req.RouteRule,
		RouteReason:     req.RouteReason,
		RouteClass:      req.RouteClass,
		PolicySHA:       req.PolicySHA,
	}
	// K-135 typed output. A buffered runtime's parse is authoritative; for claude
	// the streamed text is what the deltas delivered.
	if parsed != nil {
		result.applyParsed(adapter.Name(), *parsed)
	} else {
		result.Runtime = adapter.Name()
		result.Provider = providerForRuntime(result.Runtime)
		result.Parsed = true
		result.Text = string(allText)
		// A streamed turn carries the text its deltas delivered, every message of
		// it; the result frame's final report is not separated out on this path.
		result.TextAll = result.Text
		// claude's own session id, from the result frame, for a chat handler to
		// resume the conversation with.
		result.SessionID = nativeSession
		// and the concrete model id (K-136: the ledger records it).
		result.ModelID = streamModelID
	}

	if tap != nil {
		result.ScanFindings, result.CancelReason, result.ScanOffReason = tap.findings, tap.cancelled, tap.offReason
	}

	// Finish the ledger entry identically to Run (parity invariant).
	acct.FinishAt(result, tsEnd)

	// Emit the terminal summary chunk.
	onChunk(StreamChunk{
		Type:            "summary",
		ExitCode:        exitCode,
		DurationS:       durationS,
		OutputBytes:     outputBytes,
		ModelResolved:   req.ModelResolved,
		TotalCostUSD:    costUSD,
		RuntimeResolved: req.Runtime,
		Usage:           usageCost,
		NativeSessionID: result.SessionID,
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

// emitThinkingChunk translates a runtime.ThinkingEvent into a StreamChunk and
// calls onChunk.  No truncation is applied here: the 64 KiB cap is enforced by
// the parser (runtime.ParseStreamLineWithThinking / maxThinkingBytes); the
// ThinkingTruncated flag is the parser's signal that the cap was hit on this
// delta.  redacted_thinking blocks arrive with Redacted=true and a fixed
// placeholder — no text cap applies to them.
func emitThinkingChunk(te *runtime.ThinkingEvent, onChunk func(StreamChunk)) {
	onChunk(StreamChunk{
		Type:              "thinking",
		Thinking:          te.Text,
		ThinkingTruncated: te.Truncated,
		ThinkingRedacted:  te.Redacted,
	})
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

// emitNativeEvent turns one decoded runtime.NativeEvent into its stream chunk.
// Session, result and error events carry nothing live: the summary chunk has
// the session id and usage, and the error chunk is sent once, from the parse
// outcome, so a retry notice does not look like a failed turn.
func emitNativeEvent(ev runtime.NativeEvent, streamedText *bool, onChunk func(StreamChunk)) {
	switch ev.Kind {
	case runtime.EventToken:
		if ev.Plain {
			return // raw stdout of a stream not (yet) known to be structured; see Finish
		}
		onChunk(StreamChunk{Type: "token", Text: ev.Text})
		*streamedText = true
	case runtime.EventThinking:
		onChunk(StreamChunk{Type: "thinking", Thinking: ev.Text})
	case runtime.EventToolUse:
		emitToolChunk(&runtime.ToolEvent{Kind: "tool_use", ToolName: ev.ToolName, Input: ev.ToolInput}, onChunk)
	case runtime.EventToolResult:
		emitToolChunk(&runtime.ToolEvent{Kind: "tool_result", ToolName: ev.ToolName, Output: ev.ToolOutput, IsError: ev.IsError}, onChunk)
	}
}

// knowledgePersona picks the persona text of a chat turn (K-149): the stored
// knowledge block replaces the agent body on codex and agy. claude is never
// touched, so its --append-system-prompt bytes stay what they were; an agy turn
// that resumes a native agy session sends none, because that conversation
// already holds the block from its first turn.
//
// Which path skips: only a RunStream call whose Params.NativeSessions names an
// agy id, i.e. a turn of an interactive agy pane (interactive.ResumeEngine,
// K-147). The one-shot console path (chat_handler's direct RunStream) never
// passes NativeSessions for agy, so it sends the stored block on every turn.
func knowledgePersona(rt string, p Params, persona string, resumed bool) string {
	if p.Knowledge == "" || (rt != "codex" && rt != "agy") {
		return persona
	}
	if rt == "agy" && resumed {
		return ""
	}
	return p.Knowledge
}
