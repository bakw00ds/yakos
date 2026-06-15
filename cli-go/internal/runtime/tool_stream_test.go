package runtime

// tool_stream_test.go — Phase 4: fixture-driven tests for ParseStreamLineWithTools.
//
// Tests:
//  1. Fixture test: feed the recorded claude_tool_stream.ndjson fixture; assert
//     tool_use + tool_result events are emitted with correct name/input/output
//     AND that existing text tokens + final usage are byte-for-byte unchanged.
//  2. ToolUse name/input correlation: input_json_delta fragments are accumulated
//     correctly across multiple calls.
//  3. ToolResult correlation: tool_result output is matched to the correct ToolID.
//  4. ToolResult is_error flag is propagated.
//  5. Text tokens still emit correctly when interleaved with tool blocks.
//  6. ParseStreamLineWithUsage (legacy shim) is unchanged: passes nil tool maps,
//     returns same text/usage as before.
//  7. Empty line → all zero returns (nil toolEvent).

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ---- 1. Fixture-driven test ---------------------------------------------------

// TestParseStreamLineWithTools_Fixture feeds the recorded claude_tool_stream.ndjson
// fixture line-by-line and asserts:
//   - A tool_use ToolEvent is emitted for the Bash invocation with the correct
//     Name ("Bash") and fully-assembled Input ({"command":"ls -la"}).
//   - A tool_result ToolEvent is emitted with ToolName "Bash" (correlated via id),
//     Output containing the directory listing, IsError==false.
//   - Text tokens "I'll run a command." and "Done." are emitted unchanged.
//   - The final result event returns isResult==true with totalCostUSD==0.00125 and
//     usage.InputTokens==80, usage.OutputTokens==14.
//   - No ToolEvent is emitted for text blocks or the system/init line.
func TestParseStreamLineWithTools_Fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/claude_tool_stream.ndjson")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	var textTokens []string
	var toolUseEvents []*ToolEvent
	var toolResultEvents []*ToolEvent
	var finalCostUSD float64
	var finalInputTokens int64
	var finalOutputTokens int64

	for _, rawLine := range bytes.Split(data, []byte("\n")) {
		line := bytes.TrimRight(rawLine, "\r")
		if len(line) == 0 {
			continue
		}
		tok, isResult, costUSD, usage, toolEv := ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
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
		if toolEv != nil {
			switch toolEv.Kind {
			case "tool_use":
				cp := *toolEv
				toolUseEvents = append(toolUseEvents, &cp)
			case "tool_result":
				cp := *toolEv
				toolResultEvents = append(toolResultEvents, &cp)
			}
		}
	}

	// --- Assert text tokens ---
	if len(textTokens) != 2 {
		t.Errorf("expected 2 text tokens, got %d: %v", len(textTokens), textTokens)
	} else {
		if textTokens[0] != "I'll run a command." {
			t.Errorf("text[0]: got %q, want %q", textTokens[0], "I'll run a command.")
		}
		if textTokens[1] != "Done." {
			t.Errorf("text[1]: got %q, want %q", textTokens[1], "Done.")
		}
	}

	// --- Assert tool_use ---
	if len(toolUseEvents) != 1 {
		t.Fatalf("expected 1 tool_use event, got %d: %v", len(toolUseEvents), toolUseEvents)
	}
	tu := toolUseEvents[0]
	if tu.ToolName != "Bash" {
		t.Errorf("tool_use.ToolName: got %q, want %q", tu.ToolName, "Bash")
	}
	if tu.ToolID != "toolu_01abc123" {
		t.Errorf("tool_use.ToolID: got %q, want %q", tu.ToolID, "toolu_01abc123")
	}
	wantInput := `{"command":"ls -la"}`
	if tu.Input != wantInput {
		t.Errorf("tool_use.Input (assembled from deltas): got %q, want %q", tu.Input, wantInput)
	}

	// --- Assert tool_result ---
	if len(toolResultEvents) != 1 {
		t.Fatalf("expected 1 tool_result event, got %d: %v", len(toolResultEvents), toolResultEvents)
	}
	tr := toolResultEvents[0]
	if tr.ToolName != "Bash" {
		t.Errorf("tool_result.ToolName (correlated): got %q, want %q", tr.ToolName, "Bash")
	}
	if tr.ToolID != "toolu_01abc123" {
		t.Errorf("tool_result.ToolID: got %q, want %q", tr.ToolID, "toolu_01abc123")
	}
	if tr.IsError {
		t.Error("tool_result.IsError: got true, want false")
	}
	if !strings.Contains(tr.Output, "drwxr-xr-x") {
		t.Errorf("tool_result.Output: expected directory listing, got %q", tr.Output)
	}

	// --- Assert final usage (text + usage unchanged) ---
	if finalCostUSD != 0.00125 {
		t.Errorf("finalCostUSD: got %f, want 0.00125", finalCostUSD)
	}
	if finalInputTokens != 80 {
		t.Errorf("finalInputTokens: got %d, want 80", finalInputTokens)
	}
	if finalOutputTokens != 14 {
		t.Errorf("finalOutputTokens: got %d, want 14", finalOutputTokens)
	}
}

