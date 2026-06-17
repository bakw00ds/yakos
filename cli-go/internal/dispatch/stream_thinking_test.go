package dispatch

// stream_thinking_test.go — tests for the thinking StreamChunk pathway.
//
// Tests:
//  1. Fixture-based: fake claude adapter emitting a thinking stream → "thinking"
//     chunks surfaced; "token" + "summary" chunks unchanged.
//  2. Redacted thinking → StreamChunk.ThinkingRedacted==true; no panic.
//  3. Thinking chunks are NOT delivered cross-operator on an unshared session
//     (verifies same scoping as token/tool — relies on StreamChunk.Type=="thinking").
//  4. codex-style buffered adapter emits no thinking chunks.
//  5. emitThinkingChunk fields: Text, ThinkingTruncated, ThinkingRedacted populated.

import (
	"context"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// ---- thinking stream fixture lines ------------------------------------------

func thinkingStreamLines() []string {
	return []string{
		`{"type":"system","subtype":"init","cwd":"/p","tools":[]}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_th1","role":"assistant","content":[],"model":"claude-sonnet-4-5","usage":{"input_tokens":20}}}}`,
		// thinking block at index 0
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me reason."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" Done."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		// text block at index 1
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"result","subtype":"success","result":"Answer.","is_error":false,"duration_ms":500,"total_cost_usd":0.00055,"usage":{"input_tokens":20,"output_tokens":5}}`,
	}
}

func redactedThinkingStreamLines() []string {
	return []string{
		`{"type":"system","subtype":"init","cwd":"/p","tools":[]}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_rth1","role":"assistant","content":[],"model":"claude-sonnet-4-5","usage":{"input_tokens":15}}}}`,
		// redacted_thinking block at index 0
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"<opaque>"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		// text block at index 1
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Safe."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"result","subtype":"success","result":"Safe.","is_error":false,"duration_ms":200,"total_cost_usd":0.00022,"usage":{"input_tokens":15,"output_tokens":3}}`,
	}
}

// ---- 1. Thinking stream: chunks surfaced -------------------------------------

func TestRunStream_ThinkingChunksSurfaced(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: thinkingStreamLines()}

	var thinkingChunks []StreamChunk
	var tokenChunks []StreamChunk
	var summaryChunks []StreamChunk

	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			switch c.Type {
			case "thinking":
				thinkingChunks = append(thinkingChunks, c)
			case "token":
				tokenChunks = append(tokenChunks, c)
			case "summary":
				summaryChunks = append(summaryChunks, c)
			}
			onChunk(c)
		})
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{
			Agent:   "chat-agent",
			Task:    "think",
			Project: logDir,
		}, func(_ StreamChunk) {})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
	})

	// Two thinking deltas from the fixture.
	if len(thinkingChunks) != 2 {
		t.Fatalf("expected 2 thinking chunks, got %d: %v", len(thinkingChunks), thinkingChunks)
	}
	if thinkingChunks[0].Thinking != "Let me reason." {
		t.Errorf("thinking[0].Thinking: got %q, want %q", thinkingChunks[0].Thinking, "Let me reason.")
	}
	if thinkingChunks[1].Thinking != " Done." {
		t.Errorf("thinking[1].Thinking: got %q, want %q", thinkingChunks[1].Thinking, " Done.")
	}
	for i, c := range thinkingChunks {
		if c.ThinkingRedacted {
			t.Errorf("thinking[%d].ThinkingRedacted: got true, want false", i)
		}
		if c.ThinkingTruncated {
			t.Errorf("thinking[%d].ThinkingTruncated: got true, want false (within cap)", i)
		}
	}

	// Token chunk still present.
	if len(tokenChunks) != 1 || tokenChunks[0].Text != "Answer." {
		t.Errorf("token chunks: got %v, want [\"Answer.\"]", tokenChunks)
	}

	// Summary still present.
	if len(summaryChunks) != 1 {
		t.Fatalf("expected 1 summary chunk, got %d", len(summaryChunks))
	}
	if summaryChunks[0].TotalCostUSD != 0.00055 {
		t.Errorf("summary.TotalCostUSD: got %f, want 0.00055", summaryChunks[0].TotalCostUSD)
	}
}

// ---- 2. Redacted thinking: placeholder emitted, no panic ---------------------

