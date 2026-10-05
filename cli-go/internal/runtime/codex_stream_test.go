package runtime

import (
	"strings"
	"testing"
)

// The goldens below replay recordings made with codex-cli 0.154.0; see
// tests/fixtures/runtime-streams/README.md. Token expectations are the
// recorded numbers run through the documented normalization (input minus
// cached).

func TestCodexLineParser_RealOK(t *testing.T) {
	pr, evs := parse("codex", readFixture(t, "codex-exec-json-0.154.0-ok.ndjson"))
	if pr.Text != "ok" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.SessionID != "01a10c3a-338e-71f3-a8b8-6aef08430c40" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	// recorded: input 15131, cached 7424, cache_write 0, output 5.
	want := Usage{InputTokens: 7707, OutputTokens: 5, CacheRead: 7424}
	if pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	if pr.Error != "" || pr.Truncated || pr.ModelID != "" {
		t.Errorf("unexpected: %+v", pr)
	}
	wantKinds(t, evs, EventSession, EventToken, EventResult)
	if evs[1].Text != "ok" || evs[2].Usage != want || evs[2].SessionID != pr.SessionID {
		t.Errorf("events = %+v", evs)
	}
}

func TestCodexLineParser_RealCommand(t *testing.T) {
	pr, evs := parse("codex", readFixture(t, "codex-exec-json-0.154.0-command.ndjson"))
	// Two agent messages: the first ends in a newline already, so no extra one is added.
	if want := "I’ll run the command now.\ndone"; pr.Text != want {
		t.Errorf("Text = %q, want %q", pr.Text, want)
	}
	// recorded: input 30394, cached 27392, output 50.
	if want := (Usage{InputTokens: 3002, OutputTokens: 50, CacheRead: 27392}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	wantKinds(t, evs, EventSession, EventToken, EventToolUse, EventToolResult, EventToken, EventResult)
	if use := evs[2]; use.ToolName != "command_execution" || use.ToolInput != `{"command":"/bin/zsh -lc 'echo hello-from-codex'"}` {
		t.Errorf("tool_use = %+v", use)
	}
	if res := evs[3]; res.ToolOutput != "hello-from-codex\n" || res.IsError {
		t.Errorf("tool_result = %+v", res)
	}
}

// A run that failed before any answer: a warning item, a transient-looking
// error event, then turn.failed. The reported message is the INNER message of
// the JSON document codex forwards.
func TestCodexLineParser_RealFailure(t *testing.T) {
	pr, evs := parse("codex", readFixture(t, "codex-exec-json-0.154.0-failed.ndjson"))
	const want = "The 'not-a-real-model-xyz' model is not supported when using Codex with a ChatGPT account."
	if pr.Error != want {
		t.Errorf("Error = %q, want %q", pr.Error, want)
	}
	if pr.Text != "" || pr.Usage != (Usage{}) {
		t.Errorf("a failed turn has no text or usage: %+v", pr)
	}
	if pr.SessionID != "01a10c3b-d771-7bd3-8bf4-6ecea4027a00" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	wantKinds(t, evs, EventSession, EventError, EventError, EventError)
	// Only the turn.failed is fatal; the warning item and the error event are not.
	if evs[1].Fatal || evs[2].Fatal || !evs[3].Fatal {
		t.Errorf("fatal flags = %v %v %v", evs[1].Fatal, evs[2].Fatal, evs[3].Fatal)
	}
	if !strings.HasPrefix(evs[1].Text, "Model metadata for `not-a-real-model-xyz` not found") {
		t.Errorf("warning text = %q", evs[1].Text)
	}
}

func TestCodexLineParser_SyntheticItems(t *testing.T) {
	pr, evs := parse("codex", readFixture(t, "codex-exec-json-0.154.0-SYNTHETIC-items.ndjson"))
	if want := "Adding the file now.\nTests fail with exit code 2."; pr.Text != want {
		t.Errorf("Text = %q, want %q", pr.Text, want)
	}
	// input 1000 includes cached 400 and cache write 100: fresh is 500.
	if want := (Usage{InputTokens: 500, OutputTokens: 50, CacheRead: 400, CacheCreation: 100}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
	wantKinds(t, evs,
		EventSession,
		EventThinking,
		EventToken,
		EventToolUse, EventToolResult, // file_change
		EventToolUse, EventToolResult, // failing command
		EventToolUse, EventToolResult, // mcp_tool_call
		EventToolUse, EventToolResult, // web_search
		EventToken,
		EventResult,
	)
	if evs[1].Text != "Plan: add the file, then run the tests." {
		t.Errorf("reasoning = %q", evs[1].Text)
	}
	if u, r := evs[3], evs[4]; u.ToolName != "file_change" ||
		u.ToolInput != `{"changes":[{"path":"src/new.go","kind":"add"},{"path":"src/old.go","kind":"update"}]}` ||
		r.ToolOutput != "add src/new.go\nupdate src/old.go" || r.IsError {
		t.Errorf("file_change = %+v / %+v", u, r)
	}
	// A failing command is a tool error and names its exit code.
	if r := evs[6]; !r.IsError || r.ToolOutput != "FAIL src 0.01s\n\n[exit code 2]" {
		t.Errorf("failing command result = %+v", r)
	}
	if u, r := evs[7], evs[8]; u.ToolName != "mcp__docs__search" || u.ToolInput != `{"query":"retry policy"}` || r.ToolOutput != "3 results" || r.IsError {
		t.Errorf("mcp call = %+v / %+v", u, r)
	}
	if u := evs[9]; u.ToolName != "web_search" || u.ToolInput != `{"query":"go context cancellation"}` {
		t.Errorf("web_search = %+v", u)
	}
}

// codex reports each command twice (started, completed); the tool_use is
// emitted once.
func TestCodexLineParser_ToolUseEmittedOncePerItem(t *testing.T) {
	_, evs := parse("codex", readFixture(t, "codex-exec-json-0.154.0-command.ndjson"))
	uses := 0
	for _, e := range evs {
		if e.Kind == EventToolUse {
			uses++
		}
	}
	if uses != 1 {
		t.Errorf("tool_use events = %d, want 1", uses)
	}
}

func TestCodexLineParser_UsageAbsent(t *testing.T) {
	stream := `{"type":"thread.started","thread_id":"t"}` + "\n" +
		`{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"killed before turn.completed"}}`
	pr, _ := parse("codex", []byte(stream))
	if pr.Usage != (Usage{}) {
		t.Errorf("Usage = %+v, want the zero value", pr.Usage)
	}
	if pr.Text != "killed before turn.completed" || pr.Error != "" {
		t.Errorf("result = %+v", pr)
	}
}

func TestCodexLineParser_UsageSumsAcrossTurns(t *testing.T) {
	stream := `{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":50,"cached_input_tokens":50,"output_tokens":5}}`
	pr, _ := parse("codex", []byte(stream))
	if want := (Usage{InputTokens: 60, OutputTokens: 15, CacheRead: 90}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
}

// Cached tokens can never push the fresh count below zero.
func TestCodexUsage_NormalizeFloorsAtZero(t *testing.T) {
	u := codexUsage{InputTokens: 10, CachedInputTokens: 25, OutputTokens: 1}.normalize()
	if u.InputTokens != 0 || u.CacheRead != 25 {
		t.Errorf("normalize = %+v", u)
	}
}

// The OpenAI-style field names the bash parser also accepted.
func TestCodexUsage_AliasFieldNames(t *testing.T) {
	stream := `{"type":"turn.completed","usage":{"prompt_tokens":70,"completion_tokens":7,"cache_read_input_tokens":20}}`
	pr, _ := parse("codex", []byte(stream))
	if want := (Usage{InputTokens: 50, OutputTokens: 7, CacheRead: 20}); pr.Usage != want {
		t.Errorf("Usage = %+v, want %+v", pr.Usage, want)
	}
}

// A top-level error event is a notice: codex reports transient stream retries
// that way. It is the failure only when the turn never completed.
func TestCodexLineParser_ErrorEventSemantics(t *testing.T) {
	retry := `{"type":"thread.started","thread_id":"t"}` + "\n" +
		`{"type":"error","message":"Reconnecting... 2/5"}` + "\n" +
		`{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"recovered"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`
	if pr, _ := parse("codex", []byte(retry)); pr.Error != "" || pr.Text != "recovered" {
		t.Errorf("a retried-then-completed run is not an error: %+v", pr)
	}

	died := `{"type":"thread.started","thread_id":"t"}` + "\n" + `{"type":"error","message":"stream closed"}`
	if pr, _ := parse("codex", []byte(died)); pr.Error != "stream closed" {
		t.Errorf("Error = %q, want the last error event when the turn never completed", pr.Error)
	}

	// A warning ITEM alone, on a run that then completed, is never an error.
	warned := `{"type":"item.completed","item":{"id":"w","type":"error","message":"fallback metadata"}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`
	if pr, _ := parse("codex", []byte(warned)); pr.Error != "" {
		t.Errorf("Error = %q", pr.Error)
	}
}

func TestUnwrapCodexError(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"type":"error","status":400,"error":{"type":"invalid_request_error","message":"bad model"}}`, "bad model"},
		{`{"message":"flat"}`, "flat"},
		{`plain message`, "plain message"},
		{`{not json`, `{not json`},
		{`  spaced  `, "spaced"},
		{`{"unrelated":1}`, `{"unrelated":1}`},
	}
	for _, c := range cases {
		if got := unwrapCodexError(c.in); got != c.want {
			t.Errorf("unwrapCodexError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCodexLineParser_TruncatedLineInStructuredStream(t *testing.T) {
	lines := strings.Split(strings.TrimRight(string(readFixture(t, "codex-exec-json-0.154.0-ok.ndjson")), "\n"), "\n")
	lines[2] = lines[2][:len(lines[2])/2] // the agent_message line, cut in half
	pr, _ := parse("codex", []byte(strings.Join(lines, "\n")))
	if pr.Text != "" {
		t.Errorf("Text = %q, want empty (the only message was cut)", pr.Text)
	}
	if pr.SessionID == "" || pr.Usage.OutputTokens != 5 {
		t.Errorf("lines around the cut one must still parse: %+v", pr)
	}
}

// With nothing recognisable the whole stream is plain text, so a codex
// invocation that prints prose (`--output-last-message -`) still works.
func TestCodexLineParser_PlainTextFallback(t *testing.T) {
	pr, _ := parse("codex", []byte("Final answer.\n\nSecond paragraph.\n"))
	if pr.Text != "Final answer.\n\nSecond paragraph." {
		t.Errorf("Text = %q", pr.Text)
	}
	// A single cut-off JSON line is just text too.
	pr, _ = parse("codex", []byte(`{"type":"item.completed","item":{"id":"i","type":"agent_mes`))
	if !strings.HasPrefix(pr.Text, `{"type":"item.completed"`) {
		t.Errorf("Text = %q", pr.Text)
	}
}

func TestCodexLineParser_JSONInProseStaysPlain(t *testing.T) {
	prose := "A schema:\n" + `{"type":"object","required":["a"]}` + "\ndone"
	pr, _ := parse("codex", []byte(prose))
	if pr.Text != prose {
		t.Errorf("Text = %q, want the prose unchanged", pr.Text)
	}
}

func TestCodexLineParser_NoiseInStructuredStreamIgnored(t *testing.T) {
	stream := "Reading additional input from stdin...\n" +
		`{"type":"thread.started","thread_id":"t"}` + "\n" +
		"some banner\n" +
		`{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"answer"}}` + "\n"
	pr, _ := parse("codex", []byte(stream))
	if pr.Text != "answer" {
		t.Errorf("Text = %q", pr.Text)
	}
}