// ---- 2. Delta accumulation: multiple input_json_delta fragments ---------------

func TestParseStreamLineWithTools_DeltaAccumulation(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	lines := [][]byte{
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_frag","name":"Read"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"_path"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\":\"/etc/hosts\"}"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`),
	}

	var toolUseEvents []*ToolEvent
	for _, line := range lines {
		_, _, _, _, toolEv := ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
		if toolEv != nil && toolEv.Kind == "tool_use" {
			cp := *toolEv
			toolUseEvents = append(toolUseEvents, &cp)
		}
	}

	if len(toolUseEvents) != 1 {
		t.Fatalf("expected 1 tool_use event, got %d", len(toolUseEvents))
	}
	got := toolUseEvents[0]
	if got.ToolName != "Read" {
		t.Errorf("ToolName: got %q, want %q", got.ToolName, "Read")
	}
	wantInput := `{"file_path":"/etc/hosts"}`
	if got.Input != wantInput {
		t.Errorf("Input (assembled): got %q, want %q", got.Input, wantInput)
	}
}

// ---- 3. ToolResult is_error flag propagated ----------------------------------

func TestParseStreamLineWithTools_ToolResultIsError(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := map[string]string{"toolu_err": "Bash"}

	errorLine := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_err","content":"bash: command not found","is_error":true}]}}`)

	_, _, _, _, toolEv := ParseStreamLineWithTools(errorLine, textBlocks, toolUseBlocks, toolIDToName)
	if toolEv == nil {
		t.Fatal("expected a tool_result ToolEvent, got nil")
	}
	if toolEv.Kind != "tool_result" {
		t.Errorf("Kind: got %q, want %q", toolEv.Kind, "tool_result")
	}
	if !toolEv.IsError {
		t.Error("IsError: got false, want true")
	}
	if toolEv.ToolName != "Bash" {
		t.Errorf("ToolName (correlated): got %q, want %q", toolEv.ToolName, "Bash")
	}
	if toolEv.Output != "bash: command not found" {
		t.Errorf("Output: got %q, want %q", toolEv.Output, "bash: command not found")
	}
}

// ---- 4. Text tokens interleaved with tool blocks unchanged -------------------

func TestParseStreamLineWithTools_TextAndToolInterleaved(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	lines := [][]byte{
		// text block at index 0
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Before tool."}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`),
		// tool_use block at index 1
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_mix","name":"Write"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`),
		// another text block at index 2
		[]byte(`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"After tool."}}}`),
		[]byte(`{"type":"stream_event","event":{"type":"content_block_stop","index":2}}`),
	}

	var textTokens []string
	var toolUseEvents []*ToolEvent
	for _, line := range lines {
		tok, _, _, _, toolEv := ParseStreamLineWithTools(line, textBlocks, toolUseBlocks, toolIDToName)
		if tok != "" {
			textTokens = append(textTokens, tok)
		}
		if toolEv != nil && toolEv.Kind == "tool_use" {
			cp := *toolEv
			toolUseEvents = append(toolUseEvents, &cp)
		}
	}

	if len(textTokens) != 2 {
		t.Errorf("expected 2 text tokens, got %d: %v", len(textTokens), textTokens)
	} else {
		if textTokens[0] != "Before tool." {
			t.Errorf("text[0]: got %q, want %q", textTokens[0], "Before tool.")
		}
		if textTokens[1] != "After tool." {
			t.Errorf("text[1]: got %q, want %q", textTokens[1], "After tool.")
		}
	}
	if len(toolUseEvents) != 1 {
		t.Errorf("expected 1 tool_use event, got %d", len(toolUseEvents))
	} else if toolUseEvents[0].ToolName != "Write" {
		t.Errorf("tool_use.ToolName: got %q, want %q", toolUseEvents[0].ToolName, "Write")
	}
}

