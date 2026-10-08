package repl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStreamRenderingGolden(t *testing.T) {
	d := newFakeDaemon(t)
	exit1, dur := 0, 2.25
	cost := 0.0123
	d.script = func(DispatchRequest) []Event {
		return []Event{
			{Type: "route", Route: &Route{Runtime: "codex", Model: "gpt-x", Pinned: "override", Reason: "asked with @codex", RuleID: "r-1"}},
			{Type: "handoff", Handoff: &Handoff{From: "claude", To: "codex", Turns: 3, DigestBytes: 812, Redactions: 1}},
			{Type: "thinking", Thinking: "weighing "},
			{Type: "thinking", Thinking: "options"},
			{Type: "token", Text: "Looking"},
			{Type: "token", Text: " at it.\n"},
			{Type: "tool_use", ToolName: "Bash", ToolInput: `{"command":"ls -la"}`},
			{Type: "tool_result", ToolName: "Bash", ToolOutput: "a\nb\nc"},
			{Type: "tool_use", ToolName: "Read", ToolInput: "x"},
			{Type: "tool_result", ToolName: "Read", ToolOutput: "denied", IsError: true},
			{Type: "token", Text: "Done \x1b[31mred\x1b[0m ‮done"},
			{Type: "summary", ExitCode: &exit1, DurationS: &dur, TotalCostUSD: &cost, ModelResolved: "gpt-x", RuntimeResolved: "codex"},
		}
	}
	got := run(t, d, nil, "@codex:gpt-x fix it\n")
	golden := "testdata/stream.golden"
	if os.Getenv("REPL_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("stream rendering drifted from %s\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
	}
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x202e) {
		t.Fatal("control characters reached the terminal")
	}
}

func TestOverridePrefixRidesTheRequest(t *testing.T) {
	d := newFakeDaemon(t)
	run(t, d, func(c *Config) { c.Harness, c.Model = "claude", "opus-x" }, "@codex:gpt-x do it\nplain\n@bogus x\n@agy ask\n")
	if len(d.dispatches) != 4 {
		t.Fatalf("dispatches = %d", len(d.dispatches))
	}
	if g := d.dispatches[3]; g.OverrideRuntime != "agy" || g.Task != "ask" {
		t.Errorf("@agy turn = %+v", g)
	}
	a, b, c := d.dispatches[0], d.dispatches[1], d.dispatches[2]
	if a.OverrideRuntime != "codex" || a.OverrideModel != "gpt-x" || a.Task != "do it" {
		t.Errorf("override turn = %+v", a)
	}
	if a.Runtime != "claude" || a.Model != "opus-x" {
		t.Errorf("the pane's own settings must still ride along: %+v", a)
	}
	if b.OverrideRuntime != "" || b.Task != "plain" {
		t.Errorf("plain turn = %+v", b)
	}
	if c.OverrideRuntime != "" || c.Task != "@bogus x" {
		t.Errorf("unknown prefix must stay ordinary text: %+v", c)
	}
	if a.ConversationID != b.ConversationID || a.SessionID != b.SessionID || a.OperatorID != "op-test" {
		t.Errorf("one conversation, one session, the client's operator: %+v %+v", a, b)
	}
}

func TestTurnWaitsOutA409(t *testing.T) {
	d := newFakeDaemon(t)
	d.busy = 2
	sleeps := 0
	out := run(t, d, func(c *Config) { c.Sleep = func(time.Duration) { sleeps++ } }, "hi\n")
	if sleeps != 2 || len(d.dispatches) != 1 {
		t.Fatalf("sleeps=%d dispatches=%d", sleeps, len(d.dispatches))
	}
	if !strings.Contains(out, "a turn is still running on this session; waiting") || !strings.Contains(out, "hello") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestTurnGivesUpOnA409(t *testing.T) {
	d := newFakeDaemon(t)
	d.busy = 100
	out := run(t, d, func(c *Config) { c.BusyWaits = 3 }, "hi\n")
	if len(d.dispatches) != 0 || !strings.Contains(out, "did not finish") {
		t.Fatalf("dispatches=%d output:\n%s", len(d.dispatches), out)
	}
}

func TestOtherConflictIsNotWaited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "conversation is pinned to a different runtime; start a new conversation to switch", 409)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Token: testToken, OperatorID: "op"}
	err := c.Dispatch(t.Context(), DispatchRequest{})
	if err == nil || err == ErrTurnInFlight || !strings.Contains(err.Error(), "pinned to a different runtime") {
		t.Fatalf("err = %v", err)
	}
}

