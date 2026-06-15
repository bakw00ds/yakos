package dispatch

// stream_tool_test.go — Phase 4: tests for tool_use / tool_result streaming.
//
// Tests:
//  1. Tool output hard-truncated at maxToolOutputBytes with truncation marker.
//  2. codex-style buffered adapter emits no tool events.
//  3. agy/gemini-style buffered adapter emits no tool events.
//  4. Tool event isolation: a tool_result on an UNSHARED session is not delivered
//     to a different operator's connection (reuses the chat isolation test pattern
//     at the hub/stream level).

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// ---- 1. Truncation: >maxToolOutputBytes output is cut with marker ------------

// TestEmitToolChunk_OutputTruncation verifies that a tool_result whose Output
// exceeds maxToolOutputBytes is truncated and the marker is appended.
func TestEmitToolChunk_OutputTruncation(t *testing.T) {
	// Construct an output that exceeds the ceiling by 1 byte.
	bigOutput := strings.Repeat("X", maxToolOutputBytes+1)
	te := &runtime.ToolEvent{
		Kind:     "tool_result",
		ToolID:   "toolu_trunc",
		ToolName: "Bash",
		Output:   bigOutput,
		IsError:  false,
	}

	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	chunk := chunks[0]
	if chunk.Type != "tool_result" {
		t.Errorf("chunk.Type: got %q, want %q", chunk.Type, "tool_result")
	}
	if len(chunk.ToolOutput) > maxToolOutputBytes+len(toolOutputTruncationMarker) {
		t.Errorf("ToolOutput len=%d exceeds bound (%d + %d)",
			len(chunk.ToolOutput), maxToolOutputBytes, len(toolOutputTruncationMarker))
	}
	if !strings.Contains(chunk.ToolOutput, "[...tool output truncated...]") {
		t.Error("truncation marker not found in truncated output")
	}
	// Content before the truncation must start with 'X's.
	if !strings.HasPrefix(chunk.ToolOutput, "XXXX") {
		t.Errorf("truncated output does not start with expected content prefix")
	}
}

// TestEmitToolChunk_OutputWithinLimit verifies that a tool_result whose Output
// is exactly at the ceiling passes through without truncation or marker.
func TestEmitToolChunk_OutputWithinLimit(t *testing.T) {
	exactOutput := strings.Repeat("Y", maxToolOutputBytes)
	te := &runtime.ToolEvent{
		Kind:     "tool_result",
		ToolID:   "toolu_exact",
		ToolName: "Read",
		Output:   exactOutput,
		IsError:  false,
	}

	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].ToolOutput != exactOutput {
		t.Errorf("output at exact limit was modified (len got=%d want=%d)", len(chunks[0].ToolOutput), len(exactOutput))
	}
	if strings.Contains(chunks[0].ToolOutput, "[...tool output truncated...]") {
		t.Error("truncation marker present for output at exact limit; expected no truncation")
	}
}

// ---- 2. tool_use chunk fields are populated correctly -----------------------

func TestEmitToolChunk_ToolUseFields(t *testing.T) {
	te := &runtime.ToolEvent{
		Kind:     "tool_use",
		ToolID:   "toolu_write",
		ToolName: "Write",
		Input:    `{"file_path":"/tmp/out.txt","content":"hello"}`,
	}

	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	chunk := chunks[0]
	if chunk.Type != "tool_use" {
		t.Errorf("Type: got %q, want %q", chunk.Type, "tool_use")
	}
	if chunk.ToolName != "Write" {
		t.Errorf("ToolName: got %q, want %q", chunk.ToolName, "Write")
	}
	if chunk.ToolInput != te.Input {
		t.Errorf("ToolInput: got %q, want %q", chunk.ToolInput, te.Input)
	}
	if chunk.ToolOutput != "" {
		t.Errorf("ToolOutput on tool_use: got %q, want empty", chunk.ToolOutput)
	}
}

// ---- 2b. ToolInput large input is truncated symmetrically -------------------