// ---- 5. ParseStreamLineWithUsage shim unchanged (no tool maps passed) --------
//
// This test is the "confirmation text/usage parsing is unchanged" assertion.
// It feeds the same fixture lines used for text tokens and the result event
// through ParseStreamLineWithUsage (the legacy shim that passes nil tool maps)
// and verifies identical output to the pre-Phase-4 behaviour.

func TestParseStreamLineWithUsage_ShimUnchanged(t *testing.T) {
	// Feed the content_block_start + text_delta + result lines from the fixture.
	textBlocks := make(map[int]struct{})

	startLine := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`)
	tok, isResult, costUSD, usage := ParseStreamLineWithUsage(startLine, textBlocks)
	if tok != "" || isResult || costUSD != 0 || usage != nil {
		t.Errorf("start line via shim: got (%q,%v,%f,%v); want (\"\",false,0,nil)", tok, isResult, costUSD, usage)
	}
	if _, ok := textBlocks[0]; !ok {
		t.Error("shim: block 0 not registered as text block")
	}

	deltaLine := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}}`)
	tok, isResult, costUSD, usage = ParseStreamLineWithUsage(deltaLine, textBlocks)
	if tok != "hello" || isResult || costUSD != 0 || usage != nil {
		t.Errorf("delta line via shim: got (%q,%v,%f,%v); want (\"hello\",false,0,nil)", tok, isResult, costUSD, usage)
	}

	resultLine := []byte(`{"type":"result","subtype":"success","result":"hello","is_error":false,"duration_ms":100,"total_cost_usd":0.001,"usage":{"input_tokens":5,"output_tokens":2}}`)
	tok, isResult, costUSD, usage = ParseStreamLineWithUsage(resultLine, textBlocks)
	if tok != "" || !isResult || costUSD != 0.001 {
		t.Errorf("result line via shim: got (%q,%v,%f); want (\"\",true,0.001)", tok, isResult, costUSD)
	}
	if usage == nil {
		t.Fatal("result line via shim: usage is nil; want non-nil")
	}
	if usage.InputTokens != 5 || usage.OutputTokens != 2 {
		t.Errorf("usage via shim: got {%d,%d}; want {5,2}", usage.InputTokens, usage.OutputTokens)
	}
}

// ---- 6. Empty line → nil tool event, zeroed returns -------------------------

func TestParseStreamLineWithTools_EmptyLine(t *testing.T) {
	textBlocks := make(map[int]struct{})
	toolUseBlocks := make(map[int]*ToolEvent)
	toolIDToName := make(map[string]string)

	tok, isResult, costUSD, usage, toolEv := ParseStreamLineWithTools(nil, textBlocks, toolUseBlocks, toolIDToName)
	if tok != "" || isResult || costUSD != 0 || usage != nil || toolEv != nil {
		t.Errorf("empty line: got (%q,%v,%f,%v,%v); want zeroed", tok, isResult, costUSD, usage, toolEv)
	}
}

// ---- 7. Nil toolUseBlocks/toolIDToName → no panic (legacy compat) -----------

func TestParseStreamLineWithTools_NilMaps_NoPanic(t *testing.T) {
	textBlocks := make(map[int]struct{})

	// tool_use start with nil maps must not panic; must be silently ignored.
	toolStartLine := []byte(`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"x","name":"Bash"}}}`)
	tok, isResult, costUSD, usage, toolEv := ParseStreamLineWithTools(toolStartLine, textBlocks, nil, nil)
	if tok != "" || isResult || costUSD != 0 || usage != nil || toolEv != nil {
		t.Errorf("tool_use with nil maps: unexpected non-zero return: (%q,%v,%f,%v,%v)", tok, isResult, costUSD, usage, toolEv)
	}
}
