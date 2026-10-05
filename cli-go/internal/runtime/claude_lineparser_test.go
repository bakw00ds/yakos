package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	claudeOneShot = "claude-stream-json-oneshot-SYNTHETIC.ndjson"
	claudeErrFix  = "claude-stream-json-error-SYNTHETIC.ndjson"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("testdata %s: %v", name, err)
	}
	return b
}

// A framed one-shot run: complete assistant messages, no partial messages.
func TestClaudeLineParser_OneShotGolden(t *testing.T) {
	raw := readFixture(t, claudeOneShot)
	pr, evs := parse("claude", raw)

	if want := "Dispatching to the backend agent.\nThe backend agent reports: all handlers registered."; pr.Text != want {
		t.Errorf("Text = %q\nwant   %q", pr.Text, want)
	}
	if pr.SessionID != "7f3c2a9e-1b4d-4c8e-9a10-0d5e6f7a8b9c" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	if pr.ModelID != "claude-sonnet-4-5-20250929" {
		t.Errorf("ModelID = %q", pr.ModelID)
	}
	wantUsage := Usage{InputTokens: 120, OutputTokens: 45, CacheRead: 9000, CacheCreation: 3000, DurationMs: 4321, TotalCostUSD: 0.0123}
	if pr.Usage != wantUsage {
		t.Errorf("Usage = %+v\nwant    %+v", pr.Usage, wantUsage)
	}
	if pr.Error != "" || pr.Truncated {
		t.Errorf("Error=%q Truncated=%v", pr.Error, pr.Truncated)
	}

	wantKinds(t, evs, EventSession, EventThinking, EventToken, EventToolUse, EventToolResult, EventToken, EventResult)
	if evs[1].Text != "The relay should hand this to the subagent." {
		t.Errorf("thinking text = %q", evs[1].Text)
	}
	if use := evs[3]; use.ToolName != "Bash" || use.ToolInput != `{"command":"ls -la"}` {
		t.Errorf("tool_use = %+v", use)
	}
	// The tool_result is a tool ERROR and is correlated back to its tool name.
	if res := evs[4]; res.ToolName != "Bash" || res.ToolOutput != "permission denied" || !res.IsError {
		t.Errorf("tool_result = %+v", res)
	}
	if done := evs[6]; done.Usage != wantUsage || done.SessionID != pr.SessionID {
		t.Errorf("result event = %+v", done)
	}
}

// Final text keeps extractClaudeText semantics: the text blocks of the
// assistant messages, in order (plus a newline between separate messages).
func TestClaudeLineParser_MatchesExtractClaudeTextForOneMessage(t *testing.T) {
	stream := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"line one\nline two\n"}]}}` + "\n" +
		`{"type":"result","subtype":"success","result":"line one\nline two\n","usage":{"input_tokens":1,"output_tokens":2}}` + "\n"
	pr, _ := parse("claude", []byte(stream))
	want := strings.TrimRight(string(extractClaudeText([]byte(stream))), "\r\n")
	if pr.Text != want {
		t.Errorf("parser text %q != extractClaudeText %q", pr.Text, want)
	}
}

func TestClaudeLineParser_ErrorResult(t *testing.T) {
	pr, evs := parse("claude", readFixture(t, claudeErrFix))
	if pr.Text != "" {
		t.Errorf("a failed run's error message is not answer text: Text = %q", pr.Text)
	}
	if pr.Error != "Credit balance is too low" {
		t.Errorf("Error = %q", pr.Error)
	}
	if pr.SessionID != "11111111-2222-4333-8444-555555555555" || pr.ModelID != "claude-sonnet-4-5-20250929" {
		t.Errorf("ids = %q / %q", pr.SessionID, pr.ModelID)
	}
	wantKinds(t, evs, EventSession, EventResult, EventError)
	if last := evs[2]; !last.Fatal || last.Text != "Credit balance is too low" {
		t.Errorf("error event = %+v", last)
	}
}

// A partial-messages stream killed before its closing assistant message: the
// text is rebuilt from the deltas, tool events come from the content blocks.
func TestClaudeLineParser_PartialMessagesDeltaFallback(t *testing.T) {
	pr, evs := parse("claude", readTestdata(t, "claude_tool_stream.ndjson"))
	if want := "I'll run a command.\nDone."; pr.Text != want {
		t.Errorf("Text = %q, want %q", pr.Text, want)
	}
	wantUsage := Usage{InputTokens: 80, OutputTokens: 14, DurationMs: 2500, TotalCostUSD: 0.00125}
	if pr.Usage != wantUsage {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, wantUsage)
	}
	wantKinds(t, evs, EventToken, EventToolUse, EventToolResult, EventToken, EventResult)
	if use := evs[1]; use.ToolName != "Bash" || use.ToolInput != `{"command":"ls -la"}` {
		t.Errorf("tool_use = %+v", use)
	}
	if res := evs[2]; res.ToolName != "Bash" || res.IsError || !strings.HasPrefix(res.ToolOutput, "total 8\n") {
		t.Errorf("tool_result = %+v", res)
	}
}

func TestClaudeLineParser_ThinkingStream(t *testing.T) {
	pr, evs := parse("claude", readTestdata(t, "claude_thinking_stream.ndjson"))
	if pr.Text != "The answer is 42." {
		t.Errorf("Text = %q", pr.Text)
	}
	wantKinds(t, evs, EventThinking, EventThinking, EventToken, EventResult)
	if evs[0].Text != "Let me think about this." || evs[1].Text != " The answer is 42." {
		t.Errorf("thinking deltas = %q, %q", evs[0].Text, evs[1].Text)
	}
}

func TestClaudeLineParser_RedactedThinkingStream(t *testing.T) {
	_, evs := parse("claude", readTestdata(t, "claude_redacted_thinking_stream.ndjson"))
	var saw bool
	for _, e := range evs {
		if e.Kind == EventThinking && e.Text == redactedThinkingPlaceholder {
			saw = true
		}
	}
	if !saw {
		t.Errorf("no redacted-thinking placeholder event in %v", kindsOf(evs))
	}
}

// With partial messages the complete assistant message repeats text the
// deltas already delivered: it feeds the final text but must not become a
// second token event.
func TestClaudeLineParser_NoDuplicateTokensWithPartialMessages(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s","model":"m"}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"result","subtype":"success","result":"Hello","usage":{"input_tokens":1,"output_tokens":1}}`,
	}, "\n")
	pr, evs := parse("claude", []byte(stream))
	if pr.Text != "Hello" {
		t.Errorf("Text = %q", pr.Text)
	}
	tokens := 0
	for _, e := range evs {
		if e.Kind == EventToken {
			tokens++
		}
	}
	if tokens != 2 {
		t.Errorf("token events = %d, want 2 (the deltas only)", tokens)
	}
}

