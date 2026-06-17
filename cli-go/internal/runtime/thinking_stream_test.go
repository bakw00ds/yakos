package runtime

// thinking_stream_test.go — tests for ParseStreamLineWithThinking.
//
// Tests:
//  1. Fixture test: thinking stream → thinking deltas surfaced; text + usage unchanged.
//  2. Redacted-thinking fixture → placeholder emitted; text token present; no panic.
//  3. Thinking with nil thinkingBlocks → thinking delta silently dropped (legacy compat).
//  4. ParseStreamLineWithTools drops thinking (legacy path unchanged).
//  5. ParseStreamLineWithUsage shim is byte-identical with thinking present.
//  6. Thinking text capped at maxThinkingBytes; ThinkingEvent.Truncated is set.
//  7. Multiple thinking blocks at different indices accumulate independently.
//  8. Empty thinking_delta → no ThinkingEvent emitted.

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ---- 1. Fixture test: thinking stream ----------------------------------------

// TestParseStreamLineWithThinking_Fixture feeds the recorded
// claude_thinking_stream.ndjson fixture line-by-line and asserts:
//   - Two ThinkingEvents are emitted with the correct delta text.
//   - One text token "The answer is 42." is emitted.
//   - The final result event returns isResult==true with the correct cost/usage.
//   - No ThinkingEvent has Redacted==true.
func TestParseStreamLineWithThinking_Fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_thinking_stream.ndjson")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)
	thinkingBlocks := make(map[int]*ThinkingBlockEntry)

	var textTokens []string
	var thinkingEvents []*ThinkingEvent
	var finalCostUSD float64
	var finalInputTokens, finalOutputTokens int64

	for _, rawLine := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimRight(rawLine, "\r")
		if len(line) == 0 {
			continue
		}
		tok, isResult, costUSD, usage, _, thEv := ParseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
		if tok != "" {
			textTokens = append(textTokens, tok)
		}
		if isResult {
			finalCostUSD = costUSD
			if usage != nil {
				finalInputTokens = usage.InputTokens
				finalOutputTokens = usage.OutputTokens
			}
		}
		if thEv != nil {
			cp := *thEv
			thinkingEvents = append(thinkingEvents, &cp)
		}
	}

	// Two thinking deltas from the fixture.
	if len(thinkingEvents) != 2 {
		t.Fatalf("expected 2 ThinkingEvents, got %d: %v", len(thinkingEvents), thinkingEvents)
	}
	if thinkingEvents[0].Text != "Let me think about this." {
		t.Errorf("thinking[0].Text: got %q, want %q", thinkingEvents[0].Text, "Let me think about this.")
	}
	if thinkingEvents[1].Text != " The answer is 42." {
		t.Errorf("thinking[1].Text: got %q, want %q", thinkingEvents[1].Text, " The answer is 42.")
	}
	for i, ev := range thinkingEvents {
		if ev.Redacted {
			t.Errorf("thinking[%d].Redacted: got true, want false", i)
		}
		if ev.Truncated {
			t.Errorf("thinking[%d].Truncated: got true, want false (within cap)", i)
		}
	}

	// Text token unchanged.
	if len(textTokens) != 1 || textTokens[0] != "The answer is 42." {
		t.Errorf("text tokens: got %v, want [\"The answer is 42.\"]", textTokens)
	}

	// Usage unchanged.
	if finalCostUSD != 0.00077 {
		t.Errorf("finalCostUSD: got %f, want 0.00077", finalCostUSD)
	}
	if finalInputTokens != 30 {
		t.Errorf("finalInputTokens: got %d, want 30", finalInputTokens)
	}
	if finalOutputTokens != 10 {
		t.Errorf("finalOutputTokens: got %d, want 10", finalOutputTokens)
	}
}

// ---- 2. Redacted-thinking fixture -------------------------------------------

