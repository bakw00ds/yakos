package dispatch

// streamhelper_turn_test.go covers the end-of-turn summary of a multi-turn
// (interactive) session (K-136). The persistent CLI engine reads claude's stdout
// through ParseAndDispatch and never saw a turn end: the result line was parsed and
// dropped, so a chat pane stayed "streaming" and no turn could be accounted. A
// parser state built by NewStreamParserState now emits one summary chunk per result
// line, carrying the turn's usage, cost, native session id and model id.

import (
	"testing"
)

func feed(t *testing.T, ps *StreamParserState, lines ...string) []StreamChunk {
	t.Helper()
	var chunks []StreamChunk
	for _, l := range lines {
		ParseAndDispatch([]byte(l), ps, func(c StreamChunk) { chunks = append(chunks, c) })
	}
	return chunks
}

func summaries(chunks []StreamChunk) []StreamChunk {
	var out []StreamChunk
	for _, c := range chunks {
		if c.Type == "summary" {
			out = append(out, c)
		}
	}
	return out
}

const (
	turnInit    = `{"type":"system","subtype":"init","session_id":"ses_FIXTURE_0001","model":"claude-sonnet-4-5-20250929"}`
	turnDelta   = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}}`
	turnResult1 = `{"type":"result","subtype":"success","is_error":false,"duration_ms":1500,"session_id":"ses_FIXTURE_0001","total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}`
	turnResult2 = `{"type":"result","subtype":"success","is_error":false,"duration_ms":900,"session_id":"ses_FIXTURE_0001","total_cost_usd":0.5,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}`
	turnFailed  = `{"type":"result","subtype":"error_during_execution","is_error":true,"duration_ms":50,"session_id":"ses_FIXTURE_0001","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0}}`
)

func TestParseAndDispatch_MultiTurnStateSummarisesEachTurn(t *testing.T) {
	ps := NewStreamParserState()
	got := summaries(feed(t, ps, turnInit, turnDelta, turnResult1, turnDelta, turnResult2))
	if len(got) != 2 {
		t.Fatalf("one summary per turn, got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.ExitCode != 0 || s.TotalCostUSD != 0.25 || s.DurationS != 1.5 || s.RuntimeResolved != "claude" ||
		s.NativeSessionID != "ses_FIXTURE_0001" || s.ModelResolved != "claude-sonnet-4-5-20250929" {
		t.Errorf("first summary = %+v", s)
	}
	if s.Usage == nil || s.Usage.InputTokens != 10 || s.Usage.OutputTokens != 5 || s.Usage.CacheRead != 100 || s.Usage.CacheCreation != 20 {
		t.Errorf("first summary usage = %+v", s.Usage)
	}
	if got[1].Usage == nil || got[1].Usage.CacheCreation != 4 || got[1].TotalCostUSD != 0.5 {
		t.Errorf("second turn reports its own usage, not a running total: %+v", got[1])
	}
}

func TestParseAndDispatch_MultiTurnSummaryReportsAFailedTurn(t *testing.T) {
	ps := NewStreamParserState()
	got := summaries(feed(t, ps, turnInit, turnFailed))
	if len(got) != 1 || got[0].ExitCode != 1 {
		t.Fatalf("a failed turn summarises with a non-zero exit code: %+v", got)
	}
	// The failed result's session id is not stored for resuming (ResultSessionID's rule).
	if got[0].NativeSessionID != "" {
		t.Errorf("an error result must not offer its session id: %q", got[0].NativeSessionID)
	}
}

// Non-result lines never produce a summary, and the one-shot path's state (built
// without EmitTurnSummary) never does either: it ends the process and writes its own.
func TestParseAndDispatch_NoSummaryUnlessTheStateAsksForOne(t *testing.T) {
	multi := NewStreamParserState()
	if n := len(summaries(feed(t, multi, turnInit, turnDelta))); n != 0 {
		t.Errorf("no result line, no summary: %d", n)
	}
	oneShot := &StreamParserState{
		TextBlocks:     NewStreamParserState().TextBlocks,
		ToolUseBlocks:  NewStreamParserState().ToolUseBlocks,
		ToolIDToName:   NewStreamParserState().ToolIDToName,
		ThinkingBlocks: NewStreamParserState().ThinkingBlocks,
	}
	if n := len(summaries(feed(t, oneShot, turnInit, turnDelta, turnResult1))); n != 0 {
		t.Errorf("the one-shot path's state must not emit a summary (it writes its own): %d", n)
	}
	// ...but it still reports the result to its caller, as before.
	res := ParseAndDispatch([]byte(turnResult1), oneShot, func(StreamChunk) {})
	if !res.IsResult || res.CostUSD != 0.25 || res.Usage == nil || res.Usage.CacheRead != 100 || res.SessionID != "ses_FIXTURE_0001" || res.Failed {
		t.Errorf("StreamLineResult = %+v", res)
	}
}