func TestInterruptCancelsTurn(t *testing.T) {
	d := newFakeDaemon(t)
	d.hold = true
	pr, pw := io.Pipe()
	intr := make(chan struct{}, 1)
	var out bytes.Buffer
	cfg := Config{Client: d.client(), In: pr, Out: &out, NewID: seqIDs(), Interrupt: intr, Sleep: func(time.Duration) {}}
	done := make(chan error, 1)
	go func() { done <- New(cfg).Run(t.Context()) }()
	d.waitSubs(1)
	_, _ = pw.Write([]byte("slow job\n"))
	for i := 0; i < 400; i++ {
		d.mu.Lock()
		n := len(d.dispatches)
		d.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	intr <- struct{}{}
	for i := 0; i < 400; i++ {
		d.mu.Lock()
		n := len(d.cancels)
		d.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(d.cancels) != 1 || d.cancels[0] != "sess-2" {
		t.Fatalf("cancels = %v", d.cancels)
	}
}

func TestInterruptAtPromptOnlyHints(t *testing.T) {
	d := newFakeDaemon(t)
	pr, pw := io.Pipe()
	intr := make(chan struct{}, 1)
	var out bytes.Buffer
	cfg := Config{Client: d.client(), In: pr, Out: &out, NewID: seqIDs(), Interrupt: intr}
	done := make(chan error, 1)
	go func() { done <- New(cfg).Run(t.Context()) }()
	d.waitSubs(1)
	intr <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Ctrl-C at the prompt does nothing") || len(d.cancels) != 0 {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestAskUserQuestionAnswered(t *testing.T) {
	d := newFakeDaemon(t)
	d.script = func(DispatchRequest) []Event {
		return []Event{
			{Type: "ask_user_question", AskToolUseID: "tu-1", AskQuestions: `[{"question":"Which db?","header":"DB","options":[{"label":"sqlite","description":"file"},{"label":"pg"}]}]`},
			{Type: "token", Text: "ok"}, summary(0),
		}
	}
	out := run(t, d, nil, "set up storage\n2\n")
	if len(d.answers) != 1 {
		t.Fatalf("answers = %d\n%s", len(d.answers), out)
	}
	a := d.answers[0]
	if a.ToolUseID != "tu-1" || a.Answers["Which db?"] != "pg" || a.ConversationID != "conv-1" || a.OperatorID != "op-test" {
		t.Fatalf("answer = %+v", a)
	}
	if !strings.Contains(out, "[DB] Which db?") || !strings.Contains(out, "1) sqlite - file") {
		t.Fatalf("question not rendered:\n%s", out)
	}
}

func TestFreeTextAnswer(t *testing.T) {
	d := newFakeDaemon(t)
	d.script = func(DispatchRequest) []Event {
		return []Event{{Type: "ask_user_question", AskToolUseID: "tu-2", AskQuestions: `[{"question":"Name?","options":[]}]`}, summary(0)}
	}
	run(t, d, nil, "go\nbob\n")
	if len(d.answers) != 1 || d.answers[0].Answers["Name?"] != "bob" {
		t.Fatalf("answers = %+v", d.answers)
	}
}

func TestSlashCommands(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  []string // substrings of the output
		check func(t *testing.T, d *fakeDaemon)
	}{
		{"harness", "/harness codex\n/harness\n", []string{"codex pinned"}, nil},
		{"harness unknown", "/harness gemini\n", []string{`unknown harness "gemini"`}, nil},
		{"harness auto", "/harness codex\n/harness auto\n", []string{"router chooses"}, nil},
		{"model list", "/harness claude\n/model\n", []string{"claude/opus-x"}, nil},
		{"model pin", "/harness codex\n/model gpt-x\nhi\n", []string{"codex / gpt-x pinned"}, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].Runtime != "codex" || d.dispatches[0].Model != "gpt-x" {
				t.Errorf("request = %+v", d.dispatches[0])
			}
		}},
		{"model wrong harness", "/harness claude\n/model gpt-x\n", []string{"gpt-x belongs to codex, not claude"}, nil},
		{"model needs harness", "/model gpt-x\n", []string{"pin a harness first"}, nil},
		{"model invalid", "/model Bad/Id\n", []string{"invalid model id"}, nil},
		{"harness change drops model", "/harness codex\n/model gpt-x\n/harness claude\nhi\n", nil, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].Model != "" {
				t.Errorf("model leaked across harnesses: %+v", d.dispatches[0])
			}
		}},
		{"auto", "/harness codex\n/model gpt-x\n/auto\nhi\n", []string{"router chooses"}, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].Runtime != "" || d.dispatches[0].Model != "" {
				t.Errorf("request = %+v", d.dispatches[0])
			}
		}},
		{"skill", "/skill review the diff\n", nil, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].Task != "/review the diff" {
				t.Errorf("task = %q", d.dispatches[0].Task)
			}
		}},
		{"skill palette", "/review now\n", nil, func(t *testing.T, d *fakeDaemon) {
			if len(d.dispatches) != 1 || d.dispatches[0].Task != "/review now" {
				t.Errorf("dispatches = %+v", d.dispatches)
			}
		}},
		{"skill unknown", "/skill nope\n", []string{`unknown skill "nope"`}, nil},
		{"skill list", "/skill\n", []string{"skills: /review"}, nil},
		{"unknown command", "/nope\n", []string{"unknown command /nope"}, nil},
		{"compact", "/compact\n", nil, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].Task != "/compact" {
				t.Errorf("task = %q", d.dispatches[0].Task)
			}
		}},
		{"compact codex", "/harness codex\n/compact\n", []string{"/compact is a claude command"}, func(t *testing.T, d *fakeDaemon) {
			if len(d.dispatches) != 0 {
				t.Error("compact must not be sent to codex")
			}
		}},
		{"new", "/new\nhi\n", []string{"new conversation conv-3"}, func(t *testing.T, d *fakeDaemon) {
			if d.dispatches[0].ConversationID != "conv-3" {
				t.Errorf("conversation = %q", d.dispatches[0].ConversationID)
			}
		}},
		{"cost", "hi\nhi\n/cost\n", []string{"2 turn(s), $0.0200 total, $0.0100 last turn"}, nil},
		{"help", "/help\n", []string{"/harness", "/attach", "1 skills available"}, nil},
		{"exit stops", "/exit\nhi\n", nil, func(t *testing.T, d *fakeDaemon) {
			if len(d.dispatches) != 0 {
				t.Error("input after /exit must not run")
			}
		}},
		{"detach idle", "/detach\n", []string{"not attached"}, nil},
		{"attach unavailable", "/attach claude\n", []string{"/attach is not available"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDaemon(t)
			out := run(t, d, nil, tc.in)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			if tc.check != nil {
				tc.check(t, d)
			}
		})
	}
}

