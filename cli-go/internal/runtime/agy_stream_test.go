package runtime

import (
	"strings"
	"testing"
)

// agyReal* replay REAL recordings from agy 1.2.17 (gemini-3.8-flash-low); the
// only edit is that init.cwd was rewritten to /work/project. The agySynthetic*
// fixtures are the vendor's published examples (see
// tests/fixtures/runtime-streams/README.md) for the cases not recorded:
// checkpoint steps, a two-turn stdin session, tool errors and the
// --output-format json envelope.
const (
	agyRealOK        = "agy-stream-json-1.2.17-ok.ndjson"
	agyRealTool      = "agy-stream-json-1.2.17-tool.ndjson"
	agySingle        = "agy-stream-json-1.2.17-SYNTHETIC-checkpoint.ndjson"
	agyMultiturn     = "agy-stream-json-1.2.17-SYNTHETIC-multiturn.ndjson"
	agyToolErrorFix  = "agy-stream-json-1.2.17-SYNTHETIC-tool-error.ndjson"
	agyEnvelopeFixed = "agy-json-1.2.17-SYNTHETIC-envelope.ndjson"
)

// ---- real recordings ----------------------------------------------------------

func TestAgyLineParser_RealOK(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyRealOK))
	if pr.Text != "ok" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "45b505d2-bbd2-46e2-ad18-6557161bf134" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	// The model id is whatever the init frame reports, verbatim, effort suffix included.
	if pr.ModelID != "gemini-3.8-flash-low" {
		t.Errorf("ModelID = %q", pr.ModelID)
	}
	want := Usage{InputTokens: 12863, OutputTokens: 1, DurationMs: 2055}
	if pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	if pr.Error != "" || pr.Truncated {
		t.Errorf("unexpected: %+v", pr)
	}
	// "ok" arrives as an ACTIVE fragment, then a DONE step carrying "\n".
	wantKinds(t, evs, EventSession, EventToken, EventToken, EventResult)
	if evs[0].Model != "gemini-3.8-flash-low" || evs[1].Text != "ok" || evs[2].Text != "\n" {
		t.Errorf("events = %+v", evs)
	}
}

// A run with a shell tool step: the first agent_response step carries usage but
// no text (the model's tool call), the tool step is seen ACTIVE then DONE, and
// the answer comes from the last agent_response step.
func TestAgyLineParser_RealToolRun(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyRealTool))
	if pr.Text != "done" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "1f18ba00-a3ce-4a9e-8200-fdf181ecaeb6" || pr.ModelID != "gemini-3.8-flash-low" {
		t.Errorf("SessionID/ModelID = %q/%q", pr.SessionID, pr.ModelID)
	}
	want := Usage{InputTokens: 25958, OutputTokens: 128, DurationMs: 8730}
	if pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	wantKinds(t, evs, EventSession, EventToolUse, EventToolResult, EventToken, EventToken, EventResult)
	if u := evs[1]; u.ToolName != "run_command" || u.ToolInput != `{"CommandLine":"echo hello_p0c"}` {
		t.Errorf("tool_use = %+v", u)
	}
	if r := evs[2]; r.ToolName != "run_command" || r.ToolOutput != "hello_p0c\r\n" || r.IsError {
		t.Errorf("tool_result = %+v", r)
	}
}

// Recorded evidence for the fallback: summing the DONE steps' usage reproduces
// the terminal result's totals exactly (12870 + 13088 input, 127 + 1 output).
func TestAgyLineParser_RealRunStepUsageSumMatchesResult(t *testing.T) {
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(readFixture(t, agyRealTool)), "\n"), "\n") {
		if !strings.Contains(l, `"event":"result"`) {
			kept = append(kept, l)
		}
	}
	pr, _ := parse("agy", []byte(strings.Join(kept, "\n")))
	if want := (Usage{InputTokens: 25958, OutputTokens: 128}); pr.Usage != want {
		t.Errorf("fallback Usage = %+v, want %+v", pr.Usage, want)
	}
}

// A model id with an effort suffix is carried verbatim; the result frame may
// report one too.
func TestAgyLineParser_ModelIDIsVerbatim(t *testing.T) {
	for _, id := range []string{"gemini-3.8-flash-high", "claude-opus-5-5-medium"} {
		init := `{"event":"init","conversation_id":"c","init":{"model":"` + id + `"}}`
		if pr, _ := parse("agy", []byte(init)); pr.ModelID != id {
			t.Errorf("init model %q -> ModelID %q", id, pr.ModelID)
		}
		res := `{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"x","model":"` + id + `"}}`
		if pr, _ := parse("agy", []byte(res)); pr.ModelID != id {
			t.Errorf("result model %q -> ModelID %q", id, pr.ModelID)
		}
	}
	// init wins when both report one.
	both := `{"event":"init","conversation_id":"c","init":{"model":"from-init"}}` + "\n" +
		`{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","model":"from-result"}}`
	if pr, _ := parse("agy", []byte(both)); pr.ModelID != "from-init" {
		t.Errorf("ModelID = %q, want from-init", pr.ModelID)
	}
}

