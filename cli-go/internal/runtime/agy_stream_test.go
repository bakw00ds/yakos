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
	agyConvTurn1     = "agy-stream-json-1.2.17-conversation-turn1.ndjson" // recorded by wp-p0b (K-133)
	agyConvTurn2     = "agy-stream-json-1.2.17-conversation-turn2.ndjson" // recorded by wp-p0b (K-133)
	agyEffortFail    = "agy-stream-json-1.2.17-effort-conflict.ndjson"    // recorded by wp-p0b (K-133)
	agySandboxDenied = "agy-stream-json-1.2.17-sandbox-denied.ndjson"     // recorded by wp-p0b (K-133)
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

// ---- real recordings by wp-p0b (K-133) -----------------------------------------

// With --conversation, the result frame's usage is CUMULATIVE over the
// conversation: turn 2 reports turn 1's tokens plus its own. The parser reports
// the counts verbatim and marks them, so an accounting layer can subtract.
func TestAgyLineParser_RealConversationUsageIsCumulative(t *testing.T) {
	r1, ev1 := parse("agy", readFixture(t, agyConvTurn1))
	r2, ev2 := parse("agy", readFixture(t, agyConvTurn2))

	if r1.Text != "ok" || r2.Text != "again" {
		t.Errorf("Text = %q / %q", r1.Text, r2.Text)
	}
	const conv = "390dbd9d-ac3e-4fc9-9383-8f11318029e0"
	if r1.SessionID != conv || r2.SessionID != conv {
		t.Errorf("both turns share one conversation id: %q / %q", r1.SessionID, r2.SessionID)
	}
	// Verbatim from the result frames.
	if want := (Usage{InputTokens: 12859, OutputTokens: 26, DurationMs: 1994}); r1.Usage != want {
		t.Errorf("turn 1 Usage = %+v, want %+v", r1.Usage, want)
	}
	if want := (Usage{InputTokens: 25950, OutputTokens: 719, DurationMs: 35865}); r2.Usage != want {
		t.Errorf("turn 2 Usage = %+v, want %+v", r2.Usage, want)
	}
	if !r1.UsageCumulative || !r2.UsageCumulative {
		t.Errorf("UsageCumulative = %v / %v, want true for a result-frame total", r1.UsageCumulative, r2.UsageCumulative)
	}
	wantKinds(t, ev1, EventSession, EventToken, EventToken, EventResult)
	wantKinds(t, ev2, EventSession, EventToken, EventToken, EventResult)
	if !ev1[3].UsageCumulative || !ev2[3].UsageCumulative {
		t.Error("the result event carries the marker too")
	}

	// The accounting rule the marker exists for: subtracting the previous total
	// gives this turn's own tokens, and that equals what turn 2's own DONE steps
	// report (13091 input, 693 output).
	own := Usage{InputTokens: r2.Usage.InputTokens - r1.Usage.InputTokens, OutputTokens: r2.Usage.OutputTokens - r1.Usage.OutputTokens}
	if want := (Usage{InputTokens: 13091, OutputTokens: 693}); own != want {
		t.Errorf("turn 2 own tokens = %+v, want %+v", own, want)
	}
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(string(readFixture(t, agyConvTurn2)), "\n"), "\n") {
		if !strings.Contains(l, `"event":"result"`) {
			kept = append(kept, l)
		}
	}
	steps, _ := parse("agy", []byte(strings.Join(kept, "\n")))
	if steps.Usage != own {
		t.Errorf("DONE-step sum = %+v, want the per-turn figure %+v", steps.Usage, own)
	}
	// Without a result frame the usage is this run's own and is not cumulative.
	if steps.UsageCumulative {
		t.Error("a step-sum fallback is this run only and must not be marked cumulative")
	}
}

// A failed run on a real recording: a lone result frame with status ERROR, an
// empty conversation id and all-zero usage, no init and no steps.
func TestAgyLineParser_RealRunThatFailedBeforeStarting(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agyEffortFail))
	const want = `invalid model selection (--model "gemini-3.8-flash-low" --effort "high"): --model gemini-3.8-flash-low conflicts with --effort=high`
	if pr.Error != want {
		t.Errorf("Error = %q, want %q", pr.Error, want)
	}
	if pr.Text != "" || pr.SessionID != "" || pr.Usage != (Usage{}) || pr.UsageCumulative {
		t.Errorf("a run that never started has no text, session or usage: %+v", pr)
	}
	wantKinds(t, evs, EventResult, EventError)
	if !evs[1].Fatal {
		t.Error("the error event is fatal")
	}
}

