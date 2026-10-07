package runtime

// k144_parsers_test.go: goldens for the codex and agy line parsers over the
// version-pinned recordings in testdata/<harness>/<version>/ (K-144), the
// streamed-equals-buffered property, the defects a real pipe produces
// (truncated, malformed, oversize and stray lines, missing usage), the usage
// math across a three-turn agy conversation, and a fuzz target per parser.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func readPinned(t *testing.T, harness, name string) []byte {
	t.Helper()
	v := RecordedVersions[harness]
	b, err := os.ReadFile(filepath.Join("testdata", harness, v, name+".ndjson")) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("pinned fixture %s/%s/%s: %v", harness, v, name, err)
	}
	return b
}

// The table in fixture_version.go and the directories under testdata move
// together: the newest directory is the recorded version.
func TestRecordedVersionsMatchTestdata(t *testing.T) {
	for harness, v := range RecordedVersions {
		dirs, err := os.ReadDir(filepath.Join("testdata", harness))
		if err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		if len(dirs) == 0 || dirs[len(dirs)-1].Name() != v {
			t.Errorf("%s: RecordedVersions says %s but testdata holds %v", harness, v, dirs)
		}
	}
}

func TestVersionParseAndSkew(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"codex-cli 0.154.0\n", "0.154.0"}, {"1.3.1\n", "1.3.1"}, {"agy version 1.3.1 (build 9)\nmore 9.9.9", "1.3.1"},
		{"", ""}, {"no digits here", ""},
	} {
		if got := ParseVersion(c.in); got != c.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, skew := VersionSkew("codex", "0.154.0"); skew {
		t.Error("the recorded version is not a skew")
	}
	if rec, skew := VersionSkew("codex", "0.200.0"); !skew || rec != "0.154.0" {
		t.Errorf("VersionSkew = %q, %v", rec, skew)
	}
	for _, c := range [][2]string{{"codex", ""}, {"claude", "9.9.9"}} {
		if _, skew := VersionSkew(c[0], c[1]); skew {
			t.Errorf("VersionSkew(%q, %q) must report nothing", c[0], c[1])
		}
	}
	long := strings.Repeat("x", 600) + " 9.9.9"
	if ParseVersion(long) != "" {
		t.Error("ParseVersion must only read the first 512 bytes")
	}
}

func TestPinnedRecordingsContainNoPersonalData(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*", "*", "*.ndjson"))
	if len(files) < 9 {
		t.Fatalf("expected the pinned recordings, found %d", len(files))
	}
	id := currentIdentity()
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // test path
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range id.findPersonalData(string(b)) {
			t.Errorf("%s contains %s", f, hit)
		}
	}
}