// ---- vendor examples (synthetic) -----------------------------------------------

func TestAgyLineParser_VendorExampleWithCheckpoint(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agySingle))
	const text = "Git rebase destructively rewrites a branch's commit history by systematically detaching its unique commits and sequentially reapplying them onto a new base commit."
	if pr.Text != text {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "c3b66b04-872b-4fbe-a3a4-058a026ef20a" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	// agy's input_tokens already excludes cache reads; the result's totals win.
	want := Usage{InputTokens: 10418, OutputTokens: 589, CacheRead: 8113, DurationMs: 6880}
	if pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	if pr.ModelID != "" || pr.Error != "" || pr.Truncated {
		t.Errorf("unexpected: %+v", pr)
	}
	wantKinds(t, evs, EventSession, EventToken, EventResult)
}

// Without a terminal result (killed run) the per-step usage is summed. In the
// vendor's own example the sum equals the result's totals.
func TestAgyLineParser_StepUsageFallbackMatchesResult(t *testing.T) {
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(readFixture(t, agySingle)), "\n"), "\n") {
		if !strings.Contains(l, `"event":"result"`) {
			kept = append(kept, l)
		}
	}
	pr, _ := parse("agy", []byte(strings.Join(kept, "\n")))
	want := Usage{InputTokens: 10418, OutputTokens: 589, CacheRead: 8113}
	if pr.Usage != want {
		t.Errorf("fallback Usage = %+v, want %+v", pr.Usage, want)
	}
	if !strings.HasPrefix(pr.Text, "Git rebase destructively") {
		t.Errorf("Text = %q", pr.Text)
	}
}

// ACTIVE/DONE fragments of one message are joined without a separator; the
// second turn of the same process is a new message; the last result carries
// the cumulative usage.
func TestAgyLineParser_MultiTurnFragments(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyMultiturn))
	if pr.Text != "apple\napple" {
		t.Errorf("Text = %q", pr.Text)
	}
	want := Usage{InputTokens: 30662, OutputTokens: 8, CacheRead: 30214, DurationMs: 2548}
	if pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	if pr.ModelID != "gemini-3-pro" || pr.SessionID != "9ec58bfd-4d67-4f5e-83a5-9d907e9c6b1f" {
		t.Errorf("ids = %q / %q", pr.ModelID, pr.SessionID)
	}
	wantKinds(t, evs, EventSession, EventToken, EventToken, EventResult, EventToken, EventResult)
	if evs[1].Text != "apple" || evs[2].Text != "\n" {
		t.Errorf("fragments = %q, %q", evs[1].Text, evs[2].Text)
	}
}

func TestAgyLineParser_ToolFailureStep(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyToolErrorFix))
	if pr.Text != "Running the command.\nThe command printed hello_headless_demo." {
		t.Errorf("Text = %q", pr.Text)
	}
	wantKinds(t, evs, EventSession, EventToken,
		EventToolUse, EventToolResult, // run_command echo
		EventToolUse, EventToolResult, // run_command that failed
		EventToken, EventResult)
	if u, r := evs[2], evs[3]; u.ToolName != "run_command" || u.ToolInput != `{"CommandLine":"echo hello_headless_demo"}` ||
		r.ToolOutput != "hello_headless_demo\r\n" || r.IsError {
		t.Errorf("echo step = %+v / %+v", u, r)
	}
	// tool_info.error is a tool ERROR; its message is the output.
	if r := evs[5]; !r.IsError || r.ToolOutput != "exit status 1" {
		t.Errorf("failed step result = %+v", r)
	}
	if want := (Usage{InputTokens: 9400, OutputTokens: 52, CacheRead: 8800, DurationMs: 3100}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
}

// A tool step seen ACTIVE then DONE yields one tool_use and one tool_result.
func TestAgyLineParser_ToolStepActiveThenDone(t *testing.T) {
	stream := `{"event":"step_update","step_update":{"step_index":4,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"}}}}` + "\n" +
		`{"event":"step_update","step_update":{"step_index":4,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"ls"},"output":"a\nb"}}}`
	_, evs := parse("agy", []byte(stream))
	wantKinds(t, evs, EventToolUse, EventToolResult)
}