func TestRunStream_RedactedThinking(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "You are a helpful assistant.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: redactedThinkingStreamLines()}

	var thinkingChunks []StreamChunk
	var tokenChunks []StreamChunk

	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			switch c.Type {
			case "thinking":
				thinkingChunks = append(thinkingChunks, c)
			case "token":
				tokenChunks = append(tokenChunks, c)
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

	if len(thinkingChunks) != 1 {
		t.Fatalf("expected 1 thinking chunk (redacted), got %d", len(thinkingChunks))
	}
	if !thinkingChunks[0].ThinkingRedacted {
		t.Error("ThinkingRedacted: got false, want true for redacted_thinking block")
	}
	if thinkingChunks[0].Thinking == "" {
		t.Error("Thinking: got empty, want non-empty placeholder for redacted block")
	}

	// Text token still present.
	if len(tokenChunks) != 1 || tokenChunks[0].Text != "Safe." {
		t.Errorf("token chunks: got %v, want [\"Safe.\"]", tokenChunks)
	}
}

// ---- 3. Thinking scoped same as token (StreamChunk.Type sanity check) --------

// TestRunStream_ThinkingChunkType verifies that thinking chunks carry Type=="thinking",
// which is the selector used by the consoleui layer to route them with the same
// owner-scoping as token/tool events.  This is a structural check — the actual
// hub routing isolation is tested in chat_test.go.
func TestRunStream_ThinkingChunkType(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeClaudeStreamAdapter{lines: thinkingStreamLines()}

	var thinkingChunkTypes []string
	withStreamRunFn(func(ctx context.Context, req Request, _ runtime.Adapter, chatReq runtime.ChatDispatchRequest, onChunk func(StreamChunk)) (Result, error) {
		return execWithStreaming(ctx, req, fakeAdapter, chatReq, func(c StreamChunk) {
			if c.Type == "thinking" {
				thinkingChunkTypes = append(thinkingChunkTypes, c.Type)
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

	for i, typ := range thinkingChunkTypes {
		if typ != "thinking" {
			t.Errorf("chunk[%d].Type: got %q, want %q", i, typ, "thinking")
		}
	}
	if len(thinkingChunkTypes) == 0 {
		t.Error("expected at least one thinking chunk from thinking stream fixture")
	}
}

// ---- 4. codex-style buffered adapter emits no thinking chunks ---------------

func TestRunStream_CodexBuffered_NoThinkingChunks(t *testing.T) {
	logDir := isolatedLogDir(t)
	yakosRoot := buildFakeRoster(t, "chat-agent", "System prompt.")

	svc := NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: logDir,
		OperatorID:    "test-op",
	})

	fakeAdapter := &fakeCodexStreamAdapter{output: "plain output"}

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

	for _, chunk := range chunks {
		if chunk.Type == "thinking" {
			t.Errorf("codex buffered path emitted unexpected thinking chunk: %+v", chunk)
		}
	}
}

// ---- 5. emitThinkingChunk fields populated correctly -----------------------

func TestEmitThinkingChunk_Fields(t *testing.T) {
	te := &runtime.ThinkingEvent{
		Text:      "Let me think...",
		Truncated: false,
		Redacted:  false,
	}
	var chunks []StreamChunk
	emitThinkingChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	c := chunks[0]
	if c.Type != "thinking" {
		t.Errorf("Type: got %q, want %q", c.Type, "thinking")
	}
	if c.Thinking != "Let me think..." {
		t.Errorf("Thinking: got %q, want %q", c.Thinking, "Let me think...")
	}
	if c.ThinkingTruncated {
		t.Error("ThinkingTruncated: got true, want false")
	}
	if c.ThinkingRedacted {
		t.Error("ThinkingRedacted: got true, want false")
	}
}

func TestEmitThinkingChunk_TruncatedFields(t *testing.T) {
	bigText := strings.Repeat("X", 100)
	te := &runtime.ThinkingEvent{
		Text:      bigText,
		Truncated: true,
		Redacted:  false,
	}
	var chunks []StreamChunk
	emitThinkingChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !chunks[0].ThinkingTruncated {
		t.Error("ThinkingTruncated: got false, want true")
	}
	if chunks[0].ThinkingRedacted {
		t.Error("ThinkingRedacted: got true, want false for truncated (non-redacted) chunk")
	}
}

func TestEmitThinkingChunk_RedactedFields(t *testing.T) {
	te := &runtime.ThinkingEvent{
		Text:     "[redacted thinking]",
		Redacted: true,
	}
	var chunks []StreamChunk
	emitThinkingChunk(te, func(c StreamChunk) { chunks = append(chunks, c) })

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !chunks[0].ThinkingRedacted {
		t.Error("ThinkingRedacted: got false, want true")
	}
	if chunks[0].ThinkingTruncated {
		t.Error("ThinkingTruncated: got true, want false for redacted chunk")
	}
}
