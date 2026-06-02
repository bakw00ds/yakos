package dispatch

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiEscape matches ANSI CSI escape sequences (ESC [ ... m and friends).
// Pattern: ESC (0x1b), followed by '[', followed by digits/semicolons, followed by a letter.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// processStderr applies the PR #34 contract to raw stderr bytes:
//
//  1. Strip ANSI escape codes.
//  2. Extract the last 30 lines.
//  3. Truncate to 4096 bytes maximum.
//  4. Determine if truncation occurred.
//
// Returns (processed string, truncated bool). If the input is empty or only
// whitespace after stripping ANSI, returns ("", false).
//
// This logic mirrors the bash implementation in cli/lib/dispatch.sh, section
// "stderr capture for dispatch-log" (lines 397-425).
func processStderr(raw []byte) (tail string, truncated bool) {
	if len(raw) == 0 {
		return "", false
	}

	// Strip ANSI escape codes.
	stripped := ansiEscape.ReplaceAll(raw, nil)

	// Validate/clean UTF-8 so JSON encoding is safe.
	if !utf8.Valid(stripped) {
		stripped = []byte(strings.ToValidUTF8(string(stripped), ""))
	}

	rawLineCount := bytes.Count(stripped, []byte{'\n'})
	rawSize := len(stripped)

	// Extract last 30 lines.
	lines := bytes.Split(stripped, []byte{'\n'})
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
		truncated = true
	}

	// Truncate to 4096 bytes.
	joined := bytes.Join(lines, []byte{'\n'})
	if len(joined) > 4096 {
		joined = joined[len(joined)-4096:]
		truncated = true
	}

	// Also set truncated if original was larger than what we kept.
	if !truncated && (rawSize > 4096 || rawLineCount > 30) {
		truncated = true
	}

	result := strings.TrimSpace(string(joined))
	if result == "" {
		return "", false
	}
	return result, truncated
}