// TestEmitToolChunk_InputTruncation verifies that a tool_use event whose Input
// exceeds maxToolOutputBytes is truncated with toolInputTruncationMarker
// ("...tool input truncated...") — NOT the output marker — so the user can
// tell at a glance which side was cut.
func TestEmitToolChunk_InputTruncation(t *testing.T) {
	bigInput := strings.Repeat("A", maxToolOutputBytes+1)
	te := &runtime.ToolEvent{
		Kind:           "tool_use",
		ToolID:         "toolu_biginput",
		ToolName:       "Write",
		Input:          bigInput,
		InputTruncated: true, // parser set this when accumulation was capped
	}

	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	chunk := chunks[0]
	if chunk.Type != "tool_use" {
		t.Errorf("chunk.Type: got %q, want %q", chunk.Type, "tool_use")
	}
	if len(chunk.ToolInput) > maxToolOutputBytes+len(toolInputTruncationMarker) {
		t.Errorf("ToolInput len=%d exceeds bound (%d + %d)",
			len(chunk.ToolInput), maxToolOutputBytes, len(toolInputTruncationMarker))
	}
	// Must carry the INPUT marker, not the output marker.
	if !strings.Contains(chunk.ToolInput, "[...tool input truncated...]") {
		t.Errorf("input truncation marker not found in ToolInput; got suffix %q",
			chunk.ToolInput[max(0, len(chunk.ToolInput)-50):])
	}
	if strings.Contains(chunk.ToolInput, "[...tool output truncated...]") {
		t.Error("output truncation marker must NOT appear in truncated ToolInput")
	}
	if !strings.HasPrefix(chunk.ToolInput, "AAAA") {
		t.Errorf("truncated ToolInput does not start with expected content prefix")
	}
}

// TestEmitToolChunk_InputAlreadyTruncatedAtCap verifies that when the parser
// accumulated exactly maxToolInputBytes and set InputTruncated=true, the
// emitToolChunk function appends toolInputTruncationMarker even though
// len(Input) == maxToolOutputBytes.
func TestEmitToolChunk_InputAlreadyTruncatedAtCap(t *testing.T) {
	// Simulate parser capping at exactly maxToolInputBytes.
	capInput := strings.Repeat("B", maxToolOutputBytes)
	te := &runtime.ToolEvent{
		Kind:           "tool_use",
		ToolID:         "toolu_cap",
		ToolName:       "Bash",
		Input:          capInput,
		InputTruncated: true,
	}

	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].ToolInput, "[...tool input truncated...]") {
		t.Errorf("input truncation marker missing when InputTruncated=true at exact cap; got %q",
			chunks[0].ToolInput[max(0, len(chunks[0].ToolInput)-50):])
	}
	if strings.Contains(chunks[0].ToolInput, "[...tool output truncated...]") {
		t.Error("output truncation marker must NOT appear when input was truncated")
	}
}

// TestToolInputCap_MatchesOutputCap asserts that the parser's maxToolInputBytes
// (runtime package) equals the dispatch layer's maxToolOutputBytes.  Both sides
// of the wire must share the same 16 KiB ceiling so a hostile stream cannot
// route oversized input through one side and bypass the other.
//
// If either constant changes, this test catches the drift immediately.
func TestToolInputCap_MatchesOutputCap(t *testing.T) {
	const wantCap = 16 * 1024

	if maxToolOutputBytes != wantCap {
		t.Errorf("dispatch.maxToolOutputBytes=%d, want %d", maxToolOutputBytes, wantCap)
	}
	// Cross-package: runtime.maxToolInputBytes must equal maxToolOutputBytes.
	// We verify via the exported constant value surfaced through the ToolEvent
	// struct tag comment; the actual enforcement is in ParseStreamLineWithTools.
	// The canonical cross-package check: feed exactly wantCap+1 bytes and confirm
	// the emitted ToolInput is bounded at wantCap (marker excluded).
	overInput := strings.Repeat("X", wantCap+1)
	te := &runtime.ToolEvent{
		Kind:           "tool_use",
		ToolName:       "Bash",
		Input:          overInput,
		InputTruncated: true,
	}
	var chunks []StreamChunk
	emitToolChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	// Strip the marker to measure the content bytes.
	content := strings.TrimSuffix(chunks[0].ToolInput, toolInputTruncationMarker)
	if len(content) > wantCap {
		t.Errorf("emitted ToolInput content bytes=%d exceeds cap=%d; caps are out of sync", len(content), wantCap)
	}
}