func TestPinnedGoldens(t *testing.T) {
	for _, c := range []struct {
		harness, name string
		text          string
		session       string
		usage         Usage
		kinds         []NativeEventKind // distinct kinds, in first-seen order
		err           bool
	}{
		{"codex", "ok", "ok", "", Usage{}, []NativeEventKind{EventSession, EventToken, EventResult}, false},
		{"codex", "command", "I’ll run the command now.\ndone", "01a10c3a-86a8-7ba3-ac6f-d3e51daa8d78",
			Usage{InputTokens: 3002, OutputTokens: 50, CacheRead: 27392}, []NativeEventKind{EventSession, EventToken, EventToolUse, EventToolResult, EventResult}, false},
		{"codex", "SYNTHETIC-tool-error", "Running the tests.\nThe command failed.", "00000000-0000-7000-8000-0000000000aa",
			Usage{InputTokens: 400, OutputTokens: 40, CacheRead: 600}, []NativeEventKind{EventSession, EventToken, EventToolUse, EventToolResult, EventResult}, false},
		{"codex", "failed", "", "", Usage{}, []NativeEventKind{EventSession, EventError}, true},
		{"agy", "ok", "ok", "", Usage{InputTokens: 12859, OutputTokens: 1, DurationMs: 2006}, []NativeEventKind{EventSession, EventToken, EventResult}, false},
		{"agy", "tool", "", "", Usage{}, []NativeEventKind{EventSession, EventToolUse, EventToolResult, EventToken, EventResult}, false},
		{"agy", "t2", "two", "", Usage{InputTokens: 13067, OutputTokens: 1}, []NativeEventKind{EventSession, EventToken, EventResult}, false},
	} {
		t.Run(c.harness+"/"+c.name, func(t *testing.T) {
			pr, evs := parse(c.harness, readPinned(t, c.harness, c.name))
			var kinds []NativeEventKind
			seen := map[NativeEventKind]bool{}
			for _, e := range evs {
				if !seen[e.Kind] {
					seen[e.Kind] = true
					kinds = append(kinds, e.Kind)
				}
			}
			if !equalKinds(kinds, c.kinds) {
				t.Errorf("event kinds = %v, want %v", kinds, c.kinds)
			}
			if c.text != "" && pr.Text != c.text {
				t.Errorf("Text = %q, want %q", pr.Text, c.text)
			}
			if c.usage != (Usage{}) && pr.Usage != c.usage {
				t.Errorf("Usage = %+v, want %+v", pr.Usage, c.usage)
			}
			if c.session != "" && pr.SessionID != c.session {
				t.Errorf("SessionID = %q, want %q", pr.SessionID, c.session)
			}
			if (pr.Error != "") != c.err {
				t.Errorf("Error = %q, want error=%v", pr.Error, c.err)
			}
			if pr.LinesSkipped != 0 || pr.LinesDropped != 0 || pr.PlainText {
				t.Errorf("a clean recording must skip nothing: skipped=%d dropped=%d plain=%v", pr.LinesSkipped, pr.LinesDropped, pr.PlainText)
			}
		})
	}
}

