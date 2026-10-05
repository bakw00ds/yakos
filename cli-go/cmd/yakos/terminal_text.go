package main

// terminal_text.go: provider-controlled text that the CLI prints (K-135).
//
// A harness's failure message and its stderr tail come from outside yakOS, and
// a hostile or corrupted one can carry terminal control sequences: set the
// window title (OSC), clear the screen (CSI), reset the terminal (ESC c), write
// over earlier lines with a carriage return, ring the bell. The CLI prints such
// text only after sanitizeForTerminal.

import (
	"strings"
)

// sanitizeForTerminal returns s with every terminal control removed, so that
// printing it can show text and nothing else.
//
//   - Escape sequences are removed whole, payload included: CSI (ESC [ ... and a
//     final byte), the string sequences OSC, DCS, SOS, PM and APC (up to BEL or
//     ST, which is ESC \ or U+009C; one that is never terminated goes to the end
//     of the text), and the short forms such as ESC c and ESC ( B.
//   - The 8-bit introducers (C1) get the same treatment, because a UTF-8
//     terminal may honour U+009B and its kin.
//   - Every other control character goes too, C0, DEL and C1 alike, except
//     newline and tab. A carriage return cannot overwrite an earlier line, and
//     BEL cannot ring.
//   - Invalid UTF-8 is dropped.
//
// Printable text, non-ASCII included, is left as it is.
func sanitizeForTerminal(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i = escapeEnd(s, i)
		case c == '\n' || c == '\t':
			b.WriteByte(c)
			i++
		case c < 0x20 || c == 0x7f:
			i++ // the other C0 controls, and DEL
		case c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f:
			i = c1End(s, i) // a C1 control, encoded as C2 80 to C2 9F
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// escapeEnd returns the index just past the escape sequence that starts at s[i],
// where s[i] is ESC.
func escapeEnd(s string, i int) int {
	i++
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return csiEnd(s, i+1)
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC
		return stringEnd(s, i+1)
	}
	// Any other sequence: intermediate bytes (0x20 to 0x2F), then one final byte
	// (0x30 to 0x7E).
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// csiEnd returns the index just past a control sequence whose parameters start
// at s[i]: parameter bytes (0x30 to 0x3F), intermediate bytes (0x20 to 0x2F),
// then one final byte (0x40 to 0x7E). A sequence that breaks off ends where it
// breaks.
func csiEnd(s string, i int) int {
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

// stringEnd returns the index just past a string sequence whose payload starts
// at s[i]. It ends at BEL, at U+009C (the 8-bit ST), or at CAN or SUB, which
// cancel it. It ends before an ESC, which either starts the 7-bit ST (ESC \) or
// begins a new sequence; the caller removes that one in turn. Unterminated, it
// runs to the end of s.
func stringEnd(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case 0x07, 0x18, 0x1a:
			return i + 1
		case 0x1b:
			return i
		case 0xc2:
			if i+1 < len(s) && s[i+1] == 0x9c {
				return i + 2
			}
		}
		i++
	}
	return i
}

// c1End returns the index just past the C1 control that starts at s[i] (the two
// bytes C2 80 to C2 9F). The introducers of a control sequence (U+009B) or of a
// string sequence (U+0090, U+0098, U+009D, U+009E, U+009F) take their payload
// with them; any other C1 control stands alone.
func c1End(s string, i int) int {
	switch s[i+1] {
	case 0x9b:
		return csiEnd(s, i+2)
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
		return stringEnd(s, i+2)
	}
	return i + 2
}