func TestClaudeLineParser_NoResultMeansNoUsage(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"killed before the result"}]}}`
	pr, _ := parse("claude", []byte(stream))
	if pr.Usage != (Usage{}) {
		t.Errorf("Usage = %+v, want the zero value", pr.Usage)
	}
	if pr.Text != "killed before the result" {
		t.Errorf("Text = %q", pr.Text)
	}
}

// A line cut mid-JSON (killed process, truncated read) is ignored once the
// stream is structured; the rest still parses.
func TestClaudeLineParser_TruncatedLineInStructuredStream(t *testing.T) {
	raw := string(readFixture(t, claudeOneShot))
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	lines[2] = lines[2][:len(lines[2])/2] // cut the first text message in half
	pr, _ := parse("claude", []byte(strings.Join(lines, "\n")))
	if pr.Text != "The backend agent reports: all handlers registered." {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.Usage.InputTokens != 120 {
		t.Errorf("the result after the cut line was lost: %+v", pr.Usage)
	}
}

func TestClaudeLineParser_PlainTextFallback(t *testing.T) {
	pr, evs := parse("claude", []byte("hello\n\nworld\n"))
	if pr.Text != "hello\n\nworld" {
		t.Errorf("Text = %q", pr.Text)
	}
	wantKinds(t, evs, EventToken, EventToken, EventToken)
}

// JSON inside prose, or an object that is not a claude event, never flips the
// parser out of its plain-text fallback.
func TestClaudeLineParser_JSONInProseStaysPlain(t *testing.T) {
	prose := "Here is the schema:\n" + `{"type":"object","properties":{}}` + "\nand that is all."
	pr, _ := parse("claude", []byte(prose))
	if pr.Text != prose {
		t.Errorf("Text = %q, want the prose unchanged", pr.Text)
	}
}

// A noise line inside a structured stream is dropped, not mixed into the text.
func TestClaudeLineParser_NoiseInStructuredStreamIgnored(t *testing.T) {
	stream := "starting up...\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"answer"}]}}` + "\n" +
		"warning: something\n"
	pr, _ := parse("claude", []byte(stream))
	if pr.Text != "answer" {
		t.Errorf("Text = %q", pr.Text)
	}
}

// When a session is resumed the id on the terminal result is the one that
// holds the whole conversation, so it wins over the id on init.
func TestClaudeLineParser_ResultSessionIDWins(t *testing.T) {
	stream := `{"type":"system","subtype":"init","session_id":"old"}` + "\n" +
		`{"type":"result","subtype":"success","result":"x","session_id":"new","usage":{"input_tokens":1,"output_tokens":1}}`
	pr, _ := parse("claude", []byte(stream))
	if pr.SessionID != "new" {
		t.Errorf("SessionID = %q, want new", pr.SessionID)
	}
}

func TestClaudeLineParser_ModelFromAssistantWhenInitHasNone(t *testing.T) {
	stream := `{"type":"assistant","message":{"model":"claude-opus-x","content":[{"type":"text","text":"hi"}]},"session_id":"s1"}`
	pr, _ := parse("claude", []byte(stream))
	if pr.ModelID != "claude-opus-x" || pr.SessionID != "s1" {
		t.Errorf("ids = %q / %q", pr.ModelID, pr.SessionID)
	}
}

// The text of a successful run falls back to the result string when no
// assistant message arrived (the stub binaries older tests use print only a
// result line).
func TestClaudeLineParser_ResultStringFallback(t *testing.T) {
	pr, _ := parse("claude", []byte(`{"type":"result","result":"ok"}`))
	if pr.Text != "ok" {
		t.Errorf("Text = %q", pr.Text)
	}
}
