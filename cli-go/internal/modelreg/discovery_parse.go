package modelreg

import (
	"strings"
	"unicode"
)

// Bounds on what one listing may contribute. They are generous for a real
// listing (agy 1.2.17 prints 18 lines of at most 40 characters) and small enough
// that a hostile or broken command cannot make the registry hold or print
// something large.
const (
	// maxListedModels caps the ids kept from one listing.
	maxListedModels = 512
	// maxNameRunes caps a display name.
	maxNameRunes = 80
	// maxReasonRunes caps a reason sentence shown to the operator.
	maxReasonRunes = 160
)

// utf8BOM is the UTF-8 byte order mark (U+FEFF), spelled as bytes so the source
// stays plain ASCII.
const utf8BOM = "\xef\xbb\xbf"

// parseAgyModels reads the standard output of `agy models`: one model per line,
// the id, a tab, then a display name (`gemini-3.8-flash-high<TAB>Gemini 3.8 Flash
// (High)`). The progress line agy prints ("Fetching available models...") goes to
// standard error and is not part of this input.
//
// The input is the output of another program, so every rule here narrows it:
//
//   - A UTF-8 byte order mark at the very start is ignored. A listing written by a
//     tool that adds one would otherwise lose its first id (the mark is not part of
//     the id, and the id rule would drop the line).
//   - A blank line or a line starting with '#' is skipped (not counted).
//   - The line is cut at its FIRST tab. The id is the left part, the name the
//     rest. A line with no tab is not a model line and is dropped and counted:
//     the format is tab-separated and every real line has its tab, so accepting a
//     bare word would let any one-word message ("unauthorized", "loading") that
//     a failing or wrapped agy prints on standard output become a one-model
//     listing and mark every other model unavailable.
//   - The id must pass ValidID, the rule dispatch applies before an id reaches a
//     harness's argv. A line whose id does not is dropped and counted, so a
//     listing that changed format shows up as "N lines unusable" and not as a
//     silently shorter list.
//   - A repeated id keeps its first line (a repeat is not counted: its id was
//     usable). Ids beyond maxListedModels are dropped and counted.
//   - The name is display text only; sanitizeText removes everything a terminal
//     or a browser could act on.
//
// It never returns an id that ValidID rejects, whatever the input.
func parseAgyModels(stdout []byte) (models []DiscoveredModel, dropped int) {
	seen := make(map[string]struct{})
	text := strings.TrimPrefix(string(stdout), utf8BOM)
	for _, raw := range strings.Split(text, "\n") {
		// Only the line break is trimmed before the cut: a trailing tab is the
		// separator of a model with no display name, not whitespace.
		line := strings.TrimRight(raw, "\r")
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		idPart, name, hasTab := strings.Cut(line, "\t")
		id := strings.TrimSpace(idPart)
		if !hasTab || !ValidID(id) {
			dropped++
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if len(models) >= maxListedModels {
			dropped++
			continue
		}
		seen[id] = struct{}{}
		models = append(models, DiscoveredModel{ID: id, Name: sanitizeText(name, maxNameRunes)})
	}
	return models, dropped
}

// sanitizeText turns text from outside the program into one plain line safe to
// print in a terminal, a log or a web page:
//
//   - bytes that are not valid UTF-8 are removed;
//   - ANSI escape sequences are removed whole (CSI "ESC [ ... final", string
//     sequences "ESC ] ... BEL|ST", and the two-byte and intermediate forms), so
//     "\x1b[31mred" becomes "red" and not "[31mred";
//   - every remaining control character (NUL, C0, DEL, C1) and every Unicode
//     format character (bidirectional overrides and isolates, zero-width
//     characters, the BOM, tag characters) is removed, so a name cannot reorder or
//     hide the text around it;
//   - runs of whitespace (spaces, tabs, line breaks, no-break and line/paragraph
//     separators) collapse to one space, and the ends are trimmed;
//   - the result is cut to maxRunes runes, never leaving a trailing space.
//
// maxRunes <= 0 yields "".
func sanitizeText(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	s = stripEscapes(strings.ToValidUTF8(s, ""))
	var b strings.Builder
	runes := 0
	pendingSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			// Only a space between two visible runes survives; leading and
			// trailing ones are never written.
			pendingSpace = runes > 0
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		default:
			need := 1
			if pendingSpace {
				need = 2
			}
			if runes+need > maxRunes {
				return b.String()
			}
			if pendingSpace {
				b.WriteByte(' ')
				runes++
				pendingSpace = false
			}
			b.WriteRune(r)
			runes++
		}
	}
	return b.String()
}

// stripEscapes removes ANSI/ECMA-48 escape sequences. A malformed or truncated
// sequence is dropped as far as it goes and the scan always advances, so no input
// can make it loop.
func stripEscapes(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // the ESC itself
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: parameter bytes, intermediate bytes, one final byte
			i++
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
				i++
			}
			if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
				i++
			}
		case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: a string that ends at BEL or ST (ESC \)
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // ESC, intermediate bytes, one final byte (ESC ( B, ESC c, ESC M)
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
				i++
			}
			if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
				i++
			}
		}
	}
	return b.String()
}
