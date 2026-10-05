package main

// terminal_text_test.go: sanitizeForTerminal leaves provider-controlled text
// that can only be shown (K-135). Each case is a sequence a hostile or corrupted
// harness could send; the invariants test below throws bytes at it.

import (
	"math/rand"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSanitizeForTerminal(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text is untouched", "model is not supported", "model is not supported"},
		{"non-ASCII text is untouched", "héllo ✓ 日本語", "héllo ✓ 日本語"},
		{"newline and tab stay", "a\n\tb", "a\n\tb"},
		{"empty", "", ""},

		// Escape sequences go whole, payload included.
		{"OSC window title ended by BEL", "a\x1b]0;title\x07b", "ab"},
		{"OSC window title ended by ST", "a\x1b]0;title\x1b\\b", "ab"},
		{"OSC hyperlink", "see \x1b]8;;http://evil.example\x1b\\here\x1b]8;;\x1b\\ now", "see here now"},
		{"OSC clipboard write", "a\x1b]52;c;aGVsbG8=\x07b", "ab"},
		{"CSI clear screen", "a\x1b[2Jb", "ab"},
		{"CSI with a private marker", "a\x1b[?25lb", "ab"},
		{"CSI colours", "\x1b[38;2;255;0;0mred\x1b[0m", "red"},
		{"CSI cursor movement", "keep\x1b[5A\x1b[2Koverwritten", "keepoverwritten"},
		{"DCS string", "a\x1bP1$r0m\x1b\\b", "ab"},
		{"APC string", "a\x1b_payload\x1b\\b", "ab"},
		{"PM and SOS strings", "a\x1b^pm\x1b\\\x1bXsos\x1b\\b", "ab"},
		{"full reset", "a\x1bcb", "ab"},
		{"charset designation", "a\x1b(Bb", "ab"},
		{"save and restore cursor", "a\x1b7\x1b8b", "ab"},
		{"unterminated OSC runs to the end", "ok \x1b]0;never ends", "ok "},
		{"CAN cancels a string", "a\x1b]0;title\x18b", "ab"},
		{"lone ESC at the end", "ok\x1b", "ok"},
		{"ESC inside an OSC starts the next sequence", "\x1b]0;a\x1b[2Jb", "b"},
		{"a CSI that breaks off ends where it breaks", "a\x1b[1;\nb", "a\nb"},

		// Other control characters go, newline and tab excepted.
		{"carriage return cannot overwrite a line", "good\rbad\r\nnext", "goodbad\nnext"},
		{"bell, backspace, NUL, DEL, VT, FF", "a\x07\x08\x00\x7f\x0b\x0cb", "ab"},

		// The 8-bit forms, which a UTF-8 terminal may honour.
		{"C1 CSI", "a\u009b2Jb", "ab"},
		{"C1 OSC ended by BEL", "a\u009d0;title\x07b", "ab"},
		{"C1 OSC ended by C1 ST", "a\u009d0;title\u009cb", "ab"},
		{"C1 DCS", "a\u00901;2\x1b\\b", "ab"},
		{"other C1 controls", "a\u0085\u0084b", "ab"},
		{"invalid UTF-8 is dropped", "a\x9bb\xffc", "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeForTerminal(c.in); got != c.want {
				t.Errorf("sanitizeForTerminal(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Whatever comes in, what goes out is valid UTF-8 with no control character but
// newline and tab, and sanitizing it again changes nothing.
func TestSanitizeForTerminal_InvariantsOnHostileInput(t *testing.T) {
	// An alphabet heavy in the bytes that matter: introducers, terminators,
	// parameter and final bytes, a C1 lead byte, multi-byte text.
	pieces := []string{
		"\x1b", "\x1b[", "\x1b]", "\x1bP", "\x1b_", "\x1b\\", "\x07", "\x18", "\x1a", "\r", "\n", "\t",
		"[", "]", "(", "?", ";", "0", "2", "J", "m", "H", "8", "c", "\\", " ", "a", "Z",
		"\u009b", "\u009d", "\u009c", "\u0090", "\u0085", "\xc2", "\x9b", "\xff", "é", "日", "\x00", "\x7f",
	}
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 5000; n++ {
		var in []byte
		for k := rng.Intn(24); k >= 0; k-- {
			in = append(in, pieces[rng.Intn(len(pieces))]...)
		}
		out := sanitizeForTerminal(string(in))
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 out of %q: %q", in, out)
		}
		for _, r := range out {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				t.Fatalf("control character %U survived from %q: %q", r, in, out)
			}
		}
		if again := sanitizeForTerminal(out); again != out {
			t.Fatalf("not idempotent for %q: %q then %q", in, out, again)
		}
	}
}