func TestResumeShowsTailAndSwitches(t *testing.T) {
	d := newFakeDaemon(t)
	d.transcript = []TranscriptEntry{
		{Role: "user", Text: "first question"},
		{Role: "route", Runtime: "claude", Model: "opus-x", Text: "default"},
		{Role: "assistant", Text: "the "}, {Role: "assistant", Text: "answer\x1b[2J"},
		{Role: "summary"},
	}
	out := run(t, d, nil, "/resume conv-old\nnext\n")
	if !strings.Contains(out, "> first question") || !strings.Contains(out, "the answer") || strings.ContainsRune(out, 0x1b) {
		t.Fatalf("tail:\n%s", out)
	}
	if d.dispatches[0].ConversationID != "conv-old" {
		t.Fatalf("conversation = %q", d.dispatches[0].ConversationID)
	}
	d2 := newFakeDaemon(t)
	out = run(t, d2, nil, "/resume\n/resume bad id!\n/resume conv-empty\n")
	for _, w := range []string{"conversation conv-1", "invalid conversation id", "no transcript"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestAttachRunsHook(t *testing.T) {
	d := newFakeDaemon(t)
	var got []string
	out := run(t, d, func(c *Config) {
		c.Attach = func(_ context.Context, rt string) error { got = append(got, rt); return nil }
	}, "/attach codex\n/attach vim\n/attach\n")
	if len(got) != 1 || got[0] != "codex" {
		t.Fatalf("attach calls = %v", got)
	}
	if !strings.Contains(out, "back in the yakOS REPL") || strings.Count(out, "usage: /attach") != 2 {
		t.Fatalf("output:\n%s", out)
	}
}

func TestServerTextIsSanitized(t *testing.T) {
	d := newFakeDaemon(t)
	d.script = func(DispatchRequest) []Event {
		return []Event{
			{Type: "route", Route: &Route{Runtime: "claude\x1b]0;pwn\x07", Reason: "why\x1b[2J"}},
			{Type: "error", Text: "boom\x1b[31m"},
		}
	}
	out := run(t, d, nil, "x\n")
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Fatalf("escape reached the terminal: %q", out)
	}
}

func TestMirroredSessionsAreIgnored(t *testing.T) {
	d := newFakeDaemon(t)
	d.script = func(DispatchRequest) []Event {
		return []Event{{SessionID: "other-pane", Type: "token", Text: "LEAK"}, {Type: "token", Text: "mine"}, summary(0)}
	}
	out := run(t, d, nil, "hi\n")
	if strings.Contains(out, "LEAK") || !strings.Contains(out, "mine") {
		t.Fatalf("another session's tokens rendered:\n%s", out)
	}
}

func TestDispatchErrorsShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat/stream" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown agent \"claude\"; not in roster"})
	}))
	defer srv.Close()
	var out bytes.Buffer
	c := &Client{Base: srv.URL, Token: testToken, OperatorID: "op"}
	cfg := Config{Client: c, In: strings.NewReader("hi\n"), Out: &out}
	if err := New(cfg).Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "error: daemon answered 400: unknown agent") {
		t.Fatalf("output:\n%s", out.String())
	}
}

// The stale Ctrl-C is either eaten at the prompt (harmless) or, when the next
// line is already waiting, picked by the turn's select; Go's select chooses at
// random, so repeat until the mutant cannot hide.
func TestInterruptDuringAttachDoesNotCancelNextTurn(t *testing.T) {
	for i := 0; i < 16; i++ {
		d := newFakeDaemon(t)
		d.script = func(DispatchRequest) []Event { // late enough that a stale Ctrl-C would be seen first
			time.Sleep(40 * time.Millisecond)
			return []Event{{Type: "token", Text: "ok"}, summary(0)}
		}
		intr := make(chan struct{}, 1)
		run(t, d, func(c *Config) {
			c.Interrupt = intr
			c.Attach = func(context.Context, string) error { intr <- struct{}{}; return nil } // Ctrl-C meant for the TUI
		}, "/attach claude\nhello\n")
		if len(d.cancels) != 0 || len(d.dispatches) != 1 {
			t.Fatalf("run %d: cancels=%v dispatches=%d", i, d.cancels, len(d.dispatches))
		}
	}
}
