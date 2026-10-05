package runtime

// lineparser_test.go pins the guarantees every LineParser gives (bounded,
// deterministic, NUL-free, alias-safe, plain-text fallback) plus the shared
// helpers. The per-harness goldens live beside each parser.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// ---- helpers ----------------------------------------------------------------

// readFixture loads tests/fixtures/runtime-streams/<name>. A missing fixture
// is a hard failure: a skipped golden is no golden.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "..", "tests", "fixtures", "runtime-streams", name)
	b, err := os.ReadFile(p) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

// feedAll feeds data line by line, the way ParseOutput does, and returns every
// event.
func feedAll(p LineParser, data []byte) []NativeEvent {
	var evs []NativeEvent
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		evs = append(evs, p.Feed(line)...)
	}
	return evs
}

// parse runs data through a fresh parser for runtimeName.
func parse(runtimeName string, data []byte) (ParseResult, []NativeEvent) {
	p := ParserFor(runtimeName)
	evs := feedAll(p, data)
	return p.Finish(), evs
}

func kindsOf(evs []NativeEvent) []NativeEventKind {
	out := make([]NativeEventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func wantKinds(t *testing.T, evs []NativeEvent, want ...NativeEventKind) {
	t.Helper()
	if got := kindsOf(evs); !reflect.DeepEqual(got, want) {
		t.Errorf("event kinds:\n got %v\nwant %v", got, want)
	}
}

// ---- ParserFor --------------------------------------------------------------

func TestParserFor_Selection(t *testing.T) {
	cases := []struct {
		name string
		want any
	}{
		{"claude", &claudeLineParser{}},
		{"codex", &codexLineParser{}},
		{"agy", &agyLineParser{}},
		{"gemini", &plainLineParser{}},
		{"some-plugin", &plainLineParser{}},
		{"", &plainLineParser{}},
	}
	for _, c := range cases {
		got := ParserFor(c.name)
		if reflect.TypeOf(got) != reflect.TypeOf(c.want) {
			t.Errorf("ParserFor(%q) = %T, want %T", c.name, got, c.want)
		}
	}
	if ParserFor("codex") == ParserFor("codex") {
		t.Error("ParserFor must return a fresh parser per call")
	}
}

// ---- plain-text fallback ----------------------------------------------------

func TestPlainParser_BlankLinesKeptTrailingNewlinesTrimmed(t *testing.T) {
	pr, evs := parse("gemini", []byte("para one\r\n\r\npara two\r\n\r\n"))
	if pr.Text != "para one\n\npara two" {
		t.Errorf("Text = %q", pr.Text)
	}
	if pr.Truncated || pr.Error != "" || pr.Usage != (Usage{}) || pr.SessionID != "" {
		t.Errorf("unexpected metadata: %+v", pr)
	}
	wantKinds(t, evs, EventToken, EventToken, EventToken, EventToken)
}

func TestParseOutput_EmptyAndNoTrailingNewline(t *testing.T) {
	if pr := ParseOutput(ParserFor("codex"), nil); pr.Text != "" {
		t.Errorf("empty output: Text = %q", pr.Text)
	}
	if pr := ParseOutput(ParserFor("agy"), []byte("no newline at end")); pr.Text != "no newline at end" {
		t.Errorf("Text = %q", pr.Text)
	}
}

// ---- NUL --------------------------------------------------------------------

func TestParsers_NULBytesStripped(t *testing.T) {
	nul := string(rune(0))
	cases := []struct {
		runtime string
		stream  string
	}{
		// a JSON \u0000 escape decodes to a NUL; a raw NUL byte can sit anywhere.
		{"claude", `{"type":"assistant","message":{"content":[{"type":"text","text":"a\u0000b` + nul + `c"}]}}`},
		{"codex", `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"a\u0000b` + nul + `c"}}`},
		{"agy", `{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"a\u0000b` + nul + `c"}}`},
		{"gemini", "a" + nul + "b" + nul + "c"},
	}
	for _, c := range cases {
		t.Run(c.runtime, func(t *testing.T) {
			pr, evs := parse(c.runtime, []byte(c.stream))
			if pr.Text != "abc" {
				t.Errorf("Text = %q, want %q", pr.Text, "abc")
			}
			for _, e := range evs {
				if strings.Contains(e.Text+e.ToolName+e.ToolInput+e.ToolOutput, nul) {
					t.Errorf("event carries a NUL: %+v", e)
				}
			}
		})
	}
}

func TestParsers_NULStrippedFromEveryDecodedField(t *testing.T) {
	// Fields other than text: session ids, tool names and tool output.
	codex := `{"type":"thread.started","thread_id":"t\u0000-1"}` + "\n" +
		`{"type":"item.completed","item":{"id":"c1","type":"command_execution","command":"ls\u0000","aggregated_output":"o\u0000k","exit_code":0,"status":"completed"}}`
	pr, evs := parse("codex", []byte(codex))
	if pr.SessionID != "t-1" {
		t.Errorf("SessionID = %q", pr.SessionID)
	}
	var sawTool bool
	for _, e := range evs {
		if e.Kind == EventToolResult && e.ToolOutput == "ok" {
			sawTool = true
		}
		if e.Kind == EventToolUse && e.ToolInput != `{"command":"ls"}` {
			t.Errorf("tool_use input = %q", e.ToolInput)
		}
	}
	if !sawTool {
		t.Errorf("tool_result output not NUL-stripped: %+v", evs)
	}
}

// ---- bounds -----------------------------------------------------------------

// goodLine returns, per runtime, one line that yields the text "after".
var goodLine = map[string]string{
	"claude": `{"type":"assistant","message":{"content":[{"type":"text","text":"after"}]}}`,
	"codex":  `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"after"}}`,
	"agy":    `{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"after"}}`,
	"plain":  `after`,
}

func TestParsers_OverlongLineIsDroppedAndFlagged(t *testing.T) {
	for _, rt := range []string{"claude", "codex", "agy", "plain"} {
		t.Run(rt, func(t *testing.T) {
			p := ParserFor(rt)
			if evs := p.Feed(bytes.Repeat([]byte("x"), MaxStreamLineBytes+1)); len(evs) != 0 {
				t.Errorf("overlong line produced events: %v", kindsOf(evs))
			}
			p.Feed([]byte(goodLine[rt]))
			pr := p.Finish()
			if !pr.Truncated {
				t.Error("Truncated must be set when a line was dropped for length")
			}
			if pr.LinesDropped != 1 || pr.TextCapped {
				t.Errorf("LinesDropped/TextCapped = %d/%v, want 1/false", pr.LinesDropped, pr.TextCapped)
			}
			if pr.Text != "after" {
				t.Errorf("a line after the dropped one must still parse; Text = %q", pr.Text)
			}
		})
	}
}

// A line of exactly MaxStreamLineBytes is accepted; one byte more is dropped.
func TestParsers_LineAtTheCapIsAcceptedOneOverIsDropped(t *testing.T) {
	const head = `{"type":"thread.started","thread_id":"abc","pad":"`
	const tail = `"}`
	atCap := head + strings.Repeat("a", MaxStreamLineBytes-len(head)-len(tail)) + tail
	if len(atCap) != MaxStreamLineBytes {
		t.Fatalf("test setup: line is %d bytes", len(atCap))
	}
	pr, _ := parse("codex", []byte(atCap))
	if pr.SessionID != "abc" || pr.Truncated {
		t.Errorf("a line at the cap must be parsed: %+v", pr)
	}
	pr, _ = parse("codex", []byte(atCap+"a"))
	if pr.SessionID != "" || !pr.Truncated {
		t.Errorf("a line over the cap must be dropped and flagged: %+v", pr)
	}
}

func TestParsers_TextCapIsOneMiB(t *testing.T) {
	chunk := strings.Repeat("a", 100_000)
	build := map[string]func() []byte{
		"claude": func() []byte {
			return []byte(strings.Repeat(`{"type":"assistant","message":{"content":[{"type":"text","text":"`+chunk+`"}]}}`+"\n", 12))
		},
		"codex": func() []byte {
			return []byte(strings.Repeat(`{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"`+chunk+`"}}`+"\n", 12))
		},
		"agy": func() []byte {
			return []byte(strings.Repeat(`{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"`+chunk+`"}}`+"\n", 12))
		},
		"plain": func() []byte { return []byte(strings.Repeat(chunk+"\n", 12)) },
	}
	for rt, mk := range build {
		t.Run(rt, func(t *testing.T) {
			pr, _ := parse(rt, mk())
			if !pr.Truncated {
				t.Error("Truncated must be set past the text cap")
			}
			if !pr.TextCapped || pr.LinesDropped != 0 {
				t.Errorf("TextCapped/LinesDropped = %v/%d, want true/0", pr.TextCapped, pr.LinesDropped)
			}
			// Message separators count toward the cap, so the cut lands inside a chunk.
			if len(pr.Text) > MaxParsedTextBytes || len(pr.Text) < MaxParsedTextBytes-16 {
				t.Errorf("len(Text) = %d, want ~%d", len(pr.Text), MaxParsedTextBytes)
			}
		})
	}
}

func TestParsers_TextCapNeverSplitsARune(t *testing.T) {
	// "€" is 3 bytes and 1 MiB is 1 mod 3, so the cut falls inside a rune.
	euro := strings.Repeat("€", 4000)
	line := `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"` + euro + `"}}` + "\n"
	pr, _ := parse("codex", []byte(strings.Repeat(line, 100)))
	if !pr.Truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(pr.Text) {
		t.Error("truncated text is not valid UTF-8")
	}
	if len(pr.Text) != MaxParsedTextBytes-1 {
		t.Errorf("len(Text) = %d, want %d (cut backed off to a rune boundary)", len(pr.Text), MaxParsedTextBytes-1)
	}
}

func TestParsers_ToolPayloadIsCapped(t *testing.T) {
	big := strings.Repeat("o", maxNativeEventPayloadBytes+5000)
	line := `{"type":"item.completed","item":{"id":"c","type":"command_execution","command":"x","aggregated_output":"` + big + `","exit_code":0,"status":"completed"}}`
	_, evs := parse("codex", []byte(line))
	for _, e := range evs {
		if e.Kind == EventToolResult {
			if len(e.ToolOutput) > maxNativeEventPayloadBytes+len(nativeTruncationMarker) {
				t.Errorf("tool output not capped: %d bytes", len(e.ToolOutput))
			}
			if !strings.HasSuffix(e.ToolOutput, nativeTruncationMarker) {
				t.Error("capped tool output lacks the truncation marker")
			}
			return
		}
	}
	t.Fatal("no tool_result event")
}

// ---- aliasing and determinism ------------------------------------------------

// fixtureFor names, per runtime, a fixture that exercises most event kinds.
var fixtureFor = map[string]string{
	"claude": "claude-stream-json-oneshot-SYNTHETIC.ndjson",
	"codex":  "codex-exec-json-0.154.0-SYNTHETIC-items.ndjson",
	"agy":    "agy-stream-json-1.2.17-tool.ndjson",
}

// The shared line reader reuses one buffer; a parser that kept a reference to
// a fed line would see it change. Feed from a buffer that is clobbered
// immediately after each call and require identical output.
func TestParsers_FeedDoesNotRetainTheLine(t *testing.T) {
	for rt, name := range fixtureFor {
		t.Run(rt, func(t *testing.T) {
			data := readFixture(t, name)
			wantRes, wantEvs := parse(rt, data)

			p := ParserFor(rt)
			var gotEvs []NativeEvent
			buf := make([]byte, 0, 4096)
			for _, line := range bytes.Split(data, []byte("\n")) {
				buf = append(buf[:0], line...)
				gotEvs = append(gotEvs, p.Feed(buf)...)
				for i := range buf { // clobber, as the next read would
					buf[i] = 'X'
				}
			}
			if !reflect.DeepEqual(p.Finish(), wantRes) {
				t.Errorf("result changed when the fed buffer was reused:\n got %+v\nwant %+v", p.Finish(), wantRes)
			}
			if !reflect.DeepEqual(gotEvs, wantEvs) {
				t.Error("events changed when the fed buffer was reused")
			}
		})
	}
}

func TestParsers_Deterministic(t *testing.T) {
	for rt, name := range fixtureFor {
		t.Run(rt, func(t *testing.T) {
			data := readFixture(t, name)
			r1, e1 := parse(rt, data)
			r2, e2 := parse(rt, data)
			if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(e1, e2) {
				t.Error("same input produced different output")
			}
		})
	}
}

func TestParsers_RawIsABoundedCopy(t *testing.T) {
	long := `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"` + strings.Repeat("a", 3*maxRawEventBytes) + `"}}`
	_, evs := parse("codex", []byte(long))
	if len(evs) != 1 || len(evs[0].Raw) != maxRawEventBytes {
		t.Fatalf("Raw length = %d, want %d", len(evs[0].Raw), maxRawEventBytes)
	}
}

// Finish may be called repeatedly and does not consume the parser.
func TestParsers_FinishIsIdempotent(t *testing.T) {
	p := ParserFor("codex")
	feedAll(p, readFixture(t, "codex-exec-json-0.154.0-ok.ndjson"))
	a, b := p.Finish(), p.Finish()
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Finish not idempotent: %+v vs %+v", a, b)
	}
}

// ---- text joining -----------------------------------------------------------

func TestTextAccumulator_MessageBoundaries(t *testing.T) {
	var a textAccumulator
	a.beginMessage()
	a.add("first")
	a.beginMessage()
	a.add("second\n")
	a.beginMessage()
	a.add("third")
	if got := a.text(); got != "first\nsecond\nthird" {
		t.Errorf("text = %q", got)
	}

	// Fragments of ONE message are never separated.
	var b textAccumulator
	b.beginMessage()
	b.add("ap")
	b.add("ple")
	if got := b.text(); got != "apple" {
		t.Errorf("fragments joined with a separator: %q", got)
	}
}

// ---- error messages -----------------------------------------------------------

func TestParsers_FailureMessagesAreBounded(t *testing.T) {
	big := strings.Repeat("e", 3*maxErrorBytes)
	cases := map[string]string{
		"claude": `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"` + big + `"}`,
		"codex":  `{"type":"turn.failed","error":{"message":"` + big + `"}}`,
		"agy":    `{"event":"result","result":{"conversation_id":"c","status":"ERROR","error":"` + big + `"}}`,
	}
	for rt, line := range cases {
		t.Run(rt, func(t *testing.T) {
			pr, evs := parse(rt, []byte(line))
			if len(pr.Error) == 0 || len(pr.Error) > maxErrorBytes+len("...") {
				t.Errorf("len(Error) = %d, want 1..%d", len(pr.Error), maxErrorBytes+3)
			}
			for _, e := range evs {
				if e.Kind == EventError && len(e.Text) > maxErrorBytes+len("...") {
					t.Errorf("error event text is %d bytes", len(e.Text))
				}
			}
		})
	}
}

// ---- why a parse is truncated -------------------------------------------------

// Truncated is exactly "the text cap was hit or a line was dropped", and the two
// reasons are reported separately so a caller can tell a user which one it was.
func TestParsers_TruncationReasonsAreReportedSeparately(t *testing.T) {
	long := bytes.Repeat([]byte("x"), MaxStreamLineBytes+1)
	chunk := strings.Repeat("a", 100_000)
	for _, rt := range []string{"claude", "codex", "agy", "plain"} {
		t.Run(rt, func(t *testing.T) {
			// A clean parse reports nothing.
			p := ParserFor(rt)
			p.Feed([]byte(goodLine[rt]))
			if pr := p.Finish(); pr.Truncated || pr.TextCapped || pr.LinesDropped != 0 {
				t.Errorf("clean parse: %+v", pr)
			}

			// Three dropped lines are counted, and are not a text cap.
			p = ParserFor(rt)
			for i := 0; i < 3; i++ {
				p.Feed(long)
			}
			p.Feed([]byte(goodLine[rt]))
			if pr := p.Finish(); pr.LinesDropped != 3 || pr.TextCapped || !pr.Truncated {
				t.Errorf("dropped lines: LinesDropped=%d TextCapped=%v Truncated=%v", pr.LinesDropped, pr.TextCapped, pr.Truncated)
			}

			// Hitting the text cap is not a dropped line.
			p = ParserFor(rt)
			line := map[string]string{
				"claude": `{"type":"assistant","message":{"content":[{"type":"text","text":"` + chunk + `"}]}}`,
				"codex":  `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"` + chunk + `"}}`,
				"agy":    `{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"` + chunk + `"}}`,
				"plain":  chunk,
			}[rt]
			for i := 0; i < 12; i++ {
				p.Feed([]byte(line))
			}
			if pr := p.Finish(); !pr.TextCapped || pr.LinesDropped != 0 || !pr.Truncated {
				t.Errorf("text cap: TextCapped=%v LinesDropped=%d Truncated=%v", pr.TextCapped, pr.LinesDropped, pr.Truncated)
			}

			// Both at once.
			p = ParserFor(rt)
			p.Feed(long)
			for i := 0; i < 12; i++ {
				p.Feed([]byte(line))
			}
			if pr := p.Finish(); !pr.TextCapped || pr.LinesDropped != 1 || !pr.Truncated {
				t.Errorf("both: TextCapped=%v LinesDropped=%d Truncated=%v", pr.TextCapped, pr.LinesDropped, pr.Truncated)
			}
		})
	}
}
