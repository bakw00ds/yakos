// mutate.go — in-memory mutations for a parsed kanban.md Board.
//
// All mutations work on b.RawLines to maintain byte-identical parity with
// the bash awk-based mutations in kanban.sh. After mutation, call b.Save()
// to persist.
//
// The bash implementations that this package mirrors:
//
//	cmd_add:    inserts a task block immediately after "## TODO"
//	cmd_notes:  replaces the "  - notes:" line within a task block
//	cmd_move:   removes a task block from its current column and inserts
//	            it immediately after the target column heading
//	cmd_done:   alias for Move(id, ColDone)
//	cmd_delete: removes a task block entirely
//
// Task block structure (as written by cmd_add):
//
//   - [ ] K-N — title
//   - category: <c>
//   - assigned: unassigned
//   - blockers: none
//   - notes: <text>
//
// Block detection: a block starts with a line matching "^- [<x>]" that
// contains " <id> " as a whole-token substring (space-delimited, matching
// bash's `index($0, " " id " ") > 0` idiom). A block ends at the next line
// that does NOT start with "  - " (two spaces, dash, space).
//
// Next-ID logic: scan all lines for "K-N" tokens, find the maximum N,
// return N+1. This matches bash's _kanban_next_id.
package kanban

import (
	"fmt"
	"strings"
)

// em dash as used in task header lines: "- [ ] K-1 — title"
const emDash = "\xe2\x80\x94" // U+2014 em dash UTF-8

// Item is the parsed representation of a single kanban task.
type Item struct {
	ID       string
	Title    string
	Category string
	Notes    string
}

// nextID scans RawLines for the highest K-N id and returns K-(N+1).
func (b *Board) nextID() string {
	max := 0
	for _, line := range b.RawLines {
		// Walk all tokens in the line looking for K-<digits>.
		i := 0
		for i < len(line) {
			idx := strings.Index(line[i:], "K-")
			if idx < 0 {
				break
			}
			pos := i + idx + 2 // position right after "K-"
			j := pos
			for j < len(line) && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j > pos {
				n := 0
				for _, ch := range line[pos:j] {
					n = n*10 + int(ch-'0')
				}
				if n > max {
					max = n
				}
			}
			i += idx + 2
		}
	}
	return fmt.Sprintf("K-%d", max+1)
}

// isTaskHeader returns true if line is a task header line (starts with "- [").
func isTaskHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "- [")
}

// isSubField returns true if line is a task sub-field line (starts with "  - ").
func isSubField(line string) bool {
	return strings.HasPrefix(line, "  - ")
}

// taskIDMatch returns true if the header line contains " id " as a whole token.
// This mirrors bash: `index($0, " " id " ") > 0`.
func taskIDMatch(line, id string) bool {
	return strings.Contains(line, " "+id+" ")
}

// findTaskBlock returns the (start, end) indices of the task block for id
// within lines. start is the index of the header line; end is the first
// index AFTER the block (i.e., lines[start:end] is the complete block).
// Returns (-1, -1) if not found.
func findTaskBlock(lines []string, id string) (start, end int) {
	for i, line := range lines {
		if isTaskHeader(line) && taskIDMatch(line, id) {
			j := i + 1
			for j < len(lines) && isSubField(lines[j]) {
				j++
			}
			return i, j
		}
	}
	return -1, -1
}

// columnHeadingIndex returns the index of the "## <col>" line in lines,
// or -1 if not found. The match is exact (after TrimSpace on both sides).
func columnHeadingIndex(lines []string, col string) int {
	target := "## " + col
	for i, line := range lines {
		if strings.TrimSpace(line) == target {
			return i
		}
	}
	return -1
}

// Add appends a new task to the TODO column.
//
// The task is inserted immediately after the "## TODO" heading line,
// matching bash cmd_add's awk behavior:
//
//	/^## TODO/ { print; print block; next }
//
// The notes value is included verbatim (single-line; caller must normalize
// newlines before calling, matching bash's `tr '\n\r' '  '` in cmd_notes).
func (b *Board) Add(title, category, notes string) (id string) {
	if category == "" {
		category = "other"
	}
	id = b.nextID()

	// Build the task block lines.
	block := []string{
		"- [ ] " + id + " " + emDash + " " + title,
		"  - category: " + category,
		"  - assigned: unassigned",
		"  - blockers: none",
		"  - notes: " + notes,
	}

	todoIdx := columnHeadingIndex(b.RawLines, ColTODO)
	if todoIdx < 0 {
		// No TODO heading; append to the end.
		b.RawLines = append(b.RawLines, block...)
		b.rebuildTyped()
		return id
	}

	// Insert block immediately after the TODO heading line.
	after := todoIdx + 1
	newLines := make([]string, 0, len(b.RawLines)+len(block))
	newLines = append(newLines, b.RawLines[:after]...)
	newLines = append(newLines, block...)
	newLines = append(newLines, b.RawLines[after:]...)
	b.RawLines = newLines
	b.rebuildTyped()
	return id
}

