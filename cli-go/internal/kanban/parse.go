// Package kanban provides a parser, writer, and mutator for yakOS kanban.md files.
//
// Board format (3-column markdown):
//
//	## TODO
//	- [ ] K-1 — title
//	  - category: other
//	  - assigned: unassigned
//	  - blockers: none
//	  - notes: optional free-form text
//
//	## IN PROGRESS
//	- task …
//
//	## DONE
//	- task …
//
// The parse layer is deliberately tolerant: it preserves all lines verbatim
// in RawLines so that write.go can round-trip the file byte-identically.
package kanban

import (
	"bufio"
	"io"
	"strings"
)

// Column names as they appear in the board headings.
const (
	ColTODO       = "TODO"
	ColInProgress = "IN PROGRESS"
	ColDone       = "DONE"
)

// Board is a parsed snapshot of a kanban.md file.
//
// For read-only consumers (e.g., status) use Summary().
// For mutation + write consumers use the methods on Board directly.
//
// RawLines holds the original file lines in order; the write path
// uses RawLines as its source of truth for byte-identical preservation.
//
// Field names for the typed column slices use the "Items" suffix to avoid
// collision with the Done() method defined in mutate.go.
type Board struct {
	// RawLines are the verbatim lines of the source file (no trailing newline
	// per line). Write() uses these to reconstruct the file.
	RawLines []string

	// TODOItems, InProgressItems, DoneItems are the task titles parsed from
	// each column. These are used by status/Summary; mutations work through
	// RawLines.
	TODOItems       []string
	InProgressItems []string
	DoneItems       []string
}

// Parse reads a kanban.md board from r.  It is tolerant of whitespace
// variation and does not fail if a column is absent.  Unknown headings
// are skipped.
//
// Only top-level bullet lines (lines starting with "- " or "* ") that
// appear under a known column heading are collected into the typed
// fields; sub-bullets and prose are skipped from the typed view but
// preserved verbatim in RawLines.
func Parse(r io.Reader) (*Board, error) {
	b := &Board{}
	var current *[]string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		b.RawLines = append(b.RawLines, line)

		trimmed := strings.TrimSpace(line)

		// Section heading detection: "## <name>" (with optional trailing whitespace).
		if strings.HasPrefix(trimmed, "## ") {
			heading := strings.TrimSpace(trimmed[3:])
			switch heading {
			case ColTODO:
				current = &b.TODOItems
			case ColInProgress:
				current = &b.InProgressItems
			case ColDone:
				current = &b.DoneItems
			default:
				current = nil
			}
			continue
		}

		// Top-level bullet line: must start with "- " or "* " at column 0
		// (not indented). This distinguishes task headers from sub-field lines
		// like "  - category: bug" which are indented sub-bullets.
		if current != nil && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")) {
			// Store the raw text after the bullet marker.
			*current = append(*current, strings.TrimSpace(line[2:]))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

// Summary returns a single-line summary of the board state, e.g.:
//
//	"TODO: 3  IN PROGRESS: 1  DONE: 5"
//
// This matches the format used by bash status.sh.
func (b *Board) Summary() string {
	return strings.Join([]string{
		"TODO: " + itoa(len(b.TODOItems)),
		"IN PROGRESS: " + itoa(len(b.InProgressItems)),
		"DONE: " + itoa(len(b.DoneItems)),
	}, "  ")
}

// itoa converts n to its decimal string representation without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	if neg {
		buf = append(buf, '-')
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
