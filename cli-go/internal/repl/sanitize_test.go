package repl

import (
	"strings"
	"testing"
)

func TestSanitizeStripsWholeSequences(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", "hello world\n\tok", "hello world\n\tok"},
		{"csi color", "a\x1b[31mred\x1b[0mb", "aredb"},
		{"csi clear screen", "x\x1b[2J\x1b[Hy", "xy"},
		{"osc title bel", "a\x1b]0;PWNED-TITLE\x07b", "ab"},
		{"osc title st", "a\x1b]2;PWNED\x1b\\b", "ab"},
		{"osc 52 clipboard", "a\x1b]52;c;cHduZWQ=\x07b", "ab"},
		{"osc 52 st", "a\x1b]52;c;cHduZWQ=\x1b\\b", "ab"},
		{"osc unterminated eats the rest", "a\x1b]52;c;cHduZWQ=tail", "a"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"apc", "a\x1b_Gf=24;AAAA\x1b\\b", "ab"},
		{"c1 csi", "a\u009b31mb", "ab"},
		{"c1 osc", "a\u009d0;TITLE\u009cb", "ab"},
		{"c1 bare", "a\u0085b", "ab"},
		{"c1 dcs eats the rest", "a\u0090q;x", "a"},
		{"two byte esc", "a\x1bcb\x1b7c", "abc"},
		{"lone trailing esc", "a\x1b", "a"},
		{"bel and nul", "a\x07\x00b", "ab"},
		{"cr and del", "a\rb\x7f", "ab"},
		{"bidi and zero width", "a\u202eb\u2066c\u200bd\u200fe\u061cf\ufeffg", "abcdefg"},
		{"invalid utf8 c1", "a\x9b31mb", "a\ufffd31mb"},
		{"title plus clipboard in one line", "ok\x1b]0;x\x07\x1b]52;c;QQ==\x07done", "okdone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize(tc.in)
			if got != tc.want {
				t.Fatalf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Every command that prints registry or daemon text must be escape-free, even
// from a rogue listener: models, harness and model pinning, and the banner.
func TestHostileRegistryTextNeverReachesTerminal(t *testing.T) {
	d := newFakeDaemon(t)
	d.models = Models{Models: []ModelEntry{
		{ID: "evil\x1b]0;PWNED-TITLE\x07\x1b[2J", Harness: "codex", Usable: true},
		{ID: "gpt-x", Harness: "claude\x1b]52;c;cHduZWQ=\x07", Usable: true},
	}}
	out := run(t, d, nil, "/model\n/harness codex\n/model gpt-x\n/model\n/harness auto\n/model\n/help\n/exit\n")
	for _, bad := range []string{"\x1b", "\x07", "PWNED-TITLE", "cHduZWQ="} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%q", bad, out)
		}
	}
	if !strings.Contains(out, "codex/evil") {
		t.Errorf("sanitized model list missing:\n%s", out)
	}
}
