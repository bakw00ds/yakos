// Package kanban provides a read-only parser for yakOS kanban.md files.
//
// This is a deliberate sliver-port ahead of the full rank-7 kanban port.
// It exposes only the read path (column counts, task lists) required by
// the `status` subcommand (rank 4). Write, mutate, and serve operations
// are NOT in scope here and must NOT be added until the full kanban port
// (rank 7) lands.
//
// Board format (3-column markdown):
//
//	## TODO
//	- task …
//
//	## IN PROGRESS
//	- task …
//
//	## DONE
//	- task …
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

// Board is a parsed, read-only snapshot of a kanban.md file.
// All fields are shallow: tasks are raw markdown bullet lines.
type Board struct {
	TODO       []string
	InProgress []string
	Done       []string
}

// Parse reads a kanban.md board from r.  It is tolerant of whitespace
// variation and does not fail if a column is absent.  Unknown headings
// are skipped.
//
// Only top-level bullet lines (lines starting with "- " or "* ") that
// appear under a known column heading are collected; sub-bullets and
// prose are ignored.
func Parse(r io.Reader) (*Board, error) {
	b := &Board{}
	var current *[]string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Section heading detection: "## <name>" (with optional trailing whitespace).
		if strings.HasPrefix(trimmed, "## ") {
			heading := strings.TrimSpace(trimmed[3:])
			switch heading {
			case ColTODO:
				current = &b.TODO
			case ColInProgress:
				current = &b.InProgress
			case ColDone:
				current = &b.Done
			default:
				current = nil
			}
			continue
		}

		// Bullet line: must start with "- " or "* ".
		if current != nil && (strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ")) {
			// Store the raw text after the bullet marker.
			*current = append(*current, strings.TrimSpace(trimmed[2:]))
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
// This matches the format used by bash status.sh (which it derives from
// wc -l counts on each section).
func (b *Board) Summary() string {
	return strings.Join([]string{
		"TODO: " + itoa(len(b.TODO)),
		"IN PROGRESS: " + itoa(len(b.InProgress)),
		"DONE: " + itoa(len(b.Done)),
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