func equalKinds(a, b []NativeEventKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The tool card of a recorded agy run carries the real name, input and output.
func TestPinnedAgyToolCard(t *testing.T) {
	_, evs := parse("agy", readPinned(t, "agy", "tool"))
	var use, res *NativeEvent
	for i := range evs {
		switch evs[i].Kind {
		case EventToolUse:
			use = &evs[i]
		case EventToolResult:
			res = &evs[i]
		}
	}
	if use == nil || res == nil {
		t.Fatal("no tool card")
	}
	if use.ToolName != "run_command" || !strings.Contains(use.ToolInput, "echo hello-k144") {
		t.Errorf("tool_use = %+v", use)
	}
	if res.IsError || !strings.Contains(res.ToolOutput, "hello-k144") {
		t.Errorf("tool_result = %+v", res)
	}
}

// Usage math across a three-turn --conversation: every turn's own usage is the
// sum of its DONE steps, the result frame carries the running total, and the
// total minus the previous total is the turn (the subtraction the plan names).
func TestAgyUsageAcrossThreeTurns(t *testing.T) {
	type turn struct{ own, cum Usage }
	want := []turn{
		{Usage{InputTokens: 12860, OutputTokens: 1}, Usage{InputTokens: 12860, OutputTokens: 1}},
		{Usage{InputTokens: 13067, OutputTokens: 1}, Usage{InputTokens: 25927, OutputTokens: 2}},
		{Usage{InputTokens: 13274, OutputTokens: 1}, Usage{InputTokens: 39201, OutputTokens: 3}},
	}
	var prev, sum Usage
	for i, name := range []string{"t1", "t2", "t3"} {
		pr, _ := parse("agy", readPinned(t, "agy", name))
		own, cum := pr.Usage, pr.CumulativeUsage
		own.DurationMs, cum.DurationMs = 0, 0 // the frame clock is the session's
		if own != want[i].own || cum != want[i].cum {
			t.Errorf("turn %d: own %+v cumulative %+v, want %+v / %+v", i+1, own, cum, want[i].own, want[i].cum)
		}
		sub := Usage{InputTokens: cum.InputTokens - prev.InputTokens, OutputTokens: cum.OutputTokens - prev.OutputTokens}
		if sub.InputTokens != own.InputTokens || sub.OutputTokens != own.OutputTokens {
			t.Errorf("turn %d: total minus previous total %+v differs from the turn's usage %+v", i+1, sub, own)
		}
		sum.InputTokens += own.InputTokens
		sum.OutputTokens += own.OutputTokens
		prev = cum
	}
	if sum.InputTokens != prev.InputTokens || sum.OutputTokens != prev.OutputTokens {
		t.Errorf("turns add up to %+v, the conversation total is %+v", sum, prev)
	}
}

// The codex usage table: input includes cached, the package convention is
// fresh + cache_read, so the sum survives a round trip.
func TestCodexUsageMath(t *testing.T) {
	for _, c := range []struct {
		name, usage string
		want        Usage
	}{
		{"cached inside input", `{"input_tokens":30394,"cached_input_tokens":27392,"output_tokens":50}`, Usage{InputTokens: 3002, CacheRead: 27392, OutputTokens: 50}},
		{"no cache", `{"input_tokens":100,"output_tokens":5}`, Usage{InputTokens: 100, OutputTokens: 5}},
		{"cache write", `{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":100,"output_tokens":9}`, Usage{InputTokens: 300, CacheRead: 600, CacheCreation: 100, OutputTokens: 9}},
		{"cached exceeds input never goes negative", `{"input_tokens":10,"cached_input_tokens":50,"output_tokens":1}`, Usage{CacheRead: 50, OutputTokens: 1}},
		{"absent", `{}`, Usage{}},
		{"negative and huge counts are clamped", `{"input_tokens":-5,"cached_input_tokens":9223372036854775807,"output_tokens":-1}`, Usage{CacheRead: 1 << 40}},
	} {
		t.Run(c.name, func(t *testing.T) {
			pr, _ := parse("codex", []byte(`{"type":"turn.completed","usage":`+c.usage+"}\n"))
			if pr.Usage != c.want {
				t.Errorf("Usage = %+v, want %+v", pr.Usage, c.want)
			}
		})
	}
}

// What a live consumer receives, the token events joined, is the buffered
// answer, for every recording of both harnesses (the differential check).
func TestStreamedTokensEqualBufferedText(t *testing.T) {
	var files []string
	for _, pat := range []string{
		filepath.Join("testdata", "*", "*", "*.ndjson"),
		filepath.Join(streamFixtureDir, "codex-*.ndjson"),
		filepath.Join(streamFixtureDir, "agy-*.ndjson"),
	} {
		m, _ := filepath.Glob(pat)
		files = append(files, m...)
	}
	if len(files) < 25 {
		t.Fatalf("corpus too small: %d files", len(files))
	}
	for _, f := range files {
		harness := "codex"
		if strings.Contains(filepath.Base(f), "agy") || strings.Contains(f, string(filepath.Separator)+"agy"+string(filepath.Separator)) {
			harness = "agy"
		}
		data, err := os.ReadFile(f) //nolint:gosec // test path
		if err != nil {
			t.Fatal(err)
		}
		pr, evs := parse(harness, data)
		var sb strings.Builder
		for _, e := range evs {
			if e.Kind == EventToken && !e.Plain {
				sb.WriteString(e.Text)
			}
		}
		got := strings.TrimRight(sb.String(), "\r\n")
		if pr.PlainText {
			continue // prose is delivered whole at exit, not as events
		}
		// agy's envelope form carries its text on the result frame only.
		if got == "" && pr.Text != "" {
			continue
		}
		if got != pr.Text {
			t.Errorf("%s: streamed %q, buffered %q", f, got, pr.Text)
		}
	}
}

// A hostile agy frame cannot subtract from, or overflow, the ledger.
func TestAgyUsageIsClamped(t *testing.T) {
	pr, _ := parse("agy", []byte(`{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"x","num_turns":1,"usage":{"input_tokens":-9,"output_tokens":9223372036854775807}}}`+"\n"))
	if pr.Usage.InputTokens != 0 || pr.Usage.OutputTokens != 1<<40 {
		t.Errorf("Usage = %+v", pr.Usage)
	}
}

func feedLines(p LineParser, lines ...string) []NativeEvent {
	var evs []NativeEvent
	for _, l := range lines {
		evs = append(evs, p.Feed([]byte(l))...)
	}
	return evs
}

// A real pipe loses, garbles and interleaves lines. None of it may crash a
// parser or leak into the answer, and every skipped line is counted.
func TestParserDefects(t *testing.T) {
	codexOK := strings.Split(strings.TrimRight(string(readPinned(t, "codex", "command")), "\n"), "\n")
	agyOK := strings.Split(strings.TrimRight(string(readPinned(t, "agy", "tool")), "\n"), "\n")

	t.Run("truncated last line", func(t *testing.T) {
		for _, c := range []struct {
			harness string
			lines   []string
		}{{"codex", codexOK}, {"agy", agyOK}} {
			last := len(c.lines) - 1
			lines := append(append([]string(nil), c.lines[:last]...), c.lines[last][:len(c.lines[last])/2])
			p := ParserFor(c.harness)
			feedLines(p, lines...)
			pr := p.Finish()
			if pr.LinesSkipped != 1 {
				t.Errorf("%s: skipped = %d, want 1", c.harness, pr.LinesSkipped)
			}
			if c.harness == "codex" && pr.Usage != (Usage{}) {
				t.Errorf("codex: usage %+v from a lost turn.completed", pr.Usage)
			}
			if pr.Text == "" {
				t.Errorf("%s: the text before the cut must survive", c.harness)
			}
		}
	})

	t.Run("malformed line in the middle", func(t *testing.T) {
		for _, c := range []struct {
			harness string
			lines   []string
		}{{"codex", codexOK}, {"agy", agyOK}} {
			want, _ := parse(c.harness, []byte(strings.Join(c.lines, "\n")+"\n"))
			lines := append([]string{c.lines[0], `{"type": not json`, `garbage {{{`, "}"}, c.lines[1:]...)
			p := ParserFor(c.harness)
			feedLines(p, lines...)
			got := p.Finish()
			if got.LinesSkipped != 3 {
				t.Errorf("%s: skipped = %d, want 3", c.harness, got.LinesSkipped)
			}
			got.LinesSkipped = 0
			if got != want {
				t.Errorf("%s: a malformed line changed the result:\n got %+v\nwant %+v", c.harness, got, want)
			}
		}
	})

	t.Run("usage absent", func(t *testing.T) {
		p := ParserFor("codex")
		feedLines(p, codexOK[:len(codexOK)-1]...)
		if pr := p.Finish(); pr.Usage != (Usage{}) || pr.Text == "" {
			t.Errorf("codex without turn.completed: %+v", pr)
		}
		p = ParserFor("agy")
		feedLines(p, agyOK[:len(agyOK)-1]...)
		if pr := p.Finish(); pr.Usage == (Usage{}) {
			t.Error("agy without the result frame still has its DONE steps' usage")
		}
		p = ParserFor("agy")
		feedLines(p, `{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"hi","num_turns":1}}`)
		if pr := p.Finish(); pr.Usage != (Usage{}) || pr.Text != "hi" {
			t.Errorf("agy result without usage: %+v", pr)
		}
	})

	t.Run("oversize line", func(t *testing.T) {
		huge := `{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"` + strings.Repeat("x", MaxStreamLineBytes) + `"}}`
		p := ParserFor("codex")
		evs := feedLines(p, codexOK[0], huge, codexOK[len(codexOK)-2])
		pr := p.Finish()
		if pr.LinesDropped != 1 || !pr.Truncated {
			t.Errorf("dropped=%d truncated=%v, want 1/true", pr.LinesDropped, pr.Truncated)
		}
		for _, e := range evs {
			if len(e.Text) > 1000 || len(e.Raw) > maxRawEventBytes {
				t.Errorf("the oversize event leaked: %d bytes", len(e.Text))
			}
		}
		if MaxStreamLineBytes != 1<<20 {
			t.Errorf("cap = %d, want 1 MiB", MaxStreamLineBytes)
		}
	})

	t.Run("stray prose ahead of a structured stream", func(t *testing.T) {
		p := ParserFor("codex")
		evs := feedLines(p, append([]string{"Reading additional input from stdin..."}, codexOK...)...)
		pr := p.Finish()
		if pr.PlainText || strings.Contains(pr.Text, "Reading") {
			t.Errorf("stray line became the answer: %+v", pr)
		}
		plain := 0
		for _, e := range evs {
			if e.Plain {
				plain++
			}
		}
		if plain != 1 {
			t.Errorf("the stray line is the one Plain token, got %d", plain)
		}
	})

	t.Run("pure prose is plain text", func(t *testing.T) {
		for _, h := range []string{"codex", "agy"} {
			p := ParserFor(h)
			feedLines(p, "hello", "", "world")
			if pr := p.Finish(); !pr.PlainText || pr.Text != "hello\n\nworld" || pr.LinesSkipped != 0 {
				t.Errorf("%s: %+v", h, pr)
			}
		}
	})
}

func fuzzSeeds(f *testing.F, harness string, names ...string) {
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join("testdata", harness, RecordedVersions[harness], n+".ndjson")) //nolint:gosec // test path
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("{\n{\"type\":\n\x00\xff\xfe\n{}\n[]\n"))
}

