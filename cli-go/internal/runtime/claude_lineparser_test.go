package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	claudeOneShot  = "claude-stream-json-oneshot-SYNTHETIC.ndjson"
	claudeErrFix   = "claude-stream-json-error-SYNTHETIC.ndjson"
	claudeSubagent = "claude-stream-json-subagent-SYNTHETIC.ndjson"
)

// withoutResultFrame drops the terminal result line, as a killed run would.
func withoutResultFrame(t *testing.T, raw []byte) []byte {
	t.Helper()
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if !strings.Contains(l, `"type":"result"`) {
			kept = append(kept, l)
		}
	}
	return []byte(strings.Join(kept, "\n"))
}

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

	// Text is the result frame's final report; the relay's lead-in is not part of
	// it. TextAll keeps every assistant message, as the bash dispatcher printed.
	if want := "The backend agent reports: all handlers registered."; pr.Text != want {
		t.Errorf("Text = %q\nwant   %q", pr.Text, want)
	}
	if want := "Dispatching to the backend agent.\nThe backend agent reports: all handlers registered."; pr.TextAll != want {
		t.Errorf("TextAll = %q\nwant      %q", pr.TextAll, want)
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

// TextAll keeps extractClaudeText semantics: the text blocks of the assistant
// messages, in order (plus a newline between separate messages).
func TestClaudeLineParser_TextAllMatchesExtractClaudeTextForOneMessage(t *testing.T) {
	stream := `{"type":"system","subtype":"init","session_id":"s"}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"line one\nline two\n"}]}}` + "\n" +
		`{"type":"result","subtype":"success","result":"line one\nline two\n","usage":{"input_tokens":1,"output_tokens":2}}` + "\n"
	pr, _ := parse("claude", []byte(stream))
	want := strings.TrimRight(string(extractClaudeText([]byte(stream))), "\r\n")
	if pr.TextAll != want {
		t.Errorf("TextAll %q != extractClaudeText %q", pr.TextAll, want)
	}
	if pr.Text != want {
		t.Errorf("Text %q != %q (the result frame carries the same text here)", pr.Text, want)
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

// A partial-messages stream with no closing assistant messages: Text is the
// result frame's, TextAll is rebuilt from the deltas, tool events come from the
// content blocks.
func TestClaudeLineParser_PartialMessages(t *testing.T) {
	pr, evs := parse("claude", readTestdata(t, "claude_tool_stream.ndjson"))
	if pr.Text != "Done." {
		t.Errorf("Text = %q, want the result frame's %q", pr.Text, "Done.")
	}
	if want := "I'll run a command.\nDone."; pr.TextAll != want {
		t.Errorf("TextAll = %q, want %q", pr.TextAll, want)
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

// ---- the text contract (result frame first) -----------------------------------------

// Without a result frame (a killed run) the answer is the text blocks of the
// top-level assistant messages.
func TestClaudeLineParser_NoResultFrameFallsBackToTheTopLevelAssistantText(t *testing.T) {
	pr, _ := parse("claude", withoutResultFrame(t, readFixture(t, claudeOneShot)))
	const want = "Dispatching to the backend agent.\nThe backend agent reports: all handlers registered."
	if pr.Text != want || pr.TextAll != want {
		t.Errorf("Text = %q, TextAll = %q, want both %q", pr.Text, pr.TextAll, want)
	}
	if pr.Usage != (Usage{}) {
		t.Errorf("no result frame, no usage: %+v", pr.Usage)
	}
}

// An empty result string is not an answer: the assistant text stands in.
func TestClaudeLineParser_EmptyResultFrameFallsBack(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"the real answer"}]}}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"","usage":{"input_tokens":1,"output_tokens":1}}`
	pr, _ := parse("claude", []byte(stream))
	if pr.Text != "the real answer" {
		t.Errorf("Text = %q", pr.Text)
	}
}

// A whitespace-only result is not an answer either: kept, it would shadow the
// text the run did say. The assistant text stands in, and so do the deltas of a
// partial-messages stream.
func TestClaudeLineParser_BlankResultFrameFallsBack(t *testing.T) {
	for _, blank := range []string{`\n`, ` `, `  \t\n `, `\r\n`} {
		result := `{"type":"result","subtype":"success","is_error":false,"result":"` + blank + `","usage":{"input_tokens":1,"output_tokens":1}}`

		assistant := `{"type":"assistant","message":{"content":[{"type":"text","text":"real answer"}]}}` + "\n" + result
		if pr, _ := parse("claude", []byte(assistant)); pr.Text != "real answer" || pr.TextAll != "real answer" {
			t.Errorf("result %q: Text = %q, TextAll = %q, want the assistant text", blank, pr.Text, pr.TextAll)
		}

		partial := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}` + "\n" +
			`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streamed answer"}}}` + "\n" + result
		if pr, _ := parse("claude", []byte(partial)); pr.Text != "streamed answer" {
			t.Errorf("result %q: Text = %q, want the streamed text", blank, pr.Text)
		}
	}
}