// SetNotes replaces the "  - notes:" line within the task block for id.
//
// This mirrors bash cmd_notes's awk:
//
//	in_task && /^  - notes:/ { print "  - notes: " newtext; next }
//
// If id is not found, returns an error.
func (b *Board) SetNotes(id, text string) error {
	// Normalize embedded newlines to spaces (matches bash `tr '\n\r' '  '`).
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, text)

	found := false
	inTask := false
	for i, line := range b.RawLines {
		if isTaskHeader(line) {
			inTask = taskIDMatch(line, id)
			if inTask {
				found = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			inTask = false
			continue
		}
		if inTask && strings.HasPrefix(line, "  - notes:") {
			b.RawLines[i] = "  - notes: " + text
		}
	}
	if !found {
		return fmt.Errorf("kanban: task %q not found", id)
	}
	return nil
}

// Move removes task id from its current column and inserts it immediately
// after the target column's heading. This is a two-pass operation matching
// bash cmd_move exactly.
//
// col must be one of ColTODO, ColInProgress, or ColDone. Use NormalizeColumn
// to accept user input.
func (b *Board) Move(id, col string) error {
	// Pass 1: extract the task block from its current location.
	start, end := findTaskBlock(b.RawLines, id)
	if start < 0 {
		return fmt.Errorf("kanban: task %q not found", id)
	}

	block := make([]string, end-start)
	copy(block, b.RawLines[start:end])

	// Remove the block from lines.
	without := make([]string, 0, len(b.RawLines)-(end-start))
	without = append(without, b.RawLines[:start]...)
	without = append(without, b.RawLines[end:]...)

	// Pass 2: insert block immediately after the target column heading.
	tgtIdx := columnHeadingIndex(without, col)
	if tgtIdx < 0 {
		return fmt.Errorf("kanban: column %q not found", col)
	}

	after := tgtIdx + 1
	newLines := make([]string, 0, len(without)+len(block))
	newLines = append(newLines, without[:after]...)
	newLines = append(newLines, block...)
	newLines = append(newLines, without[after:]...)
	b.RawLines = newLines
	b.rebuildTyped()
	return nil
}

// Done is a shortcut for Move(id, ColDone).
func (b *Board) Done(id string) error {
	return b.Move(id, ColDone)
}

// Delete removes a task block (header + all sub-field lines) from the board.
// This mirrors bash cmd_delete's awk pass.
func (b *Board) Delete(id string) error {
	start, end := findTaskBlock(b.RawLines, id)
	if start < 0 {
		return fmt.Errorf("kanban: task %q not found", id)
	}
	newLines := make([]string, 0, len(b.RawLines)-(end-start))
	newLines = append(newLines, b.RawLines[:start]...)
	newLines = append(newLines, b.RawLines[end:]...)
	b.RawLines = newLines
	b.rebuildTyped()
	return nil
}

// NormalizeColumn normalizes user-supplied column names to the canonical form.
// Returns an error for unknown values.
func NormalizeColumn(s string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TODO":
		return ColTODO, nil
	case "IN PROGRESS", "IN-PROGRESS", "IN_PROGRESS":
		return ColInProgress, nil
	case "DONE":
		return ColDone, nil
	default:
		return "", fmt.Errorf("unknown column %q (TODO | IN PROGRESS | DONE)", s)
	}
}

// rebuildTyped rebuilds the TODOItems/InProgressItems/DoneItems slices from RawLines.
// Called after any mutation that changes the structure.
func (b *Board) rebuildTyped() {
	b.TODOItems = nil
	b.InProgressItems = nil
	b.DoneItems = nil
	var current *[]string
	for _, line := range b.RawLines {
		trimmed := strings.TrimSpace(line)
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
		// Top-level bullet only (not indented sub-fields like "  - category:").
		if current != nil && (strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")) {
			*current = append(*current, strings.TrimSpace(line[2:]))
		}
	}
}

// Seed initializes a bare kanban.md board in memory if the board is empty.
// The generated content matches bash _kanban_seed exactly (without the dynamic
// title line since we don't have project context here — callers should provide
// a header via SeedWithTitle).
func Seed() *Board {
	return SeedWithTitle("session")
}

// SeedWithTitle creates a new empty board with the given title comment.
// The format matches bash _kanban_seed:
//
//	# Kanban — <title>
//
//	## TODO
//
//	## IN PROGRESS
//
//	## DONE
func SeedWithTitle(title string) *Board {
	b := &Board{
		RawLines: []string{
			"# Kanban \xe2\x80\x94 " + title,
			"",
			"## TODO",
			"",
			"## IN PROGRESS",
			"",
			"## DONE",
		},
	}
	b.rebuildTyped()
	return b
}