func fuzzParser(t *testing.T, harness string, data []byte) {
	p := ParserFor(harness)
	var tokens strings.Builder
	rest := data
	for len(rest) > 0 {
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			line, rest = rest, nil
		}
		for _, e := range p.Feed(line) {
			for _, s := range []string{e.Text, e.ToolName, e.ToolInput, e.ToolOutput, e.SessionID, e.Model} {
				if strings.IndexByte(s, 0) >= 0 {
					t.Fatalf("NUL in %s event text %q", e.Kind, s)
				}
			}
			if e.Kind == EventToken && !e.Plain {
				tokens.WriteString(e.Text)
			}
		}
	}
	pr := p.Finish()
	again := p.Finish()
	if pr != again {
		t.Fatal("Finish is not idempotent")
	}
	if len(pr.Text) > MaxParsedTextBytes || len(pr.Error) > maxErrorBytes+3 {
		t.Fatalf("unbounded result: text %d error %d", len(pr.Text), len(pr.Error))
	}
	if strings.IndexByte(pr.Text, 0) >= 0 || strings.IndexByte(pr.Error, 0) >= 0 || strings.IndexByte(pr.SessionID, 0) >= 0 {
		t.Fatal("NUL in the result")
	}
	if !utf8.ValidString(pr.Text) && utf8.ValidString(string(data)) {
		t.Fatal("valid input produced invalid UTF-8 text")
	}
	if pr.Usage.InputTokens < 0 || pr.Usage.OutputTokens < 0 || pr.Usage.CacheRead < 0 {
		t.Fatalf("negative usage %+v", pr.Usage)
	}
	// What was streamed is the answer (modulo the trailing-newline trim).
	if !pr.PlainText && !pr.TextCapped && pr.Text != "" && tokens.Len() > 0 {
		if got := strings.TrimRight(tokens.String(), "\r\n"); got != pr.Text && harness == "codex" {
			t.Fatalf("streamed %q, buffered %q", got, pr.Text)
		}
	}
}

func FuzzCodexLineParser(f *testing.F) {
	fuzzSeeds(f, "codex", "ok", "command", "failed", "SYNTHETIC-tool-error")
	f.Fuzz(func(t *testing.T, data []byte) { fuzzParser(t, "codex", data) })
}

func FuzzAgyLineParser(f *testing.F) {
	fuzzSeeds(f, "agy", "ok", "tool", "t2", "t3")
	f.Fuzz(func(t *testing.T, data []byte) { fuzzParser(t, "agy", data) })
}