// An error result's message is the failure, never the answer; whatever the run
// said before it failed is still its text.
func TestClaudeLineParser_ErrorResultMessageIsNeverTheAnswer(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"partial work before the failure"}]}}` + "\n" +
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Credit balance is too low","usage":{"input_tokens":1,"output_tokens":1}}`
	pr, _ := parse("claude", []byte(stream))
	if pr.Text != "partial work before the failure" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.Error != "Credit balance is too low" {
		t.Errorf("Error = %q", pr.Error)
	}
}

// Sub-agent narration (assistant lines carrying parent_tool_use_id, forwarded
// when CLAUDE_CODE_FORWARD_SUBAGENT_TEXT is set) is not the answer. With the
// result frame the answer is its string; without it, the top-level text only.
// TextAll keeps everything.
func TestClaudeLineParser_SubagentNarrationIsNotTheAnswer(t *testing.T) {
	raw := readFixture(t, claudeSubagent)

	withResult, evs := parse("claude", raw)
	if withResult.Text != "The backend agent reports: all handlers registered." {
		t.Errorf("Text = %q", withResult.Text)
	}
	const everything = "Dispatching to the backend agent.\nLet me look at the handlers.\nAll handlers are registered.\nThe backend agent reports: all handlers registered."
	if withResult.TextAll != everything {
		t.Errorf("TextAll = %q\nwant      %q", withResult.TextAll, everything)
	}
	if withResult.SessionID != "5a1e0c3f-9d2b-4e7a-8c41-1b2d3e4f5a6b" {
		t.Errorf("SessionID = %q", withResult.SessionID)
	}
	// The live event feed still shows the narration and the sub-agent's tool calls.
	wantKinds(t, evs, EventSession, EventToken, EventToolUse, EventToken, EventToolUse, EventToolResult, EventToken, EventToolResult, EventToken, EventResult)
	if evs[5].ToolName != "Read" || evs[7].ToolName != "Task" {
		t.Errorf("tool results are correlated to their calls: %q, %q", evs[5].ToolName, evs[7].ToolName)
	}

	// No result frame: the top-level text, still without the sub-agent's.
	killed, _ := parse("claude", withoutResultFrame(t, raw))
	const topLevel = "Dispatching to the backend agent.\nThe backend agent reports: all handlers registered."
	if killed.Text != topLevel {
		t.Errorf("Text without a result frame = %q\nwant %q", killed.Text, topLevel)
	}
	if killed.TextAll != everything {
		t.Errorf("TextAll without a result frame = %q", killed.TextAll)
	}
}

// Sub-agent text deltas of a partial-messages stream are left out of the
// delta fallback too.
func TestClaudeLineParser_SubagentDeltasAreNotTheFallbackAnswer(t *testing.T) {
	delta := func(parent, text string) string {
		return `{"type":"stream_event","parent_tool_use_id":` + parent + `,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}}`
	}
	start := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`
	stream := strings.Join([]string{start, delta(`"toolu_task1"`, "sub-agent narration. "), delta("null", "The answer.")}, "\n")
	pr, _ := parse("claude", []byte(stream))
	if pr.Text != "The answer." {
		t.Errorf("Text = %q", pr.Text)
	}
}

// Every other harness says one thing: TextAll is Text.
func TestParsers_TextAllEqualsTextOutsideClaude(t *testing.T) {
	for rt, name := range map[string]string{
		"codex": "codex-exec-json-0.154.0-command.ndjson",
		"agy":   "agy-stream-json-1.2.17-tool.ndjson",
	} {
		pr, _ := parse(rt, readFixture(t, name))
		if pr.TextAll != pr.Text || pr.TextAllCapped != pr.TextCapped {
			t.Errorf("%s: TextAll %q vs Text %q", rt, pr.TextAll, pr.Text)
		}
	}
	if pr, _ := parse("gemini", []byte("plain\nprose")); pr.TextAll != "plain\nprose" || pr.Text != pr.TextAll {
		t.Errorf("plain: %+v", pr)
	}
}

// Text and TextAll are capped separately: a long run of narration must not mark
// the (small) final report as incomplete.
func TestClaudeLineParser_TextAllCapIsReportedSeparately(t *testing.T) {
	chunk := strings.Repeat("n", 100_000)
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, `{"type":"assistant","parent_tool_use_id":"toolu_task1","message":{"content":[{"type":"text","text":"`+chunk+`"}]}}`)
	}
	lines = append(lines, `{"type":"result","subtype":"success","is_error":false,"result":"short final report","usage":{"input_tokens":1,"output_tokens":1}}`)
	pr, _ := parse("claude", []byte(strings.Join(lines, "\n")))
	if pr.Text != "short final report" || pr.TextCapped || pr.Truncated {
		t.Errorf("Text=%q TextCapped=%v Truncated=%v: the final report is complete", pr.Text, pr.TextCapped, pr.Truncated)
	}
	if !pr.TextAllCapped || len(pr.TextAll) > MaxParsedTextBytes {
		t.Errorf("TextAllCapped=%v len(TextAll)=%d", pr.TextAllCapped, len(pr.TextAll))
	}
}