// TestParseStreamLineWithThinking_RedactedFixture feeds the
// claude_redacted_thinking_stream.ndjson fixture and asserts:
//   - One ThinkingEvent with Redacted==true and Text==redactedThinkingPlaceholder.
//   - One text token "Done.".
//   - No panic.
func TestParseStreamLineWithThinking_RedactedFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_redacted_thinking_stream.ndjson")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)
	thinkingBlocks := make(map[int]*ThinkingBlockEntry)

	var textTokens []string
	var thinkingEvents []*ThinkingEvent

	for _, rawLine := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimRight(rawLine, "\r")
		if len(line) == 0 {
			continue
		}
		tok, _, _, _, _, thEv := ParseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
		if tok != "" {
			textTokens = append(textTokens, tok)
		}
		if thEv != nil {
			cp := *thEv
			thinkingEvents = append(thinkingEvents, &cp)
		}
	}

	if len(thinkingEvents) != 1 {
		t.Fatalf("expected 1 redacted ThinkingEvent, got %d", len(thinkingEvents))
	}
	ev := thinkingEvents[0]
	if !ev.Redacted {
		t.Error("ThinkingEvent.Redacted: got false, want true for redacted_thinking block")
	}
	if ev.Text != redactedThinkingPlaceholder {
		t.Errorf("ThinkingEvent.Text: got %q, want %q", ev.Text, redactedThinkingPlaceholder)
	}
	if len(textTokens) != 1 || textTokens[0] != "Done." {
		t.Errorf("text tokens: got %v, want [\"Done.\"]", textTokens)
	}
}

// ---- 3. nil thinkingBlocks → thinking delta silently dropped ----------------

func TestParseStreamLineWithThinking_NilThinkingBlocks(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	lines := [][]byte{
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"secret thoughts"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`),
	}

	for _, line := range lines {
		_, _, _, _, _, thEv := ParseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, nil)
		if thEv != nil {
			t.Errorf("expected nil ThinkingEvent with nil thinkingBlocks, got %+v", thEv)
		}
	}
}

// ---- 4. ParseStreamLineWithTools drops thinking (legacy path unchanged) ------

// TestParseStreamLineWithTools_ThinkingDropped verifies that the legacy
// ParseStreamLineWithTools function (which takes no thinkingBlocks map) still
// drops thinking events without any change to its existing return values.
func TestParseStreamLineWithTools_ThinkingDropped(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	lines := [][]byte{
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`),
		// a text token after thinking to verify text still works
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello"}}}`),
	}

	var gotText string
	for _, line := range lines {
		tok, _, _, _, toolEv := ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
		if tok != "" {
			gotText += tok
		}
		if toolEv != nil {
			t.Errorf("ParseStreamLineWithTools: unexpected tool event from thinking stream: %+v", toolEv)
		}
	}

	if gotText != "hello" {
		t.Errorf("text after thinking block: got %q, want %q", gotText, "hello")
	}
}

// ---- 5. ParseStreamLineWithUsage shim byte-identical with thinking present ---

