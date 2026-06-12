package runtime

import (
	"testing"
)

// TestParseStreamLine_TextDelta verifies that a content_block_delta event for a
// confirmed text block produces a non-empty text token.
func TestParseStreamLine_TextDelta(t *testing.T) {
	textBlocks := map[int]struct{}{}

	// First: a content_block_start that marks block 0 as a text block.
	startLine := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`)
	tok, isResult, cost := ParseStreamLine(startLine, textBlocks)
	if tok != "" {
		t.Errorf("content_block_start: expected empty token, got %q", tok)
	}
	if isResult {
		t.Error("content_block_start: expected isResult=false")
	}
	if cost != 0 {
		t.Errorf("content_block_start: expected cost=0, got %f", cost)
	}
	if _, ok := textBlocks[0]; !ok {
		t.Error("content_block_start: block 0 should be registered as text block")
	}

	// Now: a content_block_delta for that text block.
	deltaLine := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello, world!"}}}`)
	tok, isResult, cost = ParseStreamLine(deltaLine, textBlocks)
	if tok != "Hello, world!" {
		t.Errorf("content_block_delta: expected %q, got %q", "Hello, world!", tok)
	}
	if isResult {
		t.Error("content_block_delta: expected isResult=false")
	}
	if cost != 0 {
		t.Errorf("content_block_delta: expected cost=0, got %f", cost)
	}
}

// TestParseStreamLine_ThinkingBlockDropped verifies that deltas from a
// non-text block (e.g. "thinking") are NOT emitted.
func TestParseStreamLine_ThinkingBlockDropped(t *testing.T) {
	textBlocks := map[int]struct{}{}

	// Register block 0 as "thinking" (not text).
	thinkingStart := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	ParseStreamLine(thinkingStart, textBlocks)

	if _, ok := textBlocks[0]; ok {
		t.Error("thinking block should NOT be registered as a text block")
	}

	// A delta on block 0 should be dropped.
	deltaLine := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private thoughts"}}}`)
	tok, _, _ := ParseStreamLine(deltaLine, textBlocks)
	if tok != "" {
		t.Errorf("thinking delta: expected empty token, got %q", tok)
	}
}

// TestParseStreamLine_SystemEventDropped verifies that the multi-KB system/init
// event is silently dropped (returns empty token, not a result).
func TestParseStreamLine_SystemEventDropped(t *testing.T) {
	textBlocks := map[int]struct{}{}

	sysLine := []byte(`{"type":"system","subtype":"init","cwd":"/project","tools":[{"name":"Bash"},{"name":"Read"}],"mcp_servers":[]}`)
	tok, isResult, cost := ParseStreamLine(sysLine, textBlocks)
	if tok != "" {
		t.Errorf("system event: expected empty token, got %q", tok)
	}
	if isResult {
		t.Error("system event: expected isResult=false")
	}
	if cost != 0 {
		t.Errorf("system event: expected cost=0, got %f", cost)
	}
}

// TestParseStreamLine_ResultEvent verifies that the terminal result event
// sets isResult=true and extracts total_cost_usd.
func TestParseStreamLine_ResultEvent(t *testing.T) {
	textBlocks := map[int]struct{}{}

	resultLine := []byte(`{"type":"result","subtype":"success","result":"Final answer.","is_error":false,"duration_ms":4200,"total_cost_usd":0.0123,"usage":{"input_tokens":100,"output_tokens":50}}`)
	tok, isResult, cost := ParseStreamLine(resultLine, textBlocks)
	if tok != "" {
		t.Errorf("result event: expected empty token, got %q", tok)
	}
	if !isResult {
		t.Error("result event: expected isResult=true")
	}
	if cost != 0.0123 {
		t.Errorf("result event: expected cost=0.0123, got %f", cost)
	}
}

// TestParseStreamLine_MessageStopDropped verifies that message_stop and other
// non-delta events don't produce tokens.
func TestParseStreamLine_MessageStopDropped(t *testing.T) {
	textBlocks := map[int]struct{}{}

	stopLine := []byte(`{"type":"stream_event","event":{"type":"message_stop"}}`)
	tok, isResult, _ := ParseStreamLine(stopLine, textBlocks)
	if tok != "" {
		t.Errorf("message_stop: expected empty token, got %q", tok)
	}
	if isResult {
		t.Error("message_stop: expected isResult=false")
	}
}

// TestParseStreamLine_MultipleTextBlocks verifies that two text blocks at
// different indices both produce tokens.
func TestParseStreamLine_MultipleTextBlocks(t *testing.T) {
	textBlocks := map[int]struct{}{}

	// Register blocks 0 and 2 as text; block 1 as thinking.
	ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`), textBlocks)
	ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}}`), textBlocks)
	ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}}`), textBlocks)

	tok0, _, _ := ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"A"}}}`), textBlocks)
	tok1, _, _ := ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"B"}}}`), textBlocks)
	tok2, _, _ := ParseStreamLine([]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"C"}}}`), textBlocks)

	if tok0 != "A" {
		t.Errorf("block 0 (text): expected %q, got %q", "A", tok0)
	}
	if tok1 != "" {
		t.Errorf("block 1 (thinking): expected empty, got %q", tok1)
	}
	if tok2 != "C" {
		t.Errorf("block 2 (text): expected %q, got %q", "C", tok2)
	}
}

// TestParseStreamLine_EmptyLine verifies empty input returns zeroed output.
func TestParseStreamLine_EmptyLine(t *testing.T) {
	textBlocks := map[int]struct{}{}
	tok, isResult, cost := ParseStreamLine(nil, textBlocks)
	if tok != "" || isResult || cost != 0 {
		t.Errorf("empty line: got (%q, %v, %f), want (\"\", false, 0)", tok, isResult, cost)
	}
}
