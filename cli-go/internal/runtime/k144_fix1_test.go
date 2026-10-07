package runtime

// k144_fix1_test.go: the review follow-ups to the codex and agy parsers: ids
// validated at the source, running totals that cannot wrap, durations that stay
// finite, the shared line cap on claude, and the version parse.

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

var hostileIDs = []string{"--dangerous-flag", "../../etc/passwd", "-x", "a b", "a\nb", strings.Repeat("a", 129)}

func TestCodexDropsInvalidThreadID(t *testing.T) {
	for _, id := range hostileIDs {
		p := ParserFor("codex")
		evs := feedLines(p,
			fmt.Sprintf(`{"type":"thread.started","thread_id":%q}`, id),
			`{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"hi"}}`,
			`{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":1}}`)
		for _, e := range evs {
			if e.SessionID != "" {
				t.Errorf("%q: event carries session id %q", id, e.SessionID)
			}
		}
		pr := p.Finish()
		if pr.SessionID != "" || pr.LinesSkipped != 1 || pr.Text != "hi" {
			t.Errorf("%q: session %q skipped %d text %q", id, pr.SessionID, pr.LinesSkipped, pr.Text)
		}
	}
	p := ParserFor("codex")
	feedLines(p, `{"type":"thread.started","thread_id":"019a-b_c:d.e"}`)
	if pr := p.Finish(); pr.SessionID != "019a-b_c:d.e" || pr.LinesSkipped != 0 {
		t.Errorf("a valid id must pass: %+v", pr)
	}
}

func TestAgyDropsInvalidConversationID(t *testing.T) {
	for _, id := range hostileIDs {
		for _, line := range []string{
			fmt.Sprintf(`{"event":"init","conversation_id":%q,"init":{"model":"m"}}`, id),
			fmt.Sprintf(`{"event":"step_update","step_update":{"conversation_id":%q,"step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"hi"}}`, id),
			fmt.Sprintf(`{"event":"result","result":{"conversation_id":%q,"status":"SUCCESS","response":"hi","num_turns":1}}`, id),
		} {
			p := ParserFor("agy")
			evs := feedLines(p, line)
			for _, e := range evs {
				if e.SessionID != "" {
					t.Errorf("%q: event carries session id %q", id, e.SessionID)
				}
			}
			if pr := p.Finish(); pr.SessionID != "" || pr.LinesSkipped != 1 {
				t.Errorf("%q: session %q skipped %d", id, pr.SessionID, pr.LinesSkipped)
			}
		}
	}
	p := ParserFor("agy")
	feedLines(p, `{"event":"init","conversation_id":"c-1.2","init":{}}`)
	if pr := p.Finish(); pr.SessionID != "c-1.2" || pr.LinesSkipped != 0 {
		t.Errorf("a valid id must pass: %+v", pr)
	}
}

func TestRunningTotalsSaturate(t *testing.T) {
	big := int64(1) << 40
	codexLine := fmt.Sprintf(`{"type":"turn.completed","usage":{"input_tokens":%d,"output_tokens":%d,"cached_input_tokens":0}}`, big, big)
	agyLine := fmt.Sprintf(`{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"agent_response","usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_tokens":%d}}}`, big, big, big)
	// 2^23 lines of 2^40 wrap an int64. Seed each total just under the wrap so
	// the test needs a handful of lines, not 8M, then add many more.
	seed := int64(math.MaxInt64) - 3*big
	for _, name := range []string{"codex", "agy"} {
		p := ParserFor(name)
		line := []byte(codexLine)
		if name == "codex" {
			cp := p.(*codexLineParser)
			cp.usage = Usage{InputTokens: seed, OutputTokens: seed}
		} else {
			line = []byte(agyLine)
			ap := p.(*agyLineParser)
			ap.run.sum = Usage{InputTokens: seed, OutputTokens: seed, CacheRead: seed}
			ap.run.seen = true
		}
		for i := 0; i < 2000; i++ {
			p.Feed(line)
		}
		u := p.Finish().Usage
		for _, v := range []int64{u.InputTokens, u.OutputTokens, u.CacheRead, u.CacheCreation} {
			if v < 0 || v > 1<<60 {
				t.Fatalf("%s: a total left [0, 2^60]: %+v", name, u)
			}
		}
	}
	if addTokens(5, 7) != 12 || addTokens(maxTokenTotal, 1) != maxTokenTotal || addTokens(-9, 3) != 3 {
		t.Error("addTokens arithmetic")
	}
}

func TestAgyDurationClamped(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int64
	}{{"1e308", maxDurationMs}, {"-1", 0}, {"-1e308", 0}, {"1e400", 0}, {"2.5", 2500}, {"0", 0}} {
		p := ParserFor("agy")
		feedLines(p, `{"event":"result","result":{"conversation_id":"c","status":"SUCCESS","response":"x","num_turns":1,"duration_seconds":`+c.raw+`,"usage":{"input_tokens":1}}}`)
		pr := p.Finish()
		if pr.Usage.DurationMs != c.want || pr.CumulativeUsage.DurationMs != c.want {
			t.Errorf("duration_seconds %s: got %d / %d, want %d", c.raw, pr.Usage.DurationMs, pr.CumulativeUsage.DurationMs, c.want)
		}
	}
	if clampDurationMs(math.NaN()) != 0 || clampDurationMs(math.Inf(1)) != maxDurationMs || clampDurationMs(math.Inf(-1)) != 0 {
		t.Error("clampDurationMs must map NaN/Inf to a finite value")
	}
}

// The 1 MiB cap is shared: a claude line of 1.5 MiB is dropped whole and
// counted, and the next line still parses.
func TestClaudeLineOverSharedCapDropped(t *testing.T) {
	p := ParserFor("claude")
	huge := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + strings.Repeat("x", 1536*1024) + `"}]}}`
	p.Feed([]byte(huge))
	p.Feed([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s-1"}`))
	pr := p.Finish()
	if pr.LinesDropped != 1 {
		t.Errorf("dropped = %d, want 1", pr.LinesDropped)
	}
	if strings.Contains(pr.Text, "xxxx") {
		t.Error("an over-cap line must contribute nothing")
	}
	if pr.SessionID != "s-1" {
		t.Errorf("the line after the dropped one must parse: session %q", pr.SessionID)
	}
}

func TestVersionParsePrefixAndPrerelease(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"v1.4.0\n", "1.4.0"}, {"agy v1.3.1", "1.3.1"}, {"codex-cli 0.154.0-beta.1\n", "0.154.0-beta.1"},
		{"1.3.1-rc2 (build 9)", "1.3.1-rc2"}, {"x1.2.3", ""}, {"codex-cli 0.154.0", "0.154.0"},
	} {
		if got := ParseVersion(c.in); got != c.want {
			t.Errorf("ParseVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, skew := VersionSkew("codex", ParseVersion("v0.154.0")); skew {
		t.Error("v-prefixed recorded version is not a skew")
	}
	if rec, skew := VersionSkew("codex", ParseVersion("0.154.0-beta.1")); !skew || rec != "0.154.0" {
		t.Errorf("a prerelease must report skew: %q %v", rec, skew)
	}
	if _, skew := VersionSkew("codex", ParseVersion("v1.4.0")); !skew {
		t.Error("v1.4.0 must report skew")
	}
}