// TestParseStreamLineWithUsage_UnchangedWithThinkingInStream verifies that the
// ParseStreamLineWithUsage legacy shim returns identical text/isResult/cost/usage
// as before when the stream contains thinking blocks (thinking is simply not
// returned — not an error, not a mutation of existing return values).
func TestParseStreamLineWithUsage_UnchangedWithThinkingInStream(t *testing.T) {
	textBlocks := make(map[int]struct{})

	// Thinking block start (should be ignored by shim).
	thinkStart := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	tok, isResult, cost, usage := ParseStreamLineWithUsage(thinkStart, textBlocks)
	if tok != "" || isResult || cost != 0 || usage != nil {
		t.Errorf("shim on thinking block_start: got (%q,%v,%f,%v); want zeroed", tok, isResult, cost, usage)
	}
	if _, ok := textBlocks[0]; ok {
		t.Error("shim: thinking block should NOT be registered as a text block")
	}

	// Thinking delta (should be ignored by shim).
	thinkDelta := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"private"}}}`)
	tok, isResult, cost, usage = ParseStreamLineWithUsage(thinkDelta, textBlocks)
	if tok != "" || isResult || cost != 0 || usage != nil {
		t.Errorf("shim on thinking_delta: got (%q,%v,%f,%v); want zeroed", tok, isResult, cost, usage)
	}

	// Text block at index 1 still works.
	textBlocks2 := make(map[int]struct{})
	textStart := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`)
	ParseStreamLineWithUsage(textStart, textBlocks2)
	textDelta := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"world"}}}`)
	tok, isResult, cost, usage = ParseStreamLineWithUsage(textDelta, textBlocks2)
	if tok != "world" || isResult || cost != 0 || usage != nil {
		t.Errorf("shim on text_delta: got (%q,%v,%f,%v); want (\"world\",false,0,nil)", tok, isResult, cost, usage)
	}
}

// ---- 6. Thinking text capped at maxThinkingBytes ----------------------------

// TestParseStreamLineWithThinking_ThinkingCapped verifies that accumulated
// thinking text is bounded at maxThinkingBytes.  When a delta would push the
// accumulated total past the cap, the delta is truncated and ThinkingEvent.Truncated
// is set.  Subsequent deltas are silently dropped.
func TestParseStreamLineWithThinking_ThinkingCapped(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)
	thinkingBlocks := make(map[int]*ThinkingBlockEntry)

	// Start a thinking block.
	startLine := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	_, _, _, _, _, thEv := ParseStreamLineWithThinking(startLine, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
	if thEv != nil {
		t.Errorf("block_start: unexpected ThinkingEvent: %+v", thEv)
	}

	// Send deltas totalling maxThinkingBytes + 512 bytes.
	chunkSize := 8192
	totalBytes := maxThinkingBytes + 512
	sent := 0
	var allEvents []*ThinkingEvent
	for sent < totalBytes {
		n := chunkSize
		if sent+n > totalBytes {
			n = totalBytes - sent
		}
		fragment := strings.Repeat("T", n)
		line := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"` + fragment + `"}}}`)
		_, _, _, _, _, thEv2 := ParseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
		if thEv2 != nil {
			cp := *thEv2
			allEvents = append(allEvents, &cp)
		}
		sent += n
	}

	// At least one event must have Truncated==true.
	var foundTruncated bool
	totalTextLen := 0
	for _, ev := range allEvents {
		totalTextLen += len(ev.Text)
		if ev.Truncated {
			foundTruncated = true
		}
	}
	if !foundTruncated {
		t.Error("no ThinkingEvent with Truncated==true; expected cap to trigger")
	}
	if totalTextLen > maxThinkingBytes {
		t.Errorf("total thinking text surfaced (%d bytes) exceeds maxThinkingBytes (%d)", totalTextLen, maxThinkingBytes)
	}

	// Subsequent delta after cap must emit nothing.
	afterDelta := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"should be dropped"}}}`)
	_, _, _, _, _, afterEv := ParseStreamLineWithThinking(afterDelta, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
	if afterEv != nil {
		t.Errorf("delta after cap: expected nil ThinkingEvent, got %+v", afterEv)
	}
}

// ---- 7. Multiple thinking blocks at different indices -----------------------

func TestParseStreamLineWithThinking_MultipleThinkingBlocks(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)
	thinkingBlocks := make(map[int]*ThinkingBlockEntry)

	lines := [][]byte{
		// thinking block at index 0
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"first"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`),
		// thinking block at index 1
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"second"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`),
	}

	var texts []string
	for _, line := range lines {
		_, _, _, _, _, thEv := ParseStreamLineWithThinking(line, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
		if thEv != nil {
			texts = append(texts, thEv.Text)
		}
	}

	if len(texts) != 2 {
		t.Fatalf("expected 2 thinking deltas, got %d: %v", len(texts), texts)
	}
	if texts[0] != "first" {
		t.Errorf("thinking[0]: got %q, want %q", texts[0], "first")
	}
	if texts[1] != "second" {
		t.Errorf("thinking[1]: got %q, want %q", texts[1], "second")
	}
}

// ---- 8. Empty thinking_delta → no ThinkingEvent emitted ---------------------

func TestParseStreamLineWithThinking_EmptyDelta(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)
	thinkingBlocks := make(map[int]*ThinkingBlockEntry)

	// Register a thinking block.
	startLine := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`)
	ParseStreamLineWithThinking(startLine, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)

	// Send an empty thinking_delta.
	emptyDelta := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}}`)
	_, _, _, _, _, thEv := ParseStreamLineWithThinking(emptyDelta, textBlocks, toolUseBlocks, toolIDToName, thinkingBlocks)
	if thEv != nil {
		t.Errorf("empty thinking_delta: expected nil ThinkingEvent, got %+v", thEv)
	}
}