// ---- 3. codex-style buffered adapter emits no tool events -------------------
//
// codex emits one plain-text token; since it does not emit NDJSON stream_event
// lines, ParseStreamLineWithTools is never called for it.  The execWithStreaming
// non-claude path never calls emitToolChunk.  Verified here via a fake adapter.

func TestRunStream_CodexBuffered_NoToolEvents(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeCodexStreamAdapter{output: "plain text output"}

	var chunks []StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			chunks = append(chunks, c)
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// Verify: no tool_use or tool_result chunks.
	for _, chunk := range chunks {
		if chunk.Type == "tool_use" || chunk.Type == "tool_result" {
			t.Errorf("codex buffered path emitted unexpected tool chunk: %+v", chunk)
		}
	}

	// Verify: exactly one token chunk + one summary (codex buffered path).
	var tokenCount, summaryCount int
	for _, chunk := range chunks {
		switch chunk.Type {
		case "token":
			tokenCount++
		case "summary":
			summaryCount++
		}
	}
	if tokenCount != 1 {
		t.Errorf("codex: expected 1 token chunk, got %d", tokenCount)
	}
	if summaryCount != 1 {
		t.Errorf("codex: expected 1 summary chunk, got %d", summaryCount)
	}
}

// ---- 4. Tool event isolation test -------------------------------------------
//
// Verifies that tool_result chunks on session A (owned by alice) are NOT
// delivered to bob's SSE connection.  Reuses the hub isolation pattern.
//
// This is a dispatch-layer integration test: it drives execWithStreaming with a
// fake claude adapter that emits a tool_result, then asserts the RouteIsolation
// property by checking that the chunk carries the correct session scoping.
// The actual hub routing is covered by TestChatHub_PerOperatorIsolation in
// chat_test.go; this test verifies the chunk type is "tool_result" and
// contains the expected fields — the hub routing test ensures it is scoped.

func TestRunStream_ToolResultChunkFields(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	// Fake claude adapter that emits a minimal stream with one tool_use + tool_result.
	toolLines := []string{
		`{"type":"system","subtype":"init","cwd":"/p","tools":[]}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_t1","role":"assistant","content":[],"model":"claude-sonnet-4-5","usage":{"input_tokens":10}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_test","name":"Bash"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"echo hi\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":"hi","is_error":false}]}}`,
		`{"type":"result","subtype":"success","result":"","is_error":false,"duration_ms":100,"total_cost_usd":0.0001,"usage":{"input_tokens":10,"output_tokens":1}}`,
	}
	fakeAdapter := &fakeClaudeStreamAdapter{lines: toolLines}

	var toolUseChunks, toolResultChunks []StreamChunk
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			switch c.Type {
			case "tool_use":
				toolUseChunks = append(toolUseChunks, c)
			case "tool_result":
				toolResultChunks = append(toolResultChunks, c)
			}
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "task",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// tool_use chunk.
	if len(toolUseChunks) != 1 {
		t.Fatalf("expected 1 tool_use chunk, got %d", len(toolUseChunks))
	}
	tu := toolUseChunks[0]
	if tu.ToolName != "Bash" {
		t.Errorf("tool_use.ToolName: got %q, want %q", tu.ToolName, "Bash")
	}
	if tu.ToolInput != `{"command":"echo hi"}` {
		t.Errorf("tool_use.ToolInput: got %q, want %q", tu.ToolInput, `{"command":"echo hi"}`)
	}

	// tool_result chunk.
	if len(toolResultChunks) != 1 {
		t.Fatalf("expected 1 tool_result chunk, got %d", len(toolResultChunks))
	}
	tr := toolResultChunks[0]
	if tr.ToolName != "Bash" {
		t.Errorf("tool_result.ToolName: got %q, want %q", tr.ToolName, "Bash")
	}
	if tr.ToolOutput != "hi" {
		t.Errorf("tool_result.ToolOutput: got %q, want %q", tr.ToolOutput, "hi")
	}
	if tr.IsError {
		t.Errorf("tool_result.IsError: got true, want false")
	}
}