// A tool step whose command was refused by the sandbox is a normal tool step to
// agy: no error object, the refusal is in the output.
func TestAgyLineParser_RealSandboxDeniedRun(t *testing.T) {
	pr, evs := parse("agy", readFixture(t, agySandboxDenied))
	if pr.Text != "1" || pr.ModelID != "gemini-3.8-flash-low" || pr.Error != "" {
		t.Errorf("Text/ModelID/Error = %q/%q/%q", pr.Text, pr.ModelID, pr.Error)
	}
	if want := (Usage{InputTokens: 26152, OutputTokens: 236, DurationMs: 14062}); pr.Usage != want || !pr.UsageCumulative {
		t.Errorf("Usage = %+v cumulative=%v, want %+v true", pr.Usage, pr.UsageCumulative, want)
	}
	wantKinds(t, evs, EventSession, EventToolUse, EventToolResult, EventToken, EventToken, EventResult)
	use, res := evs[1], evs[2]
	if use.ToolName != "run_command" || !strings.HasPrefix(use.ToolInput, `{"CommandLine":"sh -c`) || !strings.Contains(use.ToolInput, "p0b-agy-probe.txt") {
		t.Errorf("tool_use = %+v", use)
	}
	if !strings.Contains(res.ToolOutput, "Operation not permitted") || res.IsError {
		t.Errorf("tool_result = %+v", res)
	}
}

// ---- other shapes ------------------------------------------------------------------

// A stream in a different schema (a `type` key instead of `event`, a result line
// with a status and a conversation id but no "response") is not agy's. Its lines
// come back as the text instead of being swallowed by the json-envelope check.
func TestAgyLineParser_ADifferentSchemaFallsBackToTheRawLines(t *testing.T) {
	lines := []string{
		`{"type":"init","conversation_id":"3f2a9c1e-7b4d-4e0a-9a51-2c6d8e1f0b73","model":"gemini-3.1-pro","cwd":"/work/project"}`,
		`{"type":"step_update","conversation_id":"3f2a9c1e-7b4d-4e0a-9a51-2c6d8e1f0b73","step":{"index":1,"type":"planner_response","status":"done","text":"ok"}}`,
		`{"type":"result","status":"success","conversation_id":"3f2a9c1e-7b4d-4e0a-9a51-2c6d8e1f0b73","result":"ok","usage":{"input_tokens":1234,"output_tokens":5,"total_tokens":1239},"duration_ms":2310}`,
	}
	pr, _ := parse("agy", []byte(strings.Join(lines, "\n")))
	if pr.Text != strings.Join(lines, "\n") {
		t.Errorf("Text = %q, want the raw lines", pr.Text)
	}
	if pr.SessionID != "" || pr.Usage != (Usage{}) || pr.Error != "" || pr.UsageCumulative {
		t.Errorf("nothing in a foreign schema is interpreted: %+v", pr)
	}
}

// The json envelope needs all three of its keys, but the values may be empty
// (a failed run reports conversation_id "").
func TestAgyLineParser_JSONEnvelopeNeedsItsKeysNotTheirValues(t *testing.T) {
	failed := `{"conversation_id":"","status":"ERROR","response":"","error":"boom","duration_seconds":0,"num_turns":0}`
	if pr, _ := parse("agy", []byte(failed)); pr.Error != "boom" || pr.Text != "" {
		t.Errorf("failed envelope: Error=%q Text=%q", pr.Error, pr.Text)
	}
	noResponse := `{"conversation_id":"c","status":"SUCCESS","result":"x"}`
	if pr, _ := parse("agy", []byte(noResponse)); pr.Text != noResponse {
		t.Errorf("an object without a response key is not the envelope; Text = %q", pr.Text)
	}
}

// A prompt that starts with a slash command is answered by agy itself: a
// command_result frame, then a result frame with no session and zero usage.
// (Shape reported by wp-p0b from a live run; not recorded, because the real
// listing names the user's installed plugins.)
func TestAgyLineParser_SlashCommandStream(t *testing.T) {
	stream := `{"event":"command_result","command":{"name":"skills","data":{"skills":[{"name":"x","description":"d","path":"/p/x","builtin":true,"model_invocable":false}]}}}` + "\n" +
		`{"event":"result","result":{"conversation_id":"","status":"SUCCESS","response":"Skills available: x\n","duration_seconds":0,"num_turns":0,"usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":0,"cache_read_tokens":0,"total_tokens":0},"command":{"name":"skills","data":{"skills":[]}}}}`
	pr, evs := parse("agy", []byte(stream))
	if pr.Text != "Skills available: x" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "" || pr.Usage != (Usage{}) || pr.UsageCumulative || pr.Error != "" {
		t.Errorf("no session, no usage, no error expected: %+v", pr)
	}
	// The command frame is not text and not a token.
	wantKinds(t, evs, EventResult)

	// Even without the result frame (a killed run) the command JSON is not text.
	alone, _ := parse("agy", []byte(strings.SplitN(stream, "\n", 2)[0]))
	if alone.Text != "" {
		t.Errorf("a lone command_result frame must not become text: %q", alone.Text)
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