// --output-format json: one envelope, no event key.
func TestAgyLineParser_JSONEnvelope(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyEnvelopeFixed))
	if !strings.HasPrefix(pr.Text, "A git rebase rewrites the commit history") || strings.HasSuffix(pr.Text, "\n") {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "055a398f-db14-4c5f-abbb-1bf03f8120a7" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	if want := (Usage{InputTokens: 10415, OutputTokens: 657, CacheRead: 8113, DurationMs: 7160}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	wantKinds(t, evs, EventResult)
}

// With no text deltas the result's response is the text.
func TestAgyLineParser_ResponseFallback(t *testing.T) {
	stream := `{"event":"init","conversation_id":"c","init":{}}` + "\n" +
		`{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"from result\n","usage":{"input_tokens":1,"output_tokens":1}}}`
	pr, _ := parse("agy", []byte(stream))
	if pr.Text != "from result" {
		t.Errorf("Text = %q", pr.Text)
	}
}

func TestAgyLineParser_ErrorStatuses(t *testing.T) {
	partial := `{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"partial answer"}}` + "\n" +
		`{"event":"result","result":{"conversation_id":"c","status":"ERROR","error":"model unavailable","usage":{"input_tokens":5,"output_tokens":1}}}`
	pr, evs := parse("agy", []byte(partial))
	if pr.Error != "model unavailable" {
		t.Errorf("Error = %q", pr.Error)
	}
	if pr.Text != "partial answer" {
		t.Errorf("a failed run keeps the text it streamed: %q", pr.Text)
	}
	wantKinds(t, evs, EventToken, EventResult, EventError)
	if !evs[2].Fatal {
		t.Error("a failed result's error event is fatal")
	}

	// A non-SUCCESS status without a message still names itself.
	waiting := `{"event":"result","result":{"conversation_id":"c","status":"WAITING"}}`
	if pr, _ := parse("agy", []byte(waiting)); pr.Error != "agy: status WAITING" {
		t.Errorf("Error = %q", pr.Error)
	}

	// An error object ({type,message}) is read like a string.
	obj := `{"event":"result","result":{"conversation_id":"c","status":"ERROR","error":{"type":"QUOTA","message":"quota exhausted"}}}`
	if pr, _ := parse("agy", []byte(obj)); pr.Error != "quota exhausted" {
		t.Errorf("Error = %q", pr.Error)
	}

	// SUCCESS, in any case, is not an error.
	ok := `{"event":"result","result":{"conversation_id":"c","status":"success","response":"x"}}`
	if pr, _ := parse("agy", []byte(ok)); pr.Error != "" {
		t.Errorf("Error = %q", pr.Error)
	}
}

func TestAgyLineParser_UsageAbsent(t *testing.T) {
	stream := `{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"no usage reported"}}`
	pr, _ := parse("agy", []byte(stream))
	if pr.Usage != (Usage{}) {
		t.Errorf("Usage = %+v, want the zero value", pr.Usage)
	}
}

func TestAgyLineParser_TruncatedLineInStructuredStream(t *testing.T) {
	lines := strings.Split(strings.TrimRight(string(readFixture(t, agySingle)), "\n"), "\n")
	lines[2] = lines[2][:len(lines[2])/2] // the agent_response step, cut in half
	pr, _ := parse("agy", []byte(strings.Join(lines, "\n")))
	// The cut step is lost; the result's response still supplies the text.
	if !strings.HasPrefix(pr.Text, "Git rebase destructively") {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.Usage.InputTokens != 10418 {
		t.Errorf("Usage = %+v", pr.Usage)
	}
}

// Today's adapter prints plain text; that must keep working unchanged.
func TestAgyLineParser_PlainTextFallback(t *testing.T) {
	pr, evs := parse("agy", []byte("Plain answer.\n\nWith a second paragraph.\n"))
	if pr.Text != "Plain answer.\n\nWith a second paragraph." {
		t.Errorf("Text = %q", pr.Text)
	}
	wantKinds(t, evs, EventToken, EventToken, EventToken)
}

// JSON that merely resembles agy output, inside prose, stays prose.
func TestAgyLineParser_JSONInProseStaysPlain(t *testing.T) {
	prose := "Webhook payload:\n" + `{"event":"push","ref":"main"}` + "\nAPI reply:\n" + `{"status":"ok","response":"fine"}` + "\nend"
	pr, _ := parse("agy", []byte(prose))
	if pr.Text != prose {
		t.Errorf("Text = %q, want the prose unchanged", pr.Text)
	}
}

func TestAgyLineParser_NoiseInStructuredStreamIgnored(t *testing.T) {
	stream := "starting\n" + string(readFixture(t, agySingle)) + "bye\n"
	pr, _ := parse("agy", []byte(stream))
	if strings.Contains(pr.Text, "starting") || strings.Contains(pr.Text, "bye") {
		t.Errorf("noise leaked into the text: %q", pr.Text)
	}
}
